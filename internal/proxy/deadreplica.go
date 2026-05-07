package proxy

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"time"
)

// errStreamStalled signals that the upstream replica has not produced any bytes
// (pre-first-byte stall) or has not emitted the SSE [DONE] sentinel within the
// configured stall window. Treated as a retryable upstream failure when no
// bytes have yet been forwarded to the client.
var errStreamStalled = errors.New("dead replica: stream stalled before terminating character")

// peekResult is the outcome of waiting for a replica's first body byte.
type peekResult struct {
	// reader yields the full upstream body (peeked byte rejoined in front).
	reader io.Reader
	// err is non-nil when the body closed or the stall timeout elapsed before
	// the replica emitted any data.
	err error
}

// peekFirstByte blocks until the upstream emits at least one body byte or the
// stall timeout elapses. A timeout indicates the replica is dead — it accepted
// the request, returned response headers, but never produced output. The
// caller is expected to cancel the upstream context on error so the dangling
// goroutine unblocks promptly.
//
// On success the returned reader replays the peeked byte before yielding the
// rest of the body, so streaming downstream is byte-identical to a direct
// copy of body.
func peekFirstByte(body io.Reader, stallTimeout time.Duration) peekResult {
	type readOutcome struct {
		b   byte
		err error
	}
	ch := make(chan readOutcome, 1)
	go func() {
		var buf [1]byte
		_, err := io.ReadFull(body, buf[:])
		ch <- readOutcome{buf[0], err}
	}()

	timer := time.NewTimer(stallTimeout)
	defer timer.Stop()

	select {
	case out := <-ch:
		if out.err != nil {
			return peekResult{err: out.err}
		}
		return peekResult{
			reader: io.MultiReader(bytes.NewReader([]byte{out.b}), body),
		}
	case <-timer.C:
		return peekResult{err: errStreamStalled}
	}
}

// stallWatchdog cancels its target context when no activity has been observed
// for stallTimeout. activity() is expected to return the most recent Write
// timestamp on the streamInterceptor; the watchdog samples it on a fixed tick
// (stallTimeout / 4) so detection latency is bounded by ~1.25 × stallTimeout
// in the worst case.
//
// The watchdog is safe for concurrent stop after cancel has already fired.
type stallWatchdog struct {
	stop chan struct{}
	wg   sync.WaitGroup
}

// newStallWatchdog spawns a goroutine that calls onStall once if no activity
// is observed for stallTimeout. activity must return a monotonically advancing
// time.Time; a zero value defers the deadline until the first activity sample.
func newStallWatchdog(stallTimeout time.Duration, activity func() time.Time, onStall func()) *stallWatchdog {
	if stallTimeout <= 0 {
		return &stallWatchdog{stop: make(chan struct{})}
	}

	tick := stallTimeout / 4
	if tick < 50*time.Millisecond {
		tick = 50 * time.Millisecond
	}

	w := &stallWatchdog{stop: make(chan struct{})}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		started := time.Now()
		for {
			select {
			case <-w.stop:
				return
			case now := <-ticker.C:
				last := activity()
				if last.IsZero() {
					last = started
				}
				if now.Sub(last) >= stallTimeout {
					onStall()
					return
				}
			}
		}
	}()
	return w
}

// Stop halts the watchdog. Safe to call multiple times.
func (w *stallWatchdog) Stop() {
	select {
	case <-w.stop:
	default:
		close(w.stop)
	}
	w.wg.Wait()
}
