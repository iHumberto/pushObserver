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
	"html/template"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"forgejo.humbertof.dev/humberto/push-observer/internal/config"
	"forgejo.humbertof.dev/humberto/push-observer/internal/deploy"
	"forgejo.humbertof.dev/humberto/push-observer/internal/server"
)

func main() {
	logLevel := slog.LevelInfo
	if os.Getenv("PUSH_OBSERVER_LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})))

	slog.Info("pushObserver starting", "version", "1.0.0-dev", "pid", os.Getpid())

	// Load configuration.
	configPath := os.Getenv("PUSH_OBSERVER_CONFIG")
	if configPath == "" {
		configPath = "push-observer.yaml"
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("failed to load config", "path", configPath, "error", err)
		os.Exit(1)
	}

	// Initialize deploy engine.
	engine := deploy.New(cfg.Hooks, cfg.Server.WriteTimeout.Duration())

	// Parse HTML templates.
	tmpl := template.Must(template.New("").Funcs(server.TemplateFuncs()).ParseGlob("internal/server/templates/*.html"))

	// Create and start HTTP server.
	srv := server.New(cfg, engine, tmpl)

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		slog.Info("pushObserver shutting down", "signal", ctx.Err())
		srv.Shutdown()
	}()

	if err := srv.Start(); err != nil {
		slog.Error("server stopped with error", "error", err)
		os.Exit(1)
	}

	slog.Info("pushObserver stopped.")
}
