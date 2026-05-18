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

			// With 20k samples we expect <5% error on both moments.
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
// Poisson load — light traffic should all succeed
// ---------------------------------------------------------------------------

func poissonPromptPool() []Request {
	// A diverse pool covering all four routing tiers so the harness exercises
	// every model in the mock fleet. Inputs are small so wall-clock stays low.
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

func TestPoissonLightLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping poisson load in short mode")
	}

	models := mustLoadModels(t)
	router := &KeywordRouter{}
	pool := poissonPromptPool()

	// 5 req/s for 30 requests ≈ 6s of wall clock; well within capacity.
	res := RunPoissonBenchmark(models, router, pool, 5.0, 30, 1)

	if len(res.Results) != 30 {
		t.Fatalf("results = %d, want 30", len(res.Results))
	}

	errs := res.Errors()
	if len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("unexpected error: routed=%s text=%q reason=%s", e.RoutedTo, e.RequestText, e.Err)
		}
	}

	lat := res.LatencyMS("")
	t.Logf("lambda=%.1f/s wall=%s arrival_window=%s arrival_rate=%.2f/s observed=%.2f/s n=%d errors=%d",
		res.Lambda, res.WallTime.Round(time.Millisecond), res.ArrivalWindow.Round(time.Millisecond),
		res.ArrivalRate(), res.ObservedRate(), len(res.Results), len(errs))
	t.Logf("latency p50=%.0fms p95=%.0fms p99=%.0fms",
		Percentile(lat, 50), Percentile(lat, 95), Percentile(lat, 99))

	// Realized arrival rate should track lambda within ±40% with n=30. We
	// don't assert on ObservedRate because wall time is biased by the latency
	// of the last in-flight request.
	rate := res.ArrivalRate()
	if rate < res.Lambda*0.6 || rate > res.Lambda*1.4 {
		t.Errorf("arrival rate %.2f/s far from target %.1f/s", rate, res.Lambda)
	}
}

// ---------------------------------------------------------------------------
// Poisson load — high λ should provoke capacity errors on the large model
// ---------------------------------------------------------------------------

func TestPoissonHighLoadProvokesCapacity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping poisson high-load in short mode")
	}

	models := mustLoadModels(t)
	router := &KeywordRouter{}
	// All prompts route to "large" (max_concurrent=4) with seconds-long
	// generation; firing at 20/s will saturate it and trigger capacity errors.
	// Outputs are sized so a successful run takes ~5s, keeping wall time small.
	pool := []Request{
		{Text: "write a comprehensive research paper", InputTokens: 100, MaxOutputTokens: 150, IdealModel: "large", Category: "writing"},
		{Text: "produce an in depth review", InputTokens: 100, MaxOutputTokens: 150, IdealModel: "large", Category: "writing"},
	}

	res := RunPoissonBenchmark(models, router, pool, 20.0, 30, 2)

	succ := res.Successes()
	errs := res.Errors()
	t.Logf("lambda=%.0f/s wall=%s success=%d errors=%d",
		res.Lambda, res.WallTime.Round(time.Millisecond), len(succ), len(errs))

	if len(errs) == 0 {
		t.Error("expected capacity errors under high Poisson load; got none — large model may be over-provisioned for this test")
	}
}

// ---------------------------------------------------------------------------
// Poisson load — broad spectrum sweep across lambdas
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
		// Cap request count so wall clock stays bounded — 4 seconds of traffic.
		n := int(lam * 4)
		if n < 10 {
			n = 10
		}
		res := RunPoissonBenchmark(models, router, pool, lam, n, 1234)

		lat := res.LatencyMS("")
		errPct := 0.0
		if len(res.Results) > 0 {
			errPct = float64(len(res.Errors())) / float64(len(res.Results)) * 100
		}
		t.Logf("%-10.1f %-6d %-10s %-10.2f %-8.1f %-10.0f %-10.0f",
			lam, n, res.WallTime.Round(time.Millisecond), res.ArrivalRate(), errPct,
			Percentile(lat, 50), Percentile(lat, 99))
	}
}

// ---------------------------------------------------------------------------
// Poisson + MT-Bench — realistic prompt mix (skipped when dataset missing)
// ---------------------------------------------------------------------------

func TestPoissonMTBench(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping poisson MT-Bench in short mode")
	}

	requests := mustLoadMTBench(t)
	models := mustLoadModels(t)
	router := &KeywordRouter{}

	res := RunPoissonBenchmark(models, router, requests, 10.0, 80, 99)

	t.Logf("lambda=%.1f/s n=%d wall=%s arrival_rate=%.2f/s accuracy=%.1f%% errors=%d",
		res.Lambda, res.RequestCount, res.WallTime.Round(time.Millisecond),
		res.ArrivalRate(), res.Accuracy()*100, len(res.Errors()))

	for _, key := range []string{"small", "code", "reasoning", "large"} {
		lat := res.LatencyMS(key)
		if len(lat) == 0 {
			continue
		}
		t.Logf("  [%s] n=%d p50=%.0fms p95=%.0fms p99=%.0fms",
			key, len(lat), Percentile(lat, 50), Percentile(lat, 95), Percentile(lat, 99))
	}
}

// ---------------------------------------------------------------------------
// Go-style benchmarks (run with `go test -bench`)
// ---------------------------------------------------------------------------

// BenchmarkPoissonArrivals measures the cost of generating an arrival schedule
// — purely CPU-bound, no I/O. Useful to catch regressions in the inverse-CDF
// path. Reports ns/op and allocs/op.
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

// BenchmarkKeywordRouterRoute measures the hot-path Route() call on the
// baseline router for a fixed prompt mix. The classifier and scorer are
// declared zero-allocation in CLAUDE.md, so this benchmark protects that
// invariant for the KeywordRouter too.
func BenchmarkKeywordRouterRoute(b *testing.B) {
	router := &KeywordRouter{}
	pool := poissonPromptPool()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = router.Route(pool[i%len(pool)])
	}
}
