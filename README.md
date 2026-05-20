# Distributed API Gateway

A production-ready, high-performance API Gateway written in Go. It routes microservice traffic with resilience, observability, and concurrency protection.

![CI](https://github.com/Dhairya0531/API_Gateway/actions/workflows/ci.yml/badge.svg)

## Overview

Features:
- Pluggable load balancing (`round-robin`, `least-connections`, `weighted-latency` / EWMA)
- Circuit breaker (closed → open → half-open)
- Redis-backed sliding-window rate limiting
- Idempotency support (cache downstream responses by Idempotency-Key)
- Async batched audit logging to PostgreSQL
- Prometheus metrics at `/metrics` and structured logging via `slog`
- Background health checking

## Prerequisites

- Go 1.25.6 (for local builds and tooling)
- Docker & Docker Compose (for local multi-service stack)

## Quick Start (Docker Compose)

Bring up the gateway and dependencies (Redis, Postgres, Prometheus, Grafana, mock upstreams):

```bash
docker compose -f docker/docker-compose.yml up --build -d
```

Service endpoints:
- Gateway: `http://localhost:8080`
- Admin API: `http://localhost:9090`
- Grafana: `http://localhost:3000` (default `admin`/`admin`)

Shut down:

```bash
docker compose -f docker/docker-compose.yml down
```

## Run Locally (without Docker)

1. Install Go 1.25.6.
2. Download dependencies:

```bash
go mod download
```

3. Build and run:

```bash
CGO_ENABLED=0 go build -o gateway ./cmd/gateway
./gateway
```

## Configuration

Primary config file: `config/config.yaml`. Example:

```yaml
server:
  port: 8080

services:
  payments:
    upstreams:
      - http://payments-svc:9002
    health_check:
      path: /health
      interval: 10s
    balance_strategy: weighted-latency

routes:
  - path: /payments
    service: payments
    timeout: 15s
    rate_limit:
      requests_per_minute: 10
```

Notes:
- `balance_strategy` accepts `round-robin`, `least-connections`, or `weighted-latency`.
- Health check path defaults to `/health` if omitted.

## Observability & Tracing

- Metrics: `GET /metrics` (Prometheus)
- Tracing: OTLP HTTP exporter is used by default; configure collector endpoint via environment or code.

## Testing

Run tests locally:

```bash
go test ./... -v
```

Unit tests use `miniredis` to mock Redis — no external Redis required for unit tests.

## Linting

Locally run the linter:

```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
golangci-lint run ./...
```

CI runs `golangci-lint` and `go test` as part of the workflow at `.github/workflows/ci.yml`.

## Troubleshooting

- If the linter fails about Go version mismatch, ensure the runner's Go version matches the `go` directive in `go.mod` (1.25.6).
- CI cache errors usually resolve on re-run (GitHub cache service intermittent failures).

## Contributing

Contributions welcome — please open a PR. Simple guidelines:
1. Fork and create a branch
2. Run `golangci-lint` and tests locally
3. Add unit tests for bug fixes or new features

## License

This project is licensed; see the `LICENSE` file for details.