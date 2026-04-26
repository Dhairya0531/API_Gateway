package health

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Dhairya0531/API_Gateway/internal/balancer"
	cfg "github.com/Dhairya0531/API_Gateway/internal/config"
)

// Checker runs background health probes against all upstream instances.
// It marks upstreams as healthy/unhealthy in their respective pools.
//
// Design decisions:
//   - Runs in its own goroutine — never blocks request handling
//   - Uses a dedicated http.Client with short timeout (don't let probes hang)
//   - RWMutex in Pool means health updates don't block concurrent reads
type Checker struct {
	pools    map[string]*balancer.Pool
	services map[string]cfg.ServiceConfig
	log      *slog.Logger
	client   *http.Client
}

// New creates a new health checker. Call Start() to begin probing.
func New(
	pools map[string]*balancer.Pool,
	services map[string]cfg.ServiceConfig,
	log *slog.Logger,
) *Checker {
	return &Checker{
		pools:    pools,
		services: services,
		log:      log,
		client: &http.Client{
			Timeout: 3 * time.Second, // health probes must be fast
		},
	}
}

// Start launches background health checking. It respects context cancellation
// for graceful shutdown. Call as: go checker.Start(ctx)
func (c *Checker) Start(ctx context.Context) {
	// Build per-service tickers based on config interval
	type serviceTimer struct {
		name    string
		ticker  *time.Ticker
		svcCfg  cfg.ServiceConfig
	}

	timers := make([]serviceTimer, 0, len(c.services))
	for name, svc := range c.services {
		interval := svc.HealthCheck.Interval
		if interval == 0 {
			interval = 10 * time.Second
		}
		timers = append(timers, serviceTimer{
			name:   name,
			ticker: time.NewTicker(interval),
			svcCfg: svc,
		})
	}

	// Run an initial check immediately before waiting for ticks
	c.checkAll()

	c.log.Info("health checker started", slog.Int("services", len(timers)))

	for {
		// Build a combined select over all tickers + context done
		// For simplicity with variable tickers, we use a single 5s poll
		// TODO: per-service ticker in a more advanced implementation
		select {
		case <-ctx.Done():
			c.log.Info("health checker shutting down")
			for _, t := range timers {
				t.ticker.Stop()
			}
			return
		case <-time.After(5 * time.Second):
			c.checkAll()
		}
	}
}

// checkAll probes every upstream in every service pool
func (c *Checker) checkAll() {
	for serviceName, svc := range c.services {
		pool, ok := c.pools[serviceName]
		if !ok {
			continue
		}

		healthPath := svc.HealthCheck.Path
		if healthPath == "" {
			healthPath = "/health"
		}

		for _, upstream := range pool.All() {
			url := upstream.URL + healthPath
			healthy := c.probe(url)

			// Only log state transitions to avoid log noise
			if healthy != upstream.Healthy {
				if healthy {
					c.log.Info("upstream recovered",
						slog.String("service", serviceName),
						slog.String("upstream", upstream.URL),
					)
				} else {
					c.log.Warn("upstream unhealthy",
						slog.String("service", serviceName),
						slog.String("upstream", upstream.URL),
					)
				}
			}

			pool.SetHealthy(upstream.URL, healthy)
		}
	}
}

// probe sends a single GET request to the health endpoint.
// Returns true only on HTTP 200.
func (c *Checker) probe(url string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}
