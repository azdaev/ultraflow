package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo makes a throwaway git repo with one commit and returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t.dev"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return repo
}

// TestCreateUsesAbsolutePath guards the bug where a relative worktree root made
// git and the daemon resolve the checkout against different cwds: the created
// worktree path must be absolute so `cmd.Dir = path` works from any cwd.
// commitFile writes name in repo's working tree and commits it, advancing the
// repo's checked-out (base) branch — used to make a task's branch fall behind.
func commitFile(t *testing.T, repo, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", msg}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// TestFreshness verifies the behind-main count: 0 right after fork, then N after
// the base branch advances by N commits the task branch doesn't have.
func TestFreshness(t *testing.T) {
	repo := initRepo(t)
	m := New(filepath.Join(t.TempDir(), "worktrees"))
	if _, err := m.Create(repo, "fresh"); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer m.Remove(repo, "fresh")

	if behind, base, err := m.Freshness(repo, "fresh"); err != nil || behind != 0 {
		t.Fatalf("just-forked branch: behind=%d base=%q err=%v; want behind=0", behind, base, err)
	}

	// Advance main by two commits the worktree branch doesn't have.
	commitFile(t, repo, "a.txt", "a", "add a")
	commitFile(t, repo, "b.txt", "b", "add b")

	behind, base, err := m.Freshness(repo, "fresh")
	if err != nil {
		t.Fatalf("freshness: %v", err)
	}
	if behind != 2 {
		t.Fatalf("behind = %d; want 2", behind)
	}
	if base == "" {
		t.Fatal("base branch should be named, not empty")
	}
}

// TestRebaseClean verifies a stale branch replays onto the advanced base without
// conflict, picking up the new base commits, and then merges cleanly.
func TestRebaseClean(t *testing.T) {
	repo := initRepo(t)
	m := New(filepath.Join(t.TempDir(), "worktrees"))
	w, err := m.Create(repo, "clean")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer m.Remove(repo, "clean")

	// Agent work in the worktree (uncommitted) on one file...
	if err := os.WriteFile(filepath.Join(w.Path, "work.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatalf("write work: %v", err)
	}
	// ...while main advances on a DIFFERENT file — no conflict.
	commitFile(t, repo, "upstream.txt", "theirs", "upstream change")

	conflicted, out, err := m.Rebase(repo, "clean", "clean rebase")
	if err != nil {
		t.Fatalf("rebase: %v (%s)", err, out)
	}
	if conflicted {
		t.Fatalf("expected a clean rebase, got a conflict: %s", out)
	}
	// The worktree branch now sits on top of main's new commit.
	if _, err := os.Stat(filepath.Join(w.Path, "upstream.txt")); err != nil {
		t.Fatalf("rebased branch missing the upstream commit: %v", err)
	}
	// And it lands cleanly.
	if _, err := m.Merge(repo, "clean", "land it"); err != nil {
		t.Fatalf("merge after rebase: %v", err)
	}
}

// TestCreateResumePreservesCommits is the regression guard for the data-loss bug:
// re-Creating a task's worktree (a resume after a daemon restart / in-flight
// recovery) must REUSE the existing branch and keep every commit the agent already
// made — not prune the branch and re-create it at main's HEAD, silently wiping the
// work. It also confirms a genuinely new task still branches fresh off HEAD.
func TestCreateResumePreservesCommits(t *testing.T) {
	repo := initRepo(t)
	m := New(filepath.Join(t.TempDir(), "worktrees"))

	// First run: fresh worktree, then the agent commits its work on the task branch.
	w, err := m.Create(repo, "resume")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	commitFile(t, w.Path, "feature.txt", "the agent's work", "agent work")

	// Main advances independently (as it does while the task runs).
	commitFile(t, repo, "unrelated.txt", "x", "main moves on")

	// Second run for the SAME task — this is what a restart/recovery triggers.
	w2, err := m.Create(repo, "resume")
	if err != nil {
		t.Fatalf("resume create: %v", err)
	}
	// The agent's commit must still be checked out — the bug wiped it back to main.
	if _, err := os.Stat(filepath.Join(w2.Path, "feature.txt")); err != nil {
		t.Fatalf("resume lost the agent's committed work (branch was reset to main): %v", err)
	}

	// A different, never-seen task must still branch fresh (no leftover files).
	fresh, err := m.Create(repo, "brand-new")
	if err != nil {
		t.Fatalf("fresh create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fresh.Path, "feature.txt")); err == nil {
		t.Fatalf("a brand-new task should not carry another task's files")
	}
}

// TestRebaseConflict verifies that when the branch and main both change the SAME
// lines, the rebase reports a conflict and leaves the worktree CLEAN (aborted, no
// half-finished rebase) so the caller can hand it to the agent's self-heal.
func TestRebaseConflict(t *testing.T) {
	repo := initRepo(t)
	// Seed a shared file both sides will edit.
	commitFile(t, repo, "shared.txt", "base\n", "seed shared")

	m := New(filepath.Join(t.TempDir(), "worktrees"))
	w, err := m.Create(repo, "clash")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer m.Remove(repo, "clash")

	// The agent edits shared.txt in the worktree...
	if err := os.WriteFile(filepath.Join(w.Path, "shared.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatalf("write worktree edit: %v", err)
	}
	// ...and main edits the SAME line differently.
	commitFile(t, repo, "shared.txt", "theirs\n", "upstream edit")

	conflicted, _, err := m.Rebase(repo, "clash", "conflicting rebase")
	if err != nil {
		t.Fatalf("rebase returned a hard error instead of a conflict signal: %v", err)
	}
	if !conflicted {
		t.Fatal("expected conflicted=true for a same-line clash")
	}
	// Worktree must be clean (rebase aborted): no unmerged paths left wedged.
	if out, _ := run(w.Path, "diff", "--name-only", "--diff-filter=U"); strings.TrimSpace(out) != "" {
		t.Fatalf("worktree left with unmerged paths after a conflict: %s", out)
	}
}

func TestCreateUsesAbsolutePath(t *testing.T) {
	repo := initRepo(t)
	// Deliberately construct the manager with a RELATIVE root.
	m := New("relative-worktrees-root")
	t.Cleanup(func() { os.RemoveAll("relative-worktrees-root") })

	w, err := m.Create(repo, "abs")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer m.Remove(repo, "abs")
	if !filepath.IsAbs(w.Path) {
		t.Fatalf("worktree path must be absolute, got %q", w.Path)
	}
	if st, err := os.Stat(w.Path); err != nil || !st.IsDir() {
		t.Fatalf("worktree dir not found at %q: %v", w.Path, err)
	}
}

func TestIsGitRepo(t *testing.T) {
	repo := initRepo(t)
	if !IsGitRepo(repo) {
		t.Fatal("expected repo to be recognized as a git repo")
	}
	if IsGitRepo(t.TempDir()) {
		t.Fatal("expected a bare temp dir NOT to be a git repo")
	}
}

func TestCreateAndRemove(t *testing.T) {
	repo := initRepo(t)
	m := New(filepath.Join(t.TempDir(), "worktrees"))

	w, err := m.Create(repo, "task123")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.Branch != "ultraflow/task123" {
		t.Fatalf("unexpected branch %q", w.Branch)
	}
	if st, err := os.Stat(w.Path); err != nil || !st.IsDir() {
		t.Fatalf("worktree path not a dir: %v", err)
	}
	// It must be a real, independent checkout: a file written here doesn't touch
	// the origin repo's working tree.
	if err := os.WriteFile(filepath.Join(w.Path, "scratch.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write in worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "scratch.txt")); !os.IsNotExist(err) {
		t.Fatal("file leaked into the origin repo — worktree not isolated")
	}

	m.Remove(repo, "task123")
	if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
		t.Fatal("worktree dir still present after Remove")
	}
}

// TestMerge verifies the review→done merge: work left in the worktree (even
// uncommitted) lands on the origin repo's checked-out branch.
func TestMerge(t *testing.T) {
	repo := initRepo(t)
	m := New(filepath.Join(t.TempDir(), "worktrees"))

	w, err := m.Create(repo, "feat")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Agent-style edit in the worktree, deliberately NOT committed.
	if err := os.WriteFile(filepath.Join(w.Path, "feature.txt"), []byte("done"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := m.Merge(repo, "feat", "add feature"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	// The file must now exist in the origin repo's working tree.
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Fatalf("merged file missing from origin: %v", err)
	}

	// Teardown after a successful merge removes the worktree and branch.
	m.Remove(repo, "feat")
	if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
		t.Fatal("worktree dir still present after Remove")
	}
}

// TestDiff verifies the review diff reflects the agent's work regardless of
// whether it committed: it must include both an uncommitted edit to a tracked
// file AND a brand-new (untracked) file, with correct magnitude counts.
func TestDiff(t *testing.T) {
	repo := initRepo(t)
	// Seed a tracked file on the base branch so we can test a modification.
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "seed"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	m := New(filepath.Join(t.TempDir(), "worktrees"))
	w, err := m.Create(repo, "difftask")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer m.Remove(repo, "difftask")

	// Agent-style work, deliberately NOT committed: modify a tracked file and add
	// a new untracked one.
	if err := os.WriteFile(filepath.Join(w.Path, "tracked.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("modify: %v", err)
	}
	if err := os.WriteFile(filepath.Join(w.Path, "new.txt"), []byte("fresh\n"), 0o644); err != nil {
		t.Fatalf("new file: %v", err)
	}

	d, err := m.Diff(repo, "difftask")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	// +1 for the added line in tracked.txt, +1 for the new file's single line.
	if d.Added != 2 || d.Removed != 0 {
		t.Fatalf("magnitude = +%d −%d; want +2 −0", d.Added, d.Removed)
	}
	if len(d.Files) != 2 {
		t.Fatalf("expected 2 changed files, got %d: %+v", len(d.Files), d.Files)
	}
	// The new (untracked) file must appear — the key property (git diff alone
	// would miss it without the intent-to-add).
	if !strings.Contains(d.Patch, "new.txt") || !strings.Contains(d.Patch, "+fresh") {
		t.Fatalf("patch missing the new untracked file:\n%s", d.Patch)
	}
	if !strings.Contains(d.Patch, "+two") {
		t.Fatalf("patch missing the tracked-file edit:\n%s", d.Patch)
	}
}

// TestCreateIdempotentForRetry verifies Create cleans up a prior attempt so a
// retried task (same id → same branch) starts fresh instead of erroring on a
// branch/worktree that already exists.
func TestCreateIdempotentForRetry(t *testing.T) {
	repo := initRepo(t)
	m := New(filepath.Join(t.TempDir(), "worktrees"))

	first, err := m.Create(repo, "dup")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(first.Path, "old.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	second, err := m.Create(repo, "dup")
	if err != nil {
		t.Fatalf("second create (retry) should succeed, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(second.Path, "old.txt")); !os.IsNotExist(err) {
		t.Fatal("retry worktree still holds the previous attempt's file")
	}
}
