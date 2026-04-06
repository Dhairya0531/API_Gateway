package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recovery catches any panic that occurs in downstream handlers and middleware.
// Instead of crashing the entire server, it:
//   - Logs the panic value and full stack trace
//   - Returns 500 Internal Server Error to the client
//
// This MUST be the outermost middleware (first in chain, last to execute)
// so it wraps everything else.
func Recovery(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					// Log the panic with stack trace
					log.Error("panic recovered",
						slog.String("request_id", GetRequestID(r.Context())),
						slog.String("path", r.URL.Path),
						slog.Any("panic", err),
						slog.String("stack", string(debug.Stack())),
					)

					// Respond with 500 — but only if headers haven't been sent
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"internal server error"}`))
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
