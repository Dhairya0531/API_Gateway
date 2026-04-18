package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// SlidingWindow implements a sliding window rate limiter using Redis sorted sets.
//
// Why sliding window instead of fixed window?
//   Fixed window (INCR + EXPIRE) has a well-known boundary problem:
//   At the edge of two windows, a user can burst 2x the limit.
//   Example: limit=100/min, user sends 100 at 0:59 and 100 at 1:00 → 200 in 2 seconds.
//
//   Sliding window uses a ZSET where each request is a member scored by timestamp.
//   We remove entries older than the window, then count remaining.
//   This gives accurate rate limiting regardless of when the window boundary falls.
//
// Implementation uses a Redis Lua script for atomicity — the entire
// check-count-add operation runs as a single command (no race conditions).
type SlidingWindow struct {
	client *redis.Client
	script *redis.Script
}

// Result contains the outcome of a rate limit check.
type Result struct {
	Allowed    bool          // whether the request is permitted
	Remaining  int           // how many requests are left in the window
	RetryAfter time.Duration // how long to wait before retrying (only if !Allowed)
	Limit      int           // the configured limit
}

// slidingWindowScript is the Lua script that runs atomically in Redis.
//
// KEYS[1] = the rate limit key (e.g., "ratelimit:user123:/payments")
// ARGV[1] = current timestamp in microseconds
// ARGV[2] = window size in microseconds
// ARGV[3] = max allowed requests in the window
// ARGV[4] = unique member ID (timestamp + random suffix to avoid collisions)
//
// Returns: {allowed (0 or 1), current_count}
var slidingWindowScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]

-- Remove all entries outside the current window
redis.call('ZREMRANGEBYSCORE', key, 0, now - window)

-- Count entries in the current window
local count = redis.call('ZCARD', key)

if count < limit then
    -- Under limit: add this request and set expiry
    redis.call('ZADD', key, now, member)
    redis.call('PEXPIRE', key, math.ceil(window / 1000))
    return {1, count + 1}
end

-- Over limit: reject
return {0, count}
`)

// NewSlidingWindow creates a sliding window rate limiter backed by Redis.
func NewSlidingWindow(client *redis.Client) *SlidingWindow {
	return &SlidingWindow{
		client: client,
		script: slidingWindowScript,
	}
}

// Allow checks whether a request identified by `key` is within the rate limit.
//
// Parameters:
//   - key:    unique identifier (e.g., "ratelimit:{userID}:{path}")
//   - limit:  maximum requests allowed in the window
//   - window: time window duration
//
// The operation is atomic — concurrent requests are safely handled by Redis.
func (sw *SlidingWindow) Allow(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	now := time.Now().UnixMicro()
	windowMicro := window.Microseconds()

	// Unique member: timestamp + nanosecond suffix to prevent collisions
	// when multiple requests arrive in the same microsecond
	member := fmt.Sprintf("%d-%d", now, time.Now().UnixNano())

	raw, err := sw.script.Run(ctx, sw.client, []string{key}, now, windowMicro, limit, member).Int64Slice()
	if err != nil {
		return Result{}, fmt.Errorf("rate limit script: %w", err)
	}

	allowed := raw[0] == 1
	count := int(raw[1])
	remaining := limit - count
	if remaining < 0 {
		remaining = 0
	}

	result := Result{
		Allowed:   allowed,
		Remaining: remaining,
		Limit:     limit,
	}

	if !allowed {
		// Calculate when the oldest entry in the window will expire
		// This gives the client a useful Retry-After value
		oldestScore, err := sw.client.ZRangeWithScores(ctx, key, 0, 0).Result()
		if err == nil && len(oldestScore) > 0 {
			oldestTime := time.UnixMicro(int64(oldestScore[0].Score))
			expiresAt := oldestTime.Add(window)
			retryAfter := time.Until(expiresAt)
			if retryAfter < 0 {
				retryAfter = time.Second
			}
			result.RetryAfter = retryAfter
		} else {
			result.RetryAfter = time.Second
		}
	}

	return result, nil
}
