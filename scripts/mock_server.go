// mock_server.go — a fake backend service for local development and testing.
// Run multiple instances on different ports to simulate a real microservice cluster.
//
// Usage:
//   go run ./scripts/mock_server.go -port 9001 -service users
//   go run ./scripts/mock_server.go -port 9002 -service payments
//   go run ./scripts/mock_server.go -port 9003 -service transactions
//   go run ./scripts/mock_server.go -port 9001 -service users -slow  (adds 500ms delay)

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	port := flag.Int("port", 9001, "Port to listen on")
	service := flag.String("service", "mock", "Service name to display in responses")
	slow := flag.Bool("slow", false, "Add 500ms artificial delay to simulate slow upstream")
	flag.Parse()

	hostname, _ := os.Hostname()
	addr := fmt.Sprintf(":%d", *port)

	mux := http.NewServeMux()

	// /health — used by the gateway's health checker
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"service": *service,
			"host":    hostname,
		}); err != nil {
			log.Printf("failed to encode health response: %v", err)
		}
	})

	// /slow — simulates a slow upstream (for testing timeout middleware)
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"message": "finally responded",
		}); err != nil {
			log.Printf("failed to encode slow response: %v", err)
		}
	})

	// Catch-all handler for all other paths
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if *slow {
			time.Sleep(500 * time.Millisecond)
		}

		requestID := r.Header.Get("X-Request-ID")
		forwardedFor := r.Header.Get("X-Forwarded-For")

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"service":        *service,
			"host":           hostname,
			"port":           *port,
			"path":           r.URL.Path,
			"method":         r.Method,
			"request_id":     requestID,     // echoed back for tracing verification
			"forwarded_for":  forwardedFor,  // shows gateway forwarded this correctly
			"timestamp":      time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			log.Printf("failed to encode response: %v", err)
		}
	})

	log.Printf("[%s] Mock server listening on %s", *service, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
