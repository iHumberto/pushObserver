// Package webhook handles incoming webhook POST requests from Git platforms.
//
// Supported platforms: GitHub (sha256, sha1 legacy), Forgejo/Gitea (sha256),
// GitLab (token). Dev mode supports plain (no HMAC).
//
// Flow:
//  1. Extract hook ID from URL path
//  2. Look up hook config
//  3. Validate HMAC signature
//  4. Parse payload (JSON or form) to extract repo/branch/commit info
//  5. Trigger deploy engine
//  6. Return deploy result as JSON
package webhook

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"forgejo.humbertof.dev/humberto/push-observer/internal/config"
	"forgejo.humbertof.dev/humberto/push-observer/internal/deploy"
)

// Handler serves POST /hook/{id} with HMAC validation and deploy triggering.
type Handler struct {
	cfg      *config.Config // hook configuration access
	notifier func(hookID string, result *deploy.DeployResult) // notification callback
	engine   *deploy.Engine
}

// New creates a new webhook Handler.
func New(cfg *config.Config, engine *deploy.Engine) *Handler {
	return &Handler{
		cfg:    cfg,
		engine: engine,
	}
}

// SetNotifier sets the notification callback invoked after each deploy.
func (h *Handler) SetNotifier(fn func(hookID string, result *deploy.DeployResult)) {
	h.notifier = fn
}

// RegisterRoutes registers the webhook handler on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /hook/{id}", h.ServeHTTP)
}

// ServeHTTP handles POST /hook/{id}.
//
// Security:
//   - HMAC validation before any processing
//   - Error responses never leak secrets or internal paths
//   - Deploy serialization via engine lock (concurrent requests get 409)
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hookID := r.PathValue("id")
	if hookID == "" {
		writeHookError(w, http.StatusNotFound, "hook ID is required")
		return
	}

	// Validate content type.
	ct := r.Header.Get("Content-Type")
	if !isSupportedContentType(ct) {
		writeHookError(w, http.StatusUnsupportedMediaType,
			fmt.Sprintf("unsupported content type: %q (use application/json or application/x-www-form-urlencoded)", ct))
		return
	}

	// Find hook config.
	hook := h.findHook(hookID)
	if hook == nil {
		writeHookError(w, http.StatusNotFound, fmt.Sprintf("hook %q not found", hookID))
		return
	}

	// Read and preserve body for HMAC + downstream use.
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeHookError(w, http.StatusBadRequest, "cannot read request body")
		return
	}
	r.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))

	// HMAC validation (unless plain/dev mode).
	hmacCfg := hook.HMAC
	if hmacCfg.Type != "" && hmacCfg.Type != "plain" {
		hmacHeader := hmacCfg.Header
		if hmacHeader == "" {
			hmacHeader = "X-Hub-Signature-256"
		}
		if err := Validate(r, hmacCfg.Type, hmacCfg.Secret, hmacHeader); err != nil {
			slog.Warn("HMAC validation failed", "hook_id", hookID, "error", err)
			writeHookError(w, http.StatusUnauthorized, "HMAC validation failed")
			return
		}
	}

	// Trigger deploy.
	slog.Info("webhook received", "hook_id", hookID)
	result, err := h.engine.Deploy(r.Context(), hookID)
	if err != nil {
		slog.Error("deploy failed", "hook_id", hookID, "error", err)
		writeHookError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Notify if configured.
	if h.notifier != nil {
		h.notifier(hookID, result)
	}

	writeHookResponse(w, result)
}

// findHook looks up a hook configuration by ID.
func (h *Handler) findHook(hookID string) *config.HookConfig {
	if h.cfg != nil {
		if hk := h.cfg.HookByID(hookID); hk != nil {
			return hk
		}
	}
	// Fallback: use engine's view of hooks.
	return h.engine.HookByID(hookID)
}

// isSupportedContentType returns true for JSON and form-urlencoded.
func isSupportedContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	// Strip charset suffix (e.g., "application/json; charset=utf-8").
	if idx := strings.Index(ct, ";"); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	return ct == "application/json" || ct == "application/x-www-form-urlencoded"
}

// writeHookResponse writes a deploy result as JSON.
func writeHookResponse(w http.ResponseWriter, result *deploy.DeployResult) {
	status := "deployed"
	if result.Error != nil {
		status = "failed"
	}

	var services []string
	hasChanges := false
	for _, svc := range result.Services {
		if svc.Changed {
			hasChanges = true
			services = append(services, svc.Name)
		}
	}
	if !hasChanges && result.Error == nil && result.CommitBefore == result.CommitAfter {
		status = "no_changes"
	}

	resp := map[string]interface{}{
		"status":      status,
		"hook_id":     result.HookID,
		"commit":      result.CommitAfter,
		"duration_ms": result.Duration.Milliseconds(),
	}

	if len(services) > 0 {
		resp["services"] = services
	}
	if result.Error != nil {
		resp["error"] = result.Error.Error()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to write hook response", "error", err)
	}
}

// writeHookError writes a JSON error response without leaking secrets.
func writeHookError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": message}); err != nil {
		slog.Error("failed to write hook error response", "error", err)
	}
}
