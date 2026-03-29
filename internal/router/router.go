package router

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Dhairya0531/API_Gateway/internal/balancer"
	"github.com/Dhairya0531/API_Gateway/internal/circuitbreaker"
	cfg "github.com/Dhairya0531/API_Gateway/internal/config"
	"github.com/Dhairya0531/API_Gateway/internal/middleware"
	"github.com/Dhairya0531/API_Gateway/internal/proxy"
)

// route holds everything needed to handle requests for a path prefix
type route struct {
	prefix  string
	timeout time.Duration
	proxy   *proxy.ReverseProxy
}

// Router matches incoming requests by URL path prefix and
// forwards them to the appropriate upstream pool via ReverseProxy.
//
// Matching is longest-prefix: /payments/refund matches /payments before /
// This is the same scheme used by NGINX location blocks.
type Router struct {
	routes atomic.Pointer[[]*route]
	log    *slog.Logger
}

// New builds the router from config routes + pre-built pools.
// The pools map (service name → *balancer.Pool) must be built before calling New.
func New(
	routes []cfg.RouteConfig,
	pools map[string]*balancer.Pool,
	cbManager *circuitbreaker.Manager,
	log *slog.Logger,
) (*Router, error) {
	r := &Router{log: log}
	if err := r.UpdateConfig(routes, pools, cbManager); err != nil {
		return nil, err
	}
	return r, nil
}

// UpdateConfig hot-swaps the routing table dynamically in a thread-safe, lock-free manner.
func (r *Router) UpdateConfig(
	routes []cfg.RouteConfig,
	pools map[string]*balancer.Pool,
	cbManager *circuitbreaker.Manager,
) error {
	var newRoutes []*route

	for _, routeCfg := range routes {
		pool, ok := pools[routeCfg.Service]
		if !ok {
			return fmt.Errorf("no pool found for service %q (route: %s)", routeCfg.Service, routeCfg.Path)
		}

		timeout := routeCfg.Timeout
		if timeout == 0 {
			timeout = 10 * time.Second
		}

		newRoutes = append(newRoutes, &route{
			prefix:  routeCfg.Path,
			timeout: timeout,
			proxy:   proxy.New(pool, cbManager, r.log),
		})

		r.log.Info("route registered",
			slog.String("path", routeCfg.Path),
			slog.String("service", routeCfg.Service),
			slog.Duration("timeout", timeout),
		)
	}

	r.routes.Store(&newRoutes)
	return nil
}

// ServeHTTP implements http.Handler — the gateway's main dispatch function.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	route := r.match(req.URL.Path)
	if route == nil {
		r.log.Warn("no route matched",
			slog.String("request_id", middleware.GetRequestID(req.Context())),
			slog.String("path", req.URL.Path),
		)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"error":"no route for path %q"}`, req.URL.Path)
		return
	}

	// Apply per-route timeout via context
	// If the upstream doesn't respond in time, context.Done() fires
	// and the ReverseProxy aborts the request
	ctx, cancel := context.WithTimeout(req.Context(), route.timeout)
	defer cancel()

	route.proxy.ServeHTTP(w, req.WithContext(ctx))
}

// match finds the route with the longest matching prefix.
// e.g. "/payments/refund" → matches "/payments" over "/"
func (r *Router) match(path string) *route {
	var best *route
	bestLen := -1

	routesPtr := r.routes.Load()
	if routesPtr == nil {
		return nil
	}

	for _, rt := range *routesPtr {
		if strings.HasPrefix(path, rt.prefix) {
			if len(rt.prefix) > bestLen {
				bestLen = len(rt.prefix)
				best = rt
			}
		}
	}

	return best
}
