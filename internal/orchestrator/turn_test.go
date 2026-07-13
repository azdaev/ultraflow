package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"ultraflow/internal/devserver"
	"ultraflow/internal/model"
	"ultraflow/internal/port"
	"ultraflow/internal/terminal"
	"ultraflow/internal/worktree"
)

// scriptedTurnRunner keeps orchestration-policy tests deterministic and free of
// shells, PTYs, timing, and agent installations. Real terminal integration is
// covered separately by the observer and idle tests.
type scriptedTurnRunner struct {
	results  []turnResult
	requests []turnRequest
}

func (r *scriptedTurnRunner) Run(_ context.Context, req turnRequest) turnResult {
	r.requests = append(r.requests, req)
	if len(r.results) == 0 {
		panic("scriptedTurnRunner: unexpected turn")
	}
	result := r.results[0]
	r.results = r.results[1:]
	return result
}

func TestRetryPolicyIsIndependentOfLiveTerminal(t *testing.T) {
	svc := newTestSvc(t)
	o := New(svc, "/shared", worktree.New(t.TempDir()), terminal.NewManager(), port.NewAllocator(), devserver.NewManager(), "http://mcp", 1)
	task, err := svc.CreateTaskFull("retry", "", "", "claude", "solo")
	if err != nil {
		t.Fatal(err)
	}

	script := &scriptedTurnRunner{results: []turnResult{
		{outcome: turnCrashed, err: errors.New("first crash")},
		{outcome: turnCrashed, err: errors.New("second crash")},
		{outcome: turnCompleted},
	}}
	o.turns = script

	result := o.runRetryingTurns(context.Background(), retryPlan{
		task: task,
		makeTurn: func(retry, budget int, lastErr error) turnRequest {
			last := "none"
			if lastErr != nil {
				last = lastErr.Error()
			}
			return turnRequest{taskID: task.ID, prompt: fmt.Sprintf("%d/%d after %s", retry, budget, last)}
		},
	})

	if result.outcome != turnCompleted {
		t.Fatalf("outcome = %s, want completed", result.outcome.String())
	}
	if len(script.requests) != 3 {
		t.Fatalf("turns = %d, want initial + two retries", len(script.requests))
	}
	wantPrompt := fmt.Sprintf("2/%d after second crash", task.MaxAttempts)
	if got := script.requests[2].prompt; got != wantPrompt {
		t.Fatalf("last retry prompt = %q", got)
	}
	got, _ := svc.GetTask(task.ID)
	if got.Attempt != 2 {
		t.Fatalf("persisted attempt = %d, want 2", got.Attempt)
	}
	if pending, _ := svc.PendingRequests(); len(pending) != 0 {
		t.Fatalf("successful retry must not escalate, pending requests = %d", len(pending))
	}
}

func TestLaunchFailureIsTerminalAndDoesNotRetry(t *testing.T) {
	svc := newTestSvc(t)
	o := New(svc, "/shared", worktree.New(t.TempDir()), terminal.NewManager(), port.NewAllocator(), devserver.NewManager(), "http://mcp", 1)
	task, _ := svc.CreateTaskFull("launch", "", "", "claude", "solo")
	if err := svc.UpdateStatus(task.ID, model.StatusRunning); err != nil {
		t.Fatal(err)
	}
	script := &scriptedTurnRunner{results: []turnResult{{outcome: turnLaunchFailed, err: errors.New("no binary")}}}
	o.turns = script

	result := o.runRetryingTurns(context.Background(), retryPlan{
		task:     task,
		makeTurn: func(int, int, error) turnRequest { return turnRequest{taskID: task.ID} },
	})

	if result.outcome != turnLaunchFailed || len(script.requests) != 1 {
		t.Fatalf("result=%s turns=%d, want one launch failure", result.outcome.String(), len(script.requests))
	}
	got, _ := svc.GetTask(task.ID)
	if got.Status != model.StatusFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
	if pending, _ := svc.PendingRequests(); len(pending) != 0 {
		t.Fatalf("infrastructure failure must not create a self-heal checkpoint")
	}
}

func TestHumanCheckpointClassifiesAsParkedEvenOnCleanExit(t *testing.T) {
	svc := newTestSvc(t)
	task, _ := svc.CreateTaskFull("question", "", "", "claude", "solo")
	if _, err := svc.AskHuman(task.ID, "Which way?", nil, ""); err != nil {
		t.Fatal(err)
	}
	runner := newTerminalTurnRunner(svc, terminal.NewManager(), nil)

	result := runner.classify(context.Background(), turnRequest{taskID: task.ID}, nil)
	if result.outcome != turnParked {
		t.Fatalf("outcome = %s, want parked", result.outcome.String())
	}
	got, _ := svc.GetTask(task.ID)
	if got.Status != model.StatusNeedsHuman {
		t.Fatalf("classification changed parked task to %s", got.Status)
	}
}
