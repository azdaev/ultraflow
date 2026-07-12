// Package orchestrator picks up backlog tasks and runs them through their flow.
// M0 implements only the "solo" flow: one Claude agent in its own git worktree.
// The multi-step flows and other adapters are not wired yet, so task creation
// normalizes any other choice down to claude/solo (see core.CreateTaskFull).
package orchestrator

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"ultraflow/internal/agent"
	"ultraflow/internal/core"
	"ultraflow/internal/model"
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
	baseCtx context.Context // daemon lifetime; used by out-of-band launches (Revise)

	// Resizable concurrency limit. A plain buffered-channel semaphore is fixed at
	// creation; this mutex+cond version lets SetLimit change the ceiling at runtime
	// — raising it wakes queued acquirers immediately, lowering it just stops new
	// starts (already-running agents past the new limit are never killed).
	mu     sync.Mutex
	cond   *sync.Cond
	active int
	limit  int
}

func New(svc *core.Service, workdir string, wt *worktree.Manager, term *terminal.Manager, mcpURL string, maxConcurrent int) *Orchestrator {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	o := &Orchestrator{
		svc:     svc,
		agents:  map[string]agent.Agent{"claude": agent.NewClaude(mcpURL)},
		workdir: workdir,
		wt:      wt,
		term:    term,
		limit:   maxConcurrent,
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

		cmd, cleanup, err := ia.Command(ctx, dir, buildPrompt(t))
		if err != nil {
			o.fail(t.ID, "couldn't build the agent command: "+err.Error())
			return
		}
		o.launch(t.ID, cmd, cleanup, "running — open the card to watch progress (Ctrl-C to interrupt)")
	}()
}

// launch runs cmd as the task's live PTY agent: it registers the session (so the
// board can attach and watch/type/Ctrl-C), flips the card to running only once
// the terminal exists (never a 404), waits for the process to exit, then applies
// the finish semantics. The normal finish is the agent calling finish_task, which
// moves the task to review and closes the session; only when the process exited
// WITHOUT that signal (status still running/queued) do we decide here — a
// non-zero exit is a genuine failure, a clean exit lands in review. Shared by a
// fresh start and a review send-back (Revise).
func (o *Orchestrator) launch(taskID string, cmd *exec.Cmd, cleanup func(), runningMsg string) {
	defer cleanup()

	sess, err := o.term.Start(taskID, cmd)
	if err != nil {
		o.fail(taskID, "couldn't start the agent terminal: "+err.Error())
		return
	}
	o.svc.UpdateStatus(taskID, model.StatusRunning)
	o.svc.AppendTaskEvent(taskID, "status", runningMsg)

	werr := sess.Wait()
	cur, err := o.svc.GetTask(taskID)
	if err == nil && (cur.Status == model.StatusRunning || cur.Status == model.StatusQueued) {
		if werr != nil {
			log.Printf("task %s: agent exited before finishing: %v", taskID, werr)
			o.fail(taskID, "agent exited before reporting completion: "+werr.Error())
		} else {
			o.svc.UpdateStatus(taskID, model.StatusReview)
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

	go func() {
		o.acquire()
		defer o.release()
		cmd, cleanup, err := ia.ResumeCommand(o.ctx(), dir, buildRevisePrompt(t, feedback))
		if err != nil {
			o.fail(taskID, "couldn't build the agent command: "+err.Error())
			return
		}
		o.launch(taskID, cmd, cleanup, "reworking on your feedback (Ctrl-C to interrupt)")
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
// always explains why instead of just showing "Gave up".
func (o *Orchestrator) fail(taskID, reason string) {
	o.svc.AppendTaskEvent(taskID, "error", reason)
	o.svc.UpdateStatus(taskID, model.StatusFailed)
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

// screenshotInstruction tells the agent to leave visual evidence for the review
// screen. Screenshots saved here are served and shown in the task's review, so
// the human can see a visual change without checking the branch out and running
// it. Shared by the initial and the send-back prompts.
const screenshotInstruction = `If you changed anything VISUAL (UI, frontend, layout, styling), before you ` +
	`finish capture screenshots of the affected screens and save them as PNG files ` +
	`under .ultraflow/shots/ in your working directory. The board shows them on the ` +
	`review screen, so the human can see the change without running it.`

func buildPrompt(t model.Task) string {
	return fmt.Sprintf(`You are working on an Ultraflow task.

Task ID: %s
Title: %s

%s

IMPORTANT: You have an MCP tool "ask_human". When a decision is irreversible,
visual, or architectural — or you need the human to review something — do NOT
guess. Call ask_human with task_id="%s", a clear question, suggested options,
and helpful context (a diff, a plan, or a screenshot description). It blocks
until the human replies on the board, then returns their answer.

%s

WHEN YOU ARE DONE: call the MCP tool "finish_task" with task_id="%s" and a one-
line summary. That sends your work to review and ends this session — do not sit
idle at the prompt waiting; call finish_task and stop.`,
		t.ID, t.Title, t.Body, t.ID, screenshotInstruction, t.ID)
}

// buildRevisePrompt is the message seeded when the human sends a reviewed task
// back for changes. The agent's earlier work is still in the worktree (and, via
// --continue, in its conversation memory), so this re-states the feedback and the
// finish contract. It's self-contained enough to be useful even if the prior
// conversation couldn't be restored.
func buildRevisePrompt(t model.Task, feedback string) string {
	return fmt.Sprintf(`The human reviewed your work on this Ultraflow task and is sending it back for changes.

Task ID: %s
Title: %s

Their feedback:
%s

Your earlier changes are still here in this working directory — review them, then
address the feedback. %s

WHEN YOU ARE DONE: call the MCP tool "finish_task" with task_id="%s" and a one-
line summary to send it back to review.`,
		t.ID, t.Title, feedback, screenshotInstruction, t.ID)
}
