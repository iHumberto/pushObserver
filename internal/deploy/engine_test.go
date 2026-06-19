package deploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forgejo.humbertof.dev/humberto/push-observer/internal/config"
)

// ───────────────────── ShouldRestart tests ───────────────────────────────

func TestShouldRestart_Always(t *testing.T) {
	svc := config.ServiceConfig{
		Name:           "api",
		Path:           "api",
		RestartTrigger: "always",
	}

	if !ShouldRestart(svc, nil, nil, "/tmp/repo") {
		t.Error("always trigger should restart regardless of changed files")
	}
}

func TestShouldRestart_Default_NoFiles(t *testing.T) {
	svc := config.ServiceConfig{
		Name:           "web",
		Path:           "web",
		RestartTrigger: "default",
	}

	if ShouldRestart(svc, nil, nil, "/tmp/repo") {
		t.Error("default trigger with no changed files should not restart")
	}
}

func TestShouldRestart_Default_TriggerFileEnv(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "web",
		Path:           "web",
		RestartTrigger: "default",
	}
	createFile(t, repoDir, "web/.env", "DEBUG=true")

	if !ShouldRestart(svc, []string{"web/.env"}, nil, repoDir) {
		t.Error("default trigger: .env changed should restart")
	}
}

func TestShouldRestart_Default_TriggerFileDockerfile(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "web",
		Path:           "web",
		RestartTrigger: "default",
	}
	createFile(t, repoDir, "web/Dockerfile", "FROM alpine")

	if !ShouldRestart(svc, []string{"web/Dockerfile"}, nil, repoDir) {
		t.Error("default trigger: Dockerfile changed should restart")
	}
}

func TestShouldRestart_Default_TriggerFileComposeYAML(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "web",
		Path:           "web",
		RestartTrigger: "default",
	}
	createFile(t, repoDir, "web/docker-compose.yaml", "services:")

	if !ShouldRestart(svc, []string{"web/docker-compose.yaml"}, nil, repoDir) {
		t.Error("default trigger: docker-compose.yaml changed should restart")
	}
}

func TestShouldRestart_Default_TriggerFileComposeYML(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "web",
		Path:           "web",
		RestartTrigger: "default",
	}
	createFile(t, repoDir, "web/docker-compose.yml", "services:")

	if !ShouldRestart(svc, []string{"web/docker-compose.yml"}, nil, repoDir) {
		t.Error("default trigger: docker-compose.yml changed should restart")
	}
}

func TestShouldRestart_Default_NonTriggerFile(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "web",
		Path:           "web",
		RestartTrigger: "default",
	}
	createFile(t, repoDir, "web/main.go", "package main")

	if ShouldRestart(svc, []string{"web/main.go"}, nil, repoDir) {
		t.Error("default trigger: non-trigger file changed should NOT restart")
	}
}

func TestShouldRestart_Default_FileOutsideServicePath(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "web",
		Path:           "web",
		RestartTrigger: "default",
	}
	createFile(t, repoDir, "api/.env", "DEBUG=true")

	if ShouldRestart(svc, []string{"api/.env"}, nil, repoDir) {
		t.Error("default trigger: trigger file outside service path should not restart")
	}
}

func TestShouldRestart_OnChange_CustomExtensionMatch(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "api",
		Path:           "api",
		RestartTrigger: "on-change",
	}
	createFile(t, repoDir, "api/handler.py", "def handle(): pass")

	customExt := []string{".py", ".yaml"}

	if !ShouldRestart(svc, []string{"api/handler.py"}, customExt, repoDir) {
		t.Error("on-change trigger: .py file with .py custom extension should restart")
	}
}

func TestShouldRestart_OnChange_CustomExtensionNoMatch(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "api",
		Path:           "api",
		RestartTrigger: "on-change",
	}
	createFile(t, repoDir, "api/main.go", "package main")

	customExt := []string{".py", ".yaml"}

	if ShouldRestart(svc, []string{"api/main.go"}, customExt, repoDir) {
		t.Error("on-change trigger: .go file with .py extensions should NOT restart")
	}
}

func TestShouldRestart_OnChange_TriggerFileStillWorks(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "api",
		Path:           "api",
		RestartTrigger: "on-change",
	}
	createFile(t, repoDir, "api/Dockerfile", "FROM alpine")

	// Default trigger files (.env, Dockerfile, compose files) should always work
	// even with on-change trigger and no matching custom extensions.
	if !ShouldRestart(svc, []string{"api/Dockerfile"}, nil, repoDir) {
		t.Error("on-change trigger: Dockerfile should always trigger restart")
	}
}

func TestShouldRestart_OnChange_EnvStillWorks(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "api",
		Path:           "api",
		RestartTrigger: "on-change",
	}
	createFile(t, repoDir, "api/.env", "KEY=value")

	if !ShouldRestart(svc, []string{"api/.env"}, []string{}, repoDir) {
		t.Error("on-change trigger: .env should always trigger restart")
	}
}

func TestShouldRestart_UnknownTrigger_BehavesLikeDefault(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "svc",
		Path:           "svc",
		RestartTrigger: "unknown-custom",
	}
	createFile(t, repoDir, "svc/.env", "KEY=val")

	// Unknown triggers fall through to default behavior.
	if !ShouldRestart(svc, []string{"svc/.env"}, nil, repoDir) {
		t.Error("unknown trigger with .env change: should behave like default and restart")
	}

	// Non-trigger file should NOT restart.
	if ShouldRestart(svc, []string{"svc/random.txt"}, nil, repoDir) {
		t.Error("unknown trigger with non-trigger file: should not restart")
	}
}

func TestShouldRestart_DotPath(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "root",
		Path:           ".",
		RestartTrigger: "default",
	}
	createFile(t, repoDir, ".env", "KEY=val")

	if !ShouldRestart(svc, []string{".env"}, nil, repoDir) {
		t.Error("dot path: .env at repo root should trigger restart")
	}
}

func TestShouldRestart_EmptyExtensions(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "api",
		Path:           "api",
		RestartTrigger: "on-change",
	}
	createFile(t, repoDir, "api/main.py", "print('hi')")

	// Empty custom extensions → only default triggers apply.
	if ShouldRestart(svc, []string{"api/main.py"}, []string{}, repoDir) {
		t.Error("on-change with empty custom extensions: non-trigger file should NOT restart")
	}
}

func TestShouldRestart_DeeplyNestedFile(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "api",
		Path:           "api",
		RestartTrigger: "on-change",
	}
	createFile(t, repoDir, "api/deep/nested/file.py", "x=1")

	if !ShouldRestart(svc, []string{"api/deep/nested/file.py"}, []string{".py"}, repoDir) {
		t.Error("on-change: deeply nested .py file should trigger restart")
	}
}

// ───────────────────── Engine tests ──────────────────────────────────────

func TestNewEngine(t *testing.T) {
	hooks := []config.HookConfig{
		{ID: "test-hook", RepoDir: "/tmp/test", Branch: "main", RepoURL: "git@example.com:repo.git"},
	}

	engine := New(hooks, 5*time.Minute)

	if engine == nil {
		t.Fatal("New() returned nil")
	}
	if len(engine.hooks) != 1 {
		t.Errorf("expected 1 hook, got %d", len(engine.hooks))
	}
	var hook config.HookConfig
	for _, h := range engine.hooks {
		hook = h
	}
	if hook.ID != "test-hook" {
		t.Errorf("hook ID = %q, want \"test-hook\"", hook.ID)
	}
}

func TestEngine_AcquireReleaseLock(t *testing.T) {
	engine := New(nil, 30*time.Second)

	// Acquire lock
	mu := engine.acquireLock("hook-1")
	if mu == nil {
		t.Fatal("acquireLock should succeed for free lock")
	}

	// Second acquire on same hook should fail
	mu2 := engine.acquireLock("hook-1")
	if mu2 != nil {
		mu2.Unlock()
		t.Error("second acquireLock on same hook should fail while lock is held")
	}

	// Release
	mu.Unlock()

	// Now second acquire should succeed
	mu3 := engine.acquireLock("hook-1")
	if mu3 == nil {
		t.Error("acquireLock should succeed after unlock")
	}
	if mu3 != nil {
		mu3.Unlock()
	}
}

func TestEngine_AcquireLockDifferentHooks(t *testing.T) {
	engine := New(nil, 30*time.Second)

	muA := engine.acquireLock("hook-a")
	if muA == nil {
		t.Fatal("acquireLock hook-a failed")
	}

	muB := engine.acquireLock("hook-b")
	if muB == nil {
		t.Fatal("acquireLock hook-b failed — different hooks should not block each other")
	}

	muA.Unlock()
	muB.Unlock()
}

// ───────────────────── Deploy integration tests ──────────────────────────

// mockExecHelper is unused but kept as documentation for future mock setup.
var _ = mockExecHelper

func mockExecHelper() {}

// createTempGitRepo creates a real git repository with a compose file for testing.
func createTempGitRepo(t *testing.T) (repoDir, remoteDir string) {
	t.Helper()

	// Create "remote" bare repo
	remoteDir = t.TempDir()
	runGitCmd(t, remoteDir, "init", "--bare")

	// Create working repo
	repoDir = t.TempDir()
	runGitCmd(t, repoDir, "init", "-b", "main")
	runGitCmd(t, repoDir, "config", "user.email", "test@test.local")
	runGitCmd(t, repoDir, "config", "user.name", "Test")

	// Create a service directory with docker-compose.yaml
	svcDir := filepath.Join(repoDir, "web")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatalf("mkdir web: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(svcDir, "docker-compose.yaml"),
		[]byte("services:\n  web:\n    image: nginx\n"),
		0o644,
	); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	// Commit
	runGitCmd(t, repoDir, "add", ".")
	runGitCmd(t, repoDir, "commit", "-m", "initial")

	// Push to remote
	runGitCmd(t, repoDir, "remote", "add", "origin", remoteDir)
	runGitCmd(t, repoDir, "push", "origin", "main")

	return repoDir, remoteDir
}

func runGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func TestDeploy_FirstDeploy_AlwaysServices(t *testing.T) {
	repoDir, remoteDir := createTempGitRepo(t)
	_ = remoteDir
	defer os.RemoveAll(repoDir)

	// Create target dir for clone
	targetDir := t.TempDir()

	hooks := []config.HookConfig{
		{
			ID:      "test-hook",
			RepoURL: remoteDir,
			RepoDir: targetDir,
			Branch:  "main",
			Services: []config.ServiceConfig{
				{Name: "web", Path: "web", RestartTrigger: "always"},
				{Name: "db", Path: "db", RestartTrigger: "default"},
			},
			Compose: config.ComposeConfig{Build: false},
		},
	}

	engine := New(hooks, 30*time.Second)

	result, err := engine.Deploy(context.Background(), "test-hook")
	if err != nil {
		t.Fatalf("Deploy() error: %v", err)
	}

	if result.HookID != "test-hook" {
		t.Errorf("result.HookID = %q, want \"test-hook\"", result.HookID)
	}
	if result.CommitBefore != "" {
		t.Errorf("first deploy CommitBefore should be empty, got %q", result.CommitBefore)
	}
	if result.CommitAfter == "" {
		t.Error("CommitAfter should not be empty after clone")
	}
	// "always" service should be in results
	foundWeb := false
	for _, svc := range result.Services {
		if svc.Name == "web" {
			foundWeb = true
			if !svc.Changed {
				t.Error("'always' service should be marked as changed")
			}
		}
	}
	if !foundWeb {
		t.Error("web service not found in deploy result")
	}
	if result.Duration <= 0 {
		t.Error("duration should be > 0")
	}
}

func TestDeploy_NoChanges(t *testing.T) {
	repoDir, remoteDir := createTempGitRepo(t)
	_ = repoDir

	targetDir := filepath.Join(t.TempDir(), "nochange-target")

	hooks := []config.HookConfig{
		{
			ID:      "nochange-hook",
			RepoURL: remoteDir,
			RepoDir: targetDir,
			Branch:  "main",
			Services: []config.ServiceConfig{
				{Name: "web", Path: "web", RestartTrigger: "default"},
			},
		},
	}

	engine := New(hooks, 30*time.Second)

	// First deploy to clone
	_, err := engine.Deploy(context.Background(), "nochange-hook")
	if err != nil {
		t.Fatalf("first Deploy() error: %v", err)
	}

	// Second deploy — no new commits, should detect no changes
	result, err := engine.Deploy(context.Background(), "nochange-hook")
	if err != nil {
		t.Fatalf("second Deploy() error: %v", err)
	}

	if result.CommitBefore != result.CommitAfter {
		t.Error("no new commits: CommitBefore should equal CommitAfter")
	}
	for _, svc := range result.Services {
		if svc.Changed {
			t.Errorf("service %s should not be marked changed with no new commits", svc.Name)
		}
	}
}

func TestDeploy_MultipleServices_RestartOnlyAffected(t *testing.T) {
	repoDir, remoteDir := createTempGitRepo(t)

	targetDir := filepath.Join(t.TempDir(), "multi-target")

	hooks := []config.HookConfig{
		{
			ID:      "multi-hook",
			RepoURL: remoteDir,
			RepoDir: targetDir,
			Branch:  "main",
			Services: []config.ServiceConfig{
				{Name: "web", Path: "web", RestartTrigger: "default"},
				{Name: "api", Path: "api", RestartTrigger: "default"},
			},
		},
	}

	engine := New(hooks, 30*time.Second)

	// First deploy
	_, err := engine.Deploy(context.Background(), "multi-hook")
	if err != nil {
		t.Fatalf("first Deploy() error: %v", err)
	}

	// Add a new service directory in the SOURCE repo, push to remote,
	// so the deploy's Pull will fetch the changes.
	apiDir := filepath.Join(repoDir, "api")
	if err := os.MkdirAll(apiDir, 0o755); err != nil {
		t.Fatalf("mkdir api: %v", err)
	}
	if err := os.WriteFile(filepath.Join(apiDir, ".env"), []byte("KEY=val"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	runGitCmd(t, repoDir, "add", "api/.env")
	runGitCmd(t, repoDir, "commit", "-m", "add api .env")

	// Push to remote so the pull during deploy fetches it
	runGitCmd(t, repoDir, "push", "origin", "main")

	// Second deploy — api/.env changed, web unchanged
	result, err := engine.Deploy(context.Background(), "multi-hook")
	if err != nil {
		t.Fatalf("second Deploy() error: %v", err)
	}

	for _, svc := range result.Services {
		switch svc.Name {
		case "api":
			if !svc.Changed {
				t.Error("api service with .env change should be marked changed")
			}
		case "web":
			if svc.Changed {
				t.Error("web service with no changes should not be marked changed")
			}
		}
	}
}

func TestDeploy_InvalidHookID(t *testing.T) {
	engine := New(nil, 30*time.Second)

	_, err := engine.Deploy(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("Deploy() with nonexistent hook ID should error")
	}
	if !strings.Contains(err.Error(), "hook") {
		t.Errorf("error should mention hook: %v", err)
	}
}

// ───────────────────── Security tests ────────────────────────────────────

func TestSecurity_CommandInjection_HookID(t *testing.T) {
	engine := New(nil, 30*time.Second)

	// Hook ID with shell metacharacters should not be executed
	_, err := engine.Deploy(context.Background(), "test; rm -rf /")
	if err == nil {
		t.Fatal("Deploy() with shell metacharacters in hook ID should error")
	}
}

func TestSecurity_PathTraversal_ServicePath(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "evil",
		Path:           "../../etc",
		RestartTrigger: "default",
	}

	// Even if a trigger file "exists" at the resolved path outside the repo,
	// ShouldRestart should NOT match because the path escapes the repo.
	if ShouldRestart(svc, []string{"passwd"}, nil, repoDir) {
		t.Error("path traversal service path should NOT trigger restart for files outside repo")
	}
}

func TestSecurity_ShouldRestart_NilExtensions(t *testing.T) {
	repoDir := setupRepoDir(t)
	defer os.RemoveAll(repoDir)

	svc := config.ServiceConfig{
		Name:           "api",
		Path:           "api",
		RestartTrigger: "on-change",
	}
	createFile(t, repoDir, "api/Dockerfile", "FROM alpine")

	// nil custom extensions should not panic, Dockerfile should still trigger.
	if !ShouldRestart(svc, []string{"api/Dockerfile"}, nil, repoDir) {
		t.Error("nil custom extensions: Dockerfile should still trigger restart")
	}
}

func TestSecurity_Deploy_ContextCancellation(t *testing.T) {
	_, remoteDir := createTempGitRepo(t)

	targetDir := filepath.Join(t.TempDir(), "ctx-cancel-target")

	hooks := []config.HookConfig{
		{
			ID:      "ctx-hook",
			RepoURL: remoteDir,
			RepoDir: targetDir,
			Branch:  "main",
			Services: []config.ServiceConfig{
				{Name: "web", Path: "web", RestartTrigger: "always"},
			},
		},
	}

	engine := New(hooks, 30*time.Second)

	// First deploy to clone
	if _, err := engine.Deploy(context.Background(), "ctx-hook"); err != nil {
		t.Fatalf("first Deploy() error: %v", err)
	}

	// Cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := engine.Deploy(ctx, "ctx-hook")
	if err == nil {
		t.Fatal("Deploy() with cancelled context should error")
	}
}

// ───────────────────── Test helpers ──────────────────────────────────────

// setupRepoDir creates a temporary directory structure for ShouldRestart tests.
func setupRepoDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// createFile creates a file at the given path relative to repoDir.
func createFile(t *testing.T, repoDir, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(repoDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(fullPath), err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", relPath, err)
	}
}

// ───────────────────── Benchmarks ────────────────────────────────────────

func BenchmarkShouldRestart(b *testing.B) {
	repoDir := b.TempDir()
	apiDir := filepath.Join(repoDir, "api")
	os.MkdirAll(apiDir, 0o755)
	os.WriteFile(filepath.Join(apiDir, ".env"), []byte("KEY=val"), 0o644)
	os.WriteFile(filepath.Join(apiDir, "main.py"), []byte("print('hi')"), 0o644)

	svc := config.ServiceConfig{
		Name:           "api",
		Path:           "api",
		RestartTrigger: "on-change",
	}
	changedFiles := []string{"api/main.py", "api/.env", "README.md"}
	customExt := []string{".py", ".yaml"}

	for i := 0; i < b.N; i++ {
		ShouldRestart(svc, changedFiles, customExt, repoDir)
	}
}
