package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Dhairya0531/API_Gateway/internal/admin"
	"github.com/Dhairya0531/API_Gateway/internal/balancer"
	"github.com/Dhairya0531/API_Gateway/internal/cache"
	"github.com/Dhairya0531/API_Gateway/internal/circuitbreaker"
	cfg "github.com/Dhairya0531/API_Gateway/internal/config"
	"github.com/Dhairya0531/API_Gateway/internal/health"
	"github.com/Dhairya0531/API_Gateway/internal/idempotency"
	"github.com/Dhairya0531/API_Gateway/internal/logger"
	"github.com/Dhairya0531/API_Gateway/internal/metrics"
	"github.com/Dhairya0531/API_Gateway/internal/middleware"
	"github.com/Dhairya0531/API_Gateway/internal/proxy"
	"github.com/Dhairya0531/API_Gateway/internal/ratelimit"
	"github.com/Dhairya0531/API_Gateway/internal/router"
	"github.com/Dhairya0531/API_Gateway/internal/store"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

// initTracer creates a new trace provider instance and registers it as global trace provider.
func initTracer(url string) (*sdktrace.TracerProvider, error) {
	// Use OTLP HTTP exporter. Prefer OTLP over the deprecated Jaeger exporter.
	ctx := context.Background()
	// Default to localhost:4318 (common OTLP HTTP endpoint). If your collector
	// uses a different endpoint, set the `url` accordingly in the caller.
	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint("localhost:4318"),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("api-gateway"),
		)),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp, nil
}

func main() {
	// ─── Logger ─────────────────────────────────────────────────────────────
	log := logger.New()
	log.Info("API Gateway starting up")

	// ─── Config ─────────────────────────────────────────────────────────────
	configPath := "config/config.yaml"
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		configPath = p
	}

	config, err := cfg.Load(configPath)
	if err != nil {
		log.Error("failed to load config", slog.String("error", err.Error()))
		os.Exit(1)
	}
	log.Info("config loaded", slog.String("path", configPath))

	// ─── OpenTelemetry ──────────────────────────────────────────────────────
	tp, err := initTracer("http://localhost:14268/api/traces")
	if err == nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := tp.Shutdown(ctx); err != nil {
				log.Warn("tracer shutdown error", slog.String("error", err.Error()))
			}
		}()
		log.Info("opentelemetry tracing initialized")
	} else {
		log.Warn("failed to initialize tracing", slog.String("error", err.Error()))
	}

	// ─── Redis ──────────────────────────────────────────────────────────────
	// Required for: rate limiting, idempotency (Day 3), caching (Day 5)
	redisClient, err := store.NewRedis(store.RedisConfig{
		Addr:     config.Redis.Addr,
		Password: config.Redis.Password,
		DB:       config.Redis.DB,
	}, log)
	if err != nil {
		log.Error("failed to connect to redis", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer redisClient.Close()

	// ─── PostgreSQL ─────────────────────────────────────────────────────────
	// Required for: async audit logging
	pgClient, err := store.NewPostgres(store.PgConfig{
		Host:     config.Postgres.Host,
		Port:     config.Postgres.Port,
		User:     config.Postgres.User,
		Password: config.Postgres.Password,
		DBName:   config.Postgres.DBName,
		SSLMode:  config.Postgres.SSLMode,
	}, log)
	if err != nil {
		log.Error("failed to connect to postgres", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer pgClient.Close()

	// ─── Audit Logger ───────────────────────────────────────────────────────
	// Async batched writes to PostgreSQL — never blocks request handling
	auditLogger := logger.NewAuditLogger(pgClient, log)
	defer auditLogger.Stop()

	// ─── Rate Limiter ───────────────────────────────────────────────────────
	// Sliding window limiter backed by Redis sorted sets
	slidingWindow := ratelimit.NewSlidingWindow(redisClient.Client())

	// ─── Context (used by JWT cache, health checker, config watcher) ─────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ─── JWT Validator ───────────────────────────────────────────────────────
	// Cryptographically validates RS256 JWTs via a JWKS public key endpoint.
	// Falls back to static token matching if jwt.enabled = false in config.
	var jwtValidator *middleware.JWTValidator
	if config.JWT.Enabled {
		v, jwtErr := middleware.NewJWTValidator(
			ctx,
			config.JWT.JWKSURL,
			config.JWT.Issuer,
			config.JWT.Audience,
			log,
		)
		if jwtErr != nil {
			log.Error("failed to initialize JWT validator", slog.String("error", jwtErr.Error()))
			os.Exit(1)
		}
		jwtValidator = v
		log.Info("JWT validation enabled", slog.String("jwks_url", config.JWT.JWKSURL))
	} else {
		log.Info("JWT disabled — using static token auth")
	}

	// ─── mTLS Transport ───────────────────────────────────────────────────────
	// When tls.enabled=true, the gateway presents a client cert to all backends.
	// Backends can then reject connections that don't have the gateway's cert.
	var baseTransport http.RoundTripper = http.DefaultTransport
	if config.TLS.Enabled {
		tlsTransport, tlsErr := proxy.NewMTLSTransport(
			config.TLS.CACert,
			config.TLS.ClientCert,
			config.TLS.ClientKey,
		)
		if tlsErr != nil {
			log.Error("failed to build mTLS transport", slog.String("error", tlsErr.Error()))
			os.Exit(1)
		}
		baseTransport = tlsTransport
		log.Info("mTLS enabled",
			slog.String("ca_cert", config.TLS.CACert),
			slog.String("client_cert", config.TLS.ClientCert),
		)
	} else {
		log.Info("mTLS disabled — using plain HTTP transport to upstreams")
	}
	_ = baseTransport // will be wired into GatewayTransport in a future refactor

	// ─── Build Upstream Pools ────────────────────────────────────────────────
	// One pool per service. Each pool holds all upstream URLs for that service.
	pools := make(map[string]*balancer.Pool)
	for name, svc := range config.Services {
		pool := balancer.NewPool(svc.Upstreams)
		pool.SetStrategy(balancer.StrategyFromName(svc.BalanceStrategy))
		pools[name] = pool
		log.Info("upstream pool created",
			slog.String("service", name),
			slog.Int("upstreams", len(svc.Upstreams)),
			slog.String("strategy", pool.GetStrategy()),
		)
	}

	// ─── Health Checker ──────────────────────────────────────────────────────
	// Starts probing upstreams every N seconds in the background.
	checker := health.New(pools, config.Services, log)
	go checker.Start(ctx)

	// ─── Circuit Breaker ─────────────────────────────────────────────────────
	// Manages state of upstream health to prevent cascading failures
	cbManager := circuitbreaker.NewManager(5, 10*time.Second)

	// ─── Router ─────────────────────────────────────────────────────────────
	// Matches request paths to upstream pools, applies per-route timeouts
	r, err := router.New(config.Routes, pools, cbManager, log)
	if err != nil {
		log.Error("failed to build router", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// ─── Dynamic Config Watcher ──────────────────────────────────────────────
	// Subscribes to Redis and hot-reloads the router when updates are published
	go cfg.WatchConfig(ctx, redisClient, r, cbManager, log)

	// ─── Middleware Chain ────────────────────────────────────────────────────
	// Order matters — outermost middleware executes first on request,
	// last on response.
	//
	//  Recovery (outermost — catches panics from everything below)
	//    └── RequestID (generates UUID before any logging)
	//          └── Metrics (records request count + latency) [Day 5]
	//                └── Logger (logs after response, needs request ID)
	//                      └── Auth (reject unauthorized before hitting upstreams)
	//                            └── RateLimit (enforce per-user per-route limits)
	//                                  └── Idempotency [Day 3]
	//                                        └── Router/Handler (innermost — does actual work)
	var handler http.Handler = r

	handler = ratelimit.Middleware(slidingWindow, buildRateLimitMap(config.Routes), log)(handler)

	// Idempotency — prevents duplicate POST/PUT requests
	handler = idempotency.Middleware(redisClient, log)(handler)

	// Response Caching - caches GET requests
	handler = cache.Middleware(redisClient, 5*time.Minute, log)(handler)

	handler = middleware.Auth(log, config.Auth.ValidTokens, config.Auth.Enabled, jwtValidator)(handler)

	// Audit logging middleware — wraps response to capture status + latency
	handler = auditMiddleware(auditLogger, log)(handler)

	// Prometheus metrics
	handler = metrics.Middleware()(handler)

	handler = middleware.Logger(log)(handler)
	handler = middleware.RequestID(handler)
	handler = middleware.TracingMiddleware()(handler)
	handler = middleware.Recovery(log)(handler)

	// ─── HTTP Mux ────────────────────────────────────────────────────────────
	mux := http.NewServeMux()

	// Health endpoint — bypasses router and middleware (always accessible)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"time":    time.Now().UTC().Format(time.RFC3339),
			"version": "v1.0.0",
			"pools":   poolStats(pools),
		}); err != nil {
			// Best-effort — health endpoint
			return
		}
	})

	// Serve OpenAPI / Swagger specs
	mux.Handle("/docs/", http.StripPrefix("/docs/", http.FileServer(http.Dir("docs"))))

	// Prometheus metrics endpoint
	mux.Handle("/metrics", metrics.Handler())

	// All other requests go through the full middleware chain + router
	mux.Handle("/", handler)

	// ─── HTTP Server ─────────────────────────────────────────────────────────
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", config.Server.Port),
		Handler:      mux,
		ReadTimeout:  config.Server.ReadTimeout,
		WriteTimeout: config.Server.WriteTimeout,
		IdleTimeout:  config.Server.IdleTimeout,
	}

	// ─── Admin API ───────────────────────────────────────────────────────────
	// Exposes internal state on a separate port (9090 by default) for security
	adminAPI := admin.New(pools)
	adminServer := &http.Server{
		Addr:    ":9090",
		Handler: adminAPI.Handler(),
	}

	go func() {
		log.Info("admin api listening", slog.Int("port", 9090))
		if err := adminServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("admin server error", slog.String("error", err.Error()))
		}
	}()

	// Start server in a goroutine so we can listen for shutdown signals
	serverErr := make(chan error, 1)
	go func() {
		log.Info("gateway listening", slog.Int("port", config.Server.Port))
		serverErr <- server.ListenAndServe()
	}()

	// ─── Graceful Shutdown ───────────────────────────────────────────────────
	// Listen for SIGINT (Ctrl+C) or SIGTERM (Docker stop)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		log.Error("server error", slog.String("error", err.Error()))
	case sig := <-quit:
		log.Info("shutdown signal received", slog.String("signal", sig.String()))
	}

	// Give in-flight requests 30 seconds to complete before forcing close
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	cancel() // stop health checker

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error("forced shutdown gateway", slog.String("error", err.Error()))
	} else {
		log.Info("gateway shut down cleanly")
	}

	if err := adminServer.Shutdown(shutdownCtx); err != nil {
		log.Error("forced shutdown admin server", slog.String("error", err.Error()))
	} else {
		log.Info("admin server shut down cleanly")
	}
}

// poolStats returns a summary of healthy/total upstreams per service.
// Embedded in the /health response for quick operational visibility.
func poolStats(pools map[string]*balancer.Pool) map[string]any {
	stats := make(map[string]any, len(pools))
	for name, pool := range pools {
		all := pool.All()
		stats[name] = map[string]int{
			"healthy": pool.HealthyCount(),
			"total":   len(all),
		}
	}
	return stats
}

// buildRateLimitMap converts route configs into the format expected by the rate limit middleware.
func buildRateLimitMap(routes []cfg.RouteConfig) map[string]ratelimit.RouteLimitConfig {
	m := make(map[string]ratelimit.RouteLimitConfig, len(routes))
	for _, r := range routes {
		if r.RateLimit.RequestsPerMinute > 0 {
			m[r.Path] = ratelimit.RouteLimitConfig{
				RequestsPerMinute: r.RateLimit.RequestsPerMinute,
			}
		}
	}
	return m
}

// auditMiddleware captures request/response data and sends it to the async audit logger.
// It wraps the response writer to capture the status code and measures latency.
func auditMiddleware(audit *logger.AuditLogger, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := &statusCapture{ResponseWriter: w}

			next.ServeHTTP(wrapped, r)

			// Send to async audit logger — never blocks
			audit.Log(store.LogEntry{
				RequestID: middleware.GetRequestID(r.Context()),
				Path:      r.URL.Path,
				Method:    r.Method,
				Status:    wrapped.status(),
				LatencyMs: time.Since(start).Milliseconds(),
				IPAddress: r.RemoteAddr,
				CreatedAt: start,
			})
		})
	}
}

// statusCapture wraps ResponseWriter to capture the HTTP status code.
type statusCapture struct {
	http.ResponseWriter
	code        int
	wroteHeader bool
}

func (sc *statusCapture) WriteHeader(code int) {
	if !sc.wroteHeader {
		sc.code = code
		sc.wroteHeader = true
		sc.ResponseWriter.WriteHeader(code)
	}
}

func (sc *statusCapture) status() int {
	if sc.code == 0 {
		return http.StatusOK
	}
	return sc.code
}
