package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Configure installs the process-wide structured JSON logger. Secrets and
// request bodies must never be supplied as attributes by callers.
func Configure(level string) *slog.Logger {
	return ConfigureWriter(level, os.Stdout)
}

func ConfigureWriter(level string, output io.Writer) *slog.Logger {
	var slogLevel slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn", "warning":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: slogLevel}))
	slog.SetDefault(logger)
	return logger
}
