package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ohanyere/cluster-meter/internal/cli"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cmd := cli.NewRootCommand(cli.RootOptions{
		Version: version,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Logger:  logger,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cmd.ExecuteContext(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}

		logger.Error("command execution failed", slog.Any("error", err))
		os.Exit(1)
	}
}
