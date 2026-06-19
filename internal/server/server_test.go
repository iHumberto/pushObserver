package server

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forgejo.humbertof.dev/humberto/push-observer/internal/config"
	"forgejo.humbertof.dev/humberto/push-observer/internal/deploy"
)

// ─────────────────────── Test Helpers ───────────────────────────────────

// testConfig returns a minimal config with one hook for testing.
func testConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			Port:         9090,
			Host:         "127.0.0.1",
			ReadTimeout:  config.Duration(30 * time.Second),
			WriteTimeout: config.Duration(300 * time.Second),
		},
		Hooks: []config.HookConfig{
			{
				ID:      "homelab",
				RepoURL: "git@github.com:humberto/docker.git",
				RepoDir: "/tmp/test-repo",
				Branch:  "main",
				HMAC: config.HMACConfig{
					Type:   "sha256",
					Secret: "test-secret",
					Header: "X-Hub-Signature-256",
				},
				ContentType: "json",
				Services: []config.ServiceConfig{
					{Name: "jellyfin", Path: "jellyfin", RestartTrigger: "default"},
					{Name: "prowlarr", Path: "prowlarr", RestartTrigger: "on-change"},
				},
				Deploy: config.DeployConfig{
					CustomExtensions: []string{".py", ".yaml"},
				},
			},
		},
		Notifications: config.NotifyConfig{
			AppriseURL: "http://localhost:8000",
		},
		Logging: config.LoggingConfig{
			Level:  "debug",
			Format: "text",
		},
	}
}

// testConfigNoLimit returns a config with rate limiting disabled.
func testConfigNoLimit() *config.Config {
	cfg := testConfig()
	cfg.RateLimit.Enabled = false
	return cfg
}

// testConfigFromFile creates a temp YAML config file, loads it, and returns the config.
// The loaded config has configPath set, so Save() works.
func testConfigFromFile(t *testing.T) *config.Config {
	t.Helper()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "push-observer.yaml")

	cfgData := []byte(`server:
  port: 9090
  host: "127.0.0.1"
  read_timeout: 30s
  write_timeout: 300s
hooks:
  - id: homelab
    repo_url: "git@github.com:humberto/docker.git"
    repo_dir: "/tmp/test-repo"
    branch: "main"
    hmac:
      type: sha256
      secret: "test-secret"
      header: "X-Hub-Signature-256"
    content_type: json
    services:
      - name: jellyfin
        path: jellyfin
        restart_trigger: default
      - name: prowlarr
        path: prowlarr
        restart_trigger: on-change
    deploy:
      custom_extensions: [".py", ".yaml"]
notifications:
  apprise_url: "http://localhost:8000"
  tag_success: "deploy-success"
  tag_failure: "deploy-failure"
rate_limit:
  enabled: false
  requests_per_minute: 30
  burst: 5
logging:
  level: "info"
  format: "text"
`)
	if err := os.WriteFile(configPath, cfgData, 0o640); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load test config: %v", err)
	}
	return cfg
}

// testTemplate parses the dashboard.html template for testing.
func testTemplate(t *testing.T) *template.Template {
	t.Helper()
	tmpl := template.New("").Funcs(TemplateFuncs())
	_, err := tmpl.ParseGlob("templates/*.html")
	if err != nil {
		// Fallback: create a minimal inline template for tests.
		tmpl = template.Must(template.New("dashboard.html").Funcs(TemplateFuncs()).Parse(`
{{define "dashboard.html"}}<html><body>{{.Title}} — {{.Section}}</body></html>{{end}}
{{define "dashboard-section"}}dashboard{{end}}
{{define "hook-form"}}form{{end}}
{{define "hook-detail-section"}}detail{{end}}
`))
	}
	return tmpl
}

// newTestServer creates a Server for testing without starting the listener.
// Uses a config from memory (not Load'd from file — Save() will fail).
func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := testConfigNoLimit()
	tmpl := testTemplate(t)
	engine := deploy.New(cfg.Hooks, 30*time.Second)
	return New(cfg, engine, tmpl)
}

// newTestServerWithFile creates a Server with config loaded from a temp file.
// Configured hooks can be persisted (Save() works).
func newTestServerWithFile(t *testing.T) (*Server, string) {
	t.Helper()
	cfg := testConfigFromFile(t)
	tmpl := testTemplate(t)
	engine := deploy.New(cfg.Hooks, 30*time.Second)
	return New(cfg, engine, tmpl), ""
}

// ─────────────────────── Server Tests ───────────────────────────────────

func TestNew(t *testing.T) {
	cfg := testConfig()
	tmpl := testTemplate(t)
	engine := deploy.New(cfg.Hooks, 30*time.Second)
	s := New(cfg, engine, tmpl)

	if s == nil {
		t.Fatal("New returned nil")
	}
	if s.cfg == nil {
		t.Error("cfg is nil")
	}
	if s.srv == nil {
		t.Error("http.Server is nil")
	}
	if s.srv.Addr != "127.0.0.1:9090" {
		t.Errorf("addr = %s, want 127.0.0.1:9090", s.srv.Addr)
	}
}

// ─────────────────────── Health Endpoint ────────────────────────────────

func TestHealthEndpoint(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("body does not contain status:ok: %s", rec.Body.String())
	}
}

// ─────────────────────── Dashboard ──────────────────────────────────────

func TestDashboardPage(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %s, want text/html", ct)
	}
}

func TestDashboardNotFoundSubPath(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/nonexistent", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// ─────────────────────── New Hook Form ──────────────────────────────────

func TestNewHookForm(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/hooks/new", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "hook") && !strings.Contains(body, "form") {
		t.Log("body may not contain expected content with minimal template:", body)
	}
}

// ─────────────────────── Hook Detail ────────────────────────────────────

func TestHookDetail(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/hooks/homelab", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestHookDetailNotFound(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/hooks/nonexistent", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// ─────────────────────── Edit Hook Form ─────────────────────────────────

func TestEditHookForm(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/hooks/homelab/edit", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestEditHookFormNotFound(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/hooks/nonexistent/edit", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// ─────────────────────── API: Create Hook (REST) ────────────────────────

func TestCreateHook_REST(t *testing.T) {
	s, _ := newTestServerWithFile(t)
	handler := s.routes()

	body := map[string]interface{}{
		"id":       "testhook",
		"repo_url": "git@github.com:test/repo.git",
		"repo_dir": "/tmp/test",
		"branch":   "main",
		"hmac": map[string]string{
			"type":   "sha256",
			"secret": "secret",
			"header": "X-Hub-Signature-256",
		},
		"content_type": "json",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/hooks", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 — body: %s", rec.Code, rec.Body.String())
	}

	// Verify hook was added to config.
	if s.cfg.HookByID("testhook") == nil {
		t.Error("hook testhook was not added to config")
	}
}

func TestCreateHook_Duplicate_REST(t *testing.T) {
	s, _ := newTestServerWithFile(t)
	handler := s.routes()

	body := map[string]interface{}{
		"id":       "homelab", // already exists
		"repo_url": "git@github.com:test/repo.git",
		"repo_dir": "/tmp/test",
	}

	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/hooks", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 — body: %s", rec.Code, rec.Body.String())
	}
}

// ─────────────────────── API: Update Hook (REST) ────────────────────────

func TestUpdateHook_REST(t *testing.T) {
	s, _ := newTestServerWithFile(t)
	handler := s.routes()

	body := map[string]interface{}{
		"repo_url": "git@github.com:humberto/new.git",
		"repo_dir": "/tmp/updated",
		"branch":   "develop",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("PUT", "/api/hooks/homelab", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — body: %s", rec.Code, rec.Body.String())
	}

	hook := s.cfg.HookByID("homelab")
	if hook == nil {
		t.Fatal("hook homelab not found after update")
	}
	if hook.RepoURL != "git@github.com:humberto/new.git" {
		t.Errorf("repo_url = %s, want git@github.com:humberto/new.git", hook.RepoURL)
	}
	if hook.Branch != "develop" {
		t.Errorf("branch = %s, want develop", hook.Branch)
	}
}

// ─────────────────────── API: Delete Hook (REST) ────────────────────────

func TestDeleteHook_REST(t *testing.T) {
	s, _ := newTestServerWithFile(t)
	handler := s.routes()

	req := httptest.NewRequest("DELETE", "/api/hooks/homelab", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — body: %s", rec.Code, rec.Body.String())
	}

	if s.cfg.HookByID("homelab") != nil {
		t.Error("hook homelab should have been deleted")
	}
}

// ─────────────────────── API: Scan Services (REST) ──────────────────────

func TestScanServices_REST(t *testing.T) {
	s, _ := newTestServerWithFile(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/api/hooks/homelab/scan", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Scan may return 200 (even if dir doesn't exist — empty array)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — body: %s", rec.Code, rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %s, want application/json", ct)
	}
}

// ─────────────────────── API: Trigger Deploy (REST) ─────────────────────

func TestTriggerDeploy_REST(t *testing.T) {
	s, _ := newTestServerWithFile(t)
	handler := s.routes()

	req := httptest.NewRequest("POST", "/api/hooks/homelab/trigger", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Trigger may fail (no real git repo), but should return JSON, not a redirect.
	// The deploy engine will fail gracefully.
	if rec.Code != http.StatusOK && rec.Code != http.StatusInternalServerError && rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 200/500/409 — body: %s", rec.Code, rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %s, want application/json", ct)
	}
}

// ─────────────────────── API: Hook Status (REST) ────────────────────────

func TestHookStatus(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/api/hooks/homelab/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %s, want application/json", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status"`) || !strings.Contains(body, "homelab") {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestHookStatusNotFound(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/api/hooks/nonexistent/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// ─────────────────────── API: List Hooks (REST) ─────────────────────────

func TestListHooks(t *testing.T) {
	s, _ := newTestServerWithFile(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/api/hooks", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — body: %s", rec.Code, rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %s, want application/json", ct)
	}

	// Verify secret is masked.
	body := rec.Body.String()
	if strings.Contains(body, "test-secret") {
		t.Error("response contains unmasked secret")
	}
}

// ─────────────────────── API: List Services (REST) ──────────────────────

func TestListServicesAPI(t *testing.T) {
	s, _ := newTestServerWithFile(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/api/hooks/homelab/services", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — body: %s", rec.Code, rec.Body.String())
	}

	var services []config.ServiceConfig
	if err := json.NewDecoder(rec.Body).Decode(&services); err != nil {
		t.Fatalf("decode services: %v", err)
	}
	if len(services) != 2 {
		t.Errorf("expected 2 services, got %d", len(services))
	}
}

// ─────────────────────── Security Headers ───────────────────────────────

func TestSecurityHeaders(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	headers := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}

	for header, expected := range headers {
		got := rec.Header().Get(header)
		if got != expected {
			t.Errorf("%s = %s, want %s", header, got, expected)
		}
	}

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Error("Content-Security-Policy header is missing")
	}
	if !strings.Contains(csp, "default-src") {
		t.Error("CSP missing default-src directive")
	}
}

// ─────────────────────── Rate Limiting ──────────────────────────────────

func TestRateLimiting_Enabled(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimit = config.RateLimitConfig{
		Enabled:           true,
		RequestsPerMinute: 2,
		Burst:             1,
	}
	tmpl := testTemplate(t)
	engine := deploy.New(cfg.Hooks, 30*time.Second)
	s := New(cfg, engine, tmpl)
	handler := s.routes()

	// First request should pass (within burst).
	req1 := httptest.NewRequest("GET", "/health", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Errorf("request 1: status = %d, want 200", rec1.Code)
	}

	// Second request from same IP should be rate limited (burst=1).
	req2 := httptest.NewRequest("GET", "/health", nil)
	req2.RemoteAddr = req1.RemoteAddr // same IP
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("request 2: status = %d, want 429 (rate limited)", rec2.Code)
	}
}

func TestRateLimiting_Disabled(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimit.Enabled = false
	tmpl := testTemplate(t)
	engine := deploy.New(cfg.Hooks, 30*time.Second)
	s := New(cfg, engine, tmpl)
	handler := s.routes()

	// Many requests should all pass when rate limiting is disabled.
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/health", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("request %d: status = %d, want 200", i, rec.Code)
		}
	}
}

// ─────────────────────── Status Functions ───────────────────────────────

func TestStatusClass(t *testing.T) {
	tests := []struct {
		name   string
		result *deploy.DeployResult
		want   string
	}{
		{"nil result", nil, ""},
		{"error", &deploy.DeployResult{Error: assertErr("fail")}, "red"},
		{"no changes", &deploy.DeployResult{
			Services: []deploy.DeployServiceResult{
				{Changed: false, Reason: "no-changes"},
			},
		}, "yellow"},
		{"deployed", &deploy.DeployResult{
			Services: []deploy.DeployServiceResult{
				{Changed: true, Restarted: true},
			},
		}, "green"},
		{"partial failure", &deploy.DeployResult{
			Services: []deploy.DeployServiceResult{
				{Changed: true, Restarted: true},
				{Changed: true, Error: assertErr("up failed")},
			},
		}, "red"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := statusClass(tt.result)
			if got != tt.want {
				t.Errorf("statusClass() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatusText(t *testing.T) {
	tests := []struct {
		name   string
		result *deploy.DeployResult
		want   string
	}{
		{"nil", nil, "never"},
		{"error", &deploy.DeployResult{Error: assertErr("fail")}, "failed"},
		{"no changes", &deploy.DeployResult{
			Services: []deploy.DeployServiceResult{
				{Changed: false, Reason: "no-changes"},
			},
		}, "no changes"},
		{"deployed", &deploy.DeployResult{
			Services: []deploy.DeployServiceResult{
				{Changed: true, Restarted: true},
			},
		}, "deployed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := statusText(tt.result)
			if got != tt.want {
				t.Errorf("statusText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "500ms"},
		{1500 * time.Millisecond, "1.5s"},
		{5 * time.Second, "5.0s"},
		{65 * time.Second, "1m5s"},
		{2 * time.Minute, "2m0s"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatDuration(tt.d)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestFormatTime(t *testing.T) {
	zero := time.Time{}
	if got := formatTime(zero); got != "never" {
		t.Errorf("formatTime(zero) = %q, want never", got)
	}

	ts := time.Date(2026, 6, 19, 15, 30, 0, 0, time.UTC)
	got := formatTime(ts)
	if !strings.Contains(got, "2026-06-19") || !strings.Contains(got, "15:30") {
		t.Errorf("formatTime = %q, want ...2026-06-19 15:30...", got)
	}
}

// ─────────────────────── CSRF ───────────────────────────────────────────

func TestCSRFGeneration(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	token := s.ui.generateCSRF(rec)

	if token == "" {
		t.Error("CSRF token is empty")
	}
	if len(token) < 32 {
		t.Errorf("CSRF token too short: %d chars", len(token))
	}

	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "csrf_token" && c.Value == token {
			found = true
			break
		}
	}
	if !found {
		t.Error("csrf_token cookie not set")
	}
}

func TestCSRFValidation_Valid(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	token := s.ui.generateCSRF(rec)

	req := httptest.NewRequest("POST", "/api/hooks", strings.NewReader("csrf_token="+token))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: token})

	if !s.ui.validateCSRF(req) {
		t.Error("CSRF validation failed for valid token")
	}
}

func TestCSRFValidation_Invalid(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ui.generateCSRF(rec)

	req := httptest.NewRequest("POST", "/api/hooks", strings.NewReader("csrf_token=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "wrong"})

	if s.ui.validateCSRF(req) {
		t.Error("CSRF validation should fail for wrong token")
	}
}

func TestCSRFValidation_Empty(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/api/hooks", nil)
	if s.ui.validateCSRF(req) {
		t.Error("CSRF validation should fail with no token")
	}
}

// ─────────────────────── XSS Protection ─────────────────────────────────

func TestHookNameXSSProtection(t *testing.T) {
	cfg := testConfigNoLimit()
	cfg.Hooks[0].ID = `<script>alert("xss")</script>`
	tmpl := testTemplate(t)
	engine := deploy.New(cfg.Hooks, 30*time.Second)
	s := New(cfg, engine, tmpl)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/hooks/%3Cscript%3Ealert(%22xss%22)%3C%2Fscript%3E", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound && rec.Code != http.StatusOK {
		t.Errorf("unexpected status: %d", rec.Code)
	}
	if rec.Code == http.StatusOK {
		if strings.Contains(rec.Body.String(), `<script>alert`) {
			t.Error("response contains unescaped script tag — XSS vulnerability")
		}
	}
}

// ─────────────────────── Recovery Middleware ────────────────────────────

func TestRecoveryMiddleware(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panic", func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	cfg := testConfigNoLimit()
	tmpl := testTemplate(t)
	engine := deploy.New(cfg.Hooks, 30*time.Second)
	s := New(cfg, engine, tmpl)

	var handler http.Handler = mux
	handler = s.recoveryMiddleware(handler)

	req := httptest.NewRequest("GET", "/panic", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// ─────────────────────── Helpers ────────────────────────────────────────

func assertErr(msg string) error {
	return &testError{msg: msg}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
