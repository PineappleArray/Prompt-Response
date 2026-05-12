package classifier

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// classifierURL returns the real classifier endpoint.
// Override with CLASSIFIER_URL env var, defaults to localhost:8000.
func classifierURL() string {
	if url := os.Getenv("CLASSIFIER_URL"); url != "" {
		return url
	}
	return "http://127.0.0.1:8000/classify"
}

// skipIfNotRunning skips the test if the classifier isn't reachable.
func skipIfNotRunning(t *testing.T) {
	t.Helper()
	url := classifierURL()

	// try the health endpoint (strip /classify, add /health)
	healthURL := url[:len(url)-len("/classify")] + "/health"
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(healthURL)
	if err != nil {
		t.Skipf("classifier not running at %s: %v", healthURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("classifier health check returned %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Basic classification against real server
// ---------------------------------------------------------------------------

func TestIntegrationClassifySmall(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	resp, err := r.Classify(context.Background(), Request{
		UserMessage: "what is 2+2",
		TokenCount:  10,
		HasCode:     false,
		ConvTurns:   0,
	})
	if err != nil {
		t.Fatalf("classify failed: %v", err)
	}

	t.Logf("tier=%s score=%.4f reason=%q signals=%v",
		resp.Tier, resp.Score, resp.BuildReason, resp.Signals)

	if resp.Tier != "small" {
		t.Errorf("expected small tier for simple prompt, got %q", resp.Tier)
	}
}

func TestIntegrationClassifyCode(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	resp, err := r.Classify(context.Background(), Request{
		UserMessage: "implement a binary search tree in python with insert and delete methods",
		TokenCount:  80,
		HasCode:     true,
		ConvTurns:   0,
	})
	if err != nil {
		t.Fatalf("classify failed: %v", err)
	}

	t.Logf("tier=%s score=%.4f reason=%q signals=%v",
		resp.Tier, resp.Score, resp.BuildReason, resp.Signals)

	if resp.Tier != "code" {
		t.Errorf("expected code tier for coding prompt, got %q", resp.Tier)
	}
}

func TestIntegrationClassifyReasoning(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	resp, err := r.Classify(context.Background(), Request{
		UserMessage: "explain step by step why inflation affects interest rates and analyze the trade-offs of monetary policy",
		TokenCount:  80,
		HasCode:     false,
		ConvTurns:   3,
	})
	if err != nil {
		t.Fatalf("classify failed: %v", err)
	}

	t.Logf("tier=%s score=%.4f reason=%q signals=%v",
		resp.Tier, resp.Score, resp.BuildReason, resp.Signals)

	if resp.Tier != "reasoning" {
		t.Errorf("expected reasoning tier, got %q", resp.Tier)
	}
}

func TestIntegrationClassifyLarge(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	resp, err := r.Classify(context.Background(), Request{
		SystemPrompt: "You are an expert policy analyst and researcher",
		UserMessage:  "write a comprehensive research paper analyzing the global semiconductor supply chain, including detailed analysis of trade policies, geopolitical implications, and compare the approaches of major nations step by step",
		TokenCount:   400,
		HasCode:      false,
		ConvTurns:    8,
	})
	if err != nil {
		t.Fatalf("classify failed: %v", err)
	}

	t.Logf("tier=%s score=%.4f reason=%q signals=%v",
		resp.Tier, resp.Score, resp.BuildReason, resp.Signals)

	if resp.Tier != "large" {
		t.Errorf("expected large tier for complex prompt, got %q", resp.Tier)
	}
}

// ---------------------------------------------------------------------------
// Response structure validation
// ---------------------------------------------------------------------------

func TestIntegrationResponseFields(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	resp, err := r.Classify(context.Background(), Request{
		SystemPrompt: "be helpful",
		UserMessage:  "explain how neural networks learn",
		TokenCount:   50,
		HasCode:      false,
		ConvTurns:    1,
	})
	if err != nil {
		t.Fatalf("classify failed: %v", err)
	}

	// tier should be a known value
	validTiers := map[string]bool{"small": true, "code": true, "reasoning": true, "large": true}
	if !validTiers[string(resp.Tier)] {
		t.Errorf("unexpected tier %q", resp.Tier)
	}

	// score should be 0-1
	if resp.Score < 0 || resp.Score > 1 {
		t.Errorf("score %f out of [0, 1] range", resp.Score)
	}

	// signals should exist
	if resp.Signals == nil {
		t.Error("signals map is nil")
	}

	// build_reason should not be empty
	if resp.BuildReason == "" {
		t.Error("build_reason is empty")
	}

	t.Logf("tier=%s score=%.4f signals=%v reason=%q",
		resp.Tier, resp.Score, resp.Signals, resp.BuildReason)
}

// ---------------------------------------------------------------------------
// Latency
// ---------------------------------------------------------------------------

func TestIntegrationLatency(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	// warm up
	r.Classify(context.Background(), Request{UserMessage: "warmup", TokenCount: 1})

	iterations := 50
	var total time.Duration
	var min, max time.Duration
	min = time.Hour

	for i := 0; i < iterations; i++ {
		start := time.Now()
		_, err := r.Classify(context.Background(), Request{
			UserMessage: "explain how machine learning works",
			TokenCount:  30,
		})
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}

		total += elapsed
		if elapsed < min {
			min = elapsed
		}
		if elapsed > max {
			max = elapsed
		}
	}

	avg := total / time.Duration(iterations)
	t.Logf("latency over %d calls: avg=%s min=%s max=%s", iterations, avg, min, max)

	// real server should respond within 50ms on localhost
	if avg > 50*time.Millisecond {
		t.Errorf("avg latency %s exceeds 50ms budget", avg)
	}
}

func TestIntegrationLatencyByComplexity(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	cases := []struct {
		name string
		req  Request
	}{
		{"simple", Request{UserMessage: "hi", TokenCount: 5}},
		{"medium", Request{UserMessage: "explain how transformers work step by step", TokenCount: 50, ConvTurns: 2}},
		{"complex", Request{
			SystemPrompt: "You are an expert researcher",
			UserMessage:  "write a comprehensive analysis of global trade policy comparing approaches across nations with detailed reasoning",
			TokenCount:   300,
			HasCode:      false,
			ConvTurns:    10,
		}},
		{"code", Request{UserMessage: "implement a red-black tree with insert delete and rebalance", TokenCount: 80, HasCode: true}},
	}

	iterations := 20
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// warm up
			r.Classify(context.Background(), tc.req)

			var total time.Duration
			var lastResp *ClassifyResponse
			for i := 0; i < iterations; i++ {
				start := time.Now()
				resp, err := r.Classify(context.Background(), tc.req)
				total += time.Since(start)
				if err != nil {
					t.Fatalf("iteration %d: %v", i, err)
				}
				lastResp = resp
			}
			avg := total / time.Duration(iterations)
			t.Logf("avg=%s tier=%s score=%.4f", avg, lastResp.Tier, lastResp.Score)
		})
	}
}

// ---------------------------------------------------------------------------
// Concurrency against real server
// ---------------------------------------------------------------------------

func TestIntegrationConcurrent(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	n := 20
	var wg sync.WaitGroup
	var errors atomic.Int64
	var totalLatency atomic.Int64

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			start := time.Now()
			resp, err := r.Classify(context.Background(), Request{
				UserMessage: fmt.Sprintf("test prompt number %d explain why", idx),
				TokenCount:  20,
				ConvTurns:   idx % 5,
			})
			elapsed := time.Since(start)
			totalLatency.Add(elapsed.Microseconds())

			if err != nil {
				errors.Add(1)
				t.Errorf("request %d failed: %v", idx, err)
				return
			}
			if resp.Tier == "" {
				errors.Add(1)
				t.Errorf("request %d: empty tier", idx)
			}
		}(i)
	}

	wg.Wait()

	avgUs := totalLatency.Load() / int64(n)
	t.Logf("concurrent: %d requests, %d errors, avg latency %dµs",
		n, errors.Load(), avgUs)

	if errors.Load() > 0 {
		t.Errorf("%d/%d requests failed", errors.Load(), n)
	}
}

func TestIntegrationConcurrentHeavy(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	n := 50
	var wg sync.WaitGroup
	var errors atomic.Int64
	var totalLatency atomic.Int64

	prompts := []Request{
		{UserMessage: "hi", TokenCount: 5},
		{UserMessage: "implement quicksort", TokenCount: 30, HasCode: true},
		{UserMessage: "explain step by step why the sky is blue", TokenCount: 50, ConvTurns: 2},
		{UserMessage: "write a comprehensive research paper on AI safety", TokenCount: 200, ConvTurns: 5},
	}

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			req := prompts[idx%len(prompts)]

			start := time.Now()
			resp, err := r.Classify(context.Background(), req)
			elapsed := time.Since(start)
			totalLatency.Add(elapsed.Microseconds())

			if err != nil {
				errors.Add(1)
				return
			}
			_ = resp
		}(i)
	}

	wg.Wait()

	avgUs := totalLatency.Load() / int64(n)
	t.Logf("heavy concurrent: %d requests, %d errors, avg latency %dµs",
		n, errors.Load(), avgUs)

	if errors.Load() > 2 {
		t.Errorf("too many failures: %d/%d", errors.Load(), n)
	}
}

// ---------------------------------------------------------------------------
// Consistency — same input should produce same output
// ---------------------------------------------------------------------------

func TestIntegrationConsistency(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	req := Request{
		UserMessage: "implement a linked list in python",
		TokenCount:  40,
		HasCode:     true,
		ConvTurns:   0,
	}

	first, err := r.Classify(context.Background(), req)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	for i := 0; i < 10; i++ {
		resp, err := r.Classify(context.Background(), req)
		if err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
		if resp.Tier != first.Tier {
			t.Errorf("call %d: tier %q != first tier %q", i, resp.Tier, first.Tier)
		}
		if resp.Score != first.Score {
			t.Errorf("call %d: score %f != first score %f", i, resp.Score, first.Score)
		}
	}
}

// ---------------------------------------------------------------------------
// Edge cases against real server
// ---------------------------------------------------------------------------

func TestIntegrationEmptyPrompt(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	resp, err := r.Classify(context.Background(), Request{
		UserMessage: "",
		TokenCount:  0,
	})

	// should not error — server should handle gracefully
	if err != nil {
		t.Fatalf("empty prompt failed: %v", err)
	}

	t.Logf("empty prompt: tier=%s score=%.4f", resp.Tier, resp.Score)
}

func TestIntegrationLongPrompt(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	// generate a very long prompt
	long := ""
	for i := 0; i < 200; i++ {
		long += "explain the implications of this complex scenario in detail. "
	}

	start := time.Now()
	resp, err := r.Classify(context.Background(), Request{
		UserMessage: long,
		TokenCount:  5000,
		ConvTurns:   20,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("long prompt failed: %v", err)
	}

	t.Logf("long prompt: tier=%s score=%.4f latency=%s", resp.Tier, resp.Score, elapsed)
}

func TestIntegrationUnicodePrompt(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	resp, err := r.Classify(context.Background(), Request{
		UserMessage: "解释一下量子计算的基本原理，并比较经典计算机和量子计算机的区别",
		TokenCount:  60,
	})

	if err != nil {
		t.Fatalf("unicode prompt failed: %v", err)
	}

	t.Logf("unicode prompt: tier=%s score=%.4f", resp.Tier, resp.Score)
}

func TestIntegrationCodeBlockInPrompt(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	resp, err := r.Classify(context.Background(), Request{
		UserMessage: "fix this code:\n```python\ndef fib(n):\n    if n <= 1:\n        return n\n    return fib(n-1) + fib(n-2)\n```\nit's too slow for large n",
		TokenCount:  60,
		HasCode:     true,
	})

	if err != nil {
		t.Fatalf("code block prompt failed: %v", err)
	}

	t.Logf("code block: tier=%s score=%.4f", resp.Tier, resp.Score)

	if resp.Tier != "code" {
		t.Errorf("expected code tier for prompt with code block, got %q", resp.Tier)
	}
}
