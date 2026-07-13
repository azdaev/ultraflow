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
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"ultraflow/internal/agent"
	"ultraflow/internal/core"
	"ultraflow/internal/devserver"
	"ultraflow/internal/flow"
	"ultraflow/internal/journal"
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

	// Resizable concurrency limit (mutex+cond, not a fixed channel semaphore, so
	// SetLimit can change the ceiling at runtime: raising wakes queued acquirers,
	// lowering only stops new starts).
	mu     sync.Mutex
	cond   *sync.Cond
	active int
	limit  int

	// paused, when true, holds ALL agents: acquire blocks every new/queued/out-of-band
	// start on it, and SetPaused SIGSTOPs the live sessions so running agents freeze too.
	// Transient (in-memory only) — a restart re-runs in-flight tasks unpaused.
	paused bool

	// healing tracks tasks with a live self-heal loop, so an answer-driven re-engage
	// knows that loop already owns the recovery and won't launch a second agent.
	// Guarded by mu.
	healing map[string]bool
}

// launchIntent describes one agent launch (fresh run, revision, re-engage, or
// rebase); execute below owns the shared command-build, port-inject, self-heal path.
type launchIntent struct {
	task       model.Task
	dir        string
	agent      interactiveAgent
	prompt     string
	port       int
	fresh      bool
	runningMsg string
	buildErr   string
}

func (o *Orchestrator) interactiveAgent(t model.Task) (interactiveAgent, error) {
	ag := o.agents[t.Agent]
	if ag == nil {
		ag = o.agents["claude"]
	}
	ia, ok := ag.(interactiveAgent)
	if !ok {
		return nil, fmt.Errorf("agent %s can't run interactively", ag.Name())
	}
	return ia, nil
}

// launch runs an intent in the background under a concurrency slot.
func (o *Orchestrator) launch(intent launchIntent) {
	go func() {
		o.acquire()
		defer o.release()
		o.execute(o.ctx(), intent)
	}()
}

func (o *Orchestrator) execute(ctx context.Context, intent launchIntent) {
	var cmd *exec.Cmd
	var cleanup func()
	var err error
	if intent.fresh {
		cmd, cleanup, err = intent.agent.Command(ctx, intent.dir, intent.prompt)
	} else {
		cmd, cleanup, err = intent.agent.ResumeCommand(ctx, intent.dir, intent.prompt)
	}
	if err != nil {
		o.fail(intent.task.ID, intent.buildErr+err.Error())
		return
	}
	injectPort(cmd, intent.port)
	o.runWithSelfHeal(ctx, intent.task, intent.dir, intent.agent, cmd, cleanup, intent.runningMsg)
}

func New(svc *core.Service, workdir string, wt *worktree.Manager, term *terminal.Manager, ports *port.Allocator, dev *devserver.Manager, mcpURL string, maxConcurrent int) *Orchestrator {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	o := &Orchestrator{
		svc: svc,
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

// SetPaused holds or releases ALL agents. Pausing sets the flag (so acquire blocks
// every future start — fresh backlog, queued, and out-of-band Revise/Reengage/Rebase
// alike, since they all pass through acquire) AND SIGSTOPs the live sessions so the
// agents already running freeze in place. Resuming SIGCONTs them back to where they
// left off and broadcasts so queued acquirers re-check and start. Either way it
// publishes so open boards sync instantly. Idempotent: setting the same state again
// just re-signals (harmless) and re-broadcasts.
func (o *Orchestrator) SetPaused(p bool) {
	o.mu.Lock()
	o.paused = p
	o.mu.Unlock()
	if p {
		o.term.SuspendAll()
	} else {
		o.term.ResumeAll()
	}
	o.cond.Broadcast()
	o.svc.PublishPaused(p)
}

// Paused reports whether all agents are currently held.
func (o *Orchestrator) Paused() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.paused
}

// acquire blocks until a concurrency slot is free under the current limit and the
// orchestrator isn't paused, then reserves it. release must be called (deferred)
// when the agent is done.
func (o *Orchestrator) acquire() {
	o.mu.Lock()
	for o.active >= o.limit || o.paused {
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
	// Claim it out of backlog synchronously (→ queued, since it may wait for a slot)
	// so the next tick can't re-pick and double-run it. On a failed write it stays in
	// backlog for a later tick, so bail.
	if !o.svc.ClaimTask(t.ID) {
		log.Printf("task %s: could not claim backlog state; will retry next tick", t.ID)
		return
	}

	go func() {
		o.acquire()
		defer o.release()
		o.runClaimed(ctx, t)
	}()
}

// runClaimed drives a task start already claimed out of backlog, now holding a
// slot. It re-reads the task (it may have been stopped or flagged for
// restart-resume during the queued wait) and routes it: resume-in-place, a
// multi-step flow, or a fresh solo run.
func (o *Orchestrator) runClaimed(ctx context.Context, t model.Task) {
	// Re-read: the human may have Stopped it during the (possibly minutes-long) queued
	// wait, and this also picks up any resume marker RecoverInFlight set.
	cur, err := o.svc.GetTask(t.ID)
	if err != nil {
		// Transient read failure — put it back to backlog (guarded, so we can't clobber
		// a concurrent stop) for a later poll; BacklogTasks never re-picks a queued task.
		o.svc.SwapStatus(t.ID, []model.TaskStatus{model.StatusQueued}, model.StatusBacklog)
		return
	}
	if cur.Status != model.StatusQueued {
		return // stopped while queued — don't revive it
	}

	// t.Agent was normalized to an implemented adapter at creation (CreateTaskFull);
	// interactiveAgent applies the belt-and-braces claude fallback.
	ia, err := o.interactiveAgent(cur)
	if err != nil {
		o.fail(cur.ID, err.Error())
		return
	}

	// A daemon restart interrupted this task mid-run: resume IN PLACE (same worktree,
	// no prune; for claude the same conversation via --continue) rather than starting
	// over. One-shot marker — clear it now. A gone worktree falls through to a fresh
	// start; a multi-step flow resumes via its own graph walker at the persisted cursor.
	if cur.Resume {
		o.svc.SetResume(cur.ID, false)
		if cur.Worktree != "" && isDir(cur.Worktree) {
			// A multi-step flow with a still-live cursor resumes through its graph walker.
			// But a flow whose run already COMPLETED and was then re-engaged for a
			// post-review repair (Revise/Rebase) has an empty cursor — routing it into the
			// flow runner would hit its "already completed → nothing to resume" bail and
			// strand the task in queued forever. That repair is a solo conversation, so
			// resume it in place like any solo task (its finish routes back to review).
			if fl := flow.ResolveFor(o.repoPath(cur), cur.Flow); fl.Multi() {
				if run, ok := o.svc.Run(cur.ID); !ok || run.Cursor != "" {
					o.resumeFlowAfterRestart(ctx, cur, fl)
					return
				}
			}
			o.resumeAfterRestart(ctx, cur, ia)
			return
		}
	}

	// Isolated checkout so parallel agents don't collide (shared workdir when the task
	// has no git repo). Kept after the run for review; a retry of the same id reclaims
	// the branch.
	dir := o.prepareWorkdir(cur)

	// Reserve a dev-server port and boot the project's dev hook if any.
	prt := o.setupPort(cur, dir)

	// A multi-step flow walks a graph sharing this worktree; solo stays on the
	// execute() path below. Project .ultraflow/flows.yaml overrides are honored.
	if fl := flow.ResolveFor(o.repoPath(cur), cur.Flow); fl.Multi() {
		o.runFlow(ctx, cur, dir, prt, fl)
		return
	}

	o.execute(ctx, launchIntent{task: cur, dir: dir, agent: ia, prompt: buildPrompt(cur, prt), port: prt, fresh: true,
		runningMsg: "running — open the card to watch progress (Ctrl-C to interrupt)", buildErr: "couldn't build the agent command: "})
}

// resumeAfterRestart re-launches a solo task a daemon restart cut short. It reuses
// the EXISTING worktree (no prune, so uncommitted edits survive) and resumes via
// ResumeCommand — for claude reconnecting the prior conversation (`--continue`) — so
// a restart continues the task instead of starting it over. The dev server, killed
// on shutdown, comes back on its same reserved port. Runs under the normal self-heal.
func (o *Orchestrator) resumeAfterRestart(ctx context.Context, t model.Task, ia interactiveAgent) {
	o.svc.AppendTaskEvent(t.ID, "status",
		"resuming after an Ultraflow restart — same worktree, picking up where it left off")

	prt := o.restorePort(t)
	o.execute(ctx, launchIntent{task: t, dir: t.Worktree, agent: ia, prompt: buildResumePrompt(t, prt), port: prt, fresh: false,
		runningMsg: "resuming after restart (Ctrl-C to interrupt)", buildErr: "couldn't relaunch the agent to resume: "})
}

// resumeFlowAfterRestart resumes a multi-step FLOW task after a restart. Like
// resumeAfterRestart it reuses the worktree and restores the port, but hands off to
// the flow runner, which picks up at the persisted cursor (its step, or the gate it
// was parked at).
func (o *Orchestrator) resumeFlowAfterRestart(ctx context.Context, t model.Task, fl flow.Flow) {
	o.svc.AppendTaskEvent(t.ID, "status",
		"resuming flow after an Ultraflow restart — same worktree, picking up at the current step")
	prt := o.restorePort(t)
	o.runFlow(ctx, t, t.Worktree, prt, fl)
}

// restorePort brings a resumed task's dev server back up on its already-reserved
// port (a restart killed it), reserving a fresh one only if it never had a port.
// Shared by the solo and flow restart-resume paths.
func (o *Orchestrator) restorePort(t model.Task) int {
	if t.Port > 0 {
		if o.ports != nil {
			o.ports.Reserve(t.Port) // idempotent with main.go's startup re-reservation
		}
		o.startDevServer(t, t.Worktree, t.Port)
		return t.Port
	}
	return o.setupPort(t, t.Worktree)
}

// runWithSelfHeal runs a task's agent and, on an unexpected error, auto-retries up
// to the budget — staying `running` with a "fixing itself · k/N" sub-state, resuming
// the same worktree conversation each time — then escalates to a needs_human
// checkpoint. A clean exit resolves to review; a signalled exit is a human stop →
// failed. See spec.md "Failure self-heals".
//
// While its goroutine is live it marks the task `healing`, so an answer-driven
// re-engage stands down and can't race a second agent onto the worktree.
// `cmd`/`cleanup` are the first attempt; further attempts are built here.
func (o *Orchestrator) runWithSelfHeal(ctx context.Context, t model.Task, dir string, ia interactiveAgent, cmd *exec.Cmd, cleanup func(), runningMsg string) {
	o.beginHeal(t.ID)
	defer o.endHeal(t.ID)

	budget := retryBudget(t)
	retries := 0
	o.svc.SetAttempt(t.ID, retries) // 0 = the original run, no sub-state

	// Only claude sessions get the context-cap monitor: it reads Claude Code's
	// transcript format (see watchContext). Resolved once from the concrete adapter,
	// not t.Agent, so a fallback-to-claude is still covered.
	_, isClaude := ia.(*agent.Claude)

	for {
		werr, started := o.runAgent(t.ID, dir, isClaude, cmd, cleanup, runningMsg)
		if !started {
			return // runAgent already failed the task
		}
		if ctx.Err() != nil {
			return // daemon shutting down — startup recovery requeues it
		}
		// finish_task/idle-watcher (→ review) and human Stop (→ cancelled) both Close
		// the session, so the exit looks like a crash. If the task already reached one
		// of those states, it ended on purpose — don't self-heal into a spurious retry.
		if cur, _ := o.svc.GetTask(t.ID); cur.Status == model.StatusReview || cur.Status == model.StatusCancelled {
			return
		}
		if werr == nil {
			// Clean exit — the guarded resolver handles every case, race-safe against a
			// concurrent answer.
			o.svc.ResolveAgentExit(t.ID, false, "")
			return
		}
		if stoppedByHuman(werr) {
			o.fail(t.ID, "you stopped this task") // Ctrl-C = you-said-stop → terminal
			return
		}

		// The agent errored. If the budget is spent, escalate to the human as an
		// ordinary needs_human item — never a raw red dump.
		if retries >= budget {
			o.escalate(t.ID, budget, werr.Error())
			return
		}

		retries++
		// Raw error is a collapsed thread disclosure; the friendly sub-state leads the card.
		o.svc.AppendTaskEvent(t.ID, "error", fmt.Sprintf("attempt failed: %s", truncateErr(werr.Error())))
		o.svc.AbandonRequests(t.ID) // a stale parked checkpoint would linger otherwise
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

// startTurnObservers starts the transcript-backed observers shared by solo runs
// and flow steps. Keeping this wiring in one place ensures every live agent turn
// publishes its concrete model, regardless of which execution path started it.
// Claude additionally exposes context usage through the same transcript.
func (o *Orchestrator) startTurnObservers(sess *terminal.Session, taskID, dir string, isClaude bool) {
	if isClaude {
		go o.watchContext(sess, taskID, dir)
	}
	go o.watchModel(sess, taskID, dir, isClaude)
}

// runAgent runs cmd as the task's live PTY agent for one attempt: register the
// session, flip the card to running only once the terminal exists (never a 404),
// wait, and return the exit error (nil = clean). started is false only when the
// terminal couldn't start, in which case the task is already failed.
func (o *Orchestrator) runAgent(taskID, dir string, isClaude bool, cmd *exec.Cmd, cleanup func(), runningMsg string) (werr error, started bool) {
	defer cleanup()

	sess, err := o.term.Start(taskID, cmd)
	if err != nil {
		o.fail(taskID, "couldn't start the agent terminal: "+err.Error())
		return nil, false
	}
	o.svc.AgentStarted(taskID)
	o.svc.AppendTaskEvent(taskID, "status", runningMsg)
	journal.Log("agent", "start", map[string]any{"task": taskID, "dir": dir, "claude": isClaude})

	// An interactive TUI never exits on its own, so a turn-end without finish_task
	// would hold the slot forever: watchIdle sends an idle turn-end to review and
	// kills the session (which self-heal reads as an intentional end, not a crash).
	go o.watchIdle(sess, taskID, idleTimeout, idlePoll)

	// Publish transcript-backed context/model data for this attempt. A fallback-model
	// retry starts fresh observers and is therefore re-detected.
	o.startTurnObservers(sess, taskID, dir, isClaude)

	werr = sess.Wait()
	if werr != nil {
		log.Printf("task %s: agent exited before finishing: %v", taskID, werr)
	}
	// Journal the exit with its signal disposition so the analysis can finally tell
	// a normal step/idle close (the daemon SIGKILLs the PTY to end a turn) from a
	// human stop (SIGINT/SIGTERM) or a real crash. The event stream just above in
	// the journal ("advancing the flow" / "went idle" / an error) gives the reason.
	fields := map[string]any{"task": taskID, "ok": werr == nil}
	if werr != nil {
		fields["err"] = werr.Error()
		fields["human_stop"] = stoppedByHuman(werr)
	}
	journal.Log("agent", "exit", fields)
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

// isDir reports whether p exists and is a directory — used to confirm a task's
// worktree survived a restart before resuming into it.
func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
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

// retryBudget is a task's self-heal retry ceiling: its own MaxAttempts, or the
// default when unset. The single source for the policy both the solo
// (runWithSelfHeal) and flow (runStep) loops retry against.
func retryBudget(t model.Task) int {
	if t.MaxAttempts < 1 {
		return core.DefaultMaxAttempts
	}
	return t.MaxAttempts
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

// watchIdle sends a task to review and kills the session when its agent goes idle
// without finish_task, freeing the slot. It runs until the session ends.
//
// The guarded FinishForReview swap is what protects the intentional ask_human wait:
// a parked agent is SUPPOSED to idle, but ask_human already moved it to needs_human,
// so running→review fails and we keep watching (a later answer returns it to running,
// where a fresh idle can be caught). The swap also arbitrates an ask_human racing in
// as we act.
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
			if o.svc.FinishForReview(taskID) {
				o.svc.AppendTaskEvent(taskID, "status",
					"agent went idle without calling finish_task — sent to review and freed the slot")
				sess.Close()
				return
			}
		}
	}
}

// Revise re-engages a reviewed/failed task's agent in the SAME worktree with the
// human's feedback (and, via `claude --continue`, its conversation memory), flipping
// the card back to running. This is what makes review a conversation rather than a
// merge-or-nothing dead-end.
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

	ia, err := o.interactiveAgent(t)
	if err != nil {
		return err
	}
	dir := t.Worktree
	if dir == "" {
		dir = o.workdir // ran in place (non-git / shared-workdir project)
	}

	// Flip out of review synchronously so a double-click can't launch two agents.
	if !o.svc.QueueRevision(taskID) {
		return fmt.Errorf("task status changed before it could be queued")
	}
	o.svc.AppendTaskEvent(taskID, "human_answer", feedback)

	// Reuse the first run's port (its dev server stayed up through review).
	prt := t.Port
	if prt == 0 {
		prt = o.setupPort(t, dir)
	}

	o.launch(launchIntent{task: t, dir: dir, agent: ia, prompt: buildRevisePrompt(t, feedback, prt), port: prt,
		runningMsg: "reworking on your feedback (Ctrl-C to interrupt)", buildErr: "couldn't build the agent command: "})
	return nil
}

// Reengage re-launches a task's agent after the human answered its self-heal
// escalation checkpoint, resuming the worktree conversation with the guidance and a
// FRESH retry budget. Driven from AnswerHuman when the checkpoint's agent is no
// longer live. No-op when a self-heal loop still owns the task, so it can't race a
// second agent onto the worktree.
func (o *Orchestrator) Reengage(taskID, guidance string) error {
	if o.isHealing(taskID) {
		return nil
	}
	t, err := o.svc.GetTask(taskID)
	if err != nil {
		return err
	}
	// A flow task answered mid-flow re-enters its graph: a gate answer routes it
	// (resumeGate), a work-step escalation resumes that step (resumeStep). Only a
	// solo task falls through to the conversation resume below.
	if run, ok := o.svc.Run(taskID); ok && run.Cursor != "" {
		fl := flow.ResolveFor(o.repoPath(t), run.Flow)
		if step, ok := fl.Step(run.Cursor); ok {
			if step.Gate {
				return o.resumeGate(t, fl, step, guidance)
			}
			return o.resumeStep(t, fl, step, guidance)
		}
	}

	ia, err := o.interactiveAgent(t)
	if err != nil {
		return err
	}
	dir := t.Worktree
	if dir == "" {
		dir = o.workdir // ran in place (non-git / shared-workdir project)
	}

	o.launch(launchIntent{task: t, dir: dir, agent: ia, prompt: buildReengagePrompt(t, guidance), port: t.Port,
		runningMsg: "back on it with your guidance (Ctrl-C to interrupt)", buildErr: "couldn't relaunch the agent: "})
	return nil
}

// Rebase re-engages a reviewed task's agent to resolve a stale-branch rebase whose
// conflicts the mechanical auto-rebase couldn't handle (core.ErrRebaseConflict). It
// reuses Revise's send-back machinery, so the agent resolves the rebase under the
// same self-heal policy and a clean finish returns the task to review atop main.
func (o *Orchestrator) Rebase(taskID string) error {
	t, err := o.svc.GetTask(taskID)
	if err != nil {
		return err
	}
	if t.Status != model.StatusReview {
		return fmt.Errorf("a rebase self-heal only starts from review (this one is %s)", t.Status)
	}
	if t.Worktree == "" {
		return fmt.Errorf("this task has no worktree to rebase")
	}
	if _, live := o.term.Get(taskID); live {
		return fmt.Errorf("this task already has a live session — type into its terminal instead")
	}

	ia, err := o.interactiveAgent(t)
	if err != nil {
		return err
	}

	// Best-effort freshness figure for the prompt; a lookup failure just yields a
	// generic "behind main" phrasing.
	behind, base := 0, "main"
	if p, perr := o.svc.ProjectByName(t.Project); perr == nil && p.RepoPath != "" {
		if n, b, ferr := o.wt.Freshness(p.RepoPath, taskID); ferr == nil {
			behind = n
			if b != "" {
				base = b
			}
		}
	}

	// Flip out of review synchronously so a double-click can't launch two agents.
	if !o.svc.QueueRebase(taskID) {
		return fmt.Errorf("task status changed before it could be queued")
	}
	o.svc.AppendTaskEvent(taskID, "status", "auto-rebasing onto "+base+" — resolving conflicts")

	o.launch(launchIntent{task: t, dir: t.Worktree, agent: ia, prompt: buildRebasePrompt(t, base, behind), port: t.Port,
		runningMsg: "rebasing onto " + base + " — resolving conflicts (Ctrl-C to interrupt)", buildErr: "couldn't build the agent command: "})
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

// fail marks a task terminally failed with a reason. Reserved for genuine dead-ends
// (couldn't build/start the agent, or a human Ctrl-C) — an agent that ran and
// errored self-heals instead. The guarded swap in FailExecution won't clobber a
// task an answer already moved on.
func (o *Orchestrator) fail(taskID, reason string) {
	o.svc.FailExecution(taskID, reason)
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
	cmd.Env = append(cmd.Env, port.EnvVars(p)...)
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
		t.ID, t.Title, renameTaskContract(t.ID), t.Body, portInstruction(prt), t.ID, screenshotInstruction, t.ID)
}

// buildResumePrompt is seeded when a daemon restart interrupted the task mid-run
// and the orchestrator resumes it in place. For claude the prior conversation is
// restored (`--continue`), so this is a nudge to carry on; for codex the resume
// starts a fresh session, so it re-states the task self-containedly. Either way
// the earlier work is still in the worktree. A restart also cancelled any open
// ask_human, so it tells the agent to re-ask if it was mid-question.
func buildResumePrompt(t model.Task, prt int) string {
	return fmt.Sprintf(`Ultraflow was restarted while you were in the middle of this task, so your live session was interrupted. You are being resumed in the SAME working directory — everything you had already done is still here.

Task ID: %s
Title: %s

%s

Pick up where you left off: first check what you have already changed in this working directory, then carry on and finish the task — don't start over from scratch. If you were waiting on a human answer when the restart happened, that question was cancelled; re-ask via ask_human (task_id="%s") if you still need it.

%s%sWHEN YOU ARE DONE: call the MCP tool "finish_task" with task_id="%s" and a one-line summary.`,
		t.ID, t.Title, t.Body, t.ID, portInstruction(prt), screenshotInstruction+"\n\n", t.ID)
}

// buildRevisePrompt is the message seeded when the human sends a reviewed task
// back for changes. The agent's earlier work is still in the worktree (and, via
// --continue, in its conversation memory), so this re-states the feedback and the
// finish contract. It's self-contained enough to be useful even if the prior
// conversation couldn't be restored.
func buildRevisePrompt(t model.Task, feedback string, prt int) string {
	return fmt.Sprintf(`The human reviewed your work on this Ultraflow task and is sending it back for changes.

Task ID: %s
Title: %s%s

Their feedback:
%s

Your earlier changes are still here in this working directory — review them, then
address the feedback. %s

%sWHEN YOU ARE DONE: call the MCP tool "finish_task" with task_id="%s" and a one-
line summary to send it back to review.`,
		t.ID, t.Title, taskBrief(t), feedback, screenshotInstruction, portInstruction(prt), t.ID)
}

// taskBrief restates a task's full instructions for a re-entry prompt. The
// self-heal / revise / reengage / rebase paths lean on the prior conversation
// still being in memory — true for claude (`--continue`) but NOT for codex, whose
// ResumeCommand starts a FRESH session. Without the body restated, a codex agent
// resuming any of those paths would see only the short title and the immediate
// error/feedback, having lost the actual task. Redundant (harmless) for claude.
// Empty when the task has no body.
func taskBrief(t model.Task) string {
	body := strings.TrimSpace(t.Body)
	if body == "" {
		return ""
	}
	return "\n\nThe task, in full:\n" + body
}

// buildSelfHealPrompt is seeded when the agent's previous run ended in an ERROR and
// the orchestrator resumes it to auto-diagnose. Because it resumes the same
// conversation (`claude --continue`), the agent still remembers what it was doing;
// this re-states the error and the finish contract so the diagnosis is grounded.
func buildSelfHealPrompt(t model.Task, retry, budget int, errText string) string {
	return fmt.Sprintf(`Your last run on this Ultraflow task ended with an ERROR before you finished — the process exited unexpectedly.

Task ID: %s
Title: %s%s

The error:
%s

This is self-heal retry %d of %d. Work out what went wrong, fix the root cause, and
carry on with the task — your earlier work is still here in this working directory.
Don't just repeat what failed; diagnose it first.

WHEN YOU ARE DONE: call the MCP tool "finish_task" with task_id="%s" and a one-line summary.`,
		t.ID, t.Title, taskBrief(t), truncateErr(errText), retry, budget, t.ID)
}

// buildReengagePrompt is seeded when the human answers a self-heal escalation — the
// agent gave up after N tries and asked whether to replan or be guided. It resumes
// the conversation with that answer and the finish contract.
func buildReengagePrompt(t model.Task, guidance string) string {
	return fmt.Sprintf(`You got stuck on this Ultraflow task after several self-heal attempts and asked the human for help. They have responded.

Task ID: %s
Title: %s%s

Their guidance:
%s

Use it to get unstuck. Your earlier work is still here in this working directory. %s

WHEN YOU ARE DONE: call the MCP tool "finish_task" with task_id="%s" and a one-line summary.`,
		t.ID, t.Title, taskBrief(t), guidance, screenshotInstruction, t.ID)
}

// buildRebasePrompt is seeded when a task's branch has fallen behind main and the
// mechanical auto-rebase hit conflicts. The agent's work is still in this worktree
// (and, via --continue, in its memory). It asks the agent to rebase its branch
// onto the latest base and resolve conflicts with the same self-heal policy it
// uses for build/test failures: fix what it can, retry, and only escalate a truly
// unresolvable conflict via ask_human — never leave the branch half-rebased.
func buildRebasePrompt(t model.Task, base string, behind int) string {
	behindStr := "behind the latest " + base
	if behind > 0 {
		behindStr = fmt.Sprintf("%d commit(s) behind the latest %s", behind, base)
	}
	return fmt.Sprintf(`Your branch for this Ultraflow task has fallen %s, and an automatic rebase
hit merge conflicts it could not resolve on its own. Bring the branch up to date
so it can land cleanly.

Task ID: %s
Title: %s%s

Do this in your working directory (your earlier changes are already here):
  1. Run: git rebase %s
  2. Resolve each conflict using your judgment about BOTH your task's intent and
     the changes that landed on %s. Edit the files, then git add them and run
     git rebase --continue. Repeat until the rebase completes.
  3. Re-run the build / tests to confirm your work still holds on top of %s. If
     something broke because of the rebase, fix it — this is the same self-heal
     you'd do for any failure: diagnose and retry, don't give up on the first try.

If you hit a conflict you genuinely cannot resolve safely (a real semantic clash
where you'd only be guessing), do NOT force it and do NOT abort silently. Call
ask_human with task_id="%s", explain the conflict in plain language, and give
options. Then STOP and wait for the answer.

%s

WHEN THE REBASE IS COMPLETE and your work is healthy on top of %s: call the MCP
tool "finish_task" with task_id="%s" and a one-line summary. That returns it to
review, now up to date and ready to merge.`,
		behindStr, t.ID, t.Title, taskBrief(t), base, base, base, t.ID, screenshotInstruction, base, t.ID)
}
