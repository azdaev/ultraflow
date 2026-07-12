// Package worktree gives each task its own git worktree off the project's repo,
// so parallel agents edit isolated checkouts instead of stomping one shared
// directory. This is the M1 unlock for safe concurrency (see spec/roadmap.md).
package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

	if err := commitPending(wtPath, message); err != nil {
		return err.Error(), err
	}

	if out, err := run(repoPath, "merge", "--no-ff", "-m", message, branch); err != nil {
		_, _ = run(repoPath, "merge", "--abort") // leave the repo clean, not half-merged
		return out, fmt.Errorf("git merge: %w: %s", err, out)
	}
	return "merged " + branch, nil
}

// baseBranch is the branch a task forks from and merges into: the repo's
// currently checked-out branch (what Merge lands onto). Empty when the repo is
// in a detached HEAD, in which case there's no named "main" to measure freshness
// against or rebase onto.
func baseBranch(repoPath string) string {
	if b, err := run(repoPath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && b != "" && b != "HEAD" {
		return b
	}
	return ""
}

// commitPending stages and commits any uncommitted work an agent left in the
// worktree, so an operation that acts on commits (merge, rebase) sees the latest
// edits. A no-op when the tree is clean. `diff --cached --quiet` exits non-zero
// exactly when there is something staged to commit.
func commitPending(wtPath, message string) error {
	// `git add -A` succeeds (with nothing to do) on a clean tree, so a non-nil error
	// here is a genuine staging failure — surface it rather than proceeding to a
	// merge/rebase that would silently act on the agent's uncommitted work being lost.
	if out, err := run(wtPath, "add", "-A"); err != nil {
		return fmt.Errorf("staging worktree changes: %w: %s", err, out)
	}
	if _, err := run(wtPath, "diff", "--cached", "--quiet"); err != nil {
		if out, cerr := run(wtPath, "commit", "-m", message); cerr != nil {
			return fmt.Errorf("committing worktree changes: %w: %s", cerr, out)
		}
	}
	return nil
}

// Freshness reports how many commits the task's branch is behind the project's
// base branch — commits that landed on base (the branch this task will merge
// into) since the task forked and that the task doesn't have yet. behind == 0
// means the branch is up to date; base is the branch it measured against ("" when
// the repo is detached, in which case there is nothing to be behind).
//
// It measures against the LOCAL base branch on purpose: that is the exact ref
// Merge lands onto, so "behind" reflects what an auto-rebase would actually
// replay onto and what will really change when the work merges — not a remote we
// don't merge into.
func (m *Manager) Freshness(repoPath, taskID string) (behind int, base string, err error) {
	base = baseBranch(repoPath)
	if base == "" {
		return 0, "", nil
	}
	branch := branchFor(taskID)
	out, err := run(repoPath, "rev-list", "--count", branch+".."+base)
	if err != nil {
		return 0, base, fmt.Errorf("counting commits behind %s: %w: %s", base, err, out)
	}
	behind, _ = strconv.Atoi(strings.TrimSpace(out))
	return behind, base, nil
}

// Rebase replays the task's branch onto the latest base branch so the work sits
// on top of everything that landed since it forked — then a merge reflects what
// will actually land (spec.md "Failure self-heals" → stale branch). Any pending
// agent edits are committed first (same as Merge).
//
// Returns:
//   - conflicted == true when the replay stops on conflicts git can't resolve
//     mechanically. The rebase is ABORTED (the worktree is left clean, on the
//     original branch tip) so the caller can hand the whole rebase to the agent's
//     self-heal instead of leaving a half-finished rebase wedged in the checkout.
//   - conflicted == false, err == nil on a clean replay, when already up to date,
//     or when the base is detached (nothing to rebase onto).
//
// A non-conflict failure is returned as err (the rebase, if any, is aborted).
func (m *Manager) Rebase(repoPath, taskID, message string) (conflicted bool, out string, err error) {
	base := baseBranch(repoPath)
	if base == "" {
		return false, "", nil
	}
	wtPath := m.pathFor(taskID)
	if cerr := commitPending(wtPath, message); cerr != nil {
		return false, cerr.Error(), cerr
	}

	out, err = run(wtPath, "rebase", base)
	if err == nil {
		return false, out, nil
	}
	// The replay stopped. Unmerged paths mean genuine conflicts (agent-resolvable);
	// anything else is a real error. Either way, abort so the worktree is left clean.
	conflicted = hasConflicts(wtPath)
	_, _ = run(wtPath, "rebase", "--abort")
	if conflicted {
		return true, out, nil
	}
	return false, out, fmt.Errorf("git rebase onto %s: %w: %s", base, err, out)
}

// hasConflicts reports whether the worktree currently has unmerged (conflicted)
// paths — true while a rebase/merge is stopped on a conflict.
func hasConflicts(wtPath string) bool {
	out, err := run(wtPath, "diff", "--name-only", "--diff-filter=U")
	return err == nil && strings.TrimSpace(out) != ""
}

// DiffFile is one changed path in a task's worktree with its line magnitude.
type DiffFile struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// DiffResult is a task's full change set vs the branch it forked from: the
// magnitude the board leads with (+Added −Removed across Files) plus the raw
// unified patch (secondary — the human rarely reads it). Patch is capped, with
// Truncated set when it was cut, so a huge diff never bloats the response.
type DiffResult struct {
	Base      string     `json:"base"`
	Added     int        `json:"added"`
	Removed   int        `json:"removed"`
	Files     []DiffFile `json:"files"`
	Patch     string     `json:"patch"`
	Truncated bool       `json:"truncated"`
}

// patchCap bounds the unified patch returned to the board (~200 KB) so a massive
// change set doesn't blow up the review payload; the magnitude counts are exact.
const patchCap = 200 * 1024

// Diff computes what a task changed in its worktree, relative to the fork point
// with the repo's checked-out branch — so it reflects the actual work regardless
// of whether the agent committed. Uncommitted edits AND new (untracked) files are
// included: a non-destructive intent-to-add surfaces new files in the diff
// without staging their content. Used by the review diff viewer.
func (m *Manager) Diff(repoPath, taskID string) (DiffResult, error) {
	wtPath := m.pathFor(taskID)
	if st, err := os.Stat(wtPath); err != nil || !st.IsDir() {
		return DiffResult{}, fmt.Errorf("this task has no worktree to diff")
	}

	// The base the branch will merge into is the repo's checked-out branch; fall
	// back to a raw HEAD ref if it's detached.
	base := "HEAD"
	if b, err := run(repoPath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && b != "" && b != "HEAD" {
		base = b
	}
	// The fork point (merge-base) is the right comparison even if the agent made
	// commits on the branch or the base moved on since; fall back to base itself.
	fork := base
	if mb, err := run(wtPath, "merge-base", base, "HEAD"); err == nil && mb != "" {
		fork = mb
	}

	// Intent-to-add untracked files so `git diff` shows them as additions. `-N`
	// records only the path (empty blob), not content, so it's reversible and
	// doesn't disturb a later merge (which stages everything anyway).
	_, _ = run(wtPath, "add", "-A", "-N")

	res := DiffResult{Base: fork, Files: []DiffFile{}}
	if numstat, err := run(wtPath, "diff", "--numstat", fork); err == nil && numstat != "" {
		for _, line := range strings.Split(numstat, "\n") {
			cols := strings.SplitN(line, "\t", 3)
			if len(cols) != 3 {
				continue
			}
			// Binary files report "-" for both counts; treat as zero-magnitude.
			added, _ := strconv.Atoi(cols[0])
			removed, _ := strconv.Atoi(cols[1])
			res.Added += added
			res.Removed += removed
			res.Files = append(res.Files, DiffFile{Path: cols[2], Added: added, Removed: removed})
		}
	}

	patch, err := run(wtPath, "diff", fork)
	if err != nil {
		return DiffResult{}, fmt.Errorf("git diff: %w: %s", err, patch)
	}
	if len(patch) > patchCap {
		patch = patch[:patchCap]
		res.Truncated = true
	}
	res.Patch = patch
	return res, nil
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
