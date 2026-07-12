package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"ultraflow/internal/core"
	"ultraflow/internal/store"
)

func newTestSvc(t *testing.T) *core.Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return core.NewService(st)
}

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t.dev"},
		{"config", "user.name", "t"}, {"commit", "--allow-empty", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return repo
}

// TestPrepareWorkdirCreatesWorktree covers the M1 happy path: a task whose
// project is a git repo runs in an isolated worktree, and the path is recorded.
func TestPrepareWorkdirCreatesWorktree(t *testing.T) {
	svc := newTestSvc(t)
	repo := gitRepo(t)
	if _, err := svc.CreateProject("proj", repo); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, _ := svc.CreateTaskFull("t", "", "proj", "claude", "solo")

	wtRoot := filepath.Join(t.TempDir(), "worktrees")
	o := New(svc, "/shared", wtRoot, "http://mcp", 2)

	dir := o.prepareWorkdir(task)
	if filepath.Dir(dir) != wtRoot {
		t.Fatalf("expected worktree under %s, got %s", wtRoot, dir)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("worktree dir missing: %v", err)
	}
	// The path must be persisted on the task.
	got, _ := svc.GetTask(task.ID)
	if got.Worktree != dir {
		t.Fatalf("worktree not recorded on task: %q vs %q", got.Worktree, dir)
	}
}

// TestPrepareWorkdirFallsBack covers the degraded paths: no project → shared
// workdir; a non-git project folder → that folder directly (no worktree).
func TestPrepareWorkdirFallsBack(t *testing.T) {
	svc := newTestSvc(t)
	o := New(svc, "/shared", filepath.Join(t.TempDir(), "wt"), "http://mcp", 2)

	noProj, _ := svc.CreateTaskFull("t", "", "", "claude", "solo")
	if dir := o.prepareWorkdir(noProj); dir != "/shared" {
		t.Fatalf("no-project task should use shared workdir, got %s", dir)
	}

	plain := t.TempDir() // exists but not a git repo
	if _, err := svc.CreateProject("plain", plain); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, _ := svc.CreateTaskFull("t2", "", "plain", "claude", "solo")
	if dir := o.prepareWorkdir(task); dir != plain {
		t.Fatalf("non-git project should run in the folder directly, got %s", dir)
	}
	if got, _ := svc.GetTask(task.ID); got.Worktree != "" {
		t.Fatalf("no worktree should be recorded for a non-git project, got %q", got.Worktree)
	}
}
