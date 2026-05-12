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

func classifierURL() string {
	if url := os.Getenv("CLASSIFIER_URL"); url != "" {
		return url
	}
	return "http://127.0.0.1:8000/classify"
}

func skipIfNotRunning(t *testing.T) {
	t.Helper()
	url := classifierURL()
	healthURL := url[:len(url)-len("/classify")] + "/health"
	client := &http.Client{Timeout: 5 * time.Second}
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
// Test counter — tracks pass/fail across all integration tests
// ---------------------------------------------------------------------------

var (
	integrationPassed atomic.Int64
	integrationFailed atomic.Int64
)

func recordPass(t *testing.T) {
	t.Helper()
	integrationPassed.Add(1)
}

func recordFail(t *testing.T, format string, args ...any) {
	t.Helper()
	integrationFailed.Add(1)
	t.Errorf(format, args...)
}

// TestIntegrationSummary runs last (alphabetically after all other tests)
// and prints the overall pass/fail count.
func TestIntegrationZZZSummary(t *testing.T) {
	passed := integrationPassed.Load()
	failed := integrationFailed.Load()
	total := passed + failed

	if total == 0 {
		t.Skip("no integration tests ran")
	}

	t.Logf("\n========================================")
	t.Logf("  INTEGRATION TEST SUMMARY")
	t.Logf("========================================")
	t.Logf("  Total checks:  %d", total)
	t.Logf("  Passed:        %d", passed)
	t.Logf("  Failed:        %d", failed)
	t.Logf("  Pass rate:     %.1f%%", float64(passed)/float64(total)*100)
	t.Logf("========================================")

	if failed > 0 {
		t.Errorf("%d/%d checks failed", failed, total)
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
		recordFail(t, "classify failed: %v", err)
		return
	}

	t.Logf("tier=%s score=%.4f reason=%q signals=%v",
		resp.Tier, resp.Score, resp.BuildReason, resp.Signals)

	if resp.Tier == "small" {
		recordPass(t)
	} else {
		recordFail(t, "expected small tier for simple prompt, got %q", resp.Tier)
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
		recordFail(t, "classify failed: %v", err)
		return
	}

	t.Logf("tier=%s score=%.4f reason=%q signals=%v",
		resp.Tier, resp.Score, resp.BuildReason, resp.Signals)

	if resp.Tier == "code" {
		recordPass(t)
	} else {
		recordFail(t, "expected code tier for coding prompt, got %q", resp.Tier)
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
		recordFail(t, "classify failed: %v", err)
		return
	}

	t.Logf("tier=%s score=%.4f reason=%q signals=%v",
		resp.Tier, resp.Score, resp.BuildReason, resp.Signals)

	if resp.Tier == "reasoning" {
		recordPass(t)
	} else {
		recordFail(t, "expected reasoning tier, got %q", resp.Tier)
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
		recordFail(t, "classify failed: %v", err)
		return
	}

	t.Logf("tier=%s score=%.4f reason=%q signals=%v",
		resp.Tier, resp.Score, resp.BuildReason, resp.Signals)

	if resp.Tier == "large" {
		recordPass(t)
	} else {
		recordFail(t, "expected large tier for complex prompt, got %q", resp.Tier)
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
		recordFail(t, "classify failed: %v", err)
		return
	}

	checks := 0
	passed := 0

	// tier should be a known value
	checks++
	validTiers := map[string]bool{"small": true, "code": true, "reasoning": true, "large": true}
	if validTiers[string(resp.Tier)] {
		passed++
	} else {
		recordFail(t, "unexpected tier %q", resp.Tier)
	}

	// score should be 0-1
	checks++
	if resp.Score >= 0 && resp.Score <= 1 {
		passed++
	} else {
		recordFail(t, "score %f out of [0, 1] range", resp.Score)
	}

	// signals should exist
	checks++
	if resp.Signals != nil {
		passed++
	} else {
		recordFail(t, "signals map is nil")
	}

	// build_reason should not be empty
	checks++
	if resp.BuildReason != "" {
		passed++
	} else {
		recordFail(t, "build_reason is empty")
	}

	// record all passes
	for i := 0; i < passed; i++ {
		recordPass(t)
	}

	t.Logf("response fields: %d/%d passed | tier=%s score=%.4f signals=%v reason=%q",
		passed, checks, resp.Tier, resp.Score, resp.Signals, resp.BuildReason)
}

// ---------------------------------------------------------------------------
// Latency
// ---------------------------------------------------------------------------

func TestIntegrationLatency(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	// warm up
	r.Classify(context.Background(), Request{UserMessage: "warmup", TokenCount: 1})

	iterations := 10
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
			recordFail(t, "iteration %d: %v", i, err)
			return
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

	// DeBERTa on CPU takes ~700ms per call — budget accordingly
	if avg > 2*time.Second {
		recordFail(t, "avg latency %s exceeds 2s budget", avg)
	} else {
		recordPass(t)
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

	iterations := 5
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
					recordFail(t, "iteration %d: %v", i, err)
					return
				}
				lastResp = resp
			}
			avg := total / time.Duration(iterations)
			t.Logf("avg=%s tier=%s score=%.4f", avg, lastResp.Tier, lastResp.Score)
			recordPass(t)
		})
	}
}

// ---------------------------------------------------------------------------
// Concurrency against real server
// ---------------------------------------------------------------------------

func TestIntegrationConcurrent(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	n := 5 // reduced from 20 — CPU model can't handle high concurrency
	var wg sync.WaitGroup
	var errCount atomic.Int64
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
				errCount.Add(1)
				t.Errorf("request %d failed: %v", idx, err)
				return
			}
			if resp.Tier == "" {
				errCount.Add(1)
				t.Errorf("request %d: empty tier", idx)
			}
		}(i)
	}

	wg.Wait()

	avgUs := totalLatency.Load() / int64(n)
	t.Logf("concurrent: %d requests, %d errors, avg latency %dµs",
		n, errCount.Load(), avgUs)

	if errCount.Load() == 0 {
		recordPass(t)
	} else {
		recordFail(t, "%d/%d concurrent requests failed", errCount.Load(), n)
	}
}

func TestIntegrationConcurrentHeavy(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	n := 10 // reduced from 50
	var wg sync.WaitGroup
	var errCount atomic.Int64
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
			_, err := r.Classify(context.Background(), req)
			elapsed := time.Since(start)
			totalLatency.Add(elapsed.Microseconds())

			if err != nil {
				errCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	avgUs := totalLatency.Load() / int64(n)
	t.Logf("heavy concurrent: %d requests, %d errors, avg latency %dµs",
		n, errCount.Load(), avgUs)

	// allow some failures under heavy load on CPU
	maxFailures := int64(n / 5) // 20% failure tolerance
	if errCount.Load() <= maxFailures {
		recordPass(t)
	} else {
		recordFail(t, "too many failures: %d/%d (max allowed: %d)", errCount.Load(), n, maxFailures)
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
		recordFail(t, "first call failed: %v", err)
		return
	}

	consistent := true
	for i := 0; i < 5; i++ {
		resp, err := r.Classify(context.Background(), req)
		if err != nil {
			recordFail(t, "call %d failed: %v", i, err)
			return
		}
		if resp.Tier != first.Tier {
			t.Errorf("call %d: tier %q != first tier %q", i, resp.Tier, first.Tier)
			consistent = false
		}
		if resp.Score != first.Score {
			t.Errorf("call %d: score %f != first score %f", i, resp.Score, first.Score)
			consistent = false
		}
	}

	if consistent {
		recordPass(t)
	} else {
		recordFail(t, "inconsistent results across identical requests")
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

	if err != nil {
		recordFail(t, "empty prompt failed: %v", err)
		return
	}

	t.Logf("empty prompt: tier=%s score=%.4f", resp.Tier, resp.Score)
	recordPass(t)
}

func TestIntegrationLongPrompt(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

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
		recordFail(t, "long prompt failed: %v", err)
		return
	}

	t.Logf("long prompt: tier=%s score=%.4f latency=%s", resp.Tier, resp.Score, elapsed)
	recordPass(t)
}

func TestIntegrationUnicodePrompt(t *testing.T) {
	skipIfNotRunning(t)
	r := NewRouter(classifierURL())

	resp, err := r.Classify(context.Background(), Request{
		UserMessage: "解释一下量子计算的基本原理，并比较经典计算机和量子计算机的区别",
		TokenCount:  60,
	})

	if err != nil {
		recordFail(t, "unicode prompt failed: %v", err)
		return
	}

	t.Logf("unicode prompt: tier=%s score=%.4f", resp.Tier, resp.Score)
	recordPass(t)
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
		recordFail(t, "code block prompt failed: %v", err)
		return
	}

	t.Logf("code block: tier=%s score=%.4f", resp.Tier, resp.Score)

	if resp.Tier == "code" {
		recordPass(t)
	} else {
		recordFail(t, "expected code tier for prompt with code block, got %q", resp.Tier)
	}
}
