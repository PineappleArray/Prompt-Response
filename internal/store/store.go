package store

import "time"

// Store is the interface the scorer uses.
// RedisStore implements it. Tests use an in-memory version.
type Store interface {
	GetAffinity(hash uint64) (replicaID string, ok bool)
	SetAffinity(hash uint64, replicaID string, ttl time.Duration)
}