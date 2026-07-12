// Package core holds Ultraflow's business logic: task lifecycle, the blocking
// ask_human protocol, and the SSE event broker. Both the MCP server and the web
// API depend on it.
package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"ultraflow/internal/model"
	"ultraflow/internal/store"
	"ultraflow/internal/worktree"
)

// Service is the central coordinator shared by the MCP server, web API and
// orchestrator.
type Service struct {
	store  *store.Store
	Broker *Broker
	wt     *worktree.Manager // set via UseWorktrees; nil = merge/teardown disabled

	mu      sync.Mutex
	pending map[string]chan string // human_request id -> answer channel
}

// UseWorktrees gives the service the worktree manager it needs to merge a task's
// branch and tear its checkout down. Shares the orchestrator's manager (same
// root), so both agree on where a task's worktree lives.
func (s *Service) UseWorktrees(m *worktree.Manager) { s.wt = m }

func NewService(st *store.Store) *Service {
	return &Service{
		store:   st,
		Broker:  NewBroker(),
		pending: make(map[string]chan string),
	}
}

// NewID returns a short random hex id.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Service) publish(kind string, payload any) {
	msg, _ := json.Marshal(map[string]any{"kind": kind, "data": payload})
	s.Broker.Publish(msg)
}

func (s *Service) appendEvent(taskID, kind, data string) {
	e := model.Event{TaskID: taskID, Kind: kind, Data: data, CreatedAt: time.Now()}
	if id, err := s.store.AppendEvent(e); err == nil {
		e.ID = id
	}
	s.publish("event", e)
}

// --- tasks ---

func (s *Service) CreateTask(title, body, project string) (model.Task, error) {
	return s.CreateTaskFull(title, body, project, "", "")
}

// implementedAgents / implementedFlows are what the orchestrator can actually
// execute today (M0). A task's recorded agent/flow must never claim more than
// that — the board shows the agent's name and colour on the card, so a task that
// really ran Claude must not be stored (and later displayed) as "Codex". Blank or
// not-yet-implemented values normalize to the working defaults.
var implementedAgents = map[string]bool{"claude": true}
var implementedFlows = map[string]bool{"solo": true}

// CreateTaskFull creates a task, defaulting agent and flow when blank and
// normalizing any not-yet-implemented choice to what the orchestrator will
// actually run, so the stored value never misrepresents the execution.
func (s *Service) CreateTaskFull(title, body, project, agent, flow string) (model.Task, error) {
	if !implementedAgents[agent] {
		agent = "claude"
	}
	if !implementedFlows[flow] {
		flow = "solo"
	}
	now := time.Now()
	t := model.Task{
		ID:        NewID(),
		Title:     title,
		Body:      body,
		Project:   project,
		Agent:     agent,
		Flow:      flow,
		Status:    model.StatusBacklog,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.CreateTask(t); err != nil {
		return t, err
	}
	s.publish("task_created", t)
	return t, nil
}

// RecoverInFlight requeues tasks stranded by a previous daemon exit and cancels
// their orphaned human requests (see store.RecoverInFlight). Call once at
// startup, before the orchestrator begins polling. No SSE subscribers exist yet,
// so it only writes; the first board snapshot reflects the recovered state.
func (s *Service) RecoverInFlight() (int64, error) { return s.store.RecoverInFlight() }

func (s *Service) ListTasks() ([]model.Task, error)     { return s.store.ListTasks() }
func (s *Service) BacklogTasks() ([]model.Task, error)  { return s.store.BacklogTasks() }
func (s *Service) GetTask(id string) (model.Task, error) { return s.store.GetTask(id) }

// UpdateStatus persists a task's new status and broadcasts it. It returns the
// store error so callers whose correctness depends on the write (e.g. the
// orchestrator's backlog→queued flip that prevents a re-pick) can react; the
// board is only told about transitions that actually persisted.
func (s *Service) UpdateStatus(id string, st model.TaskStatus) error {
	updatedAt, err := s.store.UpdateTaskStatus(id, st)
	if err != nil {
		return err
	}
	s.publish("task_updated", map[string]any{"taskId": id, "status": st, "updatedAt": updatedAt})
	return nil
}

func (s *Service) SetWorktree(id, wt string) {
	if err := s.store.SetWorktree(id, wt); err != nil {
		return
	}
	s.publish("task_updated", map[string]any{"taskId": id, "worktree": wt})
}

// RetryTask re-queues a task by moving it back to the backlog; the orchestrator
// picks it up on its next tick. Used by the board's "Retry" action on a task the
// agent gave up on (self-heal exhausted). See spec.md "Failure self-heals".
func (s *Service) RetryTask(id string) error {
	t, err := s.store.GetTask(id)
	if err != nil {
		return err
	}
	s.UpdateStatus(t.ID, model.StatusBacklog)
	s.appendEvent(t.ID, "status", "re-queued by human")
	return nil
}

// MergeTask lands a reviewed task's work in the project repo and finishes it:
// commit + merge the task's worktree branch into the repo's checked-out branch,
// then tear the worktree down and mark the task done. A conflict (or any git
// failure) is aborted so the repo stays clean, and the task returns to review
// with the worktree intact so the human can retry or inspect it.
func (s *Service) MergeTask(id string) error {
	t, err := s.store.GetTask(id)
	if err != nil {
		return err
	}
	if t.Status != model.StatusReview {
		return fmt.Errorf("only a task in review can be merged (this one is %s)", t.Status)
	}
	if s.wt == nil || t.Worktree == "" {
		return fmt.Errorf("this task has no worktree to merge")
	}
	p, err := s.store.ProjectByName(t.Project)
	if err != nil || p.RepoPath == "" {
		return fmt.Errorf("couldn't find the project repo to merge into")
	}

	_ = s.UpdateStatus(id, model.StatusMerging)
	s.appendEvent(id, "status", "merging into "+p.Name)

	if _, err := s.wt.Merge(p.RepoPath, id, "Ultraflow: "+t.Title); err != nil {
		_ = s.UpdateStatus(id, model.StatusReview) // keep the worktree for a retry
		s.appendEvent(id, "status", "merge couldn't complete (your repo was left clean): "+err.Error())
		return err
	}

	_ = s.wt.Remove(p.RepoPath, id)
	if err := s.UpdateStatus(id, model.StatusDone); err != nil {
		return err
	}
	s.appendEvent(id, "status", "merged and cleaned up the worktree")
	return nil
}

// AppendTaskEvent records an agent-produced event and fans it out live.
func (s *Service) AppendTaskEvent(taskID, kind, text string) { s.appendEvent(taskID, kind, text) }

// --- projects ---

// projectPalette holds board hues for projects — deliberately distinct from the
// reserved status colors (orange=needs_human, steel=running, moss=done,
// rust=failed) so a project chip never reads as a status.
var projectPalette = []string{
	"#45617D", // slate
	"#7A5C86", // plum
	"#3E6E64", // pine
	"#8A6D3B", // brass
	"#5B6B7A", // steel-gray
	"#6E5773", // mauve
}

func (s *Service) CreateProject(name, repoPath string) (model.Project, error) {
	n, _ := s.store.ProjectCount()
	p := model.Project{
		ID:        NewID(),
		Name:      name,
		RepoPath:  repoPath,
		Color:     projectPalette[n%len(projectPalette)],
		CreatedAt: time.Now(),
	}
	if err := s.store.CreateProject(p); err != nil {
		return p, err
	}
	s.publish("project_created", p)
	return p, nil
}

func (s *Service) ListProjects() ([]model.Project, error) { return s.store.ListProjects() }

// ProjectByName resolves a task's project name to its record (for the repo path
// the worktree manager branches from).
func (s *Service) ProjectByName(name string) (model.Project, error) {
	return s.store.ProjectByName(name)
}

func (s *Service) DeleteProject(id string) error {
	if err := s.store.DeleteProject(id); err != nil {
		return err
	}
	s.publish("project_deleted", map[string]any{"id": id})
	return nil
}

// --- settings ---

// Concurrency bounds: at least one agent, and a ceiling that keeps a single
// subscription from being hammered by too many parallel agents.
const (
	MinConcurrent     = 1
	MaxConcurrentCap  = 8
	settingKeyMaxConc = "max_concurrent"
)

// clampConcurrent forces n into the allowed 1..8 range.
func clampConcurrent(n int) int {
	if n < MinConcurrent {
		return MinConcurrent
	}
	if n > MaxConcurrentCap {
		return MaxConcurrentCap
	}
	return n
}

// GetMaxConcurrent returns the persisted parallel-agent limit and whether one
// was ever set. When unset (ok=false) the caller keeps its own default (the
// -max-concurrent launch flag).
func (s *Service) GetMaxConcurrent() (n int, ok bool, err error) {
	v, present, err := s.store.GetSetting(settingKeyMaxConc)
	if err != nil || !present {
		return 0, false, err
	}
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return 0, false, nil // corrupt value → treat as unset
	}
	return clampConcurrent(n), true, nil
}

// SetMaxConcurrent clamps n to 1..8, persists it, and returns the stored value
// so the caller can echo the effective (clamped) number back to the UI.
func (s *Service) SetMaxConcurrent(n int) (int, error) {
	n = clampConcurrent(n)
	if err := s.store.SetSetting(settingKeyMaxConc, fmt.Sprintf("%d", n)); err != nil {
		return 0, err
	}
	return n, nil
}

// --- the ask_human protocol (the core of Ultraflow) ---

// AskHuman parks the caller until the human answers the request on the board,
// then returns the chosen answer. This is invoked from the MCP tool handler, so
// the agent's tool call blocks for exactly as long as this does.
func (s *Service) AskHuman(ctx context.Context, taskID, question string, options []string, contextStr string) (string, error) {
	req := model.HumanRequest{
		ID:        NewID(),
		TaskID:    taskID,
		Question:  question,
		Options:   options,
		Context:   contextStr,
		Status:    "pending",
		CreatedAt: time.Now(),
	}
	if err := s.store.CreateHumanRequest(req); err != nil {
		return "", err
	}

	// Register the answer channel BEFORE announcing the request, so an answer
	// that races in immediately after the board sees it always finds the channel.
	ch := make(chan string, 1)
	s.mu.Lock()
	s.pending[req.ID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, req.ID)
		s.mu.Unlock()
	}()

	_ = s.UpdateStatus(taskID, model.StatusNeedsHuman)
	s.appendEvent(taskID, "human_request", req.Question)
	s.publish("human_request", req)

	select {
	case <-ctx.Done():
		// The agent went away while parked (process died, MCP connection dropped).
		// Retire the request so it doesn't linger in the attention rail as an
		// unanswerable checkpoint — and so a later human answer can't resurrect a
		// task that has no agent behind it. Mark the task failed; the human can
		// retry it from the board.
		if cancelled, _ := s.store.CancelHumanRequest(req.ID); cancelled {
			s.publish("human_cancelled", map[string]any{"id": req.ID, "taskId": taskID})
			_ = s.UpdateStatus(taskID, model.StatusFailed)
			s.appendEvent(taskID, "status", "agent stopped while awaiting your answer")
		}
		return "", ctx.Err()
	case ans := <-ch:
		return ans, nil
	}
}

// AnswerHuman is called by the web API when the human replies on the board. It
// unblocks the parked AskHuman call.
func (s *Service) AnswerHuman(reqID, answer string) error {
	updated, err := s.store.AnswerHumanRequest(reqID, answer)
	if err != nil {
		return err
	}
	// A duplicate/late answer (already answered, or unknown id) is a no-op: don't
	// re-run side effects or touch the parked channel — the AskHuman caller may
	// already be gone, and a second blocking send would hang this handler.
	if !updated {
		return nil
	}

	if req, err := s.store.GetHumanRequest(reqID); err == nil {
		s.UpdateStatus(req.TaskID, model.StatusRunning)
		s.appendEvent(req.TaskID, "human_answer", answer)
	}
	s.publish("human_answered", map[string]any{"id": reqID, "answer": answer})

	s.mu.Lock()
	ch, ok := s.pending[reqID]
	s.mu.Unlock()
	if ok {
		// Non-blocking: the buffered channel (cap 1) always accepts the first
		// answer; the guard above ensures we only reach here once per request.
		select {
		case ch <- answer:
		default:
		}
	}
	return nil
}

func (s *Service) PendingRequests() ([]model.HumanRequest, error) {
	return s.store.PendingHumanRequests()
}

// TaskEvents returns a task's event timeline (the thread).
func (s *Service) TaskEvents(taskID string) ([]model.Event, error) {
	return s.store.TaskEvents(taskID)
}

// LatestActivity returns the latest activity line per task for the board.
func (s *Service) LatestActivity() (map[string]string, error) {
	return s.store.LatestActivity()
}
