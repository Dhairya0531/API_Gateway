package balancer

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Upstream represents a single backend server instance.
//
// Fields updated concurrently:
//   - Healthy:     by health checker goroutine (protected by Pool.mu)
//   - ActiveConns: by proxy goroutines via atomic operations (no lock needed)
//   - latencyEWMA: by proxy goroutines via atomic operations (stores float64 as uint64 bits)
type Upstream struct {
	URL         string
	Healthy     bool
	ActiveConns atomic.Int64  // current in-flight requests to this upstream
	latencyEWMA atomic.Uint64 // EWMA of response latency in milliseconds (stored as float64 bits)
}

// ewmaAlpha controls how much weight recent latency samples get.
// 0.3 = 30% recent, 70% historical. Responsive but smooth.
const ewmaAlpha = 0.3

// IncrConns increments the active connection count.
// Called when a request is forwarded to this upstream.
func (u *Upstream) IncrConns() {
	u.ActiveConns.Add(1)
}

// DecrConns decrements the active connection count.
// Called when the response is received (or request fails).
func (u *Upstream) DecrConns() {
	u.ActiveConns.Add(-1)
}

// RecordLatency updates the EWMA with a new latency sample.
// Uses atomic operations to store a float64 as uint64 bits — lock-free.
//
// EWMA formula: newAvg = α × sample + (1 - α) × oldAvg
func (u *Upstream) RecordLatency(d time.Duration) {
	sample := float64(d.Milliseconds())
	for {
		oldBits := u.latencyEWMA.Load()
		oldVal := math.Float64frombits(oldBits)

		var newVal float64
		if oldVal == 0 {
			// First sample — use it directly
			newVal = sample
		} else {
			newVal = ewmaAlpha*sample + (1-ewmaAlpha)*oldVal
		}

		newBits := math.Float64bits(newVal)
		if u.latencyEWMA.CompareAndSwap(oldBits, newBits) {
			return
		}
		// CAS failed (another goroutine updated) — retry
	}
}

// GetLatencyEWMA returns the current EWMA latency in milliseconds.
func (u *Upstream) GetLatencyEWMA() float64 {
	return math.Float64frombits(u.latencyEWMA.Load())
}

// Pool holds a set of upstreams for a single service and distributes
// requests across them using a pluggable Strategy.
//
// Thread-safety:
//   - ActiveConns uses atomic operations (no mutex needed for increment)
//   - latencyEWMA uses atomic CAS (lock-free float64 updates)
//   - healthy/unhealthy mutations use RWMutex (writes are rare, reads are frequent)
//   - strategy is set once at startup (no synchronization needed)
type Pool struct {
	mu        sync.RWMutex
	upstreams []*Upstream
	strategy  Strategy
}

var ErrNoHealthyUpstream = errors.New("no healthy upstream available")

// NewPool initializes a pool from a slice of URL strings.
// All upstreams start as healthy. Defaults to round-robin strategy.
func NewPool(urls []string) *Pool {
	p := &Pool{
		strategy: NewRoundRobin(),
	}
	for _, u := range urls {
		p.upstreams = append(p.upstreams, &Upstream{URL: u, Healthy: true})
	}
	return p
}

// SetStrategy changes the load balancing strategy.
// Call this during startup before the pool starts receiving traffic.
func (p *Pool) SetStrategy(s Strategy) {
	p.strategy = s
}

// GetStrategy returns the current strategy name.
func (p *Pool) GetStrategy() string {
	return p.strategy.Name()
}

// Next returns the next healthy upstream using the configured strategy.
// If no healthy upstreams exist, returns ErrNoHealthyUpstream.
func (p *Pool) Next() (*Upstream, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.strategy.Pick(p.upstreams)
}

// SetHealthy marks an upstream as healthy or unhealthy by URL.
// Called by the health checker goroutine.
func (p *Pool) SetHealthy(url string, healthy bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, u := range p.upstreams {
		if u.URL == url {
			u.Healthy = healthy
			return
		}
	}
}

// All returns the list of upstream pointers (healthy and unhealthy).
// We return a shallow copy of the slice to avoid exposing internal
// slice mutations while still avoiding copying the Upstream struct
// (which embeds atomic fields and must not be copied).
func (p *Pool) All() []*Upstream {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]*Upstream, len(p.upstreams))
	copy(result, p.upstreams)
	return result
}

// HealthyCount returns the number of currently healthy upstreams.
func (p *Pool) HealthyCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	count := 0
	for _, u := range p.upstreams {
		if u.Healthy {
			count++
		}
	}
	return count
}
