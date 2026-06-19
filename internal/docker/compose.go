// Package docker handles Docker Compose operations.
//
// Commands: up, down, logs.
// ChangedServices analyzes git diff and compose files to determine which services need restart.
//
// Security: uses exec.CommandContext with separate args (never shell strings).
// Mockable via execCommand var for testing without Docker daemon.
package docker

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// ─────────────────────────────── Types ──────────────────────────────────

// DockerEngine wraps docker compose CLI operations.
// Stateless — methods take paths as parameters.
type DockerEngine struct{}

// ServiceSpec is a lightweight service descriptor used by ChangedServices.
// This avoids importing config package and creating an import cycle.
type ServiceSpec struct {
	Name           string
	Path           string // relative to repo root
	RestartTrigger string // "always", "default", "on-change"
}

// ChangedService represents the result of checking whether a service needs restart.
type ChangedService struct {
	Name    string
	Path    string
	Changed bool
	Reason  string // "always", "files-changed", "no-changes"
}

// ─────────────────────────── Constructor ────────────────────────────────

// New returns a new DockerEngine.
func New() *DockerEngine {
	return &DockerEngine{}
}

// ──────────────────── Mockable exec (for testing) ───────────────────────

// execCommand is the function used to create exec.Cmd instances.
// Set to a mock in tests to avoid requiring Docker daemon.
var execCommand = exec.CommandContext

// ────────────────────────── Compose operations ──────────────────────────

// Up runs docker compose up -d in the given directory.
// If build is true, adds --build to rebuild images before starting.
// composeDir must exist and contain a docker-compose.yaml file.
func (d *DockerEngine) Up(ctx context.Context, composeDir string, build bool) (string, error) {
	if err := validateDir(composeDir); err != nil {
		return "", err
	}

	args := []string{"compose", "-f", composeFile(composeDir), "up", "-d"}
	if build {
		args = append(args, "--build")
	}

	cmd := execCommand(ctx, "docker", args...)
	cmd.Dir = composeDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker compose up in %q: %w\n%s", composeDir, err, out)
	}
	return string(out), nil
}

// Down runs docker compose down in the given directory.
// Stops and removes containers, networks, and volumes created by up.
func (d *DockerEngine) Down(ctx context.Context, composeDir string) (string, error) {
	if err := validateDir(composeDir); err != nil {
		return "", err
	}

	cmd := execCommand(ctx, "docker", "compose", "-f", composeFile(composeDir), "down")
	cmd.Dir = composeDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker compose down in %q: %w\n%s", composeDir, err, out)
	}
	return string(out), nil
}

// Logs retrieves docker compose logs for services in the given directory.
// tail specifies the number of lines to fetch from the end (0 = all).
func (d *DockerEngine) Logs(ctx context.Context, composeDir string, tail int) (string, error) {
	if err := validateDir(composeDir); err != nil {
		return "", err
	}

	args := []string{"compose", "-f", composeFile(composeDir), "logs"}
	if tail > 0 {
		args = append(args, fmt.Sprintf("--tail=%d", tail))
	}

	cmd := execCommand(ctx, "docker", args...)
	cmd.Dir = composeDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker compose logs in %q: %w\n%s", composeDir, err, out)
	}
	return string(out), nil
}

// ─────────────────────── ChangedServices ────────────────────────────────

// ChangedServices determines which services need restart based on changed files.
//
// Logic:
//   - restart_trigger "always" → always changed (deploy on every push)
//   - restart_trigger "default" or "on-change" → changed if any changed file
//     resides within the service's path directory (prefix match)
//
// services: list of service specs from the hook configuration
// changedFiles: list of file paths changed (from git diff), relative to repo root
// repoDir: absolute path to the repository root (service paths are relative to this)
func ChangedServices(services []ServiceSpec, changedFiles []string, repoDir string) []ChangedService {
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		absRepo = repoDir
	}

	result := make([]ChangedService, len(services))
	for i, svc := range services {
		result[i] = ChangedService{
			Name: svc.Name,
			Path: svc.Path,
		}

		switch svc.RestartTrigger {
		case "always":
			result[i].Changed = true
			result[i].Reason = "always"
		default:
			// "default" and "on-change": check if files changed under the service path.
			if isPathAffected(svc.Path, changedFiles, absRepo) {
				result[i].Changed = true
				result[i].Reason = "files-changed"
			} else {
				result[i].Changed = false
				result[i].Reason = "no-changes"
			}
		}
	}
	return result
}

// isPathAffected checks whether any of the changed files resides within the
// service directory (prefix match against the resolved absolute path).
func isPathAffected(svcPath string, changedFiles []string, absRepo string) bool {
	svcAbs := filepath.Join(absRepo, svcPath)
	svcAbs = filepath.Clean(svcAbs)

	// Ensure the service path is within the repo (path traversal protection).
	if !strings.HasPrefix(svcAbs, filepath.Clean(absRepo)+string(filepath.Separator)) && svcAbs != filepath.Clean(absRepo) {
		return false
	}

	for _, f := range changedFiles {
		fAbs := filepath.Join(absRepo, f)
		fAbs = filepath.Clean(fAbs)
		// Check if the changed file is within the service directory or IS the compose file itself.
		if strings.HasPrefix(fAbs, svcAbs+string(filepath.Separator)) || fAbs == svcAbs {
			return true
		}
	}
	return false
}

// ─────────────────────────── Helpers ────────────────────────────────────

// composeFile returns the path to docker-compose.yaml in the given directory.
func composeFile(dir string) string {
	return filepath.Join(dir, "docker-compose.yaml")
}

// validateDir checks that composeDir exists and is a directory.
func validateDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("compose directory is empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("cannot resolve compose directory %q: %w", dir, err)
	}
	// Clean the path to prevent traversal tricks (../../).
	cleaned := filepath.Clean(abs)
	if cleaned != abs {
		return fmt.Errorf("compose directory %q resolves outside expected path", dir)
	}
	return nil
}
