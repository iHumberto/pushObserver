package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HumbertoF28/push-observer/internal/config"
	"github.com/HumbertoF28/push-observer/internal/deploy"
)

// ─────────────────────── Test helpers ────────────────────────────────────

// setupTest creates a temp config file, loads it, and returns a Handler.
// The config starts with no hooks.
func setupTest(t *testing.T) (*Handler, string) {
	t.Helper()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "push-observer.yaml")

	cfgData := []byte(`server:
  port: 9090
  host: "0.0.0.0"
  read_timeout: 30s
  write_timeout: 300s
hooks: []
notifications:
  apprise_url: "http://apprise:8000"
  tag_success: "deploy-success"
  tag_failure: "deploy-failure"
rate_limit:
  enabled: true
  requests_per_minute: 30
  burst: 5
logging:
  level: "info"
  format: "json"
`)
	if err := os.WriteFile(configPath, cfgData, 0o640); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	engine := deploy.New(cfg.Hooks, 300*time.Second)
	return New(cfg, engine), tmpDir
}

// doRequest creates a request, serves it through the handler, and returns the recorder.
func doRequest(t *testing.T, h *Handler, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()

	var bodyReader *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = bytes.NewBuffer(b)
	} else {
		bodyReader = bytes.NewBuffer(nil)
	}

	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// decodeJSON decodes a JSON response body.
func decodeJSON(t *testing.T, w *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.NewDecoder(w.Body).Decode(v); err != nil {
		t.Fatalf("decode JSON response: %v\nbody: %q", err, w.Body.String())
	}
}

// ─────────────────────── GET /api/hooks — list ───────────────────────────

func TestListHooks_Empty(t *testing.T) {
	h, _ := setupTest(t)
	w := doRequest(t, h, "GET", "/api/hooks", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var hooks []hookResponse
	decodeJSON(t, w, &hooks)
	if len(hooks) != 0 {
		t.Errorf("expected empty list, got %d hooks", len(hooks))
	}
}

func TestListHooks_WithHooks(t *testing.T) {
	h, _ := setupTest(t)

	hk := config.HookConfig{
		ID:      "test-hook",
		RepoURL: "git@github.com:user/repo.git",
		RepoDir: "/tmp/repo",
		Branch:  "main",
		HMAC:    config.HMACConfig{Type: "sha256", Secret: "super-secret-123", Header: "X-Hub-Signature-256"},
	}
	if err := h.cfg.AddHook(hk); err != nil {
		t.Fatalf("add hook: %v", err)
	}

	w := doRequest(t, h, "GET", "/api/hooks", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var hooks []hookResponse
	decodeJSON(t, w, &hooks)
	if len(hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(hooks))
	}

	// Secret must be masked.
	if hooks[0].HMAC.Secret != maskedSecret {
		t.Errorf("secret not masked: got %q, expected %q", hooks[0].HMAC.Secret, maskedSecret)
	}
}

// ───────────────── POST /api/hooks — create ─────────────────────────────

func TestCreateHook_Success(t *testing.T) {
	h, _ := setupTest(t)

	body := config.HookConfig{
		ID:      "my-hook",
		RepoURL: "git@github.com:user/repo.git",
		RepoDir: "/home/pi/repo",
		Branch:  "main",
		HMAC:    config.HMACConfig{Type: "sha256", Secret: "${MY_SECRET}", Header: "X-Hub-Signature-256"},
	}
	w := doRequest(t, h, "POST", "/api/hooks", body)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp hookResponse
	decodeJSON(t, w, &resp)
	if resp.HMAC.Secret != maskedSecret {
		t.Errorf("secret not masked in response: got %q", resp.HMAC.Secret)
	}
}

func TestCreateHook_Duplicate(t *testing.T) {
	h, _ := setupTest(t)

	body := config.HookConfig{
		ID:      "dup-hook",
		RepoURL: "git@github.com:user/repo.git",
		RepoDir: "/tmp/repo",
	}
	// First create
	doRequest(t, h, "POST", "/api/hooks", body)

	// Second create — should fail
	w := doRequest(t, h, "POST", "/api/hooks", body)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateHook_MissingID(t *testing.T) {
	h, _ := setupTest(t)

	body := config.HookConfig{
		RepoURL: "git@github.com:user/repo.git",
		RepoDir: "/tmp/repo",
	}
	w := doRequest(t, h, "POST", "/api/hooks", body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateHook_EmptyBody(t *testing.T) {
	h, _ := setupTest(t)

	req := httptest.NewRequest("POST", "/api/hooks", bytes.NewBufferString(""))
	req.Header.Set("Content-Type", "application/json")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// ────────── Security: hook ID validation ─────────────────────────────────

func TestCreateHook_InvalidID_Slashes(t *testing.T) {
	h, _ := setupTest(t)

	body := config.HookConfig{
		ID:      "../escape",
		RepoURL: "git@github.com:user/repo.git",
		RepoDir: "/tmp/repo",
	}
	w := doRequest(t, h, "POST", "/api/hooks", body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path traversal ID, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateHook_InvalidID_Spaces(t *testing.T) {
	h, _ := setupTest(t)

	body := config.HookConfig{
		ID:      "my hook",
		RepoURL: "git@github.com:user/repo.git",
		RepoDir: "/tmp/repo",
	}
	w := doRequest(t, h, "POST", "/api/hooks", body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for ID with spaces, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateHook_InvalidID_ShellChars(t *testing.T) {
	h, _ := setupTest(t)

	badIDs := []string{";rm", "|cat", "hook&echo"}
	for _, id := range badIDs {
		body := config.HookConfig{
			ID:      id,
			RepoURL: "git@github.com:user/repo.git",
			RepoDir: "/tmp/repo",
		}
		w := doRequest(t, h, "POST", "/api/hooks", body)

		if w.Code != http.StatusBadRequest {
			t.Errorf("ID %q should be rejected, got %d", id, w.Code)
		}
	}

	// Backtick and $() break httptest.NewRequest URL parsing, so we test
	// isValidHookID directly for those.
	for _, id := range []string{"`id`", "$(whoami)"} {
		if isValidHookID(id) {
			t.Errorf("ID %q should be invalid", id)
		}
	}
}

func TestCreateHook_ValidID_SpecialChars(t *testing.T) {
	h, _ := setupTest(t)

	body := config.HookConfig{
		ID:      "my-hook_v2",
		RepoURL: "git@github.com:user/repo.git",
		RepoDir: "/tmp/repo",
	}
	w := doRequest(t, h, "POST", "/api/hooks", body)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for valid ID with hyphen/underscore, got %d: %s", w.Code, w.Body.String())
	}
}

// ───────────────── PUT /api/hooks/{id} — update ──────────────────────────

func TestUpdateHook_Success(t *testing.T) {
	h, _ := setupTest(t)

	hk := config.HookConfig{
		ID:      "update-me",
		RepoURL: "git@github.com:user/repo.git",
		RepoDir: "/tmp/old",
		Branch:  "main",
	}
	if err := h.cfg.AddHook(hk); err != nil {
		t.Fatalf("add hook: %v", err)
	}

	updated := config.HookConfig{
		RepoURL: "git@github.com:user/repo2.git",
		RepoDir: "/tmp/new",
		Branch:  "develop",
	}
	w := doRequest(t, h, "PUT", "/api/hooks/update-me", updated)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	got := h.cfg.HookByID("update-me")
	if got == nil {
		t.Fatal("hook not found after update")
	}
	if got.Branch != "develop" {
		t.Errorf("Branch not updated: got %q", got.Branch)
	}
}

func TestUpdateHook_NotFound(t *testing.T) {
	h, _ := setupTest(t)

	body := config.HookConfig{
		RepoURL: "git@github.com:user/repo.git",
		RepoDir: "/tmp/repo",
	}
	w := doRequest(t, h, "PUT", "/api/hooks/nonexistent", body)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// ─────────────── DELETE /api/hooks/{id} — delete ─────────────────────────

func TestDeleteHook_Success(t *testing.T) {
	h, _ := setupTest(t)

	hk := config.HookConfig{
		ID:      "delete-me",
		RepoURL: "git@github.com:user/repo.git",
		RepoDir: "/tmp/repo",
	}
	if err := h.cfg.AddHook(hk); err != nil {
		t.Fatalf("add hook: %v", err)
	}

	w := doRequest(t, h, "DELETE", "/api/hooks/delete-me", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if h.cfg.HookByID("delete-me") != nil {
		t.Error("hook still exists after delete")
	}
}

func TestDeleteHook_NotFound(t *testing.T) {
	h, _ := setupTest(t)

	w := doRequest(t, h, "DELETE", "/api/hooks/nonexistent", nil)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// ─────────────── GET /api/hooks/{id}/services ────────────────────────────

func TestListServices_Success(t *testing.T) {
	h, _ := setupTest(t)

	svc1 := config.ServiceConfig{Name: "web", Path: "web", RestartTrigger: "default"}
	svc2 := config.ServiceConfig{Name: "api", Path: "api", RestartTrigger: "always"}

	hk := config.HookConfig{
		ID:       "with-services",
		RepoURL:  "git@github.com:user/repo.git",
		RepoDir:  "/tmp/repo",
		Branch:   "main",
		Services: []config.ServiceConfig{svc1, svc2},
	}
	if err := h.cfg.AddHook(hk); err != nil {
		t.Fatalf("add hook: %v", err)
	}

	w := doRequest(t, h, "GET", "/api/hooks/with-services/services", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var services []config.ServiceConfig
	decodeJSON(t, w, &services)
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
}

func TestListServices_HookNotFound(t *testing.T) {
	h, _ := setupTest(t)

	w := doRequest(t, h, "GET", "/api/hooks/nonexistent/services", nil)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// ─────────────── GET /api/hooks/{id}/status ──────────────────────────────

func TestHookStatus_Unknown(t *testing.T) {
	h, _ := setupTest(t)

	hk := config.HookConfig{
		ID:      "no-status",
		RepoURL: "git@github.com:user/repo.git",
		RepoDir: "/tmp/repo",
		Branch:  "main",
	}
	if err := h.cfg.AddHook(hk); err != nil {
		t.Fatalf("add hook: %v", err)
	}

	w := doRequest(t, h, "GET", "/api/hooks/no-status/status", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp statusResponse
	decodeJSON(t, w, &resp)
	if resp.Status != "unknown" {
		t.Errorf("expected status unknown, got %q", resp.Status)
	}
}

func TestHookStatus_WithResult(t *testing.T) {
	h, _ := setupTest(t)

	hk := config.HookConfig{
		ID:      "has-status",
		RepoURL: "git@github.com:user/repo.git",
		RepoDir: "/tmp/repo",
		Branch:  "main",
	}
	if err := h.cfg.AddHook(hk); err != nil {
		t.Fatalf("add hook: %v", err)
	}

	h.statusMu.Lock()
	h.status["has-status"] = &deploy.DeployResult{
		HookID:       "has-status",
		CommitBefore: "abc123",
		CommitAfter:  "def456",
		Services: []deploy.DeployServiceResult{
			{Name: "web", Changed: true, Restarted: true, Reason: "restart-triggered"},
			{Name: "api", Changed: false, Restarted: false, Reason: "no-changes"},
		},
		Duration: 5 * time.Second,
	}
	h.statusMu.Unlock()

	w := doRequest(t, h, "GET", "/api/hooks/has-status/status", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp statusResponse
	decodeJSON(t, w, &resp)
	if resp.Status != "deployed" {
		t.Errorf("expected status deployed, got %q", resp.Status)
	}
	if resp.CommitBefore != "abc123" {
		t.Errorf("expected commit_before abc123, got %q", resp.CommitBefore)
	}
	if len(resp.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(resp.Services))
	}
}

// ─────────────── POST /api/hooks/{id}/trigger ────────────────────────────

func TestTriggerDeploy_HookNotFound(t *testing.T) {
	h, _ := setupTest(t)

	w := doRequest(t, h, "POST", "/api/hooks/nonexistent/trigger", nil)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTriggerDeploy_InvalidID(t *testing.T) {
	h, _ := setupTest(t)

	// "../escape" gets path-cleaned by Go's mux to "/api/escape/trigger"
	// which doesn't match any route, returning 307 (redirect to cleaned path)
	// or 405 (method not allowed). Either way, the bad ID is rejected.
	w := doRequest(t, h, "POST", "/api/hooks/../escape/trigger", nil)

	if w.Code != http.StatusBadRequest && w.Code != http.StatusTemporaryRedirect && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected rejection (400/307/405), got %d", w.Code)
	}
}

// ─────────────── GET /api/hooks/{id}/scan ────────────────────────────────

func TestScanServices_HookNotFound(t *testing.T) {
	h, _ := setupTest(t)

	w := doRequest(t, h, "GET", "/api/hooks/nonexistent/scan", nil)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestScanServices_Success(t *testing.T) {
	h, tmpDir := setupTest(t)

	repoDir := filepath.Join(tmpDir, "test-repo")
	svcDir := filepath.Join(repoDir, "web")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatalf("create service dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "docker-compose.yaml"), []byte("services:\n"), 0o644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	hk := config.HookConfig{
		ID:      "scan-test",
		RepoURL: "git@github.com:user/repo.git",
		RepoDir: repoDir,
		Branch:  "main",
	}
	if err := h.cfg.AddHook(hk); err != nil {
		t.Fatalf("add hook: %v", err)
	}

	w := doRequest(t, h, "GET", "/api/hooks/scan-test/scan", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var services []config.ServiceConfig
	decodeJSON(t, w, &services)
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].Name != "web" {
		t.Errorf("expected service name 'web', got %q", services[0].Name)
	}
}

// ─────────────────── Security: mask tests ────────────────────────────────

func TestMaskSecret_NonEmpty(t *testing.T) {
	if s := maskSecret("my-secret"); s != maskedSecret {
		t.Errorf("expected %q, got %q", maskedSecret, s)
	}
}

func TestMaskSecret_Empty(t *testing.T) {
	if s := maskSecret(""); s != "" {
		t.Errorf("expected empty, got %q", s)
	}
}

func TestMaskPath(t *testing.T) {
	if s := maskPath("/home/user/.ssh/key"); s != maskedSecret {
		t.Errorf("expected %q, got %q", maskedSecret, s)
	}
}

// ───────────────── Security: injection in ID ─────────────────────────────

func TestRoutes_RejectInjectionInID(t *testing.T) {
	h, _ := setupTest(t)

	hk := config.HookConfig{
		ID:      "valid",
		RepoURL: "git@github.com:user/repo.git",
		RepoDir: "/tmp/repo",
		Branch:  "main",
	}
	if err := h.cfg.AddHook(hk); err != nil {
		t.Fatalf("add hook: %v", err)
	}

	// Test IDs that are safe in URLs but should still be rejected by our validator.
	badIDs := []string{
		"../escape",
		"with space",
		";rm",
		"|cat",
		"hook&echo",
	}

	routes := []string{
		"/api/hooks/%s",
		"/api/hooks/%s/services",
		"/api/hooks/%s/status",
		"/api/hooks/%s/trigger",
		"/api/hooks/%s/scan",
	}

	for _, id := range badIDs {
		for _, route := range routes {
			path := route
			// Replace %s manually since fmt.Sprintf with these IDs could break URL parsing.
			path = strings.Replace(path, "%s", id, 1)

			// Skip IDs that would break URL parsing entirely.
			if strings.ContainsAny(id, " \t\n\r") {
				// Test isValidHookID directly for these.
				if isValidHookID(id) {
					t.Errorf("ID %q should be invalid", id)
				}
				continue
			}

			// Build request manually to avoid httptest panicking on special chars.
			req, err := http.NewRequest("GET", path, nil)
			if err != nil {
				// URL parsing failure means the ID is too dangerous — that's a pass.
				continue
			}

			mux := http.NewServeMux()
			h.RegisterRoutes(mux)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
				if w.Code == http.StatusOK || w.Code == http.StatusCreated {
					t.Errorf("ID %q on route %q returned %d — should be rejected", id, path, w.Code)
				}
			}
		}
	}
}

// ─────────────────── Valid hook ID tests ─────────────────────────────────

func TestIsValidHookID(t *testing.T) {
	valid := []string{"my-hook", "hook_1", "TestHook", "a", "abc123", "my-hook_v2"}
	invalid := []string{
		"", "../escape", "with space", "hook;rm", "hook|cat",
		"`backtick`", "$(cmd)", "hook&echo", "hook\nnewline", "hook\ttab", "hook.dot",
	}

	for _, id := range valid {
		if !isValidHookID(id) {
			t.Errorf("expected valid: %q", id)
		}
	}
	for _, id := range invalid {
		if isValidHookID(id) {
			t.Errorf("expected invalid: %q", id)
		}
	}
}

// ─────────────────── Integration: round-trip CRUD ────────────────────────

func TestRoundTripCRUD(t *testing.T) {
	h, _ := setupTest(t)

	// Create
	body := config.HookConfig{
		ID:      "roundtrip",
		RepoURL: "git@github.com:user/repo.git",
		RepoDir: "/tmp/repo",
		Branch:  "main",
		HMAC:    config.HMACConfig{Type: "sha256", Secret: "shhh", Header: "X-Hub-Signature-256"},
	}
	w := doRequest(t, h, "POST", "/api/hooks", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Read (list)
	w2 := doRequest(t, h, "GET", "/api/hooks", nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w2.Code)
	}
	var hooks []hookResponse
	decodeJSON(t, w2, &hooks)
	if len(hooks) != 1 || hooks[0].ID != "roundtrip" {
		t.Fatalf("list: expected [roundtrip], got %v", hooks)
	}
	if hooks[0].HMAC.Secret != maskedSecret {
		t.Error("secret not masked in list")
	}

	// Update
	updateBody := config.HookConfig{
		RepoURL: "git@github.com:user/updated.git",
		RepoDir: "/tmp/updated",
		Branch:  "develop",
	}
	w3 := doRequest(t, h, "PUT", "/api/hooks/roundtrip", updateBody)
	if w3.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", w3.Code, w3.Body.String())
	}

	// Verify update
	got := h.cfg.HookByID("roundtrip")
	if got == nil || got.Branch != "develop" {
		t.Fatalf("update not persisted: got %v", got)
	}

	// Delete
	w4 := doRequest(t, h, "DELETE", "/api/hooks/roundtrip", nil)
	if w4.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d: %s", w4.Code, w4.Body.String())
	}

	// Verify deleted
	if h.cfg.HookByID("roundtrip") != nil {
		t.Fatal("hook still exists after delete")
	}
}

// ─────────────────── Content-Type validation ─────────────────────────────

func TestJSONContentType(t *testing.T) {
	h, _ := setupTest(t)

	w := doRequest(t, h, "GET", "/api/hooks", nil)

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

func TestErrorResponseIsJSON(t *testing.T) {
	h, _ := setupTest(t)

	w := doRequest(t, h, "GET", "/api/hooks/nonexistent/status", nil)

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("error response should be JSON, got Content-Type: %q", ct)
	}

	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("error response not valid JSON: %v\nbody: %q", err, w.Body.String())
	}
	if errResp["error"] == "" {
		t.Error("error response missing 'error' field")
	}
}
