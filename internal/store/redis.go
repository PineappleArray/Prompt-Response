package store

import (
	"context"
	"fmt"
	"time"

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
	r.client.Set(ctx, key, replicaID, ttl)
}