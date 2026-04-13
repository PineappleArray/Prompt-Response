package circuit

import (
	"log/slog"
	"sync"
	"time"
)

// State represents the circuit breaker state.
type State int

const (
	StateClosed   State = 0
	StateOpen     State = 1
	StateHalfOpen State = 2
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

const numBuckets = 10

type bucket struct {
	successes int
	failures  int
}

// slidingWindow tracks successes and failures across fixed-duration buckets.
// The ring buffer rotates based on wall-clock time, expiring old data
// without requiring background goroutines.
type slidingWindow struct {
	buckets   [numBuckets]bucket
	bucketDur time.Duration
	current   int
	lastTime  time.Time
}

func newSlidingWindow(windowSize time.Duration, now time.Time) slidingWindow {
	return slidingWindow{
		bucketDur: windowSize / numBuckets,
		lastTime:  now,
	}
}

func (sw *slidingWindow) advance(now time.Time) {
	elapsed := now.Sub(sw.lastTime)
	steps := int(elapsed / sw.bucketDur)
	if steps <= 0 {
		return
	}
	if steps > numBuckets {
		steps = numBuckets
	}
	for i := 0; i < steps; i++ {
		sw.current = (sw.current + 1) % numBuckets
		sw.buckets[sw.current] = bucket{}
	}
	sw.lastTime = now
}

func (sw *slidingWindow) recordSuccess(now time.Time) {
	sw.advance(now)
	sw.buckets[sw.current].successes++
}

func (sw *slidingWindow) recordFailure(now time.Time) {
	sw.advance(now)
	sw.buckets[sw.current].failures++
}

func (sw *slidingWindow) totals(now time.Time) (successes, failures int) {
	sw.advance(now)
	for i := 0; i < numBuckets; i++ {
		successes += sw.buckets[i].successes
		failures += sw.buckets[i].failures
	}
	return
}

// Breaker implements a per-replica circuit breaker with three states:
//   - Closed: requests flow normally; failures tracked in a sliding window.
//   - Open: requests rejected immediately; transitions to HalfOpen after cooldown.
//   - HalfOpen: one probe request allowed; success closes, failure re-opens.
type Breaker struct {
	mu         sync.Mutex
	state      State
	window     slidingWindow
	openedAt   time.Time
	cooldown   time.Duration
	threshold  float64
	minSamples int
	now        func() time.Time // injectable clock for testing
}

func newBreaker(threshold float64, windowSize, cooldown time.Duration, minSamples int, now func() time.Time) *Breaker {
	return &Breaker{
		state:      StateClosed,
		window:     newSlidingWindow(windowSize, now()),
		cooldown:   cooldown,
		threshold:  threshold,
		minSamples: minSamples,
		now:        now,
	}
}

// Allow reports whether a request may be sent through this breaker.
// In the HalfOpen state, only the first caller after the cooldown gets through.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		if b.now().Sub(b.openedAt) >= b.cooldown {
			b.state = StateHalfOpen
			return true // one probe allowed
		}
		return false
	case StateHalfOpen:
		return false // probe already in flight
	default:
		return true
	}
}

// RecordSuccess records a successful request. In HalfOpen, this closes the
// circuit and resets the sliding window.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	b.window.recordSuccess(now)
	if b.state == StateHalfOpen {
		b.state = StateClosed
		b.window = newSlidingWindow(b.window.bucketDur*numBuckets, now)
	}
}

// RecordFailure records a failed request and potentially trips the breaker.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	b.window.recordFailure(now)

	switch b.state {
	case StateClosed:
		successes, failures := b.window.totals(now)
		total := successes + failures
		if total >= b.minSamples {
			errorRate := float64(failures) / float64(total)
			if errorRate >= b.threshold {
				b.state = StateOpen
				b.openedAt = now
				slog.Warn("circuit breaker opened",
					"error_rate", errorRate,
					"threshold", b.threshold,
					"total", total,
				)
			}
		}
	case StateHalfOpen:
		b.state = StateOpen
		b.openedAt = now
	}
}

// State returns the current breaker state, accounting for cooldown expiry.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == StateOpen && b.now().Sub(b.openedAt) >= b.cooldown {
		b.state = StateHalfOpen
	}
	return b.state
}

// ErrorRate returns the current error rate across the sliding window.
func (b *Breaker) ErrorRate() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	successes, failures := b.window.totals(b.now())
	total := successes + failures
	if total == 0 {
		return 0
	}
	return float64(failures) / float64(total)
}

// Config holds tuning parameters for circuit breakers.
type Config struct {
	ErrorThreshold float64
	WindowSize     time.Duration
	Cooldown       time.Duration
	MinSamples     int
}

// Registry manages per-replica circuit breakers, lazily creating them on first use.
type Registry struct {
	breakers   map[string]*Breaker
	mu         sync.RWMutex
	threshold  float64
	windowSize time.Duration
	cooldown   time.Duration
	minSamples int
	now        func() time.Time
}

// NewRegistry creates a registry with the given configuration.
func NewRegistry(cfg Config) *Registry {
	return &Registry{
		breakers:   make(map[string]*Breaker),
		threshold:  cfg.ErrorThreshold,
		windowSize: cfg.WindowSize,
		cooldown:   cfg.Cooldown,
		minSamples: cfg.MinSamples,
		now:        time.Now,
	}
}

// newRegistryWithClock creates a registry with an injectable clock (for testing).
func newRegistryWithClock(cfg Config, now func() time.Time) *Registry {
	r := NewRegistry(cfg)
	r.now = now
	return r
}

func (reg *Registry) getOrCreate(id string) *Breaker {
	reg.mu.RLock()
	b, ok := reg.breakers[id]
	reg.mu.RUnlock()
	if ok {
		return b
	}

	reg.mu.Lock()
	defer reg.mu.Unlock()
	if b, ok = reg.breakers[id]; ok {
		return b
	}
	b = newBreaker(reg.threshold, reg.windowSize, reg.cooldown, reg.minSamples, reg.now)
	reg.breakers[id] = b
	return b
}

// Allow reports whether the replica's circuit breaker permits a request.
func (reg *Registry) Allow(id string) bool {
	return reg.getOrCreate(id).Allow()
}

// RecordSuccess records a successful response from the replica.
func (reg *Registry) RecordSuccess(id string) {
	reg.getOrCreate(id).RecordSuccess()
}

// RecordFailure records a failed response from the replica.
func (reg *Registry) RecordFailure(id string) {
	reg.getOrCreate(id).RecordFailure()
}

// State returns the current circuit breaker state for the replica.
func (reg *Registry) State(id string) State {
	return reg.getOrCreate(id).State()
}

// ErrorRate returns the current error rate for the replica.
func (reg *Registry) ErrorRate(id string) float64 {
	return reg.getOrCreate(id).ErrorRate()
}
