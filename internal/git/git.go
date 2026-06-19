// Package git handles Git operations: clone, pull, diff, commit history.
//
// Uses os/exec with git CLI. SSH keys via GIT_SSH_COMMAND env var.
// Repository-level locking prevents concurrent operations on the same repo.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// GitEngine manages git operations for a single repository worktree.
// All operations are safe for concurrent use via mutex map.
type GitEngine struct {
	workDir string
	sshKey  string
	timeout time.Duration
}

// Mutex map: one mutex per canonical workDir.
var (
	lockMap   = make(map[string]*sync.Mutex)
	lockMapMu sync.Mutex
)

// New creates a GitEngine for the given workDir and SSH deploy key.
// timeout is the maximum duration for each git operation.
// sshKey is validated: paths with shell metacharacters (;, |, `, $, &, etc.) are rejected.
func New(workDir, sshKey string, timeout time.Duration) *GitEngine {
	// Validate SSH key path — reject shell metacharacters
	if sshKey != "" && containsShellMeta(sshKey) {
		sshKey = "" // reject dangerous paths; caller should check
	}
	return &GitEngine{
		workDir: workDir,
		sshKey:  sshKey,
		timeout: timeout,
	}
}

// ───────────────── lock management ─────────────────

// acquireLock tries to acquire the mutex for this engine's workDir.
// Returns nil if the lock is already held (caller must not proceed).
// Returns the mutex (locked) on success — caller MUST Unlock().
func (g *GitEngine) acquireLock() *sync.Mutex {
	absPath, err := filepath.Abs(g.workDir)
	if err != nil {
		absPath = g.workDir
	}

	lockMapMu.Lock()
	mu, exists := lockMap[absPath]
	if !exists {
		mu = &sync.Mutex{}
		lockMap[absPath] = mu
	}
	lockMapMu.Unlock()

	// TryLock: if already locked, return nil
	if !mu.TryLock() {
		return nil
	}
	return mu
}

// ───────────────── SSH helper ─────────────────

// sshCommand returns the GIT_SSH_COMMAND value for this engine.
// If no SSH key is configured, returns empty string.
// Paths with spaces are single-quoted for shell safety.
func (g *GitEngine) sshCommand() string {
	if g.sshKey == "" {
		return ""
	}
	key := g.sshKey
	// Single-quote the path to handle spaces safely
	return fmt.Sprintf("ssh -i '%s' -o StrictHostKeyChecking=accept-new -o PasswordAuthentication=no", key)
}

// ───────────────── git execution ─────────────────

// gitCmd builds an exec.Cmd with the engine's timeout context and SSH env.
func (g *GitEngine) gitCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.workDir

	if sshCmd := g.sshCommand(); sshCmd != "" {
		cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd)
	}
	return cmd
}

// gitRun runs a git command and returns trimmed stdout or an error.
func (g *GitEngine) gitRun(ctx context.Context, args ...string) (string, error) {
	cmd := g.gitCmd(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		stderrStr := stderr.String()
		// Sanitize error: strip potential secrets in URLs
		stderrStr = sanitizeError(stderrStr)
		return "", fmt.Errorf("git %v: %w\n%s", args, err, stderrStr)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ───────────────── public API ─────────────────

// Clone clones repoURL into workDir, checking out the given branch.
// If workDir already exists and is non-empty, returns an error.
// Uses SSH deploy key if configured.
func (g *GitEngine) Clone(ctx context.Context, repoURL, branch string) error {
	// Validate inputs against shell injection
	if containsShellMeta(repoURL) {
		return fmt.Errorf("invalid repo URL: contains shell metacharacters")
	}
	if containsShellMeta(branch) {
		return fmt.Errorf("invalid branch name: contains shell metacharacters")
	}

	lock := g.acquireLock()
	if lock == nil {
		return fmt.Errorf("repository %s is locked by another operation", g.workDir)
	}
	defer lock.Unlock()

	// Check if workDir already exists and is non-empty
	if info, err := os.Stat(g.workDir); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("workDir %q exists and is not a directory", g.workDir)
		}
		entries, _ := os.ReadDir(g.workDir)
		if len(entries) > 0 {
			return fmt.Errorf("workDir %q already exists and is not empty", g.workDir)
		}
	}

	args := []string{"clone", "--branch", branch, repoURL, g.workDir}
	// When workDir doesn't exist yet, we need to clone into parent dir.
	// git clone creates workDir automatically — run from parent.
	parentDir := filepath.Dir(g.workDir)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = parentDir
	if sshCmd := g.sshCommand(); sshCmd != "" {
		cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := sanitizeError(stderr.String())
		return fmt.Errorf("git clone: %w\n%s", err, stderrStr)
	}
	return nil
}

// Pull fetches and resets to origin/branch (hard reset, clean state).
// Requires a prior clone (workDir must be a git repository).
func (g *GitEngine) Pull(ctx context.Context, branch string) error {
	if containsShellMeta(branch) {
		return fmt.Errorf("invalid branch name: contains shell metacharacters")
	}

	lock := g.acquireLock()
	if lock == nil {
		return fmt.Errorf("repository %s is locked by another operation", g.workDir)
	}
	defer lock.Unlock()

	// Verify it's a git repo
	if _, err := os.Stat(filepath.Join(g.workDir, ".git")); os.IsNotExist(err) {
		return fmt.Errorf("workDir %q is not a git repository (run Clone first)", g.workDir)
	}

	// Fetch from origin
	if _, err := g.gitRun(ctx, "fetch", "origin", branch); err != nil {
		return fmt.Errorf("git fetch: %w", err)
	}

	// Hard reset to origin/branch — clean state, no merge conflicts
	if _, err := g.gitRun(ctx, "reset", "--hard", "origin/"+branch); err != nil {
		return fmt.Errorf("git reset: %w", err)
	}

	return nil
}

// ChangedFiles returns the list of files changed between sinceCommit and HEAD.
// Uses git diff --name-only.
func (g *GitEngine) ChangedFiles(ctx context.Context, sinceCommit string) ([]string, error) {
	if containsShellMeta(sinceCommit) {
		return nil, fmt.Errorf("invalid commit hash: contains shell metacharacters")
	}

	lock := g.acquireLock()
	if lock == nil {
		return nil, fmt.Errorf("repository %s is locked by another operation", g.workDir)
	}
	defer lock.Unlock()

	out, err := g.gitRun(ctx, "diff", "--name-only", sinceCommit, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// LastCommit returns the full SHA of HEAD.
func (g *GitEngine) LastCommit(ctx context.Context) (string, error) {
	lock := g.acquireLock()
	if lock == nil {
		return "", fmt.Errorf("repository %s is locked by another operation", g.workDir)
	}
	defer lock.Unlock()

	return g.gitRun(ctx, "rev-parse", "HEAD")
}

// CurrentBranch returns the name of the current branch.
func (g *GitEngine) CurrentBranch(ctx context.Context) (string, error) {
	lock := g.acquireLock()
	if lock == nil {
		return "", fmt.Errorf("repository %s is locked by another operation", g.workDir)
	}
	defer lock.Unlock()

	return g.gitRun(ctx, "rev-parse", "--abbrev-ref", "HEAD")
}

// ───────────────── sanitization ─────────────────

// shellMetaPattern matches shell metacharacters that could enable command injection.
var shellMetaPattern = regexp.MustCompile(`[;&|` + "`" + `$(){}\[\]<>!\n\r]`)

// containsShellMeta returns true if the input contains dangerous shell metacharacters.
func containsShellMeta(s string) bool {
	return shellMetaPattern.MatchString(s)
}

// urlSecretPattern matches common secret patterns in git URLs (tokens, passwords).
var urlSecretPattern = regexp.MustCompile(`(https?://)[^@]*@`)

// sanitizeError removes sensitive information from git error output.
func sanitizeError(s string) string {
	return urlSecretPattern.ReplaceAllString(s, "$1***@")
}

// ───────────────── helpers (exported for tests) ─────────────────

// WorkDir returns the engine's work directory (for tests).
func (g *GitEngine) WorkDir() string { return g.workDir }

// SSHKey returns the engine's SSH key path (for tests).
func (g *GitEngine) SSHKey() string { return g.sshKey }
