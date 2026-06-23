package types

import (
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Model tier — the routing decision, identified by name
// ---------------------------------------------------------------------------

// ModelTier is the name of a routing tier. It is a string so it can be
// compared cheaply, carried in JSON to/from the classifier service, and
// stored in Redis.
type ModelTier string

const (
	TierSmall     ModelTier = "small"
	TierCode      ModelTier = "code"
	TierMedium    ModelTier = "medium"
	TierLarge     ModelTier = "large"
	TierReasoning ModelTier = "reasoning"
)

// canonicalTiers is the set of tier names the router understands.
var canonicalTiers = map[ModelTier]bool{
	TierSmall:     true,
	TierCode:      true,
	TierMedium:    true,
	TierLarge:     true,
	TierReasoning: true,
}

// ValidTier reports whether t is a recognised tier name.
func ValidTier(t ModelTier) bool {
	return canonicalTiers[t]
}

// ---------------------------------------------------------------------------
// Routing rules — runtime representation
// ---------------------------------------------------------------------------

// TierRouting defines when a tier should be selected by the classifier.
type TierRouting struct {
	Rules       []RoutingRule
	TaskTypes   []string
	CodeSignals bool
	Fallback    bool
}

// RoutingRule defines threshold conditions on classifier output.
// All non-nil fields must match (AND). Multiple rules use OR logic.
type RoutingRule struct {
	MinScore      *float64
	MaxScore      *float64
	MinReasoning  *float64
	MaxReasoning  *float64
	MinDomain     *float64
	MaxDomain     *float64
	MinCreativity *float64
	MaxCreativity *float64
	MinConstraint *float64
	MaxConstraint *float64
	TaskTypes     []string
}

// ---------------------------------------------------------------------------
// Classifier result — output from DeBERTa + heuristics
// ---------------------------------------------------------------------------

// ClassifierResult holds all signals the routing engine uses to pick a tier.
type ClassifierResult struct {
	TaskType   string
	Score      float64 // prompt_complexity_score
	Reasoning  float64
	Domain     float64 // domain_knowledge
	Creativity float64 // creativity_scope
	Constraint float64 // constraint_ct
	Text       string  // raw prompt text for code signal detection
}

// ---------------------------------------------------------------------------
// Core types
// ---------------------------------------------------------------------------

// TierConfig is the full configuration of a tier: its name plus the routing
// rules and model properties compiled from config.yaml.
type TierConfig struct {
	Name        ModelTier
	Priority    int
	MaxSize     int64
	IsCode      bool
	IsReasoning bool
	Routing     TierRouting
}

// Provider identifies what kind of upstream a replica talks to. The empty
// string is treated as ProviderVLLM for backward compatibility with configs
// written before hybrid API routing existed.
const (
	ProviderVLLM      = "vllm"
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
)

// Replica is a single model server instance. It may be a local vLLM replica
// (the default) or an external LLM API endpoint (e.g. Anthropic Claude),
// distinguished by Provider.
type Replica struct {
	ID        string
	URL       string     // vLLM base URL, or API base URL for API providers
	Model     string     // model name to request from the upstream
	Tier      ModelTier  // tier name — cheap to compare and store
	TierCfg   TierConfig // full routing config for this replica's tier
	ParamSize int64

	// Provider is "vllm" (or "") for local replicas, or an API provider such
	// as "anthropic". API replicas are not health-polled (they expose no
	// vLLM-style metrics) and carry their own credential.
	Provider string
	APIKey   string // resolved credential for API providers; empty for vLLM
}

// IsAPI reports whether this replica routes to an external LLM API rather than
// a local vLLM server.
func (r Replica) IsAPI() bool {
	return r.Provider != "" && r.Provider != ProviderVLLM
}

type ReplicaList struct {
	Replicas []Replica
}

type ReplicaHealth struct {
	ReplicaID  string
	Healthy    bool
	KVUsage    float64
	QueueDepth int
}

// ConvState is the routing state pinned to a conversation. It is persisted
// in Redis so that every turn of a multi-turn conversation routes to the
// same (or a higher) tier, keeping answers consistent.
type ConvState struct {
	Tier      ModelTier // current tier the conversation is pinned to
	Model     string    // model name last served to this conversation
	Bucket    string    // complexity-score band (see ScoreBucket)
	Turns     int       // number of turns observed so far
	UpdatedAt time.Time // last time this conversation was routed
}

// ScoreBucket maps a prompt_complexity_score to a coarse band. Buckets give
// escalation hysteresis: small score jitter within a band does not re-tier.
func ScoreBucket(score float64) string {
	switch {
	case score < 0.15:
		return "b0"
	case score < 0.35:
		return "b1"
	case score < 0.55:
		return "b2"
	case score < 0.75:
		return "b3"
	default:
		return "b4"
	}
}

// ---------------------------------------------------------------------------
// Tier selection — the routing engine
// ---------------------------------------------------------------------------

// TierForClassification evaluates all tiers in priority order and returns
// the name of the first match. All routing logic lives in the YAML config.
//
// Evaluation per tier:
//  1. Check task_type match against tier's TaskTypes
//  2. Check code_signals (```, def, class, function in text)
//  3. Evaluate each routing rule (OR logic)
//  4. If nothing matched and tier is fallback, select it
func (r ReplicaList) TierForClassification(result ClassifierResult) ModelTier {
	// Tiers are already sorted by priority from ToReplicaList.
	seen := make(map[ModelTier]bool)
	for _, rep := range r.Replicas {
		if seen[rep.TierCfg.Name] {
			continue
		}
		seen[rep.TierCfg.Name] = true

		routing := rep.TierCfg.Routing

		// Skip fallback on first pass.
		if routing.Fallback {
			continue
		}

		if tierMatches(routing, result) {
			return rep.TierCfg.Name
		}
	}

	// No explicit match — use fallback tier.
	for _, rep := range r.Replicas {
		if rep.TierCfg.Routing.Fallback {
			return rep.TierCfg.Name
		}
	}

	// Should never happen if config validation passed.
	return r.LargestTier()
}

func tierMatches(routing TierRouting, result ClassifierResult) bool {
	// Check task type match.
	if matchesTaskType(routing.TaskTypes, result.TaskType) {
		return true
	}

	// Check code signals in the raw prompt text.
	if routing.CodeSignals && hasCodeInText(result.Text) {
		// Only match if complexity is low enough that it's a
		// straightforward code task, not a complex architecture question.
		if result.Score < 0.60 {
			return true
		}
	}

	// Evaluate routing rules (OR logic — any match selects this tier).
	for _, rule := range routing.Rules {
		if ruleMatches(rule, result) {
			return true
		}
	}

	return false
}

func ruleMatches(rule RoutingRule, r ClassifierResult) bool {
	// If rule has task_types, check them first.
	if len(rule.TaskTypes) > 0 && !matchesTaskType(rule.TaskTypes, r.TaskType) {
		return false
	}

	if rule.MinScore != nil && r.Score < *rule.MinScore {
		return false
	}
	if rule.MaxScore != nil && r.Score >= *rule.MaxScore {
		return false
	}
	if rule.MinReasoning != nil && r.Reasoning < *rule.MinReasoning {
		return false
	}
	if rule.MaxReasoning != nil && r.Reasoning >= *rule.MaxReasoning {
		return false
	}
	if rule.MinDomain != nil && r.Domain < *rule.MinDomain {
		return false
	}
	if rule.MaxDomain != nil && r.Domain >= *rule.MaxDomain {
		return false
	}
	if rule.MinCreativity != nil && r.Creativity < *rule.MinCreativity {
		return false
	}
	if rule.MaxCreativity != nil && r.Creativity >= *rule.MaxCreativity {
		return false
	}
	if rule.MinConstraint != nil && r.Constraint < *rule.MinConstraint {
		return false
	}
	if rule.MaxConstraint != nil && r.Constraint >= *rule.MaxConstraint {
		return false
	}
	return true
}

func matchesTaskType(allowed []string, actual string) bool {
	for _, t := range allowed {
		if strings.EqualFold(t, actual) {
			return true
		}
	}
	return false
}

// hasCodeInText detects code patterns in raw prompt text.
func hasCodeInText(text string) bool {
	if text == "" {
		return false
	}
	return strings.Contains(text, "```") ||
		strings.Contains(text, "def ") ||
		strings.Contains(text, "class ") ||
		strings.Contains(text, "function ") ||
		strings.Contains(text, "func ") ||
		strings.Contains(text, "import ") ||
		strings.Contains(text, "package ")
}

// ---------------------------------------------------------------------------
// Helper methods
// ---------------------------------------------------------------------------

func (r ReplicaList) SmallestTier() ModelTier {
	smallest := r.Replicas[0]
	for _, rep := range r.Replicas[1:] {
		if rep.ParamSize > 0 && rep.ParamSize < smallest.ParamSize {
			smallest = rep
		}
	}
	return smallest.Tier
}

func (r ReplicaList) LargestTier() ModelTier {
	largest := r.Replicas[0]
	for _, rep := range r.Replicas[1:] {
		if rep.ParamSize > largest.ParamSize {
			largest = rep
		}
	}
	return largest.Tier
}

func (r ReplicaList) CodeTier() *TierConfig {
	for _, rep := range r.Replicas {
		if rep.TierCfg.IsCode {
			t := rep.TierCfg
			return &t
		}
	}
	return nil
}

func (r ReplicaList) ReasoningTier() *TierConfig {
	for _, rep := range r.Replicas {
		if rep.TierCfg.IsReasoning {
			t := rep.TierCfg
			return &t
		}
	}
	return nil
}

func (r ReplicaList) TierByName(name ModelTier) *TierConfig {
	for _, rep := range r.Replicas {
		if rep.TierCfg.Name == name {
			t := rep.TierCfg
			return &t
		}
	}
	return nil
}

func (r ReplicaList) TierByPriority(priority int) *TierConfig {
	for _, rep := range r.Replicas {
		if rep.TierCfg.Priority == priority {
			t := rep.TierCfg
			return &t
		}
	}
	return nil
}

func (r ReplicaList) ReplicasForTier(name ModelTier) []Replica {
	var result []Replica
	for _, rep := range r.Replicas {
		if rep.Tier == name {
			result = append(result, rep)
		}
	}
	return result
}

// PriorityOf returns the configured priority of a tier name, or -1 if the
// tier is unknown to this replica list.
func (r ReplicaList) PriorityOf(name ModelTier) int {
	for _, rep := range r.Replicas {
		if rep.TierCfg.Name == name {
			return rep.TierCfg.Priority
		}
	}
	return -1
}

// IsEscalation reports whether moving from current to candidate is an
// upgrade (a higher-priority tier). Used for tier lock-up: a conversation's
// tier only ever goes up.
func (r ReplicaList) IsEscalation(current, candidate ModelTier) bool {
	return r.PriorityOf(candidate) > r.PriorityOf(current)
}

// HigherTier returns whichever of a or b has the higher priority. An unknown
// or empty tier loses to a known one; if both are unknown, b is returned.
func (r ReplicaList) HigherTier(a, b ModelTier) ModelTier {
	if r.PriorityOf(a) >= r.PriorityOf(b) {
		return a
	}
	return b
}
