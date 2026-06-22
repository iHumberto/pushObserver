// Package server implements the HTTP server, middlewares, and UI rendering.
//
// Responsibilities:
//   - HTTP server setup with graceful shutdown
//   - Middleware: rate limiting, request logging, panic recovery
//   - Server-side HTML dashboard rendering
//   - API handler delegation (REST /api/hooks)
//   - Webhook handler delegation (POST /hook/{id})
package server

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/HumbertoF28/push-observer/internal/api"
	"github.com/HumbertoF28/push-observer/internal/config"
	"github.com/HumbertoF28/push-observer/internal/deploy"
	"github.com/HumbertoF28/push-observer/internal/notify"
	"github.com/HumbertoF28/push-observer/internal/ratelimit"
	"github.com/HumbertoF28/push-observer/internal/webhook"
)

// ─────────────────────────────── Types ──────────────────────────────────

// Server holds the HTTP server state, middleware chain, and UI renderer.
type Server struct {
	cfg       *config.Config
	ui        *UIRenderer
	deploy    *deploy.Engine
	api       *api.Handler
	webhook   *webhook.Handler
	notifier  *notify.Notifier
	limiter   *ratelimit.Limiter
	srv       *http.Server
	resultsMu sync.RWMutex
	results   map[string]*deploy.DeployResult // hookID → last deploy result
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
}

// ───────────────────────── Constructor ──────────────────────────────────

// New creates a Server with parsed templates, deploy engine, rate limiter,
// API handler, and webhook handler.
func New(cfg *config.Config, deployEngine *deploy.Engine, tmpl *template.Template) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize rate limiter if enabled.
	var limiter *ratelimit.Limiter
	if cfg.RateLimit.Enabled {
		limiter = ratelimit.New(cfg.RateLimit.RequestsPerMinute, cfg.RateLimit.Burst)
	}

	// Initialize webhook handler.
	wh := webhook.New(cfg, deployEngine)

	// Initialize notifier and connect it to the webhook handler.
	var ntf *notify.Notifier
	if cfg.Notifications.AppriseURL != "" {
		timeout := 10 * time.Second
		ntf = notify.New(
			cfg.Notifications.AppriseURL,
			cfg.Notifications.TagSuccess,
			cfg.Notifications.TagFailure,
			cfg.Notifications.TagNoChanges,
			timeout,
		)
		wh.SetNotifier(func(hookID string, result *deploy.DeployResult) {
			// Notification is best-effort — errors are logged, not returned.
			if err := ntf.SendDeployResult(context.Background(), result, hookID); err != nil {
				slog.Warn("notification failed", "hook_id", hookID, "error", err)
			}
		})
	}

	s := &Server{
		cfg:      cfg,
		deploy:   deployEngine,
		api:      api.New(cfg, deployEngine),
		webhook:  wh,
		notifier: ntf,
		limiter:  limiter,
		results:  make(map[string]*deploy.DeployResult),
		ctx:      ctx,
		cancel:   cancel,
	}

	// Initialize UI renderer with parsed templates.
	s.ui = NewUIRenderer(tmpl, cfg, s)

	// Build the HTTP server.
	s.srv = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      s.routes(),
		ReadTimeout:  cfg.Server.ReadTimeout.Duration(),
		WriteTimeout: cfg.Server.WriteTimeout.Duration(),
		IdleTimeout:  120 * time.Second,
	}

	return s
}

// ─────────────────────── Lifecycle ──────────────────────────────────────

// Start begins listening and blocks until Shutdown is called or a signal arrives.
func (s *Server) Start() error {
	// Listen for OS signals in a goroutine.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case sig := <-sigCh:
			slog.Info("received signal, shutting down", "signal", sig)
			s.Shutdown()
		case <-s.ctx.Done():
			// Already shutting down.
		}
	}()

	slog.Info("HTTP server listening", "addr", s.srv.Addr)

	err := s.srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Shutdown gracefully stops the HTTP server with a 30s deadline.
func (s *Server) Shutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	slog.Info("shutting down HTTP server")
	return s.srv.Shutdown(ctx)
}

// ─────────────────────── Routing ────────────────────────────────────────

// routes builds the HTTP handler chain with middleware and endpoint registration.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// ── Webhook endpoint (POST /hook/{id}) ──
	s.webhook.RegisterRoutes(mux)

	// ── Dashboard UI pages (GET only) ──
	mux.HandleFunc("GET /", s.ui.Dashboard)
	mux.HandleFunc("GET /hooks/new", s.ui.NewHookForm)
	mux.HandleFunc("GET /hooks/{id}", s.ui.HookDetail)
	mux.HandleFunc("GET /hooks/{id}/edit", s.ui.EditHookForm)

	// ── Dashboard UI form submissions (POST/PUT/DELETE, form-encoded) ──
	mux.HandleFunc("POST /hooks", s.ui.CreateHook)
	mux.HandleFunc("PUT /hooks/{id}", s.ui.UpdateHook)
	mux.HandleFunc("DELETE /hooks/{id}", s.ui.DeleteHook)
	mux.HandleFunc("POST /hooks/{id}/scan", s.ui.ScanServices)
	mux.HandleFunc("POST /hooks/{id}/trigger", s.ui.TriggerDeploy)

	// ── REST API (delegated to api.Handler, JSON) ──
	s.api.RegisterRoutes(mux)

	// ── Health ──
	mux.HandleFunc("GET /health", s.handleHealth)

	// ── Middleware chain (outermost first) ──
	var handler http.Handler = mux
	handler = s.recoveryMiddleware(handler)
	handler = s.loggingMiddleware(handler)
	handler = s.securityHeadersMiddleware(handler)
	if s.limiter != nil {
		handler = s.rateLimitMiddleware(handler)
	}

	return handler
}

// ─────────────────────── Health ─────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// ─────────────────────── Last Result ────────────────────────────────────

// setLastResult stores a deploy result keyed by hook ID.
func (s *Server) setLastResult(hookID string, result *deploy.DeployResult) {
	s.resultsMu.Lock()
	defer s.resultsMu.Unlock()
	s.results[hookID] = result
}

// getLastResult retrieves the last deploy result for a hook, or nil if never deployed.
func (s *Server) getLastResult(hookID string) *deploy.DeployResult {
	s.resultsMu.RLock()
	defer s.resultsMu.RUnlock()
	return s.results[hookID]
}
