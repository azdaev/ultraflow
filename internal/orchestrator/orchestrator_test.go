package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"ultraflow/internal/core"
	"ultraflow/internal/model"
	"ultraflow/internal/store"
	"ultraflow/internal/terminal"
	"ultraflow/internal/worktree"
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

func newTestOrch(t *testing.T, limit int) *Orchestrator {
	t.Helper()
	return New(newTestSvc(t), "/shared", worktree.New(filepath.Join(t.TempDir(), "wt")),
		terminal.NewManager(), "http://mcp", limit)
}

// TestSemaphoreRaiseWakesQueued verifies the core resizable-semaphore contract:
// with the limit at 1, a second acquirer blocks; raising the limit must let it
// proceed immediately without any slot being released.
func TestSemaphoreRaiseWakesQueued(t *testing.T) {
	o := newTestOrch(t, 1)

	o.acquire() // hold the only slot; don't release it

	started := make(chan struct{})
	go func() {
		o.acquire() // blocks: active(1) >= limit(1)
		close(started)
	}()

	select {
	case <-started:
		t.Fatal("second acquire should block while limit is 1 and a slot is held")
	case <-time.After(50 * time.Millisecond):
	}

	o.SetLimit(2) // raising the ceiling must wake the blocked acquirer

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("raising the limit should let the queued acquirer proceed immediately")
	}
}

// TestSemaphoreLowerDoesNotEvict verifies that lowering the limit below the
// number of active agents doesn't disturb them (no panic, active is untouched);
// it only prevents new acquisitions, which is checked by the blocking behaviour.
func TestSemaphoreLowerDoesNotEvict(t *testing.T) {
	o := newTestOrch(t, 3)
	o.acquire()
	o.acquire() // 2 active under limit 3

	o.SetLimit(1) // below active; running ones must be untouched
	if o.Limit() != 1 {
		t.Fatalf("limit should be 1, got %d", o.Limit())
	}

	// A fresh acquire must now block (active 2 >= limit 1).
	got := make(chan struct{})
	go func() { o.acquire(); close(got) }()
	select {
	case <-got:
		t.Fatal("acquire should block when active exceeds a lowered limit")
	case <-time.After(50 * time.Millisecond):
	}

	// Releasing down to 0 active still leaves us at limit 1, so exactly one more
	// acquire may now proceed (the blocked one above).
	o.release()
	o.release()
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("a slot should free up once active drops below the lowered limit")
	}
}

// TestSetLimitClamps guards the floor: SetLimit never drops below 1.
func TestSetLimitClamps(t *testing.T) {
	o := newTestOrch(t, 3)
	o.SetLimit(0)
	if o.Limit() != 1 {
		t.Fatalf("SetLimit(0) should clamp to 1, got %d", o.Limit())
	}
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, why string, cond func() bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", why)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// TestWatchIdleClosesFinishedTurn: an agent that ends its turn at its prompt
// without finish_task (a silent, still-alive process) is sent to review and its
// session killed, so the slot frees — the whole point of this change.
func TestWatchIdleClosesFinishedTurn(t *testing.T) {
	svc := newTestSvc(t)
	o := New(svc, "/shared", worktree.New(filepath.Join(t.TempDir(), "wt")), terminal.NewManager(), "http://mcp", 1)

	task, _ := svc.CreateTaskFull("t", "", "", "claude", "solo")
	if err := svc.UpdateStatus(task.ID, model.StatusRunning); err != nil {
		t.Fatalf("to running: %v", err)
	}

	// `cat` stays alive and silent — a stand-in for a TUI idling at its prompt.
	sess, err := terminal.NewManager().Start(task.ID, exec.Command("cat"))
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	defer sess.Close()

	go o.watchIdle(sess, task.ID, 30*time.Millisecond, 5*time.Millisecond)

	waitFor(t, "task to land in review", func() bool {
		got, _ := svc.GetTask(task.ID)
		return got.Status == model.StatusReview
	})
	waitFor(t, "the idle session to be closed", func() bool {
		select {
		case <-sess.Done():
			return true
		default:
			return false
		}
	})
}

// TestWatchIdleLeavesAskHumanParked: a task parked on an open human request is
// SUPPOSED to idle at its prompt (the durable ask_human wait). The watcher must
// not touch it — status stays needs_human and the session stays alive.
func TestWatchIdleLeavesAskHumanParked(t *testing.T) {
	svc := newTestSvc(t)
	o := New(svc, "/shared", worktree.New(filepath.Join(t.TempDir(), "wt")), terminal.NewManager(), "http://mcp", 1)

	task, _ := svc.CreateTaskFull("t", "", "", "claude", "solo")
	// AskHuman moves the task to needs_human — the parked state the watcher excludes.
	if _, err := svc.AskHuman(task.ID, "which way?", nil, ""); err != nil {
		t.Fatalf("ask_human: %v", err)
	}

	sess, err := terminal.NewManager().Start(task.ID, exec.Command("cat"))
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	defer sess.Close()

	go o.watchIdle(sess, task.ID, 30*time.Millisecond, 5*time.Millisecond)

	// Give the watcher well past its timeout to (wrongly) act, then assert it didn't.
	time.Sleep(200 * time.Millisecond)
	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusNeedsHuman {
		t.Fatalf("parked task should stay needs_human, got %s", got.Status)
	}
	select {
	case <-sess.Done():
		t.Fatal("watcher killed a session parked on ask_human")
	default:
	}
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
	o := New(svc, "/shared", worktree.New(wtRoot), terminal.NewManager(), "http://mcp", 2)

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
	o := New(svc, "/shared", worktree.New(filepath.Join(t.TempDir(), "wt")), terminal.NewManager(), "http://mcp", 2)

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
