// Package ratelimit provides per-tenant token bucket rate limiting.
package ratelimit

import (
	"sync"
	"time"
)

// Config holds rate limiter configuration.
type Config struct {
	RequestsPerMinute float64
	Burst             int
}

// TokenBucket implements the token bucket algorithm for rate limiting.
// Tokens are added at a steady rate up to a maximum capacity (burst).
// Each Allow() call consumes one token.
type TokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	capacity float64
	rate     float64 // tokens per second
	last     time.Time
	now      func() time.Time
}

func newBucket(capacity float64, rate float64, now func() time.Time) *TokenBucket {
	return &TokenBucket{
		tokens:   capacity, // start full
		capacity: capacity,
		rate:     rate,
		last:     now(),
		now:      now,
	}
}

// Allow consumes one token and returns whether the request is permitted,
// along with the approximate number of remaining tokens.
func (b *TokenBucket) Allow() (bool, float64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now

	b.tokens += elapsed * b.rate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}

	if b.tokens < 1 {
		return false, 0
	}
	b.tokens--
	return true, b.tokens
}

// Registry manages per-key token buckets with lazy creation.
type Registry struct {
	mu      sync.RWMutex
	buckets map[string]*TokenBucket
	cap     float64
	rate    float64 // tokens per second
	now     func() time.Time
}

// NewRegistry creates a rate limiter registry from configuration.
func NewRegistry(cfg Config) *Registry {
	return &Registry{
		buckets: make(map[string]*TokenBucket),
		cap:     float64(cfg.Burst),
		rate:    cfg.RequestsPerMinute / 60.0,
		now:     time.Now,
	}
}

// newRegistryForTest creates a registry with an injectable clock.
func newRegistryForTest(cfg Config, now func() time.Time) *Registry {
	return &Registry{
		buckets: make(map[string]*TokenBucket),
		cap:     float64(cfg.Burst),
		rate:    cfg.RequestsPerMinute / 60.0,
		now:     now,
	}
}

// Allow checks the rate limit for the given key. Returns whether the request
// is permitted and the approximate remaining tokens.
func (r *Registry) Allow(key string) (bool, float64) {
	b := r.getOrCreate(key)
	return b.Allow()
}

// Limit returns the configured rate limit in requests per minute.
func (r *Registry) Limit() float64 {
	return r.rate * 60
}

func (r *Registry) getOrCreate(key string) *TokenBucket {
	r.mu.RLock()
	b, ok := r.buckets[key]
	r.mu.RUnlock()
	if ok {
		return b
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok = r.buckets[key]; ok {
		return b
	}
	b = newBucket(r.cap, r.rate, r.now)
	r.buckets[key] = b
	return b
}
