package classifier

import (
	"fmt"
	"math"
	"strings"

	"prompt-response/internal/types"
)

const (
	maxTokensForNormalization  = 120 // tokens beyond this score 1.0 on length signal
	codeKeywordThreshold       = 3   // code keyword hits for max score
	reasoningKeywordThreshold  = 2   // reasoning keyword hits for max score
	complexityKeywordThreshold = 2   // complexity keyword hits for max score
	maxConversationDepth       = 5   // turns beyond this score 1.0 on depth signal
	maxTokensForOutputEstimate = 200 // input tokens beyond this assume long output
)

// Signal lists hoisted to package scope so each Classify call reuses the same
// backing array instead of allocating a fresh slice header.
var (
	codeStrongSignals = []string{"implement", "build", "write", "create"}

	longOutputSignals = []string{
		"list all", "write a", "generate", "explain step by step",
		"create a", "implement", "describe in detail",
	}
	shortOutputSignals = []string{
		"what is", "yes or no", "true or false",
		"which one", "name the", "how many",
	}
)

type HeuristicClassifier struct {
	weights   SignalWeights
	threshold float64
	keywords  KeywordSets
}

type HeuristicConfig struct {
	Weights   SignalWeights
	Threshold float64
}

type SignalWeights struct {
	Length       float64
	Code         float64
	Reasoning    float64
	Complexity   float64
	ConvDepth    float64
	OutputLength float64
}

type KeywordSets struct {
	Code       []string
	Reasoning  []string
	Complexity []string
}

func NewHeuristic(cfg HeuristicConfig) *HeuristicClassifier {
	return &HeuristicClassifier{
		weights:   cfg.Weights,
		threshold: cfg.Threshold,
		keywords:  defaultKeywords(),
	}
}

func (h *HeuristicClassifier) Classify(req Request) (Response, error) {
	signals := map[string]float64{
		"length":        h.scoreLength(req.TokenCount),
		"code":          h.scoreCode(req),
		"reasoning":     h.scoreReasoning(req),
		"complexity":    h.scoreComplexity(req),
		"conv_depth":    h.scoreConversationDepth(req.ConvTurns),
		"output_length": h.scoreExpectedOutputLength(req),
	}

	score := h.weights.Length*signals["length"] +
		h.weights.Code*signals["code"] +
		h.weights.Reasoning*signals["reasoning"] +
		h.weights.Complexity*signals["complexity"] +
		h.weights.ConvDepth*signals["conv_depth"] +
		h.weights.OutputLength*signals["output_length"]

	tier := types.TierSmall
	if score >= h.threshold {
		tier = types.TierLarge
	}

	return Response{
		Tier:    tier,
		Score:   score,
		Signals: signals,
		Reason:  h.buildReason(signals, req),
	}, nil
}

func (h *HeuristicClassifier) scoreLength(tokens int) float64 {
	return math.Min(1.0, float64(tokens)/float64(maxTokensForNormalization))
}

// scoreCode: 1.0 if code block present, else keyword ratio
func (h *HeuristicClassifier) scoreCode(req Request) float64 {
	if req.HasCode {
		return 1.0
	}

	lower := strings.ToLower(req.UserMessage)
	for _, s := range codeStrongSignals {
		if strings.Contains(lower, s) {
			return 1.0
		}
	}
	hits := h.countKeywords(req.UserMessage, h.keywords.Code)
	return math.Min(1.0, float64(hits)/float64(codeKeywordThreshold))
}

// scoreReasoning: keyword density in user message
func (h *HeuristicClassifier) scoreReasoning(req Request) float64 {
	hits := h.countKeywords(req.UserMessage, h.keywords.Reasoning)
	return math.Min(1.0, float64(hits)/float64(reasoningKeywordThreshold))
}

// scoreComplexity: multi-step or edge-case signals
func (h *HeuristicClassifier) scoreComplexity(req Request) float64 {
	hits := h.countKeywords(req.UserMessage, h.keywords.Complexity)
	return math.Min(1.0, float64(hits)/float64(complexityKeywordThreshold))
}

// scoreConversationDepth: multi-turn conversations need more KV cache and
// context management. 1-2 turns = simple, 5+ turns = complex.
func (h *HeuristicClassifier) scoreConversationDepth(turns int) float64 {
	return math.Min(1.0, float64(turns)/float64(maxConversationDepth))
}

// scoreExpectedOutputLength estimates relative output length using heuristic
// patterns. Inspired by Shortest-Job-First (SJF) scheduling research showing
// that ranking requests by estimated output length (rather than predicting
// exact token counts) can achieve significant latency reductions under load.
func (h *HeuristicClassifier) scoreExpectedOutputLength(req Request) float64 {
	lower := strings.ToLower(req.UserMessage)
	for _, s := range longOutputSignals {
		if strings.Contains(lower, s) {
			return 0.8
		}
	}
	for _, s := range shortOutputSignals {
		if strings.Contains(lower, s) {
			return 0.2
		}
	}
	return math.Min(1.0, float64(req.TokenCount)/float64(maxTokensForOutputEstimate))
}

func (h *HeuristicClassifier) countKeywords(text string, keywords []string) int {
	lower := strings.ToLower(text)
	count := 0
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			count++
		}
	}
	return count
}

func (h *HeuristicClassifier) buildReason(signals map[string]float64, req Request) string {
	var parts []string
	if signals["code"] >= 0.5 {
		parts = append(parts, "code present")
	}
	if signals["reasoning"] >= 0.5 {
		parts = append(parts, "reasoning keywords")
	}
	if signals["length"] >= 0.5 {
		parts = append(parts, fmt.Sprintf("prompt length %d tokens", req.TokenCount))
	}
	if signals["conv_depth"] >= 0.5 {
		parts = append(parts, fmt.Sprintf("conversation depth %d turns", req.ConvTurns))
	}
	if signals["output_length"] >= 0.6 {
		parts = append(parts, "expected long output")
	}
	if len(parts) == 0 {
		return "no strong signals, defaulting to small"
	}
	return strings.Join(parts, ", ")
}

func defaultKeywords() KeywordSets {
	return KeywordSets{
		Code: []string{
			"function", "algorithm", "class",
			"struct", "interface", "refactor", "debug",
		},
		Reasoning: []string{
			"explain", "why", "compare", "difference",
			"tradeoff", "design", "architecture",
		},
		Complexity: []string{
			"step by step", "edge case", "production",
			"distributed", "scale", "optimize",
		},
	}
}
