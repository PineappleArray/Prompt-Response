// Package usage tracks per-tenant token consumption for cost attribution
// and chargeback reporting. Counters are kept in-memory with O(1) updates
// and exposed via /v1/router/usage and Prometheus.
package usage

import (
	"sync"
	"time"
)

// Usage captures a single tenant's cumulative token consumption.
type Usage struct {
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	Requests     int64     `json:"requests"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
}

// Tracker maintains per-tenant token usage counters. Safe for concurrent use.
type Tracker struct {
	mu   sync.RWMutex
	data map[string]*Usage
	now  func() time.Time
}

// NewTracker returns a Tracker backed by the real wall clock.
func NewTracker() *Tracker {
	return &Tracker{
		data: make(map[string]*Usage),
		now:  time.Now,
	}
}

// newTrackerForTest returns a Tracker with an injected clock; test-only.
func newTrackerForTest(now func() time.Time) *Tracker {
	return &Tracker{
		data: make(map[string]*Usage),
		now:  now,
	}
}

// Record increments counters for the given tenant. Negative token counts
// are clamped to zero. An empty tenant is accepted and tracked literally;
// callers should substitute a sentinel like "anonymous" if needed.
func (t *Tracker) Record(tenant string, inputTokens, outputTokens int) {
	if inputTokens < 0 {
		inputTokens = 0
	}
	if outputTokens < 0 {
		outputTokens = 0
	}

	now := t.now()

	t.mu.Lock()
	defer t.mu.Unlock()

	u, ok := t.data[tenant]
	if !ok {
		u = &Usage{FirstSeen: now}
		t.data[tenant] = u
	}
	u.InputTokens += int64(inputTokens)
	u.OutputTokens += int64(outputTokens)
	u.Requests++
	u.LastSeen = now
}

// Get returns a copy of the named tenant's usage, or (zero, false) if absent.
func (t *Tracker) Get(tenant string) (Usage, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	u, ok := t.data[tenant]
	if !ok {
		return Usage{}, false
	}
	return *u, true
}

// All returns a snapshot of every tenant's usage. The returned map is a
// fresh copy; callers may mutate it without affecting the tracker.
func (t *Tracker) All() map[string]Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]Usage, len(t.data))
	for k, v := range t.data {
		out[k] = *v
	}
	return out
}

// Reset deletes usage for the named tenant. If tenant is "", resets all
// tenants. Returns the number of entries removed.
func (t *Tracker) Reset(tenant string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if tenant == "" {
		n := len(t.data)
		t.data = make(map[string]*Usage)
		return n
	}
	if _, ok := t.data[tenant]; ok {
		delete(t.data, tenant)
		return 1
	}
	return 0
}

// Len returns the number of tenants currently tracked.
func (t *Tracker) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.data)
}
