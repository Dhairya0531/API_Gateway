package idempotency

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Dhairya0531/API_Gateway/internal/store"
	"github.com/alicebob/miniredis/v2"
)

func TestIdempotencyMiddleware(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	redisClient, err := store.NewRedis(store.RedisConfig{
		Addr: mr.Addr(),
	}, log)
	if err != nil {
		t.Fatalf("failed to create redis client: %v", err)
	}
	defer redisClient.Close()

	middleware := Middleware(redisClient, log)

	var callCount int
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("X-Custom", "test")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"success":true}`))
	})

	handler := middleware(nextHandler)

	req1 := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"data":"1"}`))
	req1.Header.Set("Idempotency-Key", "key-123")
	rec1 := httptest.NewRecorder()

	handler.ServeHTTP(rec1, req1)

	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
	if rec1.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rec1.Code)
	}

	// Give redis background goroutine time to save
	time.Sleep(100 * time.Millisecond)

	req2 := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"data":"1"}`))
	req2.Header.Set("Idempotency-Key", "key-123")
	rec2 := httptest.NewRecorder()

	handler.ServeHTTP(rec2, req2)

	if callCount != 1 {
		t.Errorf("expected still 1 call (cached), got %d", callCount)
	}
	if rec2.Code != http.StatusCreated {
		t.Errorf("expected status 201 from cache, got %d", rec2.Code)
	}
	if rec2.Header().Get("X-Idempotency-Cached") != "true" {
		t.Error("expected X-Idempotency-Cached header to be true")
	}
	if rec2.Header().Get("X-Custom") != "test" {
		t.Error("expected X-Custom header to be restored")
	}

	// Request with same key but different body should be rejected
	req3 := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"data":"2"}`))
	req3.Header.Set("Idempotency-Key", "key-123")
	rec3 := httptest.NewRecorder()

	handler.ServeHTTP(rec3, req3)

	if callCount != 1 {
		t.Errorf("expected still 1 call, got %d", callCount)
	}
	if rec3.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected status 422 for different payload, got %d", rec3.Code)
	}
}
