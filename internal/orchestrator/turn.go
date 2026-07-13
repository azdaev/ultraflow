package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"time"

	"ultraflow/internal/journal"
	"ultraflow/internal/model"
	"ultraflow/internal/terminal"
)

// turnRunner is the seam between orchestration policy and a live coding-agent
// turn. The orchestrator decides which turn to run and what follows; this module
// owns command construction, the PTY, observers, idle completion, and exit
// classification. Tests use a scripted adapter, while terminalTurnRunner is the
// production adapter.
type turnRunner interface {
	Run(context.Context, turnRequest) turnResult
}

type turnCompletion uint8

const (
	turnCompletesTask turnCompletion = iota
	turnCompletesStep
)

type turnRequest struct {
	taskID     string
	dir        string
	agent      interactiveAgent
	prompt     string
	port       int
	resume     bool
	runningMsg string
	completion turnCompletion
	isClaude   bool
}

type turnOutcome uint8

const (
	turnUnknown turnOutcome = iota
	turnCompleted
	turnCrashed
	turnStopped
	turnDaemonDown
	turnLaunchFailed
	turnParked
)

type turnResult struct {
	outcome turnOutcome
	err     error
}

type terminalTurnRunner struct {
	svc     turnState
	term    *terminal.Manager
	observe func(*terminal.Session, string, string, bool)
	timeout time.Duration
	poll    time.Duration
}

// turnState is the narrow durable interface a live turn needs. Keeping it here
// makes the runner independently scriptable without exposing the orchestrator's
// much wider core dependency.
type turnState interface {
	AgentStarted(string) bool
	AppendTaskEvent(string, string, string)
	FinishForReview(string) bool
	SetTurnDone(string, bool) bool
	GetTask(string) (model.Task, error)
	Run(string) (model.Run, bool)
}

func newTerminalTurnRunner(svc turnState, term *terminal.Manager, observe func(*terminal.Session, string, string, bool)) *terminalTurnRunner {
	return &terminalTurnRunner{svc: svc, term: term, observe: observe, timeout: idleTimeout, poll: idlePoll}
}

func (r *terminalTurnRunner) Run(ctx context.Context, req turnRequest) turnResult {
	cmd, cleanup, err := buildTurnCommand(ctx, req)
	if err != nil {
		return turnResult{outcome: turnLaunchFailed, err: fmt.Errorf("build agent command: %w", err)}
	}
	if cleanup != nil {
		defer cleanup()
	}
	injectPort(cmd, req.port)

	sess, err := r.term.Start(req.taskID, cmd)
	if err != nil {
		return turnResult{outcome: turnLaunchFailed, err: fmt.Errorf("start agent terminal: %w", err)}
	}
	// A stop can win after a task acquired its slot but before the PTY existed. Do
	// not leave that newly-created process alive behind a cancelled card.
	if !r.svc.AgentStarted(req.taskID) {
		sess.Close()
		_ = sess.Wait()
		return turnResult{outcome: turnStopped}
	}

	r.svc.AppendTaskEvent(req.taskID, "status", req.runningMsg)
	journal.Log("agent", "start", map[string]any{"task": req.taskID, "dir": req.dir, "claude": req.isClaude})
	go r.watchIdle(sess, req)
	if r.observe != nil {
		r.observe(sess, req.taskID, req.dir, req.isClaude)
	}

	werr := sess.Wait()
	result := r.classify(ctx, req, werr)
	fields := map[string]any{"task": req.taskID, "ok": werr == nil, "outcome": result.outcome.String()}
	if werr != nil {
		fields["err"] = werr.Error()
		fields["human_stop"] = stoppedByHuman(werr)
		log.Printf("task %s: agent turn exited: %v", req.taskID, werr)
	}
	journal.Log("agent", "exit", fields)
	return result
}

func buildTurnCommand(ctx context.Context, req turnRequest) (*exec.Cmd, func(), error) {
	if req.agent == nil {
		return nil, nil, errors.New("no interactive agent configured")
	}
	if req.resume {
		return req.agent.ResumeCommand(ctx, req.dir, req.prompt)
	}
	return req.agent.Command(ctx, req.dir, req.prompt)
}

func (r *terminalTurnRunner) classify(ctx context.Context, req turnRequest, werr error) turnResult {
	if ctx.Err() != nil {
		return turnResult{outcome: turnDaemonDown, err: ctx.Err()}
	}
	task, err := r.svc.GetTask(req.taskID)
	if err != nil {
		return turnResult{outcome: turnLaunchFailed, err: fmt.Errorf("read task state after turn: %w", err)}
	}
	if task.Status == model.StatusCancelled {
		return turnResult{outcome: turnStopped, err: werr}
	}
	// ask_human is a deliberate durable pause. A CLI may remain open or exit after
	// asking; either way the orchestration result is parked, never completed.
	if task.Status == model.StatusNeedsHuman {
		return turnResult{outcome: turnParked, err: werr}
	}
	if req.completion == turnCompletesStep {
		if run, ok := r.svc.Run(req.taskID); ok && run.TurnDone {
			return turnResult{outcome: turnCompleted}
		}
	} else if task.Status == model.StatusReview {
		return turnResult{outcome: turnCompleted}
	}
	if werr == nil {
		return turnResult{outcome: turnCompleted}
	}
	if stoppedByHuman(werr) {
		return turnResult{outcome: turnStopped, err: werr}
	}
	return turnResult{outcome: turnCrashed, err: werr}
}

// watchIdle is the single idle policy for both execution paths. The only
// variation is the durable completion action: a solo turn goes to review, while
// a flow turn marks its current step complete for the graph walker.
func (r *terminalTurnRunner) watchIdle(sess *terminal.Session, req turnRequest) {
	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()
	for {
		select {
		case <-sess.Done():
			return
		case <-ticker.C:
			if sess.IdleFor() < r.timeout {
				continue
			}
			if req.completion == turnCompletesTask {
				if !r.svc.FinishForReview(req.taskID) {
					continue // usually parked on ask_human
				}
				r.svc.AppendTaskEvent(req.taskID, "status", "agent went idle without calling finish_task — sent to review and freed the slot")
			} else {
				cur, _ := r.svc.GetTask(req.taskID)
				if cur.Status != model.StatusRunning {
					continue // parked on a mid-step ask_human
				}
				if !r.svc.SetTurnDone(req.taskID, true) {
					return
				}
				r.svc.AppendTaskEvent(req.taskID, "status", "step ended its turn — advancing the flow")
			}
			sess.Close()
			return
		}
	}
}

func (o turnOutcome) String() string {
	switch o {
	case turnCompleted:
		return "completed"
	case turnCrashed:
		return "crashed"
	case turnStopped:
		return "stopped"
	case turnDaemonDown:
		return "daemon_down"
	case turnLaunchFailed:
		return "launch_failed"
	case turnParked:
		return "parked"
	default:
		return "unknown"
	}
}

func turnErrorDetail(err error) string {
	if err == nil {
		return "agent turn ended without an error detail"
	}
	return err.Error()
}

// retryPlan contains the policy that genuinely varies between solo tasks and
// flow steps. Everything else — attempt accounting, crash classification,
// request retirement, retry budget, and escalation — lives in one place.
type retryPlan struct {
	task             model.Task
	makeTurn         func(retry, budget int, lastErr error) turnRequest
	beforeTurn       func()
	beforeEscalation func()
	failureEvent     func(error) string
	retryStatus      func(retry, budget int) string
}

func (o *Orchestrator) runRetryingTurns(ctx context.Context, plan retryPlan) turnResult {
	budget := retryBudget(plan.task)
	retries := 0
	var lastErr error
	o.svc.SetAttempt(plan.task.ID, 0)

	for {
		if plan.beforeTurn != nil {
			plan.beforeTurn()
		}
		result := o.turns.Run(ctx, plan.makeTurn(retries, budget, lastErr))
		if result.outcome == turnCrashed && result.err == nil {
			result.err = errors.New("agent turn crashed without an error detail")
		}
		if result.outcome != turnCrashed {
			switch result.outcome {
			case turnLaunchFailed:
				o.fail(plan.task.ID, "couldn't launch the agent: "+turnErrorDetail(result.err))
			case turnStopped:
				o.fail(plan.task.ID, "you stopped this task")
			}
			return result
		}

		if retries >= budget {
			if plan.beforeEscalation != nil {
				plan.beforeEscalation()
			}
			o.escalate(plan.task.ID, budget, turnErrorDetail(result.err))
			return turnResult{outcome: turnParked, err: result.err}
		}

		retries++
		message := "attempt failed: " + truncateErr(turnErrorDetail(result.err))
		if plan.failureEvent != nil {
			message = plan.failureEvent(result.err)
		}
		o.svc.AppendTaskEvent(plan.task.ID, "error", message)
		o.svc.AbandonRequests(plan.task.ID)
		o.svc.SetAttempt(plan.task.ID, retries)
		status := fmt.Sprintf("fixing itself · %d/%d — diagnosing the error and retrying", retries, budget)
		if plan.retryStatus != nil {
			status = plan.retryStatus(retries, budget)
		}
		o.svc.AppendTaskEvent(plan.task.ID, "status", status)

		lastErr = result.err
	}
}
