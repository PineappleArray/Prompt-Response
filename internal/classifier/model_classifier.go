package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"prompt-response/internal/types"
)

type ModelClassifier struct {
	weights   SignalWeights
	threshold float64
	keywords  KeywordSets
	http      *http.Client
	endpoint  string
}

type Connection struct {
	endpoint string
	http     *http.Client
}

type Result struct {
	Model      string  `json:"model"`
	TaskType   string  `json:"task_type"`
	Reasoning  float64 `json:"reasoning"`
	Complexity float64 `json:"complexity"`
}

// remoteRequest mirrors the FastAPI /classify request shape.
type remoteRequest struct {
	Prompt string `json:"prompt"`
}

// remoteResponse mirrors the FastAPI /classify response shape.
type remoteResponse struct {
	Model      string  `json:"model"`
	TaskType   string  `json:"task_type"`
	Reasoning  float64 `json:"reasoning"`
	Complexity float64 `json:"complexity"`
}

func New(endpoint string, timeout time.Duration, weights SignalWeights, threshold float64, keywords KeywordSets) *ModelClassifier {
	return &ModelClassifier{
		endpoint: endpoint,
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		weights:   weights,
		threshold: threshold,
		keywords:  keywords,
	}
}

func (mc *ModelClassifier) Classify(req Request) (Response, error) {
	prompt := buildPrompt(req)

	remote, err := mc.callRemote(prompt)
	if err != nil {
		// Fall back to a local heuristic if the remote classifier is unreachable.
		return mc.classifyLocal(req, fmt.Sprintf("remote unavailable: %v", err)), nil
	}

	tier := mc.tierFromRemote(remote)

	// Composite score combining reasoning and complexity signals, clamped to [0,1].
	score := 0.6*remote.Reasoning + 0.4*remote.Complexity
	if score < 0 {
		score = 0
	} else if score > 1 {
		score = 1
	}

	signals := map[string]float64{
		"reasoning":  remote.Reasoning,
		"complexity": remote.Complexity,
	}

	reason := fmt.Sprintf(
		"remote: task=%s reasoning=%.2f complexity=%.2f -> model=%s",
		remote.TaskType, remote.Reasoning, remote.Complexity, remote.Model,
	)

	return Response{
		Tier:    tier,
		Score:   score,
		Signals: signals,
		Reason:  reason,
	}, nil
}

// buildPrompt flattens the Request into a single string for the remote classifier.
// The NVIDIA model was trained on raw user prompts, so we prioritize the user
// message and fold in the system prompt only if present.
func buildPrompt(req Request) string {
	var b strings.Builder
	if req.SystemPrompt != "" {
		b.WriteString(req.SystemPrompt)
		b.WriteString("\n\n")
	}
	b.WriteString(req.UserMessage)
	return b.String()
}

func (mc *ModelClassifier) callRemote(prompt string) (remoteResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mc.http.Timeout)
	defer cancel()

	body, err := json.Marshal(remoteRequest{Prompt: prompt})
	if err != nil {
		return remoteResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(mc.endpoint, "/") + "/classify"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return remoteResponse{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := mc.http.Do(httpReq)
	if err != nil {
		return remoteResponse{}, fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return remoteResponse{}, fmt.Errorf("classifier returned %d: %s", resp.StatusCode, snippet)
	}

	var out remoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return remoteResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

// tierFromRemote maps the remote classifier's model choice to a ModelTier.
// The Python side picks one of three models; we translate to tiers.
func (mc *ModelClassifier) tierFromRemote(r remoteResponse) types.ModelTier {
	switch r.Model {
	case "qwen2.5-32b-instruct":
		return types.TierLarge
	case "qwen2.5-coder-7b-instruct":
		return types.TierCode
	case "llama-3.1-8b-instruct":
		return types.TierSmall
	}

	// Fallback: use the composite score against the threshold.
	score := 0.6*r.Reasoning + 0.4*r.Complexity
	if score >= mc.threshold {
		return types.TierLarge
	}
	return types.TierSmall
}

// classifyLocal is the fallback path when the remote classifier is unreachable.
// It uses the weights, keywords, and threshold configured on the classifier
// to make a purely local decision.
func (mc *ModelClassifier) classifyLocal(req Request, note string) Response {
	signals := make(map[string]float64, 4)

	// Length signal
	const tokenCeiling = 8000.0
	length := float64(req.TokenCount) / tokenCeiling
	if length > 1 {
		length = 1
	}
	signals["length"] = length

	// Code signal
	code := 0.0
	if req.HasCode {
		code = 1.0
	}
	signals["code"] = code

	// Conversation depth
	const turnCeiling = 20.0
	conv := float64(req.ConvTurns) / turnCeiling
	if conv > 1 {
		conv = 1
	}
	signals["conversation"] = conv

	// Reasoning keyword signal
	reasoning := keywordScore(req.UserMessage, mc.keywords.Reasoning)
	signals["reasoning"] = reasoning

	score := signals["length"]*mc.weights.Length +
		signals["code"]*mc.weights.Code +
		signals["complexity"]*mc.weights.Complexity +
		signals["reasoning"]*mc.weights.Reasoning
	if score < 0 {
		score = 0
	} else if score > 1 {
		score = 1
	}

	var tier types.ModelTier
	switch {
	case req.HasCode:
		tier = types.TierCode
	case score >= mc.threshold:
		tier = types.TierLarge
	default:
		tier = types.TierSmall
	}

	return Response{
		Tier:    tier,
		Score:   score,
		Signals: signals,
		Reason:  fmt.Sprintf("local fallback (%s): score=%.2f threshold=%.2f", note, score, mc.threshold),
	}
}

// --- helpers ---

func keywordScore(text string, needles []string) float64 {
	if len(needles) == 0 || text == "" {
		return 0
	}
	lower := strings.ToLower(text)
	hits := 0
	for _, n := range needles {
		if n != "" && strings.Contains(lower, strings.ToLower(n)) {
			hits++
		}
	}
	score := float64(hits) / 3.0
	if score > 1 {
		score = 1
	}
	return score
}
