// Package worktree gives each task its own git worktree off the project's repo,
// so parallel agents edit isolated checkouts instead of stomping one shared
// directory. This is the M1 unlock for safe concurrency (see spec/roadmap.md).
package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Manager creates and tears down per-task worktrees under a single root dir.
type Manager struct {
	root string // where task worktrees live, e.g. <data>/worktrees
}

// New returns a Manager rooting its worktrees at root. The root is resolved to
// an absolute path so a worktree's location is the SAME whether git creates it
// (with cwd at the project repo) or the daemon later chdirs into it (with its
// own cwd) — a relative root resolves against different directories and the
// checkout can't be found.
func New(root string) *Manager {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return &Manager{root: root}
}

// Worktree is a created checkout: an absolute path plus the branch it's on.
type Worktree struct {
	Path   string
	Branch string
}

// branchFor / pathFor derive deterministic names from the task id, so retrying a
// task reuses (and first prunes) the same branch/path instead of leaking new ones.
func branchFor(taskID string) string { return "ultraflow/" + taskID }
func (m *Manager) pathFor(taskID string) string {
	return filepath.Join(m.root, taskID)
}

// IsGitRepo reports whether repoPath is inside a git working tree. Callers fall
// back to running directly in the folder when it isn't one.
func IsGitRepo(repoPath string) bool {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// Create makes a fresh worktree for taskID branched off repoPath's current HEAD.
// It is idempotent: any stale worktree/branch from a previous run of the same
// task is removed first, so a retry starts clean.
func (m *Manager) Create(repoPath, taskID string) (Worktree, error) {
	branch := branchFor(taskID)
	path := m.pathFor(taskID)

	// Prune any leftovers from a prior attempt (ignore errors — they just mean
	// nothing was there to clean up).
	m.pruneTask(repoPath, taskID)

	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return Worktree{}, fmt.Errorf("worktree root: %w", err)
	}

	// -b creates the branch at HEAD and checks it out into path in one step.
	if out, err := run(repoPath, "worktree", "add", "-b", branch, path); err != nil {
		return Worktree{}, fmt.Errorf("git worktree add: %w: %s", err, out)
	}
	return Worktree{Path: path, Branch: branch}, nil
}

// Merge brings a task's work into repoPath's currently checked-out branch. The
// agent may have edited files in the worktree without committing, so any pending
// changes are committed onto the task branch first, then that branch is merged
// with a merge commit. On any failure (notably a conflict) the merge is aborted
// so the human's repo is left clean, and the git output is returned to explain
// why. Does NOT tear down the worktree — the caller does that only on success.
func (m *Manager) Merge(repoPath, taskID, message string) (string, error) {
	branch := branchFor(taskID)
	wtPath := m.pathFor(taskID)
	if message == "" {
		message = "Merge " + branch
	}

	// Commit whatever the agent left uncommitted in the worktree. `diff --cached
	// --quiet` exits non-zero exactly when there is something staged to commit.
	if _, err := run(wtPath, "add", "-A"); err == nil {
		if _, err := run(wtPath, "diff", "--cached", "--quiet"); err != nil {
			if out, cerr := run(wtPath, "commit", "-m", message); cerr != nil {
				return out, fmt.Errorf("committing worktree changes: %w: %s", cerr, out)
			}
		}
	}

	if out, err := run(repoPath, "merge", "--no-ff", "-m", message, branch); err != nil {
		_, _ = run(repoPath, "merge", "--abort") // leave the repo clean, not half-merged
		return out, fmt.Errorf("git merge: %w: %s", err, out)
	}
	return "merged " + branch, nil
}

// Remove tears down a task's worktree and deletes its branch. Safe to call even
// if nothing exists (e.g. the task never got a worktree).
func (m *Manager) Remove(repoPath, taskID string) error {
	m.pruneTask(repoPath, taskID)
	return nil
}

// pruneTask force-removes the worktree, its registration, the leftover dir, and
// the branch — each step best-effort so a partial prior state still gets cleaned.
func (m *Manager) pruneTask(repoPath, taskID string) {
	path := m.pathFor(taskID)
	_, _ = run(repoPath, "worktree", "remove", "--force", path)
	_, _ = run(repoPath, "worktree", "prune")
	_ = os.RemoveAll(path)
	_, _ = run(repoPath, "branch", "-D", branchFor(taskID))
}

func run(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoPath}, args...)...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
