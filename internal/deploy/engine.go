// Package deploy orchestrates the full deployment pipeline.
//
// Pipeline: lock → git pull → detect changes → docker compose up → unlock.
// Uses mutex per hook ID to serialize deployments for the same hook.
// Different hooks deploy concurrently.
package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"forgejo.humbertof.dev/humberto/push-observer/internal/config"
	"forgejo.humbertof.dev/humberto/push-observer/internal/docker"
	"forgejo.humbertof.dev/humberto/push-observer/internal/git"
)

// ─────────────────────────────── Types ──────────────────────────────────

// Engine orchestrates the full deployment pipeline: lock, git pull/clone,
// change detection, docker compose up, and result collection.
type Engine struct {
	hooks     map[string]config.HookConfig
	lockMap   map[string]*sync.Mutex
	lockMapMu sync.Mutex
	timeout   time.Duration
}

// DeployResult holds the outcome of a deployment for a single hook.
type DeployResult struct {
	HookID       string
	CommitBefore string
	CommitAfter  string
	Services     []DeployServiceResult
	Duration     time.Duration
	Error        error
}

// DeployServiceResult holds the outcome for a single service within a deploy.
type DeployServiceResult struct {
	Name      string
	Changed   bool
	Restarted bool
	Reason    string
	Output    string
	Error     error
}

// ──────────────────── Default trigger files ─────────────────────────────

// defaultTriggerFiles are filenames that always trigger a restart when changed
// within a service directory, regardless of the restart_trigger setting.
// These are the files that universally signal a configuration change.
var defaultTriggerFiles = map[string]bool{
	".env":                true,
	"Dockerfile":          true,
	"docker-compose.yaml": true,
	"docker-compose.yml":  true,
}

// ───────────────────────── Constructor ──────────────────────────────────

// New creates a new deploy Engine with the given hooks and default timeout.
func New(hooks []config.HookConfig, timeout time.Duration) *Engine {
	hookMap := make(map[string]config.HookConfig, len(hooks))
	for _, h := range hooks {
		hookMap[h.ID] = h
	}
	return &Engine{
		hooks:   hookMap,
		timeout: timeout,
	}
}

// ─────────────────────── Lock management ────────────────────────────────

// acquireLock tries to acquire a mutex for the given hook ID.
// Returns nil if the lock is already held by another deployment.
func (e *Engine) acquireLock(hookID string) *sync.Mutex {
	e.lockMapMu.Lock()
	if e.lockMap == nil {
		e.lockMap = make(map[string]*sync.Mutex)
	}
	mu, exists := e.lockMap[hookID]
	if !exists {
		mu = &sync.Mutex{}
		e.lockMap[hookID] = mu
	}
	e.lockMapMu.Unlock()

	if !mu.TryLock() {
		return nil
	}
	return mu
}

// ────────────────────────── Deploy ──────────────────────────────────────

// Deploy orchestrates the full deployment pipeline for a hook.
//
// Flow:
//  1. Lock by hook ID
//  2. Git clone (first deploy) or pull (subsequent)
//  3. Detect changed files via git diff
//  4. For each configured service, determine if restart is needed (ShouldRestart)
//  5. Docker compose up for affected services
//  6. Unlock (via defer)
//  7. Return DeployResult with timing and per-service outcome
func (e *Engine) Deploy(ctx context.Context, hookID string) (*DeployResult, error) {
	hook, ok := e.hooks[hookID]
	if !ok {
		return nil, fmt.Errorf("hook %q not found", hookID)
	}

	// 1. Lock
	lock := e.acquireLock(hookID)
	if lock == nil {
		return nil, fmt.Errorf("hook %q is already being deployed", hookID)
	}
	defer lock.Unlock()

	start := time.Now()
	result := &DeployResult{HookID: hookID}

	// Determine effective timeout
	timeout := e.timeout
	if hook.Deploy.Timeout > 0 {
		timeout = hook.Deploy.Timeout.Duration()
	}

	// 2. Create Git engine
	gitEngine := git.New(hook.RepoDir, hook.GitSSHKey, timeout)

	// Check if repository already exists on disk
	repoExists := false
	if info, err := os.Stat(filepath.Join(hook.RepoDir, ".git")); err == nil && info.IsDir() {
		repoExists = true
	}

	if !repoExists {
		// First deploy: clone the repository
		if err := gitEngine.Clone(ctx, hook.RepoURL, hook.Branch); err != nil {
			result.Error = fmt.Errorf("git clone: %w", err)
			result.Duration = time.Since(start)
			return result, result.Error
		}
		result.CommitBefore = ""
	} else {
		// Existing repo: save commit hash before pull
		commitBefore, err := gitEngine.LastCommit(ctx)
		if err != nil {
			result.Error = fmt.Errorf("git last commit: %w", err)
			result.Duration = time.Since(start)
			return result, result.Error
		}
		result.CommitBefore = commitBefore

		// Pull latest changes
		if err := gitEngine.Pull(ctx, hook.Branch); err != nil {
			result.Error = fmt.Errorf("git pull: %w", err)
			result.Duration = time.Since(start)
			return result, result.Error
		}
	}

	// 3. Get current commit after clone/pull
	commitAfter, err := gitEngine.LastCommit(ctx)
	if err != nil {
		result.Error = fmt.Errorf("git last commit (after): %w", err)
		result.Duration = time.Since(start)
		return result, result.Error
	}
	result.CommitAfter = commitAfter

	// 4. Detect changed files (only if we have a "before" commit to diff against)
	var changedFiles []string
	if result.CommitBefore != "" && result.CommitBefore != result.CommitAfter {
		changedFiles, err = gitEngine.ChangedFiles(ctx, result.CommitBefore)
		if err != nil {
			result.Error = fmt.Errorf("git diff: %w", err)
			result.Duration = time.Since(start)
			return result, result.Error
		}
	}

	// 5. Process each service
	dockerEngine := docker.New()
	customExtensions := hook.Deploy.CustomExtensions

	for _, svc := range hook.Services {
		svcResult := DeployServiceResult{Name: svc.Name}

		// On first deploy (no CommitBefore), deploy all services
		shouldRestart := result.CommitBefore == "" || ShouldRestart(svc, changedFiles, customExtensions, hook.RepoDir)
		svcResult.Changed = shouldRestart

		if !shouldRestart {
			svcResult.Reason = "no-changes"
			svcResult.Restarted = false
			result.Services = append(result.Services, svcResult)
			continue
		}

		svcResult.Reason = "restart-triggered"

		// Resolve compose directory
		composeDir := filepath.Join(hook.RepoDir, svc.Path)
		if svc.Path == "." {
			composeDir = hook.RepoDir
		}

		// Docker compose up
		output, upErr := dockerEngine.Up(ctx, composeDir, hook.Compose.Build)
		svcResult.Output = output
		if upErr != nil {
			svcResult.Error = upErr
			svcResult.Restarted = false
		} else {
			svcResult.Restarted = true
		}

		result.Services = append(result.Services, svcResult)
	}

	result.Duration = time.Since(start)
	return result, nil
}

// ──────────────────────── ShouldRestart ─────────────────────────────────

// ShouldRestart determines whether a service should be restarted based on
// changed files, restart trigger configuration, and custom extensions.
//
// Logic:
//   - "always": restart regardless of changed files.
//   - "default": restart if any default trigger file (.env, Dockerfile,
//     docker-compose.yaml, docker-compose.yml) changed within the service path.
//   - "on-change": restart if any default trigger file OR any file with a
//     matching custom extension changed within the service path.
//   - Unknown trigger: behaves like "default".
//
// Path traversal protection: service paths that resolve outside the repo
// directory always return false.
func ShouldRestart(svc config.ServiceConfig, changedFiles []string, customExtensions []string, repoDir string) bool {
	// "always" trigger bypasses all checks.
	if svc.RestartTrigger == "always" {
		return true
	}

	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		absRepo = repoDir
	}
	svcAbs := filepath.Join(absRepo, svc.Path)
	svcAbs = filepath.Clean(svcAbs)

	// Path traversal protection: reject paths that escape the repo directory.
	cleanRepo := filepath.Clean(absRepo)
	if !strings.HasPrefix(svcAbs, cleanRepo+string(filepath.Separator)) && svcAbs != cleanRepo {
		return false
	}

	for _, f := range changedFiles {
		fAbs := filepath.Join(absRepo, f)
		fAbs = filepath.Clean(fAbs)

		// Check if the file is within the service directory.
		if !strings.HasPrefix(fAbs, svcAbs+string(filepath.Separator)) && fAbs != svcAbs {
			continue
		}

		// Default trigger files always trigger restart.
		base := filepath.Base(f)
		if defaultTriggerFiles[base] {
			return true
		}

		// "on-change" trigger: check custom extensions.
		if svc.RestartTrigger == "on-change" {
			ext := filepath.Ext(f)
			for _, customExt := range customExtensions {
				if ext == customExt {
					return true
				}
			}
		}
	}

	return false
}
