package proxy

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// blockingReader blocks Read until released (or EOF after release).
type blockingReader struct {
	release chan struct{}
	body    []byte
	once    bool
}

func newBlockingReader(body string) *blockingReader {
	return &blockingReader{
		release: make(chan struct{}),
		body:    []byte(body),
	}
}

func (b *blockingReader) Read(p []byte) (int, error) {
	if b.once {
		return 0, io.EOF
	}
	<-b.release
	b.once = true
	if len(b.body) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.body)
	return n, nil
}

func (b *blockingReader) Close() error {
	select {
	case <-b.release:
	default:
		close(b.release)
	}
	return nil
}

func TestPeekFirstByte_ImmediateData(t *testing.T) {
	body := io.NopCloser(strings.NewReader("data: hello\n\n"))
	res := peekFirstByte(body, 100*time.Millisecond)
	if res.err != nil {
		t.Fatalf("expected nil err, got %v", res.err)
	}
	full, err := io.ReadAll(res.reader)
	if err != nil {
		t.Fatalf("read remaining: %v", err)
	}
	if string(full) != "data: hello\n\n" {
		t.Errorf("expected peeked byte rejoined, got %q", full)
	}
}

func TestPeekFirstByte_StallTimeout(t *testing.T) {
	body := newBlockingReader("late")
	defer body.Close()

	start := time.Now()
	res := peekFirstByte(body, 50*time.Millisecond)
	elapsed := time.Since(start)

	if !errors.Is(res.err, errStreamStalled) {
		t.Fatalf("expected errStreamStalled, got %v", res.err)
	}
	if elapsed < 45*time.Millisecond {
		t.Errorf("expected stall timeout >= 45ms, got %v", elapsed)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("stall returned too late: %v", elapsed)
	}
}

func TestPeekFirstByte_BodyClosedEarly(t *testing.T) {
	body := io.NopCloser(bytes.NewReader(nil)) // immediate EOF
	res := peekFirstByte(body, time.Second)
	if res.err == nil {
		t.Fatal("expected err on empty body, got nil")
	}
	if errors.Is(res.err, errStreamStalled) {
		t.Errorf("EOF should not be reported as stall: %v", res.err)
	}
}

func TestStallWatchdog_FiresOnInactivity(t *testing.T) {
	var fired atomic.Int32
	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())

	w := newStallWatchdog(120*time.Millisecond,
		func() time.Time { return time.Unix(0, lastActivity.Load()) },
		func() { fired.Add(1) })
	defer w.Stop()

	// Wait long enough for watchdog to detect stall (timeout + tick + slack).
	time.Sleep(300 * time.Millisecond)

	if got := fired.Load(); got != 1 {
		t.Errorf("expected watchdog to fire once, got %d", got)
	}
}

func TestStallWatchdog_QuietWhenActive(t *testing.T) {
	var fired atomic.Int32
	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())

	w := newStallWatchdog(150*time.Millisecond,
		func() time.Time { return time.Unix(0, lastActivity.Load()) },
		func() { fired.Add(1) })

	// Heartbeat faster than the stall window for 300ms.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		lastActivity.Store(time.Now().UnixNano())
		time.Sleep(40 * time.Millisecond)
	}
	w.Stop()

	if got := fired.Load(); got != 0 {
		t.Errorf("expected watchdog not to fire while active, got %d", got)
	}
}

func TestStallWatchdog_StopBeforeFire(t *testing.T) {
	var fired atomic.Int32
	w := newStallWatchdog(time.Second,
		func() time.Time { return time.Now() },
		func() { fired.Add(1) })
	w.Stop()
	w.Stop() // double-stop must not panic

	time.Sleep(50 * time.Millisecond)
	if got := fired.Load(); got != 0 {
		t.Errorf("watchdog fired after Stop, count=%d", got)
	}
}
