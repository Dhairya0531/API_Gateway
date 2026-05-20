package cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Dhairya0531/API_Gateway/internal/middleware"
	"github.com/Dhairya0531/API_Gateway/internal/store"
)

// CachedResponse represents the data stored in Redis for a cached response.
type CachedResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

// Middleware creates an HTTP middleware that caches GET responses in Redis.
// Cache keys include the method, path, query params, and Authorization header.
func Middleware(redisClient *store.RedisClient, ttl time.Duration, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only cache GET requests
			if r.Method != http.MethodGet {
				next.ServeHTTP(w, r)
				return
			}

			// Generate cache key
			// Note: We include the Authorization header to prevent serving
			// user A's cached private data to user B.
			keyInput := fmt.Sprintf("%s|%s|%s|%s", r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"))
			hash := sha256.Sum256([]byte(keyInput))
			cacheKey := "cache:" + hex.EncodeToString(hash[:])

			// 1. Try to fetch from cache
			val, found, err := redisClient.Get(r.Context(), cacheKey)
			if err != nil {
				log.Warn("cache get failed", slog.String("error", err.Error()))
			} else if found {
				var cached CachedResponse
				if err := json.Unmarshal([]byte(val), &cached); err == nil {
					// Cache hit!
					for k, vals := range cached.Headers {
						for _, v := range vals {
							w.Header().Add(k, v)
						}
					}
					w.Header().Set("X-Cache", "HIT")
					w.WriteHeader(cached.Status)
					if _, err := w.Write([]byte(cached.Body)); err != nil {
						log.Warn("failed to write cached response", slog.String("error", err.Error()))
					}

					log.Debug("served from cache",
						slog.String("request_id", middleware.GetRequestID(r.Context())),
						slog.String("path", r.URL.Path),
					)
					return
				}
				log.Warn("failed to unmarshal cached response", slog.String("error", err.Error()))
			}

			// 2. Cache miss — fetch from upstream and capture response
			rec := &responseRecorder{
				ResponseWriter: w,
				body:           &bytes.Buffer{},
				status:         http.StatusOK,
			}

			next.ServeHTTP(rec, r)

			// 3. Cache the response (only if successful)
			if rec.status >= 200 && rec.status < 400 {
				cached := CachedResponse{
					Status:  rec.status,
					Headers: rec.Header(),
					Body:    rec.body.String(),
				}

				// Exclude gateway-specific headers from cache
				delete(cached.Headers, "X-Request-Id")
				delete(cached.Headers, "X-Cache")

				cachedJSON, _ := json.Marshal(cached)

				// Write to cache asynchronously
				go func(ctx context.Context, key string, data string) {
					cacheCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()

					if err := redisClient.Set(cacheCtx, key, data, ttl); err != nil {
						log.Error("failed to write to cache", slog.String("error", err.Error()))
					}
				}(r.Context(), cacheKey, string(cachedJSON))
			}

			// If not a hit, mark as miss
			if w.Header().Get("X-Cache") == "" {
				w.Header().Set("X-Cache", "MISS")
			}
		})
	}
}

// responseRecorder implements http.ResponseWriter and buffers the response
// so we can cache it after the upstream returns.
type responseRecorder struct {
	http.ResponseWriter
	body        *bytes.Buffer
	status      int
	wroteHeader bool
}

func (r *responseRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
		r.ResponseWriter.WriteHeader(code)
	}
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}
