package circuit

import (
	"sync"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		ErrorThreshold: 0.5,
		WindowSize:     10 * time.Second,
		Cooldown:       30 * time.Second,
		MinSamples:     5,
	}
}

func TestBreaker_StaysClosedBelowThreshold(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }

	cfg := testConfig()
	reg := newRegistryWithClock(cfg, clock)

	// 8 successes, 2 failures = 20% error rate, below 50% threshold
	for i := 0; i < 8; i++ {
		reg.RecordSuccess("r1")
	}
	for i := 0; i < 2; i++ {
		reg.RecordFailure("r1")
	}

	if !reg.Allow("r1") {
		t.Error("circuit should remain closed below threshold")
	}
	if s := reg.State("r1"); s != StateClosed {
		t.Errorf("expected StateClosed, got %s", s)
	}
}

func TestBreaker_OpensAtThreshold(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }

	cfg := testConfig()
	reg := newRegistryWithClock(cfg, clock)

	// 2 successes, 3 failures = 60% error rate, above 50% threshold
	for i := 0; i < 2; i++ {
		reg.RecordSuccess("r1")
	}
	for i := 0; i < 3; i++ {
		reg.RecordFailure("r1")
	}

	if s := reg.State("r1"); s != StateOpen {
		t.Errorf("expected StateOpen after exceeding threshold, got %s", s)
	}
	if reg.Allow("r1") {
		t.Error("open circuit should reject requests")
	}
}

func TestBreaker_RejectsWhenOpen(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }

	cfg := testConfig()
	reg := newRegistryWithClock(cfg, clock)

	// Trip the circuit
	for i := 0; i < 5; i++ {
		reg.RecordFailure("r1")
	}

	// Multiple Allow calls should all return false
	for i := 0; i < 3; i++ {
		if reg.Allow("r1") {
			t.Errorf("open circuit should reject request %d", i)
		}
	}
}

func TestBreaker_TransitionsToHalfOpen(t *testing.T) {
	now := time.Now()
	mu := sync.Mutex{}
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}

	cfg := testConfig()
	reg := newRegistryWithClock(cfg, clock)

	// Trip the circuit
	for i := 0; i < 5; i++ {
		reg.RecordFailure("r1")
	}
	if reg.Allow("r1") {
		t.Fatal("should be open")
	}

	// Advance past cooldown
	mu.Lock()
	now = now.Add(31 * time.Second)
	mu.Unlock()

	// First Allow after cooldown should succeed (probe)
	if !reg.Allow("r1") {
		t.Error("should allow one probe after cooldown")
	}
	// State should be HalfOpen
	if s := reg.State("r1"); s != StateHalfOpen {
		t.Errorf("expected StateHalfOpen, got %s", s)
	}
	// Second Allow while probe in flight should be rejected
	if reg.Allow("r1") {
		t.Error("should reject during half-open probe")
	}
}

func TestBreaker_HalfOpenSuccessCloses(t *testing.T) {
	now := time.Now()
	mu := sync.Mutex{}
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}

	cfg := testConfig()
	reg := newRegistryWithClock(cfg, clock)

	// Trip, wait cooldown, send probe
	for i := 0; i < 5; i++ {
		reg.RecordFailure("r1")
	}
	mu.Lock()
	now = now.Add(31 * time.Second)
	mu.Unlock()

	reg.Allow("r1") // transition to half-open, allow probe
	reg.RecordSuccess("r1")

	if s := reg.State("r1"); s != StateClosed {
		t.Errorf("expected StateClosed after successful probe, got %s", s)
	}
	if !reg.Allow("r1") {
		t.Error("circuit should be closed and allowing requests")
	}
}

func TestBreaker_HalfOpenFailureReopens(t *testing.T) {
	now := time.Now()
	mu := sync.Mutex{}
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}

	cfg := testConfig()
	reg := newRegistryWithClock(cfg, clock)

	// Trip, wait cooldown, send probe
	for i := 0; i < 5; i++ {
		reg.RecordFailure("r1")
	}
	mu.Lock()
	now = now.Add(31 * time.Second)
	mu.Unlock()

	reg.Allow("r1") // transition to half-open
	reg.RecordFailure("r1")

	if s := reg.State("r1"); s != StateOpen {
		t.Errorf("expected StateOpen after failed probe, got %s", s)
	}
	if reg.Allow("r1") {
		t.Error("circuit should be re-opened and rejecting")
	}
}

func TestBreaker_MinSamplesRespected(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }

	cfg := testConfig()
	cfg.MinSamples = 5
	reg := newRegistryWithClock(cfg, clock)

	// 3 failures out of 3 = 100% error rate, but only 3 samples (< 5 min)
	for i := 0; i < 3; i++ {
		reg.RecordFailure("r1")
	}

	if s := reg.State("r1"); s != StateClosed {
		t.Errorf("expected StateClosed (below min_samples), got %s", s)
	}
	if !reg.Allow("r1") {
		t.Error("should allow requests when below min_samples")
	}
}

func TestBreaker_WindowExpiry(t *testing.T) {
	now := time.Now()
	mu := sync.Mutex{}
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}

	cfg := testConfig()
	cfg.WindowSize = 10 * time.Second // 10 buckets × 1s each
	cfg.MinSamples = 3
	reg := newRegistryWithClock(cfg, clock)

	// Record failures
	for i := 0; i < 4; i++ {
		reg.RecordFailure("r1")
	}
	reg.RecordSuccess("r1") // 4/5 = 80% → trips at min_samples=3

	if s := reg.State("r1"); s != StateOpen {
		t.Fatalf("expected circuit to trip, got %s", s)
	}

	// Wait for cooldown, then wait for window to expire
	mu.Lock()
	now = now.Add(31 * time.Second)
	mu.Unlock()

	// Probe succeeds, closing the circuit
	reg.Allow("r1")
	reg.RecordSuccess("r1")

	// After closing, window is reset — old failures are gone
	rate := reg.ErrorRate("r1")
	if rate > 0.01 {
		t.Errorf("expected near-zero error rate after reset, got %f", rate)
	}
}

func TestRegistry_IndependentBreakers(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }

	cfg := testConfig()
	reg := newRegistryWithClock(cfg, clock)

	// Trip r1
	for i := 0; i < 5; i++ {
		reg.RecordFailure("r1")
	}

	if reg.Allow("r1") {
		t.Error("r1 should be open")
	}
	if !reg.Allow("r2") {
		t.Error("r2 should be independent and allow traffic")
	}
}

func TestRegistry_AllowUnknownReplica(t *testing.T) {
	cfg := testConfig()
	reg := NewRegistry(cfg)

	// First call for unknown replica should create a closed breaker
	if !reg.Allow("never-seen") {
		t.Error("new replica should have closed circuit")
	}
}

func TestBreaker_ConcurrentAccess(t *testing.T) {
	cfg := testConfig()
	reg := NewRegistry(cfg)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			reg.Allow("r1")
		}()
		go func() {
			defer wg.Done()
			reg.RecordSuccess("r1")
		}()
		go func() {
			defer wg.Done()
			reg.RecordFailure("r1")
		}()
	}
	wg.Wait()

	// Just verify no panic or data race (run with -race)
	_ = reg.State("r1")
	_ = reg.ErrorRate("r1")
}

func TestState_String(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half_open"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}
