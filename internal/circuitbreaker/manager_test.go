package circuitbreaker

import (
	"sync"
	"testing"
	"time"
)

func TestManagerGetOrCreate(t *testing.T) {
	m := NewManager(3, time.Second)
	
	b1 := m.GetOrCreate("http://a")
	if b1 == nil {
		t.Fatal("expected breaker, got nil")
	}

	b2 := m.GetOrCreate("http://a")
	if b1 != b2 {
		t.Error("expected same breaker instance")
	}

	b3 := m.GetOrCreate("http://b")
	if b1 == b3 {
		t.Error("expected different breaker instances for different URLs")
	}
}

func TestManagerConcurrentCreation(t *testing.T) {
	m := NewManager(3, time.Second)
	
	var wg sync.WaitGroup
	breakers := make([]*Breaker, 100)
	
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			breakers[idx] = m.GetOrCreate("http://concurrent")
		}(i)
	}
	wg.Wait()
	
	for i := 1; i < 100; i++ {
		if breakers[0] != breakers[i] {
			t.Error("expected all concurrent calls to return same breaker instance")
		}
	}
}
