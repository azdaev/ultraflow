package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestHandoffMigrationRepairsLegacyReviewTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for i, migration := range migrations[:9] {
		if _, err := db.Exec(migration); err != nil {
			t.Fatalf("apply legacy migration %d: %v", i+1, err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version = 9`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	insert := `INSERT INTO tasks
		(id,title,body,project,agent,flow,status,worktree,outcome,attempt,max_attempts,port,resume,created_at,updated_at)
		VALUES (?,?, '', '', 'claude', 'solo', 'review', '', '', 0, 3, 0, 0, ?, ?)`
	for _, id := range []string{"with-report", "without-report"} {
		if _, err := db.Exec(insert, id, id, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO events (task_id,kind,data,created_at) VALUES ('with-report','report','# Done',?)`, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	valid, err := st.GetTask("with-report")
	if err != nil {
		t.Fatal(err)
	}
	if valid.Status != "review" || !valid.Handoff {
		t.Fatalf("reported review changed unexpectedly: status=%s handoff=%v", valid.Status, valid.Handoff)
	}

	incomplete, err := st.GetTask("without-report")
	if err != nil {
		t.Fatal(err)
	}
	if incomplete.Status != "failed" || incomplete.Handoff {
		t.Fatalf("incomplete review was not repaired: status=%s handoff=%v", incomplete.Status, incomplete.Handoff)
	}
	events, err := st.TaskEvents(incomplete.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != "error" {
		t.Fatalf("repair should explain itself with one error event: %s", fmt.Sprint(events))
	}
}

func TestAddFeedback(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "feedback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.AddFeedback("this is great", "/board"); err != nil {
		t.Fatal(err)
	}

	var count int
	var message, path string
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM feedback`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 feedback row, got %d", count)
	}
	if err := st.db.QueryRow(`SELECT message, path FROM feedback`).Scan(&message, &path); err != nil {
		t.Fatal(err)
	}
	if message != "this is great" || path != "/board" {
		t.Fatalf("got message=%q path=%q, want %q %q", message, path, "this is great", "/board")
	}
}
