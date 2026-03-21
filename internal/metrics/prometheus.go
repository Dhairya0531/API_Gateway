// Package metrics exposes Prometheus metrics for the API Gateway.
// You will build this on Day 18 of the project.
//
// Metrics to implement:
//   - gateway_requests_total{path, method, status}    — request counter
//   - gateway_request_duration_seconds{path}          — latency histogram
//   - gateway_upstream_health{service, host}          — 1=healthy, 0=unhealthy
//   - gateway_rate_limit_hits_total{user, route}      — rate limit events
package metrics
