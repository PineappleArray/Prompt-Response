package test

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers shared across new Poisson tests
// ---------------------------------------------------------------------------

// staticRouter always routes every request to the same model key. Used when a
// test wants to isolate one model's behaviour without keyword dispatch noise.
type staticRouter struct{ model string }

func (r *staticRouter) Route(_ Request) string { return r.model }

// apiModelProfile returns a ModelProfile for a simulated remote API provider.
// KVCachePerTokenMB=0 disables the KV cache check; the resulting maxKVCacheMB
// is also 0, but cacheNeeded=0 so the guard (0 > 0) never fires.
// MaxConcurrent=50 simulates the high concurrency available at remote API providers.
func apiModelProfile() ModelProfile {
	return ModelProfile{
		Name:              "claude-sonnet-api",
		PrefillTPS:        500,
		DecodeTPS:         100,
		MaxConcurrent:     50,
		KVCachePerTokenMB: 0,
	}
}

// mediumTierPromptPool returns requests representative of the new task types
// (Summarization, Extraction) that the classifier now routes to the medium tier.
func mediumTierPromptPool() []Request {
	return []Request{
		{Text: "summarize this article about climate change", InputTokens: 50, MaxOutputTokens: 150, IdealModel: "medium", Category: "summarization"},
		{Text: "give me a tldr of the quarterly earnings report", InputTokens: 60, MaxOutputTokens: 100, IdealModel: "medium", Category: "summarization"},
		{Text: "condense these meeting notes into key points", InputTokens: 80, MaxOutputTokens: 120, IdealModel: "medium", Category: "summarization"},
		{Text: "shorten this paragraph without losing meaning", InputTokens: 45, MaxOutputTokens: 80, IdealModel: "medium", Category: "summarization"},
		{Text: "extract all dates and names from this document", InputTokens: 70, MaxOutputTokens: 200, IdealModel: "medium", Category: "extraction"},
		{Text: "list all action items from the meeting notes", InputTokens: 65, MaxOutputTokens: 150, IdealModel: "medium", Category: "extraction"},
		{Text: "find all references to legal obligations in the contract", InputTokens: 90, MaxOutputTokens: 200, IdealModel: "medium", Category: "extraction"},
		{Text: "pull out every product name mentioned in the review", InputTokens: 55, MaxOutputTokens: 120, IdealModel: "medium", Category: "extraction"},
	}
}

// mixedNewTaskPool combines the medium-tier prompts with the existing small/code/reasoning
// pool to produce a realistic mixed load reflecting all task types after the classifier changes.
func mixedNewTaskPool() []Request {
	return append(mediumTierPromptPool(), poissonPromptPool()...)
}

// ---------------------------------------------------------------------------
// Medium tier — light load should all succeed and route correctly
// ---------------------------------------------------------------------------

func TestPoissonMediumTierLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping poisson medium-tier test in short mode")
	}

	models := mustLoadModels(t)
	if _, ok := models["medium"]; !ok {
		t.Fatal("medium model not found in testdata/model_profiles.json")
	}

	router := &KeywordRouter{}
	pool := mediumTierPromptPool()

	// 4 req/s × 32 requests ≈ 8s of arrivals. Medium model has max_concurrent=8
	// so this rate is well within capacity.
	const lambda = 4.0
	const n = 32
	res := RunPoissonBenchmark(models, router, pool, lambda, n, 11)

	if len(res.Results) != n {
		t.Fatalf("results = %d, want %d", len(res.Results), n)
	}

	// All prompts contain medium keywords; every successful request must go to "medium".
	for _, r := range res.Results {
		if r.Err == "" && r.RoutedTo != "medium" {
			t.Errorf("expected route=medium, got %q (text=%q)", r.RoutedTo, r.RequestText)
		}
	}

	errs := res.Errors()
	if len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("unexpected error: routed=%s text=%q reason=%s", e.RoutedTo, e.RequestText, e.Err)
		}
	}

	accuracy := res.Accuracy()
	if accuracy < 0.90 {
		t.Errorf("accuracy %.1f%% below 90%% — medium-tier prompts should route to medium", accuracy*100)
	}

	lat := steadyStateLatencyMS(&res, "medium")
	t.Logf("medium tier: lambda=%.1f/s n=%d wall=%s accuracy=%.1f%% errors=%d",
		lambda, n, res.WallTime.Round(time.Millisecond), accuracy*100, len(errs))
	if len(lat) > 0 {
		t.Logf("  p50=%.0fms p95=%.0fms p99=%.0fms", Percentile(lat, 50), Percentile(lat, 95), Percentile(lat, 99))
	}
}

// ---------------------------------------------------------------------------
// Medium tier — high load should provoke capacity errors
// ---------------------------------------------------------------------------

// TestPoissonMediumTierHighLoad verifies backpressure from the medium model
// (max_concurrent=8). At λ=25 the arrival rate far exceeds drain rate, so a
// meaningful fraction of requests must be rejected.
func TestPoissonMediumTierHighLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping poisson medium-tier high-load in short mode")
	}

	models := mustLoadModels(t)
	router := &staticRouter{"medium"}

	pool := []Request{
		{Text: "summarize this lengthy report", InputTokens: 200, MaxOutputTokens: 300, IdealModel: "medium", Category: "summarization"},
		{Text: "extract all action items from the document", InputTokens: 180, MaxOutputTokens: 250, IdealModel: "medium", Category: "extraction"},
	}

	const lambda = 25.0
	const n = 50
	res := RunPoissonBenchmark(models, router, pool, lambda, n, 77)

	succ := res.Successes()
	errs := res.Errors()
	errRate := steadyStateErrorRate(&res)

	t.Logf("medium high-load: lambda=%.0f/s n=%d wall=%s success=%d errors=%d steady_err_rate=%.1f%%",
		lambda, n, res.WallTime.Round(time.Millisecond), len(succ), len(errs), errRate*100)

	if len(errs) == 0 {
		t.Error("expected capacity errors under high load on medium model (max_concurrent=8); got none")
	}

	if len(succ) == 0 {
		t.Error("expected at least some successes; got zero — model may be misconfigured")
	}

	if errRate < 0.25 {
		t.Errorf("steady-state error rate %.1f%% below 25%%; expected >25%% at lambda=%.0f on medium model",
			errRate*100, lambda)
	}
}

// ---------------------------------------------------------------------------
// API model — no KV cache pressure under heavy token load
// ---------------------------------------------------------------------------

// TestPoissonAPIModelNoOOM verifies that an API-provider mock (KVCachePerTokenMB=0)
// never hits KV cache exhaustion regardless of token sizes. This reflects the
// scorer change that omits cache pressure scoring for API replicas: those
// replicas handle prefix caching internally, so the router should not reject
// them based on a metric it never observes.
func TestPoissonAPIModelNoOOM(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping poisson API OOM test in short mode")
	}

	api := NewMockModel("api", apiModelProfile())
	models := map[string]*MockModel{"api": api}
	router := &staticRouter{"api"}

	// Large input tokens that would exhaust KV cache on vLLM models. The API
	// mock must accept them all without error.
	pool := []Request{
		{Text: "summarize this report", InputTokens: 2000, MaxOutputTokens: 400, IdealModel: "api", Category: "api"},
		{Text: "extract key entities from this passage", InputTokens: 3000, MaxOutputTokens: 500, IdealModel: "api", Category: "api"},
		{Text: "condense this transcript to bullet points", InputTokens: 1500, MaxOutputTokens: 300, IdealModel: "api", Category: "api"},
	}

	const lambda = 12.0
	const n = 36
	res := RunPoissonBenchmark(models, router, pool, lambda, n, 55)

	for _, e := range res.Errors() {
		if strings.Contains(e.Err, "OOM") || strings.Contains(e.Err, "kv cache") {
			t.Errorf("API model should never OOM; got: %s", e.Err)
		}
	}

	successRate := float64(len(res.Successes())) / float64(n)
	t.Logf("api model no-OOM: n=%d wall=%s successes=%d (%.0f%%) errors=%d",
		n, res.WallTime.Round(time.Millisecond), len(res.Successes()), successRate*100, len(res.Errors()))

	// At λ=12 with max_concurrent=50 the API model should handle nearly all requests.
	if successRate < 0.80 {
		t.Errorf("success rate %.1f%% below 80%%; API model may be misconfigured", successRate*100)
	}
}

// ---------------------------------------------------------------------------
// Mixed new task types — full pool at moderate rate
// ---------------------------------------------------------------------------

// TestPoissonNewTaskTypeMix fires a pool that includes all new task types
// (Summarization, Extraction, Text Generation, Dialogue) alongside the existing
// small/code/reasoning categories. The KeywordRouter's medium keywords catch
// summarize/extract prompts; the rest fall through to existing routing.
func TestPoissonNewTaskTypeMix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping poisson new-task-type mix in short mode")
	}

	models := mustLoadModels(t)
	router := &KeywordRouter{}
	pool := mixedNewTaskPool()

	const lambda = 6.0
	const n = 60
	res := RunPoissonBenchmark(models, router, pool, lambda, n, 333)

	errRate := steadyStateErrorRate(&res)
	accuracy := res.Accuracy()

	t.Logf("mixed-task: lambda=%.1f/s n=%d wall=%s arrival=%.2f/s accuracy=%.1f%% err_rate=%.1f%%",
		lambda, n, res.WallTime.Round(time.Millisecond), res.ArrivalRate(), accuracy*100, errRate*100)

	for _, key := range []string{"small", "code", "medium", "reasoning"} {
		lat := steadyStateLatencyMS(&res, key)
		if len(lat) == 0 {
			continue
		}
		t.Logf("  [%s] n=%d p50=%.0fms p95=%.0fms", key, len(lat), Percentile(lat, 50), Percentile(lat, 95))
	}

	// At λ=6 with a diverse pool the fleet should handle most requests.
	if errRate > 0.30 {
		t.Errorf("steady-state error rate %.1f%% > 30%% at lambda=%.1f on mixed pool", errRate*100, lambda)
	}

	// Medium tier should receive some traffic from the summarization/extraction prompts.
	mediumCount := 0
	for _, r := range res.Successes() {
		if r.RoutedTo == "medium" {
			mediumCount++
		}
	}
	if mediumCount == 0 {
		t.Error("expected some requests to route to medium tier; got none — medium keywords may not be routing correctly")
	}
	t.Logf("  medium tier received %d/%d successful requests", mediumCount, len(res.Successes()))
}

// ---------------------------------------------------------------------------
// Rate sweep including medium tier
// ---------------------------------------------------------------------------

// TestPoissonMediumRateSweep sweeps λ across the medium model's capacity
// range. This mirrors TestPoissonRateSweep but focuses exclusively on the
// medium tier so its latency profile under increasing load is visible.
func TestPoissonMediumRateSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping poisson medium rate sweep in short mode")
	}

	models := mustLoadModels(t)
	router := &staticRouter{"medium"}

	pool := []Request{
		{Text: "summarize the following article", InputTokens: 100, MaxOutputTokens: 200, IdealModel: "medium", Category: "summarization"},
		{Text: "extract key entities from this text", InputTokens: 120, MaxOutputTokens: 180, IdealModel: "medium", Category: "extraction"},
	}

	lambdas := []float64{2, 4, 8, 16}
	t.Logf("%-10s %-6s %-10s %-10s %-8s %-10s %-10s",
		"lambda", "n", "wall", "arrival/s", "err%", "p50ms", "p99ms")

	for _, lam := range lambdas {
		n := int(lam * 8)
		if n < 16 {
			n = 16
		}
		res := RunPoissonBenchmark(models, router, pool, lam, n, 888)

		lat := steadyStateLatencyMS(&res, "medium")
		errPct := steadyStateErrorRate(&res) * 100
		p50, p99 := 0.0, 0.0
		if len(lat) > 0 {
			p50 = Percentile(lat, 50)
			p99 = Percentile(lat, 99)
		}
		t.Logf("%-10.1f %-6d %-10s %-10.2f %-8.1f %-10.0f %-10.0f",
			lam, n, res.WallTime.Round(time.Millisecond), res.ArrivalRate(), errPct, p50, p99)
	}

	// Sanity: at λ=2 the medium model (max_concurrent=8) should have near-zero errors.
	low := RunPoissonBenchmark(models, router, pool, 2.0, 16, 888)
	if errRate := steadyStateErrorRate(&low); errRate > 0.05 {
		t.Errorf("medium lambda=2 error rate %.1f%% > 5%%; expected near-zero at light load", errRate*100)
	}
}
