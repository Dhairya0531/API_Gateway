package middleware

import (
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// TracingMiddleware adds OpenTelemetry distributed tracing to the request.
// It extracts incoming trace contexts from headers, starts a new span for the gateway,
// and injects the context into the downstream request.
func TracingMiddleware() func(http.Handler) http.Handler {
	tracer := otel.Tracer("github.com/Dhairya0531/API_Gateway")
	propagators := otel.GetTextMapPropagator()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract trace context from incoming headers
			ctx := propagators.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			// Start a new span
			ctx, span := tracer.Start(ctx, r.URL.Path, trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.url", r.URL.String()),
				attribute.String("http.user_agent", r.UserAgent()),
			))
			defer span.End()

			// Create a response writer wrapper to capture status code
			wrappedWriter := &responseWriterInterceptor{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			// Pass the new context down the chain
			r = r.WithContext(ctx)

			// Inject the trace context into the outbound headers for downstream services
			propagators.Inject(ctx, propagation.HeaderCarrier(r.Header))

			next.ServeHTTP(wrappedWriter, r)

			// Record the final status code in the span
			span.SetAttributes(attribute.Int("http.status_code", wrappedWriter.statusCode))
			if wrappedWriter.statusCode >= 500 {
				span.RecordError(nil) // Mark span as error implicitly via status code
			}
		})
	}
}

type responseWriterInterceptor struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriterInterceptor) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
