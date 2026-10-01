// Command redigate exposes a Redis deployment (standalone, cluster or sentinel) over an HTTP API.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/btrvodka/redigate/internal/app"
	"github.com/btrvodka/redigate/internal/config"
	"github.com/btrvodka/redigate/internal/logger"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev" //nolint:gochecknoglobals // build-time variable

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(resolveVersion()) //nolint:forbidigo // CLI output

		return
	}

	if err := run(); err != nil {
		slog.Error("redigate stopped with error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log, err := logger.New(os.Stderr, cfg.Log)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}

	slog.SetDefault(log)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.InfoContext(ctx, "starting redigate", slog.String("version", resolveVersion()))

	return app.Run(ctx, cfg, log, resolveVersion()) //nolint:wrapcheck // top-level error
}

func resolveVersion() string {
	if version != "dev" {
		return version
	}

	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}

	return version
}
