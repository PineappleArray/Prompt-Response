package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"prompt-response/internal/types"
	"time"
)

// ModelTier represents the target model tier for routing.
// In your real codebase this is types.ModelTier — keeping it local here
// so the test package compiles standalone. Replace with the import when
// you drop this into internal/classifier/.
type ModelTier = string

type ClassifyResponse struct {
	Tier        types.ModelTier    `json:"tier"`
	Score       float64            `json:"score"`
	Signals     map[string]float64 `json:"signals"`
	BuildReason string             `json:"build_reason"`
}

type KeywordSets struct {
	Code       []string
	Reasoning  []string
	Complexity []string
}

type Router struct {
	mlEndpoint string
	httpClient *http.Client
}

type Request struct {
	SystemPrompt string `json:"system_prompt"`
	UserMessage  string `json:"user_message"`
	TokenCount   int    `json:"token_count"`
	HasCode      bool   `json:"has_code"`
	ConvTurns    int    `json:"conv_turns"`
}

type Response struct {
	Tier    types.ModelTier    // routing decision
	Score   float64            // raw composite score 0–1
	Signals map[string]float64 // per-signal breakdown
	Reason  string             // human-readable explanation
}

// NewRouter creates a Router with a configurable endpoint.
// Use this in tests and when the endpoint comes from config.
func NewRouter(endpoint string) *Router {
	return &Router{
		mlEndpoint: endpoint,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// initializeClassifier uses the hardcoded default endpoint.
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
