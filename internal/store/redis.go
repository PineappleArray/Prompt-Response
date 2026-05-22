package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"prompt-response/internal/types"

	"github.com/redis/go-redis/v9"
)

type RedisStore struct {
	client *redis.Client
}

func NewRedis(addr string) *RedisStore {
	return &RedisStore{
		client: redis.NewClient(&redis.Options{Addr: addr}),
	}
}

func (r *RedisStore) GetAffinity(hash uint64) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	key := fmt.Sprintf("pfx:%016x", hash)
	val, err := r.client.Get(ctx, key).Result()
	if err != nil {
		return "", false
	}
	return val, true
}

func (r *RedisStore) SetAffinity(hash uint64, replicaID string, ttl time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	key := fmt.Sprintf("pfx:%016x", hash)
	if err := r.client.Set(ctx, key, replicaID, ttl).Err(); err != nil {
		slog.Warn("failed to set affinity in redis", "key", key, "replica", replicaID, "err", err)
	}
}

// Ping checks Redis connectivity.
func (r *RedisStore) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

func (r *RedisStore) addSession(sessionID string, userID string, model types.ModelTier, ttl time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	key := fmt.Sprintf("session:%s", sessionID)
	pipe := r.client.Pipeline()

	tierCmd := pipe.Get(ctx, fmt.Sprintf("sessionID:%s", sessionID))
	affinityCmd := pipe.Get(ctx, fmt.Sprintf("pfx:%s", prefixHash))
	_, err := pipe.Exec(ctx)

	tier, err := tierCmd.Result()
	if err == redis.Nil {

		pipe.HSet(ctx, key, map[string]interface{}{
			"user_id": userID,
			"model":   string(model),
		})
		pipe.Expire(ctx, key, ttl)
		if _, err := pipe.Exec(ctx); err != nil {
			slog.Warn("failed to set session in redis", "key", key, "err", err)
			return false
		}
		return true
	}
}
