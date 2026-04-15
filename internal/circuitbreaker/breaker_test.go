package circuitbreaker

import (
	"testing"
	"time"
)

func TestBreakerInitialState(t *testing.T) {
	b := New(3, time.Second)
	if b.State() != StateClosed {
		t.Errorf("expected state %s, got %s", StateClosed, b.State())
	}
	if !b.Allow() {
		t.Error("expected initial Allow() to be true")
	}
}

func TestBreakerTrips(t *testing.T) {
	b := New(3, time.Second)

	b.RecordFailure()
	b.RecordFailure()
	if b.State() != StateClosed {
		t.Errorf("expected state %s after 2 failures, got %s", StateClosed, b.State())
	}

	b.RecordFailure()
	if b.State() != StateOpen {
		t.Errorf("expected state %s after 3 failures, got %s", StateOpen, b.State())
	}
	if b.Allow() {
		t.Error("expected Allow() to be false when Open")
	}
}

func TestBreakerRecovery(t *testing.T) {
	b := New(2, 50*time.Millisecond)

	b.RecordFailure()
	b.RecordFailure() // Trips

	if b.Allow() {
		t.Error("expected Allow() to be false immediately after tripping")
	}

	time.Sleep(100 * time.Millisecond)

	// Cooldown passed, first request should be allowed (HalfOpen)
	if !b.Allow() {
		t.Error("expected Allow() to be true after cooldown")
	}
	if b.State() != StateHalfOpen {
		t.Errorf("expected state %s, got %s", StateHalfOpen, b.State())
	}

	// Second concurrent request should be blocked
	if b.Allow() {
		t.Error("expected Allow() to be false for second probe request")
	}

	// Probe succeeds
	b.RecordSuccess()
	if b.State() != StateClosed {
		t.Errorf("expected state %s after probe success, got %s", StateClosed, b.State())
	}
}

func TestBreakerProbeFailure(t *testing.T) {
	b := New(2, 50*time.Millisecond)

	b.RecordFailure()
	b.RecordFailure() // Trips
	
	time.Sleep(100 * time.Millisecond)
	
	if !b.Allow() {
		t.Error("expected Allow() to be true after cooldown")
	}
	
	b.RecordFailure() // Probe fails
	
	if b.State() != StateOpen {
		t.Errorf("expected state %s after probe failure, got %s", StateOpen, b.State())
	}
}
