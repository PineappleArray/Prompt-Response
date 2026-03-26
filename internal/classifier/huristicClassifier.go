package classifier

import (
	"fmt"
	"math"
	"strings"
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
	Length     float64 // default 0.25
	Code       float64 // default 0.35
	Reasoning  float64 // default 0.25
	Complexity float64 // default 0.15
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
		"length":     h.scoreLength(req.TokenCount),
		"code":       h.scoreCode(req),
		"reasoning":  h.scoreReasoning(req),
		"complexity": h.scoreComplexity(req),
	}

	score := h.weights.Length*signals["length"] +
		h.weights.Code*signals["code"] +
		h.weights.Reasoning*signals["reasoning"] +
		h.weights.Complexity*signals["complexity"]

	tier := TierSmall
	if score >= h.threshold {
		tier = TierLarge
	}

	return Response{
		Tier:    tier,
		Score:   score,
		Signals: signals,
		Reason:  h.buildReason(signals, req),
	}, nil
}

func (h *HeuristicClassifier) scoreLength(tokens int) float64 {
	return math.Min(1.0, float64(tokens)/120.0)
}

// scoreCode: 1.0 if code block present, else keyword ratio
func (h *HeuristicClassifier) scoreCode(req Request) float64 {
	 if req.HasCode {
        return 1.0
    }

	strongSignals := []string{"implement", "build", "write", "create"}
	for _, s := range strongSignals {
        if strings.Contains(strings.ToLower(req.UserMessage), s) {
            return 1.0
        }
    }
	hits := h.countKeywords(req.UserMessage, h.keywords.Code)
	return math.Min(1.0, float64(hits)/3.0)
}

// scoreReasoning: keyword density in user message
func (h *HeuristicClassifier) scoreReasoning(req Request) float64 {
	hits := h.countKeywords(req.UserMessage, h.keywords.Reasoning)
	return math.Min(1.0, float64(hits)/2.0)
}

// scoreComplexity: multi-step or edge-case signals
func (h *HeuristicClassifier) scoreComplexity(req Request) float64 {
	hits := h.countKeywords(req.UserMessage, h.keywords.Complexity)
	return math.Min(1.0, float64(hits)/2.0)
}

// countKeywords: case-insensitive substring scan
func (h *HeuristicClassifier) countKeywords(
	text string, keywords []string,
) int {
	lower := strings.ToLower(text)
	count := 0
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			count++
		}
	}
	return count
}

func (h *HeuristicClassifier) buildReason(
	signals map[string]float64, req Request,
) string {
	var parts []string
	if signals["code"] >= 0.5 {
		parts = append(parts, "code present")
	}
	if signals["reasoning"] >= 0.5 {
		parts = append(parts, "reasoning keywords")
	}
	if signals["length"] >= 0.5 {
		parts = append(parts,
			fmt.Sprintf("prompt length %d tokens", req.TokenCount))
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
