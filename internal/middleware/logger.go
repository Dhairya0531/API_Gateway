package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// responseWriter wraps http.ResponseWriter to capture the status code
// after it has been written. The standard ResponseWriter doesn't expose this.
type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func wrapResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{ResponseWriter: w}
}

func (rw *responseWriter) Status() int {
	if rw.status == 0 {
		return http.StatusOK // default when WriteHeader was never called
	}
	return rw.status
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.status = code
		rw.wroteHeader = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

// Logger logs every request as a structured JSON line containing:
// request_id, method, path, status, latency_ms, remote_addr
//
// This must come AFTER the RequestID middleware in the chain
// so that the request_id is available in context.
func Logger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := wrapResponseWriter(w)

			// Serve the request
			next.ServeHTTP(wrapped, r)

			// Log after response is written — we now know status + latency
			log.Info("request",
				slog.String("request_id", GetRequestID(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", wrapped.Status()),
				slog.Int64("latency_ms", time.Since(start).Milliseconds()),
				slog.String("remote_addr", r.RemoteAddr),
				slog.String("user_agent", r.UserAgent()),
			)
		})
	}
}
