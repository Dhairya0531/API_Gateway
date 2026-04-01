package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/Dhairya0531/API_Gateway/internal/balancer"
	"github.com/Dhairya0531/API_Gateway/internal/circuitbreaker"
	"github.com/Dhairya0531/API_Gateway/internal/middleware"
)

// ReverseProxy wraps httputil.ReverseProxy with our load balancer.
// For each request it:
//  1. Picks a healthy upstream from the pool (round-robin)
//  2. Rewrites the request URL to point at that upstream
//  3. Forwards X-Forwarded-For and X-Request-ID headers
//  4. Streams the upstream response back to the client
type ReverseProxy struct {
	pool   *balancer.Pool
	log    *slog.Logger
	proxy  *httputil.ReverseProxy
	transport *http.Transport
}

type contextKey string
const upstreamCtxKey contextKey = "upstream"
const startTimeCtxKey contextKey = "start_time"

// New creates a ReverseProxy for a given upstream pool.
// The transport is tuned for low-latency gateway use:
//   - Short dial timeout: fail fast on dead upstreams
//   - ResponseHeaderTimeout: abort if upstream is too slow to start responding
//   - Connection pooling: reuse TCP connections across requests (huge perf win)
func New(pool *balancer.Pool, cbManager *circuitbreaker.Manager, log *slog.Logger) *ReverseProxy {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   2 * time.Second,  // max time to establish TCP connection
			KeepAlive: 30 * time.Second, // TCP keepalive interval
		}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second, // time to receive first response byte
		TLSHandshakeTimeout:   5 * time.Second,
	}

	rp := &ReverseProxy{
		pool:      pool,
		log:       log,
		transport: transport,
	}

	// Wrap the base transport with our resilience layer (retries, circuit breaker)
	gatewayTransport := NewGatewayTransport(transport, cbManager, log)

	proxy := &httputil.ReverseProxy{
		Director:       rp.director,
		Transport:      gatewayTransport,
		ErrorHandler:   rp.errorHandler,
		ModifyResponse: rp.modifyResponse,
	}

	rp.proxy = proxy
	return rp
}

// ServeHTTP implements http.Handler — this is where requests enter the proxy.
func (rp *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := context.WithValue(r.Context(), startTimeCtxKey, time.Now())
	rp.proxy.ServeHTTP(w, r.WithContext(ctx))
}

// director modifies the outgoing request before it is sent to the upstream.
// This is called by httputil.ReverseProxy on every request.
func (rp *ReverseProxy) director(req *http.Request) {
	// Pick a healthy upstream
	upstream, err := rp.pool.Next()
	if err != nil {
		// No healthy upstream — we can't do much in director, so we set a flag
		// The errorHandler will catch the resulting error
		rp.log.Error("no healthy upstream available",
			slog.String("request_id", middleware.GetRequestID(req.Context())),
			slog.String("path", req.URL.Path),
		)
		// Force an invalid URL so the transport fails with a clear error
		req.URL, _ = url.Parse("http://0.0.0.0:0")
		return
	}

	// Rewrite request URL to point at the upstream
	target, err := url.Parse(upstream.URL)
	if err != nil {
		rp.log.Error("invalid upstream URL",
			slog.String("url", upstream.URL),
			slog.String("error", err.Error()),
		)
		return
	}

	req.URL.Scheme = target.Scheme
	req.URL.Host = target.Host
	req.Host = target.Host

	// Track connection for least-connections / EWMA
	upstream.IncrConns()

	// Add to context so modifyResponse and errorHandler can access it
	ctx := context.WithValue(req.Context(), upstreamCtxKey, upstream)
	*req = *req.WithContext(ctx)

	// Add X-Forwarded-For: append client IP to any existing value
	// This lets upstream services know the real client IP
	clientIP, _, err := net.SplitHostPort(req.RemoteAddr)
	if err == nil {
		prior := req.Header.Get("X-Forwarded-For")
		if prior == "" {
			req.Header.Set("X-Forwarded-For", clientIP)
		} else {
			req.Header.Set("X-Forwarded-For", prior+", "+clientIP)
		}
	}

	// Forward request ID to upstream for cross-service tracing
	if rid := middleware.GetRequestID(req.Context()); rid != "" {
		req.Header.Set("X-Request-ID", rid)
	}

	rp.log.Debug("forwarding request",
		slog.String("request_id", middleware.GetRequestID(req.Context())),
		slog.String("upstream", upstream.URL),
		slog.String("path", req.URL.Path),
	)
}

// errorHandler is called when the upstream request fails entirely
// (connection refused, timeout, etc.)
func (rp *ReverseProxy) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	if upstream, ok := r.Context().Value(upstreamCtxKey).(*balancer.Upstream); ok {
		upstream.DecrConns()
	}

	rp.log.Error("upstream error",
		slog.String("request_id", middleware.GetRequestID(r.Context())),
		slog.String("path", r.URL.Path),
		slog.String("error", err.Error()),
	)

	// Distinguish connection errors (502) from timeouts (504)
	if isTimeoutError(err) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGatewayTimeout)
		fmt.Fprint(w, `{"error":"upstream timeout"}`)
		return
	}

	// For other errors (like no healthy upstream or circuit breaker open), gracefully degrade
	writeDegradedResponse(w, r)
}

// modifyResponse can alter the upstream response before returning to client.
// Currently just a passthrough — add response header manipulation here.
func (rp *ReverseProxy) modifyResponse(resp *http.Response) error {
	ctx := resp.Request.Context()
	
	if upstream, ok := ctx.Value(upstreamCtxKey).(*balancer.Upstream); ok {
		upstream.DecrConns()
		if start, ok := ctx.Value(startTimeCtxKey).(time.Time); ok {
			upstream.RecordLatency(time.Since(start))
		}
	}

	// Add a header to indicate this response came through our gateway
	resp.Header.Set("X-Gateway", "api-gateway/v1")
	return nil
}

// isTimeoutError detects context deadline exceeded or net timeout errors
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	netErr, ok := err.(net.Error)
	return ok && netErr.Timeout()
}
