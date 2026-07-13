package core

import (
	"fmt"
	"log"

	"ultraflow/internal/flow"
	"ultraflow/internal/model"
)

// This file holds the service-side of the multi-step flow engine: the run
// lifecycle the orchestrator drives, and the board-facing progress the card's
// stepper renders. A run exists ONLY for a multi-step flow — a solo task has none,
// which is how CompleteTurn tells the two apart and keeps solo on its unchanged
// single-agent path.

// StartRun begins a task's flow at the given start step and publishes its
// progress so the board lights the first step immediately.
func (s *Service) StartRun(taskID, flowKey, startStep string) error {
	if err := s.store.CreateRun(taskID, flowKey, startStep); err != nil {
		return err
	}
	s.publishRun(taskID)
	return nil
}

// Run returns a task's flow run and whether it has one (solo tasks don't).
func (s *Service) Run(taskID string) (model.Run, bool) {
	r, ok, err := s.store.GetRun(taskID)
	if err != nil {
		log.Printf("task %s: get run: %v", taskID, err)
		return model.Run{}, false
	}
	return r, ok
}

// SetRunCursor points a run at a step without recording a completion — used when
// the orchestrator (re)enters a step, resuming after a restart or routing a gate.
func (s *Service) SetRunCursor(taskID, cursor string) {
	if err := s.store.SetRunCursor(taskID, cursor); err != nil {
		log.Printf("task %s: set run cursor: %v", taskID, err)
		return
	}
	s.publishRun(taskID)
}

func (s *Service) SetRunPhase(taskID string, phase model.RunPhase) {
	if err := s.store.SetRunPhase(taskID, phase); err != nil {
		log.Printf("task %s: set run phase: %v", taskID, err)
	}
}

// AdvanceRun records the step just finished and moves the cursor to the next one,
// then publishes the new progress. completedStep is the step left; next is the
// step entered.
func (s *Service) AdvanceRun(taskID, completedStep, next string) {
	if err := s.store.AdvanceRun(taskID, completedStep, next); err != nil {
		log.Printf("task %s: advance run: %v", taskID, err)
		return
	}
	s.publishRun(taskID)
}

// SetTurnDone flips the transient per-step flag the orchestrator reads after a
// step agent exits (true = the agent ended its turn deliberately, so advance;
// false = the fresh-step default set when a step (re)starts).
func (s *Service) SetTurnDone(taskID string, done bool) bool {
	changed, err := s.store.SetRunTurnDone(taskID, done)
	if err != nil {
		log.Printf("task %s: set turn done: %v", taskID, err)
	}
	return changed
}

// FinishFlow completes a multi-step flow: the cursor is cleared (every step done)
// and the task goes to review — the same destination as a solo finish, reached
// from the flow's terminal step or an approve at its final gate. It uses the same
// guarded FinishForReview transition as solo, so a task cancelled in the same
// instant isn't revived into review.
func (s *Service) FinishFlow(taskID string) error {
	if err := s.store.SetRunCursor(taskID, ""); err != nil {
		log.Printf("task %s: finish flow cursor: %v", taskID, err)
	}
	s.publishRun(taskID)
	s.SetRunPhase(taskID, model.RunComplete)
	if s.FinishForReview(taskID) {
		s.appendEvent(taskID, "result", "flow complete — sent to review")
	}
	return nil
}

// CompleteTurn is finish_task's entry point. It records the agent's report and
// one-line result, then routes on whether the task is running a multi-step flow:
//
//   - No run (a solo task): the guarded finish to review (FinishForReview) — the
//     unchanged solo path; a task no longer running is rejected.
//   - An active run exists (a flow step): the orchestrator's flow runner owns the
//     transition, so we only mark the step's turn done and leave the status alone.
//     On the agent's exit the runner advances the graph, or finishes to review at
//     a terminal step. Not touching status here is what stops the card flashing to
//     review between steps.
//   - A completed run exists: this is a post-review repair (revision or conflict
//     rebase), not another flow step. Finish directly back to review while keeping
//     the completed run as historical progress.
//
// The caller (mcp finish_task) closes the live session regardless.
func (s *Service) CompleteTurn(taskID, summary, report string) error {
	if report != "" {
		s.appendEvent(taskID, "report", report)
	}
	if summary == "" {
		summary = "agent reported the step complete"
	}
	s.appendEvent(taskID, "result", summary)

	if run, ok := s.Run(taskID); ok && run.Phase != model.RunComplete {
		if !s.SetTurnDone(taskID, true) {
			return fmt.Errorf("flow step is not active")
		}
		return nil
	}
	if !s.FinishForReview(taskID) {
		return fmt.Errorf("task is no longer running")
	}
	return nil
}

// RunsProgress builds the board-facing flow progress for the given tasks (those
// with a run), keyed by task id — one query plus a per-task graph lookup, for the
// board snapshot.
func (s *Service) RunsProgress(tasks []model.Task) map[string]model.RunProgress {
	out := map[string]model.RunProgress{}
	ids := make([]string, 0, len(tasks))
	agents := make(map[string]string, len(tasks))
	projects := make(map[string]string, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
		agents[t.ID] = t.Agent
		projects[t.ID] = t.Project
	}
	runs, err := s.store.RunsForTasks(ids)
	if err != nil {
		log.Printf("runs progress: %v", err)
		return out
	}
	repos := s.repoByProject() // one query, not one ProjectByName per task
	for id, r := range runs {
		out[id] = buildProgress(r, agents[id], repos[projects[id]])
	}
	return out
}

// publishRun fans a single task's flow progress out over SSE, so the card's
// stepper moves live as the orchestrator walks the graph.
func (s *Service) publishRun(taskID string) {
	r, ok := s.Run(taskID)
	if !ok {
		return
	}
	t, err := s.store.GetTask(taskID)
	agent, repo := "", ""
	if err == nil {
		agent = t.Agent
		repo = s.repoFor(t.Project)
	}
	p := buildProgress(r, agent, repo)
	s.publish("run_updated", map[string]any{"taskId": taskID, "progress": p})
}

// buildProgress turns a stored run into the card's RunProgress view, resolving the
// project's flow (honoring any .ultraflow/flows.yaml override) to derive the
// active step's index, sub-agent, gate-ness, and caption.
func buildProgress(r model.Run, taskAgent, repoPath string) model.RunProgress {
	f := flow.ResolveFor(repoPath, r.Flow)
	p := model.RunProgress{
		Flow:    r.Flow,
		Step:    r.Cursor,
		Index:   f.IndexOf(r.Cursor),
		Total:   len(f.Steps),
		Agent:   taskAgent,
		Caption: f.Caption(r.Cursor),
	}
	if step, ok := f.Step(r.Cursor); ok {
		p.Gate = step.Gate
		if step.Agent != "" {
			p.Agent = step.Agent
		}
	}
	return p
}

// repoFor resolves a project name to its git repo path (for flow override
// loading), or "" when the task has no registered project.
func (s *Service) repoFor(project string) string {
	if project == "" {
		return ""
	}
	p, err := s.store.ProjectByName(project)
	if err != nil {
		return ""
	}
	return p.RepoPath
}

// repoByProject returns a name→repoPath map of every project, so a batch caller
// (RunsProgress over the whole board) resolves flow-override repos in one query
// instead of a ProjectByName per task. Nil on error — callers read "" per missing
// key, the same fallback as repoFor.
func (s *Service) repoByProject() map[string]string {
	ps, err := s.store.ListProjects()
	if err != nil {
		log.Printf("runs progress: list projects: %v", err)
		return nil
	}
	m := make(map[string]string, len(ps))
	for _, p := range ps {
		m[p.Name] = p.RepoPath
	}
	return m
}
