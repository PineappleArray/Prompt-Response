package proxy

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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

// newDeadDetectHandler returns a Handler with stream stall detection enabled.
// stallTimeout is exposed as a parameter so each test can choose a window
// short enough to keep tests fast while remaining well above scheduler jitter.
func newDeadDetectHandler(replicas []config.Replica, stallTimeout time.Duration) *Handler {
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
			MaxRetries: 2,
			Timeout:    30 * time.Second,
		},
		Stream: config.Stream{
			StallTimeout: stallTimeout,
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
	return New(scor, cls, cfg, cr, nil, nil)
}

// streamingBackend builds an httptest server that emits the given tokens with
// a per-token gap, then a [DONE] sentinel. Returns the server (caller closes).
func streamingBackend(tokens []string, gap time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, tok := range tokens {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"%s\"}}]}\n\n", tok)
			if flusher != nil {
				flusher.Flush()
			}
			if gap > 0 {
				time.Sleep(gap)
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
}

// TestDeadReplica_PreFirstByteReroute verifies that a replica which accepts a
// request but never emits a body byte is detected and the request is rerouted
// to a healthy replica before any data reaches the client.
func TestDeadReplica_PreFirstByteReroute(t *testing.T) {
	// Dead backend: writes 200 + headers but never sends a body.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Block until the client gives up. The peek goroutine cancels its
		// context, which closes this connection.
		<-r.Context().Done()
	}))
	defer dead.Close()

	healthy := streamingBackend([]string{"all", " good"}, 0)
	defer healthy.Close()

	replicas := []config.Replica{
		{ID: "dead", URL: dead.URL, Model: "test", Tier: types.TierSmall},
		{ID: "healthy", URL: healthy.URL, Model: "test", Tier: types.TierSmall},
	}
	h := newDeadDetectHandler(replicas, 80*time.Millisecond)

	payload := `{"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	w := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(w, req)
	elapsed := time.Since(start)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 after reroute, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "all") {
		t.Errorf("expected response from healthy replica, got %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("expected [DONE] terminator in response, got %s", w.Body.String())
	}
	// Reroute must be quicker than dead backend's full read timeout (would be
	// indefinite); sanity-cap at 5×stall_timeout.
	if elapsed > 2*time.Second {
		t.Errorf("reroute too slow: %v", elapsed)
	}
}

// TestDeadReplica_AllReplicasDeadReturns503 verifies that exhausting all
// replicas to pre-first-byte stalls produces the expected 503.
func TestDeadReplica_AllReplicasDeadReturns503(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer dead.Close()

	replicas := []config.Replica{
		{ID: "d1", URL: dead.URL, Model: "test", Tier: types.TierSmall},
		{ID: "d2", URL: dead.URL, Model: "test", Tier: types.TierSmall},
	}
	h := newDeadDetectHandler(replicas, 60*time.Millisecond)

	payload := `{"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when all replicas are dead, got %d", w.Code)
	}
}

// TestDeadReplica_MidStreamStall verifies that a replica which begins
// streaming but stalls before [DONE] is aborted by the watchdog. The client
// sees the partial stream that was already forwarded.
func TestDeadReplica_MidStreamStall(t *testing.T) {
	stall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		// Hold the connection open without further data — the router's
		// stall watchdog should cancel us.
		<-r.Context().Done()
	}))
	defer stall.Close()

	replicas := []config.Replica{
		{ID: "stall", URL: stall.URL, Model: "test", Tier: types.TierSmall},
	}
	h := newDeadDetectHandler(replicas, 100*time.Millisecond)

	payload := `{"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	w := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(w, req)
	elapsed := time.Since(start)

	if !strings.Contains(w.Body.String(), "partial") {
		t.Errorf("expected partial output forwarded before abort, got %q", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("must not see [DONE] from a stalled replica, got %q", w.Body.String())
	}
	// Watchdog should fire within ~stall_timeout × 1.25 + jitter.
	if elapsed > time.Second {
		t.Errorf("mid-stream watchdog too slow: %v", elapsed)
	}
}

// TestBurst_ConsistencyOfRepeatedQueries fires a burst of identical requests
// concurrently and verifies that every one terminates cleanly with the
// [DONE] sentinel — the central consistency guarantee for streaming.
func TestBurst_ConsistencyOfRepeatedQueries(t *testing.T) {
	const burst = 64

	var inflight, peak atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inflight.Add(1)
		defer inflight.Add(-1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, tok := range []string{"alpha", " beta", " gamma"} {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"%s\"}}]}\n\n", tok)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer backend.Close()

	replicas := []config.Replica{
		{ID: "r1", URL: backend.URL, Model: "test", Tier: types.TierSmall},
		{ID: "r2", URL: backend.URL, Model: "test", Tier: types.TierSmall},
	}
	h := newDeadDetectHandler(replicas, 5*time.Second)

	payload := `{"messages":[{"role":"user","content":"identical query"}]}`

	var wg sync.WaitGroup
	var failed atomic.Int32
	var doneCount atomic.Int32

	wg.Add(burst)
	startGate := make(chan struct{})
	for i := 0; i < burst; i++ {
		go func() {
			defer wg.Done()
			<-startGate
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				failed.Add(1)
				return
			}
			body := w.Body.String()
			if !strings.Contains(body, "[DONE]") {
				failed.Add(1)
				return
			}
			if !strings.Contains(body, "alpha") || !strings.Contains(body, "beta") || !strings.Contains(body, "gamma") {
				failed.Add(1)
				return
			}
			doneCount.Add(1)
		}()
	}
	close(startGate)
	wg.Wait()

	if got := failed.Load(); got != 0 {
		t.Errorf("burst consistency failure: %d/%d requests did not complete cleanly", got, burst)
	}
	if got := doneCount.Load(); int(got) != burst {
		t.Errorf("expected %d clean completions, got %d", burst, got)
	}
	if peak.Load() < 2 {
		t.Logf("note: peak concurrency was %d (low parallelism is acceptable but suspicious)", peak.Load())
	}
}

// TestLongQuery_ConsistencyMultipleRuns verifies that long streams (many
// tokens, mid-stream gaps) terminate cleanly across repeated runs, with no
// missed [DONE] sentinels.
func TestLongQuery_ConsistencyMultipleRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("long-query consistency test takes a few seconds")
	}
	const tokensPerStream = 40
	const tokenGap = 5 * time.Millisecond
	const runs = 16

	tokens := make([]string, tokensPerStream)
	for i := range tokens {
		tokens[i] = fmt.Sprintf("t%d", i)
	}

	backend := streamingBackend(tokens, tokenGap)
	defer backend.Close()

	replicas := []config.Replica{
		{ID: "r1", URL: backend.URL, Model: "test", Tier: types.TierSmall},
	}
	// Stall budget must comfortably exceed tokenGap.
	h := newDeadDetectHandler(replicas, 500*time.Millisecond)

	payload := `{"messages":[{"role":"user","content":"long query please"}]}`
	for i := 0; i < runs; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("run %d: expected 200, got %d", i, w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "[DONE]") {
			t.Errorf("run %d: missing [DONE] terminator", i)
		}
		// First and last tokens must both be present — a missed mid-stream
		// abort would truncate the tail.
		if !strings.Contains(body, tokens[0]) || !strings.Contains(body, tokens[len(tokens)-1]) {
			t.Errorf("run %d: stream truncated, body=%q", i, body)
		}
	}
}

// TestPromptToDoneLatencyDistribution drives many requests of varying length
// through the router and reports the empirical distribution of total latency
// from prompt-start to SSE [DONE]. This is the user-visible metric for the
// dead-replica detection feature: clean completions are timed end-to-end.
//
// The test asserts only that the distribution is well-formed (sane bounds and
// monotonic percentiles) so it does not become flaky under load.
func TestPromptToDoneLatencyDistribution(t *testing.T) {
	if testing.Short() {
		t.Skip("latency distribution test takes a few seconds")
	}

	// Per-request token counts spanning short → long.
	lengths := []int{4, 8, 16, 32, 64}
	const trialsPerLength = 8

	makeBackend := func(n int, gap time.Duration) *httptest.Server {
		toks := make([]string, n)
		for i := range toks {
			toks[i] = fmt.Sprintf("t%d", i)
		}
		return streamingBackend(toks, gap)
	}

	type sample struct {
		length  int
		elapsed time.Duration
	}
	var samples []sample

	for _, n := range lengths {
		backend := makeBackend(n, 2*time.Millisecond)
		replicas := []config.Replica{
			{ID: fmt.Sprintf("r-%d", n), URL: backend.URL, Model: "test", Tier: types.TierSmall},
		}
		h := newDeadDetectHandler(replicas, 500*time.Millisecond)
		payload := `{"messages":[{"role":"user","content":"latency probe"}]}`

		for i := 0; i < trialsPerLength; i++ {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
			w := httptest.NewRecorder()
			start := time.Now()
			h.ServeHTTP(w, req)
			elapsed := time.Since(start)
			if w.Code != http.StatusOK {
				t.Fatalf("len=%d trial=%d: status=%d", n, i, w.Code)
			}
			if !strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatalf("len=%d trial=%d: missing [DONE]", n, i)
			}
			samples = append(samples, sample{length: n, elapsed: elapsed})
		}
		backend.Close()
	}

	// Sort and compute percentiles across all samples — the distribution
	// shape we care about for end-to-end latency.
	sort.Slice(samples, func(i, j int) bool { return samples[i].elapsed < samples[j].elapsed })
	pct := func(p float64) time.Duration {
		idx := int(math.Round(p * float64(len(samples)-1)))
		return samples[idx].elapsed
	}
	p50, p90, p99 := pct(0.50), pct(0.90), pct(0.99)
	t.Logf("prompt→[DONE] distribution: n=%d  min=%v  p50=%v  p90=%v  p99=%v  max=%v",
		len(samples), samples[0].elapsed, p50, p90, p99, samples[len(samples)-1].elapsed)

	if p50 <= 0 {
		t.Errorf("p50 must be positive, got %v", p50)
	}
	if p99 < p50 {
		t.Errorf("p99 (%v) must be >= p50 (%v)", p99, p50)
	}
	if p99 > 30*time.Second {
		t.Errorf("p99 latency unreasonably large: %v", p99)
	}

	// Sanity: longer prompts should not be faster on average. Compute mean
	// per length and verify the longest length isn't the fastest.
	mean := func(ss []sample) time.Duration {
		var total time.Duration
		for _, s := range ss {
			total += s.elapsed
		}
		return total / time.Duration(len(ss))
	}
	byLen := map[int][]sample{}
	for _, s := range samples {
		byLen[s.length] = append(byLen[s.length], s)
	}
	shortMean := mean(byLen[lengths[0]])
	longMean := mean(byLen[lengths[len(lengths)-1]])
	t.Logf("mean latency: shortest=%v  longest=%v", shortMean, longMean)
	if longMean < shortMean/2 {
		t.Errorf("longest stream mean (%v) implausibly faster than shortest (%v)", longMean, shortMean)
	}
}
