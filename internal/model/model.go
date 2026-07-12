// Package model holds Ultraflow's core domain types shared across packages.
package model

import "time"

// TaskStatus is the lifecycle state of a task on the board.
type TaskStatus string

const (
	StatusBacklog    TaskStatus = "backlog"
	StatusQueued     TaskStatus = "queued" // picked up, waiting for a concurrency slot
	StatusPlanning   TaskStatus = "planning"
	StatusRunning    TaskStatus = "running"
	StatusNeedsHuman TaskStatus = "needs_human"
	StatusReview     TaskStatus = "review"
	StatusMerging    TaskStatus = "merging"
	StatusDone       TaskStatus = "done"
	StatusFailed     TaskStatus = "failed"
	StatusCancelled  TaskStatus = "cancelled"
)

// Project is a registered codebase an agent works in: a name plus the local git
// repo path that is the root for its tasks' worktrees. Color is a stable board
// hue assigned at creation (distinct from the reserved status colors).
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	RepoPath  string    `json:"repoPath"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"createdAt"`
}

// Task is a unit of work an agent runs, shown as a card on the board.
type Task struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Project   string     `json:"project"`
	Agent     string     `json:"agent"` // which CLI adapter: claude, codex, opencode
	Flow      string     `json:"flow"`  // flow preset name
	Status    TaskStatus `json:"status"`
	Worktree  string     `json:"worktree"`
	// Self-heal sub-state. On an agent error the orchestrator auto-diagnoses and
	// re-runs the task up to MaxAttempts times while it STAYS `running` — Attempt is
	// how many auto-retries it has spent (0 = the original run, no sub-state; k>0 =
	// on its k-th retry, shown as "fixing itself · k/N"). Only when the budget is
	// exhausted does it escalate to the human; failure is a card state, not a
	// destination (see spec.md "Failure self-heals").
	Attempt     int       `json:"attempt"`
	MaxAttempts int       `json:"maxAttempts"`
	Port        int       `json:"port"` // dev-server port reserved for this task (0 = none)
	// Resume marks a task a daemon restart interrupted mid-run: the orchestrator
	// picks it back up IN PLACE (same worktree, and for claude the same
	// conversation via --continue) instead of pruning and starting it over. Set by
	// store.RecoverInFlight at startup, cleared when the resume launches. A one-shot
	// recovery signal, not a lifecycle state.
	Resume      bool      `json:"resume"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ChangedFile is one path a task touched, with its line magnitude — the
// at-a-glance signal the board leads with (see spec.md "What to surface").
type ChangedFile struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// HumanRequest is a blocking question an agent raised via ask_human. The agent's
// MCP call is parked until Status becomes "answered" and Answer is filled.
//
// Added/Removed/Files/Shots are the fast context the daemon captures server-side
// at ask_human time — the worktree's change magnitude and any screenshots the
// agent saved — so the decision surfaces show what changed without the agent
// hand-formatting it into Context.
type HumanRequest struct {
	ID         string        `json:"id"`
	TaskID     string        `json:"taskId"`
	Question   string        `json:"question"`
	Options    []string      `json:"options"`
	Context    string        `json:"context"`
	Answer     string        `json:"answer"`
	Status     string        `json:"status"` // pending, answered
	Added      int           `json:"added"`
	Removed    int           `json:"removed"`
	Files      []ChangedFile `json:"files"`
	Shots      []string      `json:"shots"`
	CreatedAt  time.Time     `json:"createdAt"`
	AnsweredAt *time.Time    `json:"answeredAt,omitempty"`
}

// Event is an append-only record of something that happened on a task, also
// fanned out live to the board over SSE.
type Event struct {
	ID        int64     `json:"id"`
	TaskID    string    `json:"taskId"`
	Kind      string    `json:"kind"` // status, log, human_request, human_answer
	Data      string    `json:"data"`
	CreatedAt time.Time `json:"createdAt"`
}
