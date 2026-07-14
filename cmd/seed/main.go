// Command seed populates a database with demo tasks for visual verification of
// the board. Throwaway; not part of the product. Uses non-backlog statuses so
// the orchestrator leaves them alone.
package main

import (
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
	// port sets a demo dev-server port on a task so the card's localhost:PORT link
	// renders (distinct ports mirror the M1 per-worktree allocation).
	port := func(t model.Task, p int) { svc.SetPort(t.ID, p) }

	// Only claude/solo are implemented, and CreateTaskFull normalizes anything
	// else to them — so the seed uses claude/solo directly rather than varied
	// literals that would render as uniform cards anyway.
	port(mk("Add rate-limit meter to the topbar", "Show N run · M queued.", "ultraflow", "claude", "solo", model.StatusRunning, "Edit internal/web/web.go"), 41207)
	// Two tasks in review, each with a DISTINCT reserved dev-server port and a
	// worktree — mirrors two parallel worktrees landing, so the board shows both
	// localhost:PORT links side by side (the M1 acceptance, visualized).
	rv1 := mk("Port allocation for dev servers", "", "worktrees", "claude", "solo", model.StatusReview, "")
	port(rv1, 41562)
	svc.SetWorktree(rv1.ID, "/Users/you/Code/worktrees/.ultraflow/worktrees/"+rv1.ID)
	rv2 := mk("Wire SSE reconnect backoff", "", "ultraflow", "claude", "solo", model.StatusReview, "")
	port(rv2, 41839)
	svc.SetWorktree(rv2.ID, "/Users/you/Code/ultraflow/.ultraflow/worktrees/"+rv2.ID)
	mk("Draft the flows YAML schema", "", "ultraflow", "claude", "solo", model.StatusDone, "")
	// A task the human Stopped mid-run: it sits in the Done column as a muted
	// "Stopped" card (see groupColumns/TaskCard) and can be Removed or cleared.
	mk("Try a second empty-state layout", "", "worktrees", "claude", "solo", model.StatusCancelled, "stopped by you")
	mk("Migrate answer store to WAL", "", "ultraflow", "claude", "solo", model.StatusFailed, "go build ./... : undefined: foo")
	mk("Add keyboard shortcuts help", "", "worktrees", "claude", "solo", model.StatusQueued, "")

	// A reviewed task whose branch fell behind main: the card shows a "stale ·
	// N behind main" warning (the auto-rebase runs at merge). The freshness event
	// is the task's latest activity, so LatestActivityKind reports "stale".
	stale := mk("Refactor the worktree merge flow", "", "ultraflow", "claude", "solo", model.StatusReview, "")
	svc.AppendTaskEvent(stale.ID, "stale", "stale · 3 behind main")

	// A task waiting on the human. AskHuman just persists the request row and
	// flips the task to needs_human — no goroutine to park — so the pending row
	// exists for the visual check.
	waiting := mk("Redesign the empty-board state", "", "ultraflow", "claude", "solo", model.StatusRunning, "Read web/src/App.tsx")
	svc.AskHuman(waiting.ID,
		"I made two empty-state variants — which direction?",
		[]string{"Minimal (icon + one line)", "Guided (checklist)"},
		"+64 −0 · web/src/App.tsx · affects first-run only")

	// A LONG question with a context line — exercises the rail's wrapping and the
	// "the question is the hero" hierarchy (the readability case the redesign fixes).
	pay := mk("Wire the Stripe webhook", "", "ultraflow", "claude", "solo", model.StatusRunning, "Edit internal/pay/webhook.go")
	svc.AskHuman(pay.ID,
		"Should I verify the webhook signature with Stripe's official SDK helper, or roll a manual HMAC check against the raw body? The SDK adds a dependency but handles replay windows for us.",
		[]string{"Stripe SDK helper", "Manual HMAC"},
		"security-critical · the agent won't proceed until you pick")

	// A VISUAL checkpoint: shots present → the rail routes to the full page instead
	// of answering inline. Built through the store so we can attach fake shots/diff
	// without a real worktree (captureContext would return empty for a seed task).
	vis := mk("Redesign the landing page", "", "ultraflow", "claude", "solo", model.StatusRunning, "wrote web/src/Landing.tsx")
	_ = st.CreateHumanRequest(model.HumanRequest{
		ID: core.NewID(), TaskID: vis.ID, Status: "pending",
		Question: "Frontend's ready — does the hero look right to build on?",
		Added:    180, Removed: 24,
		Shots:     []string{"hero.png", "hero-dark.png"},
		CreatedAt: time.Now().Add(-1 * time.Minute),
	})
	svc.UpdateStatus(vis.ID, model.StatusNeedsHuman)

	// A failure with a LONG error — exercises the two-line clamp in the failed row.
	longFail := mk("Refactor worktree ports", "", "ultraflow", "claude", "solo", model.StatusFailed,
		"./internal/worktree/ports.go:41:12: undefined: allocPort — the self-heal loop tried three times and each build failed on the same missing symbol; giving up so you can look")

	// A merge that couldn't land: a review task whose latest event is merge_failed.
	badMerge := mk("Add SSE reconnect backoff", "", "ultraflow", "claude", "solo", model.StatusReview, "")
	port(badMerge, 41902)
	svc.AppendTaskEvent(badMerge.ID, "merge_failed", "CONFLICT in web/src/App.tsx — auto-rebase onto main left the tree dirty")
	_ = longFail

	log.Printf("seeded %s", *dbPath)
}
