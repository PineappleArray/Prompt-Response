package test

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// PoissonArrivals — distribution properties
// ---------------------------------------------------------------------------

func TestPoissonArrivalsMeanAndVariance(t *testing.T) {
	cases := []struct {
		name   string
		lambda float64
		n      int
	}{
		{"lambda=10/s", 10, 20000},
		{"lambda=100/s", 100, 20000},
		{"lambda=1000/s", 1000, 20000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(42))
			gaps := PoissonArrivals(tc.lambda, tc.n, rng)
			stats := SummarizeArrivals(gaps)

			// For Exp(lambda): mean = 1/lambda, variance = 1/lambda^2.
			wantMean := 1.0 / tc.lambda
			wantVar := 1.0 / (tc.lambda * tc.lambda)

			meanErr := math.Abs(stats.MeanSec-wantMean) / wantMean
			varErr := math.Abs(stats.VarianceS-wantVar) / wantVar

			if meanErr > 0.05 {
				t.Errorf("mean %.6g vs want %.6g (err %.2f%%)", stats.MeanSec, wantMean, meanErr*100)
			}
			if varErr > 0.10 {
				t.Errorf("variance %.6g vs want %.6g (err %.2f%%)", stats.VarianceS, wantVar, varErr*100)
			}
			t.Logf("n=%d mean=%.6gs (want %.6g) var=%.6g (want %.6g) min=%.6g max=%.6g",
				stats.N, stats.MeanSec, wantMean, stats.VarianceS, wantVar, stats.MinSec, stats.MaxSec)
		})
	}
}

func TestPoissonArrivalsReproducible(t *testing.T) {
	a := PoissonArrivals(50, 100, rand.New(rand.NewSource(7)))
	b := PoissonArrivals(50, 100, rand.New(rand.NewSource(7)))
	if len(a) != len(b) {
		t.Fatalf("lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("gap %d differs: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestPoissonArrivalsPanicsOnBadLambda(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for lambda <= 0")
		}
	}()
	PoissonArrivals(0, 10, rand.New(rand.NewSource(1)))
}

// ---------------------------------------------------------------------------
// Helpers — steady-state measurement after warmup trimming
// ---------------------------------------------------------------------------

// warmupFraction is the proportion of initial results discarded before
// measuring steady-state metrics. The first requests experience cold-start
// effects (goroutine scheduling, timer resolution) that bias measurements.
const warmupFraction = 0.20

// steadyStateSlice returns the results after discarding the warmup window.
func steadyStateSlice(results []PoissonResult) []PoissonResult {
	skip := int(float64(len(results)) * warmupFraction)
	if skip >= len(results) {
		return results
	}
	return results[skip:]
}

// steadyStateLatencyMS returns latency values (ms) for steady-state successes,
// optionally filtered by routed-to model key. Pass "" for all models.
func steadyStateLatencyMS(res *PoissonBenchmarkResults, modelKey string) []float64 {
	steady := steadyStateSlice(res.Results)
	var out []float64
	for _, r := range steady {
		if r.Err != "" {
			continue
		}
		if modelKey != "" && r.RoutedTo != modelKey {
			continue
		}
		out = append(out, r.Latency.Seconds()*1000)
	}
	return out
}

// steadyStateErrorRate returns the error fraction among steady-state results.
func steadyStateErrorRate(res *PoissonBenchmarkResults) float64 {
	steady := steadyStateSlice(res.Results)
	if len(steady) == 0 {
		return 0
	}
	errs := 0
	for _, r := range steady {
		if r.Err != "" {
			errs++
		}
	}
	return float64(errs) / float64(len(steady))
}

// ---------------------------------------------------------------------------
// Prompt pool
// ---------------------------------------------------------------------------

func poissonPromptPool() []Request {
	return []Request{
		{Text: "hi", InputTokens: 5, MaxOutputTokens: 10, IdealModel: "small", Category: "extraction"},
		{Text: "what is 2+2", InputTokens: 8, MaxOutputTokens: 15, IdealModel: "small", Category: "extraction"},
		{Text: "define gravity", InputTokens: 10, MaxOutputTokens: 30, IdealModel: "small", Category: "extraction"},
		{Text: "implement a quicksort function", InputTokens: 30, MaxOutputTokens: 200, IdealModel: "code", Category: "coding"},
		{Text: "debug this parser script", InputTokens: 40, MaxOutputTokens: 250, IdealModel: "code", Category: "coding"},
		{Text: "refactor the api endpoint handler", InputTokens: 35, MaxOutputTokens: 200, IdealModel: "code", Category: "coding"},
		{Text: "explain how transformers work step by step", InputTokens: 25, MaxOutputTokens: 300, IdealModel: "reasoning", Category: "reasoning"},
		{Text: "why does entropy always increase, analyze pros and cons", InputTokens: 30, MaxOutputTokens: 300, IdealModel: "reasoning", Category: "reasoning"},
		{Text: "compare TCP and UDP, reason through tradeoffs", InputTokens: 25, MaxOutputTokens: 250, IdealModel: "reasoning", Category: "reasoning"},
	}
}

// ---------------------------------------------------------------------------
// Poisson load — light traffic should all succeed
// ---------------------------------------------------------------------------

func TestPoissonLightLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping poisson load in short mode")
	}

	models := mustLoadModels(t)
	router := &KeywordRouter{}
	pool := poissonPromptPool()

	// 5 req/s × 60 requests ≈ 12s of arrivals. Well within capacity, so
	// every request should succeed. After 20% warmup trim we still have
	// ~48 steady-state samples.
	const lambda = 5.0
	const n = 60
	res := RunPoissonBenchmark(models, router, pool, lambda, n, 1)

	if len(res.Results) != n {
		t.Fatalf("results = %d, want %d", len(res.Results), n)
	}

	errs := res.Errors()
	if len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("unexpected error: routed=%s text=%q reason=%s", e.RoutedTo, e.RequestText, e.Err)
		}
	}

	// Arrival rate should track lambda within ±30%. ArrivalRate divides n
	// by the span from first to last scheduled arrival, so with exponential
	// gaps the tail can compress or stretch the window. ±30% accommodates
	// that variance at n=60 without being meaninglessly loose.
	rate := res.ArrivalRate()
	if rate < lambda*0.70 || rate > lambda*1.30 {
		t.Errorf("arrival rate %.2f/s outside ±30%% of target %.1f/s", rate, lambda)
	}

	lat := steadyStateLatencyMS(&res, "")
	t.Logf("lambda=%.1f/s n=%d wall=%s arrival_rate=%.2f/s errors=%d",
		lambda, n, res.WallTime.Round(time.Millisecond), rate, len(errs))
	if len(lat) > 0 {
		t.Logf("steady-state latency (after %.0f%% warmup): p50=%.0fms p95=%.0fms p99=%.0fms",
			warmupFraction*100, Percentile(lat, 50), Percentile(lat, 95), Percentile(lat, 99))
	}
}

// ---------------------------------------------------------------------------
// Poisson load — high λ should provoke capacity errors
// ---------------------------------------------------------------------------

// TestPoissonHighLoadProvokesCapacity fires requests at a rate that exceeds
// the "large" model's max_concurrent slots. We expect the backend to reject
// a significant fraction. This validates that backpressure propagates — if
// zero errors appear, the concurrency limit isn't biting.
func TestPoissonHighLoadProvokesCapacity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping poisson high-load in short mode")
	}

	models := mustLoadModels(t)
	router := &KeywordRouter{}

	// All prompts route to "large" (max_concurrent=4). At λ=20 the arrival
	// rate far exceeds drain rate, so requests pile up and hit capacity.
	pool := []Request{
		{Text: "write a comprehensive research paper", InputTokens: 100, MaxOutputTokens: 150, IdealModel: "large", Category: "writing"},
		{Text: "produce an in depth review", InputTokens: 100, MaxOutputTokens: 150, IdealModel: "large", Category: "writing"},
	}

	const lambda = 20.0
	const n = 40
	res := RunPoissonBenchmark(models, router, pool, lambda, n, 2)

	succ := res.Successes()
	errs := res.Errors()
	errRate := steadyStateErrorRate(&res)

	t.Logf("lambda=%.0f/s n=%d wall=%s success=%d errors=%d steady_state_err_rate=%.1f%%",
		lambda, n, res.WallTime.Round(time.Millisecond), len(succ), len(errs), errRate*100)

	if len(errs) == 0 {
		t.Error("expected capacity errors under high Poisson load; got none — " +
			"large model may be over-provisioned for this test")
	}

	// At least some requests should succeed — the first arrivals fit within
	// max_concurrent before saturation kicks in.
	if len(succ) == 0 {
		t.Error("expected at least some successes; got zero — " +
			"model may be misconfigured or immediately rejecting")
	}

	// Steady-state error rate should be substantial to confirm backpressure.
	if errRate < 0.30 {
		t.Errorf("steady-state error rate %.1f%% too low; expected >30%% "+
			"under sustained overload at lambda=%.0f", errRate*100, lambda)
	}
}

// ---------------------------------------------------------------------------
// Poisson load — rate sweep across lambdas
// ---------------------------------------------------------------------------

func TestPoissonRateSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping poisson rate sweep in short mode")
	}

	models := mustLoadModels(t)
	router := &KeywordRouter{}
	pool := poissonPromptPool()

	lambdas := []float64{2, 5, 10, 20}
	t.Logf("%-10s %-6s %-10s %-10s %-8s %-10s %-10s",
		"lambda", "n", "wall", "arrival/s", "err%", "p50ms", "p99ms")

	for _, lam := range lambdas {
		// 10 seconds of traffic per lambda. Longer windows produce stabler
		// rate estimates and leave a useful window after warmup trimming.
		n := int(lam * 10)
		if n < 20 {
			n = 20
		}

		res := RunPoissonBenchmark(models, router, pool, lam, n, 1234)

		lat := steadyStateLatencyMS(&res, "")
		errPct := steadyStateErrorRate(&res) * 100

		p50, p99 := 0.0, 0.0
		if len(lat) > 0 {
			p50 = Percentile(lat, 50)
			p99 = Percentile(lat, 99)
		}

		t.Logf("%-10.1f %-6d %-10s %-10.2f %-8.1f %-10.0f %-10.0f",
			lam, n, res.WallTime.Round(time.Millisecond), res.ArrivalRate(), errPct, p50, p99)
	}

	// Sanity check: at λ=2 (well under capacity) errors should be near zero.
	low := RunPoissonBenchmark(models, router, pool, 2.0, 20, 1234)
	if errRate := steadyStateErrorRate(&low); errRate > 0.05 {
		t.Errorf("lambda=2 steady-state error rate %.1f%% > 5%%; "+
			"expected near-zero errors at light load", errRate*100)
	}
}

// ---------------------------------------------------------------------------
// Poisson + MT-Bench — realistic prompt mix
// ---------------------------------------------------------------------------

// TestPoissonMTBench runs MT-Bench prompts through the Poisson harness at a
// moderate arrival rate. The KeywordRouter is a simple baseline so accuracy
// is expected to be low (~25-45%). This test establishes a floor — if accuracy
// drops below 20%, routing logic is likely broken. The primary value is the
// per-category latency profile under realistic load.
func TestPoissonMTBench(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping poisson MT-Bench in short mode")
	}

	requests := mustLoadMTBench(t)
	models := mustLoadModels(t)
	router := &KeywordRouter{}

	// 10 req/s × 100 requests = ~10s of arrivals. Enough for stable metrics
	// after warmup trimming.
	const lambda = 10.0
	const n = 100
	res := RunPoissonBenchmark(models, router, requests, lambda, n, 99)

	accuracy := res.Accuracy()
	errCount := len(res.Errors())
	errRate := steadyStateErrorRate(&res)

	t.Logf("lambda=%.1f/s n=%d wall=%s arrival_rate=%.2f/s",
		lambda, n, res.WallTime.Round(time.Millisecond), res.ArrivalRate())
	t.Logf("accuracy=%.1f%% total_errors=%d steady_state_err_rate=%.1f%%",
		accuracy*100, errCount, errRate*100)

	for _, key := range []string{"small", "code", "reasoning", "large"} {
		lat := steadyStateLatencyMS(&res, key)
		if len(lat) == 0 {
			continue
		}
		t.Logf("  [%s] n=%d p50=%.0fms p95=%.0fms p99=%.0fms",
			key, len(lat), Percentile(lat, 50), Percentile(lat, 95), Percentile(lat, 99))
	}

	// Accuracy floor: keyword router should beat random chance (~25% across
	// 4 tiers). Below 20% means routing is broken, not just imprecise.
	if accuracy < 0.20 {
		t.Errorf("accuracy %.1f%% below 20%% floor — routing may be broken", accuracy*100)
	}

	// Error rate: at λ=10 with heavier MT-Bench prompts, capacity errors
	// are expected — longer generation times saturate the fleet. Allow up
	// to 65%; above that the fleet is genuinely undersized.
	if errRate > 0.65 {
		t.Errorf("steady-state error rate %.1f%% exceeds 65%% — "+
			"fleet may be undersized for MT-Bench at lambda=%.0f", errRate*100, lambda)
	}
}

// ---------------------------------------------------------------------------
// Go-style benchmarks (run with `go test -bench`)
// ---------------------------------------------------------------------------

func BenchmarkPoissonArrivals(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			rng := rand.New(rand.NewSource(1))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				PoissonArrivals(100, n, rng)
			}
		})
	}
}

func BenchmarkKeywordRouterRoute(b *testing.B) {
	router := &KeywordRouter{}
	pool := poissonPromptPool()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = router.Route(pool[i%len(pool)])
	}
}
