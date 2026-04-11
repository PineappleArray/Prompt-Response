package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"prompt-response/internal/classifier"
	"prompt-response/internal/config"
	"prompt-response/internal/poller"
	"prompt-response/internal/scorer"
	"prompt-response/internal/store"
	"prompt-response/internal/types"
)

func newTestHandler(replicas []config.Replica) *Handler {
	mem := store.NewMemory()
	poll := poller.New(replicas, 0)
	cfg := &config.Config{
		Replicas: replicas,
		Weights: config.Weights{
			CacheAffinity:   0.50,
			QueueDepth:      0.25,
			KVCachePressure: 0.15,
			Baseline:        0.10,
		},
		AffinityTTL: 5 * time.Minute,
		MaxQueue:    20,
		Threshold:   0.35,
	}
	scor := scorer.New(replicas, mem, poll, cfg.Weights, cfg.AffinityTTL, cfg.MaxQueue)
	cls := classifier.NewHeuristic(classifier.HeuristicConfig{
		Weights: classifier.SignalWeights{
			Length: 0.20, Code: 0.30, Reasoning: 0.15,
			Complexity: 0.10, ConvDepth: 0.10, OutputLength: 0.15,
		},
		Threshold: cfg.Threshold,
	})
	return New(scor, cls, cfg)
}

func TestHealthz(t *testing.T) {
	h := newTestHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ok") {
		t.Errorf("expected ok in body, got %s", w.Body.String())
	}
}

func TestReadyz_HealthyReplicas(t *testing.T) {
	replicas := []config.Replica{
		{ID: "r1", URL: "http://localhost:8001", Model: "test", Tier: types.TierSmall},
	}
	h := newTestHandler(replicas)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ready" {
		t.Errorf("expected ready, got %v", body["status"])
	}
}

func TestModels(t *testing.T) {
	replicas := []config.Replica{
		{ID: "r1", URL: "http://l1", Model: "Qwen/Qwen2.5-1.5B", Tier: types.TierSmall},
		{ID: "r2", URL: "http://l2", Model: "Qwen/Qwen2.5-7B", Tier: types.TierLarge},
		{ID: "r3", URL: "http://l3", Model: "Qwen/Qwen2.5-1.5B", Tier: types.TierSmall}, // duplicate model
	}
	h := newTestHandler(replicas)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	data := body["data"].([]any)
	if len(data) != 2 {
		t.Errorf("expected 2 unique models, got %d", len(data))
	}
}

func TestRouterStatus(t *testing.T) {
	replicas := []config.Replica{
		{ID: "r1", URL: "http://l1", Model: "test", Tier: types.TierSmall},
	}
	h := newTestHandler(replicas)
	req := httptest.NewRequest(http.MethodGet, "/v1/router/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "running" {
		t.Errorf("expected running, got %v", body["status"])
	}
	if body["healthy_count"].(float64) != 1 {
		t.Errorf("expected 1 healthy, got %v", body["healthy_count"])
	}
}

func TestInvalidJSON(t *testing.T) {
	h := newTestHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	var body apiError
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error.Code != "invalid_json" {
		t.Errorf("expected error code invalid_json, got %s", body.Error.Code)
	}
}

func TestEmptyBody(t *testing.T) {
	h := newTestHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(""))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	var body apiError
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error.Code != "empty_body" {
		t.Errorf("expected error code empty_body, got %s", body.Error.Code)
	}
}

func TestEmptyMessages(t *testing.T) {
	h := newTestHandler(nil)
	payload := `{"messages":[]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	var body apiError
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error.Code != "invalid_request" {
		t.Errorf("expected error code invalid_request, got %s", body.Error.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := newTestHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestNoHealthyReplicas(t *testing.T) {
	replicas := []config.Replica{
		{ID: "r1", URL: "http://localhost:9999", Model: "test", Tier: types.TierSmall},
	}
	h := newTestHandler(replicas)

	// mark replica unhealthy
	poll := poller.New(replicas, 0)
	for i := 0; i < 3; i++ {
		poll.SimulateFailure("r1")
	}
	mem := store.NewMemory()
	scor := scorer.New(replicas, mem, poll, h.cfg.Weights, h.cfg.AffinityTTL, h.cfg.MaxQueue)
	h.scorer = scor

	payload := `{"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestValidRequestRoutes(t *testing.T) {
	// Create a mock vLLM backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"hi"}}]}`))
	}))
	defer backend.Close()

	replicas := []config.Replica{
		{ID: "r1", URL: backend.URL, Model: "test", Tier: types.TierSmall},
	}
	h := newTestHandler(replicas)

	payload := `{"messages":[{"role":"system","content":"You are helpful"},{"role":"user","content":"what is 2+2"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "hi") {
		t.Errorf("expected proxied response, got %s", w.Body.String())
	}
}

func TestCountTurns(t *testing.T) {
	req := openAIRequest{}
	req.Messages = []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{
		{Role: "system", Content: "be helpful"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "user", Content: "how are you"},
	}

	turns := countTurns(req)
	if turns != 2 {
		t.Errorf("expected 2 user turns, got %d", turns)
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		text string
		want int
	}{
		{"", 0},
		{"hi", 0},           // 2 chars / 4 = 0
		{"hello world!", 3}, // 12 chars / 4 = 3
	}
	for _, tt := range tests {
		got := estimateTokens(tt.text)
		if got != tt.want {
			t.Errorf("estimateTokens(%q) = %d, want %d", tt.text, got, tt.want)
		}
	}
}

func TestHasCodeBlock(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"just text", false},
		{"use ```code```", true},
		{"func main()", true},
		{"def hello():", true},
		{"class Foo:", true},
		{"no code here", false},
	}
	for _, tt := range tests {
		got := hasCodeBlock(tt.text)
		if got != tt.want {
			t.Errorf("hasCodeBlock(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}
