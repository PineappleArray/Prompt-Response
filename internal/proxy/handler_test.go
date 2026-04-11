package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"prompt-response/internal/audit"
	"prompt-response/internal/circuit"
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
		Circuit: config.Circuit{
			ErrorThreshold: 0.5,
			WindowSize:     10 * time.Second,
			Cooldown:       30 * time.Second,
			MinSamples:     5,
		},
		Retry: config.Retry{
			MaxRetries: 1,
			Timeout:    30 * time.Second,
		},
	}
	scor := scorer.New(replicas, mem, poll, cfg.Weights, cfg.AffinityTTL, cfg.MaxQueue)
	cls := classifier.NewHeuristic(classifier.HeuristicConfig{
		Weights: classifier.SignalWeights{
			Length: 0.20, Code: 0.30, Reasoning: 0.15,
			Complexity: 0.10, ConvDepth: 0.10, OutputLength: 0.15,
		},
		Threshold: cfg.Threshold,
	})
	cr := circuit.NewRegistry(circuit.Config{
		ErrorThreshold: cfg.Circuit.ErrorThreshold,
		WindowSize:     cfg.Circuit.WindowSize,
		Cooldown:       cfg.Circuit.Cooldown,
		MinSamples:     cfg.Circuit.MinSamples,
	})
	return New(scor, cls, cfg, cr, nil)
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
	// Create a mock vLLM backend that sends a realistic multi-chunk SSE stream
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, token := range []string{"Hello", " world", "!"} {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"%s\"}}]}\n\n", token)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
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
	body := w.Body.String()
	if !strings.Contains(body, "Hello") {
		t.Errorf("expected proxied response containing 'Hello', got %s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("expected [DONE] sentinel in response, got %s", body)
	}
}

func TestRetry_SuccessOnSecondAttempt(t *testing.T) {
	// Backend 1: always returns 503
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer backend1.Close()

	// Backend 2: returns success with SSE stream
	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"retried\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer backend2.Close()

	replicas := []config.Replica{
		{ID: "r1", URL: backend1.URL, Model: "test", Tier: types.TierSmall},
		{ID: "r2", URL: backend2.URL, Model: "test", Tier: types.TierSmall},
	}
	h := newTestHandler(replicas)

	payload := `{"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 after retry, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "retried") {
		t.Errorf("expected response from second backend, got %s", body)
	}
}

func TestRetry_NoRetryOn4xx(t *testing.T) {
	var attempts atomic.Int32

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer backend.Close()

	replicas := []config.Replica{
		{ID: "r1", URL: backend.URL, Model: "test", Tier: types.TierSmall},
		{ID: "r2", URL: backend.URL, Model: "test", Tier: types.TierSmall},
	}
	h := newTestHandler(replicas)

	payload := `{"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 passed through, got %d", w.Code)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("expected 1 attempt (no retry on 4xx), got %d", got)
	}
}

func TestRetry_AllReplicasExhausted(t *testing.T) {
	// All backends return 503
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer backend.Close()

	replicas := []config.Replica{
		{ID: "r1", URL: backend.URL, Model: "test", Tier: types.TierSmall},
		{ID: "r2", URL: backend.URL, Model: "test", Tier: types.TierSmall},
	}
	h := newTestHandler(replicas)

	payload := `{"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when all replicas exhausted, got %d", w.Code)
	}
}

func TestRetry_ConnectionRefused(t *testing.T) {
	// Backend 1: unreachable (closed immediately)
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	backend1.Close() // close to simulate connection refused

	// Backend 2: healthy
	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer backend2.Close()

	replicas := []config.Replica{
		{ID: "r1", URL: backend1.URL, Model: "test", Tier: types.TierSmall},
		{ID: "r2", URL: backend2.URL, Model: "test", Tier: types.TierSmall},
	}
	h := newTestHandler(replicas)

	payload := `{"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 after retry on connection refused, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ok") {
		t.Errorf("expected response from second backend")
	}
}

func TestRouterStatus_IncludesCircuit(t *testing.T) {
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

	// Check that replica status includes circuit info
	replicaList := body["replicas"].([]any)
	if len(replicaList) == 0 {
		t.Fatal("expected at least one replica in status")
	}
	r1 := replicaList[0].(map[string]any)
	if r1["circuit"] != "closed" {
		t.Errorf("expected circuit=closed for new replica, got %v", r1["circuit"])
	}

	// Check that config includes circuit and retry settings
	cfgMap := body["config"].(map[string]any)
	if _, ok := cfgMap["circuit"]; !ok {
		t.Error("expected circuit config in status response")
	}
	if _, ok := cfgMap["retry"]; !ok {
		t.Error("expected retry config in status response")
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

func TestAuditEndpoint_Disabled(t *testing.T) {
	h := newTestHandler(nil) // audit is nil
	req := httptest.NewRequest(http.MethodGet, "/v1/router/audit", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["enabled"] != false {
		t.Errorf("expected enabled=false, got %v", body["enabled"])
	}
}

func TestAuditEndpoint_WithRecords(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer backend.Close()

	replicas := []config.Replica{
		{ID: "r1", URL: backend.URL, Model: "test", Tier: types.TierSmall},
	}

	// Build handler with audit trail
	mem := store.NewMemory()
	poll := poller.New(replicas, 0)
	cfg := &config.Config{
		Replicas:    replicas,
		Weights:     config.Weights{CacheAffinity: 0.50, QueueDepth: 0.25, KVCachePressure: 0.15, Baseline: 0.10},
		AffinityTTL: 5 * time.Minute,
		MaxQueue:    20,
		Threshold:   0.35,
		Circuit:     config.Circuit{ErrorThreshold: 0.5, WindowSize: 10 * time.Second, Cooldown: 30 * time.Second, MinSamples: 5},
		Retry:       config.Retry{MaxRetries: 1, Timeout: 30 * time.Second},
	}
	scor := scorer.New(replicas, mem, poll, cfg.Weights, cfg.AffinityTTL, cfg.MaxQueue)
	cls := classifier.NewHeuristic(classifier.HeuristicConfig{
		Weights:   classifier.SignalWeights{Length: 0.20, Code: 0.30, Reasoning: 0.15, Complexity: 0.10, ConvDepth: 0.10, OutputLength: 0.15},
		Threshold: cfg.Threshold,
	})
	cr := circuit.NewRegistry(circuit.Config{ErrorThreshold: 0.5, WindowSize: 10 * time.Second, Cooldown: 30 * time.Second, MinSamples: 5})
	trail := audit.NewTrail(100)
	h := New(scor, cls, cfg, cr, trail)

	// Make a request to generate an audit record
	payload := `{"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Verify audit trail has a record
	if trail.Len() != 1 {
		t.Fatalf("expected 1 audit record, got %d", trail.Len())
	}

	// Query the audit endpoint
	req = httptest.NewRequest(http.MethodGet, "/v1/router/audit?limit=10", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["count"].(float64) != 1 {
		t.Errorf("expected count=1, got %v", body["count"])
	}
	if body["enabled"] != true {
		t.Errorf("expected enabled=true, got %v", body["enabled"])
	}

	records := body["records"].([]any)
	rec := records[0].(map[string]any)
	if rec["replica_id"] != "r1" {
		t.Errorf("expected replica_id=r1, got %v", rec["replica_id"])
	}
	if rec["status_code"].(float64) != 200 {
		t.Errorf("expected status_code=200, got %v", rec["status_code"])
	}
}

func TestAuditRecords_NoReplicas(t *testing.T) {
	replicas := []config.Replica{
		{ID: "r1", URL: "http://localhost:9999", Model: "test", Tier: types.TierSmall},
	}

	mem := store.NewMemory()
	poll := poller.New(replicas, 0)
	for i := 0; i < 3; i++ {
		poll.SimulateFailure("r1")
	}
	cfg := &config.Config{
		Replicas:    replicas,
		Weights:     config.Weights{CacheAffinity: 0.50, QueueDepth: 0.25, KVCachePressure: 0.15, Baseline: 0.10},
		AffinityTTL: 5 * time.Minute,
		MaxQueue:    20,
		Threshold:   0.35,
		Circuit:     config.Circuit{ErrorThreshold: 0.5, WindowSize: 10 * time.Second, Cooldown: 30 * time.Second, MinSamples: 5},
		Retry:       config.Retry{MaxRetries: 1, Timeout: 30 * time.Second},
	}
	scor := scorer.New(replicas, mem, poll, cfg.Weights, cfg.AffinityTTL, cfg.MaxQueue)
	cls := classifier.NewHeuristic(classifier.HeuristicConfig{
		Weights:   classifier.SignalWeights{Length: 0.20, Code: 0.30, Reasoning: 0.15, Complexity: 0.10, ConvDepth: 0.10, OutputLength: 0.15},
		Threshold: cfg.Threshold,
	})
	cr := circuit.NewRegistry(circuit.Config{ErrorThreshold: 0.5, WindowSize: 10 * time.Second, Cooldown: 30 * time.Second, MinSamples: 5})
	trail := audit.NewTrail(100)
	h := New(scor, cls, cfg, cr, trail)

	payload := `{"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}

	// Audit should record the failure
	if trail.Len() != 1 {
		t.Fatalf("expected 1 audit record for failed request, got %d", trail.Len())
	}

	rec := trail.Recent(1)[0]
	if rec.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503 in audit, got %d", rec.StatusCode)
	}
	if rec.ReplicaID != "" {
		t.Errorf("expected empty replica_id for 503, got %s", rec.ReplicaID)
	}
}
