// Package orchestrator picks up backlog tasks and runs them through their flow.
// M0 implements only the "solo" flow: one Claude agent in its own git worktree.
// The multi-step flows and other adapters are not wired yet, so task creation
// normalizes any other choice down to claude/solo (see core.CreateTaskFull).
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"ultraflow/internal/agent"
	"ultraflow/internal/core"
	"ultraflow/internal/devserver"
	"ultraflow/internal/model"
	"ultraflow/internal/port"
	"ultraflow/internal/terminal"
	"ultraflow/internal/worktree"
)

// interactiveAgent is an adapter that can run as a live PTY session (a real
// terminal the human can watch and type into), as opposed to headless.
// ResumeCommand re-opens a prior conversation for a review send-back.
type interactiveAgent interface {
	Command(ctx context.Context, dir, prompt string) (*exec.Cmd, func(), error)
	ResumeCommand(ctx context.Context, dir, prompt string) (*exec.Cmd, func(), error)
}

type Orchestrator struct {
	svc     *core.Service
	agents  map[string]agent.Agent
	workdir string
	wt      *worktree.Manager
	term    *terminal.Manager
	ports   *port.Allocator    // reserves a distinct dev-server port per task
	dev     *devserver.Manager // runs the per-project dev-server hook, detached
	baseCtx context.Context    // daemon lifetime; used by out-of-band launches (Revise)

	// Resizable concurrency limit. A plain buffered-channel semaphore is fixed at
	// creation; this mutex+cond version lets SetLimit change the ceiling at runtime
	// — raising it wakes queued acquirers immediately, lowering it just stops new
	// starts (already-running agents past the new limit are never killed).
	mu     sync.Mutex
	cond   *sync.Cond
	active int
	limit  int

	// healing tracks tasks with a live self-heal loop (a runWithSelfHeal goroutine
	// mid-flight). It's how an answer-driven re-engage knows NOT to launch a second
	// agent: if a loop is still running for the task, it owns the crash resolution.
	// Guarded by mu.
	healing map[string]bool
}

func New(svc *core.Service, workdir string, wt *worktree.Manager, term *terminal.Manager, ports *port.Allocator, dev *devserver.Manager, mcpURL string, maxConcurrent int) *Orchestrator {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	o := &Orchestrator{
		svc:     svc,
		agents: map[string]agent.Agent{
			"claude": agent.NewClaude(mcpURL),
			"codex":  agent.NewCodex(mcpURL),
		},
		workdir: workdir,
		wt:      wt,
		term:    term,
		ports:   ports,
		dev:     dev,
		limit:   maxConcurrent,
		healing: map[string]bool{},
	}
	o.cond = sync.NewCond(&o.mu)
	return o
}

// SetLimit changes the max number of agents allowed to run at once. Raising it
// broadcasts so any goroutines blocked waiting for a slot wake and re-check;
// lowering it takes effect only for future acquisitions — running agents are
// left alone. A value below 1 is clamped to 1.
func (o *Orchestrator) SetLimit(n int) {
	if n < 1 {
		n = 1
	}
	o.mu.Lock()
	o.limit = n
	o.mu.Unlock()
	o.cond.Broadcast()
}

// Limit returns the current concurrency ceiling.
func (o *Orchestrator) Limit() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.limit
}

// acquire blocks until a concurrency slot is free under the current limit, then
// reserves it. release must be called (deferred) when the agent is done.
func (o *Orchestrator) acquire() {
	o.mu.Lock()
	for o.active >= o.limit {
		o.cond.Wait()
	}
	o.active++
	o.mu.Unlock()
}

func (o *Orchestrator) release() {
	o.mu.Lock()
	o.active--
	o.mu.Unlock()
	o.cond.Broadcast()
}

// Run polls the backlog and starts tasks until ctx is cancelled.
func (o *Orchestrator) Run(ctx context.Context) {
	o.baseCtx = ctx // so Revise's launch is tied to the daemon, not an HTTP request
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tasks, err := o.svc.BacklogTasks()
			if err != nil {
				continue
			}
			for _, task := range tasks {
				o.start(ctx, task)
			}
		}
	}
}

func (o *Orchestrator) start(ctx context.Context, t model.Task) {
	// Flip out of backlog synchronously so the next tick won't re-pick it, but
	// mark it "queued" — not "running" — because it may sit waiting for a
	// concurrency slot. The board shows queued distinctly from live-running.
	// If this write fails the task stays in backlog; bail so we don't spawn a
	// goroutine for a task the next tick will (correctly) pick up again — that
	// would double-run it.
	if err := o.svc.UpdateStatus(t.ID, model.StatusQueued); err != nil {
		log.Printf("task %s: could not queue (%v); will retry next tick", t.ID, err)
		return
	}

	go func() {
		o.acquire()
		defer o.release()

		// Give the task its own isolated checkout so parallel agents don't collide.
		// Falls back to the shared workdir when the task has no registered git repo.
		// The worktree is intentionally kept after the run so the human can review
		// the diff; there is no merge flow yet, so a retry of the same task id is
		// what reclaims the branch (Create prunes it before re-adding).
		dir := o.prepareWorkdir(t)

		// Reserve a distinct dev-server port for this task and, if the project ships a
		// dev-server hook, boot it — so parallel tasks never collide and the human can
		// open the task's live app from its card.
		prt := o.setupPort(t, dir)

		// t.Agent is already normalized to an implemented adapter at creation time
		// (core.CreateTaskFull), so this lookup resolves; the nil guard is a belt-
		// and-braces fallback rather than a silent substitution of a real choice.
		ag := o.agents[t.Agent]
		if ag == nil {
			ag = o.agents["claude"]
		}
		ia, ok := ag.(interactiveAgent)
		if !ok {
			o.fail(t.ID, "agent "+ag.Name()+" can't run as an interactive terminal")
			return
		}

		cmd, cleanup, err := ia.Command(ctx, dir, buildPrompt(t, prt))
		if err != nil {
			o.fail(t.ID, "couldn't build the agent command: "+err.Error())
			return
		}
		injectPort(cmd, prt)
		o.runWithSelfHeal(ctx, t, dir, ia, cmd, cleanup, "running — open the card to watch progress (Ctrl-C to interrupt)")
	}()
}

// runWithSelfHeal runs a task's agent and, on an unexpected error, AUTO-DIAGNOSES
// and re-runs it up to its retry budget — the task STAYS `running` the whole time
// with a "fixing itself · k/N" sub-state (never a red failed card). Retries resume
// the SAME worktree conversation, so the agent keeps its memory of what it built
// and can fix its own mistake. Only when the budget is exhausted does it ESCALATE
// as a plain-language needs_human checkpoint. A clean exit resolves normally
// (finish_task → review, or an ended session → review); a signalled exit is a
// human stop → terminal `failed`. See spec.md "Failure self-heals".
//
// It owns the task's execution end-to-end, so while its goroutine is live it marks
// the task `healing`: an answer-driven re-engage checks that flag and stands down,
// which is what keeps a checkpoint-gap crash from ever racing two agents onto one
// worktree. `cmd`/`cleanup` are the first attempt; further attempts are built here.
func (o *Orchestrator) runWithSelfHeal(ctx context.Context, t model.Task, dir string, ia interactiveAgent, cmd *exec.Cmd, cleanup func(), runningMsg string) {
	o.beginHeal(t.ID)
	defer o.endHeal(t.ID)

	budget := t.MaxAttempts
	if budget < 1 {
		budget = core.DefaultMaxAttempts
	}
	retries := 0
	o.svc.SetAttempt(t.ID, retries) // 0 = the original run, no sub-state

	for {
		werr, started := o.runAgent(t.ID, cmd, cleanup, runningMsg)
		if !started {
			return // runAgent already failed the task (couldn't start the terminal)
		}
		if ctx.Err() != nil {
			return // daemon shutting down — startup recovery requeues it next boot
		}
		// An intentional end, not a crash: finish_task and the idle-watcher both send
		// the task to `review` and then Close the session (SIGKILL), so the exit here
		// looks like a non-nil crash error. If the task already reached review, the
		// agent was ended on purpose — resolve, don't self-heal into a spurious retry.
		if cur, _ := o.svc.GetTask(t.ID); cur.Status == model.StatusReview {
			return
		}
		if werr == nil {
			// Clean exit: finished via finish_task (already review), the human ended
			// the session, or a parked clean-exit. The guarded resolver handles all,
			// race-safe against an answer landing in the same instant.
			o.svc.ResolveAgentExit(t.ID, false, "")
			return
		}
		if stoppedByHuman(werr) {
			o.gaveUp(t.ID, "you stopped this task") // Ctrl-C = you-said-stop → terminal
			return
		}

		// The agent errored. If the budget is spent, escalate to the human as an
		// ordinary needs_human item — never a raw red dump.
		if retries >= budget {
			o.escalate(t.ID, budget, werr.Error())
			return
		}

		retries++
		// The raw error is a COLLAPSED disclosure in the thread ("Why it failed"),
		// never the headline; the friendly sub-state line is what leads the card.
		o.svc.AppendTaskEvent(t.ID, "error", fmt.Sprintf("attempt failed: %s", truncateErr(werr.Error())))
		// A stale checkpoint (agent died parked) would otherwise linger on the rail.
		o.svc.AbandonRequests(t.ID)
		o.svc.SetAttempt(t.ID, retries)
		o.svc.AppendTaskEvent(t.ID, "status",
			fmt.Sprintf("fixing itself · %d/%d — diagnosing the error and retrying", retries, budget))

		next, ncleanup, berr := ia.ResumeCommand(ctx, dir, buildSelfHealPrompt(t, retries, budget, werr.Error()))
		if berr != nil {
			o.escalate(t.ID, budget, "couldn't relaunch the agent to retry: "+berr.Error())
			return
		}
		cmd, cleanup, runningMsg = next, ncleanup, fmt.Sprintf("fixing itself · %d/%d (Ctrl-C to interrupt)", retries, budget)
	}
}

// runAgent runs cmd as the task's live PTY agent for a single attempt: it registers
// the session (so the board can attach and watch/type/Ctrl-C), flips the card to
// running only once the terminal exists (never a 404), waits for the process to
// exit, and returns the exit error (nil = clean). started is false only when the
// terminal couldn't be started, in which case the task is already failed.
func (o *Orchestrator) runAgent(taskID string, cmd *exec.Cmd, cleanup func(), runningMsg string) (werr error, started bool) {
	defer cleanup()

	sess, err := o.term.Start(taskID, cmd)
	if err != nil {
		o.fail(taskID, "couldn't start the agent terminal: "+err.Error())
		return nil, false
	}
	o.svc.UpdateStatus(taskID, model.StatusRunning)
	o.svc.AppendTaskEvent(taskID, "status", runningMsg)

	// Free the slot when the agent ends its turn without finish_task: an interactive
	// TUI never exits on its own, so without this it would idle at its prompt holding
	// the slot forever. Started per attempt; it sends an idle turn-end to `review`
	// and kills the session — which the self-heal loop treats as an intentional end
	// (the task is now in review), not a crash to retry. Runs for the attempt's whole
	// life so it also catches a second idle after an ask_human answer resumes.
	go o.watchIdle(sess, taskID, idleTimeout, idlePoll)

	werr = sess.Wait()
	if werr != nil {
		log.Printf("task %s: agent exited before finishing: %v", taskID, werr)
	}
	return werr, true
}

// beginHeal / endHeal / isHealing track whether a self-heal loop is live for a task
// (see the healing field). isHealing gates the answer-driven re-engage so it never
// launches a second agent onto a worktree a loop still owns.
func (o *Orchestrator) beginHeal(id string) {
	o.mu.Lock()
	o.healing[id] = true
	o.mu.Unlock()
}

func (o *Orchestrator) endHeal(id string) {
	o.mu.Lock()
	delete(o.healing, id)
	o.mu.Unlock()
}

func (o *Orchestrator) isHealing(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.healing[id]
}

// stoppedByHuman reports whether an agent exit was a deliberate interrupt (the
// human hit Ctrl-C, or the process got SIGTERM) rather than an internal crash — a
// stop is terminal, a crash self-heals.
func stoppedByHuman(werr error) bool {
	var ee *exec.ExitError
	if errors.As(werr, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			switch ws.Signal() {
			case syscall.SIGINT, syscall.SIGTERM:
				return true
			}
		}
	}
	return false
}

// truncateErr collapses an error to a single readable line for a thread event or a
// checkpoint's context, so a chatty stack trace never becomes a wall of red.
func truncateErr(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) > 200 {
		return string(r[:199]) + "…"
	}
	return s
}

// escalate hands a stuck task to the human after self-heal is exhausted. It posts
// an ORDINARY needs_human checkpoint phrased in plain language ("tried N×, stuck on
// X — replan, or guide me?"), so the task lands in the attention rail like any
// other decision — not a raw error dump. The raw error stays a collapsed thread
// disclosure. Answering it re-engages the agent (via AnswerHuman → Reengage).
func (o *Orchestrator) escalate(taskID string, budget int, lastErr string) {
	o.svc.AppendTaskEvent(taskID, "error",
		fmt.Sprintf("self-heal exhausted after %d retries: %s", budget, truncateErr(lastErr)))
	q := fmt.Sprintf("I tried %d times to fix this myself and I'm still stuck. "+
		"Want me to replan from scratch, or will you guide me?", budget)
	stuck := "Stuck on: " + truncateErr(lastErr)
	if _, err := o.svc.AskHuman(taskID, q, []string{"Replan from scratch", "I'll guide you"}, stuck); err != nil {
		log.Printf("task %s: escalate: %v", taskID, err)
	}
}

// Idle-watcher tuning. A working Claude TUI streams a spinner/elapsed-timer
// continuously, so a stretch of pure silence means the turn has ended and the agent
// is parked at its prompt. The two error costs are asymmetric: waiting too long only
// delays freeing a slot by seconds (cheap — a human-in-the-loop board), while acting
// too soon SIGKILLs a genuinely-working agent mid-task and ships partial work. So
// idleTimeout is deliberately generous — comfortably longer than any silent gap a
// working agent produces (a slow model turn or a quiet long tool run both keep the
// timer animating) — biasing hard against a false positive. idlePoll is the cadence.
const (
	idleTimeout = 90 * time.Second
	idlePoll    = 5 * time.Second
)

// watchIdle ends a session whose agent went idle at its prompt without calling
// finish_task, so a bare turn-end frees the concurrency slot instead of pinning it
// forever. It sends the task to review and kills the session (the freed slot then
// starts a queued task). It runs until the session ends.
//
// It must NOT disturb the intentional ask_human durable wait: an agent parked on an
// open human request is SUPPOSED to idle at its prompt. That case is excluded by the
// guarded swap — ask_human moved the task to needs_human, so running→review fails
// and we keep watching (a later answer returns it to running, where a fresh idle can
// be caught). The swap is also the atomic arbiter against an ask_human/answer racing
// in at the moment we act.
func (o *Orchestrator) watchIdle(sess *terminal.Session, taskID string, timeout, poll time.Duration) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-sess.Done():
			return
		case <-ticker.C:
			if sess.IdleFor() < timeout {
				continue
			}
			if o.svc.SwapStatus(taskID, []model.TaskStatus{model.StatusRunning}, model.StatusReview) {
				o.svc.AppendTaskEvent(taskID, "status",
					"agent went idle without calling finish_task — sent to review and freed the slot")
				sess.Close()
				return
			}
		}
	}
}

// Revise re-engages a task's agent after it has gone to review (or failed): it
// re-runs the agent IN THE SAME worktree — keeping every file it already wrote —
// with the human's feedback seeded, and via `claude --continue` its memory of the
// conversation. The card flips back to running so the human watches the rework
// live in the terminal, and a normal finish_task returns it to review. This is
// what makes review a conversation ("you made X wrong, redo it") instead of a
// merge-or-nothing dead-end. Reuses the same concurrency-slot machinery as a
// fresh start, so a rework still respects the parallel-agent cap.
func (o *Orchestrator) Revise(taskID, feedback string) error {
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return fmt.Errorf("write what the agent should change")
	}
	t, err := o.svc.GetTask(taskID)
	if err != nil {
		return err
	}
	if t.Status != model.StatusReview && t.Status != model.StatusFailed {
		return fmt.Errorf("you can only send a task back while it's in review or failed (this one is %s)", t.Status)
	}
	if _, live := o.term.Get(taskID); live {
		return fmt.Errorf("this task already has a live session — type into its terminal instead")
	}

	ag := o.agents[t.Agent]
	if ag == nil {
		ag = o.agents["claude"]
	}
	ia, ok := ag.(interactiveAgent)
	if !ok {
		return fmt.Errorf("agent %s can't run interactively", ag.Name())
	}
	dir := t.Worktree
	if dir == "" {
		dir = o.workdir // ran in place (non-git / shared-workdir project)
	}

	// Flip out of review synchronously so the board reflects the send-back at once
	// and a double-click can't launch two agents on the same worktree.
	if err := o.svc.UpdateStatus(taskID, model.StatusQueued); err != nil {
		return err
	}
	o.svc.AppendTaskEvent(taskID, "human_answer", feedback)

	// Reuse the port reserved on the first run (its dev server has stayed up through
	// review); reserve one now if the earlier run never got one.
	prt := t.Port
	if prt == 0 {
		prt = o.setupPort(t, dir)
	}

	go func() {
		o.acquire()
		defer o.release()
		cmd, cleanup, err := ia.ResumeCommand(o.ctx(), dir, buildRevisePrompt(t, feedback, prt))
		if err != nil {
			o.fail(taskID, "couldn't build the agent command: "+err.Error())
			return
		}
		injectPort(cmd, prt)
		o.runWithSelfHeal(o.ctx(), t, dir, ia, cmd, cleanup, "reworking on your feedback (Ctrl-C to interrupt)")
	}()
	return nil
}

// Reengage re-launches a task's agent after the human answered its self-heal
// escalation checkpoint (the "tried N×, stuck — replan, or guide me?" item). It
// resumes the same worktree conversation with the human's guidance seeded and a
// FRESH retry budget, so the agent can act on the steer and, if it stumbles again,
// self-heal anew. Driven from AnswerHuman when the answered checkpoint's agent is
// no longer live. It is a no-op when a self-heal loop is still running for the task
// (an agent that died in the checkpoint gap) — that loop already owns the recovery,
// so this can't race a second agent onto the worktree.
func (o *Orchestrator) Reengage(taskID, guidance string) error {
	if o.isHealing(taskID) {
		return nil
	}
	t, err := o.svc.GetTask(taskID)
	if err != nil {
		return err
	}
	ag := o.agents[t.Agent]
	if ag == nil {
		ag = o.agents["claude"]
	}
	ia, ok := ag.(interactiveAgent)
	if !ok {
		return fmt.Errorf("agent %s can't run interactively", ag.Name())
	}
	dir := t.Worktree
	if dir == "" {
		dir = o.workdir // ran in place (non-git / shared-workdir project)
	}

	go func() {
		o.acquire()
		defer o.release()
		cmd, cleanup, err := ia.ResumeCommand(o.ctx(), dir, buildReengagePrompt(t, guidance))
		if err != nil {
			o.fail(taskID, "couldn't relaunch the agent: "+err.Error())
			return
		}
		injectPort(cmd, t.Port) // reuse the port reserved on the first run (no-op if none)
		o.runWithSelfHeal(o.ctx(), t, dir, ia, cmd, cleanup, "back on it with your guidance (Ctrl-C to interrupt)")
	}()
	return nil
}

// ctx returns the daemon-lifetime context for out-of-band launches. Falls back
// to Background if Run hasn't recorded one yet (e.g. in a unit test).
func (o *Orchestrator) ctx() context.Context {
	if o.baseCtx != nil {
		return o.baseCtx
	}
	return context.Background()
}

// fail records a reason on the task thread and marks it failed, so a failed card
// always explains why instead of just showing "Gave up". Reserved for genuine
// dead-ends (couldn't build/start the agent at all) — an agent that ran and errored
// self-heals instead. `failed` is terminal: gave-up or you-said-stop.
func (o *Orchestrator) fail(taskID, reason string) {
	o.svc.AppendTaskEvent(taskID, "error", reason)
	o.svc.UpdateStatus(taskID, model.StatusFailed)
}

// gaveUp marks a task terminally failed because the human stopped it (Ctrl-C). The
// guarded swap retires any parked checkpoint and won't clobber a task an answer has
// already moved on, mirroring the crash resolver's race-safety.
func (o *Orchestrator) gaveUp(taskID, reason string) {
	if o.svc.SwapStatus(taskID, []model.TaskStatus{model.StatusRunning, model.StatusQueued, model.StatusNeedsHuman}, model.StatusFailed) {
		o.svc.AbandonRequests(taskID)
		o.svc.AppendTaskEvent(taskID, "error", reason)
	}
}

// prepareWorkdir resolves where a task's agent should run. In order of
// preference: an isolated git worktree off the project's repo (the safe default
// for parallel work); the registered folder directly if it isn't a git repo;
// or the shared workdir when the task has no registered project. Any degradation
// is recorded on the task thread so the human knows isolation was skipped.
func (o *Orchestrator) prepareWorkdir(t model.Task) string {
	if t.Project == "" {
		return o.workdir
	}
	p, err := o.svc.ProjectByName(t.Project)
	if err != nil || p.RepoPath == "" {
		return o.workdir
	}
	if !worktree.IsGitRepo(p.RepoPath) {
		o.svc.AppendTaskEvent(t.ID, "status",
			"project folder isn't a git repo — running without an isolated worktree")
		return p.RepoPath
	}
	w, err := o.wt.Create(p.RepoPath, t.ID)
	if err != nil {
		log.Printf("task %s: worktree create failed: %v", t.ID, err)
		o.svc.AppendTaskEvent(t.ID, "status", "couldn't create a worktree; running in the repo root")
		return p.RepoPath
	}
	o.svc.SetWorktree(t.ID, w.Path)
	o.svc.AppendTaskEvent(t.ID, "status", "worktree ready on branch "+w.Branch)
	return w.Path
}

// setupPort reserves a distinct dev-server port for the task, records it on the
// card, and boots the project's dev-server hook (if any) bound to that port. The
// port is also injected into the agent's env by injectPort. Returns 0 when no
// allocator is wired or a port couldn't be obtained (the task still runs, just
// without a reserved port).
func (o *Orchestrator) setupPort(t model.Task, dir string) int {
	if o.ports == nil {
		return 0
	}
	p, err := o.ports.Allocate()
	if err != nil {
		o.svc.AppendTaskEvent(t.ID, "status", "couldn't allocate a dev-server port: "+err.Error())
		return 0
	}
	o.svc.SetPort(t.ID, p)
	o.svc.AppendTaskEvent(t.ID, "status", fmt.Sprintf("dev-server port %d reserved → http://localhost:%d", p, p))
	o.startDevServer(t, dir, p)
	return p
}

// startDevServer runs the project's .ultraflow/dev.sh hook (if present) as a
// detached background dev server bound to p, so it stays up after the agent
// finishes and the human can open the app from the Review card. No hook is a
// no-op — the agent can still start its own server on the injected PORT.
func (o *Orchestrator) startDevServer(t model.Task, dir string, p int) {
	if o.dev == nil {
		return
	}
	repo := o.repoPath(t)
	if repo == "" {
		return
	}
	hook, ok := devserver.HookPath(repo)
	if !ok {
		return
	}
	if err := o.dev.Start(t.ID, dir, hook, p); err != nil {
		o.svc.AppendTaskEvent(t.ID, "status", "dev-server hook failed to start: "+err.Error())
		return
	}
	o.svc.AppendTaskEvent(t.ID, "status", "started dev server via .ultraflow/dev.sh")
}

// repoPath returns the registered repo path for a task's project, or "" when the
// task has no project (runs in the shared workdir).
func (o *Orchestrator) repoPath(t model.Task) string {
	if t.Project == "" {
		return ""
	}
	p, err := o.svc.ProjectByName(t.Project)
	if err != nil {
		return ""
	}
	return p.RepoPath
}

// injectPort exports the reserved dev-server port to the agent process as PORT
// and ULTRAFLOW_PORT, so a dev server the agent starts binds the task's own port.
// Appends to cmd.Env (already seeded with TERM by the adapter).
func injectPort(cmd *exec.Cmd, p int) {
	if cmd == nil || p <= 0 {
		return
	}
	cmd.Env = append(cmd.Env, fmt.Sprintf("PORT=%d", p), fmt.Sprintf("ULTRAFLOW_PORT=%d", p))
}

// screenshotInstruction tells the agent to leave visual evidence for the review
// screen. Screenshots saved here are served and shown in the task's review, so
// the human can see a visual change without checking the branch out and running
// it. Shared by the initial and the send-back prompts.
const screenshotInstruction = `If you changed anything VISUAL (UI, frontend, layout, styling), before you ` +
	`finish capture screenshots of the affected screens and save them as PNG files ` +
	`under .ultraflow/shots/ in your working directory. The board shows them on the ` +
	`review screen, so the human can see the change without running it.`

// portInstruction tells the agent about its reserved dev-server port. Empty when
// no port was allocated. Shared by the initial and send-back prompts.
func portInstruction(p int) string {
	if p <= 0 {
		return ""
	}
	return fmt.Sprintf(`A dev-server port is reserved for this task: PORT=%d (also `+
		`ULTRAFLOW_PORT). If you start a dev server, bind it to $PORT so the human can `+
		`open http://localhost:%d from the board. If this project has a `+
		`.ultraflow/dev.sh hook, Ultraflow already started it on that port for you.`+"\n\n", p, p)
}

func buildPrompt(t model.Task, prt int) string {
	return fmt.Sprintf(`You are working on an Ultraflow task.

Task ID: %s
Title: %s

%s

%sIMPORTANT: You have an MCP tool "ask_human". When a decision is irreversible,
visual, or architectural — or you need the human to review something — do NOT
guess. Call ask_human with task_id="%s", a clear question, suggested options,
and helpful context (a diff, a plan, or a screenshot description). After you call
it, STOP and end your turn — do not keep working or guess. The human's answer is
delivered to you as your next input, and you continue from there.

%s

WHEN YOU ARE DONE: call the MCP tool "finish_task" with task_id="%s" and a one-
line summary. That sends your work to review and ends this session — do not sit
idle at the prompt waiting; call finish_task and stop.`,
		t.ID, t.Title, t.Body, portInstruction(prt), t.ID, screenshotInstruction, t.ID)
}

// buildRevisePrompt is the message seeded when the human sends a reviewed task
// back for changes. The agent's earlier work is still in the worktree (and, via
// --continue, in its conversation memory), so this re-states the feedback and the
// finish contract. It's self-contained enough to be useful even if the prior
// conversation couldn't be restored.
func buildRevisePrompt(t model.Task, feedback string, prt int) string {
	return fmt.Sprintf(`The human reviewed your work on this Ultraflow task and is sending it back for changes.

Task ID: %s
Title: %s

Their feedback:
%s

Your earlier changes are still here in this working directory — review them, then
address the feedback. %s

%sWHEN YOU ARE DONE: call the MCP tool "finish_task" with task_id="%s" and a one-
line summary to send it back to review.`,
		t.ID, t.Title, feedback, screenshotInstruction, portInstruction(prt), t.ID)
}

// buildSelfHealPrompt is seeded when the agent's previous run ended in an ERROR and
// the orchestrator resumes it to auto-diagnose. Because it resumes the same
// conversation (`claude --continue`), the agent still remembers what it was doing;
// this re-states the error and the finish contract so the diagnosis is grounded.
func buildSelfHealPrompt(t model.Task, retry, budget int, errText string) string {
	return fmt.Sprintf(`Your last run on this Ultraflow task ended with an ERROR before you finished — the process exited unexpectedly.

Task ID: %s
Title: %s

The error:
%s

This is self-heal retry %d of %d. Work out what went wrong, fix the root cause, and
carry on with the task — your earlier work is still here in this working directory.
Don't just repeat what failed; diagnose it first.

WHEN YOU ARE DONE: call the MCP tool "finish_task" with task_id="%s" and a one-line summary.`,
		t.ID, t.Title, truncateErr(errText), retry, budget, t.ID)
}

// buildReengagePrompt is seeded when the human answers a self-heal escalation — the
// agent gave up after N tries and asked whether to replan or be guided. It resumes
// the conversation with that answer and the finish contract.
func buildReengagePrompt(t model.Task, guidance string) string {
	return fmt.Sprintf(`You got stuck on this Ultraflow task after several self-heal attempts and asked the human for help. They have responded.

Task ID: %s
Title: %s

Their guidance:
%s

Use it to get unstuck. Your earlier work is still here in this working directory. %s

WHEN YOU ARE DONE: call the MCP tool "finish_task" with task_id="%s" and a one-line summary.`,
		t.ID, t.Title, guidance, screenshotInstruction, t.ID)
}
