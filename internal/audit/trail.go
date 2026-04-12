package audit

import "sync"

// Trail is a fixed-size ring buffer that stores recent routing decisions.
// Insert is O(1) with constant memory. Safe for concurrent use.
type Trail struct {
	mu      sync.RWMutex
	records []Record
	head    int
	count   int
}

// NewTrail creates a trail with the given capacity.
func NewTrail(capacity int) *Trail {
	return &Trail{
		records: make([]Record, capacity),
	}
}

// Record appends a routing decision to the trail, overwriting the
// oldest entry when the buffer is full.
func (t *Trail) Record(r Record) {
	t.mu.Lock()
	t.records[t.head] = r
	t.head = (t.head + 1) % len(t.records)
	if t.count < len(t.records) {
		t.count++
	}
	t.mu.Unlock()
}

// Recent returns the last n records in reverse chronological order
// (newest first). If n exceeds the number of stored records, returns
// all available records.
func (t *Trail) Recent(n int) []Record {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if n > t.count {
		n = t.count
	}
	if n == 0 {
		return nil
	}

	result := make([]Record, n)
	for i := 0; i < n; i++ {
		idx := (t.head - 1 - i + len(t.records)) % len(t.records)
		result[i] = t.records[idx]
	}
	return result
}

// Len returns the current number of records in the trail.
func (t *Trail) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.count
}

// Cap returns the maximum capacity of the trail.
func (t *Trail) Cap() int {
	return len(t.records)
}
