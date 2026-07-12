package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	answerSubmitDelay = 0 // no real terminal in tests; skip the paste-safe submit gap
	return NewService(st)
}

// TestSelfHealAttemptFields covers the self-heal sub-state on the model: a new task
// gets the default retry budget and starts at attempt 0, and SetAttempt persists the
// counter the board renders as "fixing itself · k/N".
func TestSelfHealAttemptFields(t *testing.T) {
	svc := newTestService(t)
	task, err := svc.CreateTask("t", "", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if task.MaxAttempts != DefaultMaxAttempts {
		t.Fatalf("new task MaxAttempts = %d; want %d", task.MaxAttempts, DefaultMaxAttempts)
	}
	if task.Attempt != 0 {
		t.Fatalf("new task Attempt = %d; want 0", task.Attempt)
	}

	svc.SetAttempt(task.ID, 2)
	got, _ := svc.GetTask(task.ID)
	if got.Attempt != 2 {
		t.Fatalf("after SetAttempt(2), Attempt = %d; want 2", got.Attempt)
	}
	if got.MaxAttempts != DefaultMaxAttempts {
		t.Fatalf("SetAttempt must not disturb MaxAttempts, got %d", got.MaxAttempts)
	}
}

// TestAnswerEscalationReengages covers the self-heal escalation answer: when the
// answered checkpoint's agent is no longer live, AnswerHuman re-engages the agent
// with the human's guidance (rather than stranding the task) — captured here by a
// stub reengager.
func TestAnswerEscalationReengages(t *testing.T) {
	svc := newTestService(t)
	svc.UseTerminal(&fakeTerm{dead: true}) // no live agent to take the answer
	re := &fakeReengager{}
	svc.UseReengager(re)
	task, _ := svc.CreateTask("t", "", "")

	req, _ := svc.AskHuman(task.ID, "tried 3×, stuck — replan or guide me?",
		[]string{"Replan from scratch", "I'll guide you"}, "Stuck on: boom")
	if err := svc.AnswerHuman(req.ID, "use the other API"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if re.taskID != task.ID || re.guidance != "use the other API" {
		t.Fatalf("expected re-engage(%s, %q), got (%s, %q)", task.ID, "use the other API", re.taskID, re.guidance)
	}
}

func TestMaxConcurrentClampAndPersist(t *testing.T) {
	svc := newTestService(t)

	// Unset by default: caller keeps its own default.
	if _, ok, err := svc.GetMaxConcurrent(); err != nil || ok {
		t.Fatalf("expected unset, got ok=%v err=%v", ok, err)
	}

	// Clamps to the 1..8 range on the way in, and the clamped value is returned.
	if n, err := svc.SetMaxConcurrent(99); err != nil || n != MaxConcurrentCap {
		t.Fatalf("SetMaxConcurrent(99) = %d,%v; want %d", n, err, MaxConcurrentCap)
	}
	if n, err := svc.SetMaxConcurrent(0); err != nil || n != MinConcurrent {
		t.Fatalf("SetMaxConcurrent(0) = %d,%v; want %d", n, err, MinConcurrent)
	}

	if _, err := svc.SetMaxConcurrent(5); err != nil {
		t.Fatalf("set 5: %v", err)
	}
	n, ok, err := svc.GetMaxConcurrent()
	if err != nil || !ok || n != 5 {
		t.Fatalf("GetMaxConcurrent = %d,%v,%v; want 5,true,nil", n, ok, err)
	}
}

// TestMaxConcurrentSurvivesReopen mirrors the acceptance criterion that a set
// value persists across a daemon restart (a fresh store on the same file).
func TestMaxConcurrentSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := NewService(st).SetMaxConcurrent(6); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	st2, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	n, ok, err := NewService(st2).GetMaxConcurrent()
	if err != nil || !ok || n != 6 {
		t.Fatalf("after reopen GetMaxConcurrent = %d,%v,%v; want 6,true,nil", n, ok, err)
	}
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
	task, err := svc.CreateTaskFull("t", "", "proj", "opencode", "tdd")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if task.Agent != "claude" || task.Flow != "solo" {
		t.Fatalf("expected opencode/tdd normalized to claude/solo, got %s/%s", task.Agent, task.Flow)
	}
}

// TestCreateTaskKeepsImplementedAgent verifies a wired adapter (codex) is
// preserved, not collapsed to the default — so a codex task really runs codex.
func TestCreateTaskKeepsImplementedAgent(t *testing.T) {
	svc := newTestService(t)
	task, err := svc.CreateTaskFull("t", "", "proj", "codex", "solo")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if task.Agent != "codex" {
		t.Fatalf("expected codex preserved, got %s", task.Agent)
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

// fakeTerm captures bytes delivered to a task's terminal so tests can assert the
// human's answer is injected as the agent's next input. dead=true simulates a
// terminal whose agent has already exited (WriteTo reports no live session).
type fakeTerm struct {
	writes map[string][]byte   // concatenated bytes per task
	chunks map[string][][]byte // each WriteTo recorded separately
	dead   bool
}

func (f *fakeTerm) WriteTo(id string, p []byte) (bool, error) {
	if f.dead {
		return false, nil
	}
	if f.writes == nil {
		f.writes = map[string][]byte{}
		f.chunks = map[string][][]byte{}
	}
	f.writes[id] = append(f.writes[id], p...)
	f.chunks[id] = append(f.chunks[id], append([]byte(nil), p...))
	return true, nil
}

// fakeReengager records the last re-engage so a test can assert an answer to a
// self-heal escalation (a checkpoint whose agent has stopped) re-launches the agent.
type fakeReengager struct {
	taskID   string
	guidance string
}

func (f *fakeReengager) Reengage(taskID, guidance string) error {
	f.taskID, f.guidance = taskID, guidance
	return nil
}

// TestAskHumanPostsAndDelivers exercises the core loop: AskHuman posts a question
// without blocking (flipping the task to needs_human), and AnswerHuman records
// the reply and injects it into the parked agent's terminal.
func TestAskHumanPostsAndDelivers(t *testing.T) {
	svc := newTestService(t)
	ft := &fakeTerm{}
	svc.UseTerminal(ft)
	task, _ := svc.CreateTask("t", "", "")

	req, err := svc.AskHuman(task.ID, "Merge to main?", []string{"yes", "no"}, "diff: +10 -2")
	if err != nil {
		t.Fatalf("ask_human: %v", err)
	}
	if req.Question != "Merge to main?" {
		t.Fatalf("bad question: %q", req.Question)
	}

	// The task flips to needs_human and the request sits on the rail.
	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusNeedsHuman {
		t.Fatalf("expected needs_human, got %s", got.Status)
	}
	if reqs, _ := svc.PendingRequests(); len(reqs) != 1 || reqs[0].ID != req.ID {
		t.Fatalf("expected the request pending on the rail")
	}

	if err := svc.AnswerHuman(req.ID, "yes"); err != nil {
		t.Fatalf("answer: %v", err)
	}

	// Task back to running, request off the rail, answer injected as terminal input.
	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusRunning {
		t.Fatalf("expected running after answer, got %s", got.Status)
	}
	if reqs, _ := svc.PendingRequests(); len(reqs) != 0 {
		t.Fatalf("expected no pending requests, got %d", len(reqs))
	}
	if got := string(ft.writes[task.ID]); got != "yes\r" {
		t.Fatalf("expected answer delivered to terminal as %q, got %q", "yes\r", got)
	}
}

// TestAnswerHumanSubmitsSeparately guards the paste bug: the answer text and the
// Enter that submits it must arrive as TWO writes (text, then a lone CR), not
// glued as "yes\r". A single glued write is read as a paste by interactive TUIs,
// which keep the CR as a literal newline and never submit — the reported symptom
// of a typed-but-unsent answer.
func TestAnswerHumanSubmitsSeparately(t *testing.T) {
	svc := newTestService(t)
	ft := &fakeTerm{}
	svc.UseTerminal(ft)
	task, _ := svc.CreateTask("t", "", "")

	req, err := svc.AskHuman(task.ID, "Merge to main?", []string{"yes", "no"}, "")
	if err != nil {
		t.Fatalf("ask_human: %v", err)
	}
	if err := svc.AnswerHuman(req.ID, "yes"); err != nil {
		t.Fatalf("answer: %v", err)
	}

	chunks := ft.chunks[task.ID]
	if len(chunks) != 2 {
		t.Fatalf("expected 2 writes (text then CR), got %d: %q", len(chunks), chunks)
	}
	if string(chunks[0]) != "yes" {
		t.Fatalf("first write should be the text without a trailing CR, got %q", chunks[0])
	}
	if string(chunks[1]) != "\r" {
		t.Fatalf("second write should be a lone submit CR, got %q", chunks[1])
	}
}

// TestAskHumanCapturesContext is the acceptance check: an ask_human checkpoint on
// a task with real edits and a saved screenshot captures the +N −N magnitude, the
// changed-file list, and the shot filenames server-side — without the agent
// hand-formatting them into the context string.
func TestAskHumanCapturesContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// A throwaway repo with one commit — the base the worktree forks from.
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t.dev"}, {"config", "user.name", "t"},
		{"commit", "--allow-empty", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	svc := newTestService(t)
	wtRoot := filepath.Join(t.TempDir(), "worktrees")
	m := worktree.New(wtRoot)
	svc.UseWorktrees(m)

	if _, err := svc.CreateProject("proj", repo); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, _ := svc.CreateTask("t", "", "proj")

	wt, err := m.Create(repo, task.ID)
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	svc.SetWorktree(task.ID, wt.Path)

	// The agent's work: a new file (+3) and a saved screenshot.
	if err := os.WriteFile(filepath.Join(wt.Path, "feature.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatalf("write edit: %v", err)
	}
	shotsDir := filepath.Join(wt.Path, ".ultraflow", "shots")
	if err := os.MkdirAll(shotsDir, 0o755); err != nil {
		t.Fatalf("mkdir shots: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shotsDir, "before.png"), []byte("png"), 0o644); err != nil {
		t.Fatalf("write shot: %v", err)
	}

	req, err := svc.AskHuman(task.ID, "Ship it?", []string{"yes"}, "")
	if err != nil {
		t.Fatalf("ask_human: %v", err)
	}

	// The magnitude counts the real edit (feature.txt, +3) — captured server-side,
	// not hand-formatted by the agent.
	var feature *model.ChangedFile
	for i := range req.Files {
		if req.Files[i].Path == "feature.txt" {
			feature = &req.Files[i]
		}
	}
	if feature == nil || feature.Added != 3 || feature.Removed != 0 {
		t.Fatalf("changed files = %+v; want feature.txt +3 −0", req.Files)
	}
	// The magnitude excludes Ultraflow's own .ultraflow/ metadata (the shot), so
	// it reads as exactly the agent's +3, not +4.
	if req.Added != 3 || req.Removed != 0 {
		t.Fatalf("magnitude = +%d −%d; want +3 −0 (excluding .ultraflow metadata)", req.Added, req.Removed)
	}
	for _, f := range req.Files {
		if strings.HasPrefix(f.Path, ".ultraflow/") {
			t.Fatalf("changed files should exclude .ultraflow metadata, got %q", f.Path)
		}
	}
	if len(req.Shots) != 1 || req.Shots[0] != "before.png" {
		t.Fatalf("shots = %v; want [before.png]", req.Shots)
	}

	// And it survives the round trip through the store (the new columns persist).
	got, err := svc.store.GetHumanRequest(req.ID)
	if err != nil {
		t.Fatalf("reload request: %v", err)
	}
	if got.Added != req.Added || len(got.Files) != len(req.Files) || len(got.Shots) != 1 {
		t.Fatalf("persisted context lost: +%d files=%d shots=%d", got.Added, len(got.Files), len(got.Shots))
	}
}

func hasErrorEvent(evs []model.Event) bool {
	for _, e := range evs {
		if e.Kind == "error" {
			return true
		}
	}
	return false
}

// TestResolveCrashWhileParked: an agent that crashes (non-zero exit) while parked
// is failed, its orphaned checkpoint retired, and a reason recorded.
func TestResolveCrashWhileParked(t *testing.T) {
	svc := newTestService(t)
	svc.UseTerminal(&fakeTerm{})
	task, _ := svc.CreateTask("t", "", "")
	svc.AskHuman(task.ID, "q", []string{"yes"}, "")

	svc.ResolveAgentExit(task.ID, true, "boom")

	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusFailed {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if reqs, _ := svc.PendingRequests(); len(reqs) != 0 {
		t.Fatalf("expected the checkpoint retired, got %d pending", len(reqs))
	}
	evs, _ := svc.TaskEvents(task.ID)
	if !hasErrorEvent(evs) {
		t.Fatal("expected an error event explaining the failure")
	}
}

// TestAnswerThenCrashFails covers the answer-wins interleaving: the human answer
// resumes the task (needs_human→running) and delivers input, then the agent is
// found crashed. The crash resolution must still fail the task from running
// rather than strand it behind the dead agent.
func TestAnswerThenCrashFails(t *testing.T) {
	svc := newTestService(t)
	ft := &fakeTerm{}
	svc.UseTerminal(ft)
	task, _ := svc.CreateTask("t", "", "")
	req, _ := svc.AskHuman(task.ID, "q", []string{"yes"}, "")

	if err := svc.AnswerHuman(req.ID, "yes"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusRunning {
		t.Fatalf("expected running after answer, got %s", got.Status)
	}

	svc.ResolveAgentExit(task.ID, true, "boom")

	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusFailed {
		t.Fatalf("expected failed after crash, got %s", got.Status)
	}
}

// TestAnswerAfterCrashIsNoop covers the death-wins interleaving: the crash
// resolution runs (needs_human→failed + abandon) before the human's answer lands.
// The answer must be a harmless no-op — it must not resurrect the failed task or
// inject stale input.
func TestAnswerAfterCrashIsNoop(t *testing.T) {
	svc := newTestService(t)
	ft := &fakeTerm{}
	svc.UseTerminal(ft)
	task, _ := svc.CreateTask("t", "", "")
	req, _ := svc.AskHuman(task.ID, "q", []string{"yes"}, "")

	svc.ResolveAgentExit(task.ID, true, "boom")

	if err := svc.AnswerHuman(req.ID, "yes"); err != nil {
		t.Fatalf("late answer: %v", err)
	}
	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusFailed {
		t.Fatalf("late answer must not resurrect the task, got %s", got.Status)
	}
	if len(ft.writes[task.ID]) != 0 {
		t.Fatalf("late answer must not inject input, got %q", string(ft.writes[task.ID]))
	}
}

// TestResolveCleanExitWhileParkedFails: a clean exit (exit 0) while still parked
// means the agent never received its answer, so the task fails rather than being
// presented as reviewable.
func TestResolveCleanExitWhileParkedFails(t *testing.T) {
	svc := newTestService(t)
	svc.UseTerminal(&fakeTerm{})
	task, _ := svc.CreateTask("t", "", "")
	svc.AskHuman(task.ID, "q", []string{"yes"}, "")

	svc.ResolveAgentExit(task.ID, false, "")

	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusFailed {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if reqs, _ := svc.PendingRequests(); len(reqs) != 0 {
		t.Fatalf("expected the checkpoint retired, got %d pending", len(reqs))
	}
}

// TestCleanExitAnswerRaceAlwaysTerminal hammers the clean-exit death path against
// a concurrent human answer — the exact interleaving that twice stranded a task
// in `running` behind a dead agent. Whatever the ordering, the task must always
// come to rest terminal (review or failed), never in-flight. Run under -race.
func TestCleanExitAnswerRaceAlwaysTerminal(t *testing.T) {
	svc := newTestService(t)
	svc.UseTerminal(&fakeTerm{})
	for i := range 300 {
		task, _ := svc.CreateTask("t", "", "")
		req, _ := svc.AskHuman(task.ID, "q", []string{"yes"}, "")

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); svc.AnswerHuman(req.ID, "yes") }()
		go func() { defer wg.Done(); svc.ResolveAgentExit(task.ID, false, "") }()
		wg.Wait()

		if got, _ := svc.GetTask(task.ID); got.Status != model.StatusReview && got.Status != model.StatusFailed {
			t.Fatalf("iter %d: task not terminal after race: %s", i, got.Status)
		}
	}
}

// TestResolveCleanExitRunningReviews: a running agent that exits cleanly after
// ending its turn (never parked) goes to review.
func TestResolveCleanExitRunningReviews(t *testing.T) {
	svc := newTestService(t)
	task, _ := svc.CreateTask("t", "", "")
	svc.UpdateStatus(task.ID, model.StatusRunning)

	svc.ResolveAgentExit(task.ID, false, "")

	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusReview {
		t.Fatalf("expected review, got %s", got.Status)
	}
}

// TestAnswerToDeadTerminalStillResumes: if the terminal reports no live session,
// AnswerHuman still flips the (still-parked) task to running and records the
// answer — the orchestrator's Wait path is what fails a genuinely dead agent, so
// AnswerHuman must not silently swallow the answer.
func TestAnswerToDeadTerminalStillResumes(t *testing.T) {
	svc := newTestService(t)
	svc.UseTerminal(&fakeTerm{dead: true})
	task, _ := svc.CreateTask("t", "", "")

	req, _ := svc.AskHuman(task.ID, "q", []string{"yes"}, "")
	if err := svc.AnswerHuman(req.ID, "yes"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusRunning {
		t.Fatalf("expected running after answer, got %s", got.Status)
	}
	if reqs, _ := svc.PendingRequests(); len(reqs) != 0 {
		t.Fatalf("expected request off the rail, got %d", len(reqs))
	}
}

// TestAbandonRequests covers the parked-agent-died path: when an agent exits
// while its task is still needs_human, the orchestrator calls AbandonRequests to
// retire the now-orphaned request so it leaves the attention rail (it can no
// longer be answered into a void).
func TestAbandonRequests(t *testing.T) {
	svc := newTestService(t)
	task, _ := svc.CreateTask("t", "", "")

	if _, err := svc.AskHuman(task.ID, "q", nil, ""); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if reqs, _ := svc.PendingRequests(); len(reqs) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(reqs))
	}

	svc.AbandonRequests(task.ID)

	if reqs, _ := svc.PendingRequests(); len(reqs) != 0 {
		t.Fatalf("expected the abandoned request to leave the rail, got %d pending", len(reqs))
	}
}

// TestAnswerHumanDoubleAnswerNoop covers M-4: a second (or unknown) answer must
// be a harmless no-op that neither errors nor re-runs side effects — in
// particular it must not inject a second line into the agent's terminal.
func TestAnswerHumanDoubleAnswerNoop(t *testing.T) {
	svc := newTestService(t)
	ft := &fakeTerm{}
	svc.UseTerminal(ft)
	task, _ := svc.CreateTask("t", "", "")

	req, err := svc.AskHuman(task.ID, "q", []string{"yes"}, "")
	if err != nil {
		t.Fatalf("ask: %v", err)
	}

	if err := svc.AnswerHuman(req.ID, "yes"); err != nil {
		t.Fatalf("first answer: %v", err)
	}

	// Second answer to the same (now answered) request: no-op, no error.
	if err := svc.AnswerHuman(req.ID, "no"); err != nil {
		t.Fatalf("duplicate answer should be a no-op, got %v", err)
	}
	if got := string(ft.writes[task.ID]); got != "yes\r" {
		t.Fatalf("duplicate answer re-injected input: %q", got)
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
