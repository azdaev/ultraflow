# Plan — show something useful when you open a Done task

## Problem

Open a task in the **Done** column and the detail modal is almost empty: just the
dashed "No live session" placeholder in the main area plus the small Details rail.

In `web/src/components/TaskDetail.tsx` the main area has three branches:

- `live` (`running`/`needs_human`) → live terminal
- `showReview` = `canRevise && (worktree || report)`, where
  `canRevise = review || failed` → `ReviewPanel` (report + diff)
- else → the empty "No live session" placeholder

A `done` task is neither `live` nor `canRevise`, so it always lands in the empty
placeholder — even though it has a **report**.

## What data a Done task actually still has

I traced the backend (`internal/core/service.go`, `flowrun.go`, `worktree.go`):

- **Report — persists.** `CompleteTurn` (finish_task) writes an `appendEvent(id,
  "report", …)`; events are never deleted on merge/finish. `TaskDetail` already
  computes `report` from these events. For a multi-step flow this is the last
  step's writeup. This is the one reliably-available, meaningful artifact.
- **Result / status one-liners — persist.** `CompleteTurn` also appends a
  `result` event (agent's one-line summary); the merge path appends a `status`
  event ("merged and cleaned up the worktree") and `FinishReview` appends
  "marked done by human". Good material for an outcome line.
- **Diff — gone.** On merge, `Merge` → `s.wt.Remove(...)` tears down the worktree
  checkout (service.go:397). `TaskDiff` then 404s. (`t.Worktree` the string is
  never cleared, so it still reads truthy — a trap: naively reusing `ReviewPanel`
  with `hasWorktree={!!task.worktree}` would show a "Changes" tab that 404s.)
- **Screenshots — gone.** `ShotsDir` = `t.Worktree/.ultraflow/shots`, inside the
  removed worktree. So no shots for a merged done task.

Conclusion: the honest, low-cost win is to **show the report** (plus a one-line
outcome), and *not* offer a diff/Changes tab that can only fail.

## Approach (frontend only, minimal)

Edit **`web/src/components/TaskDetail.tsx`** only:

1. Add `const done = task?.status === "done";`.
2. Compute an outcome summary from existing events, e.g. last `result` event's
   data, falling back to the last `status` event (so there's always one line like
   "merged and cleaned up the worktree").
3. Change the main-area gate so a `done` task renders content instead of the empty
   placeholder:
   - Keep `live` → terminal, and `showReview` → `ReviewPanel` (with ReviseBox) as-is.
   - Add a `done` branch that renders:
     - a small header: "Completed" + the outcome line + `ago(updatedAt)`;
     - if `report` exists → reuse `ReviewPanel` with **`hasWorktree={false}`**
       (report-only, no diff/Changes tab, no 404); ReviewPanel already renders a
       lone Report with no tab bar.
     - if there is **no** report → keep a graceful filled state: the outcome line
       plus a muted "This task ran in place / left no writeup." note, instead of
       the bare "No live session" box.
4. Do **not** render `ReviseBox` for done (it's gated on `canRevise`, already correct).
5. Header already shows `task.status` = "done"; the Details rail (agent, flow,
   project, updated, worktree, body) stays. No rail change needed.

No backend, DB, or API change. `ReviewPanel`/`Markdown` are reused verbatim.

### Optional, explicitly out of scope (note for later)

Reviving the *merged diff* for a done code task would be genuinely useful but needs
backend work (persist the merge commit SHA at merge time + a diff-by-commit path).
Deferring — the report covers the "show something" ask with a frontend-only change.
If the human wants the merged diff too, that's a follow-up task.

## Verification

- Build web with the nvm node on PATH (`web/` — `npm run build` / `vite`).
- Run the daemon on `$PORT` (58647), seed a `done` task with a `report` event and
  one without, open each in the board, confirm:
  - report task → shows the report Markdown, no failing Changes tab;
  - no-report task → shows the outcome line + note, not the empty placeholder.
- `go build ./...` sanity (no Go changed, but cheap).
- Capture screenshots of the Done detail (with-report and without-report) into
  `.ultraflow/shots/` for the review screen.
