package redisx

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

// slogAdapter routes go-redis internal logs to slog.
type slogAdapter struct {
	log *slog.Logger
}

func (a slogAdapter) Printf(ctx context.Context, format string, v ...any) {
	a.log.WarnContext(ctx, fmt.Sprintf(format, v...), slog.String("component", "go-redis"))
}

// SetLogger replaces the global go-redis logger.
func SetLogger(logger *slog.Logger) {
	redis.SetLogger(slogAdapter{log: logger})
}
