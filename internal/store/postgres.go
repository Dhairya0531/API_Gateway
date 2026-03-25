package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PgClient wraps a pgx connection pool for the audit log database.
// Uses a pool (not a single connection) because multiple goroutines
// will write audit logs concurrently.
type PgClient struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// PgConfig holds PostgreSQL connection parameters.
type PgConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// DSN returns the PostgreSQL connection string.
func (c PgConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.DBName, c.SSLMode,
	)
}

// NewPostgres creates a connection pool and verifies connectivity.
// The pool manages connections automatically — no manual open/close per query.
func NewPostgres(cfg PgConfig, log *slog.Logger) (*PgClient, error) {
	dsn := cfg.DSN()

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing postgres config: %w", err)
	}

	// Pool tuning
	poolCfg.MaxConns = 10
	poolCfg.MinConns = 2
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating postgres pool: %w", err)
	}

	// Verify connectivity
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping failed: %w", err)
	}

	log.Info("postgres connected",
		slog.String("host", cfg.Host),
		slog.Int("port", cfg.Port),
		slog.String("dbname", cfg.DBName),
	)

	return &PgClient{pool: pool, log: log}, nil
}

// LogEntry represents a single request log to be written to PostgreSQL.
type LogEntry struct {
	RequestID string
	UserID    string
	Path      string
	Method    string
	Status    int
	LatencyMs int64
	Upstream  string
	IPAddress string
	Error     string
	CreatedAt time.Time
}

// InsertLog inserts a single log entry.
func (p *PgClient) InsertLog(ctx context.Context, entry LogEntry) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO request_logs 
			(request_id, user_id, path, method, status, latency_ms, upstream, ip_address, error, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		entry.RequestID,
		entry.UserID,
		entry.Path,
		entry.Method,
		entry.Status,
		entry.LatencyMs,
		entry.Upstream,
		entry.IPAddress,
		entry.Error,
		entry.CreatedAt,
	)
	return err
}

// InsertLogBatch inserts multiple log entries in a single transaction.
// This is used by the async audit logger to batch writes for efficiency.
//
// Why batched?
//   - 100 individual INSERTs = 100 round-trips to Postgres
//   - 1 batch INSERT = 1 round-trip (100x fewer network calls)
func (p *PgClient) InsertLogBatch(ctx context.Context, entries []LogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, entry := range entries {
		_, err := tx.Exec(ctx,
			`INSERT INTO request_logs 
				(request_id, user_id, path, method, status, latency_ms, upstream, ip_address, error, created_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			entry.RequestID,
			entry.UserID,
			entry.Path,
			entry.Method,
			entry.Status,
			entry.LatencyMs,
			entry.Upstream,
			entry.IPAddress,
			entry.Error,
			entry.CreatedAt,
		)
		if err != nil {
			return fmt.Errorf("insert log entry: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// Close shuts down the connection pool.
func (p *PgClient) Close() {
	p.log.Info("closing postgres connection pool")
	p.pool.Close()
}
