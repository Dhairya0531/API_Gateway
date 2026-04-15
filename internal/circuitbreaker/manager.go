package circuitbreaker

import (
	"sync"
	"time"
)

// Manager holds all circuit breakers for the gateway, keyed by upstream URL.
type Manager struct {
	mu       sync.RWMutex
	breakers map[string]*Breaker
	
	failureThreshold int
	cooldownPeriod   time.Duration
}

// NewManager creates a new circuit breaker manager.
func NewManager(failureThreshold int, cooldownPeriod time.Duration) *Manager {
	return &Manager{
		breakers:         make(map[string]*Breaker),
		failureThreshold: failureThreshold,
		cooldownPeriod:   cooldownPeriod,
	}
}

// GetOrCreate returns the circuit breaker for a given upstream URL.
// If it doesn't exist, it is created.
func (m *Manager) GetOrCreate(url string) *Breaker {
	m.mu.RLock()
	b, ok := m.breakers[url]
	m.mu.RUnlock()
	
	if ok {
		return b
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Double-check after acquiring write lock
	if b, ok := m.breakers[url]; ok {
		return b
	}

	b = New(m.failureThreshold, m.cooldownPeriod)
	m.breakers[url] = b
	return b
}
