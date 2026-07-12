package core

import (
	"testing"

	"ultraflow/internal/model"
)

// TestCompleteTurnSoloGoesToReview: a solo task (no run) finishing via finish_task
// lands in review — the unchanged single-agent behavior.
func TestCompleteTurnSoloGoesToReview(t *testing.T) {
	svc := newTestService(t)
	task, _ := svc.CreateTaskFull("t", "", "", "claude", "solo")
	_ = svc.UpdateStatus(task.ID, model.StatusRunning)

	if err := svc.CompleteTurn(task.ID, "did it", "# report"); err != nil {
		t.Fatalf("complete turn: %v", err)
	}
	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusReview {
		t.Fatalf("solo finish should be review, got %s", got.Status)
	}
	if _, ok := svc.Run(task.ID); ok {
		t.Fatal("a solo task should have no run")
	}
}

// TestCompleteTurnMidFlowMarksTurnDone: a mid-flow step finishing via finish_task
// must NOT flip the card to review — it only marks the step's turn done so the
// orchestrator advances; the task stays running.
func TestCompleteTurnMidFlowMarksTurnDone(t *testing.T) {
	svc := newTestService(t)
	task, _ := svc.CreateTaskFull("t", "", "", "claude", "plan-build-critic-gate")
	if err := svc.StartRun(task.ID, "plan-build-critic-gate", "plan"); err != nil {
		t.Fatalf("start run: %v", err)
	}
	svc.SetRunPhase(task.ID, model.RunActive)
	_ = svc.UpdateStatus(task.ID, model.StatusRunning)

	if err := svc.CompleteTurn(task.ID, "planned", "the plan"); err != nil {
		t.Fatalf("complete turn: %v", err)
	}
	got, _ := svc.GetTask(task.ID)
	if got.Status != model.StatusRunning {
		t.Fatalf("mid-flow finish must stay running, got %s", got.Status)
	}
	run, ok := svc.Run(task.ID)
	if !ok || !run.TurnDone {
		t.Fatalf("mid-flow finish must set turn_done (ok=%v turnDone=%v)", ok, run.TurnDone)
	}
}

// A conflict rebase or review revision happens after the original flow has
// completed. Its finish must return directly to review instead of treating the
// historical run as a live step and walking plan/build/critic again.
func TestCompleteTurnAfterCompletedFlowGoesStraightToReview(t *testing.T) {
	svc := newTestService(t)
	task, _ := svc.CreateTaskFull("t", "", "", "claude", "plan-build-critic-gate")
	if err := svc.StartRun(task.ID, "plan-build-critic-gate", "plan"); err != nil {
		t.Fatalf("start run: %v", err)
	}
	_ = svc.UpdateStatus(task.ID, model.StatusRunning)
	if err := svc.FinishFlow(task.ID); err != nil {
		t.Fatalf("finish flow: %v", err)
	}
	if !svc.QueueRebase(task.ID) {
		t.Fatal("queue post-review rebase")
	}
	_ = svc.UpdateStatus(task.ID, model.StatusRunning)

	if err := svc.CompleteTurn(task.ID, "rebased", "conflicts resolved"); err != nil {
		t.Fatalf("complete rebase turn: %v", err)
	}
	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusReview {
		t.Fatalf("completed-flow repair should return to review, got %s", got.Status)
	}
	run, ok := svc.Run(task.ID)
	if !ok || run.Phase != model.RunComplete || run.Cursor != "" {
		t.Fatalf("historical flow progress changed: %+v (ok=%v)", run, ok)
	}
}

// TestRunLifecycle exercises the run cursor + completed tracking the board reads:
// advancing records completions (deduped across a loop-back) and moves the cursor.
func TestRunLifecycle(t *testing.T) {
	svc := newTestService(t)
	task, _ := svc.CreateTaskFull("t", "", "", "claude", "plan-build-critic-gate")
	if err := svc.StartRun(task.ID, "plan-build-critic-gate", "plan"); err != nil {
		t.Fatalf("start run: %v", err)
	}
	svc.AdvanceRun(task.ID, "plan", "build")
	svc.AdvanceRun(task.ID, "build", "critic")
	svc.AdvanceRun(task.ID, "critic", "build") // a loop-back to build
	svc.AdvanceRun(task.ID, "build", "critic") // build already recorded — no dup

	run, _ := svc.Run(task.ID)
	if run.Cursor != "critic" {
		t.Fatalf("cursor should be critic, got %q", run.Cursor)
	}
	// plan, build, critic each recorded exactly once despite the loop.
	seen := map[string]int{}
	for _, s := range run.Completed {
		seen[s]++
	}
	for _, s := range []string{"plan", "build", "critic"} {
		if seen[s] != 1 {
			t.Fatalf("step %q recorded %d times, want 1 (%v)", s, seen[s], run.Completed)
		}
	}
}

// TestRunsProgressCaption checks the board-facing progress: the active step's
// index, total, gate-ness, and the composed caption.
func TestRunsProgressCaption(t *testing.T) {
	svc := newTestService(t)
	task, _ := svc.CreateTaskFull("t", "", "", "claude", "plan-build-critic-gate")
	_ = svc.StartRun(task.ID, "plan-build-critic-gate", "build")

	tasks, _ := svc.ListTasks()
	progress := svc.RunsProgress(tasks)
	p, ok := progress[task.ID]
	if !ok {
		t.Fatal("expected progress for the flow task")
	}
	if p.Index != 1 || p.Total != 4 || p.Gate {
		t.Fatalf("build progress unexpected: %+v", p)
	}
	if p.Caption != "Build · step 2 of 4 · critic + your gate next" {
		t.Fatalf("caption: %q", p.Caption)
	}
	if p.Agent != "claude" {
		t.Fatalf("sub-agent: %q", p.Agent)
	}
}
