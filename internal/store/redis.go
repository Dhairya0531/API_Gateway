package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisClient wraps the go-redis client with logging and convenience methods.
// Used by: rate limiting (sorted sets), idempotency (key-value), caching (key-value).
type RedisClient struct {
	client *redis.Client
	log    *slog.Logger
}

// RedisConfig holds connection parameters (populated from config.yaml).
type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

// NewRedis creates a Redis client and verifies connectivity.
// Returns an error if Redis is unreachable — the gateway should not start
// without Redis since rate limiting and idempotency depend on it.
func NewRedis(cfg RedisConfig, log *slog.Logger) (*RedisClient, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     20,
		MinIdleConns: 5,
	})

	// Verify connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	log.Info("redis connected", slog.String("addr", cfg.Addr))

	return &RedisClient{client: client, log: log}, nil
}

// Client exposes the underlying go-redis client for packages that need
// direct access (e.g., Lua scripts in rate limiting).
func (r *RedisClient) Client() *redis.Client {
	return r.client
}

// Close shuts down the Redis connection pool gracefully.
func (r *RedisClient) Close() error {
	r.log.Info("closing redis connection")
	return r.client.Close()
}

// Get retrieves a value by key. Returns ("", false) if the key doesn't exist.
func (r *RedisClient) Get(ctx context.Context, key string) (string, bool, error) {
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("redis get %q: %w", key, err)
	}
	return val, true, nil
}

// Set stores a key-value pair with a TTL.
func (r *RedisClient) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return r.client.Set(ctx, key, value, ttl).Err()
}

// Del deletes one or more keys.
func (r *RedisClient) Del(ctx context.Context, keys ...string) error {
	return r.client.Del(ctx, keys...).Err()
}

// Exists checks if a key exists. Returns true if it does.
func (r *RedisClient) Exists(ctx context.Context, key string) (bool, error) {
	n, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
