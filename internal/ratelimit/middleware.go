package ratelimit

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Dhairya0531/API_Gateway/internal/middleware"
)

// RouteLimitConfig holds per-route rate limit settings.
type RouteLimitConfig struct {
	RequestsPerMinute int
}

// Middleware creates an HTTP middleware that enforces per-user, per-route rate limits.
//
// How it works:
//  1. Extract user identity from the Authorization header (or fall back to IP)
//  2. Build a rate limit key: "ratelimit:{identity}:{routePrefix}"
//  3. Check the sliding window limiter
//  4. If allowed: set X-RateLimit-* headers and proceed
//  5. If denied: return 429 Too Many Requests with Retry-After
//
// The rate limits are configured per-route in config.yaml:
//
//	routes:
//	  - path: /payments
//	    rate_limit:
//	      requests_per_minute: 10
func Middleware(limiter *SlidingWindow, routeLimits map[string]RouteLimitConfig, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Find the matching route config for this path
			var matchedPrefix string
			var cfg RouteLimitConfig
			found := false

			for prefix, rc := range routeLimits {
				if strings.HasPrefix(r.URL.Path, prefix) {
					if len(prefix) > len(matchedPrefix) {
						matchedPrefix = prefix
						cfg = rc
						found = true
					}
				}
			}

			// No rate limit configured for this route — pass through
			if !found || cfg.RequestsPerMinute == 0 {
				next.ServeHTTP(w, r)
				return
			}

			// Extract user identity for per-user limiting
			identity := extractIdentity(r)
			key := fmt.Sprintf("ratelimit:%s:%s", identity, matchedPrefix)

			result, err := limiter.Allow(
				r.Context(),
				key,
				cfg.RequestsPerMinute,
				time.Minute,
			)
			if err != nil {
				// Redis error — log and allow the request (fail open)
				// Better to let some excess traffic through than block everyone
				log.Error("rate limit check failed",
					slog.String("request_id", middleware.GetRequestID(r.Context())),
					slog.String("error", err.Error()),
				)
				next.ServeHTTP(w, r)
				return
			}

			// Always set rate limit headers (even on allowed requests)
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(result.Limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))

			if !result.Allowed {
				retryAfterSecs := int(result.RetryAfter.Seconds())
				if retryAfterSecs < 1 {
					retryAfterSecs = 1
				}

				w.Header().Set("Retry-After", strconv.Itoa(retryAfterSecs))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprintf(w, `{"error":"rate limit exceeded","retry_after_seconds":%d}`, retryAfterSecs)

				log.Warn("rate limited",
					slog.String("request_id", middleware.GetRequestID(r.Context())),
					slog.String("identity", identity),
					slog.String("path", r.URL.Path),
					slog.Int("limit", result.Limit),
				)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}



// extractIdentity determines who the request is from.
// Priority: Bearer token → IP address.
// In production, you'd extract a user ID from a decoded JWT.
func extractIdentity(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth != "" {
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) == 2 {
			return parts[1] // use token as identity
		}
	}
	// Fall back to IP address (strip port)
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}
