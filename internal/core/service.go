// Package core holds Ultraflow's business logic: task lifecycle, the non-blocking
// ask_human protocol, and the SSE event broker. Both the MCP server and the web
// API depend on it.
package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ultraflow/internal/devserver"
	"ultraflow/internal/model"
	"ultraflow/internal/port"
	"ultraflow/internal/store"
	"ultraflow/internal/worktree"
)

// ErrRebaseConflict is returned by MergeTask when bringing the branch up to date
// with main stops on conflicts the auto-rebase can't resolve mechanically. It's a
// signal (not a dead-end): the caller re-engages the agent to resolve the rebase
// with the same self-heal policy, rather than silently returning to review.
var ErrRebaseConflict = errors.New("branch is behind main and the rebase hit conflicts the agent must resolve")

// termInput delivers a human's board reply into a task's live agent terminal
// (its stdin). *terminal.Manager satisfies it; kept as an interface so core need
// not import the terminal package and tests can stub it.
type termInput interface {
	WriteTo(taskID string, p []byte) (bool, error)
}

// reengager re-launches a task's agent after the human answers a checkpoint whose
// agent is no longer live — the self-heal escalation ("tried N×, stuck"): the
// orchestrator resumes the worktree with the human's guidance seeded. Kept as an
// interface so core need not import the orchestrator; nil in API-only/test setups,
// where an answer to a dead agent is simply a recorded no-op. *Orchestrator
// satisfies it (wired via UseReengager).
type reengager interface {
	Reengage(taskID, guidance string) error
}

// DefaultMaxAttempts is the self-heal retry budget a task gets when its flow
// doesn't pin one. On an agent error the orchestrator auto-diagnoses and re-runs
// the task up to this many times — staying `running` with a "fixing itself · k/N"
// sub-state — before escalating to the human. See spec.md "Failure self-heals;
// it is a card state, not a destination."
const DefaultMaxAttempts = 3

// Service is the central coordinator shared by the MCP server, web API and
// orchestrator.
type Service struct {
	store    *store.Store
	Broker   *Broker
	wt       *worktree.Manager  // set via UseWorktrees; nil = merge/teardown disabled
	term     termInput          // set via UseTerminal; nil = answers aren't delivered
	reengage reengager          // set via UseReengager; nil = escalation answers aren't re-run
	ports    *port.Allocator    // set via UsePorts; nil = no port release
	dev      *devserver.Manager // set via UseDevServer; nil = no dev-server teardown
}

// UseWorktrees gives the service the worktree manager it needs to merge a task's
// branch and tear its checkout down. Shares the orchestrator's manager (same
// root), so both agree on where a task's worktree lives.
func (s *Service) UseWorktrees(m *worktree.Manager) { s.wt = m }

// UseTerminal gives the service the terminal manager it uses to deliver a human's
// answer into the parked agent's stdin. Shares the orchestrator's manager.
func (s *Service) UseTerminal(t termInput) { s.term = t }

// UseReengager wires the orchestrator so an answer to a self-heal escalation (a
// needs_human checkpoint whose agent has already stopped) re-launches the agent
// with the human's guidance instead of stranding the task in `running`.
func (s *Service) UseReengager(r reengager) { s.reengage = r }

// UsePorts / UseDevServer share the orchestrator's port allocator and dev-server
// manager, so the service can free a task's port and stop its dev server when the
// task reaches a terminal state (merged, marked done, or failed).
func (s *Service) UsePorts(p *port.Allocator)        { s.ports = p }
func (s *Service) UseDevServer(d *devserver.Manager) { s.dev = d }

// releaseRuntime frees a finished task's dev-server port and stops its dev
// server. Called only at terminal states — NOT when a task enters review, where
// the port and dev server stay up so the human can open the live app.
func (s *Service) releaseRuntime(t model.Task) {
	if s.dev != nil {
		s.dev.Stop(t.ID)
	}
	if s.ports != nil && t.Port > 0 {
		s.ports.Release(t.Port)
	}
}

func NewService(st *store.Store) *Service {
	return &Service{
		store:  st,
		Broker: NewBroker(),
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
var implementedAgents = map[string]bool{"claude": true, "codex": true}
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
		ID:          NewID(),
		Title:       title,
		Body:        body,
		Project:     project,
		Agent:       agent,
		Flow:        flow,
		Status:      model.StatusBacklog,
		MaxAttempts: DefaultMaxAttempts,
		CreatedAt:   now,
		UpdatedAt:   now,
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

func (s *Service) ListTasks() ([]model.Task, error)      { return s.store.ListTasks() }
func (s *Service) BacklogTasks() ([]model.Task, error)   { return s.store.BacklogTasks() }
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

// SwapStatus is the guarded (compare-and-swap) form of UpdateStatus: it moves a
// task to `to` only if it is currently one of `from`, returning whether it did.
// Used where a concurrent writer may have already moved the task — a human answer
// resuming a parked task vs. the orchestrator failing it because the agent died —
// so the two can't clobber each other into a stranded state.
func (s *Service) SwapStatus(id string, from []model.TaskStatus, to model.TaskStatus) bool {
	ok, err := s.store.SwapStatusFrom(id, from, to)
	if err != nil {
		log.Printf("task %s: swap status → %s: %v", id, to, err)
		return false
	}
	if ok {
		s.publish("task_updated", map[string]any{"taskId": id, "status": to})
	}
	return ok
}

func (s *Service) SetWorktree(id, wt string) {
	if err := s.store.SetWorktree(id, wt); err != nil {
		return
	}
	s.publish("task_updated", map[string]any{"taskId": id, "worktree": wt})
}

// SetAttempt persists a task's self-heal retry counter and broadcasts it, so the
// board can render the "fixing itself · k/N" sub-state on the running card. 0 is
// the original run (no sub-state); k>0 means the agent is on its k-th auto-retry.
func (s *Service) SetAttempt(id string, attempt int) {
	updatedAt, err := s.store.SetTaskAttempt(id, attempt)
	if err != nil {
		log.Printf("task %s: set attempt %d: %v", id, attempt, err)
		return
	}
	s.publish("task_updated", map[string]any{"taskId": id, "attempt": attempt, "updatedAt": updatedAt})
}

// SetPort records (and broadcasts) the dev-server port reserved for a task, so
// the board can show a clickable http://localhost:PORT link on the card.
func (s *Service) SetPort(id string, port int) {
	if err := s.store.SetPort(id, port); err != nil {
		return
	}
	s.publish("task_updated", map[string]any{"taskId": id, "port": port})
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

	// Rebase-then-merge: bring the branch on top of whatever landed on main since
	// it forked, so what merges is what the human reviewed against the latest base
	// (spec.md "Failure self-heals" → stale branch). A clean/no-op rebase falls
	// straight through to the merge; conflicts the auto-rebase can't resolve are
	// escalated to the agent (ErrRebaseConflict), never a silent abort.
	msg := "Ultraflow: " + t.Title
	conflicted, _, rerr := s.wt.Rebase(p.RepoPath, id, msg)
	if rerr != nil {
		_ = s.UpdateStatus(id, model.StatusReview) // keep the worktree for a retry
		s.appendEvent(id, "merge_failed", "couldn't rebase onto main (your repo was left clean): "+rerr.Error())
		return rerr
	}
	if conflicted {
		// Leave it in review; the caller (web) hands the rebase to the agent's
		// self-heal. Record it as "stale" so the card shows the branch needs a rebase.
		_ = s.UpdateStatus(id, model.StatusReview)
		s.appendEvent(id, "stale", "behind main — auto-rebase hit conflicts; handing to the agent to resolve")
		return ErrRebaseConflict
	}

	if _, err := s.wt.Merge(p.RepoPath, id, msg); err != nil {
		_ = s.UpdateStatus(id, model.StatusReview) // keep the worktree for a retry
		// "merge_failed" (not "status") so the board can lift this into the
		// attention rail instead of letting it read as a quiet status line.
		s.appendEvent(id, "merge_failed", "merge couldn't complete (your repo was left clean): "+err.Error())
		return err
	}

	_ = s.wt.Remove(p.RepoPath, id)
	s.releaseRuntime(t) // stop the dev server and free its port now the task is landing
	if err := s.UpdateStatus(id, model.StatusDone); err != nil {
		return err
	}
	s.appendEvent(id, "status", "merged and cleaned up the worktree")
	return nil
}

// FinishReview marks a reviewed task done without a merge. It's for tasks that
// ran in place (a non-git or shared-workdir project has no worktree to land), so
// "merge" is meaningless — the human just confirms the work and closes it out.
func (s *Service) FinishReview(id string) error {
	t, err := s.store.GetTask(id)
	if err != nil {
		return err
	}
	if t.Status != model.StatusReview {
		return fmt.Errorf("only a task in review can be marked done (this one is %s)", t.Status)
	}
	s.releaseRuntime(t) // stop the dev server and free its port now the task is done
	if err := s.UpdateStatus(id, model.StatusDone); err != nil {
		return err
	}
	s.appendEvent(id, "status", "marked done by human")
	return nil
}

// cancellableStatuses are the live/pending states a task can be STOPPED from — it
// has (or is about to have) a running agent. A guarded swap out of one of these
// into `cancelled` is what makes stop race-safe against the agent finishing or
// dying at the same instant. Merging is deliberately excluded: it's a short git
// op we don't interrupt mid-way.
var cancellableStatuses = []model.TaskStatus{
	model.StatusQueued, model.StatusPlanning, model.StatusRunning, model.StatusNeedsHuman,
}

// deletableStatuses are the states a task can be REMOVED from outright: not-yet-
// started (backlog) or terminal (done, failed, cancelled). A live or in-review
// task must be stopped or finished first — its agent and worktree are still in
// play — so those states are absent here.
var deletableStatuses = map[model.TaskStatus]bool{
	model.StatusBacklog:   true,
	model.StatusDone:      true,
	model.StatusFailed:    true,
	model.StatusCancelled: true,
}

// CancelTask stops a running (or queued/parked) task at the human's request. It
// flips the card to `cancelled` with a guarded swap — so it can't clobber a task
// that finished or died in the same instant — retires any pending checkpoint, and
// frees the task's dev-server runtime. It does NOT kill the agent process itself;
// the caller (which owns the terminal manager) closes the live session after this
// returns. Returns whether the task actually transitioned (false → it wasn't in a
// stoppable state, with an explanatory error). The orchestrator's self-heal loop
// reads the `cancelled` status this sets and stands down instead of retrying, so
// closing the session can't be misread as a crash to auto-fix.
func (s *Service) CancelTask(id string) (bool, error) {
	t, err := s.store.GetTask(id)
	if err != nil {
		return false, err
	}
	if !s.SwapStatus(id, cancellableStatuses, model.StatusCancelled) {
		return false, fmt.Errorf("this task isn't running, so there's nothing to stop (it's %s)", t.Status)
	}
	s.AbandonRequests(id)
	s.releaseRuntime(t)
	s.appendEvent(id, "status", "stopped by you")
	return true, nil
}

// DeleteTask removes a task from the board for good: it tears down any leftover
// git worktree, frees any runtime it still held, and deletes the task with its
// events and checkpoints. Only a not-live task can be removed (see
// deletableStatuses) so we never yank a worktree out from under a working agent —
// a running/parked/review task must be stopped or finished first.
func (s *Service) DeleteTask(id string) error {
	t, err := s.store.GetTask(id)
	if err != nil {
		return err
	}
	if !deletableStatuses[t.Status] {
		return fmt.Errorf("a %s task can't be removed — stop or finish it first", t.Status)
	}
	s.teardownWorktree(t)
	s.releaseRuntime(t)
	if err := s.store.DeleteTask(id); err != nil {
		return err
	}
	s.publish("task_deleted", map[string]any{"id": id})
	return nil
}

// teardownWorktree removes a task's leftover git worktree, best-effort. A merged
// task's checkout is already gone (Merge tore it down) and an in-place task never
// had one; this cleans up the rest — e.g. a cancelled task whose worktree we kept
// for inspection. Silent on failure: a missing worktree is not an error here.
func (s *Service) teardownWorktree(t model.Task) {
	if s.wt == nil || t.Worktree == "" {
		return
	}
	p, err := s.store.ProjectByName(t.Project)
	if err != nil || p.RepoPath == "" {
		return
	}
	_ = s.wt.Remove(p.RepoPath, t.ID)
}

// ArchiveClosed removes every closed (done or cancelled) task in one sweep — the
// board's "Clear" affordance, so the Done column doesn't grow without bound over
// a week of use. Returns how many were removed.
func (s *Service) ArchiveClosed() (int, error) {
	tasks, err := s.store.ListTasks()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range tasks {
		if t.Status != model.StatusDone && t.Status != model.StatusCancelled {
			continue
		}
		if err := s.DeleteTask(t.ID); err != nil {
			log.Printf("archive: delete task %s: %v", t.ID, err)
			continue
		}
		n++
	}
	return n, nil
}

// AppendTaskEvent records an agent-produced event and fans it out live.
func (s *Service) AppendTaskEvent(taskID, kind, text string) { s.appendEvent(taskID, kind, text) }

// NoteFreshness checks whether a task's branch has fallen behind main and, if so,
// records a "stale · N behind main" event so the board can warn on the card
// (roadmap M4). Best-effort and side-effect-light: a no-op for tasks with no
// worktree / non-git project, and it never changes status — staleness is a card
// warning, not a state transition. Callers run it when a task enters review (the
// point where landing an out-of-date branch matters); safe to call from a
// goroutine so a slow git call doesn't block the caller.
func (s *Service) NoteFreshness(taskID string) {
	if s.wt == nil {
		return
	}
	t, err := s.store.GetTask(taskID)
	if err != nil || t.Worktree == "" {
		return
	}
	p, err := s.store.ProjectByName(t.Project)
	if err != nil || p.RepoPath == "" {
		return
	}
	behind, _, err := s.wt.Freshness(p.RepoPath, taskID)
	if err != nil || behind == 0 {
		return
	}
	s.appendEvent(taskID, "stale", fmt.Sprintf("stale · %d behind main", behind))
}

// TaskDiff returns the changes a task made in its worktree, for the review diff
// viewer. Requires a worktree manager and a task with a worktree on a registered
// git project (a task that ran in place has nothing isolated to diff).
func (s *Service) TaskDiff(id string) (worktree.DiffResult, error) {
	t, err := s.store.GetTask(id)
	if err != nil {
		return worktree.DiffResult{}, err
	}
	if s.wt == nil || t.Worktree == "" {
		return worktree.DiffResult{}, fmt.Errorf("this task has no worktree to diff")
	}
	p, err := s.store.ProjectByName(t.Project)
	if err != nil || p.RepoPath == "" {
		return worktree.DiffResult{}, fmt.Errorf("couldn't find the project repo to diff against")
	}
	return s.wt.Diff(p.RepoPath, id)
}

// ShotsDir resolves the directory an agent saves screenshots into for a task
// (<worktree>/.ultraflow/shots). Errors when the task ran in place (no worktree
// to hold them), so callers 404 / show an empty gallery.
func (s *Service) ShotsDir(taskID string) (string, error) {
	t, err := s.store.GetTask(taskID)
	if err != nil {
		return "", err
	}
	if t.Worktree == "" {
		return "", fmt.Errorf("no worktree")
	}
	return filepath.Join(t.Worktree, ".ultraflow", "shots"), nil
}

// TaskShots lists the screenshot filenames the agent saved for a task, in
// directory order. Best-effort: a missing dir / no worktree is simply an empty
// gallery, never an error.
func (s *Service) TaskShots(taskID string) []string {
	names := []string{}
	dir, err := s.ShotsDir(taskID)
	if err != nil {
		return names
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return names
	}
	for _, e := range entries {
		if !e.IsDir() && IsImageFile(e.Name()) {
			names = append(names, e.Name())
		}
	}
	return names
}

// IsImageFile reports whether a filename looks like a browser-renderable image,
// so the review/checkpoint galleries only list displayable screenshots.
func IsImageFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// captureContext snapshots the fast context for a task's ask_human checkpoint:
// the worktree's change magnitude (+added −removed and the changed-file list)
// plus any screenshots the agent saved. Best-effort — a task with no worktree
// (M0 shared workdir) or no edits still asks cleanly with zero magnitude.
func (s *Service) captureContext(taskID string) (added, removed int, files []model.ChangedFile, shots []string) {
	files = []model.ChangedFile{}
	if d, err := s.TaskDiff(taskID); err == nil {
		for _, f := range d.Files {
			// Ultraflow's own metadata (screenshots and the like under .ultraflow/)
			// isn't the agent's code change — the shots have their own gallery — so
			// keep it out of the magnitude the human reads as "what changed".
			if strings.HasPrefix(f.Path, ".ultraflow/") {
				continue
			}
			added += f.Added
			removed += f.Removed
			files = append(files, model.ChangedFile{Path: f.Path, Added: f.Added, Removed: f.Removed})
		}
	}
	return added, removed, files, s.TaskShots(taskID)
}

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

// AskHuman posts a question to the board and returns immediately — it does NOT
// block. The agent runs as a live interactive terminal, so after asking it ends
// its turn and idles at its prompt: a durable, tokenless, timeout-proof wait
// (nothing holds an HTTP/tool call open across human time). When the human
// answers on the board, AnswerHuman writes the reply straight into that
// terminal's stdin and the agent resumes from its next input. Returns the
// created request so the caller can tell the agent what it just asked.
func (s *Service) AskHuman(taskID, question string, options []string, contextStr string) (model.HumanRequest, error) {
	added, removed, files, shots := s.captureContext(taskID)
	req := model.HumanRequest{
		ID:        NewID(),
		TaskID:    taskID,
		Question:  question,
		Options:   options,
		Context:   contextStr,
		Status:    "pending",
		Added:     added,
		Removed:   removed,
		Files:     files,
		Shots:     shots,
		CreatedAt: time.Now(),
	}
	if err := s.store.CreateHumanRequest(req); err != nil {
		return model.HumanRequest{}, err
	}
	_ = s.UpdateStatus(taskID, model.StatusNeedsHuman)
	s.appendEvent(taskID, "human_request", req.Question)
	s.publish("human_request", req)
	return req, nil
}

// answerSubmitDelay is the gap between typing a board answer into the agent's
// terminal and sending the Enter that submits it. It must outlast the TUI's
// paste-detection window so the trailing CR is read as a keystroke, not as part
// of a paste (see AnswerHuman). A var so tests can zero it.
var answerSubmitDelay = 250 * time.Millisecond

// AnswerHuman is called by the web API when the human replies on the board. It
// records the answer, returns the task to running, and delivers the reply into
// the agent's live terminal (its stdin) — which is how the parked interactive
// agent, idle at its prompt, receives it and continues.
func (s *Service) AnswerHuman(reqID, answer string) error {
	updated, err := s.store.AnswerHumanRequest(reqID, answer)
	if err != nil {
		return err
	}
	// A duplicate/late answer (already answered, or unknown id) is a no-op: don't
	// re-run side effects or re-inject input into the agent.
	if !updated {
		return nil
	}

	req, err := s.store.GetHumanRequest(reqID)
	if err != nil {
		return err
	}

	// Resume the task only if it is still parked. If it has already left
	// needs_human the agent died and the orchestrator's death path has resolved
	// (or is resolving) it — flipping to running here would strand it behind a
	// dead agent. The guarded swap makes answer-vs-death mutually exclusive.
	if !s.SwapStatus(req.TaskID, []model.TaskStatus{model.StatusNeedsHuman}, model.StatusRunning) {
		s.appendEvent(req.TaskID, "status", "your answer arrived after the agent had already stopped")
		return nil
	}
	s.appendEvent(req.TaskID, "human_answer", answer)
	s.publish("human_answered", map[string]any{"id": reqID, "answer": answer})

	// Deliver the reply into the agent's terminal exactly as if the human typed it
	// there. Crucially the text and the Enter that submits it go as TWO writes with
	// a gap between them: interactive TUIs (Claude Code, Codex) treat text and a
	// trailing CR arriving glued together in one read as a *paste* and keep the CR
	// as a literal newline in the prompt instead of submitting — so the answer would
	// sit typed but un-sent (the "I selected an answer but Enter wasn't pressed"
	// symptom). Newlines are flattened so an embedded one can't submit early. If no
	// live terminal exists the agent exited between the swap and now — the
	// orchestrator's Wait path fails the task — so the log is just a diagnostic.
	if s.term != nil {
		line := strings.NewReplacer("\r", " ", "\n", " ").Replace(answer)
		live, werr := s.term.WriteTo(req.TaskID, []byte(line))
		if werr != nil {
			log.Printf("task %s: delivering answer to terminal: %v", req.TaskID, werr)
		}
		if live {
			// Submit as a separate keystroke once the TUI's paste-detection window
			// has closed, so the lone CR registers as Enter rather than a pasted
			// newline.
			time.Sleep(answerSubmitDelay)
			if _, err := s.term.WriteTo(req.TaskID, []byte("\r")); err != nil {
				log.Printf("task %s: submitting answer to terminal: %v", req.TaskID, err)
			}
		} else {
			// No live agent to take the answer. Normally this is a self-heal
			// escalation ("tried N×, stuck") — the agent stopped after exhausting its
			// retries — so re-launch it in place with the human's guidance rather than
			// stranding the task in `running`. Reengage no-ops if a self-heal loop is
			// still live for the task (an agent that died in the checkpoint gap), so it
			// can never race a second agent onto the same worktree.
			if s.reengage != nil {
				if err := s.reengage.Reengage(req.TaskID, answer); err != nil {
					log.Printf("task %s: re-engage after answer: %v", req.TaskID, err)
				}
			} else {
				log.Printf("task %s: answered but the agent terminal is gone", req.TaskID)
			}
		}
	}
	return nil
}

// AbandonRequests retires any still-pending human request for a task whose agent
// has exited, so an orphaned checkpoint doesn't linger on the attention rail and
// can't be answered into a void.
func (s *Service) AbandonRequests(taskID string) {
	if n, _ := s.store.CancelTaskRequests(taskID); n > 0 {
		s.publish("human_cancelled", map[string]any{"taskId": taskID})
	}
}

// ResolveAgentExit drives a task to a terminal state after its agent PROCESS has
// exited (the orchestrator learns this from sess.Wait). It is the single
// authority for "the agent is gone": finish_task already moved a completed task
// to review, so that no-ops here; anything still in-flight didn't finish and is
// resolved now. crashed is true for a non-zero exit; detail carries the exit
// error for the failed card.
//
// Every transition is a guarded compare-and-swap, so a human answer racing in at
// this instant (needs_human→running) cannot strand the task behind the dead
// agent — note that BOTH the crash swap and the clean-exit fail swap include
// `running`, so an answer that lands in the gap is still driven terminal.
func (s *Service) ResolveAgentExit(taskID string, crashed bool, detail string) {
	if crashed {
		if s.SwapStatus(taskID, []model.TaskStatus{model.StatusQueued, model.StatusRunning, model.StatusNeedsHuman}, model.StatusFailed) {
			s.AbandonRequests(taskID)
			s.releaseRuntimeByID(taskID) // agent's gone; its dev server (if any) is dead — free the port
			reason := "agent exited before reporting completion"
			if detail != "" {
				reason += ": " + detail
			}
			s.appendEvent(taskID, "error", reason)
		}
		return
	}
	// Clean exit (exit 0 without finish_task — e.g. the human ended the session at
	// the prompt): a running/queued agent that finished its turn goes to review.
	if s.SwapStatus(taskID, []model.TaskStatus{model.StatusQueued, model.StatusRunning}, model.StatusReview) {
		go s.NoteFreshness(taskID) // warn on the card if the branch fell behind main
		return
	}
	// Otherwise it was still parked — or a late answer resumed it into `running`
	// behind this now-dead agent — so it never finished: fail it (running included
	// to close the answer-in-the-gap race) and retire the orphaned checkpoint.
	if s.SwapStatus(taskID, []model.TaskStatus{model.StatusNeedsHuman, model.StatusRunning}, model.StatusFailed) {
		s.AbandonRequests(taskID)
		s.releaseRuntimeByID(taskID)
		s.appendEvent(taskID, "error", "agent stopped while awaiting your answer")
	}
}

// releaseRuntimeByID looks up a task and frees its runtime (dev server + port).
// A convenience for the agent-exit paths, which only have the task id.
func (s *Service) releaseRuntimeByID(taskID string) {
	if t, err := s.store.GetTask(taskID); err == nil {
		s.releaseRuntime(t)
	}
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

// LatestActivityKind returns the kind of each task's latest activity line, so the
// board can distinguish an ordinary status line from a "merge_failed" event it
// should raise into the attention rail.
func (s *Service) LatestActivityKind() (map[string]string, error) {
	return s.store.LatestActivityKind()
}
