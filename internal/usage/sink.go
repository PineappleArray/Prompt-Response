package usage

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"prompt-response/internal/metrics"
)

// UsageEvent is a single per-request token consumption record. Cheap to
// construct on the request hot path and cheap to send over a channel.
type UsageEvent struct {
	Tenant string
	In     int
	Out    int
	At     time.Time
}

// Sink is the persistence interface for usage events. The in-memory
// Tracker remains authoritative for the /v1/router/usage endpoint; the
// Sink is an asynchronous mirror that lets totals survive restarts.
type Sink interface {
	Enqueue(ev UsageEvent)
	Close() error
}

// NoopSink is the default when no persistence is configured.
type NoopSink struct{}

func (NoopSink) Enqueue(UsageEvent) {}
func (NoopSink) Close() error       { return nil }

// FlushFunc batches events out to a backend (e.g. Postgres). It is
// injectable so tests can substitute a fake.
type FlushFunc func(ctx context.Context, batch []UsageEvent) error

// BatchSink buffers usage events on a bounded channel and flushes them
// to flushFn every BatchSize events or every Interval, whichever comes
// first. A full buffer drops the incoming event with a counter rather
// than blocking the caller's request — usage tracking must never slow
// down routing.
type BatchSink struct {
	ch        chan UsageEvent
	flushFn   FlushFunc
	batchSize int
	interval  time.Duration
	dropped   atomic.Int64

	stop chan struct{}
	done chan struct{}
}

// NewBatchSink starts the background worker. The caller must hold the
// returned sink for the process lifetime and call Close on shutdown so
// the buffer is drained.
func NewBatchSink(bufferSize, batchSize int, interval time.Duration, flushFn FlushFunc) *BatchSink {
	s := &BatchSink{
		ch:        make(chan UsageEvent, bufferSize),
		flushFn:   flushFn,
		batchSize: batchSize,
		interval:  interval,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	go s.run()
	return s
}

// Enqueue records a usage event without blocking. If the buffer is full
// the event is dropped and the drop counter is incremented.
func (s *BatchSink) Enqueue(ev UsageEvent) {
	select {
	case s.ch <- ev:
	default:
		s.dropped.Add(1)
		metrics.UsagePostgresBufferDropped.Inc()
	}
}

// Dropped returns the number of events lost to a full buffer since the
// sink was created.
func (s *BatchSink) Dropped() int64 { return s.dropped.Load() }

// Close stops the worker, drains buffered events with a final flush,
// and returns once the worker exits.
func (s *BatchSink) Close() error {
	close(s.stop)
	<-s.done
	return nil
}

func (s *BatchSink) run() {
	defer close(s.done)

	batch := make([]UsageEvent, 0, s.batchSize)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := s.flushFn(ctx, batch)
		cancel()
		status := "ok"
		if err != nil {
			status = "error"
			slog.Warn("usage sink flush failed", "err", err, "events", len(batch))
		}
		metrics.UsagePostgresFlushTotal.WithLabelValues(status).Inc()
		batch = batch[:0]
	}

	for {
		select {
		case ev := <-s.ch:
			batch = append(batch, ev)
			if len(batch) >= s.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.stop:
			// Drain remaining queued events, then flush whatever is left.
			for {
				select {
				case ev := <-s.ch:
					batch = append(batch, ev)
				default:
					flush()
					return
				}
			}
		}
	}
}
