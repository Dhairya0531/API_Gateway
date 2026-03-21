package logger

import (
	"context"
	"log/slog"
	"time"

	"github.com/Dhairya0531/API_Gateway/internal/store"
)

// AuditLogger writes request logs to PostgreSQL asynchronously.
//
// Design:
//   - Uses a buffered channel (size 10,000) — producers (request goroutines)
//     never block even under high load
//   - A background goroutine drains the channel and batches inserts
//   - Flushes to PostgreSQL when: batch reaches 100 entries OR 1 second elapses
//     (whichever comes first)
//   - On shutdown, drains remaining entries before closing
//
// Trade-off: We accept potential loss of the last 1 second of logs
// in exchange for zero latency impact on request handling.
type AuditLogger struct {
	entries chan store.LogEntry
	pg      *store.PgClient
	log     *slog.Logger
	done    chan struct{}
}

const (
	channelSize = 10000
	batchSize   = 100
	flushInterval = 1 * time.Second
)

// NewAuditLogger creates and starts the async audit logger.
// The background writer runs until Stop() is called.
func NewAuditLogger(pg *store.PgClient, log *slog.Logger) *AuditLogger {
	al := &AuditLogger{
		entries: make(chan store.LogEntry, channelSize),
		pg:      pg,
		log:     log,
		done:    make(chan struct{}),
	}

	go al.backgroundWriter()

	log.Info("audit logger started",
		slog.Int("channel_size", channelSize),
		slog.Int("batch_size", batchSize),
	)

	return al
}

// Log sends a log entry to the background writer.
// This is called from the request goroutine and never blocks
// (drops the entry if the channel is full — better than adding latency).
func (al *AuditLogger) Log(entry store.LogEntry) {
	select {
	case al.entries <- entry:
		// sent successfully
	default:
		// Channel full — drop the entry rather than blocking
		// This should be extremely rare with a 10k buffer
		al.log.Warn("audit log channel full, dropping entry",
			slog.String("request_id", entry.RequestID),
		)
	}
}

// Stop gracefully shuts down the audit logger.
// It closes the channel and waits for the background writer to drain
// all remaining entries before returning.
func (al *AuditLogger) Stop() {
	al.log.Info("stopping audit logger, draining remaining entries...")
	close(al.entries)
	<-al.done // wait for background writer to finish
	al.log.Info("audit logger stopped")
}

// backgroundWriter runs in a goroutine, collecting log entries and
// flushing them to PostgreSQL in batches.
func (al *AuditLogger) backgroundWriter() {
	defer close(al.done)

	batch := make([]store.LogEntry, 0, batchSize)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	for {
		select {
		case entry, ok := <-al.entries:
			if !ok {
				// Channel closed — flush remaining and exit
				if len(batch) > 0 {
					al.flush(batch)
				}
				return
			}

			batch = append(batch, entry)
			if len(batch) >= batchSize {
				al.flush(batch)
				batch = batch[:0] // reset slice, keep capacity
			}

		case <-ticker.C:
			// Time-based flush — don't let entries sit too long
			if len(batch) > 0 {
				al.flush(batch)
				batch = batch[:0]
			}
		}
	}
}

// flush writes a batch of entries to PostgreSQL.
// Errors are logged but don't crash the writer — audit logging
// should never take down the gateway.
func (al *AuditLogger) flush(entries []store.LogEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := al.pg.InsertLogBatch(ctx, entries); err != nil {
		al.log.Error("failed to flush audit logs",
			slog.String("error", err.Error()),
			slog.Int("batch_size", len(entries)),
		)
		return
	}

	al.log.Debug("flushed audit logs",
		slog.Int("count", len(entries)),
	)
}
