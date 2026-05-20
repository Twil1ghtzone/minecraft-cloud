package database

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// NewRedisCluster returns a sharded Redis cluster client used by the
// player state-sync engine (Module 6) and ephemeral session storage.
func NewRedisCluster(addrs []string, password string) *redis.ClusterClient {
	return redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:    addrs,
		Password: password,
	})
}

// Ping verifies cluster connectivity.
func PingRedis(ctx context.Context, c *redis.ClusterClient) error {
	return c.Ping(ctx).Err()
}
