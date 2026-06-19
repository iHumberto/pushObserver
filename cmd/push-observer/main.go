// Package main — pushObserver entrypoint.
//
// pushObserver is a webhook receiver specialized in Git-to-Docker deployments.
// It receives webhook POSTs from Git platforms (GitHub, Forgejo, Gitea, GitLab),
// validates HMAC signatures, pulls the repo, and runs docker compose up for
// changed services.
//
// One binary. Zero scripts.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Structured JSON logging to stdout — ready for log aggregation.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("pushObserver starting",
		"version", "1.0.0-dev",
		"pid", os.Getpid(),
	)

	// TODO: Load config from push-observer.yaml (internal/config)
	// TODO: Initialize deploy engine (internal/deploy)
	// TODO: Start HTTP server (internal/server)

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	slog.Info("pushObserver shutting down", "signal", ctx.Err())
	fmt.Println("pushObserver stopped.")
}
