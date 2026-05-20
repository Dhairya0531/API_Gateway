package proxy

import (
	"encoding/json"
	"net/http"
)

// FallbackResponse is returned when the upstream is completely unavailable.
type FallbackResponse struct {
	Error      string `json:"error"`
	IsDegraded bool   `json:"is_degraded"`
	RetryAfter int    `json:"retry_after_seconds,omitempty"`
}

// writeDegradedResponse writes a graceful degradation response to the client
// when the circuit is open or no healthy upstreams are available.
func writeDegradedResponse(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "30") // Suggest retrying in 30 seconds
	w.WriteHeader(http.StatusServiceUnavailable)

	// In a real payment gateway, you might return a cached generic catalog
	// or queue the transaction for async processing if it's a POST.
	// For this API Gateway, we standardize the degraded response format.
	resp := FallbackResponse{
		Error:      "Service is currently experiencing high load or is temporarily unavailable.",
		IsDegraded: true,
		RetryAfter: 30,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return
	}
}
