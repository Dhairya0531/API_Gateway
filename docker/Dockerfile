# ── Stage 1: Build ───────────────────────────────────────────────────────────
# Use the full Go toolchain to compile our binary
FROM golang:1.25-alpine AS builder

# Install git (needed for go mod download with some private modules)
RUN apk add --no-cache git

WORKDIR /app

# Copy dependency files first — Docker caches this layer
# If only source files change but go.mod/go.sum don't, this layer is reused
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o gateway ./cmd/gateway

# ── Stage 2: Run ─────────────────────────────────────────────────────────────
# Minimal image — no Go toolchain, no source code, just the binary
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary from build stage
COPY --from=builder /app/gateway .
# Copy default config (can be overridden via volume mount)
COPY --from=builder /app/config/config.yaml ./config/config.yaml

EXPOSE 8080

# Healthcheck so Docker knows when the container is ready
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8080/health || exit 1

CMD ["./gateway"]
