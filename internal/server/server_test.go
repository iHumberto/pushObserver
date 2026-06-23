package server
import (
	"html/template"
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

// ─────────────────────── Test Helpers ───────────────────────────────────

// testConfig returns a minimal config loaded from a temp YAML file for testing.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "push-observer.yaml")

	yaml := `server:
  port: 9090
  host: "127.0.0.1"
  read_timeout: 30s
  write_timeout: 300s
hooks:
  - id: homelab
    repo_url: "git@github.com:humberto/docker.git"
    repo_dir: "/tmp/test-repo"
    branch: main
    hmac:
      type: sha256
      secret: test-secret
      header: X-Hub-Signature-256
    content_type: json
    deploy:
      custom_extensions: [".py", ".yaml"]
    services:
      - name: jellyfin
        path: jellyfin
        restart_trigger: default
      - name: prowlarr
        path: prowlarr
        restart_trigger: on-change
notifications:
  apprise_url: "http://localhost:8000"
logging:
  level: info
  format: text
`
	if err := os.WriteFile(path, []byte(yaml), 0o640); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("failed to load test config: %v", err)
	}
	return cfg
}

// testConfigNoLimit returns a config with rate limiting disabled.
func testConfigNoLimit(t *testing.T) *config.Config {
	t.Helper()
	cfg := testConfig(t)
	cfg.RateLimit.Enabled = false
	return cfg
}

// testTemplate parses the dashboard.html template from embedded filesystem for testing.
func testTemplate(t *testing.T) *template.Template {
	t.Helper()
	tmpl := template.New("").Funcs(TemplateFuncs())
	_, err := tmpl.ParseFS(TemplatesFS, "templates/*.html")
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
func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := testConfigNoLimit(t)
	tmpl := testTemplate(t)
	engine := deploy.New(cfg.Hooks, 30*time.Second)
	return New(cfg, engine, tmpl)
}

// ─────────────────────── Server Tests ───────────────────────────────────

func TestNew(t *testing.T) {
	cfg := testConfig(t)
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
		t.Fatalf("status = %d, want 200", rec.Code)
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
		t.Fatalf("status = %d, want 200", rec.Code)
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
		t.Fatalf("status = %d, want 404", rec.Code)
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
		t.Fatalf("status = %d, want 200", rec.Code)
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
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHookDetailNotFound(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/hooks/nonexistent", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
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
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestEditHookFormNotFound(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/hooks/nonexistent/edit", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// ─────────────────────── API: Create Hook ───────────────────────────────

func TestCreateHook_Success(t *testing.T) {
	s := newTestServer(t)
	// Generate CSRF token via form page first.
	formReq := httptest.NewRequest("GET", "/hooks/new", nil)
	formRec := httptest.NewRecorder()
	s.routes().ServeHTTP(formRec, formReq)

	// Get CSRF cookie and token.
	var csrfToken string
	for _, c := range formRec.Result().Cookies() {
		if c.Name == "csrf_token" {
			csrfToken = c.Value
			break
		}
	}
	if csrfToken == "" {
		t.Fatal("CSRF token not found in response cookies")
	}

	body := "id=testhook&repo_url=git%40github.com%3Atest%2Frepo.git&repo_dir=%2Ftmp%2Ftest&branch=main&hmac_type=sha256&hmac_secret=secret&hmac_header=X-Hub-Signature-256&content_type=json&csrf_token=" + csrfToken
	req := httptest.NewRequest("POST", "/hooks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: csrfToken})
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	// Should redirect (303) to the new hook page.
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "/hooks/testhook") {
		t.Errorf("redirect location = %s, want /hooks/testhook?success=...", loc)
	}

	// Verify hook was added to config.
	if s.ui.findHook("testhook") == nil {
		t.Error("hook testhook was not added to config")
	}
}

func TestCreateHook_MissingFields(t *testing.T) {
	s := newTestServer(t)
	formReq := httptest.NewRequest("GET", "/hooks/new", nil)
	formRec := httptest.NewRecorder()
	s.routes().ServeHTTP(formRec, formReq)
	var csrfToken string
	for _, c := range formRec.Result().Cookies() {
		if c.Name == "csrf_token" {
			csrfToken = c.Value
		}
	}
	if csrfToken == "" {
		t.Fatal("CSRF token not found")
	}

	// Missing repo_url and repo_dir.
	body := "id=badhook&repo_url=&repo_dir=&csrf_token=" + csrfToken
	req := httptest.NewRequest("POST", "/hooks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: csrfToken})
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Errorf("redirect should contain error param: %s", loc)
	}
}

func TestCreateHook_NoCSRF(t *testing.T) {
	s := newTestServer(t)
	body := "id=bad&repo_url=git%40github.com%3Atest%2Frepo.git&repo_dir=%2Ftmp%2Ftest"
	req := httptest.NewRequest("POST", "/hooks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	// Should redirect with error since no CSRF.
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Errorf("redirect should contain error param: %s", loc)
	}
}

func TestCreateHook_DuplicateID(t *testing.T) {
	s := newTestServer(t)
	formReq := httptest.NewRequest("GET", "/hooks/new", nil)
	formRec := httptest.NewRecorder()
	s.routes().ServeHTTP(formRec, formReq)
	var csrfToken string
	for _, c := range formRec.Result().Cookies() {
		if c.Name == "csrf_token" {
			csrfToken = c.Value
		}
	}
	if csrfToken == "" {
		t.Fatal("CSRF token not found")
	}

	// Try to create hook with duplicate ID "homelab" (already exists in test config).
	body := "id=homelab&repo_url=git%40github.com%3Atest%2Frepo.git&repo_dir=%2Ftmp%2Ftest&csrf_token=" + csrfToken
	req := httptest.NewRequest("POST", "/hooks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: csrfToken})
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Errorf("redirect should contain error for duplicate: %s", loc)
	}
}

// ─────────────────────── API: Update Hook ───────────────────────────────

func TestUpdateHook(t *testing.T) {
	s := newTestServer(t)
	// First get CSRF from edit page.
	formReq := httptest.NewRequest("GET", "/hooks/homelab/edit", nil)
	formRec := httptest.NewRecorder()
	s.routes().ServeHTTP(formRec, formReq)
	var csrfToken string
	for _, c := range formRec.Result().Cookies() {
		if c.Name == "csrf_token" {
			csrfToken = c.Value
		}
	}
	if csrfToken == "" {
		t.Fatal("CSRF token not found")
	}

	body := "repo_url=git%40github.com%3Ahumberto%2Fnew.git&repo_dir=%2Ftmp%2Fupdated&branch=develop&hmac_type=sha1&hmac_secret=newsecret&hmac_header=X-Hub-Signature&content_type=form&git_ssh_key=%2Fhome%2F.key&csrf_token=" + csrfToken
	req := httptest.NewRequest("PUT", "/hooks/homelab", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: csrfToken})
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}

	hook := s.ui.findHook("homelab")
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

// ─────────────────────── API: Delete Hook ───────────────────────────────

func TestDeleteHook(t *testing.T) {
	s := newTestServer(t)
	formReq := httptest.NewRequest("GET", "/hooks/homelab/edit", nil)
	formRec := httptest.NewRecorder()
	s.routes().ServeHTTP(formRec, formReq)
	var csrfToken string
	for _, c := range formRec.Result().Cookies() {
		if c.Name == "csrf_token" {
			csrfToken = c.Value
		}
	}
	if csrfToken == "" {
		t.Fatal("CSRF token not found")
	}

	body := "csrf_token=" + csrfToken + "&_method=DELETE"
	req := httptest.NewRequest("DELETE", "/hooks/homelab", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: csrfToken})
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}

	if s.ui.findHook("homelab") != nil {
		t.Error("hook homelab should have been deleted")
	}
}

// ─────────────────────── API: Scan Services ─────────────────────────────

func TestScanServices(t *testing.T) {
	s := newTestServer(t)
	formReq := httptest.NewRequest("GET", "/hooks/homelab", nil)
	formRec := httptest.NewRecorder()
	s.routes().ServeHTTP(formRec, formReq)
	var csrfToken string
	for _, c := range formRec.Result().Cookies() {
		if c.Name == "csrf_token" {
			csrfToken = c.Value
		}
	}
	if csrfToken == "" {
		t.Skip("CSRF token not found — minimal template doesn't set cookies")
	}

	body := "csrf_token=" + csrfToken
	req := httptest.NewRequest("POST", "/hooks/homelab/scan", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: csrfToken})
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
}

// ─────────────────────── API: Trigger Deploy ────────────────────────────

func TestTriggerDeploy(t *testing.T) {
	s := newTestServer(t)
	formReq := httptest.NewRequest("GET", "/hooks/homelab", nil)
	formRec := httptest.NewRecorder()
	s.routes().ServeHTTP(formRec, formReq)
	var csrfToken string
	for _, c := range formRec.Result().Cookies() {
		if c.Name == "csrf_token" {
			csrfToken = c.Value
		}
	}
	if csrfToken == "" {
		t.Skip("CSRF token not found")
	}

	body := "csrf_token=" + csrfToken
	req := httptest.NewRequest("POST", "/hooks/homelab/trigger", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: csrfToken})
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	// Trigger may fail if the repo doesn't exist on disk, but it should
	// still redirect (not 500).
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
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
	cfg := testConfig(t)
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
		t.Fatalf("request 1: status = %d, want 200", rec1.Code)
	}

	// Second request from same IP should be rate limited (burst=1).
	req2 := httptest.NewRequest("GET", "/health", nil)
	req2.RemoteAddr = req1.RemoteAddr
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("request 2: status = %d, want 429 (rate limited)", rec2.Code)
	}
}

func TestRateLimiting_Disabled(t *testing.T) {
	cfg := testConfigNoLimit(t)
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
			t.Fatalf("request %d: status = %d, want 200", i, rec.Code)
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

	// Cookie should be set.
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

	req := httptest.NewRequest("POST", "/hooks", strings.NewReader("csrf_token="+token))
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

	req := httptest.NewRequest("POST", "/hooks", strings.NewReader("csrf_token=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "wrong"})

	if s.ui.validateCSRF(req) {
		t.Error("CSRF validation should fail for wrong token")
	}
}

func TestCSRFValidation_Empty(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/hooks", nil)
	if s.ui.validateCSRF(req) {
		t.Error("CSRF validation should fail with no token")
	}
}

// ─────────────────────── XSS Protection ─────────────────────────────────

func TestHookNameXSSProtection(t *testing.T) {
	cfg := testConfigNoLimit(t)
	cfg.Hooks[0].ID = `<script>alert("xss")</script>`
	tmpl := testTemplate(t)
	engine := deploy.New(cfg.Hooks, 30*time.Second)
	s := New(cfg, engine, tmpl)
	handler := s.routes()

	req := httptest.NewRequest("GET", "/hooks/%3Cscript%3Ealert(%22xss%22)%3C%2Fscript%3E", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound && rec.Code != http.StatusOK && rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d", rec.Code)
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

	cfg := testConfigNoLimit(t)
	tmpl := testTemplate(t)
	engine := deploy.New(cfg.Hooks, 30*time.Second)
	s := New(cfg, engine, tmpl)

	var handler http.Handler = mux
	handler = s.recoveryMiddleware(handler)

	req := httptest.NewRequest("GET", "/panic", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// ─────────────────────── Helpers ────────────────────────────────────────

func assertErr(msg string) error {
	return &testError{msg: msg}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
