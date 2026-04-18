package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestSlidingWindow(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	limiter := NewSlidingWindow(client)
	ctx := context.Background()
	key := "test_limit"
	limit := 3
	window := 1 * time.Second

	// Should allow up to limit
	for i := 0; i < limit; i++ {
		res, err := limiter.Allow(ctx, key, limit, window)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Allowed {
			t.Errorf("expected request %d to be allowed", i+1)
		}
		if res.Remaining != limit-(i+1) {
			t.Errorf("expected %d remaining, got %d", limit-(i+1), res.Remaining)
		}
	}

	// Should reject next
	res, err := limiter.Allow(ctx, key, limit, window)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Allowed {
		t.Error("expected request over limit to be rejected")
	}
	if res.Remaining != 0 {
		t.Errorf("expected 0 remaining, got %d", res.Remaining)
	}
	
	// Advance time to pass the window
	mr.FastForward(2 * time.Second)

	// Should allow again
	res, err = limiter.Allow(ctx, key, limit, window)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Error("expected request to be allowed after window reset")
	}
}
