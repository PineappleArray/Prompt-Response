package usage

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// captureFlush returns a FlushFunc that records every flushed batch.
func captureFlush() (*sync.Mutex, *[][]UsageEvent, FlushFunc) {
	var mu sync.Mutex
	var batches [][]UsageEvent
	return &mu, &batches, func(_ context.Context, batch []UsageEvent) error {
		mu.Lock()
		defer mu.Unlock()
		cp := append([]UsageEvent(nil), batch...)
		batches = append(batches, cp)
		return nil
	}
}

func totalFlushed(batches [][]UsageEvent) int {
	n := 0
	for _, b := range batches {
		n += len(b)
	}
	return n
}

func TestBatchSink_FlushesOnBatchSize(t *testing.T) {
	mu, batches, flush := captureFlush()
	// Long interval — the batch trigger is the only one that should fire.
	s := NewBatchSink(64, 3, time.Hour, flush)
	defer s.Close()

	for i := 0; i < 6; i++ {
		s.Enqueue(UsageEvent{Tenant: "t", In: 1, Out: 1, At: time.Now()})
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(*batches)
		mu.Unlock()
		if n == 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Errorf("expected 2 batches of 3, got %d batches totalling %d events", len(*batches), totalFlushed(*batches))
}

func TestBatchSink_FlushesOnInterval(t *testing.T) {
	mu, batches, flush := captureFlush()
	// Big batch size — the timer is the only thing that should fire.
	s := NewBatchSink(64, 1000, 25*time.Millisecond, flush)
	defer s.Close()

	for i := 0; i < 4; i++ {
		s.Enqueue(UsageEvent{Tenant: "t", In: 1, Out: 1, At: time.Now()})
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		flushed := totalFlushed(*batches)
		mu.Unlock()
		if flushed == 4 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Errorf("expected 4 events flushed via interval, got %d (in %d batches)", totalFlushed(*batches), len(*batches))
}

func TestBatchSink_CloseDrains(t *testing.T) {
	mu, batches, flush := captureFlush()
	// Effectively no auto-flush — only the drain on Close should run.
	s := NewBatchSink(64, 1000, time.Hour, flush)

	for i := 0; i < 7; i++ {
		s.Enqueue(UsageEvent{Tenant: "t", In: 1, Out: 1, At: time.Now()})
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := totalFlushed(*batches); got != 7 {
		t.Errorf("Close should drain remaining events; flushed %d, want 7", got)
	}
}

func TestBatchSink_FullBufferDropsEvents(t *testing.T) {
	// Blocking flush so the worker can't drain — every event past the
	// buffer capacity must be dropped, not block the caller.
	blocking := make(chan struct{})
	var flushed atomic.Int64
	flush := func(_ context.Context, batch []UsageEvent) error {
		<-blocking
		flushed.Add(int64(len(batch)))
		return nil
	}

	s := NewBatchSink(2, 1, time.Hour, flush)
	defer func() {
		close(blocking)
		s.Close()
	}()

	// First Enqueue is consumed by the worker (then blocks in flush).
	// Next 2 fill the channel. Subsequent 5 must be dropped.
	for i := 0; i < 8; i++ {
		s.Enqueue(UsageEvent{Tenant: "t", In: 1, Out: 1, At: time.Now()})
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if s.Dropped() >= 5 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := s.Dropped(); got < 5 {
		t.Errorf("expected at least 5 dropped events, got %d", got)
	}
}

func TestNoopSink_IsSilent(t *testing.T) {
	var s Sink = NoopSink{}
	s.Enqueue(UsageEvent{Tenant: "t"})
	if err := s.Close(); err != nil {
		t.Errorf("NoopSink.Close: %v", err)
	}
}
