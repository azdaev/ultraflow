package core

import "ultraflow/internal/model"

// taskTransition is the implementation behind the task lifecycle module. It
// keeps guarded persistence and live board publication local to one place.
func (s *Service) taskTransition(id string, from []model.TaskStatus, to model.TaskStatus) bool {
	return s.SwapStatus(id, from, to)
}

// ClaimTask atomically moves a backlog task into the execution queue.
func (s *Service) ClaimTask(id string) bool {
	return s.taskTransition(id, []model.TaskStatus{model.StatusBacklog}, model.StatusQueued)
}

// QueueRevision reserves a reviewed or failed task for another agent run.
func (s *Service) QueueRevision(id string) bool {
	return s.taskTransition(id, []model.TaskStatus{model.StatusReview, model.StatusFailed}, model.StatusQueued)
}

// QueueRebase reserves a reviewed task for conflict repair.
func (s *Service) QueueRebase(id string) bool {
	return s.taskTransition(id, []model.TaskStatus{model.StatusReview}, model.StatusQueued)
}

// AgentStarted records that the terminal exists. Running is accepted because a
// self-heal retry replaces a terminal without leaving the running lifecycle state.
func (s *Service) AgentStarted(id string) bool {
	return s.taskTransition(id, []model.TaskStatus{model.StatusBacklog, model.StatusQueued, model.StatusRunning}, model.StatusRunning)
}

// FinishForReview is the single lifecycle operation used when an agent has
// completed a turn, whether explicitly through MCP or through idle detection.
func (s *Service) FinishForReview(id string) bool {
	if !s.taskTransition(id, []model.TaskStatus{model.StatusQueued, model.StatusRunning}, model.StatusReview) {
		return false
	}
	go s.NoteFreshness(id)
	return true
}

// FailExecution owns the guarded terminal transition and its coupled cleanup.
func (s *Service) FailExecution(id, reason string) bool {
	if !s.taskTransition(id, []model.TaskStatus{model.StatusQueued, model.StatusRunning, model.StatusNeedsHuman}, model.StatusFailed) {
		return false
	}
	s.AbandonRequests(id)
	s.releaseRuntimeByID(id)
	if reason != "" {
		s.appendEvent(id, "error", reason)
	}
	return true
}
