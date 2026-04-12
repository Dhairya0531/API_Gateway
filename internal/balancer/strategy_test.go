package balancer

import (
	"testing"
	"time"
)

func TestRoundRobinStrategy(t *testing.T) {
	strategy := NewRoundRobin()
	upstreams := []*Upstream{
		{URL: "http://a", Healthy: true},
		{URL: "http://b", Healthy: true},
		{URL: "http://c", Healthy: true},
	}

	for i := 0; i < 6; i++ {
		u, err := strategy.Pick(upstreams)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := upstreams[i%3].URL
		if u.URL != expected {
			t.Errorf("expected %s, got %s", expected, u.URL)
		}
	}
}

func TestLeastConnectionsStrategy(t *testing.T) {
	strategy := NewLeastConnections()
	upstreams := []*Upstream{
		{URL: "http://a", Healthy: true},
		{URL: "http://b", Healthy: true},
		{URL: "http://c", Healthy: true},
	}

	upstreams[0].ActiveConns.Store(10)
	upstreams[1].ActiveConns.Store(5)
	upstreams[2].ActiveConns.Store(100)

	u, err := strategy.Pick(upstreams)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.URL != "http://b" {
		t.Errorf("expected http://b, got %s", u.URL)
	}
}

func TestWeightedLatencyStrategy(t *testing.T) {
	strategy := NewWeightedLatency()
	upstreams := []*Upstream{
		{URL: "http://a", Healthy: true},
		{URL: "http://b", Healthy: true},
		{URL: "http://c", Healthy: true},
	}

	upstreams[0].RecordLatency(100 * time.Millisecond)
	upstreams[1].RecordLatency(50 * time.Millisecond)
	upstreams[2].RecordLatency(200 * time.Millisecond)

	u, err := strategy.Pick(upstreams)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.URL != "http://b" {
		t.Errorf("expected http://b, got %s", u.URL)
	}
}

func TestStrategiesNoHealthy(t *testing.T) {
	upstreams := []*Upstream{
		{URL: "http://a", Healthy: false},
		{URL: "http://b", Healthy: false},
	}

	strategies := []Strategy{
		NewRoundRobin(),
		NewLeastConnections(),
		NewWeightedLatency(),
	}

	for _, s := range strategies {
		_, err := s.Pick(upstreams)
		if err != ErrNoHealthyUpstream {
			t.Errorf("strategy %s expected ErrNoHealthyUpstream, got %v", s.Name(), err)
		}
	}
}
