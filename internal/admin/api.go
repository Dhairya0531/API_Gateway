package admin

import (
	"encoding/json"
	"net/http"

	"github.com/Dhairya0531/API_Gateway/internal/balancer"
)

// API provides management endpoints for the gateway.
type API struct {
	pools map[string]*balancer.Pool
}

// New creates a new Admin API instance.
func New(pools map[string]*balancer.Pool) *API {
	return &API{pools: pools}
}

// Handler returns an http.ServeMux with all admin routes configured.
func (a *API) Handler() *http.ServeMux {
	mux := http.NewServeMux()

	// GET /admin/pools — Returns the status of all upstreams
	mux.HandleFunc("/admin/pools", a.handleGetPools)

	// POST /admin/pools/{service}/upstreams/{url}/status — Manually mark upstream up/down
	// For simplicity in standard library (Go < 1.22), we use a generic path and parse manually
	mux.HandleFunc("/admin/upstream/status", a.handleSetUpstreamStatus)

	return mux
}

func (a *API) handleGetPools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type UpstreamStatus struct {
		URL         string  `json:"url"`
		Healthy     bool    `json:"healthy"`
		ActiveConns int64   `json:"active_connections"`
		LatencyEWMA float64 `json:"latency_ewma_ms"`
	}

	type PoolStatus struct {
		Strategy  string           `json:"strategy"`
		Upstreams []UpstreamStatus `json:"upstreams"`
	}

	response := make(map[string]PoolStatus)

	for name, pool := range a.pools {
		all := pool.All()
		upstreams := make([]UpstreamStatus, len(all))
		for i, u := range all {
			upstreams[i] = UpstreamStatus{
				URL:         u.URL,
				Healthy:     u.Healthy,
				ActiveConns: u.ActiveConns.Load(),
				LatencyEWMA: u.GetLatencyEWMA(),
			}
		}

		response[name] = PoolStatus{
			Strategy:  pool.GetStrategy(),
			Upstreams: upstreams,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}
}

func (a *API) handleSetUpstreamStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Service string `json:"service"`
		URL     string `json:"url"`
		Healthy bool   `json:"healthy"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	pool, ok := a.pools[req.Service]
	if !ok {
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}

	pool.SetHealthy(req.URL, req.Healthy)

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(`{"status":"success"}`)); err != nil {
		// Best-effort logging — response already started
		return
	}
}
