package orchestrator

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"time"

	"ultraflow/internal/core"
	"ultraflow/internal/flow"
	"ultraflow/internal/model"
	"ultraflow/internal/terminal"
)

// This file is the multi-step flow runner. Where the solo path (orchestrator.go)
// runs ONE agent to completion, a flow WALKS A GRAPH of steps that all share the
// task's single worktree: a work step spawns its agent, waits for the agent to end
// its turn, then advances along the graph; a gate step parks the task for a human
// decision that routes the graph. The run cursor is persisted every step, so a
// daemon restart re-picks the task and resumes at the step it was on rather than
// walking from the top. Solo tasks never enter here — they keep their unchanged,
// battle-tested path — which is what guarantees the default can't regress.

// stepOutcome is why a work step's turn ended, deciding what the walk does next.
type stepOutcome int

const (
	stepDone       stepOutcome = iota // agent ended its turn cleanly → advance the graph
	stepStopped                       // human stopped the task (Ctrl-C / cancel) → terminal
	stepHalted                        // task parked/failed (escalation, infra failure) → stop walking
	stepDaemonDown                    // daemon shutting down → startup recovery will resume
)

// runFlow drives a task through a multi-step flow. It owns the task end-to-end
// like runWithSelfHeal, so it marks the task `healing` while live: that stands the
// answer-driven re-engage down, so a mid-flow crash can't race a second walker
// onto the shared worktree. It resumes from a persisted cursor when one exists (a
// restart re-picked the task), else starts a fresh run at the flow's start step.
func (o *Orchestrator) runFlow(ctx context.Context, t model.Task, dir string, prt int, fl flow.Flow) {
	o.beginHeal(t.ID)
	defer o.endHeal(t.ID)

	cursor := fl.Start
	if run, ok := o.svc.Run(t.ID); ok {
		if run.Cursor == "" {
			return // the run already completed — nothing to resume
		}
		cursor = run.Cursor
		o.svc.AppendTaskEvent(t.ID, "status", "resuming flow at the "+stepRole(fl, cursor)+" step")
	} else {
		o.svc.StartRun(t.ID, fl.Key, fl.Start)
	}
	o.walkFlow(ctx, t, dir, prt, fl, cursor, "", false)
}

// walkFlow is the graph loop, shared by a fresh run and by the answer-driven
// re-entries (a gate reroute, a step-escalation resume). seed/resume apply ONLY to
// the entry step: seed is optional guidance to fold into its prompt, and resume
// picks up that step's prior conversation (`--continue`) instead of a fresh one.
func (o *Orchestrator) walkFlow(ctx context.Context, t model.Task, dir string, prt int, fl flow.Flow, cursor, seed string, resume bool) {
	for {
		if ctx.Err() != nil {
			return
		}
		step, ok := fl.Step(cursor)
		if !ok {
			// A cursor with no step (corrupt/renamed flow) — finish safely to review
			// rather than spin.
			_ = o.svc.FinishFlow(t.ID)
			return
		}
		o.svc.SetRunCursor(t.ID, cursor)

		if step.Gate {
			o.openGate(t, fl, step)
			return // parked for the human; an answer re-enters via resumeGate
		}

		outcome := o.runStep(ctx, t, dir, prt, fl, step, seed, resume)
		seed, resume = "", false // consumed by the entry step only

		switch outcome {
		case stepStopped, stepHalted, stepDaemonDown:
			return
		case stepDone:
			next := step.DefaultNext()
			if next == "" {
				_ = o.svc.FinishFlow(t.ID) // terminal step → review
				return
			}
			o.svc.AdvanceRun(t.ID, step.ID, next)
			cursor = next
		}
	}
}

// runStep runs one work step's agent for a single turn, with in-step self-heal on
// a crash (retry the step up to the budget, then escalate as a needs_human item).
// It returns why the turn ended. A clean turn end — the agent called finish_task,
// idled out, or its process exited 0 — is stepDone: the caller advances the graph.
func (o *Orchestrator) runStep(ctx context.Context, t model.Task, dir string, prt int, fl flow.Flow, step flow.Step, seed string, resume bool) stepOutcome {
	ia, ok := o.stepAgent(t, step)
	if !ok {
		o.fail(t.ID, "no runnable interactive agent for the "+step.Role+" step")
		return stepHalted
	}

	budget := t.MaxAttempts
	if budget < 1 {
		budget = core.DefaultMaxAttempts
	}
	retries := 0
	o.svc.SetAttempt(t.ID, 0)
	caption := fl.Caption(step.ID)

	for {
		// Fresh per-turn: a step is done only once finish_task or an idle turn-end
		// flips this. A bare crash leaves it false, which is how we tell the two apart.
		o.svc.SetTurnDone(t.ID, false)

		var (
			cmd     *exec.Cmd
			cleanup func()
			err     error
		)
		switch {
		case retries == 0 && resume:
			// Re-entering this step after a human answered its escalation: keep the
			// agent's memory via --continue and seed the guidance.
			cmd, cleanup, err = ia.ResumeCommand(ctx, dir, o.buildStepReengagePrompt(t, step, seed))
		case retries == 0:
			cmd, cleanup, err = ia.Command(ctx, dir, o.buildStepPrompt(t, fl, step, prt, seed))
		default:
			cmd, cleanup, err = ia.ResumeCommand(ctx, dir, o.buildStepSelfHealPrompt(t, step, retries, budget))
		}
		if err != nil {
			o.fail(t.ID, "couldn't build the agent command: "+err.Error())
			return stepHalted
		}
		injectPort(cmd, prt)

		werr, started := o.runStepTurn(t.ID, cmd, cleanup, caption)
		if !started {
			return stepHalted // runStepTurn already failed the task
		}
		if ctx.Err() != nil {
			return stepDaemonDown
		}

		// A clean turn end: finish_task / idle set turn_done, or the process exited 0.
		if run, _ := o.svc.Run(t.ID); run.TurnDone || werr == nil {
			return stepDone
		}
		// The human stopped the task while the step ran.
		if cur, _ := o.svc.GetTask(t.ID); cur.Status == model.StatusCancelled {
			return stepStopped
		}
		if stoppedByHuman(werr) {
			o.gaveUp(t.ID, "you stopped this task")
			return stepStopped
		}

		// A genuine crash. Retry the step in place (resuming its conversation) up to
		// the budget, then escalate to the human as an ordinary checkpoint.
		if retries >= budget {
			o.escalate(t.ID, budget, werr.Error())
			return stepHalted
		}
		retries++
		o.svc.AppendTaskEvent(t.ID, "error", fmt.Sprintf("%s step failed: %s", step.Role, truncateErr(werr.Error())))
		o.svc.AbandonRequests(t.ID)
		o.svc.SetAttempt(t.ID, retries)
		o.svc.AppendTaskEvent(t.ID, "status",
			fmt.Sprintf("fixing itself · %d/%d — diagnosing the %s step and retrying", retries, budget, step.Role))
	}
}

// runStepTurn runs cmd as the step's live PTY agent for one turn. Unlike the solo
// runAgent it does NOT drive the task to review on a turn end — the flow runner
// owns that decision — it only flips the card to running and waits. started is
// false only when the terminal couldn't start (the task is already failed).
func (o *Orchestrator) runStepTurn(taskID string, cmd *exec.Cmd, cleanup func(), runningMsg string) (werr error, started bool) {
	defer cleanup()

	sess, err := o.term.Start(taskID, cmd)
	if err != nil {
		o.fail(taskID, "couldn't start the agent terminal: "+err.Error())
		return nil, false
	}
	// Guarded running transition (same as the solo runAgent): a task cancelled in
	// the gap between acquiring a slot and starting isn't revived — runStep sees the
	// cancelled status after the turn and stops walking.
	o.svc.AgentStarted(taskID)
	o.svc.AppendTaskEvent(taskID, "status", runningMsg)

	// End a bare turn (agent idled at its prompt without finish_task) so the step
	// advances instead of pinning here forever. Unlike the solo watcher it marks the
	// turn done and closes the session rather than sending the task to review.
	go o.watchStepIdle(sess, taskID)

	werr = sess.Wait()
	return werr, true
}

// watchStepIdle ends a step's turn when its agent goes idle at the prompt without
// calling finish_task: it marks the turn done and closes the session, so the flow
// runner advances. It must NOT act while the agent is legitimately parked on its
// OWN ask_human (status needs_human) — that agent is supposed to idle; a later
// answer returns it to running, where a fresh idle can be caught.
func (o *Orchestrator) watchStepIdle(sess *terminal.Session, taskID string) {
	ticker := time.NewTicker(idlePoll)
	defer ticker.Stop()
	for {
		select {
		case <-sess.Done():
			return
		case <-ticker.C:
			if sess.IdleFor() < idleTimeout {
				continue
			}
			if cur, _ := o.svc.GetTask(taskID); cur.Status != model.StatusRunning {
				continue // parked on a mid-step ask_human — leave it be
			}
			o.svc.SetTurnDone(taskID, true)
			o.svc.AppendTaskEvent(taskID, "status", "step ended its turn — advancing the flow")
			sess.Close()
			return
		}
	}
}

// openGate parks a task at a human gate: it posts the gate's question to the board
// (needs_human) with the flow context, then returns so the walker's goroutine ends
// and frees its slot. The previous work step's agent has already exited, so there
// is no live terminal — the answer re-enters the flow via resumeGate (AnswerHuman →
// Reengage), routing the graph by the human's choice.
func (o *Orchestrator) openGate(t model.Task, fl flow.Flow, step flow.Step) {
	q := step.Prompt
	if q == "" {
		q = "This step is done — approve to continue to the next step, or send it back for changes."
	}
	context := fmt.Sprintf("Flow: %s — %s\nApproving continues to the next step (the default). Your answer routes what happens next.",
		fl.Label, fl.Caption(step.ID))
	if _, err := o.svc.AskHuman(t.ID, q, step.GateOptions(), context); err != nil {
		log.Printf("task %s: open gate %s: %v", t.ID, step.ID, err)
	}
}

// resumeGate continues a flow after the human answers a gate: it routes the gate
// by the answer and either finishes the flow (approve at a terminal gate → review)
// or re-enters the graph at the routed step, seeding the answer into that step's
// prompt (so a "send back" carries the human's ask into the rebuild). Runs on a
// fresh concurrency slot — the parked gate released the previous one.
func (o *Orchestrator) resumeGate(t model.Task, fl flow.Flow, gate flow.Step, answer string) error {
	next := gate.Route(answer)
	if next == "" {
		return o.svc.FinishFlow(t.ID) // approved at the final gate → review
	}
	o.svc.AdvanceRun(t.ID, gate.ID, next)
	dir := o.flowDir(t)
	go func() {
		o.acquire()
		defer o.release()
		o.beginHeal(t.ID)
		defer o.endHeal(t.ID)
		o.walkFlow(o.ctx(), t, dir, t.Port, fl, next, answer, false)
	}()
	return nil
}

// resumeStep re-enters a work step after the human answered its self-heal
// escalation ("tried N×, stuck"): it resumes that step's conversation with the
// guidance and carries on walking the flow from there, rather than the solo
// resume-to-review path (which would abandon the remaining steps).
func (o *Orchestrator) resumeStep(t model.Task, fl flow.Flow, step flow.Step, guidance string) error {
	dir := o.flowDir(t)
	go func() {
		o.acquire()
		defer o.release()
		o.beginHeal(t.ID)
		defer o.endHeal(t.ID)
		o.walkFlow(o.ctx(), t, dir, t.Port, fl, step.ID, guidance, true)
	}()
	return nil
}

// flowDir resolves where a flow task's steps run: its shared worktree, falling
// back to the daemon workdir for a task that ran in place (non-git project).
func (o *Orchestrator) flowDir(t model.Task) string {
	if t.Worktree != "" {
		return t.Worktree
	}
	return o.workdir
}

// stepAgent resolves the interactive adapter a step runs on: the step's own agent
// override if set, else the task's agent, else claude. Returns false only if the
// resolved adapter can't run as an interactive terminal.
func (o *Orchestrator) stepAgent(t model.Task, step flow.Step) (interactiveAgent, bool) {
	name := step.Agent
	if name == "" {
		name = t.Agent
	}
	ag := o.agents[name]
	if ag == nil {
		ag = o.agents["claude"]
	}
	ia, ok := ag.(interactiveAgent)
	return ia, ok
}

// stepRole is a small helper for status text: a step's human-facing role, or its
// id when the step is unknown.
func stepRole(fl flow.Flow, stepID string) string {
	if s, ok := fl.Step(stepID); ok && s.Role != "" {
		return s.Role
	}
	return stepID
}
