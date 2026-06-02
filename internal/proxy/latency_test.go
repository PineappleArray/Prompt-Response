package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"prompt-response/internal/circuit"
	"prompt-response/internal/classifier"
	"prompt-response/internal/config"
	"prompt-response/internal/poller"
	"prompt-response/internal/scorer"
	"prompt-response/internal/store"
	"prompt-response/internal/types"
)

// newLatencyHandler builds a Handler wired to the in-process classifier (the
// production default) so the measured latency reflects the real routing path
// with no external classifier hop.
func newLatencyHandler(replicas []config.Replica) *Handler {
	mem := store.NewMemory()
	poll := poller.New(replicas, 0)
	cfg := &config.Config{
		Replicas:    replicas,
		Weights:     config.Weights{CacheAffinity: 0.5, QueueDepth: 0.25, KVCachePressure: 0.15, Baseline: 0.10},
		AffinityTTL: 5 * time.Minute,
		MaxQueue:    20,
		Threshold:   0.35,
		Circuit:     config.Circuit{ErrorThreshold: 0.5, WindowSize: 10 * time.Second, Cooldown: 30 * time.Second, MinSamples: 5},
		Retry:       config.Retry{MaxRetries: 1, Timeout: 30 * time.Second},
	}
	scor := scorer.New(replicas, mem, poll, cfg.Weights, cfg.AffinityTTL, cfg.MaxQueue)
	cr := circuit.NewRegistry(circuit.Config{ErrorThreshold: 0.5, WindowSize: 10 * time.Second, Cooldown: 30 * time.Second, MinSamples: 5})
	return New(scor, classifier.NewLocalClassifier(), cfg, cr, nil, nil)
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p / 100 * float64(len(sorted)-1))
	return sorted[idx]
}

// TestRoutingLatency measures end-to-end routing latency over many requests and
// asserts p50/p99 stay far under the budget the in-process classifier was meant
// to recover (the former Python hop pushed p50≈5s / p99≈10s). With local
// classification the router overhead is sub-millisecond; we assert generous
// ceilings so the test guards against a real regression without being flaky.
func TestRoutingLatency(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, tok := range []string{"Hello", " from", " the", " router"} {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"%s\"}}]}\n\n", tok)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer backend.Close()

	replicas := []config.Replica{
		{ID: "r1", URL: backend.URL, Model: "test", Tier: types.TierSmall},
		{ID: "r2", URL: backend.URL, Model: "test", Tier: types.TierReasoning},
	}
	h := newLatencyHandler(replicas)

	const n = 300
	payload := `{"messages":[{"role":"user","content":"explain why the sky is blue in one sentence"}]}`

	latencies := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
		w := httptest.NewRecorder()
		start := time.Now()
		h.ServeHTTP(w, req)
		latencies = append(latencies, time.Since(start))

		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status %d", i, w.Code)
		}
		if !strings.Contains(w.Body.String(), "[DONE]") {
			t.Fatalf("request %d: missing [DONE]", i)
		}
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := percentile(latencies, 50)
	p99 := percentile(latencies, 99)
	t.Logf("routing latency over %d requests: p50=%s p99=%s max=%s", n, p50, p99, latencies[len(latencies)-1])

	if p50 > 500*time.Millisecond {
		t.Errorf("p50 latency %s exceeds 500ms budget", p50)
	}
	if p99 > 2*time.Second {
		t.Errorf("p99 latency %s exceeds 2s budget", p99)
	}
}

// TestClassifierLatencyIsLocal asserts the classification step itself is
// sub-millisecond — the core of the p50/p99 reduction, since it replaced a
// multi-second-timeout network call.
func TestClassifierLatencyIsLocal(t *testing.T) {
	c := classifier.NewLocalClassifier()
	req := classifier.Request{
		UserMessage: "Compare optimistic and pessimistic locking and explain the tradeoffs.",
		TokenCount:  16,
	}

	const n = 1000
	start := time.Now()
	for i := 0; i < n; i++ {
		if _, err := c.Classify(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	avg := time.Since(start) / n
	t.Logf("avg in-process classify latency: %s", avg)
	if avg > time.Millisecond {
		t.Errorf("classify averaged %s, expected sub-millisecond", avg)
	}
}
