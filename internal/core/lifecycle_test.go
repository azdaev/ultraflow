package core

import (
	"testing"

	"ultraflow/internal/model"
)

func TestTaskLifecycleModule(t *testing.T) {
	svc := newTestService(t)
	task, err := svc.CreateTask("lifecycle", "", "")
	if err != nil {
		t.Fatal(err)
	}

	if !svc.ClaimTask(task.ID) || svc.ClaimTask(task.ID) {
		t.Fatal("claim must move backlog to queued exactly once")
	}
	if !svc.AgentStarted(task.ID) {
		t.Fatal("queued task should start")
	}
	if svc.FinishForReview(task.ID) {
		t.Fatal("review must reject a turn with no report handoff")
	}
	if err := svc.store.SetHandoff(task.ID, true); err != nil {
		t.Fatal(err)
	}
	if !svc.FinishForReview(task.ID) || svc.FinishForReview(task.ID) {
		t.Fatal("finish must move running to review exactly once")
	}
	got, err := svc.GetTask(task.ID)
	if err != nil || got.Status != model.StatusReview {
		t.Fatalf("status = %s, err = %v; want review", got.Status, err)
	}
	if !svc.QueueRevision(task.ID) {
		t.Fatal("reviewed task should queue for revision")
	}
	if !svc.FailExecution(task.ID, "launch failed") {
		t.Fatal("queued execution should fail atomically")
	}
	got, _ = svc.GetTask(task.ID)
	if got.Status != model.StatusFailed {
		t.Fatalf("status = %s; want failed", got.Status)
	}
}
