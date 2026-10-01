// Package logger builds the application slog.Logger.
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/btrvodka/redigate/internal/config"
)

func New(w io.Writer, cfg config.Log) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		return nil, fmt.Errorf("invalid log level %q: %w", cfg.Level, err)
	}

	opts := &slog.HandlerOptions{Level: level}

	switch strings.ToLower(cfg.Format) {
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("invalid log format %q, expected text or json", cfg.Format)
	}
}
