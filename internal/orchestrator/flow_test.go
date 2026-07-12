package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"ultraflow/internal/agent"
	"ultraflow/internal/core"
	"ultraflow/internal/devserver"
	"ultraflow/internal/model"
	"ultraflow/internal/port"
	"ultraflow/internal/terminal"
	"ultraflow/internal/worktree"
)

// fakeFlowAgent is an interactiveAgent whose every turn exits cleanly (exit 0)
// without calling finish_task — which the flow runner treats as a completed turn,
// so each work step advances. It records the dir every step ran in, so a test can
// assert all steps of a flow shared one worktree.
type fakeFlowAgent struct {
	mu   sync.Mutex
	dirs []string
}

func (a *fakeFlowAgent) Name() string { return "claude" }

// Run satisfies agent.Agent (the headless path); the flow runner only ever uses
// the interactive Command/ResumeCommand, so this is an unused no-op here.
func (a *fakeFlowAgent) Run(ctx context.Context, dir, prompt string, out chan<- agent.Event) error {
	return nil
}

func (a *fakeFlowAgent) record(dir string) {
	a.mu.Lock()
	a.dirs = append(a.dirs, dir)
	a.mu.Unlock()
}

func (a *fakeFlowAgent) turns() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.dirs...)
}

func (a *fakeFlowAgent) Command(ctx context.Context, dir, prompt string) (*exec.Cmd, func(), error) {
	a.record(dir)
	return exec.Command("true"), func() {}, nil
}

func (a *fakeFlowAgent) ResumeCommand(ctx context.Context, dir, prompt string) (*exec.Cmd, func(), error) {
	a.record(dir)
	return exec.Command("true"), func() {}, nil
}

// wireFlowOrch builds an orchestrator whose only agent is a fakeFlowAgent, with the
// service fully wired (worktrees, terminal, reengager) so the gate answer path
// exercises the real AnswerHuman → Reengage → resumeGate route.
func wireFlowOrch(t *testing.T) (*core.Service, *Orchestrator, *fakeFlowAgent) {
	t.Helper()
	svc := newTestSvc(t)
	term := terminal.NewManager()
	wt := worktree.New(filepath.Join(t.TempDir(), "wt"))
	o := New(svc, "/shared", wt, term, port.NewAllocator(), devserver.NewManager(), "http://mcp", 2)
	fake := &fakeFlowAgent{}
	o.agents["claude"] = fake
	svc.UseWorktrees(wt)
	svc.UseTerminal(term)
	svc.UseReengager(o)
	return svc, o, fake
}

// TestFlowWalksSharedWorktreeToGateThenApprove is the M2 acceptance test: a task
// on Plan→Build→Critic→Gate runs plan, then build, then critic IN THE SAME
// worktree, parks at the human gate as needs_human, and on approve advances to
// review. The run cursor and completed steps track the walk throughout.
func TestFlowWalksSharedWorktreeToGateThenApprove(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("true not available")
	}
	svc, o, fake := wireFlowOrch(t)
	repo := gitRepo(t)
	if _, err := svc.CreateProject("proj", repo); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, _ := svc.CreateTaskFull("build a thing", "", "proj", "claude", "plan-build-critic-gate")

	o.start(context.Background(), task)

	// The flow walks plan→build→critic and parks at the gate.
	waitFor(t, "task to park at the gate", func() bool {
		got, _ := svc.GetTask(task.ID)
		return got.Status == model.StatusNeedsHuman
	})

	run, ok := svc.Run(task.ID)
	if !ok {
		t.Fatal("expected a run for a multi-step flow")
	}
	if run.Cursor != "gate" {
		t.Fatalf("cursor at the gate expected, got %q", run.Cursor)
	}
	for _, step := range []string{"plan", "build", "critic"} {
		if !slices.Contains(run.Completed, step) {
			t.Fatalf("completed steps %v missing %q", run.Completed, step)
		}
	}

	// Exactly the three work steps ran, all in the one shared worktree.
	got, _ := svc.GetTask(task.ID)
	if got.Worktree == "" {
		t.Fatal("flow task should have a worktree")
	}
	dirs := fake.turns()
	if len(dirs) != 3 {
		t.Fatalf("want 3 work-step turns (plan,build,critic), got %d: %v", len(dirs), dirs)
	}
	for _, d := range dirs {
		if d != got.Worktree {
			t.Fatalf("step ran in %q, not the shared worktree %q", d, got.Worktree)
		}
	}

	// The gate is a real needs_human checkpoint on the rail.
	reqs, _ := svc.PendingRequests()
	if len(reqs) != 1 {
		t.Fatalf("want one gate checkpoint, got %d", len(reqs))
	}

	// Approve → the flow finishes to review.
	if err := svc.AnswerHuman(reqs[0].ID, "Approve"); err != nil {
		t.Fatalf("answer gate: %v", err)
	}
	waitFor(t, "task to reach review after approve", func() bool {
		got, _ := svc.GetTask(task.ID)
		return got.Status == model.StatusReview
	})
	// No extra work-step turn should have run on approve (approve at the terminal
	// gate finishes; it doesn't loop back into the graph).
	if n := len(fake.turns()); n != 3 {
		t.Fatalf("approve should not run another step; turns=%d", n)
	}
}

// TestFlowGateRejectLoopsBack: a "Request changes" answer at the gate routes back
// to the build step (the graph loop), running one more work turn, and re-parks at
// the gate.
func TestFlowGateRejectLoopsBack(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("true not available")
	}
	svc, o, fake := wireFlowOrch(t)
	repo := gitRepo(t)
	if _, err := svc.CreateProject("proj", repo); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, _ := svc.CreateTaskFull("t", "", "proj", "claude", "plan-build-critic-gate")

	o.start(context.Background(), task)
	waitFor(t, "first gate", func() bool {
		got, _ := svc.GetTask(task.ID)
		return got.Status == model.StatusNeedsHuman
	})
	reqs, _ := svc.PendingRequests()

	// Reject → loop back through build → critic → gate again.
	if err := svc.AnswerHuman(reqs[0].ID, "Request changes"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	waitFor(t, "re-parked at the gate after the rebuild loop", func() bool {
		got, _ := svc.GetTask(task.ID)
		run, _ := svc.Run(task.ID)
		return got.Status == model.StatusNeedsHuman && run.Cursor == "gate"
	})
	// The reject ran at least the build (and critic) turns again — more than the
	// initial three.
	if n := len(fake.turns()); n <= 3 {
		t.Fatalf("reject should re-run build→critic; turns=%d (want >3)", n)
	}
}

// TestFlowResumesMidFlowAfterRestart proves a daemon restart resumes a flow at the
// step it was on rather than from step one. We seed a run parked at `critic` (as a
// prior daemon would have persisted) and re-pick the task: only critic runs, then
// it parks at the gate — plan and build are NOT re-run.
func TestFlowResumesMidFlowAfterRestart(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("true not available")
	}
	svc, o, fake := wireFlowOrch(t)
	repo := gitRepo(t)
	if _, err := svc.CreateProject("proj", repo); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, _ := svc.CreateTaskFull("t", "", "proj", "claude", "plan-build-critic-gate")

	// Simulate the state a restart's RecoverInFlight leaves: a persisted run whose
	// cursor is mid-flow, and the task requeued to backlog.
	if err := svc.StartRun(task.ID, "plan-build-critic-gate", "plan"); err != nil {
		t.Fatalf("start run: %v", err)
	}
	svc.AdvanceRun(task.ID, "plan", "build")
	svc.AdvanceRun(task.ID, "build", "critic")

	o.start(context.Background(), task)
	waitFor(t, "resumed flow parks at the gate", func() bool {
		got, _ := svc.GetTask(task.ID)
		run, _ := svc.Run(task.ID)
		return got.Status == model.StatusNeedsHuman && run.Cursor == "gate"
	})

	// Only the critic step ran on resume — plan and build were already done.
	if n := len(fake.turns()); n != 1 {
		t.Fatalf("resume should run only the critic step; turns=%d", n)
	}
}

// TestFlowResumeInPlaceWalksGraph guards the resume-marker interaction with the
// solo resume-in-place feature main added: a flow task a restart caught mid-run
// (resume=1 + an existing worktree) must resume through its GRAPH walker — reusing
// the worktree, picking up at the cursor — NOT the solo single-conversation resume
// (which would ignore the remaining steps and never reach the gate).
func TestFlowResumeInPlaceWalksGraph(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("true not available")
	}
	svc, o, fake := wireFlowOrch(t)
	repo := gitRepo(t)
	if _, err := svc.CreateProject("proj", repo); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, _ := svc.CreateTaskFull("t", "", "proj", "claude", "plan-build-critic-gate")

	// Simulate the state a restart's RecoverInFlight leaves for an interrupted flow
	// task: an existing worktree with the earlier steps' uncommitted work, a run
	// cursor mid-flow, and the resume marker set.
	dir := o.prepareWorkdir(task) // creates + records the worktree
	sentinel := filepath.Join(dir, "STEP_WORK.txt")
	if err := os.WriteFile(sentinel, []byte("earlier steps' work"), 0o644); err != nil {
		t.Fatalf("seed worktree file: %v", err)
	}
	if err := svc.StartRun(task.ID, "plan-build-critic-gate", "plan"); err != nil {
		t.Fatalf("start run: %v", err)
	}
	svc.AdvanceRun(task.ID, "plan", "build")
	svc.AdvanceRun(task.ID, "build", "critic")
	svc.SetResume(task.ID, true)

	o.start(context.Background(), task)
	waitFor(t, "resumed flow walks the graph to its gate", func() bool {
		got, _ := svc.GetTask(task.ID)
		run, _ := svc.Run(task.ID)
		return got.Status == model.StatusNeedsHuman && run.Cursor == "gate"
	})

	// Reusing the worktree (not pruning it) is what keeps the earlier work alive.
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("flow resume must reuse the worktree; earlier work was lost: %v", err)
	}
	// Only critic ran — plan and build were already done before the restart.
	if n := len(fake.turns()); n != 1 {
		t.Fatalf("resume should run only critic; turns=%d", n)
	}
}
