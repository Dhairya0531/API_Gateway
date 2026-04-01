package proxy

import (
	"log/slog"
	"net/http"

	"github.com/Dhairya0531/API_Gateway/internal/balancer"
	"github.com/Dhairya0531/API_Gateway/internal/circuitbreaker"
	"github.com/Dhairya0531/API_Gateway/internal/middleware"
)

// GatewayTransport wraps an http.RoundTripper to add resilience patterns:
// - Circuit Breaking
// - Retries with backoff
// - Request Coalescing
type GatewayTransport struct {
	base       http.RoundTripper
	cbManager  *circuitbreaker.Manager
	retryer    *Retryer
	coalescer  *Coalescer
	log        *slog.Logger
}

func NewGatewayTransport(base http.RoundTripper, cb *circuitbreaker.Manager, log *slog.Logger) *GatewayTransport {
	return &GatewayTransport{
		base:      base,
		cbManager: cb,
		retryer:   NewRetryer(DefaultRetryPolicy),
		coalescer: NewCoalescer(),
		log:       log,
	}
}

// RoundTrip executes the HTTP request. It intercepts the call from httputil.ReverseProxy.
func (t *GatewayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// 1. Extract upstream info set by the Director
	upstream, ok := req.Context().Value(upstreamCtxKey).(*balancer.Upstream)
	if !ok {
		// Should not happen, but fallback to base if it does
		return t.base.RoundTrip(req)
	}

	// 2. Check Circuit Breaker
	breaker := t.cbManager.GetOrCreate(upstream.URL)
	if !breaker.Allow() {
		t.log.Warn("circuit breaker open",
			slog.String("request_id", middleware.GetRequestID(req.Context())),
			slog.String("upstream", upstream.URL),
		)
		
		// Decrement connections since we incremented in Director
		upstream.DecrConns()
		
		// Return an error so ReverseProxy triggers errorHandler (502 Bad Gateway)
		return nil, ErrCircuitOpen
	}

	// 3. Setup the actual HTTP call
	// We wrap it in a function so it can be passed to Retryer and Coalescer
	doRequest := func() (*http.Response, error) {
		resp, err := t.base.RoundTrip(req)
		
		// Record circuit breaker metrics
		if err != nil || (resp != nil && resp.StatusCode >= 500) {
			breaker.RecordFailure()
		} else {
			breaker.RecordSuccess()
		}
		
		return resp, err
	}

	// 4. Execute with Retry (Note: only safe methods are retried)
	// We don't coalesce here directly because standard http.Response bodies can't be shared.
	// A true coalescer requires buffering the response, which is complex for a proxy.
	// For this 7-day sprint, we implement the structure but skip full HTTP body buffering
	// to avoid memory exhaustion. Retries are fully functional.

	var resp *http.Response
	var err error

	// Retry loop
	err = t.retryer.Execute(req.Context(), req.Method, func() (*balancer.Upstream, error) {
		resp, err = doRequest()
		return upstream, err
	})

	return resp, err
}

// ErrCircuitOpen is returned when the circuit breaker rejects a request
var ErrCircuitOpen = circuitbreaker.ErrCircuitOpen // We need to define this in circuitbreaker
