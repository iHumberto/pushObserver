package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// ──────────────────────── Env var substitution tests ─────────────────────

func TestExpandEnv_Simple(t *testing.T) {
	os.Setenv("TEST_VAR", "hello")
	defer os.Unsetenv("TEST_VAR")

	result := expandEnv([]byte("prefix ${TEST_VAR} suffix"))
	if string(result) != "prefix hello suffix" {
		t.Errorf("expected 'prefix hello suffix', got %q", string(result))
	}
}

func TestExpandEnv_MissingWithoutDefault(t *testing.T) {
	result := expandEnv([]byte("${DOES_NOT_EXIST}"))
	if string(result) != "" {
		t.Errorf("expected empty string, got %q", string(result))
	}
}

func TestExpandEnv_MissingWithDefault(t *testing.T) {
	result := expandEnv([]byte("${DOES_NOT_EXIST:fallback}"))
	if string(result) != "fallback" {
		t.Errorf("expected 'fallback', got %q", string(result))
	}
}

func TestExpandEnv_ExistingWithDefault(t *testing.T) {
	os.Setenv("PRESENT", "real")
	defer os.Unsetenv("PRESENT")

	result := expandEnv([]byte("${PRESENT:ignored}"))
	if string(result) != "real" {
		t.Errorf("expected 'real', got %q", string(result))
	}
}

func TestExpandEnv_MultipleVars(t *testing.T) {
	os.Setenv("A", "1")
	os.Setenv("B", "2")
	defer os.Unsetenv("A")
	defer os.Unsetenv("B")

	result := expandEnv([]byte("${A}:${B}:${C:nope}"))
	if string(result) != "1:2:nope" {
		t.Errorf("expected '1:2:nope', got %q", string(result))
	}
}

// ──────── Security: env var injection via crafted config ────────────────

func TestExpandEnv_NoShellExpansion(t *testing.T) {
	// Env vars containing shell metacharacters should be literal, not executed.
	os.Setenv("SHELL_VAR", "$(id) `whoami`")
	defer os.Unsetenv("SHELL_VAR")

	result := expandEnv([]byte("${SHELL_VAR}"))
	if string(result) != "$(id) `whoami`" {
		t.Errorf("shell metacharacters were altered: got %q", string(result))
	}
}

func TestExpandEnv_RecursiveExpansionBlocked(t *testing.T) {
	// A="${B}" and B="secret" — should NOT double-expand A.
	os.Setenv("A", "ref_${B}")
	os.Setenv("B", "secret")
	defer os.Unsetenv("A")
	defer os.Unsetenv("B")

	result := expandEnv([]byte("${A}"))
	if string(result) != "ref_${B}" {
		t.Errorf("recursive expansion occurred: got %q", string(result))
	}
}

func TestExpandEnv_EmptyVarName(t *testing.T) {
	// ${} is not a valid pattern — should be left alone.
	result := expandEnv([]byte("prefix ${} suffix"))
	if string(result) != "prefix ${} suffix" {
		t.Errorf("empty var name was expanded: got %q", string(result))
	}
}

// ──────── Security: YAML bomb / DoS vector ──────────────────────────────

func TestExpandEnv_LargeValue(t *testing.T) {
	// Large env values shouldn't crash.
	os.Setenv("HUGE", strings.Repeat("x", 10_000))
	defer os.Unsetenv("HUGE")

	result := expandEnv([]byte("${HUGE}"))
	if len(result) != 10_000 {
		t.Errorf("expected 10000 bytes, got %d", len(result))
	}
}

// ──────────────────────── Load tests ────────────────────────────────────

func TestLoad_ValidConfig(t *testing.T) {
	tmpDir := setupTempConfig(t, `server:
  port: 9090
  host: "0.0.0.0"
  read_timeout: 30s
  write_timeout: 300s
hooks:
  - id: test
    repo_url: "git@github.com:u/r.git"
    repo_dir: "/tmp/test"
    branch: "main"
    hmac:
      type: sha256
      secret: "${HMAC_SECRET:default}"
      header: "X-Hub-Signature-256"
notifications:
  apprise_url: "http://apprise:8000"
  tag_success: "ok"
rate_limit:
  enabled: true
  requests_per_minute: 30
  burst: 5
logging:
  level: "info"
  format: "json"
`)
	defer os.RemoveAll(tmpDir)

	os.Setenv("HMAC_SECRET", "testsecret")
	defer os.Unsetenv("HMAC_SECRET")

	cfg, err := Load(filepath.Join(tmpDir, "push-observer.yaml"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Server.ReadTimeout.Duration() != 30*time.Second {
		t.Errorf("expected 30s read_timeout, got %v", cfg.Server.ReadTimeout)
	}
	if cfg.Server.WriteTimeout.Duration() != 300*time.Second {
		t.Errorf("expected 300s write_timeout, got %v", cfg.Server.WriteTimeout)
	}
	if len(cfg.Hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(cfg.Hooks))
	}
	if cfg.Hooks[0].ID != "test" {
		t.Errorf("expected hook id 'test', got %q", cfg.Hooks[0].ID)
	}
	if cfg.Hooks[0].HMAC.Secret != "testsecret" {
		t.Errorf("expected secret 'testsecret' (from env), got %q", cfg.Hooks[0].HMAC.Secret)
	}
	if cfg.Hooks[0].HMAC.Header != "X-Hub-Signature-256" {
		t.Errorf("expected header X-Hub-Signature-256, got %q", cfg.Hooks[0].HMAC.Header)
	}
}

func TestLoad_MissingFile_CreatesDefault(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "push-observer.yaml")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load should create default: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("expected default port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("expected default level info, got %q", cfg.Logging.Level)
	}

	// File must exist now.
	if _, err := os.Stat(cfgPath); err != nil {
		t.Errorf("default config file was not created: %v", err)
	}

	// Save() must work after Load creates default from missing file.
	// This verifies configPath is set (otherwise Save fails with
	// "config was not loaded from a file").
	cfg.Hooks = append(cfg.Hooks, HookConfig{
		ID: "test", RepoURL: "git@x", RepoDir: "/tmp",
	})
	if err := cfg.Save(); err != nil {
		t.Errorf("Save after default config creation failed: %v", err)
	}
}

func TestLoad_EnvSubstitution(t *testing.T) {
	os.Setenv("MY_PORT", "1234")
	os.Setenv("MY_SECRET", "supersecret")
	defer os.Unsetenv("MY_PORT")
	defer os.Unsetenv("MY_SECRET")

	tmpDir := setupTempConfig(t, `server:
  port: ${MY_PORT}
  host: "0.0.0.0"
  read_timeout: 10s
  write_timeout: 60s
hooks:
  - id: test
    repo_url: "git@x"
    repo_dir: "/tmp"
    branch: "main"
    hmac:
      type: sha256
      secret: "${MY_SECRET}"
      header: "X-Hub-Signature-256"
notifications:
  apprise_url: "http://x"
logging:
  level: info
  format: json
`)
	defer os.RemoveAll(tmpDir)

	cfg, err := Load(filepath.Join(tmpDir, "push-observer.yaml"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.Port != 1234 {
		t.Errorf("expected port 1234 (from env), got %d", cfg.Server.Port)
	}
	if cfg.Hooks[0].HMAC.Secret != "supersecret" {
		t.Errorf("expected secret from env, got %q", cfg.Hooks[0].HMAC.Secret)
	}
}

func TestLoad_EnvDefaultFallback(t *testing.T) {
	tmpDir := setupTempConfig(t, `server:
  port: ${MISSING_PORT:8080}
  host: "0.0.0.0"
  read_timeout: 10s
  write_timeout: 60s
hooks:
  - id: test
    repo_url: "git@x"
    repo_dir: "/tmp"
    branch: "main"
    hmac:
      type: sha256
      secret: "${MISSING_SECRET:fallback-key}"
      header: "X-Hub-Signature-256"
notifications:
  apprise_url: "http://x"
logging:
  level: info
  format: json
`)
	defer os.RemoveAll(tmpDir)

	cfg, err := Load(filepath.Join(tmpDir, "push-observer.yaml"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("expected port 8080 (default), got %d", cfg.Server.Port)
	}
	if cfg.Hooks[0].HMAC.Secret != "fallback-key" {
		t.Errorf("expected 'fallback-key' from env default, got %q", cfg.Hooks[0].HMAC.Secret)
	}
}

func TestLoad_UnknownFieldRejected(t *testing.T) {
	tmpDir := setupTempConfig(t, `server:
  port: 9090
  host: "0.0.0.0"
  read_timeout: 10s
  write_timeout: 60s
  illegal_field: "should_fail"
hooks:
  - id: test
    repo_url: "git@x"
    repo_dir: "/tmp"
    branch: "main"
    hmac:
      type: sha256
      secret: "x"
      header: "X-Hub-Signature-256"
notifications:
  apprise_url: "http://x"
logging:
  level: info
  format: json
`)
	defer os.RemoveAll(tmpDir)

	_, err := Load(filepath.Join(tmpDir, "push-observer.yaml"))
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "illegal_field") {
		t.Errorf("error should mention illegal_field, got: %v", err)
	}
}

// ──────── Security: Path traversal via env var ──────────────────────────

func TestLoad_EnvSubstitutionDoesNotExpandPaths(t *testing.T) {
	// Env substitution is string replacement, not path traversal.
	// The scan uses filepath.Join which normalizes. This test verifies
	// that repo_dir values from env are used literally (no escaping).
	os.Setenv("TRAVERSAL_PATH", "../../../etc")
	defer os.Unsetenv("TRAVERSAL_PATH")

	tmpDir := setupTempConfig(t, `server:
  port: 9090
  host: "0.0.0.0"
  read_timeout: 10s
  write_timeout: 60s
hooks:
  - id: test
    repo_url: "git@x"
    repo_dir: "${TRAVERSAL_PATH}"
    branch: "main"
    hmac:
      type: plain
notifications:
  apprise_url: "http://x"
logging:
  level: info
  format: json
`)
	defer os.RemoveAll(tmpDir)

	cfg, err := Load(filepath.Join(tmpDir, "push-observer.yaml"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	// The value is literal; the caller (deploy engine) must validate
	// that repo_dir is within an allowed prefix. This test ensures
	// we don't silently normalize paths in the config layer.
	if cfg.Hooks[0].RepoDir != "../../../etc" {
		t.Errorf("expected literal '../../../etc', got %q", cfg.Hooks[0].RepoDir)
	}
}

// ──────────────────────── Validation tests ──────────────────────────────

func TestValidate_ValidConfig(t *testing.T) {
	cfg := validConfig()
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected valid config, got: %v", err)
	}
}

func TestValidate_DuplicateHookIDs(t *testing.T) {
	cfg := validConfig()
	cfg.Hooks = append(cfg.Hooks, cfg.Hooks[0]) // duplicate
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for duplicate hook ID")
	}
	if !strings.Contains(err.Error(), "duplicated") {
		t.Errorf("error should mention 'duplicated', got: %v", err)
	}
}

func TestValidate_EmptyHookID(t *testing.T) {
	cfg := validConfig()
	cfg.Hooks[0].ID = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for empty hook ID")
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Errorf("error should mention 'id is required', got: %v", err)
	}
}

func TestValidate_InvalidHMACType(t *testing.T) {
	cfg := validConfig()
	cfg.Hooks[0].HMAC.Type = "md5"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid HMAC type")
	}
	if !strings.Contains(err.Error(), "hmac.type") {
		t.Errorf("error should mention hmac.type, got: %v", err)
	}
}

func TestValidate_HMACWithoutSecret(t *testing.T) {
	cfg := validConfig()
	cfg.Hooks[0].HMAC.Type = "sha256"
	cfg.Hooks[0].HMAC.Secret = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing HMAC secret")
	}
	if !strings.Contains(err.Error(), "hmac.secret") {
		t.Errorf("error should mention hmac.secret, got: %v", err)
	}
}

func TestValidate_HMACWithoutHeader(t *testing.T) {
	cfg := validConfig()
	cfg.Hooks[0].HMAC.Type = "sha256"
	cfg.Hooks[0].HMAC.Header = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing HMAC header")
	}
	if !strings.Contains(err.Error(), "hmac.header") {
		t.Errorf("error should mention hmac.header, got: %v", err)
	}
}

func TestValidate_PlainHMACSkipsSecretCheck(t *testing.T) {
	// plain HMAC type doesn't need secret or header.
	cfg := validConfig()
	cfg.Hooks[0].HMAC.Type = "plain"
	cfg.Hooks[0].HMAC.Secret = ""
	cfg.Hooks[0].HMAC.Header = ""
	if err := cfg.Validate(); err != nil {
		t.Errorf("plain HMAC should pass without secret/header: %v", err)
	}
}

func TestValidate_PortOutOfRange(t *testing.T) {
	cfg := validConfig()
	cfg.Server.Port = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for port 0")
	}
	if !strings.Contains(err.Error(), "port") {
		t.Errorf("error should mention port: %v", err)
	}

	cfg.Server.Port = 70000
	err = cfg.Validate()
	if err == nil {
		t.Fatal("expected error for port 70000")
	}
}

func TestValidate_NoHooks(t *testing.T) {
	cfg := validConfig()
	cfg.Hooks = nil
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for no hooks")
	}
	if !strings.Contains(err.Error(), "at least one hook") {
		t.Errorf("error should mention 'at least one hook': %v", err)
	}
}

func TestValidate_InvalidLogLevel(t *testing.T) {
	cfg := validConfig()
	cfg.Logging.Level = "verbose"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid log level")
	}
	if !strings.Contains(err.Error(), "logging.level") {
		t.Errorf("error should mention logging.level: %v", err)
	}
}

func TestValidate_InvalidLogFormat(t *testing.T) {
	cfg := validConfig()
	cfg.Logging.Format = "xml"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid log format")
	}
	if !strings.Contains(err.Error(), "logging.format") {
		t.Errorf("error should mention logging.format: %v", err)
	}
}

func TestValidate_MissingRepoURL(t *testing.T) {
	cfg := validConfig()
	cfg.Hooks[0].RepoURL = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing repo_url")
	}
	if !strings.Contains(err.Error(), "repo_url") {
		t.Errorf("error should mention repo_url: %v", err)
	}
}

func TestValidate_MissingRepoDir(t *testing.T) {
	cfg := validConfig()
	cfg.Hooks[0].RepoDir = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing repo_dir")
	}
}

func TestValidate_InvalidContentType(t *testing.T) {
	cfg := validConfig()
	cfg.Hooks[0].ContentType = "xml"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid content_type")
	}
}

func TestValidate_ServiceWithoutName(t *testing.T) {
	cfg := validConfig()
	cfg.Hooks[0].Services = []ServiceConfig{{Name: "", Path: "myservice"}}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for service without name")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("error should mention 'name is required': %v", err)
	}
}

func TestValidate_ServiceWithoutPath(t *testing.T) {
	cfg := validConfig()
	cfg.Hooks[0].Services = []ServiceConfig{{Name: "svc", Path: ""}}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for service without path")
	}
	if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("error should mention 'path is required': %v", err)
	}
}

func TestValidate_InvalidRestartTrigger(t *testing.T) {
	cfg := validConfig()
	cfg.Hooks[0].Services = []ServiceConfig{{Name: "svc", Path: ".", RestartTrigger: "maybe"}}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid restart_trigger")
	}
	if !strings.Contains(err.Error(), "restart_trigger") {
		t.Errorf("error should mention restart_trigger: %v", err)
	}
}

func TestValidate_RateLimitEnabledWithoutRPM(t *testing.T) {
	cfg := validConfig()
	cfg.RateLimit.Enabled = true
	cfg.RateLimit.RequestsPerMinute = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for 0 requests_per_minute")
	}
}

func TestValidate_NegativeBurst(t *testing.T) {
	cfg := validConfig()
	cfg.RateLimit.Burst = -1
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for negative burst")
	}
}

func TestValidate_MultipleErrors(t *testing.T) {
	cfg := validConfig()
	cfg.Server.Port = 0
	cfg.Hooks[0].ID = ""
	cfg.Logging.Level = "invalid"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected multiple errors")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "port") || !strings.Contains(errStr, "id") || !strings.Contains(errStr, "logging.level") {
		t.Errorf("should report all errors, got: %v", err)
	}
}

// ──────────────────────── Defaults tests ─────────────────────────────────

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Server.Port != 9090 {
		t.Errorf("default port: %d", cfg.Server.Port)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("default host: %q", cfg.Server.Host)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("default level: %q", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "json" {
		t.Errorf("default format: %q", cfg.Logging.Format)
	}
	if !cfg.RateLimit.Enabled {
		t.Error("rate limit should be enabled by default")
	}
	if cfg.RateLimit.RequestsPerMinute != 30 {
		t.Errorf("default rpm: %d", cfg.RateLimit.RequestsPerMinute)
	}
}

func TestApplyDefaults_EmptyPort(t *testing.T) {
	cfg := &Config{}
	cfg.applyDefaults()
	if cfg.Server.Port != 9090 {
		t.Errorf("expected default port, got %d", cfg.Server.Port)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("expected default host, got %q", cfg.Server.Host)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("expected default level, got %q", cfg.Logging.Level)
	}
}

// ──────────────────────── Scan tests ─────────────────────────────────────

func TestScanServices_RepoWithCompose(t *testing.T) {
	tmpDir := createComposeDir(t, map[string]bool{
		"app1":   true,
		"app2":   true,
		"noapp":  false,
		".cache": true, // hidden — should be skipped
	})

	services := ScanServices(tmpDir)
	names := serviceNames(services)

	if len(services) != 2 {
		t.Errorf("expected 2 services, got %d: %v", len(services), names)
	}
	if !contains(names, "app1") {
		t.Error("app1 not found")
	}
	if !contains(names, "app2") {
		t.Error("app2 not found")
	}
	if contains(names, "noapp") {
		t.Error("noapp should not be a service")
	}
	if contains(names, ".cache") {
		t.Error(".cache should be skipped (hidden dir)")
	}
}

func TestScanServices_ComposeInRepoRoot(t *testing.T) {
	tmpDir := createComposeDir(t, map[string]bool{
		".": true, // docker-compose.yaml in repo root
	})

	os.MkdirAll(tmpDir, 0o755)
	os.WriteFile(filepath.Join(tmpDir, "docker-compose.yaml"), []byte("services:\n"), 0o644)

	services := ScanServices(tmpDir)
	if len(services) != 1 {
		t.Fatalf("expected 1 service (root), got %d", len(services))
	}
	if services[0].Path != "." {
		t.Errorf("expected path '.', got %q", services[0].Path)
	}
}

func TestScanServices_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	services := ScanServices(tmpDir)
	if len(services) != 0 {
		t.Errorf("expected 0 services in empty dir, got %d", len(services))
	}
}

func TestScanServices_EmptyPath(t *testing.T) {
	services := ScanServices("")
	if len(services) != 0 {
		t.Errorf("expected 0 services for empty path, got %d", len(services))
	}
}

func TestScanServices_NonexistentDir(t *testing.T) {
	services := ScanServices("/nonexistent/path/12345")
	if len(services) != 0 {
		t.Errorf("expected 0 services for nonexistent dir, got %d", len(services))
	}
}

// ──────── Security: service scan edge cases ─────────────────────────────

func TestScanServices_SymlinkOutsideRepo(t *testing.T) {
	// Symlinks pointing outside the repo should be skipped by os.ReadDir (it
	// returns directory entries, not following symlinks at depth 1).
	// os.ReadDir returns the directory name regardless; the caller (deploy
	// engine) must validate with filepath.EvalSymlinks. This test confirms
	// we don't crash and return the entry name.
	tmpDir := t.TempDir()
	os.Symlink("/etc", filepath.Join(tmpDir, "escape"))
	os.MkdirAll(filepath.Join(tmpDir, "escape"), 0o755) // this actually fails since escape is not a dir...

	// Actually, os.Symlink creates a symlink; os.MkdirAll fails if target
	// exists. Let's test that ScanServices handles the case gracefully.
	services := ScanServices(tmpDir)
	// The symlink name is "escape" — os.ReadDir returns it as DirEntry.
	// Since we check for docker-compose.yaml inside, and /etc likely doesn't
	// have one, it should be skipped.
	if len(services) != 0 {
		for _, s := range services {
			if s.Name == "escape" {
				t.Error("symlinked dir outside repo should not be included")
			}
		}
	}
}

func TestScanServices_AdjacentDockerComposeFile(t *testing.T) {
	// A file named docker-compose.yaml directly in repoDir's subdir.
	// Should be detected. Also test a file that's empty.
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "myservice")
	os.MkdirAll(subDir, 0o755)
	os.WriteFile(filepath.Join(subDir, "docker-compose.yaml"), []byte(""), 0o644)

	services := ScanServices(tmpDir)
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].Path != "myservice" {
		t.Errorf("expected path 'myservice', got %q", services[0].Path)
	}
}

// ──────────────────────── Duration tests ─────────────────────────────────

func TestDuration_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"30s", 30 * time.Second, false},
		{"5m", 5 * time.Minute, false},
		{"1h", 1 * time.Hour, false},
		{"300s", 300 * time.Second, false},
		{"0s", 0, false},
		{"invalid", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		var d Duration
		node := &yaml.Node{Kind: yaml.ScalarNode, Value: tt.input}
		err := d.UnmarshalYAML(node)
		if tt.wantErr && err == nil {
			t.Errorf("input %q: expected error", tt.input)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("input %q: unexpected error: %v", tt.input, err)
		}
		if !tt.wantErr && d.Duration() != tt.expected {
			t.Errorf("input %q: expected %v, got %v", tt.input, tt.expected, d.Duration())
		}
	}
}

func TestDuration_String(t *testing.T) {
	d := Duration(30 * time.Second)
	if d.String() != "30s" {
		t.Errorf("expected '30s', got %q", d.String())
	}
}

// ──────────────────────── Helpers ────────────────────────────────────────

// validConfig returns a configuration that passes validation.
func validConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:         9090,
			Host:         "0.0.0.0",
			ReadTimeout:  Duration(30 * time.Second),
			WriteTimeout: Duration(300 * time.Second),
		},
		Hooks: []HookConfig{
			{
				ID:      "test-hook",
				RepoURL: "git@github.com:user/repo.git",
				RepoDir: "/tmp/repo",
				Branch:  "main",
				HMAC: HMACConfig{
					Type:   "sha256",
					Secret: "secret123",
					Header: "X-Hub-Signature-256",
				},
				ContentType: "json",
			},
		},
		Notifications: NotifyConfig{
			AppriseURL: "http://apprise:8000",
			TagSuccess: "ok",
		},
		RateLimit: RateLimitConfig{
			Enabled:           true,
			RequestsPerMinute: 30,
			Burst:             5,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// setupTempConfig creates a temp directory with a push-observer.yaml file.
func setupTempConfig(t *testing.T, content string) string {
	t.Helper()
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "push-observer.yaml")
	if err := os.WriteFile(cfgPath, []byte(content), 0o640); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return tmpDir
}

// createComposeDir creates a temp directory structure. The map keys are
// subdirectory names; values are whether the subdir should contain
// docker-compose.yaml. Key "." means the root directly.
func createComposeDir(t *testing.T, dirs map[string]bool) string {
	t.Helper()
	tmpDir := t.TempDir()
	for dirName, hasCompose := range dirs {
		var path string
		if dirName == "." {
			path = tmpDir
		} else {
			path = filepath.Join(tmpDir, dirName)
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("creating dir %q: %v", path, err)
		}
		if hasCompose {
			content := fmt.Sprintf("# docker-compose.yaml for %s\nservices:\n  %s:\n    image: alpine\n", dirName, dirName)
			if err := os.WriteFile(filepath.Join(path, "docker-compose.yaml"), []byte(content), 0o644); err != nil {
				t.Fatalf("writing compose: %v", err)
			}
		}
	}
	return tmpDir
}

func serviceNames(services []ServiceConfig) []string {
	names := make([]string, len(services))
	for i, s := range services {
		names[i] = s.Name
	}
	return names
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
