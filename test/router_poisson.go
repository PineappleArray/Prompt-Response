package test

import (
	"math"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// PoissonArrivals returns inter-arrival deltas (in time.Duration) for a Poisson
// process with mean rate `lambda` events/second. The returned slice has length
// `count`; element i is the gap between event i-1 and event i (element 0 is the
// gap from t=0 to the first event). Inter-arrival times follow Exp(lambda), so
// the cumulative sum yields a Poisson-process schedule.
//
// rng must be non-nil. Callers seeding their own *rand.Rand makes the schedule
// reproducible. lambda must be > 0; values <= 0 panic — a non-positive rate
// has no meaning for a Poisson process.
func PoissonArrivals(lambda float64, count int, rng *rand.Rand) []time.Duration {
	if lambda <= 0 {
		panic("PoissonArrivals: lambda must be > 0")
	}
	out := make([]time.Duration, count)
	for i := range out {
		// Inverse-CDF of Exp(lambda): -ln(1-U)/lambda. We use rng.Float64()
		// which returns [0,1); 1-U avoids ln(0) when U==0.
		u := rng.Float64()
		gap := -math.Log(1.0-u) / lambda
		out[i] = time.Duration(gap * float64(time.Second))
	}
	return out
}

// PoissonResult captures one request's outcome under a Poisson arrival schedule.
// Latency is wall-clock from dispatch to completion; QueueWait is the delta
// between the scheduled arrival and dispatch (positive when the scheduler fell
// behind the schedule, ~0 when on time).
type PoissonResult struct {
	Scheduled   time.Duration
	Dispatched  time.Duration
	Completed   time.Duration
	Latency     time.Duration
	QueueWait   time.Duration
	RoutedTo    string
	IdealModel  string
	IsCorrect   bool
	Category    string
	QuestionID  int
	RequestText string
	Err         string
}

// PoissonBenchmarkResults aggregates a Poisson load run.
//
// ArrivalWindow is the time from t=0 to the last scheduled arrival; it is a
// better proxy than WallTime for steady-state throughput because WallTime
// includes the latency of the final in-flight request.
type PoissonBenchmarkResults struct {
	Lambda        float64
	WallTime      time.Duration
	ArrivalWindow time.Duration
	RequestCount  int
	Results       []PoissonResult
}

// Successes returns results that completed without error.
func (br PoissonBenchmarkResults) Successes() []PoissonResult {
	out := make([]PoissonResult, 0, len(br.Results))
	for _, r := range br.Results {
		if r.Err == "" {
			out = append(out, r)
		}
	}
	return out
}

// Errors returns results that failed (model at capacity, OOM, unknown route).
func (br PoissonBenchmarkResults) Errors() []PoissonResult {
	var out []PoissonResult
	for _, r := range br.Results {
		if r.Err != "" {
			out = append(out, r)
		}
	}
	return out
}

// ObservedRate returns the completion rate (req/s) over the full wall clock.
// This is biased low for short runs because WallTime includes the latency of
// the final in-flight request; use ArrivalRate for schedule fidelity.
func (br PoissonBenchmarkResults) ObservedRate() float64 {
	if br.WallTime <= 0 {
		return 0
	}
	return float64(len(br.Results)) / br.WallTime.Seconds()
}

// ArrivalRate returns the realized dispatch rate (req/s) measured over the
// arrival window. For a Poisson process with rate λ, this converges to λ as
// the run gets longer.
func (br PoissonBenchmarkResults) ArrivalRate() float64 {
	if br.ArrivalWindow <= 0 {
		return 0
	}
	return float64(len(br.Results)) / br.ArrivalWindow.Seconds()
}

// Accuracy is the fraction of successful requests routed to their ideal model.
func (br PoissonBenchmarkResults) Accuracy() float64 {
	succ := br.Successes()
	if len(succ) == 0 {
		return 0
	}
	correct := 0
	for _, r := range succ {
		if r.IsCorrect {
			correct++
		}
	}
	return float64(correct) / float64(len(succ))
}

// LatencyMS returns successful-request latencies in milliseconds, optionally
// filtered by routed model. Pass "" to get all of them.
func (br PoissonBenchmarkResults) LatencyMS(modelKey string) []float64 {
	var out []float64
	for _, r := range br.Results {
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

// QueueWaitMS returns scheduler queue-wait deltas in milliseconds. Large values
// indicate the dispatcher couldn't keep up with the target arrival rate.
func (br PoissonBenchmarkResults) QueueWaitMS() []float64 {
	out := make([]float64, 0, len(br.Results))
	for _, r := range br.Results {
		out = append(out, r.QueueWait.Seconds()*1000)
	}
	return out
}

// RunPoissonBenchmark fires `len(requests)` prompts at the router according to
// a Poisson process with mean rate `lambda` events/second. The pool of prompts
// is drawn round-robin if `requests` is shorter than `count`. Each request is
// dispatched in its own goroutine at its scheduled arrival, so completion order
// is independent of dispatch order.
//
// Use `count <= 0` to send exactly `len(requests)` prompts. Use a fixed `seed`
// to make the schedule reproducible; pass 0 to seed from the current time.
func RunPoissonBenchmark(
	models map[string]*MockModel,
	router Router,
	requests []Request,
	lambda float64,
	count int,
	seed int64,
) PoissonBenchmarkResults {
	if count <= 0 {
		count = len(requests)
	}
	if count == 0 || len(requests) == 0 {
		return PoissonBenchmarkResults{Lambda: lambda}
	}

	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	rng := rand.New(rand.NewSource(seed))
	gaps := PoissonArrivals(lambda, count, rng)

	results := make([]PoissonResult, count)
	var wg sync.WaitGroup
	var dispatchedIdx atomic.Int64

	start := time.Now()
	cumulative := time.Duration(0)
	for i, gap := range gaps {
		cumulative += gap
		scheduled := cumulative

		// Wait until scheduled arrival. If we've already passed it (dispatcher
		// fell behind), don't sleep — emit immediately and record the lag.
		now := time.Since(start)
		if scheduled > now {
			time.Sleep(scheduled - now)
		}

		req := requests[i%len(requests)]
		dispatched := time.Since(start)
		queueWait := dispatched - scheduled
		if queueWait < 0 {
			queueWait = 0
		}

		wg.Add(1)
		go func(idx int, r Request, scheduledAt, dispatchedAt time.Duration, qw time.Duration) {
			defer wg.Done()

			res := PoissonResult{
				Scheduled:   scheduledAt,
				Dispatched:  dispatchedAt,
				QueueWait:   qw,
				IdealModel:  r.IdealModel,
				Category:    r.Category,
				QuestionID:  r.QuestionID,
				RequestText: r.Text,
			}

			routed := router.Route(r)
			res.RoutedTo = routed
			res.IsCorrect = routed == r.IdealModel

			model, ok := models[routed]
			if !ok {
				res.Err = "unknown model key: " + routed
				res.Completed = time.Since(start)
				res.Latency = res.Completed - dispatchedAt
				results[idx] = res
				return
			}

			_, err := model.Generate(r.InputTokens, r.MaxOutputTokens)
			res.Completed = time.Since(start)
			res.Latency = res.Completed - dispatchedAt
			if err != nil {
				res.Err = err.Error()
			}
			results[idx] = res
			dispatchedIdx.Add(1)
		}(i, req, scheduled, dispatched, queueWait)
	}

	wg.Wait()
	wall := time.Since(start)

	return PoissonBenchmarkResults{
		Lambda:        lambda,
		WallTime:      wall,
		ArrivalWindow: cumulative,
		RequestCount:  count,
		Results:       results,
	}
}

// PoissonStats summarizes inter-arrival statistics for verification.
type PoissonStats struct {
	N         int
	MeanSec   float64
	VarianceS float64
	MinSec    float64
	MaxSec    float64
}

// SummarizeArrivals computes basic statistics on a generated arrival schedule.
// For a Poisson process with rate lambda, mean ≈ 1/lambda and variance ≈ 1/lambda².
func SummarizeArrivals(gaps []time.Duration) PoissonStats {
	if len(gaps) == 0 {
		return PoissonStats{}
	}
	secs := make([]float64, len(gaps))
	var sum float64
	minV, maxV := math.Inf(1), math.Inf(-1)
	for i, g := range gaps {
		s := g.Seconds()
		secs[i] = s
		sum += s
		if s < minV {
			minV = s
		}
		if s > maxV {
			maxV = s
		}
	}
	mean := sum / float64(len(secs))
	var ss float64
	for _, s := range secs {
		ss += (s - mean) * (s - mean)
	}
	variance := ss / float64(len(secs))
	return PoissonStats{
		N:         len(secs),
		MeanSec:   mean,
		VarianceS: variance,
		MinSec:    minV,
		MaxSec:    maxV,
	}
}

// SortFloat64sCopy returns a sorted copy of in. Used by tests that want to
// compute their own percentiles without mutating shared data.
func SortFloat64sCopy(in []float64) []float64 {
	out := make([]float64, len(in))
	copy(out, in)
	sort.Float64s(out)
	return out
}
