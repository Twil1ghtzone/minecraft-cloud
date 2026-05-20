// Package database — redis.go
// Full Redis Cluster client with distributed lock helpers.
package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisClient wraps the go-redis cluster client with AetherNet-specific helpers.
type RedisClient struct {
	rdb *redis.ClusterClient
}

// NewRedisClient connects to a Redis cluster (or single node if one addr given).
func NewRedisClient(addrs []string) (*RedisClient, error) {
	if len(addrs) == 0 {
		return nil, errors.New("redis: no addresses configured")
	}
	var rdb *redis.ClusterClient
	if len(addrs) == 1 {
		// Single node: wrap as a cluster client pointed at one node
		rdb = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs: addrs,
		})
	} else {
		rdb = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:          addrs,
			ReadOnly:       false,
			RouteByLatency: true,
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis: ping failed: %w", err)
	}
	return &RedisClient{rdb: rdb}, nil
}

// Close shuts down the Redis connection pool.
func (r *RedisClient) Close() error {
	return r.rdb.Close()
}

// SetNX sets key to value with ttl only if it does NOT already exist.
// Returns true if the key was set (lock acquired).
func (r *RedisClient) SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	return r.rdb.SetNX(ctx, key, value, ttl).Result()
}

// Get retrieves the string value of a key.
func (r *RedisClient) Get(ctx context.Context, key string) (string, error) {
	val, err := r.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return val, err
}

// Set sets a key to value with TTL (0 = no expiry).
func (r *RedisClient) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return r.rdb.Set(ctx, key, value, ttl).Err()
}

// SetBytes sets a key to a binary value.
func (r *RedisClient) SetBytes(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return r.rdb.Set(ctx, key, value, ttl).Err()
}

// GetBytes retrieves binary data.
func (r *RedisClient) GetBytes(ctx context.Context, key string) ([]byte, error) {
	val, err := r.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return val, err
}

// Del deletes one or more keys.
func (r *RedisClient) Del(ctx context.Context, keys ...string) error {
	return r.rdb.Del(ctx, keys...).Err()
}

// Exists returns true if the key exists.
func (r *RedisClient) Exists(ctx context.Context, key string) (bool, error) {
	n, err := r.rdb.Exists(ctx, key).Result()
	return n > 0, err
}

// TTL returns the remaining TTL of a key.
func (r *RedisClient) TTL(ctx context.Context, key string) (time.Duration, error) {
	return r.rdb.TTL(ctx, key).Result()
}

// Publish publishes a message to a channel.
func (r *RedisClient) Publish(ctx context.Context, channel, message string) error {
	return r.rdb.Publish(ctx, channel, message).Err()
}

// AcquireLock attempts to acquire a distributed mutex on key with the given TTL.
// Returns true if acquired. Non-blocking.
func (r *RedisClient) AcquireLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return r.SetNX(ctx, key, "1", ttl)
}

// ReleaseLock releases a lock key unconditionally.
func (r *RedisClient) ReleaseLock(ctx context.Context, key string) error {
	return r.Del(ctx, key)
}

// WaitForLockRelease polls until the lock key no longer exists or ctx is cancelled.
// Used in the anti-exploit stall pattern.
func (r *RedisClient) WaitForLockRelease(ctx context.Context, key string, pollInterval time.Duration) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			exists, err := r.Exists(ctx, key)
			if err != nil {
				return err
			}
			if !exists {
				return nil
			}
		}
	}
}
