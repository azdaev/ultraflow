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

// New returns a Manager rooting its worktrees at root.
func New(root string) *Manager { return &Manager{root: root} }

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
