package logger

import (
	"log/slog"
	"os"
)

// New creates a structured JSON logger writing to stdout.
// This is the single logger used across the entire gateway.
func New() *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: false,
	})
	return slog.New(handler)
}
