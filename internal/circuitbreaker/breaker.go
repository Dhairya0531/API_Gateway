package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("circuit breaker is open")

// State represents the current state of the circuit breaker.
type State int

const (
	StateClosed State = iota // Normal operation, requests flow freely
	StateOpen                // Tripped, requests fail immediately
	StateHalfOpen            // Probing to see if upstream has recovered
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateOpen:
		return "OPEN"
	case StateHalfOpen:
		return "HALF-OPEN"
	default:
		return "UNKNOWN"
	}
}

// Breaker implements a finite state machine to prevent cascading failures.
// It protects a single upstream instance.
type Breaker struct {
	mu sync.RWMutex

	state               State
	failures            int
	failureThreshold    int
	cooldownPeriod      time.Duration
	stateChangedAt      time.Time
	halfOpenMaxRequests int
	halfOpenRequests    int
}

// New creates a new circuit breaker in the Closed state.
func New(failureThreshold int, cooldownPeriod time.Duration) *Breaker {
	return &Breaker{
		state:               StateClosed,
		failureThreshold:    failureThreshold,
		cooldownPeriod:      cooldownPeriod,
		halfOpenMaxRequests: 1, // Only allow 1 probe request
	}
}

// Allow checks if a request is permitted to proceed.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true

	case StateOpen:
		// Check if cooldown period has elapsed
		if time.Since(b.stateChangedAt) >= b.cooldownPeriod {
			b.setState(StateHalfOpen)
			b.halfOpenRequests = 1
			return true // Allow the first probe request
		}
		return false

	case StateHalfOpen:
		// We are already probing. Allow up to halfOpenMaxRequests.
		if b.halfOpenRequests < b.halfOpenMaxRequests {
			b.halfOpenRequests++
			return true
		}
		return false
	}

	return false
}

// RecordSuccess should be called when an upstream request succeeds.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == StateHalfOpen {
		// Probe succeeded! The upstream has recovered.
		b.setState(StateClosed)
	} else if b.state == StateClosed {
		// Normal operation. Reset failure count.
		b.failures = 0
	}
}

// RecordFailure should be called when an upstream request fails (e.g., timeout, 502).
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		b.failures++
		if b.failures >= b.failureThreshold {
			// Threshold reached. Trip the breaker!
			b.setState(StateOpen)
		}
	case StateHalfOpen:
		// The probe failed. The upstream is still unhealthy.
		// Go back to Open state and restart the cooldown timer.
		b.setState(StateOpen)
	}
}

// State returns the current state (thread-safe).
func (b *Breaker) State() State {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.state
}

// setState changes the internal state. Caller must hold the mutex lock.
func (b *Breaker) setState(newState State) {
	if b.state == newState {
		return
	}
	b.state = newState
	b.stateChangedAt = time.Now()
	
	if newState == StateClosed {
		b.failures = 0
		b.halfOpenRequests = 0
	}
}
