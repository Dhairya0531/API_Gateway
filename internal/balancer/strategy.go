package balancer

import (
	"errors"
	"math"
	"sync/atomic"
)

// Strategy defines how the pool picks the next upstream.
// Each implementation offers different trade-offs:
//   - RoundRobin:        simple, fair, ignores backend state
//   - LeastConnections:  routes to the least busy upstream (best for heterogeneous backends)
//   - WeightedLatency:   routes to the fastest upstream using EWMA (best for latency-sensitive workloads)
type Strategy interface {
	Pick(upstreams []*Upstream) (*Upstream, error)
	Name() string
}

// ─── Round Robin ──────────────────────────────────────────────────────────────

// RoundRobinStrategy distributes requests evenly across all healthy upstreams
// using an atomic counter. Simple and fair, but doesn't account for backend load.
type RoundRobinStrategy struct {
	counter atomic.Uint64
}

func NewRoundRobin() *RoundRobinStrategy {
	return &RoundRobinStrategy{}
}

func (rr *RoundRobinStrategy) Name() string { return "round-robin" }

func (rr *RoundRobinStrategy) Pick(upstreams []*Upstream) (*Upstream, error) {
	total := uint64(len(upstreams))
	if total == 0 {
		return nil, errors.New("no upstreams available")
	}

	start := rr.counter.Add(1) - 1
	for i := uint64(0); i < total; i++ {
		idx := (start + i) % total
		u := upstreams[idx]
		if u.Healthy {
			return u, nil
		}
	}

	return nil, ErrNoHealthyUpstream
}

// ─── Least Connections ────────────────────────────────────────────────────────

// LeastConnectionsStrategy routes to the healthy upstream with the fewest
// in-flight requests. This naturally adapts to heterogeneous backends:
// faster servers complete requests sooner, freeing up connections, and
// therefore receive more traffic.
//
// Why this beats round-robin:
//   If upstream-1 handles requests in 5ms and upstream-2 takes 500ms,
//   round-robin gives them equal traffic. upstream-2 accumulates a massive
//   backlog while upstream-1 sits idle. Least-connections fixes this.
type LeastConnectionsStrategy struct{}

func NewLeastConnections() *LeastConnectionsStrategy {
	return &LeastConnectionsStrategy{}
}

func (lc *LeastConnectionsStrategy) Name() string { return "least-connections" }

func (lc *LeastConnectionsStrategy) Pick(upstreams []*Upstream) (*Upstream, error) {
	var best *Upstream
	bestConns := int64(math.MaxInt64)

	for _, u := range upstreams {
		if !u.Healthy {
			continue
		}
		conns := u.ActiveConns.Load()
		if conns < bestConns {
			bestConns = conns
			best = u
		}
	}

	if best == nil {
		return nil, ErrNoHealthyUpstream
	}
	return best, nil
}

// ─── Weighted Latency (EWMA) ─────────────────────────────────────────────────

// WeightedLatencyStrategy routes to the upstream with the lowest
// Exponentially Weighted Moving Average (EWMA) latency.
//
// EWMA formula: newAvg = α × latestSample + (1 - α) × previousAvg
//
// With α = 0.3:
//   - Recent samples get ~30% weight (responsive to changes)
//   - History gets ~70% weight (smooths out spikes)
//
// This is the same algorithm used in TCP congestion control (RTT estimation),
// Netflix's load balancer, and Envoy proxy.
type WeightedLatencyStrategy struct {
	counter atomic.Uint64 // tiebreaker when latencies are similar
}

func NewWeightedLatency() *WeightedLatencyStrategy {
	return &WeightedLatencyStrategy{}
}

func (wl *WeightedLatencyStrategy) Name() string { return "weighted-latency" }

func (wl *WeightedLatencyStrategy) Pick(upstreams []*Upstream) (*Upstream, error) {
	var best *Upstream
	bestLatency := math.MaxFloat64

	for _, u := range upstreams {
		if !u.Healthy {
			continue
		}

		latency := u.GetLatencyEWMA()

		// If no latency data yet (new upstream), give it a chance
		// by assigning a neutral score (0 = fastest possible)
		if latency < bestLatency {
			bestLatency = latency
			best = u
		} else if latency == bestLatency && best != nil {
			// Tiebreaker: use round-robin among equal-latency upstreams
			// to prevent all traffic going to the first one in the list
			if wl.counter.Add(1)%2 == 0 {
				best = u
			}
		}
	}

	if best == nil {
		return nil, ErrNoHealthyUpstream
	}
	return best, nil
}

// StrategyFromName creates a Strategy from a config string.
// Defaults to round-robin if the name is unrecognized.
func StrategyFromName(name string) Strategy {
	switch name {
	case "least-connections", "least-conn":
		return NewLeastConnections()
	case "weighted-latency", "ewma":
		return NewWeightedLatency()
	default:
		return NewRoundRobin()
	}
}
