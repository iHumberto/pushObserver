// Package git handles Git operations: clone, pull, diff, commit history.
//
// Uses os/exec with git CLI. SSH keys via GIT_SSH_COMMAND env var.
// Repository-level locking prevents concurrent operations on the same repo.
package git

// TODO: GitEngine struct, New(), Clone(), Pull(), ChangedFiles(), LastCommit(), CurrentBranch()
