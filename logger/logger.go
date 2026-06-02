package logger

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// New creates new slog.Logger based on the format
func New(logLevel string, logFormat string) (*slog.Logger, error) {
	var lvl slog.Level
	switch strings.ToLower(logLevel) {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("invalid log level: %s", logLevel)
	}

	switch logFormat {
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			AddSource: true,
			Level:     lvl,
		})), nil
	case "text":
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			AddSource: true,
			Level:     lvl,
		})), nil
	default:
		return nil, fmt.Errorf("invalid log format: %s", logFormat)
	}
}
