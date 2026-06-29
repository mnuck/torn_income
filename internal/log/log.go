package log

import (
	"log/slog"
	"os"
	"strings"
)

// Setup configures the global logger based on ENV and LOGLEVEL environment variables.
func Setup() {
	var level slog.Level
	levelStr := strings.ToLower(os.Getenv("LOGLEVEL"))
	switch levelStr {
	case "debug":
		level = slog.LevelDebug
	case "info", "":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	case "fatal", "panic", "disabled":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if os.Getenv("ENV") == "production" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	slog.SetDefault(slog.New(handler))
}
