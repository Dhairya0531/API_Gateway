package proxy

import (
	"context"
	"errors"
	"time"

	"github.com/Dhairya0531/API_Gateway/internal/balancer"
)

// RetryPolicy defines how to retry failed requests.
type RetryPolicy struct {
	MaxRetries    int
	BackoffBase   time.Duration
	BackoffMax    time.Duration
}

// DefaultRetryPolicy is suitable for gateway requests.
var DefaultRetryPolicy = RetryPolicy{
	MaxRetries:  2,
	BackoffBase: 100 * time.Millisecond,
	BackoffMax:  500 * time.Millisecond,
}

// Retryer handles exponential backoff retries for safe HTTP methods.
type Retryer struct {
	policy RetryPolicy
}

func NewRetryer(policy RetryPolicy) *Retryer {
	return &Retryer{policy: policy}
}

// Execute runs the provided function with retries.
// The fn is expected to pick a NEW upstream each time, and return the chosen upstream
// so we can record failures against it.
func (r *Retryer) Execute(ctx context.Context, method string, fn func() (*balancer.Upstream, error)) error {
	// Only retry safe, idempotent methods
	if !isSafeMethod(method) {
		_, err := fn()
		return err
	}

	var lastErr error
	backoff := r.policy.BackoffBase

	for attempt := 0; attempt <= r.policy.MaxRetries; attempt++ {
		// Check context deadline before attempting
		if ctx.Err() != nil {
			return ctx.Err()
		}

		_, err := fn()
		if err == nil {
			return nil // Success!
		}

		lastErr = err

		// Don't retry on the last attempt
		if attempt == r.policy.MaxRetries {
			break
		}

		// Check if the error is retryable (e.g. timeout, connection refused)
		if !isRetryableError(err) {
			break
		}

		// Wait before next attempt (exponential backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			// Proceed to next attempt
			backoff *= 2
			if backoff > r.policy.BackoffMax {
				backoff = r.policy.BackoffMax
			}
		}
	}

	return lastErr
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	
	// Example: don't retry context canceled
	if errors.Is(err, context.Canceled) {
		return false
	}

	// For a simple implementation, retry any other error 
	// (usually connection refused, timeout, 502 Bad Gateway from upstream)
	return true
}
