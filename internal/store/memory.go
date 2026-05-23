package store

import (
	"sync"
	"time"

	"prompt-response/internal/types"
)

type entry struct {
	replicaID string
	expiresAt time.Time
}

type convEntry struct {
	state     types.ConvState
	expiresAt time.Time
}

// MemoryStore is an in-memory Store implementation for tests and as a
// fallback when Redis is unavailable.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[uint64]entry
	conv map[uint64]convEntry
}

func NewMemory() *MemoryStore {
	return &MemoryStore{
		data: make(map[uint64]entry),
		conv: make(map[uint64]convEntry),
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

func (m *MemoryStore) GetConversation(convID uint64) (types.ConvState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.conv[convID]
	if !ok || time.Now().After(e.expiresAt) {
		return types.ConvState{}, false
	}
	return e.state, true
}

func (m *MemoryStore) SetConversation(convID uint64, state types.ConvState, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.conv[convID] = convEntry{
		state:     state,
		expiresAt: time.Now().Add(ttl),
	}
}
