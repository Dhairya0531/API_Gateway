package balancer

import (
	"testing"
	"time"
)

func TestPoolInitialization(t *testing.T) {
	urls := []string{"http://a", "http://b"}
	pool := NewPool(urls)

	if pool.HealthyCount() != 2 {
		t.Errorf("expected 2 healthy upstreams, got %d", pool.HealthyCount())
	}

	all := pool.All()
	if len(all) != 2 {
		t.Errorf("expected 2 upstreams total, got %d", len(all))
	}
}

func TestPoolSetHealthy(t *testing.T) {
	urls := []string{"http://a", "http://b"}
	pool := NewPool(urls)

	pool.SetHealthy("http://a", false)

	if pool.HealthyCount() != 1 {
		t.Errorf("expected 1 healthy upstream, got %d", pool.HealthyCount())
	}

	u, err := pool.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.URL != "http://b" {
		t.Errorf("expected http://b, got %s", u.URL)
	}
}

func TestUpstreamLatencyEWMA(t *testing.T) {
	u := &Upstream{URL: "http://a", Healthy: true}
	
	// Initial is 0, so first sample is used directly
	u.RecordLatency(100 * time.Millisecond)
	if val := u.GetLatencyEWMA(); val != 100 {
		t.Errorf("expected 100, got %f", val)
	}

	// Second sample uses α=0.3
	// 0.3*50 + 0.7*100 = 15 + 70 = 85
	u.RecordLatency(50 * time.Millisecond)
	if val := u.GetLatencyEWMA(); val != 85 {
		t.Errorf("expected 85, got %f", val)
	}
}

func TestUpstreamConnections(t *testing.T) {
	u := &Upstream{URL: "http://a", Healthy: true}
	
	u.IncrConns()
	u.IncrConns()
	if val := u.ActiveConns.Load(); val != 2 {
		t.Errorf("expected 2 active conns, got %d", val)
	}

	u.DecrConns()
	if val := u.ActiveConns.Load(); val != 1 {
		t.Errorf("expected 1 active conn, got %d", val)
	}
}
