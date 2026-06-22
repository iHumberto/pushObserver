package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/HumbertoF28/push-observer/internal/config"
	"github.com/HumbertoF28/push-observer/internal/deploy"
)

// ─────────────────────────────── Types ──────────────────────────────────

// UIRenderer handles server-side HTML rendering for all dashboard pages.
type UIRenderer struct {
	tmpl       *template.Template
	cfg        *config.Config
	server     *Server
	csrf       map[string]string // session → token (simple CSRF for homelab use)
	tlsEnabled bool              // set via SERVER_TLS env var; controls CSRF cookie Secure flag
}

// PageData is the base template data passed to every page.
type PageData struct {
	Title    string
	Hooks    []config.HookConfig
	Results  map[string]*deploy.DeployResult // hookID → last result
	Section  string                          // "dashboard", "new-hook", "hook-detail", "edit-hook"
	Hook     *config.HookConfig              // current hook (for detail/edit)
	Services []ServiceView                   // services with trigger info
	CSRF     string
	Error    string
	Success  string
}

// ServiceView is a read-only view of a service with trigger display info.
type ServiceView struct {
	Name           string
	Path           string
	RestartTrigger string // "default", "always", "on-change"
	TriggerLabel   string // "Default", "Always", "Custom: .py, .yaml"
	Status         string // "deployed", "no-changes", "failed", "unknown"
	StatusClass    string // "green", "yellow", "red", ""
	LastDeploy     string // human-readable time or "never"
	Duration       string // human-readable duration or ""
}

// ───────────────────────── Constructor ──────────────────────────────────

// NewUIRenderer creates a UIRenderer with parsed templates and registered funcs.
func NewUIRenderer(tmpl *template.Template, cfg *config.Config, server *Server) *UIRenderer {
	ui := &UIRenderer{
		tmpl:       tmpl,
		cfg:        cfg,
		server:     server,
		csrf:       make(map[string]string),
		tlsEnabled: os.Getenv("SERVER_TLS") == "true",
	}
	return ui
}

// ─────────────────────── Template Functions ─────────────────────────────

// TemplateFuncs returns the function map registered with Go templates.
func TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"statusClass":    statusClass,
		"statusText":     statusText,
		"formatDuration": formatDuration,
		"formatTime":     formatTime,
		"triggerOptions": triggerOptions,
		"dict":           dict,
	}
}

// statusClass returns the CSS class for a deploy result status.
func statusClass(result *deploy.DeployResult) string {
	if result == nil {
		return ""
	}
	if result.Error != nil {
		return "red"
	}
	hasChanges := false
	for _, svc := range result.Services {
		if svc.Changed {
			hasChanges = true
			if svc.Error != nil {
				return "red"
			}
		}
	}
	if !hasChanges {
		return "yellow"
	}
	return "green"
}

// statusText returns a human-readable status label for a deploy result.
func statusText(result *deploy.DeployResult) string {
	if result == nil {
		return "never"
	}
	if result.Error != nil {
		return "failed"
	}
	hasChanges := false
	hasFailures := false
	for _, svc := range result.Services {
		if svc.Changed {
			hasChanges = true
			if svc.Error != nil {
				hasFailures = true
			}
		}
	}
	if hasFailures {
		return "failed"
	}
	if !hasChanges {
		return "no changes"
	}
	return "deployed"
}

// formatDuration renders a time.Duration as a short human-readable string.
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Truncate(time.Second).String()
}

// formatTime renders a time.Time for display.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Format("2006-01-02 15:04")
}

// triggerOptions returns HTML option elements for the restart trigger dropdown.
func triggerOptions(current string) template.HTML {
	options := []struct{ value, label string }{
		{"default", "Default (.env, Dockerfile, compose.yaml)"},
		{"always", "Always (every deploy)"},
		{"on-change", "Custom (specify extensions below)"},
	}
	var b strings.Builder
	for _, o := range options {
		sel := ""
		if o.value == current {
			sel = " selected"
		}
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`, o.value, sel, o.label)
	}
	// #nosec G203 — all option values and labels are hardcoded, not user-controlled
	return template.HTML(b.String())
}

// dict creates a map from alternating key/value pairs for template use.
func dict(values ...interface{}) (map[string]interface{}, error) {
	if len(values)%2 != 0 {
		return nil, fmt.Errorf("dict requires even number of arguments")
	}
	m := make(map[string]interface{}, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict keys must be strings")
		}
		m[key] = values[i+1]
	}
	return m, nil
}

// ─────────────────────── CSRF ───────────────────────────────────────────

// generateCSRF creates a random token and stores it keyed by session ID.
func (ui *UIRenderer) generateCSRF(w http.ResponseWriter) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Fallback: use timestamp-based token (less ideal but functional).
		b = []byte(fmt.Sprintf("csrf-%d", time.Now().UnixNano()))
	}
	token := base64.URLEncoding.EncodeToString(b)
	// Store with a cookie-based key (simple homelab CSRF).
	key := "session"
	ui.csrf[key] = token
	// Set cookie for double-submit verification.
	// #nosec G124 — Secure: ui.tlsEnabled (configurable via SERVER_TLS env var).
	// Defaults to false for homelab HTTP; set SERVER_TLS=true for HTTPS reverse proxy.
	http.SetCookie(w, &http.Cookie{
		Name:     "csrf_token",
		Value:    token,
		Path:     "/",
		Secure:   ui.tlsEnabled,
		HttpOnly: true, // server-side CSRF double-submit; cookie never read by JS
		SameSite: http.SameSiteStrictMode,
		MaxAge:   3600,
	})
	return token
}

// validateCSRF checks that the request's CSRF token matches the stored value.
func (ui *UIRenderer) validateCSRF(r *http.Request) bool {
	token := r.FormValue("csrf_token")
	if token == "" {
		token = r.Header.Get("X-CSRF-Token")
	}
	if token == "" {
		return false
	}
	// Also check cookie for double-submit pattern.
	cookie, err := r.Cookie("csrf_token")
	if err != nil || cookie.Value != token {
		return false
	}
	key := "session"
	if stored, ok := ui.csrf[key]; ok && stored == token {
		return true
	}
	return false
}

// validateCSRFWithToken validates CSRF using an already-extracted form token.
// Used for DELETE requests where ParseForm ignores the body.
func (ui *UIRenderer) validateCSRFWithToken(r *http.Request, formToken string) bool {
	if formToken == "" {
		return false
	}
	cookie, err := r.Cookie("csrf_token")
	if err != nil || cookie.Value != formToken {
		return false
	}
	key := "session"
	if stored, ok := ui.csrf[key]; ok && stored == formToken {
		return true
	}
	return false
}

// ─────────────────────── Dashboard (GET /) ──────────────────────────────

// Dashboard renders the main hook list page.
func (ui *UIRenderer) Dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	results := make(map[string]*deploy.DeployResult)
	for _, h := range ui.cfg.Hooks {
		if res := ui.server.getLastResult(h.ID); res != nil {
			results[h.ID] = res
		}
	}

	data := PageData{
		Title:   "pushObserver — Dashboard",
		Hooks:   ui.cfg.Hooks,
		Results: results,
		Section: "dashboard",
	}
	ui.render(w, "dashboard.html", data)
}

// ─────────────────────── Hook Form (GET /hooks/new) ─────────────────────

// NewHookForm renders the create hook page.
func (ui *UIRenderer) NewHookForm(w http.ResponseWriter, r *http.Request) {
	data := PageData{
		Title:   "pushObserver — Create Hook",
		Section: "new-hook",
		CSRF:    ui.generateCSRF(w),
	}
	ui.render(w, "dashboard.html", data)
}

// ─────────────────────── Hook Detail (GET /hooks/{id}) ───────────────────

// HookDetail renders the services page for a specific hook.
func (ui *UIRenderer) HookDetail(w http.ResponseWriter, r *http.Request) {
	hookID := r.PathValue("id")
	if !config.ValidHookID(hookID) {
		http.Error(w, "invalid hook ID", http.StatusBadRequest)
		return
	}
	hook := ui.findHook(hookID)
	if hook == nil {
		http.NotFound(w, r)
		return
	}

	services := ui.buildServiceViews(hook)
	result := ui.server.getLastResult(hookID)

	data := PageData{
		Title:    fmt.Sprintf("pushObserver — %s", hookID),
		Section:  "hook-detail",
		Hook:     hook,
		Services: services,
		Error:    r.URL.Query().Get("error"),
		Success:  r.URL.Query().Get("success"),
	}

	// Add per-service status from last result.
	if result != nil {
		for i := range data.Services {
			for _, svcResult := range result.Services {
				if data.Services[i].Name == svcResult.Name {
					data.Services[i].Status = serviceStatus(svcResult)
					data.Services[i].StatusClass = serviceStatusClass(svcResult)
					if result.Duration != 0 {
						data.Services[i].LastDeploy = formatTime(time.Now())
						data.Services[i].Duration = formatDuration(result.Duration)
					}
				}
			}
		}
	}

	ui.render(w, "dashboard.html", data)
}

// ─────────────────────── Edit Hook (GET /hooks/{id}/edit) ────────────────

// EditHookForm renders the edit hook page pre-filled with existing config.
func (ui *UIRenderer) EditHookForm(w http.ResponseWriter, r *http.Request) {
	hookID := r.PathValue("id")
	if !config.ValidHookID(hookID) {
		http.Error(w, "invalid hook ID", http.StatusBadRequest)
		return
	}
	hook := ui.findHook(hookID)
	if hook == nil {
		http.NotFound(w, r)
		return
	}

	data := PageData{
		Title:   fmt.Sprintf("pushObserver — Edit %s", hookID),
		Section: "edit-hook",
		Hook:    hook,
		CSRF:    ui.generateCSRF(w),
	}
	ui.render(w, "dashboard.html", data)
}

// ─────────────────────── API: Create Hook ───────────────────────────────

// CreateHook handles POST /hooks — creates a new hook from form data and saves to config.
func (ui *UIRenderer) CreateHook(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		ui.redirectError(w, r, "/hooks/new", "failed to parse form")
		return
	}
	if !ui.validateCSRF(r) {
		ui.redirectError(w, r, "/hooks/new", "invalid CSRF token")
		return
	}

	hook := config.HookConfig{
		ID:      r.FormValue("id"),
		RepoURL: r.FormValue("repo_url"),
		RepoDir: r.FormValue("repo_dir"),
		Branch:  r.FormValue("branch"),
		HMAC: config.HMACConfig{
			Type:   r.FormValue("hmac_type"),
			Secret: r.FormValue("hmac_secret"),
			Header: r.FormValue("hmac_header"),
		},
		ContentType: r.FormValue("content_type"),
		GitSSHKey:   r.FormValue("git_ssh_key"),
	}

	// Validate required fields.
	if hook.ID == "" || hook.RepoURL == "" || hook.RepoDir == "" {
		ui.redirectError(w, r, "/hooks/new", "id, repo_url, and repo_dir are required")
		return
	}

	// Check for duplicate ID.
	if ui.findHook(hook.ID) != nil {
		ui.redirectError(w, r, "/hooks/new", fmt.Sprintf("hook %q already exists", hook.ID))
		return
	}

	// Apply defaults.
	if hook.Branch == "" {
		hook.Branch = "main"
	}
	if hook.HMAC.Type == "" {
		hook.HMAC.Type = "sha256"
	}
	if hook.ContentType == "" {
		hook.ContentType = "json"
	}

	// Validate hook ID contains only safe characters (prevent open redirect and injection).
	if !config.ValidHookID(hook.ID) {
		ui.redirectError(w, r, "/hooks/new", "invalid hook ID: only alphanumeric, hyphens, underscores, and dots allowed")
		return
	}

	// Use config's AddHook which handles persistence with rollback.
	if err := ui.cfg.AddHook(hook); err != nil {
		ui.redirectError(w, r, "/hooks/new", "failed to save: "+err.Error())
		return
	}

	slog.Info("hook created", "id", hook.ID)
	// #nosec G710 — hook.ID is validated by config.ValidHookID above
	http.Redirect(w, r, "/hooks/"+hook.ID+"?success=Hook+created", http.StatusSeeOther)
}

// ─────────────────────── API: Update Hook ───────────────────────────────

// UpdateHook handles PUT /hooks/{id} — updates an existing hook from form data.
func (ui *UIRenderer) UpdateHook(w http.ResponseWriter, r *http.Request) {
	hookID := r.PathValue("id")
	if !config.ValidHookID(hookID) {
		http.Error(w, "invalid hook ID", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		ui.redirectError(w, r, "/hooks/"+hookID+"/edit", "failed to parse form")
		return
	}
	if !ui.validateCSRF(r) {
		ui.redirectError(w, r, "/hooks/"+hookID+"/edit", "invalid CSRF token")
		return
	}

	existing := ui.findHook(hookID)
	if existing == nil {
		http.NotFound(w, r)
		return
	}

	// Build updated config from form data, preserving existing services.
	updated := *existing
	updated.RepoURL = r.FormValue("repo_url")
	updated.RepoDir = r.FormValue("repo_dir")
	updated.Branch = r.FormValue("branch")
	updated.GitSSHKey = r.FormValue("git_ssh_key")
	updated.ContentType = r.FormValue("content_type")
	updated.HMAC = config.HMACConfig{
		Type:   r.FormValue("hmac_type"),
		Secret: r.FormValue("hmac_secret"),
		Header: r.FormValue("hmac_header"),
	}

	if updated.Branch == "" {
		updated.Branch = "main"
	}

	// Use config's UpdateHook which handles persistence with rollback.
	if err := ui.cfg.UpdateHook(hookID, updated); err != nil {
		ui.redirectError(w, r, "/hooks/"+hookID+"/edit", "failed to save: "+err.Error())
		return
	}

	slog.Info("hook updated", "id", hookID)
	// #nosec G710 — hookID validated at top of handler
	http.Redirect(w, r, "/hooks/"+hookID+"?success=Hook+updated", http.StatusSeeOther)
}

// ─────────────────────── API: Delete Hook ───────────────────────────────

// DeleteHook handles DELETE /hooks/{id} — removes a hook from config.
// Note: Go's ParseForm only parses body for POST/PUT/PATCH, so we manually
// parse the form body for DELETE requests to extract the CSRF token.
func (ui *UIRenderer) DeleteHook(w http.ResponseWriter, r *http.Request) {
	hookID := r.PathValue("id")
	if !config.ValidHookID(hookID) {
		http.Error(w, "invalid hook ID", http.StatusBadRequest)
		return
	}

	// Manually parse form body (ParseForm ignores body for DELETE).
	bodyBytes, _ := io.ReadAll(r.Body)
	formValues, _ := url.ParseQuery(string(bodyBytes))
	csrfToken := formValues.Get("csrf_token")

	if !ui.validateCSRFWithToken(r, csrfToken) {
		ui.redirectError(w, r, "/", "invalid CSRF token")
		return
	}

	if ui.findHook(hookID) == nil {
		http.NotFound(w, r)
		return
	}

	// Use config's RemoveHook which handles persistence with rollback.
	if err := ui.cfg.RemoveHook(hookID); err != nil {
		ui.redirectError(w, r, "/", "failed to delete hook")
		return
	}

	slog.Info("hook deleted", "id", hookID)
	http.Redirect(w, r, "/?success=Hook+deleted", http.StatusSeeOther)
}

// ─────────────────────── API: Scan Services ─────────────────────────────

// ScanServices handles POST /hooks/{id}/scan — rescans repo_dir for new services.
func (ui *UIRenderer) ScanServices(w http.ResponseWriter, r *http.Request) {
	hookID := r.PathValue("id")
	if !config.ValidHookID(hookID) {
		http.Error(w, "invalid hook ID", http.StatusBadRequest)
		return
	}
	hook := ui.findHook(hookID)
	if hook == nil {
		http.NotFound(w, r)
		return
	}

	// Use config's ScanServices for docker-compose.yaml discovery.
	scanned := config.ScanServices(hook.RepoDir)

	// Merge: keep existing services that still have compose files,
	// add new ones, preserve restart triggers for existing.
	existing := make(map[string]config.ServiceConfig)
	for _, svc := range hook.Services {
		existing[svc.Path] = svc
	}

	var merged []config.ServiceConfig
	for _, svc := range scanned {
		if exist, ok := existing[svc.Path]; ok {
			merged = append(merged, exist)
		} else {
			merged = append(merged, svc)
		}
	}
	hook.Services = merged

	if err := ui.cfg.Save(); err != nil {
		slog.Error("failed to save config after scan", "hookID", hookID, "error", err)
		http.Error(w, "failed to save config", http.StatusInternalServerError)
		return
	}

	slog.Info("services scanned", "id", hookID, "count", len(merged))
	// #nosec G710 — hookID validated at top of handler
	http.Redirect(w, r, "/hooks/"+hookID+"?success=Services+rescanned", http.StatusSeeOther)
}

// ─────────────────────── API: Trigger Deploy ────────────────────────────

// TriggerDeploy handles POST /hooks/{id}/trigger — manually triggers deploy.
func (ui *UIRenderer) TriggerDeploy(w http.ResponseWriter, r *http.Request) {
	hookID := r.PathValue("id")
	if !config.ValidHookID(hookID) {
		http.Error(w, "invalid hook ID", http.StatusBadRequest)
		return
	}
	if ui.findHook(hookID) == nil {
		http.NotFound(w, r)
		return
	}

	// Run deploy synchronously (for now; could be async in the future).
	result, err := ui.server.deploy.Deploy(r.Context(), hookID)
	if err != nil {
		slog.Error("manual deploy failed", "hookID", hookID, "error", err)
		// #nosec G710 — hookID validated at top of handler
		http.Redirect(w, r, "/hooks/"+hookID+"?error=Deploy+failed: "+err.Error(), http.StatusSeeOther)
		return
	}

	ui.server.setLastResult(hookID, result)
	slog.Info("manual deploy complete", "hookID", hookID, "status", statusText(result))
	// #nosec G710 — hookID validated at top of handler
	http.Redirect(w, r, "/hooks/"+hookID+"?success=Deploy+complete", http.StatusSeeOther)
}

// ─────────────────────── Helpers ────────────────────────────────────────

// render executes the named template with data and writes to the response.
func (ui *UIRenderer) render(w http.ResponseWriter, name string, data PageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ui.tmpl.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("template render failed", "template", name, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// findHook returns the HookConfig with the given ID, or nil.
func (ui *UIRenderer) findHook(id string) *config.HookConfig {
	for i := range ui.cfg.Hooks {
		if ui.cfg.Hooks[i].ID == id {
			return &ui.cfg.Hooks[i]
		}
	}
	return nil
}

// buildServiceViews converts HookConfig.Services to ServiceView for template rendering.
func (ui *UIRenderer) buildServiceViews(hook *config.HookConfig) []ServiceView {
	var views []ServiceView
	for _, svc := range hook.Services {
		v := ServiceView{
			Name:           svc.Name,
			Path:           svc.Path,
			RestartTrigger: svc.RestartTrigger,
			Status:         "unknown",
			StatusClass:    "",
			LastDeploy:     "never",
		}
		switch svc.RestartTrigger {
		case "always":
			v.TriggerLabel = "Always"
		case "on-change":
			exts := hook.Deploy.CustomExtensions
			if len(exts) > 0 {
				v.TriggerLabel = "Custom: " + strings.Join(exts, ", ")
			} else {
				v.TriggerLabel = "Custom"
			}
		default:
			v.TriggerLabel = "Default"
		}
		views = append(views, v)
	}
	return views
}

// serviceStatus returns a status string for a single service result.
func serviceStatus(svc deploy.DeployServiceResult) string {
	if svc.Error != nil {
		return "failed"
	}
	if svc.Restarted {
		return "deployed"
	}
	if svc.Reason == "no-changes" {
		return "no-changes"
	}
	return "unknown"
}

// serviceStatusClass returns a CSS class for a service result.
func serviceStatusClass(svc deploy.DeployServiceResult) string {
	switch serviceStatus(svc) {
	case "deployed":
		return "green"
	case "no-changes":
		return "yellow"
	case "failed":
		return "red"
	}
	return ""
}

// redirectError redirects with an error query parameter.
// Path is sanitized to strip CR/LF (prevents header injection).
func (ui *UIRenderer) redirectError(w http.ResponseWriter, r *http.Request, path, msg string) {
	// Strip carriage-return and line-feed to prevent HTTP header injection.
	path = strings.ReplaceAll(path, "\r", "")
	path = strings.ReplaceAll(path, "\n", "")
	// #nosec G710 — path sanitized above (CR/LF stripped); always internal callers
	http.Redirect(w, r, path+"?error="+msg, http.StatusSeeOther)
}
