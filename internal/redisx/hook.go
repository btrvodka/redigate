package redisx

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/redis/go-redis/v9"
)

// Observer receives the result of every command sent to redis.
type Observer interface {
	ObserveRedisCommand(command, status string, duration time.Duration)
}

const (
	StatusOK    = "ok"
	StatusError = "error"

	unknownCommand = "unknown"
)

type hook struct {
	observer Observer
	commands *CommandTable
}

var _ redis.Hook = (*hook)(nil)

func (h *hook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *hook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmd)
		h.observe(cmd, time.Since(start))

		return err
	}
}

func (h *hook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmds)

		duration := time.Since(start)
		for _, cmd := range cmds {
			h.observe(cmd, duration)
		}

		return err
	}
}

func (h *hook) observe(cmd redis.Cmder, duration time.Duration) {
	if h.observer == nil {
		return
	}

	status := StatusOK
	if err := cmd.Err(); err != nil && !errors.Is(err, redis.Nil) {
		status = StatusError
	}

	h.observer.ObserveRedisCommand(h.commandLabel(cmd.Name()), status, duration)
}

// commandLabel keeps metric cardinality bounded: arbitrary names coming from
// the raw command API are reported as "unknown".
func (h *hook) commandLabel(name string) string {
	if h.commands.Has(name) {
		return name
	}

	return unknownCommand
}

// IsNetworkError reports whether err is a connectivity problem rather than a redis reply.
func IsNetworkError(err error) bool {
	var netErr net.Error

	return errors.As(err, &netErr)
}
