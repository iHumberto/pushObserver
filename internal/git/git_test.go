package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	engine := New("/tmp/test-repo", "/path/to/key", 30*time.Second)

	if engine == nil {
		t.Fatal("New() returned nil")
	}
	if engine.workDir != "/tmp/test-repo" {
		t.Errorf("workDir = %q, want %q", engine.workDir, "/tmp/test-repo")
	}
	if engine.sshKey != "/path/to/key" {
		t.Errorf("sshKey = %q, want %q", engine.sshKey, "/path/to/key")
	}
	if engine.timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", engine.timeout)
	}
}

// ───────────────── helpers ─────────────────

// tempGitRepo creates a temporary git repository with an initial commit.
func tempGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@test.local")
	runGit(t, dir, "config", "user.name", "Test")
	writeFile(t, dir, "README.md", "# test repo")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial commit")
	return dir
}

// bareRemote creates a bare repo, pushes from src to it, and returns the bare path.
// The bare repo acts as the "remote" for clone/pull tests.
func bareRemote(t *testing.T, srcDir string) string {
	t.Helper()
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare")
	runGit(t, srcDir, "remote", "add", "origin", bare)
	runGit(t, srcDir, "push", "origin", "main")
	return bare
}

// addCommit creates a new file and commits it. Returns the commit hash.
func addCommit(t *testing.T, repoDir, filename, content string) string {
	t.Helper()
	writeFile(t, repoDir, filename, content)
	runGit(t, repoDir, "add", filename)
	runGit(t, repoDir, "commit", "-m", "add "+filename)
	return lastCommitHash(t, repoDir)
}

// lastCommitHash returns the hash of HEAD.
func lastCommitHash(t *testing.T, repoDir string) string {
	t.Helper()
	out := runGit(t, repoDir, "rev-parse", "HEAD")
	return strings.TrimSpace(out)
}

// writeFile creates a file with content.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile(%q): %v", name, err)
	}
}

// runGit runs a git command in dir and returns stdout.
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

// ───────────────── functional tests ─────────────────

func TestClone(t *testing.T) {
	srcDir := tempGitRepo(t)
	remote := bareRemote(t, srcDir)
	workDir := filepath.Join(t.TempDir(), "clone-target")

	engine := New(workDir, "", 30*time.Second)
	err := engine.Clone(t.Context(), remote, "main")

	if err != nil {
		t.Fatalf("Clone() error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, "README.md")); os.IsNotExist(err) {
		t.Error("README.md not found after clone")
	}
	if _, err := os.Stat(filepath.Join(workDir, ".git")); os.IsNotExist(err) {
		t.Error(".git directory not found after clone")
	}
}

func TestCloneNonexistentBranch(t *testing.T) {
	srcDir := tempGitRepo(t)
	remote := bareRemote(t, srcDir)
	workDir := filepath.Join(t.TempDir(), "clone-target")

	engine := New(workDir, "", 30*time.Second)
	err := engine.Clone(t.Context(), remote, "nonexistent")

	if err == nil {
		t.Fatal("Clone() with nonexistent branch should error")
	}
}

func TestPull(t *testing.T) {
	srcDir := tempGitRepo(t)
	remote := bareRemote(t, srcDir)
	workDir := filepath.Join(t.TempDir(), "pull-target")

	engine := New(workDir, "", 30*time.Second)
	if err := engine.Clone(t.Context(), remote, "main"); err != nil {
		t.Fatalf("Clone() error: %v", err)
	}

	// Add a commit to the source and push to remote
	addCommit(t, srcDir, "new-file.txt", "new content")
	runGit(t, srcDir, "push", "origin", "main")

	// Pull should fetch the new commit
	if err := engine.Pull(t.Context(), "main"); err != nil {
		t.Fatalf("Pull() error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, "new-file.txt")); os.IsNotExist(err) {
		t.Error("new-file.txt not found after pull — pull didn't fetch new commit")
	}
}

func TestChangedFiles(t *testing.T) {
	srcDir := tempGitRepo(t)
	remote := bareRemote(t, srcDir)
	workDir := filepath.Join(t.TempDir(), "diff-target")

	engine := New(workDir, "", 30*time.Second)
	if err := engine.Clone(t.Context(), remote, "main"); err != nil {
		t.Fatalf("Clone() error: %v", err)
	}

	initialCommit := lastCommitHash(t, workDir)

	// Add a new commit to source and push
	addCommit(t, srcDir, "changed.txt", "changed")
	runGit(t, srcDir, "push", "origin", "main")

	// Pull to get the new commit
	if err := engine.Pull(t.Context(), "main"); err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	files, err := engine.ChangedFiles(t.Context(), initialCommit)
	if err != nil {
		t.Fatalf("ChangedFiles() error: %v", err)
	}
	if len(files) != 1 || files[0] != "changed.txt" {
		t.Errorf("ChangedFiles() = %v, want [\"changed.txt\"]", files)
	}
}

func TestChangedFilesNoChanges(t *testing.T) {
	srcDir := tempGitRepo(t)
	remote := bareRemote(t, srcDir)
	workDir := filepath.Join(t.TempDir(), "nochange-target")

	engine := New(workDir, "", 30*time.Second)
	if err := engine.Clone(t.Context(), remote, "main"); err != nil {
		t.Fatalf("Clone() error: %v", err)
	}

	head := lastCommitHash(t, workDir)
	files, err := engine.ChangedFiles(t.Context(), head)
	if err != nil {
		t.Fatalf("ChangedFiles() error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("ChangedFiles() = %v, want empty when same commit", files)
	}
}

func TestLastCommit(t *testing.T) {
	srcDir := tempGitRepo(t)
	remote := bareRemote(t, srcDir)
	workDir := filepath.Join(t.TempDir(), "commit-target")

	engine := New(workDir, "", 30*time.Second)
	if err := engine.Clone(t.Context(), remote, "main"); err != nil {
		t.Fatalf("Clone() error: %v", err)
	}

	hash, err := engine.LastCommit(t.Context())
	if err != nil {
		t.Fatalf("LastCommit() error: %v", err)
	}
	if len(hash) < 7 {
		t.Errorf("LastCommit() = %q, want 40-char SHA", hash)
	}
}

func TestCurrentBranch(t *testing.T) {
	srcDir := tempGitRepo(t)
	remote := bareRemote(t, srcDir)
	workDir := filepath.Join(t.TempDir(), "branch-target")

	engine := New(workDir, "", 30*time.Second)
	if err := engine.Clone(t.Context(), remote, "main"); err != nil {
		t.Fatalf("Clone() error: %v", err)
	}

	branch, err := engine.CurrentBranch(t.Context())
	if err != nil {
		t.Fatalf("CurrentBranch() error: %v", err)
	}
	if branch != "main" {
		t.Errorf("CurrentBranch() = %q, want \"main\"", branch)
	}
}

func TestCloneIntoExistingDir(t *testing.T) {
	srcDir := tempGitRepo(t)
	remote := bareRemote(t, srcDir)
	workDir := filepath.Join(t.TempDir(), "existing-target")

	// First clone succeeds
	engine := New(workDir, "", 30*time.Second)
	if err := engine.Clone(t.Context(), remote, "main"); err != nil {
		t.Fatalf("first Clone() error: %v", err)
	}

	// Second clone into existing dir
	engine2 := New(workDir, "", 30*time.Second)
	err := engine2.Clone(t.Context(), remote, "main")
	if err == nil {
		t.Fatal("Clone() into existing dir should error")
	}
}

// ───────────────── concurrency test ─────────────────

func TestLockConcurrency(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "lock-target")

	engine := New(workDir, "", 30*time.Second)

	// First operation acquires lock
	lock := engine.acquireLock()
	if lock == nil {
		t.Fatal("acquireLock() returned nil")
	}

	// Verify lock is held — try to acquire from another engine
	engine2 := New(workDir, "", 30*time.Second)
	lock2 := engine2.acquireLock()
	if lock2 != nil {
		lock2.Unlock()
		t.Error("second acquireLock() should return nil while lock is held")
	}

	lock.Unlock()

	// Now second engine can acquire
	lock2 = engine2.acquireLock()
	if lock2 == nil {
		t.Error("acquireLock() should succeed after unlock")
	}
	if lock2 != nil {
		lock2.Unlock()
	}
}

// ───────────────── security tests ─────────────────

func TestCloneRejectsShellMetacharactersInURL(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "injection-target")

	engine := New(workDir, "", 30*time.Second)
	err := engine.Clone(t.Context(), "/tmp/repo; rm -rf /", "main")

	if err == nil {
		t.Fatal("Clone() with shell metacharacters in URL should error")
	}
	// Ensure workDir was not created (clone didn't happen)
	if _, statErr := os.Stat(workDir); !os.IsNotExist(statErr) {
		t.Error("workDir should not exist after failed injection attempt")
	}
}

func TestCloneRejectsShellMetacharactersInBranch(t *testing.T) {
	srcDir := tempGitRepo(t)
	remote := bareRemote(t, srcDir)
	workDir := filepath.Join(t.TempDir(), "branch-injection-target")

	engine := New(workDir, "", 30*time.Second)
	err := engine.Clone(t.Context(), remote, "main; id")

	if err == nil {
		t.Fatal("Clone() with shell metacharacters in branch should error")
	}
}

func TestSSHKeyPathValidation(t *testing.T) {
	// Path with spaces must be quoted in GIT_SSH_COMMAND
	engine := New("/tmp/repo", "/path/with spaces/key", 30*time.Second)
	sshCmd := engine.sshCommand()
	if !strings.Contains(sshCmd, "'/path/with spaces/key'") {
		t.Errorf("sshCommand() = %q, path with spaces must be single-quoted", sshCmd)
	}
}

func TestSSHKeyPathRejectsDangerous(t *testing.T) {
	// Semicolons in SSH key path should be rejected
	engine := New("/tmp/repo", "/tmp/key; rm -rf /", 30*time.Second)
	if engine.sshKey != "" {
		// The constructor should sanitize
		t.Error("sshKey with shell metacharacters should be rejected")
	}
}

func TestChangedFilesInvalidCommit(t *testing.T) {
	srcDir := tempGitRepo(t)
	remote := bareRemote(t, srcDir)
	workDir := filepath.Join(t.TempDir(), "invalid-commit-target")

	engine := New(workDir, "", 30*time.Second)
	if err := engine.Clone(t.Context(), remote, "main"); err != nil {
		t.Fatalf("Clone() error: %v", err)
	}

	_, err := engine.ChangedFiles(t.Context(), "deadbeef")
	if err == nil {
		t.Fatal("ChangedFiles() with invalid commit should error")
	}
}

func TestTimeout(t *testing.T) {
	srcDir := tempGitRepo(t)
	remote := bareRemote(t, srcDir)
	workDir := filepath.Join(t.TempDir(), "timeout-target")

	// Already-cancelled context forces immediate timeout
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	engine := New(workDir, "", 30*time.Second)
	err := engine.Clone(ctx, remote, "main")
	if err == nil {
		t.Fatal("Clone() with cancelled context should error")
	}
}

func TestPullWithoutClone(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "no-clone-target")
	engine := New(workDir, "", 30*time.Second)

	err := engine.Pull(t.Context(), "main")
	if err == nil {
		t.Fatal("Pull() without prior clone should error")
	}
}
