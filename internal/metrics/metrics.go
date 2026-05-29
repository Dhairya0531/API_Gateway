package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Dhairya0531/API_Gateway/internal/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// RequestCount tracks total HTTP requests processed by the gateway
	RequestCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total number of HTTP requests processed by the API gateway.",
		},
		[]string{"method", "path", "status"},
	)

	// RequestDuration tracks the latency of requests
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_request_duration_seconds",
			Help:    "Latency of HTTP requests in seconds.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"method", "path"},
	)

	// UpstreamConns tracks active connections to upstreams
	UpstreamConns = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_upstream_active_connections",
			Help: "Current number of active connections to upstream services.",
		},
		[]string{"service", "upstream_url"},
	)

	// UpstreamHealth indicates whether an upstream is healthy (1) or unhealthy (0).
	// Documented in `internal/metrics/prometheus.go` but implemented here.
	UpstreamHealth = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_upstream_health",
			Help: "Health status of upstream services (1=healthy, 0=unhealthy).",
		},
		[]string{"service", "host"},
	)

	// RateLimitHits counts rate limit events per user and route.
	RateLimitHits = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_rate_limit_hits_total",
			Help: "Total number of rate limit events by user and route.",
		},
		[]string{"user", "route"},
	)
)

// Middleware creates an HTTP middleware that records Prometheus metrics.
func Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := &statusCapture{ResponseWriter: w}

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start).Seconds()
			statusStr := strconv.Itoa(wrapped.status())

			// Use matched route pattern when available to avoid label cardinality explosion
			// (e.g., /users/123 -> /users). Router sets the pattern in context.
			path := middleware.GetRoutePattern(r.Context())
			if path == "" {
				path = r.URL.Path
			}

			RequestCount.WithLabelValues(r.Method, path, statusStr).Inc()
			RequestDuration.WithLabelValues(r.Method, path).Observe(duration)
		})
	}
}

// Handler returns the Prometheus metrics HTTP handler to be exposed on /metrics
func Handler() http.Handler {
	return promhttp.Handler()
}

// statusCapture wraps ResponseWriter to capture the HTTP status code.
type statusCapture struct {
	http.ResponseWriter
	code        int
	wroteHeader bool
}

func (sc *statusCapture) WriteHeader(code int) {
	if !sc.wroteHeader {
		sc.code = code
		sc.wroteHeader = true
		sc.ResponseWriter.WriteHeader(code)
	}
}

func (sc *statusCapture) status() int {
	if sc.code == 0 {
		return http.StatusOK
	}
	return sc.code
}
