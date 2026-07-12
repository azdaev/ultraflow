// Package store is the SQLite persistence layer for Ultraflow.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no CGO

	"ultraflow/internal/model"
)

// Store wraps a SQLite database.
type Store struct{ db *sql.DB }

// Open opens (creating if needed) the SQLite database at path and migrates it.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Serialize ALL access through a single connection. sql.DB is a pool, and a
	// PRAGMA set via db.Exec lands on just one arbitrary pooled connection — so
	// with >1 conn, concurrent writers race on other connections and fail with
	// SQLITE_BUSY instead of honoring busy_timeout. That broke the guarded status
	// swaps (a busy error read as "CAS missed", stranding a task). One connection
	// makes every write queue behind the last, so the compare-and-swaps the
	// answer/death paths rely on are truly atomic. Fine for a local single-user
	// daemon; WAL still gives durable, non-blocking snapshot reads.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

// migrations is the ordered list of schema steps. Index i is migration version
// i+1; a DB at PRAGMA user_version = N has had migrations[0..N-1] applied. To
// evolve the schema, append a new step (e.g. an ALTER TABLE) — never edit an
// existing one, or DBs already past it will skip your change. Migration 1 is the
// original schema and stays idempotent (CREATE TABLE IF NOT EXISTS) so it applies
// cleanly to both fresh and pre-migration-runner databases.
var migrations = []string{
	schema,
	settingsSchema,
	selfHealSchema,     // migration 3: tasks.attempt, tasks.max_attempts
	portSchema,         // migration 4: tasks.port
	humanContextSchema, // migration 5: human_requests added/removed/files/shots
	resumeSchema,       // migration 6: tasks.resume
	runsSchema,         // migration 7: runs (multi-step flow progress)
}

// migrate applies every migration newer than the DB's user_version in a single
// transaction, then stamps user_version to the latest. A user_version of 0 (a
// fresh or pre-runner DB) runs them all; already-applied steps are skipped.
func (s *Store) migrate() error {
	var current int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&current); err != nil {
		return err
	}
	if current >= len(migrations) {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for v := current; v < len(migrations); v++ {
		if _, err := tx.Exec(migrations[v]); err != nil {
			return err
		}
	}
	// PRAGMA doesn't accept bound parameters, so splice the trusted int directly.
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, len(migrations))); err != nil {
		return err
	}
	return tx.Commit()
}

// Close checkpoints the WAL back into the main database file (so it doesn't grow
// unbounded across runs) and closes the underlying connection pool.
func (s *Store) Close() error {
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE);`); err != nil {
		return err
	}
	return s.db.Close()
}

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
  id         TEXT PRIMARY KEY,
  title      TEXT NOT NULL,
  body       TEXT NOT NULL DEFAULT '',
  project    TEXT NOT NULL DEFAULT '',
  agent      TEXT NOT NULL DEFAULT '',
  flow       TEXT NOT NULL DEFAULT 'solo',
  status     TEXT NOT NULL DEFAULT 'backlog',
  worktree   TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);
CREATE TABLE IF NOT EXISTS projects (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  repo_path  TEXT NOT NULL DEFAULT '',
  color      TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL
);
CREATE TABLE IF NOT EXISTS human_requests (
  id          TEXT PRIMARY KEY,
  task_id     TEXT NOT NULL,
  question    TEXT NOT NULL,
  options     TEXT NOT NULL DEFAULT '[]',
  context     TEXT NOT NULL DEFAULT '',
  answer      TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'pending',
  created_at  TIMESTAMP NOT NULL,
  answered_at TIMESTAMP
);
CREATE TABLE IF NOT EXISTS events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id    TEXT NOT NULL,
  kind       TEXT NOT NULL,
  data       TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL
);
`

// settingsSchema (migration 2) adds a simple key/value store for daemon-wide
// preferences the human can change at runtime — currently just max_concurrent.
const settingsSchema = `
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

// selfHealSchema (migration 3) records a task's self-heal state: how many auto-
// retries the orchestrator has spent on a failing agent (attempt) and the per-task
// retry budget (max_attempts, default 3). The board renders "fixing itself · k/N"
// from these while the task keeps running. Existing rows default to a fresh budget.
const selfHealSchema = `
ALTER TABLE tasks ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 3;
`

// portSchema (migration 4) records the dev-server port reserved for a task, so
// the board can show a clickable http://localhost:PORT link and the daemon can
// re-reserve it after a restart. 0 means no port.
const portSchema = `ALTER TABLE tasks ADD COLUMN port INTEGER NOT NULL DEFAULT 0;`

// humanContextSchema (migration 5) attaches the server-captured fast context to
// each request: the worktree's change magnitude (added/removed line counts plus
// the changed-file list as JSON) and the screenshots the agent saved (JSON
// filename list). Defaults keep pre-migration rows valid.
const humanContextSchema = `
ALTER TABLE human_requests ADD COLUMN added   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE human_requests ADD COLUMN removed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE human_requests ADD COLUMN files   TEXT NOT NULL DEFAULT '[]';
ALTER TABLE human_requests ADD COLUMN shots   TEXT NOT NULL DEFAULT '[]';
`

// resumeSchema (migration 6) marks a task that a daemon restart interrupted while
// it was running with a live worktree, so the orchestrator resumes it IN PLACE
// (same checkout, and for claude the same conversation via --continue) instead of
// pruning the worktree and restarting the task from scratch. Set by
// RecoverInFlight, cleared when the resume launches. Default 0 keeps fresh tasks
// and the human "Retry" action on the normal cold-start path.
const resumeSchema = `ALTER TABLE tasks ADD COLUMN resume INTEGER NOT NULL DEFAULT 0;`

// runsSchema (migration 7) records a task's progress through a multi-step flow:
// which flow it walks, the step it's currently on (cursor), the steps it has
// completed (JSON list, for the board's stepper), and a transient turn_done flag
// the orchestrator flips when a step's agent ends its turn. One row per task and
// only for multi-step flows — a solo task has no run — so a row's mere existence
// marks a task as flow-driven. Persisting the cursor is what lets a restart resume
// mid-flow (see RecoverInFlight / RunsForTasks).
const runsSchema = `
CREATE TABLE IF NOT EXISTS runs (
  task_id    TEXT PRIMARY KEY,
  flow       TEXT NOT NULL,
  cursor     TEXT NOT NULL DEFAULT '',
  completed  TEXT NOT NULL DEFAULT '[]',
  turn_done  INTEGER NOT NULL DEFAULT 0,
  updated_at TIMESTAMP NOT NULL
);
`

// --- runs (multi-step flow progress) ---

// CreateRun starts (or resets) a task's flow run at the given cursor. It upserts,
// so a re-created run for the same task overwrites the old cursor rather than
// erroring — a retry of a flow task restarts its walk cleanly.
func (s *Store) CreateRun(taskID, flow, cursor string) error {
	_, err := s.db.Exec(
		`INSERT INTO runs (task_id,flow,cursor,completed,turn_done,updated_at)
		 VALUES (?,?,?,'[]',0,?)
		 ON CONFLICT(task_id) DO UPDATE SET flow=excluded.flow, cursor=excluded.cursor,
		   completed='[]', turn_done=0, updated_at=excluded.updated_at`,
		taskID, flow, cursor, time.Now())
	return err
}

// GetRun returns a task's flow run and whether it has one (solo tasks don't).
func (s *Store) GetRun(taskID string) (model.Run, bool, error) {
	var r model.Run
	var completed string
	var turnDone int
	err := s.db.QueryRow(
		`SELECT task_id,flow,cursor,completed,turn_done,updated_at FROM runs WHERE task_id=?`, taskID,
	).Scan(&r.TaskID, &r.Flow, &r.Cursor, &completed, &turnDone, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return model.Run{}, false, nil
	}
	if err != nil {
		return model.Run{}, false, err
	}
	_ = json.Unmarshal([]byte(completed), &r.Completed)
	r.TurnDone = turnDone != 0
	return r, true, nil
}

// AdvanceRun moves a run's cursor to the next step, appends the step just left to
// the completed list (deduped so a loop-back doesn't record it twice), and clears
// the per-step turn_done flag for the new step.
func (s *Store) AdvanceRun(taskID, completedStep, next string) error {
	r, ok, err := s.GetRun(taskID)
	if err != nil || !ok {
		return err
	}
	if completedStep != "" && !slices.Contains(r.Completed, completedStep) {
		r.Completed = append(r.Completed, completedStep)
	}
	blob, _ := json.Marshal(r.Completed)
	_, err = s.db.Exec(
		`UPDATE runs SET cursor=?, completed=?, turn_done=0, updated_at=? WHERE task_id=?`,
		next, string(blob), time.Now(), taskID)
	return err
}

// SetRunCursor moves the cursor without recording a completion — used when the
// orchestrator (re)enters a step, e.g. resuming after a restart or routing a gate.
func (s *Store) SetRunCursor(taskID, cursor string) error {
	_, err := s.db.Exec(`UPDATE runs SET cursor=?, turn_done=0, updated_at=? WHERE task_id=?`,
		cursor, time.Now(), taskID)
	return err
}

// SetRunTurnDone flips the transient flag the orchestrator reads after a step's
// agent exits: true means the agent ended its turn on purpose (finish_task or an
// idle turn-end) so the flow should advance; false is the fresh-step default.
func (s *Store) SetRunTurnDone(taskID string, done bool) error {
	v := 0
	if done {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE runs SET turn_done=?, updated_at=? WHERE task_id=?`, v, time.Now(), taskID)
	return err
}

// DeleteRun removes a task's run row (called when the task is deleted, so no
// orphan progress lingers).
func (s *Store) DeleteRun(taskID string) error {
	_, err := s.db.Exec(`DELETE FROM runs WHERE task_id=?`, taskID)
	return err
}

// RunsForTasks returns the runs for the given task ids as a map, for the board
// snapshot (one query instead of N). An empty id list returns an empty map.
func (s *Store) RunsForTasks(ids []string) (map[string]model.Run, error) {
	out := make(map[string]model.Run)
	if len(ids) == 0 {
		return out, nil
	}
	q := `SELECT task_id,flow,cursor,completed,turn_done,updated_at FROM runs WHERE task_id IN (?` +
		strings.Repeat(",?", len(ids)-1) + `)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r model.Run
		var completed string
		var turnDone int
		if err := rows.Scan(&r.TaskID, &r.Flow, &r.Cursor, &completed, &turnDone, &r.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(completed), &r.Completed)
		r.TurnDone = turnDone != 0
		out[r.TaskID] = r
	}
	return out, rows.Err()
}

// --- tasks ---

func (s *Store) CreateTask(t model.Task) error {
	_, err := s.db.Exec(
		`INSERT INTO tasks (id,title,body,project,agent,flow,status,worktree,attempt,max_attempts,port,created_at,updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Title, t.Body, t.Project, t.Agent, t.Flow, string(t.Status), t.Worktree, t.Attempt, t.MaxAttempts, t.Port, t.CreatedAt, t.UpdatedAt)
	return err
}

const taskCols = `id,title,body,project,agent,flow,status,worktree,attempt,max_attempts,port,resume,created_at,updated_at`

func scanTask(sc interface{ Scan(...any) error }) (model.Task, error) {
	var t model.Task
	var status string
	var resume int
	err := sc.Scan(&t.ID, &t.Title, &t.Body, &t.Project, &t.Agent, &t.Flow, &status, &t.Worktree, &t.Attempt, &t.MaxAttempts, &t.Port, &resume, &t.CreatedAt, &t.UpdatedAt)
	t.Status = model.TaskStatus(status)
	t.Resume = resume != 0
	return t, err
}

func (s *Store) queryTasks(where string, args ...any) ([]model.Task, error) {
	rows, err := s.db.Query(`SELECT `+taskCols+` FROM tasks `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ListTasks() ([]model.Task, error) {
	return s.queryTasks(`ORDER BY created_at DESC`)
}

// DeleteTask removes a task with its dependent rows (events and human requests)
// in one transaction, so the board's Remove/Archive actions leave no orphaned
// thread or checkpoint behind. Idempotent: an unknown id affects no rows. Any git
// worktree is torn down by the caller (the store doesn't know the repo path).
func (s *Store) DeleteTask(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM events WHERE task_id=?`,
		`DELETE FROM human_requests WHERE task_id=?`,
		`DELETE FROM runs WHERE task_id=?`,
		`DELETE FROM tasks WHERE id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) BacklogTasks() ([]model.Task, error) {
	return s.queryTasks(`WHERE status='backlog' ORDER BY created_at ASC`)
}

func (s *Store) GetTask(id string) (model.Task, error) {
	return scanTask(s.db.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE id=?`, id))
}

// UpdateTaskStatus flips a task's status and returns the new updated_at, so the
// caller can broadcast it and keep the board's live timers accurate.
func (s *Store) UpdateTaskStatus(id string, st model.TaskStatus) (time.Time, error) {
	now := time.Now()
	if _, err := s.db.Exec(`UPDATE tasks SET status=?, updated_at=? WHERE id=?`, string(st), now, id); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

// SwapStatusFrom sets a task's status to `to` only if it is currently one of
// `from`, reporting whether it changed a row. This is the compare-and-swap the
// answer path and the agent-death path both use so their concurrent writes agree
// on a single outcome: a stale read followed by a blind write could otherwise
// strand a task in `running` behind a dead agent (a human answer resuming it
// after the death handler already failed it, or vice versa).
func (s *Store) SwapStatusFrom(id string, from []model.TaskStatus, to model.TaskStatus) (bool, error) {
	if len(from) == 0 {
		return false, nil
	}
	args := []any{string(to), time.Now(), id}
	for _, f := range from {
		args = append(args, string(f))
	}
	q := `UPDATE tasks SET status=?, updated_at=? WHERE id=? AND status IN (?` +
		strings.Repeat(",?", len(from)-1) + `)`
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// UpdateTaskTitleBody rewrites a task's title (and body) and returns the new
// updated_at so the caller can broadcast it. Used by the agent's rename_task:
// the raw one-liner the human dumped in becomes a short label, with the original
// preserved into body so later prompts still carry the full instructions.
func (s *Store) UpdateTaskTitleBody(id, title, body string) (time.Time, error) {
	now := time.Now()
	if _, err := s.db.Exec(`UPDATE tasks SET title=?, body=?, updated_at=? WHERE id=?`, title, body, now, id); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

func (s *Store) SetWorktree(id, wt string) error {
	_, err := s.db.Exec(`UPDATE tasks SET worktree=?, updated_at=? WHERE id=?`, wt, time.Now(), id)
	return err
}

// SetTaskAttempt persists a task's self-heal retry counter and returns the new
// updated_at so the caller can broadcast it and keep the card's live timer honest.
func (s *Store) SetTaskAttempt(id string, attempt int) (time.Time, error) {
	now := time.Now()
	if _, err := s.db.Exec(`UPDATE tasks SET attempt=?, updated_at=? WHERE id=?`, attempt, now, id); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

// SetPort records the dev-server port reserved for a task (0 to clear it).
func (s *Store) SetPort(id string, port int) error {
	_, err := s.db.Exec(`UPDATE tasks SET port=?, updated_at=? WHERE id=?`, port, time.Now(), id)
	return err
}

// SetResume sets (or clears) the restart-resume marker on a task. It deliberately
// does NOT touch updated_at: consuming the one-shot flag at pickup isn't a
// board-visible transition and shouldn't disturb the card's live timer.
func (s *Store) SetResume(id string, v bool) error {
	n := 0
	if v {
		n = 1
	}
	_, err := s.db.Exec(`UPDATE tasks SET resume=? WHERE id=?`, n, id)
	return err
}

// RecoverInFlight cleans up work stranded by a previous daemon exit. A restart
// kills every agent goroutine, so any task still marked in-flight
// (queued/running/needs_human/planning/merging) has no executor behind it and
// would otherwise sit forever — the board only offers Retry on failed cards.
// Move them back to backlog to be picked up fresh, and cancel their now-orphaned
// pending human requests (the agent that would consume the answer is gone).
// Returns how many tasks were requeued.
//
// A multi-step flow task's run row is deliberately LEFT intact: its persisted
// cursor is what makes the re-pickup resume at the step it was on (or re-open the
// gate it was parked at) rather than walking the flow again from step one. The
// orchestrator's flow runner reads the cursor on start (see startFlow).
func (s *Store) RecoverInFlight() (int64, error) {
	if _, err := s.db.Exec(
		`UPDATE human_requests SET status='cancelled' WHERE status='pending'`); err != nil {
		return 0, err
	}
	// Mark the requeued tasks that had a live worktree so the orchestrator resumes
	// them in place (same checkout + conversation) rather than pruning and starting
	// over. Scoped to the in-flight statuses so it never touches a task the human
	// already sent back to the backlog via "Retry" (which wants a clean restart).
	// Must run BEFORE the requeue below, while these rows still carry those statuses.
	if _, err := s.db.Exec(
		`UPDATE tasks SET resume=1
		 WHERE status IN ('running','needs_human','planning','merging') AND worktree != ''`); err != nil {
		return 0, err
	}
	res, err := s.db.Exec(
		`UPDATE tasks SET status='backlog', updated_at=?
		 WHERE status IN ('queued','running','needs_human','planning','merging')`,
		time.Now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// --- projects ---

func (s *Store) CreateProject(p model.Project) error {
	_, err := s.db.Exec(
		`INSERT INTO projects (id,name,repo_path,color,created_at) VALUES (?,?,?,?,?)`,
		p.ID, p.Name, p.RepoPath, p.Color, p.CreatedAt)
	return err
}

func (s *Store) ListProjects() ([]model.Project, error) {
	rows, err := s.db.Query(`SELECT id,name,repo_path,color,created_at FROM projects ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Project
	for rows.Next() {
		var p model.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.RepoPath, &p.Color, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ProjectByName(name string) (model.Project, error) {
	var p model.Project
	err := s.db.QueryRow(
		`SELECT id,name,repo_path,color,created_at FROM projects WHERE name=?`, name,
	).Scan(&p.ID, &p.Name, &p.RepoPath, &p.Color, &p.CreatedAt)
	return p, err
}

func (s *Store) ProjectCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&n)
	return n, err
}

func (s *Store) DeleteProject(id string) error {
	_, err := s.db.Exec(`DELETE FROM projects WHERE id=?`, id)
	return err
}

// --- human requests ---

func (s *Store) CreateHumanRequest(r model.HumanRequest) error {
	opts, _ := json.Marshal(r.Options)
	files, _ := json.Marshal(r.Files)
	shots, _ := json.Marshal(r.Shots)
	_, err := s.db.Exec(
		`INSERT INTO human_requests (id,task_id,question,options,context,answer,status,added,removed,files,shots,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.TaskID, r.Question, string(opts), r.Context, r.Answer, r.Status,
		r.Added, r.Removed, string(files), string(shots), r.CreatedAt)
	return err
}

const hrCols = `id,task_id,question,options,context,answer,status,added,removed,files,shots,created_at,answered_at`

func scanHumanRequest(sc interface{ Scan(...any) error }) (model.HumanRequest, error) {
	var r model.HumanRequest
	var opts, files, shots string
	var answeredAt sql.NullTime
	err := sc.Scan(&r.ID, &r.TaskID, &r.Question, &opts, &r.Context, &r.Answer, &r.Status,
		&r.Added, &r.Removed, &files, &shots, &r.CreatedAt, &answeredAt)
	if err != nil {
		return r, err
	}
	_ = json.Unmarshal([]byte(opts), &r.Options)
	_ = json.Unmarshal([]byte(files), &r.Files)
	_ = json.Unmarshal([]byte(shots), &r.Shots)
	if answeredAt.Valid {
		r.AnsweredAt = &answeredAt.Time
	}
	return r, nil
}

func (s *Store) GetHumanRequest(id string) (model.HumanRequest, error) {
	return scanHumanRequest(s.db.QueryRow(`SELECT `+hrCols+` FROM human_requests WHERE id=?`, id))
}

// AnswerHumanRequest records an answer, but only for a still-pending request.
// It returns whether a row was actually updated, so a duplicate or late answer
// (request already answered, or unknown id) is a no-op the caller can detect.
func (s *Store) AnswerHumanRequest(id, answer string) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE human_requests SET answer=?, status='answered', answered_at=? WHERE id=? AND status='pending'`,
		answer, time.Now(), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CancelTaskRequests cancels every still-pending request belonging to a task
// (its agent has exited, so no one will consume an answer). Returns how many
// were cancelled.
func (s *Store) CancelTaskRequests(taskID string) (int64, error) {
	res, err := s.db.Exec(
		`UPDATE human_requests SET status='cancelled' WHERE task_id=? AND status='pending'`, taskID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) PendingHumanRequests() ([]model.HumanRequest, error) {
	rows, err := s.db.Query(`SELECT ` + hrCols + ` FROM human_requests WHERE status='pending' ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.HumanRequest
	for rows.Next() {
		r, err := scanHumanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- events ---

func (s *Store) AppendEvent(e model.Event) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO events (task_id,kind,data,created_at) VALUES (?,?,?,?)`,
		e.TaskID, e.Kind, e.Data, e.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// TaskEvents returns a task's events oldest-first (the thread timeline).
func (s *Store) TaskEvents(taskID string) ([]model.Event, error) {
	rows, err := s.db.Query(
		`SELECT id,task_id,kind,data,created_at FROM events WHERE task_id=? ORDER BY id ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Event
	for rows.Next() {
		var e model.Event
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Kind, &e.Data, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- settings ---

// GetSetting returns a stored setting's value and whether it was present. A
// missing key (ok=false) lets the caller fall back to a default rather than
// treating absence as an error.
func (s *Store) GetSetting(key string) (value string, ok bool, err error) {
	err = s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// SetSetting upserts a setting value.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key,value) VALUES (?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// ActivityLine is a task's most recent non-empty event: the text the board's
// activity strip shows and the kind it uses to lift certain events (e.g.
// merge_failed) into the attention rail. Both come from one query.
type ActivityLine struct {
	Data string
	Kind string
}

// LatestActivity returns, per task, its most recent non-empty event — text and
// kind together. A single grouped MAX(id) join feeds both the live activity strip
// and the attention-rail promotion, rather than running the same join twice.
func (s *Store) LatestActivity() (map[string]ActivityLine, error) {
	rows, err := s.db.Query(`
		SELECT e.task_id, e.kind, e.data
		FROM events e
		JOIN (SELECT task_id, MAX(id) AS mid FROM events WHERE data <> '' GROUP BY task_id) m
		  ON e.task_id = m.task_id AND e.id = m.mid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]ActivityLine)
	for rows.Next() {
		var taskID, kind, data string
		if err := rows.Scan(&taskID, &kind, &data); err != nil {
			return nil, err
		}
		out[taskID] = ActivityLine{Data: data, Kind: kind}
	}
	return out, rows.Err()
}
