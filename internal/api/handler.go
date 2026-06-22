// Package api implements the REST API for hook management and deploy control.
//
// Endpoints:
//
//	GET    /api/hooks              — list hooks (secrets masked)
//	POST   /api/hooks              — create hook
//	PUT    /api/hooks/{id}         — update hook
//	DELETE /api/hooks/{id}         — delete hook
//	GET    /api/hooks/{id}/services — list services for a hook
//	GET    /api/hooks/{id}/status   — last deploy status
//	POST   /api/hooks/{id}/trigger  — trigger manual deploy
//	GET    /api/hooks/{id}/scan     — scan repo for docker-compose.yaml files
//
// Security:
//   - Secrets are masked in all list/get responses (never leaked via API).
//   - Hook IDs are validated against injection before use in file paths.
//   - Deploy triggers are serialized per hook (mutex in deploy.Engine).
//   - All JSON responses use Content-Type: application/json.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/HumbertoF28/push-observer/internal/config"
	"github.com/HumbertoF28/push-observer/internal/deploy"
)

const maskedSecret = "***"

// Handler serves the /api/hooks REST endpoints.
type Handler struct {
	cfg    *config.Config
	engine *deploy.Engine

	// In-memory deploy status store: hookID → last DeployResult.
	statusMu sync.RWMutex
	status   map[string]*deploy.DeployResult
}

// New creates a new API Handler.
func New(cfg *config.Config, engine *deploy.Engine) *Handler {
	return &Handler{
		cfg:    cfg,
		engine: engine,
		status: make(map[string]*deploy.DeployResult),
	}
}

// RegisterRoutes registers all API routes on the given mux.
// Uses Go 1.22+ method patterns.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/hooks", h.listHooks)
	mux.HandleFunc("POST /api/hooks", h.createHook)
	mux.HandleFunc("PUT /api/hooks/{id}", h.updateHook)
	mux.HandleFunc("DELETE /api/hooks/{id}", h.deleteHook)
	mux.HandleFunc("GET /api/hooks/{id}/services", h.listServices)
	mux.HandleFunc("GET /api/hooks/{id}/status", h.hookStatus)
	mux.HandleFunc("POST /api/hooks/{id}/trigger", h.triggerDeploy)
	mux.HandleFunc("GET /api/hooks/{id}/scan", h.scanServices)
}

// ─────────────────────────── Hook list/create ────────────────────────────

// hookResponse is the JSON shape for a hook in list/get responses.
// Secrets are masked.
type hookResponse struct {
	ID          string                   `json:"id"`
	RepoURL     string                   `json:"repo_url"`
	RepoDir     string                   `json:"repo_dir"`
	Branch      string                   `json:"branch"`
	GitSSHKey   string                   `json:"git_ssh_key"`
	HMAC        hmacResponse             `json:"hmac"`
	ContentType string                   `json:"content_type"`
	Services    []config.ServiceConfig   `json:"services"`
	Compose     config.ComposeConfig     `json:"compose"`
	Deploy      config.DeployConfig      `json:"deploy"`
	Notify      config.NotifyHookConfig  `json:"notify"`
}

type hmacResponse struct {
	Type   string `json:"type"`
	Secret string `json:"secret"` // always masked
	Header string `json:"header"`
}

// toHookResponse converts a HookConfig to a safe API response with masked secrets.
func toHookResponse(h config.HookConfig) hookResponse {
	return hookResponse{
		ID:          h.ID,
		RepoURL:     h.RepoURL,
		RepoDir:     h.RepoDir,
		Branch:      h.Branch,
		GitSSHKey:   maskPath(h.GitSSHKey),
		HMAC:        hmacResponse{Type: h.HMAC.Type, Secret: maskSecret(h.HMAC.Secret), Header: h.HMAC.Header},
		ContentType: h.ContentType,
		Services:    h.Services,
		Compose:     h.Compose,
		Deploy:      h.Deploy,
		Notify:      h.Notify,
	}
}

// maskSecret returns "***" if the secret is non-empty, otherwise "".
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	return maskedSecret
}

// maskPath masks an SSH key path for safe display.
func maskPath(s string) string {
	if s == "" {
		return ""
	}
	return maskedSecret
}

// listHooks handles GET /api/hooks.
func (h *Handler) listHooks(w http.ResponseWriter, r *http.Request) {
	hooks := make([]hookResponse, len(h.cfg.Hooks))
	for i, hk := range h.cfg.Hooks {
		hooks[i] = toHookResponse(hk)
	}
	writeJSON(w, http.StatusOK, hooks)
}

// createHook handles POST /api/hooks.
func (h *Handler) createHook(w http.ResponseWriter, r *http.Request) {
	var hk config.HookConfig
	if err := json.NewDecoder(r.Body).Decode(&hk); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if hk.ID == "" {
		writeError(w, http.StatusBadRequest, "hook id is required")
		return
	}

	// Validate the hook ID — alphanumeric + hyphens + underscores only.
	if !isValidHookID(hk.ID) {
		writeError(w, http.StatusBadRequest, "hook id contains invalid characters (use a-z, 0-9, -, _)")
		return
	}

	if hk.RepoURL == "" {
		writeError(w, http.StatusBadRequest, "repo_url is required")
		return
	}
	if hk.RepoDir == "" {
		writeError(w, http.StatusBadRequest, "repo_dir is required")
		return
	}

	// Apply defaults for empty fields.
	if hk.Branch == "" {
		hk.Branch = "main"
	}
	if hk.ContentType == "" {
		hk.ContentType = "json"
	}
	if hk.HMAC.Type == "" {
		hk.HMAC.Type = "sha256"
	}

	if err := h.cfg.AddHook(hk); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		slog.Error("failed to add hook", "hook_id", hk.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save hook")
		return
	}

	slog.Info("hook created", "hook_id", hk.ID)
	writeJSON(w, http.StatusCreated, toHookResponse(hk))
}

// ─────────────────────── Hook update/delete ──────────────────────────────

// updateHook handles PUT /api/hooks/{id}.
func (h *Handler) updateHook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || !isValidHookID(id) {
		writeError(w, http.StatusBadRequest, "invalid hook id")
		return
	}

	var updated config.HookConfig
	if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if err := h.cfg.UpdateHook(id, updated); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		slog.Error("failed to update hook", "hook_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save hook")
		return
	}

	slog.Info("hook updated", "hook_id", id)
	// Return the updated hook (re-read to get the canonical state).
	if hk := h.cfg.HookByID(id); hk != nil {
		writeJSON(w, http.StatusOK, toHookResponse(*hk))
	} else {
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "id": id})
	}
}

// deleteHook handles DELETE /api/hooks/{id}.
func (h *Handler) deleteHook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || !isValidHookID(id) {
		writeError(w, http.StatusBadRequest, "invalid hook id")
		return
	}

	if err := h.cfg.RemoveHook(id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		slog.Error("failed to delete hook", "hook_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete hook")
		return
	}

	// Clean up stored status.
	h.statusMu.Lock()
	delete(h.status, id)
	h.statusMu.Unlock()

	slog.Info("hook deleted", "hook_id", id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

// ─────────────────────── Services / Status / Trigger / Scan ──────────────

// listServices handles GET /api/hooks/{id}/services.
func (h *Handler) listServices(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || !isValidHookID(id) {
		writeError(w, http.StatusBadRequest, "invalid hook id")
		return
	}

	hk := h.cfg.HookByID(id)
	if hk == nil {
		writeError(w, http.StatusNotFound, "hook not found")
		return
	}

	writeJSON(w, http.StatusOK, hk.Services)
}

// hookStatus handles GET /api/hooks/{id}/status.
func (h *Handler) hookStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || !isValidHookID(id) {
		writeError(w, http.StatusBadRequest, "invalid hook id")
		return
	}

	if h.cfg.HookByID(id) == nil {
		writeError(w, http.StatusNotFound, "hook not found")
		return
	}

	h.statusMu.RLock()
	result, ok := h.status[id]
	h.statusMu.RUnlock()

	if !ok {
		writeJSON(w, http.StatusOK, map[string]string{
			"hook_id": id,
			"status":  "unknown",
			"message": "no deploy recorded yet",
		})
		return
	}

	writeJSON(w, http.StatusOK, statusResponseFromResult(result))
}

// statusResponse is the JSON shape for deploy status.
type statusResponse struct {
	HookID       string                   `json:"hook_id"`
	Status       string                   `json:"status"` // deployed, no_changes, failed
	CommitBefore string                   `json:"commit_before,omitempty"`
	CommitAfter  string                   `json:"commit_after,omitempty"`
	Services     []serviceStatusResponse  `json:"services,omitempty"`
	Duration     string                   `json:"duration,omitempty"`
	Error        string                   `json:"error,omitempty"`
}

type serviceStatusResponse struct {
	Name      string `json:"name"`
	Changed   bool   `json:"changed"`
	Restarted bool   `json:"restarted"`
	Reason    string `json:"reason"`
	Error     string `json:"error,omitempty"`
}

func statusResponseFromResult(r *deploy.DeployResult) statusResponse {
	sr := statusResponse{
		HookID:       r.HookID,
		CommitBefore: r.CommitBefore,
		CommitAfter:  r.CommitAfter,
		Duration:     r.Duration.String(),
	}

	if r.Error != nil {
		sr.Status = "failed"
		sr.Error = r.Error.Error()
		return sr
	}

	allNoChanges := true
	for _, svc := range r.Services {
		s := serviceStatusResponse{
			Name:    svc.Name,
			Changed: svc.Changed,
			Reason:  svc.Reason,
		}
		if svc.Restarted {
			s.Restarted = true
			allNoChanges = false
		}
		if svc.Error != nil {
			s.Error = svc.Error.Error()
		}
		sr.Services = append(sr.Services, s)
	}

	if allNoChanges && len(r.Services) > 0 {
		sr.Status = "no_changes"
	} else {
		sr.Status = "deployed"
	}

	return sr
}

// triggerDeploy handles POST /api/hooks/{id}/trigger.
func (h *Handler) triggerDeploy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || !isValidHookID(id) {
		writeError(w, http.StatusBadRequest, "invalid hook id")
		return
	}

	if h.cfg.HookByID(id) == nil {
		writeError(w, http.StatusNotFound, "hook not found")
		return
	}

	// Use request context with a reasonable timeout for deploy operations.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	result, err := h.engine.Deploy(ctx, id)
	if err != nil {
		// Deploy returned an operational error (lock contention, etc.).
		if strings.Contains(err.Error(), "already being deployed") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		slog.Error("deploy trigger failed", "hook_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Store result for status queries.
	h.statusMu.Lock()
	h.status[id] = result
	h.statusMu.Unlock()

	statusCode := http.StatusOK
	if result.Error != nil {
		statusCode = http.StatusInternalServerError
	}

	writeJSON(w, statusCode, statusResponseFromResult(result))
}

// scanServices handles GET /api/hooks/{id}/scan.
func (h *Handler) scanServices(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || !isValidHookID(id) {
		writeError(w, http.StatusBadRequest, "invalid hook id")
		return
	}

	hk := h.cfg.HookByID(id)
	if hk == nil {
		writeError(w, http.StatusNotFound, "hook not found")
		return
	}

	services := config.ScanServices(hk.RepoDir)
	writeJSON(w, http.StatusOK, services)
}

// ─────────────────────────── Helpers ─────────────────────────────────────

// isValidHookID validates that a hook ID contains only safe characters.
// Allows: a-z, A-Z, 0-9, hyphen, underscore. Must be non-empty and ≤ 128 chars.
func isValidHookID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

// writeJSON writes the value as JSON with proper Content-Type.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to write JSON response", "error", err)
	}
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
