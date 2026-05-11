package classifier

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"prompt-response/internal/types"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mockServer(resp ClassifyResponse) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func mustParseRequest(t *testing.T, r *http.Request) Request {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	defer r.Body.Close()

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshaling: %v", err)
	}
	return req
}

func newTestRouter(srvURL string) *Router {
	return NewRouter(srvURL + "/classify")
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func TestInitializeClassifier(t *testing.T) {
	r := InitializeClassifier()
	if r.mlEndpoint != "http://localhost:8080/classify" {
		print("CONNECTING TO ENDPOINT")
		t.Errorf("endpoint = %q, want http://localhost:8080/classify", r.mlEndpoint)
	}
	if r.httpClient == nil {
		t.Fatal("httpClient is nil")
	}
	if r.httpClient.Timeout != 2*time.Second {
		print("TIMEOUT")
		t.Errorf("timeout = %v, want 2s", r.httpClient.Timeout)
	}
}

func TestNewRouter(t *testing.T) {
	r := NewRouter("http://ml-service:9000/classify")
	if r.mlEndpoint != "http://ml-service:9000/classify" {
		t.Errorf("endpoint = %q", r.mlEndpoint)
	}
}

// ---------------------------------------------------------------------------
// Happy path — each tier
// ---------------------------------------------------------------------------

func TestClassifySmall(t *testing.T) {
	srv := mockServer(ClassifyResponse{
		Tier:        types.TierSmall,
		Score:       0.15,
		Signals:     map[string]float64{"length": 0.1, "code": 0.0},
		BuildReason: "short simple prompt",
	})
	defer srv.Close()

	r := newTestRouter(srv.URL)

	resp, err := r.Classify(context.Background(), Request{
		UserMessage: "what is 2+2",
		TokenCount:  10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Tier != types.TierSmall {
		t.Errorf("tier = %q, want %q", resp.Tier, types.TierSmall)
	}
	if resp.Score != 0.15 {
		t.Errorf("score = %v, want 0.15", resp.Score)
	}
	if resp.BuildReason == "" {
		t.Error("build_reason is empty")
	}
}

func TestClassifyCode(t *testing.T) {
	srv := mockServer(ClassifyResponse{
		Tier:        types.TierCode,
		Score:       0.72,
		Signals:     map[string]float64{"code": 0.9, "length": 0.3},
		BuildReason: "code block detected",
	})
	defer srv.Close()

	r := newTestRouter(srv.URL)

	resp, err := r.Classify(context.Background(), Request{
		UserMessage: "implement a binary search tree",
		TokenCount:  50,
		HasCode:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Tier != types.TierCode {
		t.Errorf("tier = %q, want %q", resp.Tier, types.TierCode)
	}
}

func TestClassifyReasoning(t *testing.T) {
	srv := mockServer(ClassifyResponse{
		Tier:        types.TierReasoning,
		Score:       0.65,
		Signals:     map[string]float64{"reasoning": 0.8},
		BuildReason: "multi-step reasoning required",
	})
	defer srv.Close()

	r := newTestRouter(srv.URL)

	resp, err := r.Classify(context.Background(), Request{
		UserMessage: "explain step by step why inflation rises",
		TokenCount:  40,
		ConvTurns:   2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Tier != types.TierReasoning {
		t.Errorf("tier = %q, want %q", resp.Tier, types.TierReasoning)
	}
}

func TestClassifyLarge(t *testing.T) {
	srv := mockServer(ClassifyResponse{
		Tier:        types.TierLarge,
		Score:       0.92,
		Signals:     map[string]float64{"complexity": 0.9, "length": 0.8},
		BuildReason: "complex multi-domain task",
	})
	defer srv.Close()

	r := newTestRouter(srv.URL)

	resp, err := r.Classify(context.Background(), Request{
		UserMessage:  "write a comprehensive research paper on climate policy",
		SystemPrompt: "You are an expert policy analyst",
		TokenCount:   200,
		ConvTurns:    5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Tier != types.TierLarge {
		t.Errorf("tier = %q, want %q", resp.Tier, types.TierLarge)
	}
}

func TestClassifyAllTiers(t *testing.T) {
	tiers := []types.ModelTier{types.TierSmall, types.TierCode, types.TierReasoning, types.TierLarge}
	for _, tier := range tiers {
		t.Run(string(tier), func(t *testing.T) {
			srv := mockServer(ClassifyResponse{Tier: tier, Score: 0.5})
			defer srv.Close()

			r := newTestRouter(srv.URL)
			resp, err := r.Classify(context.Background(), Request{
				UserMessage: "test",
				TokenCount:  5,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.Tier != tier {
				t.Errorf("tier = %q, want %q", resp.Tier, tier)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Request body — verify all fields are serialized correctly
// ---------------------------------------------------------------------------

func TestRequestBodyFields(t *testing.T) {
	var received Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q, want application/json", ct)
		}
		received = mustParseRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ClassifyResponse{Tier: types.TierSmall, Score: 0.1})
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)

	r.Classify(context.Background(), Request{
		SystemPrompt: "be concise",
		UserMessage:  "hello world",
		TokenCount:   12,
		HasCode:      true,
		ConvTurns:    3,
	})

	if received.SystemPrompt != "be concise" {
		t.Errorf("system_prompt = %q, want %q", received.SystemPrompt, "be concise")
	}
	if received.UserMessage != "hello world" {
		t.Errorf("user_message = %q, want %q", received.UserMessage, "hello world")
	}
	if received.TokenCount != 12 {
		t.Errorf("token_count = %d, want 12", received.TokenCount)
	}
	if !received.HasCode {
		t.Error("has_code should be true")
	}
	if received.ConvTurns != 3 {
		t.Errorf("conv_turns = %d, want 3", received.ConvTurns)
	}
}

func TestRequestSystemPromptForwarded(t *testing.T) {
	var received Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = mustParseRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ClassifyResponse{Tier: types.TierReasoning, Score: 0.6})
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)

	r.Classify(context.Background(), Request{
		SystemPrompt: "You are a helpful math tutor",
		UserMessage:  "explain calculus",
		TokenCount:   80,
		ConvTurns:    4,
	})

	if received.SystemPrompt != "You are a helpful math tutor" {
		t.Errorf("system_prompt = %q", received.SystemPrompt)
	}
	if received.ConvTurns != 4 {
		t.Errorf("conv_turns = %d, want 4", received.ConvTurns)
	}
}

func TestRequestHasCodeFalse(t *testing.T) {
	var received Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = mustParseRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ClassifyResponse{Tier: types.TierSmall, Score: 0.1})
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)
	r.Classify(context.Background(), Request{
		UserMessage: "what is the weather",
		TokenCount:  8,
		HasCode:     false,
	})

	if received.HasCode {
		t.Error("has_code should be false")
	}
}

func TestRequestHasCodeTrue(t *testing.T) {
	var received Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = mustParseRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ClassifyResponse{Tier: types.TierCode, Score: 0.8})
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)
	r.Classify(context.Background(), Request{
		UserMessage: "fix this:\n```go\nfunc main() {}\n```",
		TokenCount:  30,
		HasCode:     true,
	})

	if !received.HasCode {
		t.Error("has_code should be true")
	}
}

// ---------------------------------------------------------------------------
// Response fields
// ---------------------------------------------------------------------------

func TestResponseSignals(t *testing.T) {
	srv := mockServer(ClassifyResponse{
		Tier:  types.TierReasoning,
		Score: 0.65,
		Signals: map[string]float64{
			"length":     0.3,
			"code":       0.0,
			"reasoning":  0.8,
			"complexity": 0.5,
			"conv_depth": 0.2,
		},
		BuildReason: "reasoning keywords detected",
	})
	defer srv.Close()

	r := newTestRouter(srv.URL)
	resp, err := r.Classify(context.Background(), Request{
		UserMessage: "explain step by step",
		TokenCount:  30,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Signals) != 5 {
		t.Errorf("signals count = %d, want 5", len(resp.Signals))
	}
	if resp.Signals["reasoning"] != 0.8 {
		t.Errorf("reasoning signal = %v, want 0.8", resp.Signals["reasoning"])
	}
	if resp.Signals["code"] != 0.0 {
		t.Errorf("code signal = %v, want 0.0", resp.Signals["code"])
	}
}

func TestResponseEmptySignals(t *testing.T) {
	srv := mockServer(ClassifyResponse{
		Tier:  types.TierSmall,
		Score: 0.1,
	})
	defer srv.Close()

	r := newTestRouter(srv.URL)
	resp, err := r.Classify(context.Background(), Request{
		UserMessage: "hi",
		TokenCount:  2,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Signals != nil && len(resp.Signals) != 0 {
		t.Errorf("expected nil/empty signals, got %v", resp.Signals)
	}
}

// ---------------------------------------------------------------------------
// Error handling
// ---------------------------------------------------------------------------

func TestClassifyServerDown(t *testing.T) {
	r := NewRouter("http://localhost:1/classify")

	resp, err := r.Classify(context.Background(), Request{
		UserMessage: "hello",
		TokenCount:  5,
	})

	if err == nil {
		t.Fatal("expected error when server is down")
	}
	if resp != nil {
		t.Fatal("response should be nil on error")
	}
}

func TestClassifyTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		json.NewEncoder(w).Encode(ClassifyResponse{Tier: types.TierSmall})
	}))
	defer srv.Close()

	r := NewRouter(srv.URL + "/classify")

	_, err := r.Classify(context.Background(), Request{
		UserMessage: "hello",
		TokenCount:  5,
	})

	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestClassifyContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.Classify(ctx, Request{
		UserMessage: "hello",
		TokenCount:  5,
	})

	if err == nil {
		t.Fatal("expected context canceled error")
	}
}

func TestClassifyContextDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		json.NewEncoder(w).Encode(ClassifyResponse{Tier: types.TierSmall})
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := r.Classify(ctx, Request{
		UserMessage: "hello",
		TokenCount:  5,
	})

	if err == nil {
		t.Fatal("expected deadline exceeded error")
	}
}

func TestClassify500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"detail":"model loading failed"}`))
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)

	_, err := r.Classify(context.Background(), Request{
		UserMessage: "hello",
		TokenCount:  5,
	})

	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestClassify503(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)

	_, err := r.Classify(context.Background(), Request{
		UserMessage: "hello",
		TokenCount:  5,
	})

	if err == nil {
		t.Fatal("expected error on 503")
	}
}

func TestClassify422(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"detail":"validation error"}`))
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)

	_, err := r.Classify(context.Background(), Request{
		UserMessage: "",
		TokenCount:  0,
	})

	if err == nil {
		t.Fatal("expected error on 422")
	}
}

func TestClassifyMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{not valid json`))
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)

	_, err := r.Classify(context.Background(), Request{
		UserMessage: "hello",
		TokenCount:  5,
	})

	if err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}

func TestClassifyEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)

	_, err := r.Classify(context.Background(), Request{
		UserMessage: "hello",
		TokenCount:  5,
	})

	if err == nil {
		t.Fatal("expected error on empty body")
	}
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

func TestClassifyConcurrent(t *testing.T) {
	var count atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ClassifyResponse{Tier: types.TierSmall, Score: 0.1})
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)

	n := 20
	done := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := r.Classify(context.Background(), Request{
				UserMessage: "hello",
				TokenCount:  5,
			})
			done <- err
		}()
	}

	for i := 0; i < n; i++ {
		if err := <-done; err != nil {
			t.Errorf("request %d failed: %v", i, err)
		}
	}

	if got := count.Load(); got != int64(n) {
		t.Errorf("server received %d requests, want %d", got, n)
	}
}

// ---------------------------------------------------------------------------
// Latency
// ---------------------------------------------------------------------------

func TestClassifyLatency(t *testing.T) {
	srv := mockServer(ClassifyResponse{Tier: types.TierSmall, Score: 0.1})
	defer srv.Close()

	r := newTestRouter(srv.URL)

	// warm up
	r.Classify(context.Background(), Request{UserMessage: "warmup", TokenCount: 1})

	iterations := 100
	start := time.Now()
	for i := 0; i < iterations; i++ {
		_, err := r.Classify(context.Background(), Request{
			UserMessage: "hello",
			TokenCount:  5,
		})
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
	avg := time.Since(start) / time.Duration(iterations)

	t.Logf("avg latency: %s over %d calls", avg, iterations)

	if avg > 50*time.Millisecond {
		t.Errorf("avg latency %s exceeds 50ms budget", avg)
	}
}

// ---------------------------------------------------------------------------
// Retry pattern (caller-side, not built into Router)
// ---------------------------------------------------------------------------

func TestRetryOnTransientError(t *testing.T) {
	var calls atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ClassifyResponse{Tier: types.TierSmall, Score: 0.1})
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)

	var resp *ClassifyResponse
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = r.Classify(context.Background(), Request{
			UserMessage: "hello",
			TokenCount:  5,
		})
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err != nil {
		t.Fatalf("all retries failed: %v", err)
	}
	if resp.Tier != types.TierSmall {
		t.Errorf("tier = %q, want %q", resp.Tier, types.TierSmall)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("expected 3 attempts, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// Fallback pattern (caller-side)
// ---------------------------------------------------------------------------

func TestFallbackWhenClassifierDown(t *testing.T) {
	r := NewRouter("http://localhost:1/classify")

	resp, err := r.Classify(context.Background(), Request{
		UserMessage: "hello",
		TokenCount:  5,
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if resp != nil {
		t.Fatal("response should be nil on error")
	}

	// simulate the fallback your proxy handler should do
	if err != nil {
		fallbackTier := types.TierReasoning
		if fallbackTier != types.TierReasoning {
			t.Errorf("fallback tier = %q, want %q", fallbackTier, types.TierReasoning)
		}
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestClassifyEmptyUserMessage(t *testing.T) {
	var received Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = mustParseRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ClassifyResponse{Tier: types.TierSmall, Score: 0.0})
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)
	resp, err := r.Classify(context.Background(), Request{
		UserMessage: "",
		TokenCount:  0,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received.UserMessage != "" {
		t.Errorf("user_message = %q, want empty", received.UserMessage)
	}
	if resp.Score != 0.0 {
		t.Errorf("score = %v, want 0.0", resp.Score)
	}
}

func TestClassifyLargeTokenCount(t *testing.T) {
	var received Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = mustParseRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ClassifyResponse{Tier: types.TierLarge, Score: 0.95})
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)
	r.Classify(context.Background(), Request{
		UserMessage: "very long prompt...",
		TokenCount:  128000,
		ConvTurns:   50,
	})

	if received.TokenCount != 128000 {
		t.Errorf("token_count = %d, want 128000", received.TokenCount)
	}
	if received.ConvTurns != 50 {
		t.Errorf("conv_turns = %d, want 50", received.ConvTurns)
	}
}

func TestClassifyZeroConvTurns(t *testing.T) {
	var received Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = mustParseRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ClassifyResponse{Tier: types.TierSmall, Score: 0.1})
	}))
	defer srv.Close()

	r := newTestRouter(srv.URL)
	r.Classify(context.Background(), Request{
		UserMessage: "first message",
		TokenCount:  10,
		ConvTurns:   0,
	})

	if received.ConvTurns != 0 {
		t.Errorf("conv_turns = %d, want 0", received.ConvTurns)
	}
}
