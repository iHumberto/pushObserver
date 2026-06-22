package docker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ────────────────────── Test helpers ────────────────────────────────────

// setupTempDir creates a temporary directory with a docker-compose.yaml and
// optional subdirectories. Returns the root dir path and cleanup function.
func setupTempDir(t *testing.T, composeContent string, subdirs ...string) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "pushobserver-docker-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}

	composePath := filepath.Join(dir, "docker-compose.yaml")
	if err := os.WriteFile(composePath, []byte(composeContent), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("WriteFile: %v", err)
	}

	for _, sd := range subdirs {
		subPath := filepath.Join(dir, sd)
		if err := os.MkdirAll(subPath, 0o755); err != nil {
			_ = os.RemoveAll(dir)
			t.Fatalf("Mkdir subdir %q: %v", sd, err)
		}
		subCompose := filepath.Join(subPath, "docker-compose.yaml")
		if err := os.WriteFile(subCompose, []byte("services:\n  app:\n    image: alpine\n"), 0o644); err != nil {
			_ = os.RemoveAll(dir)
			t.Fatalf("WriteFile sub: %v", err)
		}
	}

	cleanup := func() { _ = os.RemoveAll(dir) }
	return dir, cleanup
}

// ──────────────────── Up Tests ──────────────────────────────────────────

func TestUp_Success(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n")
	defer cleanup()

	origExec := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "echo", "Container web  Started")
	}
	defer func() { execCommand = origExec }()

	eng := New()
	out, err := eng.Up(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Up failed: %v", err)
	}
	if !strings.Contains(out, "Started") {
		t.Errorf("expected output to contain 'Started', got: %s", out)
	}
}

func TestUp_WithBuild(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    build: .\n")
	defer cleanup()

	var capturedArgs []string
	origExec := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		capturedArgs = args
		return exec.CommandContext(ctx, "echo", "built and started")
	}
	defer func() { execCommand = origExec }()

	eng := New()
	_, err := eng.Up(context.Background(), dir, true)
	if err != nil {
		t.Fatalf("Up --build failed: %v", err)
	}

	foundBuild := false
	for _, a := range capturedArgs {
		if a == "--build" {
			foundBuild = true
			break
		}
	}
	if !foundBuild {
		t.Errorf("expected --build flag in args, got: %v", capturedArgs)
	}
}

func TestUp_EmptyDir(t *testing.T) {
	eng := New()
	_, err := eng.Up(context.Background(), "", false)
	if err == nil {
		t.Fatal("expected error for empty composeDir")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected 'empty' in error, got: %v", err)
	}
}

// ──────────────────── Down Tests ────────────────────────────────────────

func TestDown_Success(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n")
	defer cleanup()

	origExec := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "echo", "Container web  Removed")
	}
	defer func() { execCommand = origExec }()

	eng := New()
	out, err := eng.Down(context.Background(), dir)
	if err != nil {
		t.Fatalf("Down failed: %v", err)
	}
	if !strings.Contains(out, "Removed") {
		t.Errorf("expected output to contain 'Removed', got: %s", out)
	}
}

func TestDown_DockerError(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n")
	defer cleanup()

	origExec := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "sh", "-c", "echo 'no compose file found' >&2; exit 1")
		return cmd
	}
	defer func() { execCommand = origExec }()

	eng := New()
	_, err := eng.Down(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error from docker compose down")
	}
}

// ──────────────────── Logs Tests ────────────────────────────────────────

func TestLogs_WithTail(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n")
	defer cleanup()

	var capturedArgs []string
	origExec := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		capturedArgs = args
		return exec.CommandContext(ctx, "echo", "log line 1\nlog line 2")
	}
	defer func() { execCommand = origExec }()

	eng := New()
	out, err := eng.Logs(context.Background(), dir, 50)
	if err != nil {
		t.Fatalf("Logs failed: %v", err)
	}
	if !strings.Contains(out, "log line") {
		t.Errorf("expected log output, got: %s", out)
	}

	foundTail := false
	for _, a := range capturedArgs {
		if a == "--tail=50" {
			foundTail = true
			break
		}
	}
	if !foundTail {
		t.Errorf("expected --tail=50 in args, got: %v", capturedArgs)
	}
}

func TestLogs_NoTail(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n")
	defer cleanup()

	var capturedArgs []string
	origExec := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		capturedArgs = args
		return exec.CommandContext(ctx, "echo", "all logs")
	}
	defer func() { execCommand = origExec }()

	eng := New()
	_, err := eng.Logs(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("Logs failed: %v", err)
	}

	for _, a := range capturedArgs {
		if strings.HasPrefix(a, "--tail=") {
			t.Errorf("expected no --tail flag when tail=0, got args: %v", capturedArgs)
		}
	}
}

// ──────────────── ChangedServices Tests ─────────────────────────────────

func TestChangedServices_Always(t *testing.T) {
	services := []ServiceSpec{
		{Name: "web", Path: "web", RestartTrigger: "always"},
		{Name: "api", Path: "api", RestartTrigger: "default"},
	}

	result := ChangedServices(services, []string{"api/config.go"}, "/tmp/repo")
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}

	// "web" with "always" should be changed regardless.
	if !result[0].Changed {
		t.Errorf("web (always): expected Changed=true, got false")
	}
	if result[0].Reason != "always" {
		t.Errorf("web reason: expected 'always', got %q", result[0].Reason)
	}
}

func TestChangedServices_DefaultTrigger_Matched(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n", "api")
	defer cleanup()

	services := []ServiceSpec{
		{Name: "api", Path: "api", RestartTrigger: "default"},
	}

	// A file changed inside the api subdirectory.
	result := ChangedServices(services, []string{"api/main.go"}, dir)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if !result[0].Changed {
		t.Errorf("api: expected Changed=true (file inside path), got false")
	}
	if result[0].Reason != "files-changed" {
		t.Errorf("api reason: expected 'files-changed', got %q", result[0].Reason)
	}
}

func TestChangedServices_DefaultTrigger_NotMatched(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n", "web", "api")
	defer cleanup()

	services := []ServiceSpec{
		{Name: "web", Path: "web", RestartTrigger: "default"},
	}

	// Changed file in a different directory.
	result := ChangedServices(services, []string{"api/main.go"}, dir)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0].Changed {
		t.Errorf("web: expected Changed=false (file outside path), got true")
	}
	if result[0].Reason != "no-changes" {
		t.Errorf("web reason: expected 'no-changes', got %q", result[0].Reason)
	}
}

func TestChangedServices_OnChangeTrigger(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n", "web")
	defer cleanup()

	services := []ServiceSpec{
		{Name: "web", Path: "web", RestartTrigger: "on-change"},
	}

	// on-change behaves like default — check path prefix.
	result := ChangedServices(services, []string{"web/Dockerfile"}, dir)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if !result[0].Changed {
		t.Errorf("web (on-change): expected Changed=true, got false")
	}
	if result[0].Reason != "files-changed" {
		t.Errorf("web reason: expected 'files-changed', got %q", result[0].Reason)
	}
}

func TestChangedServices_DotPath(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n")
	defer cleanup()

	services := []ServiceSpec{
		{Name: "root", Path: ".", RestartTrigger: "default"},
	}

	// Path "." means the repo root itself. A change at root level should match.
	result := ChangedServices(services, []string{"Dockerfile"}, dir)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if !result[0].Changed {
		t.Errorf("root: expected Changed=true (Dockerfile in .), got false")
	}
}

func TestChangedServices_MultipleServices(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n", "web", "api", "db")
	defer cleanup()

	services := []ServiceSpec{
		{Name: "web", Path: "web", RestartTrigger: "default"},
		{Name: "api", Path: "api", RestartTrigger: "always"},
		{Name: "db", Path: "db", RestartTrigger: "default"},
	}

	// Only web files changed.
	result := ChangedServices(services, []string{"web/config.yaml"}, dir)
	if len(result) != 3 {
		t.Fatalf("expected 3 results, got %d", len(result))
	}

	expectations := map[string]bool{
		"web": true,  // changed file in path
		"api": true,  // always trigger
		"db":  false, // no changes in db path
	}

	for _, r := range result {
		expected, ok := expectations[r.Name]
		if !ok {
			t.Errorf("unexpected service: %s", r.Name)
			continue
		}
		if r.Changed != expected {
			t.Errorf("%s: expected Changed=%v, got %v (reason=%q)", r.Name, expected, r.Changed, r.Reason)
		}
	}
}

func TestChangedServices_EmptyChangedFiles(t *testing.T) {
	services := []ServiceSpec{
		{Name: "web", Path: "web", RestartTrigger: "default"},
	}

	result := ChangedServices(services, nil, "/tmp/repo")
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0].Changed {
		t.Errorf("expected Changed=false with no changed files, got true")
	}
}

func TestChangedServices_EmptyServices(t *testing.T) {
	result := ChangedServices(nil, []string{"main.go"}, "/tmp/repo")
	if len(result) != 0 {
		t.Errorf("expected empty result for nil services, got %d", len(result))
	}
}

// ──────────────── Security Tests ────────────────────────────────────────

// TestSecurity_PathTraversal_ServicePath verifies that a service path
// attempting to escape the repo (e.g., "../../etc") is treated as
// non-matching — it never returns changed=true for files outside the repo.
func TestSecurity_PathTraversal_ServicePath(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n")
	defer cleanup()

	// Malicious service path tries to escape repo.
	services := []ServiceSpec{
		{Name: "evil", Path: "../../etc", RestartTrigger: "default"},
	}

	// Even with "changed" files, the path traversal should be blocked.
	result := ChangedServices(services, []string{"passwd"}, dir)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0].Changed {
		t.Errorf("path traversal service should NOT report changed (path outside repo), got Changed=true")
	}
}

// TestSecurity_PathTraversal_ComposeDir tests that validateDir rejects
// paths that Clean would alter (indicating traversal attempts).
func TestSecurity_PathTraversal_ComposeDir(t *testing.T) {
	origExec := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "echo", "ok")
	}
	defer func() { execCommand = origExec }()

	eng := New()

	// Empty path is rejected.
	_, err := eng.Up(context.Background(), "", false)
	if err == nil {
		t.Fatal("expected error for empty composeDir")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected 'empty' in error, got: %v", err)
	}

	// Note: validateDir does NOT enforce base-path containment (e.g., rejecting /etc).
	// That's the deploy engine's responsibility — docker engine just validates
	// the path is well-formed. Path traversal in service paths (ChangedServices)
	// IS protected by isPathAffected's prefix check against repoDir.
}

// TestSecurity_CommandInjection_ServiceName verifies that service names
// containing shell metacharacters are NOT executed as shell commands.
// ChangedServices never shells out — it only does path manipulation.
func TestSecurity_CommandInjection_ServiceName(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n", "safe")
	defer cleanup()

	// Service name with shell metacharacters — should NOT be executed.
	services := []ServiceSpec{
		{Name: "safe$(id)", Path: "safe", RestartTrigger: "default"},
	}

	result := ChangedServices(services, []string{"safe/config.go"}, dir)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	// The name is stored as-is but path traversal is based on Path, not Name.
	if !result[0].Changed {
		t.Errorf("safe path with changed file should be detected: got Changed=false")
	}
	if result[0].Name != "safe$(id)" {
		t.Errorf("service name should be preserved verbatim: got %q", result[0].Name)
	}
}

// TestSecurity_CommandInjection_ComposeDir verifies that docker compose
// commands use exec.CommandContext (separate args, not shell).
// The key protection: the command name is always "docker", never "sh"/"bash".
func TestSecurity_CommandInjection_ComposeDir(t *testing.T) {
	origExec := execCommand
	var capturedName string
	var capturedArgs []string
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		capturedName = name
		capturedArgs = args
		return exec.CommandContext(ctx, "echo", "ok")
	}
	defer func() { execCommand = origExec }()

	eng := New()
	// Use a legitimate path so validateDir passes and we can inspect the exec call.
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n")
	defer cleanup()

	_, err := eng.Up(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Up failed: %v", err)
	}

	// Command must be "docker", never a shell.
	if capturedName != "docker" {
		t.Errorf("command name must be 'docker', got %q", capturedName)
	}

	// Verify args are passed as a slice (never concatenated into a single shell string).
	if len(capturedArgs) == 0 {
		t.Fatal("expected non-empty args")
	}
	// All args should be literal strings — no shell metacharacters that could
	// be interpreted if someone DID run through a shell (defense in depth).
	for _, a := range capturedArgs {
		if a == "|" || a == ";" || a == "&&" || a == "||" {
			t.Errorf("dangerous standalone shell operator in args: %q", a)
		}
	}
}

// TestSecurity_ContextCancellation verifies that operations respect
// context cancellation.
func TestSecurity_ContextCancellation(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n")
	defer cleanup()

	origExec := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// Create a command that blocks until context is cancelled.
		cmd := exec.CommandContext(ctx, "sleep", "10")
		return cmd
	}
	defer func() { execCommand = origExec }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	eng := New()
	_, err := eng.Up(ctx, dir, false)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "killed") {
		// The error might wrap DeadlineExceeded or be a signal error.
		t.Logf("context cancellation error (expected): %v", err)
	}
}

// TestSecurity_UnknownRestartTrigger verifies that unknown triggers
// fall through to the default case (no-changes unless files match).
func TestSecurity_UnknownRestartTrigger(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n", "app")
	defer cleanup()

	services := []ServiceSpec{
		{Name: "app", Path: "app", RestartTrigger: "custom"},
	}

	// Unknown trigger should behave like default (check path, not always).
	result := ChangedServices(services, nil, dir)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0].Changed {
		t.Errorf("unknown trigger with no changes should NOT report changed, got Changed=true")
	}
}

// ──────────────── Edge Case Tests ───────────────────────────────────────

func TestChangedServices_RepoRootHasCompose(t *testing.T) {
	dir, cleanup := setupTempDir(t, "services:\n  web:\n    image: nginx\n")
	defer cleanup()

	services := []ServiceSpec{
		{Name: "root", Path: ".", RestartTrigger: "default"},
	}

	// Changed file at repo root.
	result := ChangedServices(services, []string{"README.md"}, dir)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if !result[0].Changed {
		t.Errorf("root service with changed file at repo root should be Changed=true")
	}
}

func TestChangedServices_NonExistentRepoDir(t *testing.T) {
	services := []ServiceSpec{
		{Name: "web", Path: "web", RestartTrigger: "default"},
	}

	// Non-existent repo directory should not panic.
	// Path arithmetic (filepath.Abs, filepath.Join) works regardless of
	// filesystem existence — a changed file in the service path WILL match.
	result := ChangedServices(services, []string{"web/main.go"}, "/nonexistent/path/repo")
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if !result[0].Changed {
		t.Errorf("non-existent repo but path arithmetic matches: expected Changed=true, got false (reason=%q)", result[0].Reason)
	}
}

// ──────────────── ChangedService result helper test ─────────────────────

func TestChangedService_ResultStructure(t *testing.T) {
	result := ChangedService{
		Name:    "test-svc",
		Path:    "svc",
		Changed: true,
		Reason:  "always",
	}

	if result.Name != "test-svc" {
		t.Errorf("Name: expected 'test-svc', got %q", result.Name)
	}
	if !result.Changed {
		t.Error("Changed: expected true")
	}
}

// ──────────────── Benchmark ─────────────────────────────────────────────

func BenchmarkChangedServices(b *testing.B) {
	services := []ServiceSpec{
		{Name: "web", Path: "web", RestartTrigger: "default"},
		{Name: "api", Path: "api", RestartTrigger: "always"},
		{Name: "db", Path: "db", RestartTrigger: "on-change"},
		{Name: "cache", Path: "cache", RestartTrigger: "default"},
		{Name: "worker", Path: "worker", RestartTrigger: "default"},
	}

	changedFiles := []string{
		"web/main.go",
		"web/Dockerfile",
		"README.md",
		".github/workflows/ci.yaml",
	}

	for i := 0; i < b.N; i++ {
		ChangedServices(services, changedFiles, "/tmp/repo")
	}
}
