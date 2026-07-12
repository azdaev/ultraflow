// Command seed populates a database with demo tasks for visual verification of
// the board. Throwaway; not part of the product. Uses non-backlog statuses so
// the orchestrator leaves them alone.
package main

import (
	"context"
	"flag"
	"log"
	"time"

	"ultraflow/internal/core"
	"ultraflow/internal/model"
	"ultraflow/internal/store"
)

func main() {
	dbPath := flag.String("db", "seed.db", "db path")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	svc := core.NewService(st)

	// Register the projects the demo tasks reference, so chips/swimlanes render.
	_, _ = svc.CreateProject("ultraflow", "/Users/you/Code/ultraflow")
	_, _ = svc.CreateProject("worktrees", "/Users/you/Code/worktrees")

	mk := func(title, body, project, agent, flow string, status model.TaskStatus, activity string) model.Task {
		t, _ := svc.CreateTaskFull(title, body, project, agent, flow)
		if status != model.StatusBacklog {
			svc.UpdateStatus(t.ID, status)
		}
		if activity != "" {
			svc.AppendTaskEvent(t.ID, "tool", activity)
		}
		return t
	}

	// Only claude/solo are implemented, and CreateTaskFull normalizes anything
	// else to them — so the seed uses claude/solo directly rather than varied
	// literals that would render as uniform cards anyway.
	mk("Add rate-limit meter to the topbar", "Show N run · M queued.", "ultraflow", "claude", "solo", model.StatusRunning, "Edit internal/web/web.go")
	mk("Wire SSE reconnect backoff", "", "ultraflow", "claude", "solo", model.StatusRunning, "Bash go test ./...")
	mk("Port allocation for dev servers", "", "worktrees", "claude", "solo", model.StatusReview, "")
	mk("Draft the flows YAML schema", "", "ultraflow", "claude", "solo", model.StatusDone, "")
	mk("Migrate answer store to WAL", "", "ultraflow", "claude", "solo", model.StatusFailed, "go build ./... : undefined: foo")
	mk("Add keyboard shortcuts help", "", "worktrees", "claude", "solo", model.StatusQueued, "")

	// A task waiting on the human (parks no goroutine — the request row is enough
	// for a visual check; answering via the API just updates the DB).
	waiting := mk("Redesign the empty-board state", "", "ultraflow", "claude", "solo", model.StatusRunning, "Read web/src/App.tsx")
	// Launch and don't wait: AskHuman persists the request row before it parks,
	// so the pending row exists for the visual check. The parked goroutine dies
	// when this throwaway process exits.
	go svc.AskHuman(context.Background(), waiting.ID,
		"I made two empty-state variants — which direction?",
		[]string{"Minimal (icon + one line)", "Guided (checklist)"},
		"+64 −0 · web/src/App.tsx · affects first-run only")
	time.Sleep(200 * time.Millisecond)

	log.Printf("seeded %s", *dbPath)
}
