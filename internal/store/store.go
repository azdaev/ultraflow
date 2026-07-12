// Package store is the SQLite persistence layer for Ultraflow.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
	// WAL for concurrent readers; busy_timeout so a write that briefly collides
	// with another (e.g. a simultaneous answer/cancel of the same request) waits
	// and retries rather than failing with SQLITE_BUSY.
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

// --- tasks ---

func (s *Store) CreateTask(t model.Task) error {
	_, err := s.db.Exec(
		`INSERT INTO tasks (id,title,body,project,agent,flow,status,worktree,created_at,updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Title, t.Body, t.Project, t.Agent, t.Flow, string(t.Status), t.Worktree, t.CreatedAt, t.UpdatedAt)
	return err
}

const taskCols = `id,title,body,project,agent,flow,status,worktree,created_at,updated_at`

func scanTask(sc interface{ Scan(...any) error }) (model.Task, error) {
	var t model.Task
	var status string
	err := sc.Scan(&t.ID, &t.Title, &t.Body, &t.Project, &t.Agent, &t.Flow, &status, &t.Worktree, &t.CreatedAt, &t.UpdatedAt)
	t.Status = model.TaskStatus(status)
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

func (s *Store) SetWorktree(id, wt string) error {
	_, err := s.db.Exec(`UPDATE tasks SET worktree=?, updated_at=? WHERE id=?`, wt, time.Now(), id)
	return err
}

// RecoverInFlight cleans up work stranded by a previous daemon exit. A restart
// kills every agent goroutine, so any task still marked in-flight
// (queued/running/needs_human/planning/merging) has no executor behind it and
// would otherwise sit forever — the board only offers Retry on failed cards.
// Move them back to backlog to be picked up fresh, and cancel their now-orphaned
// pending human requests (the agent that would consume the answer is gone).
// Returns how many tasks were requeued.
func (s *Store) RecoverInFlight() (int64, error) {
	if _, err := s.db.Exec(
		`UPDATE human_requests SET status='cancelled' WHERE status='pending'`); err != nil {
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
	_, err := s.db.Exec(
		`INSERT INTO human_requests (id,task_id,question,options,context,answer,status,created_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		r.ID, r.TaskID, r.Question, string(opts), r.Context, r.Answer, r.Status, r.CreatedAt)
	return err
}

const hrCols = `id,task_id,question,options,context,answer,status,created_at,answered_at`

func scanHumanRequest(sc interface{ Scan(...any) error }) (model.HumanRequest, error) {
	var r model.HumanRequest
	var opts string
	var answeredAt sql.NullTime
	err := sc.Scan(&r.ID, &r.TaskID, &r.Question, &opts, &r.Context, &r.Answer, &r.Status, &r.CreatedAt, &answeredAt)
	if err != nil {
		return r, err
	}
	_ = json.Unmarshal([]byte(opts), &r.Options)
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

// CancelHumanRequest marks a still-pending request cancelled (e.g. the asking
// agent died while parked), so it drops off the attention rail and can no longer
// be answered. Returns whether a pending row was actually cancelled.
func (s *Store) CancelHumanRequest(id string) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE human_requests SET status='cancelled' WHERE id=? AND status='pending'`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
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

// LatestActivity returns, per task, the text of its most recent event — the
// board's live activity strip. Empty-text events are ignored.
func (s *Store) LatestActivity() (map[string]string, error) {
	rows, err := s.db.Query(`
		SELECT e.task_id, e.kind, e.data
		FROM events e
		JOIN (SELECT task_id, MAX(id) AS mid FROM events WHERE data <> '' GROUP BY task_id) m
		  ON e.task_id = m.task_id AND e.id = m.mid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var taskID, kind, data string
		if err := rows.Scan(&taskID, &kind, &data); err != nil {
			return nil, err
		}
		out[taskID] = data
	}
	return out, rows.Err()
}

// LatestActivityKind returns, per task, the kind of its most recent non-empty
// event (parallel to LatestActivity's text). The board uses it to lift a
// "merge_failed" event into the attention rail rather than showing it as a quiet
// status line.
func (s *Store) LatestActivityKind() (map[string]string, error) {
	rows, err := s.db.Query(`
		SELECT e.task_id, e.kind
		FROM events e
		JOIN (SELECT task_id, MAX(id) AS mid FROM events WHERE data <> '' GROUP BY task_id) m
		  ON e.task_id = m.task_id AND e.id = m.mid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var taskID, kind string
		if err := rows.Scan(&taskID, &kind); err != nil {
			return nil, err
		}
		out[taskID] = kind
	}
	return out, rows.Err()
}
