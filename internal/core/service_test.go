package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"ultraflow/internal/model"
	"ultraflow/internal/store"
	"ultraflow/internal/worktree"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return NewService(st)
}

func TestCreateTaskDefaults(t *testing.T) {
	svc := newTestService(t)
	task, err := svc.CreateTask("build the thing", "details", "proj")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if task.Agent != "claude" || task.Flow != "solo" {
		t.Fatalf("expected defaults claude/solo, got %s/%s", task.Agent, task.Flow)
	}
	if task.Status != model.StatusBacklog {
		t.Fatalf("expected backlog, got %s", task.Status)
	}

	got, err := svc.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "build the thing" {
		t.Fatalf("roundtrip title mismatch: %q", got.Title)
	}
}

// TestCreateTaskNormalizesUnimplemented verifies a not-yet-wired agent/flow is
// collapsed to what the orchestrator actually runs, so a card can never claim a
// task used an adapter or multi-step flow that doesn't exist yet.
func TestCreateTaskNormalizesUnimplemented(t *testing.T) {
	svc := newTestService(t)
	task, err := svc.CreateTaskFull("t", "", "proj", "codex", "tdd")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if task.Agent != "claude" || task.Flow != "solo" {
		t.Fatalf("expected codex/tdd normalized to claude/solo, got %s/%s", task.Agent, task.Flow)
	}
}

// TestRecoverInFlight covers startup reconciliation: tasks left mid-run by a
// previous daemon exit (no agent goroutine behind them) are requeued to backlog
// and their orphaned pending human requests are retired, so nothing is stranded.
func TestRecoverInFlight(t *testing.T) {
	svc := newTestService(t)
	running, _ := svc.CreateTask("running", "", "")
	svc.UpdateStatus(running.ID, model.StatusRunning)
	queued, _ := svc.CreateTask("queued", "", "")
	svc.UpdateStatus(queued.ID, model.StatusQueued)
	parked, _ := svc.CreateTask("parked", "", "")
	svc.UpdateStatus(parked.ID, model.StatusNeedsHuman)
	// A pending request whose asking agent is (conceptually) already dead.
	svc.store.CreateHumanRequest(model.HumanRequest{
		ID: NewID(), TaskID: parked.ID, Question: "q", Status: "pending", CreatedAt: time.Now(),
	})
	done, _ := svc.CreateTask("done", "", "")
	svc.UpdateStatus(done.ID, model.StatusDone)

	n, err := svc.RecoverInFlight()
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 in-flight tasks requeued, got %d", n)
	}
	for _, id := range []string{running.ID, queued.ID, parked.ID} {
		if got, _ := svc.GetTask(id); got.Status != model.StatusBacklog {
			t.Fatalf("task %s should be requeued to backlog, got %s", id, got.Status)
		}
	}
	// A terminal task is left alone.
	if got, _ := svc.GetTask(done.ID); got.Status != model.StatusDone {
		t.Fatalf("done task should be untouched, got %s", got.Status)
	}
	// The orphaned checkpoint must be gone from the rail.
	if reqs, _ := svc.PendingRequests(); len(reqs) != 0 {
		t.Fatalf("expected orphaned requests retired, got %d pending", len(reqs))
	}
}

// TestAskHumanBlocksThenReturns exercises the core loop: AskHuman parks the
// caller until AnswerHuman is invoked with the human's choice.
func TestAskHumanBlocksThenReturns(t *testing.T) {
	svc := newTestService(t)
	task, _ := svc.CreateTask("t", "", "")

	answered := make(chan string, 1)
	go func() {
		ans, err := svc.AskHuman(context.Background(), task.ID, "Merge to main?", []string{"yes", "no"}, "diff: +10 -2")
		if err != nil {
			t.Errorf("ask_human: %v", err)
		}
		answered <- ans
	}()

	// Wait for the request to be registered as pending.
	var reqID string
	deadline := time.After(2 * time.Second)
	for reqID == "" {
		select {
		case <-deadline:
			t.Fatal("request never became pending")
		default:
		}
		reqs, _ := svc.PendingRequests()
		if len(reqs) == 1 {
			reqID = reqs[0].ID
			if reqs[0].Question != "Merge to main?" {
				t.Fatalf("bad question: %q", reqs[0].Question)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The task should have flipped to needs_human while blocked.
	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusNeedsHuman {
		t.Fatalf("expected needs_human, got %s", got.Status)
	}

	if err := svc.AnswerHuman(reqID, "yes"); err != nil {
		t.Fatalf("answer: %v", err)
	}

	select {
	case ans := <-answered:
		if ans != "yes" {
			t.Fatalf("expected answer 'yes', got %q", ans)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AskHuman never returned after answer")
	}

	// Task should be back to running and the request no longer pending.
	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusRunning {
		t.Fatalf("expected running after answer, got %s", got.Status)
	}
	if reqs, _ := svc.PendingRequests(); len(reqs) != 0 {
		t.Fatalf("expected no pending requests, got %d", len(reqs))
	}
}

func TestAskHumanContextCancel(t *testing.T) {
	svc := newTestService(t)
	task, _ := svc.CreateTask("t", "", "")

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := svc.AskHuman(ctx, task.ID, "q", nil, "")
		errc <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("expected context error, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AskHuman did not unblock on context cancel")
	}

	// M-3: a dead agent's request must be retired, not left dangling in the rail,
	// and the task must fail (not stay stuck in needs_human).
	if reqs, _ := svc.PendingRequests(); len(reqs) != 0 {
		t.Fatalf("expected the cancelled request to leave the rail, got %d pending", len(reqs))
	}
	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusFailed {
		t.Fatalf("expected failed after agent death, got %s", got.Status)
	}
}

// TestAnswerHumanDoubleAnswerNoop covers M-4: a second (or unknown) answer must
// be a harmless no-op that neither errors, hangs, nor re-runs side effects.
func TestAnswerHumanDoubleAnswerNoop(t *testing.T) {
	svc := newTestService(t)
	task, _ := svc.CreateTask("t", "", "")

	answered := make(chan string, 1)
	go func() {
		ans, _ := svc.AskHuman(context.Background(), task.ID, "q", []string{"yes"}, "")
		answered <- ans
	}()

	var reqID string
	deadline := time.After(2 * time.Second)
	for reqID == "" {
		select {
		case <-deadline:
			t.Fatal("request never became pending")
		default:
		}
		if reqs, _ := svc.PendingRequests(); len(reqs) == 1 {
			reqID = reqs[0].ID
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := svc.AnswerHuman(reqID, "yes"); err != nil {
		t.Fatalf("first answer: %v", err)
	}
	<-answered

	// Second answer to the same (now answered) request: no-op, no hang, no error.
	done := make(chan error, 1)
	go func() { done <- svc.AnswerHuman(reqID, "no") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("duplicate answer should be a no-op, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("duplicate AnswerHuman hung")
	}

	// The original answer must stand; the task stays running (not re-flipped).
	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusRunning {
		t.Fatalf("expected running to persist, got %s", got.Status)
	}

	// Answering an unknown id is also a clean no-op.
	if err := svc.AnswerHuman("does-not-exist", "x"); err != nil {
		t.Fatalf("unknown-id answer should be a no-op, got %v", err)
	}
}

// TestMergeTask covers the review→done merge: a reviewed task's worktree work is
// merged into the project repo, the worktree is torn down, and the task finishes.
func TestMergeTask(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// A project repo with one commit.
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t.dev"},
		{"config", "user.name", "t"}, {"commit", "--allow-empty", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	svc := newTestService(t)
	wt := worktree.New(filepath.Join(t.TempDir(), "wt"))
	svc.UseWorktrees(wt)
	if _, err := svc.CreateProject("proj", repo); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, _ := svc.CreateTaskFull("ship it", "", "proj", "claude", "solo")

	// Simulate the orchestrator: give the task a worktree and put it in review
	// with some agent work left in the checkout.
	w, err := wt.Create(repo, task.ID)
	if err != nil {
		t.Fatalf("worktree create: %v", err)
	}
	svc.SetWorktree(task.ID, w.Path)
	if err := os.WriteFile(filepath.Join(w.Path, "shipped.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	svc.UpdateStatus(task.ID, model.StatusReview)

	if err := svc.MergeTask(task.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusDone {
		t.Fatalf("expected done after merge, got %s", got.Status)
	}
	// Work landed in the origin repo, and the worktree is gone.
	if _, err := os.Stat(filepath.Join(repo, "shipped.txt")); err != nil {
		t.Fatalf("merged work missing from repo: %v", err)
	}
	if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
		t.Fatal("worktree should be torn down after a successful merge")
	}
}

// TestMergeTaskRejectsNonReview guards the precondition: only a reviewed task
// can be merged.
func TestMergeTaskRejectsNonReview(t *testing.T) {
	svc := newTestService(t)
	svc.UseWorktrees(worktree.New(filepath.Join(t.TempDir(), "wt")))
	task, _ := svc.CreateTask("t", "", "")
	if err := svc.MergeTask(task.ID); err == nil {
		t.Fatal("expected merge of a backlog task to be rejected")
	}
}

func TestRetryTaskRequeues(t *testing.T) {
	svc := newTestService(t)
	task, _ := svc.CreateTask("t", "", "")
	svc.UpdateStatus(task.ID, model.StatusFailed)

	if err := svc.RetryTask(task.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusBacklog {
		t.Fatalf("expected backlog after retry, got %s", got.Status)
	}
}

func TestLatestActivity(t *testing.T) {
	svc := newTestService(t)
	task, _ := svc.CreateTask("t", "", "")
	svc.AppendTaskEvent(task.ID, "tool", "Edit main.go")
	svc.AppendTaskEvent(task.ID, "tool", "Bash go test ./...")

	act, err := svc.LatestActivity()
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if act[task.ID] != "Bash go test ./..." {
		t.Fatalf("expected latest activity, got %q", act[task.ID])
	}
}
