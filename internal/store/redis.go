package store

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"prompt-response/internal/types"

	"github.com/redis/go-redis/v9"
)

/*
Redis key schema:

Prefix cache (replica affinity):
	Key:    pfx:<xxhash64>
	Value:  replica-id
	TTL:    affinity_ttl

Conversation tier lock:
	Key:    conv:<xxhash64>
	Value:  HASH { tier, model, bucket, turns, updated_at }
	TTL:    affinity_ttl
*/

// redisTimeout bounds every Redis call. Routing must never block on a slow
// or unreachable Redis — a miss simply degrades to stateless routing.
const redisTimeout = 50 * time.Millisecond

type RedisStore struct {
	client *redis.Client
}

func NewRedis(addr string) *RedisStore {
	return &RedisStore{
		client: redis.NewClient(&redis.Options{Addr: addr}),
	}
}

// Ping checks Redis connectivity.
func (r *RedisStore) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

func (r *RedisStore) GetAffinity(hash uint64) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), redisTimeout)
	defer cancel()

	val, err := r.client.Get(ctx, affinityKey(hash)).Result()
	if err != nil {
		return "", false
	}
	return val, true
}

func (r *RedisStore) SetAffinity(hash uint64, replicaID string, ttl time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), redisTimeout)
	defer cancel()

	key := affinityKey(hash)
	if err := r.client.Set(ctx, key, replicaID, ttl).Err(); err != nil {
		slog.Warn("failed to set affinity in redis", "key", key, "replica", replicaID, "err", err)
	}
}

// GetConversation returns the tier state pinned to a conversation. A missing
// key, an expired key, or any Redis error returns ok=false so the caller
// falls back to stateless routing.
func (r *RedisStore) GetConversation(convID uint64) (types.ConvState, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), redisTimeout)
	defer cancel()

	key := convKey(convID)
	fields, err := r.client.HGetAll(ctx, key).Result()
	if err != nil || len(fields) == 0 {
		return types.ConvState{}, false
	}

	turns, _ := strconv.Atoi(fields["turns"])
	updated := time.Time{}
	if ts, err := strconv.ParseInt(fields["updated_at"], 10, 64); err == nil {
		updated = time.Unix(ts, 0)
	}
	return types.ConvState{
		Tier:      types.ModelTier(fields["tier"]),
		Model:     fields["model"],
		Bucket:    fields["bucket"],
		Turns:     turns,
		UpdatedAt: updated,
	}, true
}

// SetConversation writes the conversation's tier state and refreshes its TTL
// in a single round-trip. Failures are logged but never block routing.
func (r *RedisStore) SetConversation(convID uint64, state types.ConvState, ttl time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), redisTimeout)
	defer cancel()

	key := convKey(convID)
	pipe := r.client.Pipeline()
	pipe.HSet(ctx, key, map[string]any{
		"tier":       string(state.Tier),
		"model":      state.Model,
		"bucket":     state.Bucket,
		"turns":      state.Turns,
		"updated_at": state.UpdatedAt.Unix(),
	})
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("failed to set conversation in redis", "key", key, "err", err)
	}
}

func affinityKey(hash uint64) string { return fmt.Sprintf("pfx:%016x", hash) }
func convKey(id uint64) string       { return fmt.Sprintf("conv:%016x", id) }
