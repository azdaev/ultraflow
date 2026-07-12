package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
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
