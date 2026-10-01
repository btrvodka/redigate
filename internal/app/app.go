// Package app wires redigate components and runs them until the context is canceled.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"

	"golang.org/x/sync/errgroup"

	"github.com/btrvodka/redigate/internal/config"
	"github.com/btrvodka/redigate/internal/httpapi"
	"github.com/btrvodka/redigate/internal/metrics"
	"github.com/btrvodka/redigate/internal/redisx"
	"github.com/btrvodka/redigate/internal/service"
	"github.com/btrvodka/redigate/internal/webui"
)

func Run(ctx context.Context, cfg *config.Config, logger *slog.Logger, version string) error {
	if !cfg.Auth.Enabled() {
		logger.WarnContext(ctx, "authentication is disabled, anyone who can reach the API has full access to redis",
			slog.String("addr", cfg.HTTP.Addr))
	}

	collector := metrics.New()

	redisx.SetLogger(logger)

	redisClient, err := redisx.New(ctx, cfg.Redis, logger, collector)
	if err != nil {
		return fmt.Errorf("connect to redis: %w", err)
	}

	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.WarnContext(ctx, "close redis client", slog.Any("error", err))
		}
	}()

	svc := service.New(redisClient, cfg.Limits)

	var opts []httpapi.Option

	if cfg.UI.Enabled {
		ui, err := webui.New(cfg, svc, logger, version)
		if err != nil {
			return fmt.Errorf("web ui: %w", err)
		}

		opts = append(opts, httpapi.WithUI(ui), httpapi.WithSwaggerUI())
	}

	api := httpapi.New(cfg, svc, logger, collector, version, opts...)

	group, groupCtx := errgroup.WithContext(ctx)

	group.Go(func() error {
		return serveHTTP(groupCtx, logger, "api", cfg.HTTP, cfg.HTTP.Addr, api)
	})

	if cfg.Metrics.Addr != "" {
		group.Go(func() error {
			return serveHTTP(groupCtx, logger, "metrics", cfg.HTTP, cfg.Metrics.Addr, metricsHandler(cfg.Metrics, collector))
		})
	}

	return group.Wait() //nolint:wrapcheck // errors are already descriptive
}

func metricsHandler(cfg config.Metrics, collector *metrics.Collector) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", collector.Handler())

	if cfg.Pprof {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}

	return mux
}

// serveHTTP serves until ctx is canceled and then shuts the server down gracefully.
// Long-lived requests (streams) observe the base context, which is canceled on shutdown.
func serveHTTP(
	ctx context.Context,
	logger *slog.Logger,
	name string,
	cfg config.HTTP,
	addr string,
	handler http.Handler,
) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("%s server: listen %s: %w", name, addr, err)
	}

	baseCtx, cancelBase := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelBase()

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	server.RegisterOnShutdown(cancelBase)

	errCh := make(chan error, 1)

	go func() {
		errCh <- server.Serve(listener)
	}()

	logger.InfoContext(ctx, "http server started", slog.String("server", name), slog.String("addr", listener.Addr().String()))

	select {
	case err := <-errCh:
		return fmt.Errorf("%s server: %w", name, err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("%s server: shutdown: %w", name, err)
	}

	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("%s server: %w", name, err)
	}

	logger.InfoContext(ctx, "http server stopped", slog.String("server", name))

	return nil
}
