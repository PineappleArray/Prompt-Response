//ADD BATCHING TO THIS

// Package classifier turns an incoming prompt into a routing decision: which
// model tier should serve it. It historically delegated to a Python FastAPI
// service running an NVIDIA DeBERTa model (see internal/classifier/app). That
// network hop dominated tail latency, so the classifier now runs in-process in
// Go by default (Local): a cheap, allocation-light signal extractor feeds the
// same selection heuristic the Python side used (a direct port of
// app/model_select.py). The HTTP Router is kept as an optional backend for
// deployments that still want to call an external classifier service, and an
// optional real-DeBERTa backend can be built in behind the `onnx` build tag.
package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"prompt-response/internal/config"
	"prompt-response/internal/types"
)

// ---------------------------------------------------------------------------
// Public contract — shared by every backend
// ---------------------------------------------------------------------------

// Classifier is the routing-classification contract. Both the in-process
// Local classifier and the HTTP Router satisfy it, so the proxy depends only
// on this interface.
type Classifier interface {
	Classify(ctx context.Context, req Request) (*ClassifyResponse, error)
}

// Request is the classifier input. It is JSON-encodable so the same shape can
// be sent to an external classifier service.
type Request struct {
	SystemPrompt string          `json:"system_prompt"`
	UserMessage  string          `json:"user_message"`
	TokenCount   int             `json:"token_count"`
	HasCode      bool            `json:"has_code"`
	ConvTurns    int             `json:"conv_turns"`
	CurrentTier  types.ModelTier `json:"current_tier"`
}

// ClassifyResponse is the shape returned to the proxy. BuildReason is a short
// human-readable explanation (the DeBERTa task_type, or a heuristic note).
type ClassifyResponse struct {
	Tier        types.ModelTier    `json:"tier"`
	Score       float64            `json:"score"`
	Signals     map[string]float64 `json:"signals"`
	BuildReason string             `json:"build_reason"`
}

// Response is the in-package representation used by older call sites. It mirrors
// ClassifyResponse with a friendlier field name for the explanation.
type Response struct {
	Tier    types.ModelTier
	Score   float64
	Signals map[string]float64
	Reason  string
}

type ClassifierConfig struct {
	Keywords         config.KeywordSets
	ClassifierWeight config.ClassifierWeights
	Settings         config.ClassifierSettings
}

// ---------------------------------------------------------------------------
// Model-tier selection — a faithful port of app/model_select.py
//
// The Python side chose between four concrete model ids; the router speaks in
// tier names, so we collapse straight to tiers. The thresholds and ordering are
// kept identical so a request routes exactly as it did under the Python path.
// ---------------------------------------------------------------------------

// tierPriority ranks tiers for up-tier-only escalation. These values match the
// priority field in config.yaml and TIER_PRIORITY in app/model_select.py so the
// Go and (legacy) Python sides agree on what "a higher tier" means. Note that
// reasoning ranks highest: it is the capable default for anything non-trivial.
var tierPriority = map[types.ModelTier]int{
	types.TierSmall:     1,
	types.TierCode:      2,
	types.TierMedium:    3,
	types.TierLarge:     4,
	types.TierReasoning: 5,
}

// codeSignals are task-type substrings that force the code tier, mirroring
// CODE_SIGNALS in app/model_select.py.
//var codeSignals = []string{
//	"html", "css", "javascript", "python", "code",
//	"function", "script", "api", "sql", "regex",
//	"website", "app", "debug", "error", "compile",
//	"algorithm", "class", "import", "return",
//}

// signals is the intermediate signal set produced by a backend (heuristic or
// neural) and consumed by basePick. Field names mirror the DeBERTa output.
type signals struct {
	taskType   string  // e.g. "Code Generation", "QA", "Open QA"
	score      float64 // prompt_complexity_score, 0–1
	reasoning  float64
	domain     float64 // domain_knowledge
	creativity float64 // creativity_scope
	constraint float64 // constraint_ct
}

func (s signals) toMap() map[string]float64 {
	return map[string]float64{
		"prompt_complexity_score": s.score,
		"reasoning":               s.reasoning,
		"domain_knowledge":        s.domain,
		"creativity_scope":        s.creativity,
		"constraint_ct":           s.constraint,
	}
}

func InitConfig(cfg config.Config) ClassifierConfig {
	return ClassifierConfig{
		Keywords:         cfg.Keywords,
		ClassifierWeight: cfg.Classifier,
		Settings:         cfg.ClassifierSettings,
	}
}

// basePick is the static, stateless tier choice from the signals — a direct
// port of _base_pick(). text is the raw prompt, used to catch fenced code.
func (cfg ClassifierConfig) basePick(s signals, text string) types.ModelTier {
	task := strings.ToLower(s.taskType)

	if task == "code generation" {
		return types.TierCode
	}
	for _, w := range cfg.Keywords.Code {
		if strings.Contains(task, w) {
			return types.TierCode
		}
	}
	if text != "" && hasCodeMarker(text) && s.score < 0.60 {
		return types.TierCode
	}

	// Summarization and extraction need moderate capability but not deep reasoning —
	// matches Python _base_pick which was missing from the Go port.
	if (task == "summarization" || task == "extraction") && s.score < 0.55 {
		return types.TierMedium
	}

	isQA := strings.Contains(task, "qa") || task == "classification"
	if isQA && s.score < 0.15 && s.reasoning < 0.15 {
		return types.TierSmall
	}

	if s.reasoning >= 0.70 && s.score >= 0.55 {
		return types.TierLarge
	}
	if s.score >= 0.65 && s.domain >= 0.80 && s.constraint >= 0.60 {
		return types.TierLarge
	}

	return types.TierReasoning
}

// clampUp raises picked to current when the conversation is already pinned to a
// more capable tier — a conversation's tier only ever goes up. Port of
// _clamp_up().
func clampUp(picked, current types.ModelTier) types.ModelTier {
	if tierPriority[current] > tierPriority[picked] {
		return current
	}
	return picked
}

// selectTier chooses a tier for a request. current is the tier the conversation
// was routed to on a previous turn (empty for the first turn). Port of pick().
func (cfg ClassifierConfig) selectTier(s signals, text string, current types.ModelTier) types.ModelTier {
	chosen := cfg.basePick(s, text)
	if current != "" {
		chosen = clampUp(chosen, current)
	}
	return chosen
}

func hasCodeMarker(text string) bool {
	return strings.Contains(text, "```") ||
		strings.Contains(text, "def ") ||
		strings.Contains(text, "class ") ||
		strings.Contains(text, "function ")
}

// ---------------------------------------------------------------------------
// Local — the default in-process classifier
//
// signalExtractor is the pluggable front half: it maps a Request to signals.
// The default is heuristicSignals (pure Go, no native deps). The onnx build tag
// swaps in a real DeBERTa extractor without changing the selection logic.
// ---------------------------------------------------------------------------

type signalExtractor func(req Request) signals

// Local runs classification in-process. It is safe for concurrent use: the
// extractor is a pure function and Local holds no mutable state.
type Local struct {
	config  ClassifierConfig
	extract signalExtractor
	backend string // for logging/build_reason provenance
}

// NewLocalClassifier returns the default in-process classifier backed by the
// heuristic signal extractor. This is the constructor wired into the router.
func NewLocalClassifier(cfg ClassifierConfig) *Local {
	return &Local{extract: cfg.heuristicSignals, backend: "heuristic", config: cfg}
}

// Classify implements Classifier. It never returns an error — classification is
// best-effort and always yields a tier — but keeps the error return to satisfy
// the interface and leave room for backends that can fail.
func (l *Local) Classify(_ context.Context, req Request) (*ClassifyResponse, error) {
	s := l.extract(req)
	text := req.SystemPrompt + "\n" + req.UserMessage
	tier := l.config.selectTier(s, text, req.CurrentTier)
	reason := s.taskType
	if reason == "" {
		reason = l.backend
	}
	return &ClassifyResponse{
		Tier:        tier,
		Score:       s.score,
		Signals:     s.toMap(),
		BuildReason: reason,
	}, nil
}

// ---------------------------------------------------------------------------
// Heuristic signal extraction (the default front half)
//
// A cheap stand-in for the DeBERTa heads: it derives the same signal set from
// surface features of the prompt. Designed to be allocation-light per the
// project's zero-allocation hot-path convention — it scans the lowercased
// message once per keyword set and allocates nothing on the steady path beyond
// the returned signals value.
// ---------------------------------------------------------------------------

//var (
//	reasoningKeywords  = []string{"explain", "why", "compare", "difference", "tradeoff", "design", "architecture", "analyze", "prove", "reason", "step by step", "derive"}
//	domainKeywords     = []string{"medical", "legal", "financial", "quantum", "kernel", "cryptograph", "molecular", "theorem", "protocol", "compiler", "distributed", "kubernetes"}
//	creativityKeywords = []string{"story", "poem", "imagine", "creative", "brainstorm", "write a", "compose", "fictional", "song", "screenplay"}
//	constraintKeywords = []string{"must", "exactly", "only", "at least", "at most", "json", "format", "bullet", "table", "in words", "no more than", "step by step"}
//	codeKeywords       = []string{"function", "algorithm", "class", "struct", "interface", "refactor", "debug", "compile", "regex", "sql",("api", "implement"}
//)

func (cfg ClassifierConfig) heuristicSignals(req Request) signals {
	lower := strings.ToLower(req.UserMessage)
	s := cfg.Settings
	w := cfg.ClassifierWeight

	reasoning := keywordScore(lower, cfg.Keywords.Reasoning, s.ReasoningKeywordThreshold)
	domain := keywordScore(lower, cfg.Keywords.Domain, s.ReasoningKeywordThreshold)
	creativity := keywordScore(lower, cfg.Keywords.Creativity, 1)
	constraint := keywordScore(lower, cfg.Keywords.Constraint, s.ComplexityKeywordThreshold)

	// Strong reasoning keywords clamp the reasoning signal to 0.70, which
	// combined with score >= 0.55 triggers large-tier routing in basePick.
	if len(cfg.Keywords.StrongReasoning) > 0 && hasAnyKeyword(lower, cfg.Keywords.StrongReasoning) {
		reasoning = math.Max(reasoning, 0.70)
	}

	// Composite score mirrors the Python weighting (contextual_knowledge and
	// number_of_few_shots heads are unavailable heuristically and folded in via
	// a small length term instead).
	maxTokens := s.MaxTokensForNormalization
	if maxTokens <= 0 {
		maxTokens = 120
	}
	lengthTerm := math.Min(1.0, float64(req.TokenCount)/float64(maxTokens))
	score := w.Creativity*creativity + w.Reasoning*reasoning + w.Constraint*constraint + w.Domain*domain + w.Length*lengthTerm
	if score > 1 {
		score = 1
	}

	return signals{
		taskType:   cfg.heuristicTaskType(req, lower),
		score:      score,
		reasoning:  reasoning,
		domain:     domain,
		creativity: creativity,
		constraint: constraint,
	}
}

// heuristicTaskType maps surface features of a request to one of the task type
// labels produced by the NVIDIA DeBERTa classifier, keeping the Go and Python
// routing paths compatible without a network hop.
//
// Task types (matches nvidia/prompt-task-and-complexity-classifier output):
//   "Code Generation", "Summarization", "Extraction", "Classification",
//   "Text Generation", "Dialogue", "QA", "Open QA"
func (cfg ClassifierConfig) heuristicTaskType(req Request, lower string) string {
	if req.HasCode || hasCodeMarker(req.UserMessage) || hasAnyKeyword(lower, cfg.Keywords.Code) {
		return "Code Generation"
	}
	if hasAnyKeyword(lower, []string{"summarize", "summary", "tldr", "condense", "shorten", "brief overview"}) {
		return "Summarization"
	}
	if hasAnyKeyword(lower, []string{"extract", "pull out", "find all", "list all", "identify all"}) {
		return "Extraction"
	}
	if hasAnyKeyword(lower, []string{"classify", "categorize", "label", "which category", "what type of", "is this a"}) {
		return "Classification"
	}
	if hasAnyKeyword(lower, []string{"write a ", "write me a", "generate a", "compose a", "create a", "draft a"}) {
		return "Text Generation"
	}
	if req.ConvTurns > 0 && hasAnyKeyword(lower, []string{"continue", "follow up", "also", "and what about", "what else"}) {
		return "Dialogue"
	}
	trimmed := strings.TrimSpace(lower)
	if strings.HasSuffix(trimmed, "?") ||
		strings.HasPrefix(trimmed, "what") || strings.HasPrefix(trimmed, "who") ||
		strings.HasPrefix(trimmed, "when") || strings.HasPrefix(trimmed, "where") ||
		strings.HasPrefix(trimmed, "which") || strings.HasPrefix(trimmed, "how many") {
		return "QA"
	}
	return "Open QA"
}

// keywordScore returns hits/threshold clamped to [0,1].
func keywordScore(lowerText string, keywords []string, threshold int) float64 {
	if threshold <= 0 {
		threshold = 1
	}
	hits := 0
	for _, kw := range keywords {
		if strings.Contains(lowerText, kw) {
			hits++
		}
	}
	return math.Min(1.0, float64(hits)/float64(threshold))
}

func hasAnyKeyword(lowerText string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(lowerText, kw) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Router — optional HTTP backend (external classifier service)
//
// Retained for deployments that still front the Python DeBERTa service, and to
// keep the classifier pluggable. It also satisfies Classifier.
// ---------------------------------------------------------------------------

type Router struct {
	mlEndpoint string
	httpClient *http.Client
}

// NewRouter creates a Router pointed at an explicit /classify endpoint.
func NewRouter(endpoint string) *Router {
	return &Router{
		mlEndpoint: endpoint,
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
}

// InitializeClassifier returns a Router using the default local endpoint.
func InitializeClassifier() *Router {
	return NewRouter("http://localhost:8080/classify")
}

func (c *Router) Classify(ctx context.Context, req Request) (*ClassifyResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.mlEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling classifier: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("classifier returned %d", resp.StatusCode)
	}

	var result ClassifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

// Compile-time checks that both backends satisfy the interface.
var (
	_ Classifier = (*Local)(nil)
	_ Classifier = (*Router)(nil)
)
