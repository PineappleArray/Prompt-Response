package store

import (
	"time"

	"prompt-response/internal/types"
)

// Store is the persistence interface the router uses for routing state.
// RedisStore is the production implementation; MemoryStore is used by tests
// and as a fallback when Redis is unavailable.
type Store interface {
	// Prefix-cache affinity: route requests sharing a system-prompt prefix
	// to the same replica so vLLM reuses its KV cache.
	GetAffinity(hash uint64) (replicaID string, ok bool)
	SetAffinity(hash uint64, replicaID string, ttl time.Duration)

	// Conversation tier lock: pin a conversation to a tier so multi-turn
	// conversations stay consistent and only ever escalate.
	GetConversation(convID uint64) (state types.ConvState, ok bool)
	SetConversation(convID uint64, state types.ConvState, ttl time.Duration)
}
