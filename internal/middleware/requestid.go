package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// contextKey is an unexported type to avoid key collisions across packages
type contextKey string

const RequestIDKey contextKey = "request_id"

// RequestID generates a UUID for every incoming request and:
//   - Stores it in the request context (for downstream access)
//   - Writes it to the response header (X-Request-ID)
//   - Forwards it to the upstream (X-Request-ID on the outgoing request)
//
// This is the FIRST middleware in the chain so all subsequent
// middleware and handlers have access to the request ID.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Honor existing request ID if present (e.g. from a parent gateway)
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}

		// Store in context
		ctx := context.WithValue(r.Context(), RequestIDKey, requestID)

		// Set on both response and forwarded request headers
		w.Header().Set("X-Request-ID", requestID)
		r.Header.Set("X-Request-ID", requestID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetRequestID retrieves the request ID from the context.
// Returns empty string if not set.
func GetRequestID(ctx context.Context) string {
	id, _ := ctx.Value(RequestIDKey).(string)
	return id
}
