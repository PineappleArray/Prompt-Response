package types

import "strings"

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

type ModelTier struct {
	Name        string
	Priority    int
	MaxSize     int64
	IsCode      bool
	IsReasoning bool
	Routing     TierRouting
}

type Replica struct {
	ID        string
	URL       string
	Model     string
	Tier      ModelTier
	ParamSize int64
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

// ---------------------------------------------------------------------------
// Tier selection — the routing engine
// ---------------------------------------------------------------------------

// TierForClassification evaluates all tiers in priority order and returns
// the first match. This replaces the hardcoded pick() function — all
// routing logic lives in the YAML config.
//
// Evaluation per tier:
//  1. Check task_type match against tier's TaskTypes
//  2. Check code_signals (```, def, class, function in text)
//  3. Evaluate each routing rule (OR logic)
//  4. If nothing matched and tier is fallback, select it
func (r ReplicaList) TierForClassification(result ClassifierResult) ModelTier {
	// Tiers are already sorted by priority from ToReplicaList
	seen := make(map[string]bool)
	for _, rep := range r.Replicas {
		if seen[rep.Tier.Name] {
			continue
		}
		seen[rep.Tier.Name] = true

		routing := rep.Tier.Routing

		// Skip fallback on first pass
		if routing.Fallback {
			continue
		}

		if tierMatches(routing, result) {
			return rep.Tier
		}
	}

	// No explicit match — use fallback tier
	for _, rep := range r.Replicas {
		if rep.Tier.Routing.Fallback {
			return rep.Tier
		}
	}

	// Should never happen if config validation passed
	return r.LargestTier()
}

func tierMatches(routing TierRouting, result ClassifierResult) bool {
	// Check task type match
	if matchesTaskType(routing.TaskTypes, result.TaskType) {
		return true
	}

	// Check code signals in the raw prompt text
	if routing.CodeSignals && hasCodeInText(result.Text) {
		// Only match if complexity is low enough that it's a
		// straightforward code task, not a complex architecture question
		if result.Score < 0.60 {
			return true
		}
	}

	// Evaluate routing rules (OR logic — any match selects this tier)
	for _, rule := range routing.Rules {
		if ruleMatches(rule, result) {
			return true
		}
	}

	return false
}

func ruleMatches(rule RoutingRule, r ClassifierResult) bool {
	// If rule has task_types, check them first
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

func (r ReplicaList) ValidTier(name string) bool {
	for _, rep := range r.Replicas {
		if rep.Tier.Name == name {
			return true
		}
	}
	return false
}

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

func (r ReplicaList) CodeTier() *ModelTier {
	for _, rep := range r.Replicas {
		if rep.Tier.IsCode {
			t := rep.Tier
			return &t
		}
	}
	return nil
}

func (r ReplicaList) ReasoningTier() *ModelTier {
	for _, rep := range r.Replicas {
		if rep.Tier.IsReasoning {
			t := rep.Tier
			return &t
		}
	}
	return nil
}

func (r ReplicaList) TierByName(name string) *ModelTier {
	for _, rep := range r.Replicas {
		if rep.Tier.Name == name {
			t := rep.Tier
			return &t
		}
	}
	return nil
}

func (r ReplicaList) TierByPriority(priority int) *ModelTier {
	for _, rep := range r.Replicas {
		if rep.Tier.Priority == priority {
			t := rep.Tier
			return &t
		}
	}
	return nil
}

func (r ReplicaList) ReplicasForTier(name string) []Replica {
	var result []Replica
	for _, rep := range r.Replicas {
		if rep.Tier.Name == name {
			result = append(result, rep)
		}
	}
	return result
}

// IsEscalation returns true if moving from current to candidate is an upgrade.
// Used for tier lock-up: tiers only go up, never down.
func IsEscalation(current, candidate ModelTier) bool {
	return candidate.Priority > current.Priority
}
