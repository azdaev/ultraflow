package core

import "ultraflow/internal/model"

// The task lifecycle: each operation is a named guarded transition over
// SwapStatus, so the legal from→to moves live in one place and a concurrent writer
// can't clobber them (see SwapStatus).

// ClaimTask atomically moves a backlog task into the execution queue.
func (s *Service) ClaimTask(id string) bool {
	return s.SwapStatus(id, []model.TaskStatus{model.StatusBacklog}, model.StatusQueued)
}

// QueueRevision reserves a reviewed or failed task for another agent run.
func (s *Service) QueueRevision(id string) bool {
	return s.SwapStatus(id, []model.TaskStatus{model.StatusReview, model.StatusFailed}, model.StatusQueued)
}

// QueueRebase reserves a reviewed task for conflict repair.
func (s *Service) QueueRebase(id string) bool {
	return s.SwapStatus(id, []model.TaskStatus{model.StatusReview}, model.StatusQueued)
}

// AgentStarted records that the terminal exists. Running is accepted because a
// self-heal retry replaces a terminal without leaving the running state.
func (s *Service) AgentStarted(id string) bool {
	return s.SwapStatus(id, []model.TaskStatus{model.StatusBacklog, model.StatusQueued, model.StatusRunning}, model.StatusRunning)
}

// FinishForReview sends a completed turn (explicit finish_task or idle detection)
// to review.
func (s *Service) FinishForReview(id string) bool {
	if !s.SwapStatus(id, []model.TaskStatus{model.StatusQueued, model.StatusRunning}, model.StatusReview) {
		return false
	}
	go s.NoteFreshness(id)
	return true
}

// FailExecution is the guarded terminal transition to failed plus its cleanup
// (retire checkpoints, free the runtime, record the reason).
func (s *Service) FailExecution(id, reason string) bool {
	if !s.SwapStatus(id, []model.TaskStatus{model.StatusQueued, model.StatusRunning, model.StatusNeedsHuman}, model.StatusFailed) {
		return false
	}
	s.AbandonRequests(id)
	s.releaseRuntimeByID(id)
	if reason != "" {
		s.appendEvent(id, "error", reason)
	}
	return true
}
