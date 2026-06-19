//go:build integration

package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"forgejo.humbertof.dev/humberto/push-observer/internal/config"
)

// ──────────────── Full Deploy Pipeline Integration Tests ─────────────────

// setupIntegrationRepo creates a git repository with a docker-compose.yaml
// for a service, pushes to a bare remote. Returns (repoDir, remoteDir, cleanup).
func setupIntegrationRepo(t *testing.T) (repoDir, remoteDir string, cleanup func()) {
	t.Helper()

	remoteDir = t.TempDir()
	runGit(t, remoteDir, "init", "--bare")

	repoDir = t.TempDir()
	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "config", "user.email", "integration@test.local")
	runGit(t, repoDir, "config", "user.name", "Integration Test")

	// Create a service directory with docker-compose.yaml
	svcDir := filepath.Join(repoDir, "web")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatalf("mkdir web: %v", err)
	}
	composeContent := `services:
  integration-web:
    image: nginx:alpine
    stop_grace_period: 1s
`
	if err := os.WriteFile(
		filepath.Join(svcDir, "docker-compose.yaml"),
		[]byte(composeContent),
		0o644,
	); err != nil {
		t.Fatalf("write compose: %v", err)
	}

	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "initial")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "origin", "main")

	cleanup = func() {
		os.RemoveAll(repoDir)
		os.RemoveAll(remoteDir)
	}
	return repoDir, remoteDir, cleanup
}

// runGit runs a git command in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// ──────────────────── First Deploy (clone + deploy all) ──────────────────

func TestIntegration_Deploy_FirstDeploy(t *testing.T) {
	_, remoteDir, cleanup := setupIntegrationRepo(t)
	defer cleanup()

	targetDir := filepath.Join(t.TempDir(), "first-deploy-target")

	hooks := []config.HookConfig{
		{
			ID:      "integration-first",
			RepoURL: remoteDir,
			RepoDir: targetDir,
			Branch:  "main",
			Services: []config.ServiceConfig{
				{Name: "web", Path: "web", RestartTrigger: "always"},
			},
			Compose: config.ComposeConfig{Build: false},
		},
	}

	engine := New(hooks, 60*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result, err := engine.Deploy(ctx, "integration-first")
	if err != nil {
		t.Fatalf("first Deploy() error: %v", err)
	}

	// Verify result structure
	if result.HookID != "integration-first" {
		t.Errorf("HookID = %q, want \"integration-first\"", result.HookID)
	}
	if result.CommitBefore != "" {
		t.Errorf("first deploy CommitBefore should be empty, got %q", result.CommitBefore)
	}
	if result.CommitAfter == "" {
		t.Error("CommitAfter should not be empty after clone")
	}
	if result.Duration <= 0 {
		t.Error("Duration should be > 0")
	}

	// Verify web service was deployed
	if len(result.Services) != 1 {
		t.Fatalf("expected 1 service result, got %d", len(result.Services))
	}
	svc := result.Services[0]
	if svc.Name != "web" {
		t.Errorf("service name = %q, want \"web\"", svc.Name)
	}
	if !svc.Changed {
		t.Error("first deploy: 'always' service should be marked changed")
	}
	if !svc.Restarted {
		t.Errorf("first deploy: 'always' service should be restarted (error: %v)", svc.Error)
	}

	// Verify the container is actually running via Docker
	containers := listComposeContainers(t, filepath.Join(targetDir, "web"))
	if len(containers) == 0 {
		t.Fatal("expected at least 1 running container after deploy")
	}
	t.Logf("running containers: %v", containers)

	// Cleanup: docker compose down
	cleanupDeploy(t, filepath.Join(targetDir, "web"))
}

// ──────────────────── Second Deploy (no changes) ─────────────────────────

func TestIntegration_Deploy_NoChanges(t *testing.T) {
	_, remoteDir, cleanup := setupIntegrationRepo(t)
	defer cleanup()

	targetDir := filepath.Join(t.TempDir(), "nochange-target")

	hooks := []config.HookConfig{
		{
			ID:      "integration-nochange",
			RepoURL: remoteDir,
			RepoDir: targetDir,
			Branch:  "main",
			Services: []config.ServiceConfig{
				{Name: "web", Path: "web", RestartTrigger: "default"},
			},
		},
	}

	engine := New(hooks, 60*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// First deploy
	result1, err := engine.Deploy(ctx, "integration-nochange")
	if err != nil {
		t.Fatalf("first Deploy() error: %v", err)
	}
	t.Logf("first deploy: CommitAfter=%s Services=%d Duration=%v",
		result1.CommitAfter[:8], len(result1.Services), result1.Duration)

	// Second deploy — no new commits
	result2, err := engine.Deploy(ctx, "integration-nochange")
	if err != nil {
		t.Fatalf("second Deploy() error: %v", err)
	}
	t.Logf("second deploy: CommitBefore=%s CommitAfter=%s",
		result2.CommitBefore[:8], result2.CommitAfter[:8])

	if result2.CommitBefore != result2.CommitAfter {
		t.Error("no new commits: CommitBefore should equal CommitAfter")
	}
	for _, svc := range result2.Services {
		if svc.Changed {
			t.Errorf("service %s should not be marked changed with no new commits", svc.Name)
		}
	}

	cleanupDeploy(t, filepath.Join(targetDir, "web"))
}

// ──────────────────── Deploy With Changes ────────────────────────────────

func TestIntegration_Deploy_WithChanges(t *testing.T) {
	repoDir, remoteDir, cleanup := setupIntegrationRepo(t)
	defer cleanup()

	targetDir := filepath.Join(t.TempDir(), "changes-target")

	hooks := []config.HookConfig{
		{
			ID:      "integration-changes",
			RepoURL: remoteDir,
			RepoDir: targetDir,
			Branch:  "main",
			Services: []config.ServiceConfig{
				{Name: "web", Path: "web", RestartTrigger: "default"},
				{Name: "api", Path: "api", RestartTrigger: "default"},
			},
		},
	}

	engine := New(hooks, 60*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// First deploy
	result1, err := engine.Deploy(ctx, "integration-changes")
	if err != nil {
		t.Fatalf("first Deploy() error: %v", err)
	}
	t.Logf("first deploy OK: CommitAfter=%s", result1.CommitAfter[:8])
	cleanupDeploy(t, filepath.Join(targetDir, "web"))

	// Add api directory with .env trigger in source repo and push
	apiDir := filepath.Join(repoDir, "api")
	if err := os.MkdirAll(apiDir, 0o755); err != nil {
		t.Fatalf("mkdir api: %v", err)
	}
	if err := os.WriteFile(filepath.Join(apiDir, ".env"), []byte("KEY=value"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	runGit(t, repoDir, "add", "api/.env")
	runGit(t, repoDir, "commit", "-m", "add api .env")
	runGit(t, repoDir, "push", "origin", "main")

	// Second deploy — api should be detected as changed, web not
	result2, err := engine.Deploy(ctx, "integration-changes")
	if err != nil {
		t.Fatalf("second Deploy() error: %v", err)
	}

	for _, svc := range result2.Services {
		t.Logf("  service %s: Changed=%v Restarted=%v Reason=%s",
			svc.Name, svc.Changed, svc.Restarted, svc.Reason)
		switch svc.Name {
		case "api":
			if !svc.Changed {
				t.Error("api with .env should be marked changed")
			}
		case "web":
			if svc.Changed {
				t.Error("web with no changes should not be marked changed")
			}
		}
	}

	// Cleanup both services
	cleanupDeploy(t, filepath.Join(targetDir, "web"))
	cleanupDeploy(t, filepath.Join(targetDir, "api"))
}

// ──────────────────── Concurrent Deploy Blocking ─────────────────────────

func TestIntegration_Deploy_ConcurrentBlocking(t *testing.T) {
	_, remoteDir, cleanupRepo := setupIntegrationRepo(t)
	defer cleanupRepo()

	targetDir := filepath.Join(t.TempDir(), "concurrent-target")

	hooks := []config.HookConfig{
		{
			ID:      "integration-concurrent",
			RepoURL: remoteDir,
			RepoDir: targetDir,
			Branch:  "main",
			Services: []config.ServiceConfig{
				{Name: "web", Path: "web", RestartTrigger: "always"},
			},
		},
	}

	engine := New(hooks, 60*time.Second)

	var wg sync.WaitGroup
	var firstErr, secondErr error
	var firstStarted sync.WaitGroup

	firstStarted.Add(1)
	wg.Add(2)

	// First deploy — should succeed
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		// Signal that we've started (lock acquired)
		// We don't have a way to signal mid-operation, but we can try
		result, err := engine.Deploy(ctx, "integration-concurrent")
		firstErr = err
		if result != nil {
			t.Logf("first deploy: CommitAfter=%s", result.CommitAfter[:8])
		}
		firstStarted.Done()
	}()

	// Give the first goroutine a moment to acquire the lock
	time.Sleep(200 * time.Millisecond)

	// Second deploy — should fail because lock is held
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		_, err := engine.Deploy(ctx, "integration-concurrent")
		secondErr = err
	}()

	wg.Wait()

	if firstErr != nil {
		t.Errorf("first Deploy() should succeed: %v", firstErr)
	}
	if secondErr == nil {
		t.Error("second concurrent Deploy() should fail due to lock contention")
	} else {
		t.Logf("concurrent deploy correctly blocked: %v", secondErr)
	}

	cleanupDeploy(t, filepath.Join(targetDir, "web"))
}

// ──────────────────── Context Cancellation ───────────────────────────────

func TestIntegration_Deploy_CancelledContext(t *testing.T) {
	_, remoteDir, cleanupRepo := setupIntegrationRepo(t)
	defer cleanupRepo()

	targetDir := filepath.Join(t.TempDir(), "ctx-cancel-target")

	hooks := []config.HookConfig{
		{
			ID:      "integration-ctx",
			RepoURL: remoteDir,
			RepoDir: targetDir,
			Branch:  "main",
			Services: []config.ServiceConfig{
				{Name: "web", Path: "web", RestartTrigger: "always"},
			},
		},
	}

	engine := New(hooks, 60*time.Second)

	// First deploy to clone
	ctx1, cancel1 := context.WithTimeout(context.Background(), 120*time.Second)
	_, err := engine.Deploy(ctx1, "integration-ctx")
	cancel1()
	if err != nil {
		t.Fatalf("first Deploy() error: %v", err)
	}

	// Cancelled context for second deploy
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()

	_, err = engine.Deploy(ctx2, "integration-ctx")
	if err == nil {
		t.Fatal("Deploy() with cancelled context should error")
	}
	t.Logf("cancelled context correctly returned error: %v", err)

	cleanupDeploy(t, filepath.Join(targetDir, "web"))
}

// ──────────────────── All Services Test ──────────────────────────────────

func TestIntegration_Deploy_AllServiceTriggers(t *testing.T) {
	repoDir, remoteDir, cleanup := setupIntegrationRepo(t)
	defer cleanup()

	targetDir := filepath.Join(t.TempDir(), "all-triggers-target")

	hooks := []config.HookConfig{
		{
			ID:      "integration-all-triggers",
			RepoURL: remoteDir,
			RepoDir: targetDir,
			Branch:  "main",
			Services: []config.ServiceConfig{
				{Name: "always-svc", Path: "web", RestartTrigger: "always"},
			},
		},
	}

	engine := New(hooks, 60*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// First deploy — clone + deploy all
	result1, err := engine.Deploy(ctx, "integration-all-triggers")
	if err != nil {
		t.Fatalf("first Deploy() error: %v", err)
	}
	for _, svc := range result1.Services {
		t.Logf("first deploy: %s Changed=%v Restarted=%v Reason=%s",
			svc.Name, svc.Changed, svc.Restarted, svc.Reason)
	}

	// Now add a trigger file (.env) to the web service in source, push
	if err := os.WriteFile(
		filepath.Join(repoDir, "web", ".env"),
		[]byte("DEBUG=true"),
		0o644,
	); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	runGit(t, repoDir, "add", "web/.env")
	runGit(t, repoDir, "commit", "-m", "add web .env")
	runGit(t, repoDir, "push", "origin", "main")

	// Second deploy — always-svc should still be deployed (always), and now .env trigger
	result2, err := engine.Deploy(ctx, "integration-all-triggers")
	if err != nil {
		t.Fatalf("second Deploy() error: %v", err)
	}
	for _, svc := range result2.Services {
		t.Logf("second deploy: %s Changed=%v Restarted=%v Reason=%s",
			svc.Name, svc.Changed, svc.Restarted, svc.Reason)
		if !svc.Changed {
			t.Errorf("%s should be changed (always trigger)", svc.Name)
		}
	}

	cleanupDeploy(t, filepath.Join(targetDir, "web"))
}

// ────────────────────────────── Helpers ──────────────────────────────────

// cleanupDeploy runs docker compose down in the given directory.
func cleanupDeploy(t *testing.T, dir string) {
	t.Helper()
	composeFile := filepath.Join(dir, "docker-compose.yaml")
	if _, err := os.Stat(composeFile); os.IsNotExist(err) {
		return
	}
	cmd := exec.Command("docker", "compose", "-f", composeFile, "down", "--timeout", "1")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("cleanup deploy %s: %v\n%s", dir, err, out)
	} else {
		t.Logf("cleanup deploy %s: OK", dir)
	}
}

// listComposeContainers returns running container IDs for a compose project.
func listComposeContainers(t *testing.T, dir string) []string {
	t.Helper()
	composeFile := filepath.Join(dir, "docker-compose.yaml")
	if _, err := os.Stat(composeFile); os.IsNotExist(err) {
		return nil
	}
	cmd := exec.Command("docker", "compose", "-f", composeFile, "ps", "-q")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// fmtDuration formats a Duration nicely.
var _ = fmt.Sprintf // keep fmt import
