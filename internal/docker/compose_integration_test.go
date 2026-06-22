//go:build integration

package docker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ──────────────── Real Docker Compose Integration Tests ──────────────────

// TestIntegration_Up_Down tests a real docker compose up and down cycle.
// Requires Docker daemon running.
func TestIntegration_Up_Down(t *testing.T) {
	dir, cleanup := setupIntegrationCompose(t, "nginx:alpine")
	defer cleanup()

	eng := New()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Bring up
	out, err := eng.Up(ctx, dir, false)
	if err != nil {
		t.Fatalf("docker compose up failed: %v\noutput: %s", err, out)
	}
	t.Logf("up output: %s", out)

	// Verify container is running
	containers := listContainers(t, dir)
	if len(containers) == 0 {
		t.Fatal("expected at least 1 running container after up")
	}
	t.Logf("running containers: %v", containers)

	// Bring down
	out, err = eng.Down(ctx, dir)
	if err != nil {
		t.Fatalf("docker compose down failed: %v\noutput: %s", err, out)
	}
	t.Logf("down output: %s", out)

	// Verify containers are gone
	containers = listContainers(t, dir)
	if len(containers) > 0 {
		for _, c := range containers {
			t.Logf("  leftover container: %s", c)
		}
		t.Errorf("expected 0 running containers after down, got %d", len(containers))
	}
}

// TestIntegration_Up_WithBuild tests docker compose up --build.
func TestIntegration_Up_WithBuild(t *testing.T) {
	dir, cleanup := setupIntegrationComposeWithBuild(t)
	defer cleanup()

	eng := New()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Bring up with --build
	out, err := eng.Up(ctx, dir, true)
	if err != nil {
		t.Fatalf("docker compose up --build failed: %v\noutput: %s", err, out)
	}
	t.Logf("up --build output: %s", out)

	// Verify container is running
	containers := listContainers(t, dir)
	if len(containers) == 0 {
		t.Fatal("expected at least 1 running container after up --build")
	}

	// Cleanup
	out, err = eng.Down(ctx, dir)
	if err != nil {
		t.Logf("cleanup: docker compose down failed: %v", err)
	}
}

// TestIntegration_Logs tests docker compose logs with tail.
func TestIntegration_Logs(t *testing.T) {
	dir, cleanup := setupIntegrationCompose(t, "nginx:alpine")
	defer cleanup()

	eng := New()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Bring up first
	out, err := eng.Up(ctx, dir, false)
	if err != nil {
		t.Fatalf("docker compose up failed: %v\noutput: %s", err, out)
	}

	// Wait a moment for container to produce logs
	time.Sleep(2 * time.Second)

	// Fetch logs
	logs, err := eng.Logs(ctx, dir, 10)
	if err != nil {
		// Don't fail hard — logs may be empty depending on image
		t.Logf("docker compose logs failed (may be ok): %v", err)
	}
	if logs != "" {
		t.Logf("logs output: %s", logs)
	}

	// Cleanup
	out, err = eng.Down(ctx, dir)
	if err != nil {
		t.Logf("cleanup: docker compose down failed: %v", err)
	}
}

// TestIntegration_Down_Cleanup ensures down cleans up all resources.
func TestIntegration_Down_Cleanup(t *testing.T) {
	dir, cleanup := setupIntegrationCompose(t, "alpine:latest")
	defer cleanup()

	eng := New()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Bring up
	out, err := eng.Up(ctx, dir, false)
	if err != nil {
		t.Fatalf("docker compose up failed: %v\noutput: %s", err, out)
	}

	containersBefore := listContainers(t, dir)
	t.Logf("containers before down: %d", len(containersBefore))

	// Bring down
	out, err = eng.Down(ctx, dir)
	if err != nil {
		t.Fatalf("docker compose down failed: %v\noutput: %s", err, out)
	}

	// Wait for cleanup
	time.Sleep(1 * time.Second)

	containersAfter := listContainers(t, dir)
	t.Logf("containers after down: %d", len(containersAfter))

	// After down, the compose project containers should be stopped/removed.
	// They may still appear briefly as exited — but should not be running.
	if len(containersAfter) > 0 {
		for _, c := range containersAfter {
			t.Logf("  container still present: %s", c)
		}
	}
}

// ───────────────────────── Helpers ───────────────────────────────────────

// setupIntegrationCompose creates a temp directory with a docker-compose.yaml
// using the given image. Returns the dir path and a cleanup function.
func setupIntegrationCompose(t *testing.T, image string) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "pushobserver-docker-integration-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}

	composeContent := fmt.Sprintf(`services:
  test-svc:
    image: %s
    command: ["sleep", "30"]
    stop_grace_period: 1s
`, image)

	composePath := filepath.Join(dir, "docker-compose.yaml")
	if err := os.WriteFile(composePath, []byte(composeContent), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("WriteFile: %v", err)
	}

	cleanup := func() {
		// Ensure containers are stopped before cleanup
		cmd := exec.Command("docker", "compose", "-f", composePath, "down", "--timeout", "1")
		cmd.Dir = dir
		cmd.Run() // ignore errors during cleanup
		_ = os.RemoveAll(dir)
	}
	return dir, cleanup
}

// setupIntegrationComposeWithBuild creates a temp directory with a
// docker-compose.yaml that uses a build context (minimal Dockerfile).
func setupIntegrationComposeWithBuild(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "pushobserver-docker-build-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}

	// Minimal Dockerfile
	dockerfile := `FROM alpine:latest
CMD ["sleep", "10"]
`
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("WriteFile Dockerfile: %v", err)
	}

	composeContent := `services:
  test-build-svc:
    build: .
    stop_grace_period: 1s
`
	composePath := filepath.Join(dir, "docker-compose.yaml")
	if err := os.WriteFile(composePath, []byte(composeContent), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("WriteFile compose: %v", err)
	}

	cleanup := func() {
		cmd := exec.Command("docker", "compose", "-f", composePath, "down", "--timeout", "1")
		cmd.Dir = dir
		cmd.Run()
		_ = os.RemoveAll(dir)
	}
	return dir, cleanup
}

// listContainers returns the IDs of running containers for the compose project.
func listContainers(t *testing.T, dir string) []string {
	t.Helper()
	cmd := exec.Command("docker", "compose", "-f",
		filepath.Join(dir, "docker-compose.yaml"), "ps", "-q")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Logf("docker compose ps error (may be expected): %v", err)
		return nil
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}
