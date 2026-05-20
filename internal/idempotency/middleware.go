package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Dhairya0531/API_Gateway/internal/middleware"
	"github.com/Dhairya0531/API_Gateway/internal/store"
)

// CachedResponse represents the data we store in Redis for an idempotent request.
type CachedResponse struct {
	Status      int                 `json:"status"`
	Headers     map[string][]string `json:"headers"`
	Body        string              `json:"body"`
	RequestHash string              `json:"request_hash"`
}

// Middleware creates an HTTP middleware that enforces idempotency for POST/PUT/PATCH requests.
//
// How it works:
//  1. Client sends POST /payments with header: Idempotency-Key: <uuid>
//  2. Middleware checks Redis for the key
//  3. If found and request body matches: return cached response (no upstream call)
//  4. If found but request body differs: return 422 Unprocessable Entity
//  5. If not found: forward to upstream, buffer response, cache in Redis (24hr TTL), return to client
func Middleware(redisClient *store.RedisClient, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Idempotency only applies to mutating methods
			if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch {
				next.ServeHTTP(w, r)
				return
			}

			idemKey := r.Header.Get("Idempotency-Key")
			if idemKey == "" {
				// No key provided, proceed normally (or you could strictly enforce it)
				next.ServeHTTP(w, r)
				return
			}

			// Read and hash the request body to ensure the client isn't reusing
			// the same key for a different payload.
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
				return
			}
			// Restore the body so downstream handlers can read it
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			reqHash := hashBody(bodyBytes)
			redisKey := fmt.Sprintf("idempotency:%s", idemKey)

			// 1. Check if we already have a response for this key
			val, found, err := redisClient.Get(r.Context(), redisKey)
			if err != nil {
				log.Error("redis get failed for idempotency key",
					slog.String("request_id", middleware.GetRequestID(r.Context())),
					slog.String("error", err.Error()),
				)
				// Fail open — proceed to upstream
				next.ServeHTTP(w, r)
				return
			}

			if found {
				var cached CachedResponse
				if err := json.Unmarshal([]byte(val), &cached); err != nil {
					log.Error("failed to unmarshal cached response", slog.String("error", err.Error()))
					next.ServeHTTP(w, r)
					return
				}

				// Check if the payload matches
				if cached.RequestHash != reqHash {
					log.Warn("idempotency key reused with different payload",
						slog.String("request_id", middleware.GetRequestID(r.Context())),
						slog.String("idempotency_key", idemKey),
					)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnprocessableEntity)
					if _, err := fmt.Fprintf(w, `{"error":"idempotency key reused with different payload"}`); err != nil {
						log.Warn("failed to write idempotency error response", slog.String("error", err.Error()))
					}
					return
				}

				// Return the cached response
				for k, vals := range cached.Headers {
					for _, v := range vals {
						w.Header().Add(k, v)
					}
				}
				w.Header().Set("X-Idempotency-Cached", "true")
				w.WriteHeader(cached.Status)
				if _, err := w.Write([]byte(cached.Body)); err != nil {
					log.Warn("failed to write cached idempotent response", slog.String("error", err.Error()))
				}

				log.Info("served idempotent request from cache",
					slog.String("request_id", middleware.GetRequestID(r.Context())),
					slog.String("idempotency_key", idemKey),
				)
				return
			}

			// 2. Key not found — we need to capture the response
			rec := &responseRecorder{
				ResponseWriter: w,
				body:           &bytes.Buffer{},
				status:         http.StatusOK, // default if WriteHeader is not called
			}

			// Proceed to upstream
			next.ServeHTTP(rec, r)

			// 3. Cache the response in Redis (only if successful 2xx or 3xx)
			if rec.status >= 200 && rec.status < 400 {
				cached := CachedResponse{
					Status:      rec.status,
					Headers:     rec.Header(),
					Body:        rec.body.String(),
					RequestHash: reqHash,
				}

				cachedJSON, _ := json.Marshal(cached)
				
				// Run in background to not block the client response
				go func(ctx context.Context, key string, data []byte) {
					// Need a new context since the request context is cancelled when handler returns
					cacheCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					
					if err := redisClient.Set(cacheCtx, key, string(data), 24*time.Hour); err != nil {
						log.Error("failed to cache idempotent response", slog.String("error", err.Error()))
					}
				}(r.Context(), redisKey, cachedJSON)
			}
		})
	}
}

func hashBody(body []byte) string {
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
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
