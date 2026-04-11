package store

import (
	"sync"
	"time"
)

type entry struct {
	replicaID string
	expiresAt time.Time
}

// MemoryStore is an in-memory Store implementation for tests and as a
// fallback when Redis is unavailable.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[uint64]entry
}

func NewMemory() *MemoryStore {
	return &MemoryStore{
		data: make(map[uint64]entry),
	}
}

func (m *MemoryStore) GetAffinity(hash uint64) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.data[hash]
	if !ok || time.Now().After(e.expiresAt) {
		return "", false
	}
	return e.replicaID, true
}

func (m *MemoryStore) SetAffinity(hash uint64, replicaID string, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[hash] = entry{
		replicaID: replicaID,
		expiresAt: time.Now().Add(ttl),
	}
}
