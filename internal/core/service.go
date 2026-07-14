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
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ultraflow/internal/broker"
	"ultraflow/internal/devserver"
	"ultraflow/internal/flow"
	"ultraflow/internal/journal"
	"ultraflow/internal/model"
	"ultraflow/internal/port"
	"ultraflow/internal/worktree"
)

// ErrRebaseConflict is returned by MergeTask when the pre-merge rebase hits
// conflicts git can't resolve mechanically — a signal to re-engage the agent, not
// a dead-end.
var ErrRebaseConflict = errors.New("branch is behind main and the rebase hit conflicts the agent must resolve")

// termInput delivers a human's board reply into a task's live agent terminal
// (stdin). An interface so core need not import the terminal package. Satisfied by
// *terminal.Manager.
type termInput interface {
	WriteTo(taskID string, p []byte) (bool, error)
}

// reengager re-launches a task's agent when the human answers a checkpoint whose
// agent is no longer live (the self-heal escalation). An interface so core need not
// import the orchestrator; nil in API-only/test setups (answer becomes a no-op).
// Satisfied by *Orchestrator.
type reengager interface {
	Reengage(taskID, guidance string) error
}

// devPortReserver provisions a dev-server port only when a running agent asks
// for one. The orchestrator owns the actual allocation because it also knows the
// task worktree and how to launch a project-level dev-server hook there.
type devPortReserver interface {
	StartDevServer(taskID, command string) (port int, err error)
}

// DefaultMaxAttempts is the self-heal retry budget when a task's flow doesn't pin
// one: the orchestrator auto-retries this many times before escalating to the human.
const DefaultMaxAttempts = 3

// Service is the central coordinator shared by the MCP server, web API and
// orchestrator.
type Service struct {
	store    Repo
	events   eventBus
	wt       *worktree.Manager  // set via UseWorktrees; nil = merge/teardown disabled
	term     termInput          // set via UseTerminal; nil = answers aren't delivered
	reengage reengager          // set via UseReengager; nil = escalation answers aren't re-run
	devPorts devPortReserver    // set via UseDevPortReserver; nil = on-demand ports unavailable
	ports    *port.Allocator    // set via UsePorts; nil = no port release
	dev      *devserver.Manager // set via UseDevServer; nil = no dev-server teardown

	// ctxMu guards ctxTokens and modelName: the latest live context size (tokens)
	// and detected model name per running task, reported by the orchestrator's
	// transcript poll. Kept in memory only (ephemeral, resets on restart) and
	// mirrored into the board snapshot so a fresh load isn't blank until the next
	// poll. See PublishContext / PublishModel.
	ctxMu     sync.Mutex
	ctxTokens map[string]int
	modelName map[string]string
	telegram  telegramConfigurator
}

// The Use* setters share the orchestrator's collaborators post-construction, so
// both agree on where worktrees live, which terminal to write answers into, etc.
// Each is nil in API-only/test setups (see the field comments for the degraded
// behavior).
func (s *Service) UseWorktrees(m *worktree.Manager)     { s.wt = m }
func (s *Service) UseTerminal(t termInput)              { s.term = t }
func (s *Service) UseReengager(r reengager)             { s.reengage = r }
func (s *Service) UseDevPortReserver(r devPortReserver) { s.devPorts = r }
func (s *Service) UsePorts(p *port.Allocator)           { s.ports = p }
func (s *Service) UseDevServer(d *devserver.Manager)    { s.dev = d }

// StartDevServer is the MCP-facing entry point for lazy dev-server setup. Most
// tasks never need a browser, so they never consume a port or start a process.
func (s *Service) StartDevServer(taskID, command string) (int, error) {
	if s.devPorts == nil {
		return 0, fmt.Errorf("dev-server startup is unavailable")
	}
	return s.devPorts.StartDevServer(taskID, command)
}

// releaseRuntime frees a finished task's dev-server port and stops its dev server.
// Called only at terminal states — NOT on entering review, where both stay up so
// the human can open the live app.
func (s *Service) releaseRuntime(t model.Task) {
	if s.dev != nil {
		s.dev.Stop(t.ID)
	}
	if s.ports != nil && t.Port > 0 {
		s.ports.Release(t.Port)
	}
}

// clearRuntimeState drops a task's in-memory context/model readings. Called only
// when a task is deleted for good — a terminal-but-live card (done/review) still
// shows them in the board snapshot — so these maps don't grow unbounded over the
// life of a daemon that churns through many tasks.
func (s *Service) clearRuntimeState(id string) {
	s.ctxMu.Lock()
	delete(s.ctxTokens, id)
	delete(s.modelName, id)
	s.ctxMu.Unlock()
}

func NewService(st Repo) *Service {
	return &Service{
		store:     st,
		events:    broker.New(),
		ctxTokens: map[string]int{},
		modelName: map[string]string{},
	}
}

// PublishContext records a running task's live context size (tokens) and fans it
// out as a non-persisted "context" SSE event. The latest value is also held in
// ctxTokens and folded into the board snapshot, so a page load isn't blank until
// the next poll. Fires whether or not a context cap is set.
func (s *Service) PublishContext(taskID string, tokens int) {
	s.ctxMu.Lock()
	s.ctxTokens[taskID] = tokens
	s.ctxMu.Unlock()
	s.publish("context", map[string]any{"taskId": taskID, "tokens": tokens})
}

// ContextTokens returns a copy of the latest per-task context sizes, for the
// board snapshot. Keyed by task id; absent for tasks with no reading yet.
func (s *Service) ContextTokens() map[string]int {
	s.ctxMu.Lock()
	defer s.ctxMu.Unlock()
	out := make(map[string]int, len(s.ctxTokens))
	maps.Copy(out, s.ctxTokens)
	return out
}

// PublishModel records the model an agent is actually running (e.g.
// "claude-opus-4-8", "gpt-5.6-sol"), read from the agent's own transcript, and
// fans it out over SSE as a non-persisted "model" event so the card's footer can
// show the real model name instead of the generic provider label. The latest
// value is kept in memory (modelName) and folded into the board snapshot so a
// page load isn't blank until the next poll. De-duped: only publishes when the
// value changes, so it doesn't spam SSE on every transcript poll.
func (s *Service) PublishModel(taskID, name string) {
	if name == "" {
		return
	}
	s.ctxMu.Lock()
	changed := s.modelName[taskID] != name
	if changed {
		s.modelName[taskID] = name
	}
	s.ctxMu.Unlock()
	if changed {
		s.publish("model", map[string]any{"taskId": taskID, "model": name})
	}
}

// Models returns a copy of the latest per-task model names, for the board
// snapshot. Keyed by task id; absent for tasks whose model isn't detected yet.
func (s *Service) Models() map[string]string {
	s.ctxMu.Lock()
	defer s.ctxMu.Unlock()
	out := make(map[string]string, len(s.modelName))
	maps.Copy(out, s.modelName)
	return out
}

// NewID returns a short random hex id. It panics on a crypto/rand failure rather
// than hand back a degenerate all-zero primary key that would collide.
func NewID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("ultraflow: crypto/rand unavailable for id generation: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func (s *Service) publish(kind string, payload any) {
	// Mirror every board fan-out into the activity journal (no-op when off): this
	// single tap captures task_updated (status moves), event (all task events),
	// context, and runs — the whole task/agent story — without new call sites.
	journal.Log("bus", kind, map[string]any{"data": payload})
	msg, _ := json.Marshal(map[string]any{"kind": kind, "data": payload})
	s.events.Publish(msg)
}

// PublishPaused broadcasts a global pause/resume to every open board so the "pause
// all" toggle and the run pill sync instantly. Pause is a transient in-memory state
// the orchestrator owns; this is the one-line bridge that lets it reach the UI over
// the same SSE stream as task/project events.
func (s *Service) PublishPaused(paused bool) {
	s.publish("paused", map[string]any{"paused": paused})
}

// Subscribe / Unsubscribe expose the live SSE event stream to the transport layer
// without handing it the Broker itself, so the web package depends only on the
// Service contract, not on core's pub/sub internals.
func (s *Service) Subscribe() chan []byte     { return s.events.Subscribe() }
func (s *Service) Unsubscribe(ch chan []byte) { s.events.Unsubscribe(ch) }

func (s *Service) appendEvent(taskID, kind, data string) error {
	e := model.Event{TaskID: taskID, Kind: kind, Data: data, CreatedAt: time.Now()}
	var persistErr error
	if id, err := s.store.AppendEvent(e); err == nil {
		e.ID = id
	} else {
		// Still fans out live (ID stays 0), but a failed persist wouldn't reach a
		// client that resyncs from the DB — so surface it.
		log.Printf("task %s: append %s event: %v", taskID, kind, err)
		persistErr = err
	}
	s.publish("event", e)
	return persistErr
}

// --- tasks ---

// CreateTask is the external (MCP create_task) entry. Its `project` is advertised
// as "name or repo path", so it's resolved to a registered project's canonical name
// before storage — otherwise a caller passing the repo path (a natural thing for an
// agent that knows its cwd) would fail the orchestrator's name-keyed lookup and get
// silently stranded in the shared workdir. See resolveProjectRef.
func (s *Service) CreateTask(title, body, project string) (model.Task, error) {
	return s.CreateTaskFull(title, body, s.resolveProjectRef(project), "", "")
}

// resolveProjectRef maps a project reference to a registered project's canonical
// name: it accepts the name as-is, else matches a registered repo path (exact or
// path-cleaned). A value matching nothing is returned unchanged — the task still
// lands (running in the shared workdir, as before), we just no longer silently miss
// when the caller passed a perfectly valid path instead of the display name.
func (s *Service) resolveProjectRef(project string) string {
	project = strings.TrimSpace(project)
	if project == "" {
		return ""
	}
	if _, err := s.store.ProjectByName(project); err == nil {
		return project // already a registered name
	}
	projects, err := s.store.ListProjects()
	if err != nil {
		return project
	}
	want := filepath.Clean(project)
	for _, p := range projects {
		if p.RepoPath == project || filepath.Clean(p.RepoPath) == want {
			return p.Name
		}
	}
	return project
}

// implementedAgents is what the orchestrator can actually execute. A task's stored
// agent must never claim more (the card shows it), so an unimplemented choice
// normalizes to claude below. The flow equivalent is flow.Wired.
var implementedAgents = map[string]bool{"claude": true, "codex": true}

// CreateTaskFull creates a task, normalizing a blank or not-yet-implemented
// agent/flow to what the orchestrator will really run so the stored value never
// misrepresents the execution.
func (s *Service) CreateTaskFull(title, body, project, agent, flowKey string) (model.Task, error) {
	if !implementedAgents[agent] {
		agent = "claude"
	}
	if !flow.Wired(flowKey) {
		flowKey = "solo"
	}
	now := time.Now()
	t := model.Task{
		ID:          NewID(),
		Title:       title,
		Body:        body,
		Project:     project,
		Agent:       agent,
		Flow:        flowKey,
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

// RecoverInFlight requeues tasks stranded by a previous daemon exit (see
// store.RecoverInFlight). Call once at startup, before the orchestrator polls.
func (s *Service) RecoverInFlight() (int64, error) { return s.store.RecoverInFlight() }

func (s *Service) ListTasks() ([]model.Task, error)      { return s.store.ListTasks() }
func (s *Service) BacklogTasks() ([]model.Task, error)   { return s.store.BacklogTasks() }
func (s *Service) GetTask(id string) (model.Task, error) { return s.store.GetTask(id) }

// RenameTask gives a task a short card label (the agent's rename_task calls it
// first). The human often dumps the whole request into the title with an empty
// body, so when the body is empty the original title is moved into it before the
// short title replaces it — otherwise later prompts, which read the body, lose the
// full request.
func (s *Service) RenameTask(id, title string) (model.Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return model.Task{}, errors.New("title is required")
	}
	t, err := s.store.GetTask(id)
	if err != nil {
		return model.Task{}, err
	}
	body := t.Body
	if strings.TrimSpace(body) == "" {
		body = t.Title
	}
	updatedAt, err := s.store.UpdateTaskTitleBody(id, title, body)
	if err != nil {
		return model.Task{}, err
	}
	t.Title, t.Body, t.UpdatedAt = title, body, updatedAt
	s.publish("task_updated", map[string]any{"taskId": id, "title": title, "body": body, "updatedAt": updatedAt})
	return t, nil
}

// UpdateStatus persists a task's new status and broadcasts it. Returns the store
// error so callers whose correctness depends on the write (the backlog→queued flip
// that prevents a re-pick) can react; only a persisted transition is broadcast.
func (s *Service) UpdateStatus(id string, st model.TaskStatus) error {
	updatedAt, err := s.store.UpdateTaskStatus(id, st)
	if err != nil {
		return err
	}
	s.publish("task_updated", map[string]any{"taskId": id, "status": st, "updatedAt": updatedAt})
	return nil
}

// SwapStatus is the guarded (compare-and-swap) form of UpdateStatus: it moves the
// task to `to` only if currently one of `from`, returning whether it did. This is
// what keeps a human answer and the agent-death handler from clobbering each other
// into a stranded state.
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
		log.Printf("task %s: set worktree %q: %v", id, wt, err)
		return
	}
	s.publish("task_updated", map[string]any{"taskId": id, "worktree": wt})
}

// SetAttempt persists a task's self-heal retry counter and broadcasts it (drives
// the "fixing itself · k/N" sub-state). 0 = the original run.
func (s *Service) SetAttempt(id string, attempt int) {
	updatedAt, err := s.store.SetTaskAttempt(id, attempt)
	if err != nil {
		log.Printf("task %s: set attempt %d: %v", id, attempt, err)
		return
	}
	s.publish("task_updated", map[string]any{"taskId": id, "attempt": attempt, "updatedAt": updatedAt})
}

// SetPort records (and broadcasts) the dev-server port reserved for a task, for
// the card's clickable http://localhost:PORT link.
func (s *Service) SetPort(id string, port int) {
	if err := s.store.SetPort(id, port); err != nil {
		log.Printf("task %s: set port %d: %v", id, port, err)
		return
	}
	s.publish("task_updated", map[string]any{"taskId": id, "port": port})
}

// SetResume sets or clears a task's restart-resume marker (see model.Task.Resume).
// The orchestrator clears it as it consumes the one-shot signal at pickup.
func (s *Service) SetResume(id string, v bool) {
	if err := s.store.SetResume(id, v); err != nil {
		log.Printf("task %s: set resume=%v: %v", id, v, err)
		return
	}
	s.publish("task_updated", map[string]any{"taskId": id, "resume": v})
}

// RetryTask re-queues a task back to the backlog for the orchestrator's next tick
// — the board's "Retry" on a task the agent gave up on.
func (s *Service) RetryTask(id string) error {
	t, err := s.store.GetTask(id)
	if err != nil {
		return err
	}
	if err := s.UpdateStatus(t.ID, model.StatusBacklog); err != nil {
		return err // don't claim it was re-queued if the write didn't land
	}
	s.appendEvent(t.ID, "status", "re-queued by human")
	return nil
}

// MergeTask lands a reviewed task's worktree branch into the project repo and
// finishes it. Any git failure is aborted (repo stays clean) and the task returns
// to review with its worktree intact for a retry.
func (s *Service) MergeTask(id string) error {
	t, err := s.store.GetTask(id)
	if err != nil {
		return err
	}
	if t.Status != model.StatusReview {
		return fmt.Errorf("only a task in review can be merged (this one is %s)", t.Status)
	}
	if !t.Handoff {
		return fmt.Errorf("this task has no submitted report to review")
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

	// Rebase onto the latest main first, so what merges is what was reviewed against
	// it. A clean rebase falls through to the merge; unresolvable conflicts escalate
	// to the agent (ErrRebaseConflict), never a silent abort.
	msg := "Ultraflow: " + t.Title
	conflicted, _, rerr := s.wt.Rebase(p.RepoPath, id, msg)
	if rerr != nil {
		_ = s.UpdateStatus(id, model.StatusReview)
		s.appendEvent(id, "merge_failed", "couldn't rebase onto main (your repo was left clean): "+rerr.Error())
		return rerr
	}
	if conflicted {
		_ = s.UpdateStatus(id, model.StatusReview)
		s.appendEvent(id, "stale", "behind main — auto-rebase hit conflicts; handing to the agent to resolve")
		return ErrRebaseConflict
	}

	if _, err := s.wt.Merge(p.RepoPath, id, msg); err != nil {
		_ = s.UpdateStatus(id, model.StatusReview)
		// "merge_failed" (not "status") so the board lifts it into the attention rail.
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

// FinishReview marks a reviewed task done without a merge — for tasks that ran in
// place (no worktree to land) and for worktree tasks whose outcome isn't a merge
// (a question answered, a design explored, a change already applied), where
// "merge" is meaningless.
func (s *Service) FinishReview(id string) error {
	t, err := s.store.GetTask(id)
	if err != nil {
		return err
	}
	if t.Status != model.StatusReview {
		return fmt.Errorf("only a task in review can be marked done (this one is %s)", t.Status)
	}
	if !t.Handoff {
		return fmt.Errorf("this task has no submitted report to review")
	}
	s.releaseRuntime(t) // stop the dev server and free its port now the task is done
	// Reclaim the worktree: a non-merge outcome still ran in one for every flow task,
	// so finishing without a merge must tear it down too — otherwise each answered /
	// explored task strands its git worktree and branch. Best-effort; in-place tasks
	// (no worktree) no-op.
	s.teardownWorktree(t)
	if err := s.UpdateStatus(id, model.StatusDone); err != nil {
		return err
	}
	s.appendEvent(id, "status", "marked done by human")
	return nil
}

// cancellableStatuses are the states a task can be STOPPED from (it has, or is
// about to have, a running agent). The guarded swap into `cancelled` makes stop
// race-safe against the agent dying at the same instant. Merging is excluded — a
// short git op we don't interrupt.
var cancellableStatuses = []model.TaskStatus{
	model.StatusQueued, model.StatusRunning, model.StatusNeedsHuman,
}

// deletableStatuses are the states a task can be REMOVED from: backlog or terminal.
// A live or in-review task (agent and worktree still in play) must be stopped or
// finished first.
var deletableStatuses = map[model.TaskStatus]bool{
	model.StatusBacklog:   true,
	model.StatusDone:      true,
	model.StatusFailed:    true,
	model.StatusCancelled: true,
}

// CancelTask stops a running/queued/parked task: a guarded swap to `cancelled`,
// retire any pending checkpoint, free the runtime. It does NOT kill the agent — the
// caller (owner of the terminal manager) closes the session after this returns, and
// the self-heal loop reads `cancelled` and stands down rather than reading the close
// as a crash to retry. Returns whether the task transitioned.
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

// DeleteTask removes a task for good: tear down any worktree, free any runtime,
// delete the task with its events and checkpoints. Only a not-live task qualifies
// (see deletableStatuses), so we never yank a worktree from a working agent.
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
	s.clearRuntimeState(id)
	s.publish("task_deleted", map[string]any{"id": id})
	return nil
}

// projectRepo resolves a task's project to the git repo path its worktrees branch
// from. ok is false when the task has no project or its repo path is unset (it ran
// in place), so worktree-dependent ops bail with their own phrasing.
func (s *Service) projectRepo(t model.Task) (repo string, ok bool) {
	repo = s.repoFor(t.Project)
	return repo, repo != ""
}

// teardownWorktree removes a task's leftover git worktree, best-effort (a missing
// one is not an error) — e.g. a cancelled task whose worktree we kept for inspection.
func (s *Service) teardownWorktree(t model.Task) {
	if s.wt == nil || t.Worktree == "" {
		return
	}
	if repo, ok := s.projectRepo(t); ok {
		_ = s.wt.Remove(repo, t.ID)
	}
}

// ArchiveClosed removes every closed (done or cancelled) task in one sweep — the
// board's "Clear" affordance. Returns how many were removed.
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

// NoteFreshness records a "stale · N behind main" event when a task's branch has
// fallen behind, for the card's warning. Best-effort and never changes status;
// callers run it (often in a goroutine) as a task enters review.
func (s *Service) NoteFreshness(taskID string) {
	if s.wt == nil {
		return
	}
	t, err := s.store.GetTask(taskID)
	if err != nil || t.Worktree == "" {
		return
	}
	repo, ok := s.projectRepo(t)
	if !ok {
		return
	}
	behind, _, err := s.wt.Freshness(repo, taskID)
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
	repo, ok := s.projectRepo(t)
	if !ok {
		return worktree.DiffResult{}, fmt.Errorf("couldn't find the project repo to diff against")
	}
	return s.wt.Diff(repo, id)
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

// captureContext snapshots the fast context for a task's ask_human checkpoint: the
// worktree's change magnitude and the screenshots the agent saved. Best-effort — a
// task with no worktree or no edits still asks cleanly with zero magnitude.
func (s *Service) captureContext(taskID string) (added, removed int, files []model.ChangedFile, shots []string) {
	files = []model.ChangedFile{}
	if d, err := s.TaskDiff(taskID); err == nil {
		for _, f := range d.Files {
			// .ultraflow/ metadata (shots have their own gallery) isn't the agent's
			// code change — keep it out of the "what changed" magnitude.
			if strings.HasPrefix(f.Path, ".ultraflow/") {
				continue
			}
			added += f.Added
			removed += f.Removed
			files = append(files, f)
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
	MinConcurrent      = 1
	MaxConcurrentCap   = 8
	settingKeyMaxConc  = "max_concurrent"
	settingKeyTelegram = "telegram_config"
)

// TelegramSettings is persisted daemon-side. Token is deliberately omitted by
// the settings GET endpoint; HasToken lets the UI show that a secret is saved.
type TelegramSettings struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token"`
	UserID  int64  `json:"userId"`
	ChatID  int64  `json:"chatId"`
}

type telegramConfigurator interface{ ApplyTelegram(TelegramSettings) }

func (s *Service) UseTelegramConfigurator(c telegramConfigurator) { s.telegram = c }

func (s *Service) TelegramSettings() (TelegramSettings, bool, error) {
	v, ok, err := s.store.GetSetting(settingKeyTelegram)
	if err != nil || !ok {
		return TelegramSettings{}, ok, err
	}
	var cfg TelegramSettings
	if err := json.Unmarshal([]byte(v), &cfg); err != nil {
		return TelegramSettings{}, false, nil
	}
	return cfg, true, nil
}

func (s *Service) SetTelegramSettings(cfg TelegramSettings) error {
	if cfg.Enabled && (strings.TrimSpace(cfg.Token) == "" || cfg.UserID == 0 || cfg.ChatID == 0) {
		return fmt.Errorf("bot token, user ID, and chat ID are required when Telegram is enabled")
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := s.store.SetSetting(settingKeyTelegram, string(b)); err != nil {
		return err
	}
	if s.telegram != nil {
		s.telegram.ApplyTelegram(cfg)
	}
	return nil
}

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

// Context-budget bounds. 0 means off; any set value is held in a sane band so a
// too-low cap can't make an agent compact every turn and never progress, and a
// too-high one is meaningless against real context windows.
const (
	MinContextCap    = 50_000
	MaxContextCap    = 1_000_000
	settingKeyCtxCap = "context_cap_tokens"
)

// clampContextCap normalizes a context-cap value: 0 (and anything below) means
// off; otherwise it's forced into MinContextCap..MaxContextCap.
func clampContextCap(n int) int {
	if n <= 0 {
		return 0
	}
	if n < MinContextCap {
		return MinContextCap
	}
	if n > MaxContextCap {
		return MaxContextCap
	}
	return n
}

// ContextCap returns the persisted per-agent context budget in tokens, or 0 when
// unset or disabled. When a running claude agent's context crosses this, the
// orchestrator injects /compact into its live session (see
// orchestrator.watchContext). Default off — a behaviour-changing feature the
// human opts into from Settings.
func (s *Service) ContextCap() int {
	v, ok, err := s.store.GetSetting(settingKeyCtxCap)
	if err != nil || !ok {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return 0 // corrupt value → treat as off
	}
	return clampContextCap(n)
}

// SetContextCap clamps n (0 = off, else 50k..1M), persists it, and returns the
// stored value so the UI can echo the effective number back.
func (s *Service) SetContextCap(n int) (int, error) {
	n = clampContextCap(n)
	if err := s.store.SetSetting(settingKeyCtxCap, fmt.Sprintf("%d", n)); err != nil {
		return 0, err
	}
	// Broadcast so every open board rescales its context meter to the new budget
	// live, instead of waiting for a reload to pick it up from the snapshot.
	s.publish("settings", map[string]any{"contextCap": n})
	return n, nil
}

// --- the ask_human protocol (the core of Ultraflow) ---

// AskHuman posts a question to the board and returns immediately — it does NOT
// block. The agent then ends its turn and idles at its prompt: a durable,
// tokenless, timeout-proof wait. AnswerHuman later writes the reply into that
// terminal's stdin and the agent resumes. Returns the created request.
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

// answerSubmitDelay is the gap between typing a board answer and the Enter that
// submits it. It must outlast the TUI's paste-detection window so the trailing CR
// reads as a keystroke, not part of a paste (see AnswerHuman). A var so tests zero it.
var answerSubmitDelay = 250 * time.Millisecond

// AnswerHuman records the human's board reply, returns the task to running, and
// delivers the reply into the parked agent's terminal stdin so it resumes.
func (s *Service) AnswerHuman(reqID, answer string) error {
	updated, err := s.store.AnswerHumanRequest(reqID, answer)
	if err != nil {
		return err
	}
	// A duplicate/late answer is a no-op — don't re-run side effects.
	if !updated {
		return nil
	}

	req, err := s.store.GetHumanRequest(reqID)
	if err != nil {
		return err
	}

	// Resume only if still parked. If it already left needs_human, the agent died and
	// the death path owns it — flipping to running here would strand it. The guarded
	// swap makes answer-vs-death mutually exclusive.
	if !s.SwapStatus(req.TaskID, []model.TaskStatus{model.StatusNeedsHuman}, model.StatusRunning) {
		s.appendEvent(req.TaskID, "status", "your answer arrived after the agent had already stopped")
		return nil
	}
	s.appendEvent(req.TaskID, "human_answer", answer)
	s.publish("human_answered", map[string]any{"id": reqID, "answer": answer})

	// Deliver the reply as if typed. The text and the submitting Enter go as TWO
	// writes with a gap: a TUI reading text+CR glued in one read treats it as a paste
	// and keeps the CR as a literal newline instead of submitting (the "selected an
	// answer but Enter wasn't pressed" symptom). Newlines are flattened so an embedded
	// one can't submit early.
	live := false
	if s.term != nil {
		line := strings.NewReplacer("\r", " ", "\n", " ").Replace(answer)
		var werr error
		live, werr = s.term.WriteTo(req.TaskID, []byte(line))
		if werr != nil {
			log.Printf("task %s: delivering answer to terminal: %v", req.TaskID, werr)
		}
	}
	if live {
		// Submit as a separate keystroke once the paste-detection window has closed.
		time.Sleep(answerSubmitDelay)
		if _, err := s.term.WriteTo(req.TaskID, []byte("\r")); err != nil {
			log.Printf("task %s: submitting answer to terminal: %v", req.TaskID, err)
		}
		return nil
	}

	// No live agent took the answer — normally a self-heal escalation (the agent
	// stopped after exhausting retries), so re-launch in place with the guidance.
	// Reengage no-ops if a self-heal loop still owns the task, so it can't race a
	// second agent onto the worktree. Gated on `live`, not `s.term != nil`, so
	// re-engagement fires whenever no live session exists.
	if s.reengage != nil {
		if err := s.reengage.Reengage(req.TaskID, answer); err != nil {
			log.Printf("task %s: re-engage after answer: %v", req.TaskID, err)
		}
	} else {
		log.Printf("task %s: answered but the agent terminal is gone", req.TaskID)
	}
	return nil
}

// AbandonRequests retires any pending human request for a task whose agent has
// exited, so an orphaned checkpoint can't linger or be answered into a void.
func (s *Service) AbandonRequests(taskID string) {
	if n, _ := s.store.CancelTaskRequests(taskID); n > 0 {
		s.publish("human_cancelled", map[string]any{"taskId": taskID})
	}
}

// ResolveAgentExit drives a task terminal after its agent PROCESS exits — the
// single authority for "the agent is gone". A completed task is already in review
// (finish_task), so that no-ops; anything still in-flight is resolved now. crashed
// is a non-zero exit; detail is the exit error for the failed card.
//
// Every transition is a guarded swap, so a human answer racing in (needs_human→
// running) can't strand the task — both the crash swap and the clean-exit fail swap
// include `running` to catch an answer that lands in the gap.
func (s *Service) ResolveAgentExit(taskID string, crashed bool, detail string) {
	if crashed {
		if s.SwapStatus(taskID, []model.TaskStatus{model.StatusQueued, model.StatusRunning, model.StatusNeedsHuman}, model.StatusFailed) {
			s.AbandonRequests(taskID)
			s.releaseRuntimeByID(taskID)
			reason := "agent exited before reporting completion"
			if detail != "" {
				reason += ": " + detail
			}
			s.appendEvent(taskID, "error", reason)
		}
		return
	}
	// Exit 0 is not proof of completion. Without finish_task there is no report,
	// so presenting the task as reviewable would be a false handoff.
	if s.FailExecution(taskID, "agent exited without calling finish_task — no report was submitted") {
		return
	}
	// Otherwise it was still parked (or a late answer resumed it behind this dead
	// agent), so it never finished: fail it and retire the orphaned checkpoint.
	if s.SwapStatus(taskID, []model.TaskStatus{model.StatusNeedsHuman, model.StatusRunning}, model.StatusFailed) {
		s.AbandonRequests(taskID)
		s.releaseRuntimeByID(taskID)
		s.appendEvent(taskID, "error", "agent stopped while awaiting your answer")
	}
}

// releaseRuntimeByID frees a task's runtime by id, for the agent-exit paths that
// only have the id.
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

// LatestActivity returns, per task, the text and kind of its latest activity line
// (the kind lets the board lift e.g. "merge_failed" into the attention rail). Both
// maps come from one store query.
func (s *Service) LatestActivity() (text, kind map[string]string, err error) {
	lines, err := s.store.LatestActivity()
	if err != nil {
		return nil, nil, err
	}
	text = make(map[string]string, len(lines))
	kind = make(map[string]string, len(lines))
	for id, a := range lines {
		text[id] = a.Data
		kind[id] = a.Kind
	}
	return text, kind, nil
}
