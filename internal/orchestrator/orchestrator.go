// Package orchestrator picks up backlog tasks and runs them through their flow.
// M0 implements only the "solo" flow: one Claude agent in its own git worktree.
// The multi-step flows and other adapters are not wired yet, so task creation
// normalizes any other choice down to claude/solo (see core.CreateTaskFull).
package orchestrator

import (
	"context"
	"fmt"
	"log"
	"time"

	"ultraflow/internal/agent"
	"ultraflow/internal/core"
	"ultraflow/internal/model"
	"ultraflow/internal/worktree"
)

type Orchestrator struct {
	svc     *core.Service
	agents  map[string]agent.Agent
	workdir string
	wt      *worktree.Manager
	sem     chan struct{}
}

func New(svc *core.Service, workdir string, wt *worktree.Manager, mcpURL string, maxConcurrent int) *Orchestrator {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Orchestrator{
		svc:     svc,
		agents:  map[string]agent.Agent{"claude": agent.NewClaude(mcpURL)},
		workdir: workdir,
		wt:      wt,
		sem:     make(chan struct{}, maxConcurrent),
	}
}

// Run polls the backlog and starts tasks until ctx is cancelled.
func (o *Orchestrator) Run(ctx context.Context) {
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
		o.sem <- struct{}{}
		defer func() { <-o.sem }()

		// Slot acquired — now it is genuinely running.
		o.svc.UpdateStatus(t.ID, model.StatusRunning)

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

		out := make(chan agent.Event, 64)
		go func() {
			for ev := range out {
				o.svc.AppendTaskEvent(t.ID, ev.Kind, ev.Text)
			}
		}()

		err := ag.Run(ctx, dir, buildPrompt(t), out)
		close(out)

		if err != nil {
			log.Printf("task %s failed: %v", t.ID, err)
			o.svc.UpdateStatus(t.ID, model.StatusFailed)
			return
		}
		o.svc.UpdateStatus(t.ID, model.StatusReview)
	}()
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

func buildPrompt(t model.Task) string {
	return fmt.Sprintf(`You are working on an Ultraflow task.

Task ID: %s
Title: %s

%s

IMPORTANT: You have an MCP tool "ask_human". When a decision is irreversible,
visual, or architectural — or you need the human to review something — do NOT
guess. Call ask_human with task_id="%s", a clear question, suggested options,
and helpful context (a diff, a plan, or a screenshot description). It blocks
until the human replies on the board, then returns their answer.`,
		t.ID, t.Title, t.Body, t.ID)
}
