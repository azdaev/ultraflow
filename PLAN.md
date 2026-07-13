# Plan: task outcomes beyond "Merge to main"

## Problem

The review accept-action is binary today, keyed purely off `task.worktree`:

- `web/src/board/Card.tsx:121` and `web/src/components/TaskDetail.tsx:272`:
  `task.worktree ? <MergeAction> : <ApproveAction>`
- `MergeAction` → **"Merge to main"**; `ApproveAction` → **"Approve & close · no diff"**.

But "did it run in a worktree" is a poor proxy for "what did the agent actually
produce". Every flow task runs in a worktree, so a question that was merely
*answered*, an *audit*, or a *design exploration* all show a scary **"Merge to
main"** button even though there is nothing to land. The human wants the accept
control to say the true outcome.

## The ~10 task-outcome scenarios (from history + reasoning)

Looking at real board history (`list_tasks`) the outcomes cluster like this:

| # | Scenario | Example from history | Land in main? | Good accept label |
|---|----------|----------------------|:---:|---|
| 1 | Code change to ship | feedback button `bdeecbea`, dropdown `2ad6179`, tab-title `baa3219` | yes | **Merge to main** (+diff) |
| 2 | Question answered | "task done / in prod?" `23845f` | no | **Close · answered** |
| 3 | Audit / investigation findings | stuck-slot audit `df39754`, model-name probe `0525bd9` | no | **Accept findings** |
| 4 | Design / visual exploration (Paper, screenshots) | squid hero 10 variants `5754795` | no | **Approve design** |
| 5 | Research / gathered artifacts (links, images) | (subset of squid task) | no | **Accept** |
| 6 | Repro / diagnosis, no fix yet | — | no (spawns follow-up) | **Accept · no change** |
| 7 | Side-effect already applied outside the repo (release cut, DB write, external branch/PR) | — | no (already done) | **Mark done** |
| 8 | Spike / throwaway prototype (code exists, not for main) | "refactor from scratch" `da38c78` (kept) | optional | **Merge to main** stays available |
| 9 | Inconclusive / no-op ("couldn't reproduce", nothing to change) | — | no | **Acknowledge** |
| 10 | Blocked / needs a decision | handled today by `ask_human` before finish | — | (n/a) |

The functional axis is only two backend paths — **merge a branch**
(`api.merge` → `Service.MergeTask`) vs **finish without merging**
(`api.markDone` → `Service.FinishReview`). What actually varies across the 10 is
the **label / tone**. So the fix is: let the agent declare a small `outcome`,
persist it, and drive the accept label from it. `merge` is just one outcome, no
longer the default.

## Approach (minimal, matches existing patterns)

Add a first-class `outcome` string the agent sets at `finish_task`, collapsed to
a compact enum. Keep the current worktree/diff heuristic as the fallback for
unset (legacy tasks + agents that don't declare one).

Compact enum (agent-declared): `merge`, `answer`, `design`, `applied`, `none`.
Scenarios above map onto these (2/3/5/6 → `answer`, 4 → `design`, 7 → `applied`,
9 → `none`, 1/8 → `merge`). Only `merge` uses `api.merge`; the rest use
`api.markDone`, differing only in label/sublabel.

### Backend

1. **`internal/model/model.go`** — add `Outcome string \`json:"outcome"\`` to
   `Task` (near `Worktree`).
2. **`internal/store/store.go`** — new migration string appended to `migrations`:
   `ALTER TABLE tasks ADD COLUMN outcome TEXT NOT NULL DEFAULT ''`; add `outcome`
   to `taskCols` + the `scanTask` scan + the `INSERT INTO tasks` column list;
   add a `SetOutcome(id, outcome)` via the existing `touchField` helper (mirrors
   `SetWorktree`).
3. **`internal/core/flowrun.go`** — `CompleteTurn(taskID, summary, report string)`
   gains an `outcome string` param; when non-empty, persist via the store setter
   *before* routing (last non-empty wins, so the final flow step / solo finish
   sets the task's outcome; intermediate steps that omit it don't clobber).
4. **`internal/mcp/server.go`** — add `Outcome string` to `finishArgs` with a
   jsonschema enum + one-line-per-value description; pass to `CompleteTurn`.
   Extend the `finish_task` tool description: "Set `outcome` to say what you
   produced — `merge` (code to land in main), `answer` (a question/audit was
   answered — the report is the deliverable), `design` (visual exploration /
   screenshots), `applied` (already applied outside the repo), `none` (nothing to
   change). It decides the accept button the human sees; default `merge` only if
   you actually changed code to land."

### Frontend

5. **`web/src/api.ts`** — add `outcome?: string` to the `Task` type.
6. **`web/src/components/ReviewActions.tsx`** — collapse `MergeAction` /
   `ApproveAction` into one outcome-driven `AcceptAction({ task })`: a small map
   `outcome → { label, busyLabel, icon, run }`. `merge` keeps the diff fetch +
   `+add −rem` trailing and calls `api.merge`; the others call `api.markDone`
   with their label. Reuse the existing `MossAction` pill unchanged.
7. **Action resolution** in `Card.tsx:121` and `TaskDetail.tsx:272`: pick from
   `task.outcome`; when unset, fall back to the current rule but tightened —
   treat a worktree with an **empty diff** as no-merge (a question answered
   inside a worktree no longer shows "Merge to main"). Update the right-click
   menu label in `Card.tsx:67-70` the same way.

## Verification

- `go build ./...` and `go test ./internal/core ./internal/store ./internal/mcp ./...`;
  add/extend a `CompleteTurn` test asserting the outcome is persisted (final
  non-empty wins; empty doesn't clobber) and an `mcp` test that `finish_task`
  with `outcome` reaches the store.
- Frontend: `npm run build` in `web/` (node via nvm — prepend nvm bin).
- Drive it e2e per the board's SSE pattern: daemon on `$PORT=52876` with an
  isolated DB, headless Chrome; finish one task with `outcome:"answer"` and one
  with `outcome:"merge"`, confirm the review card renders **"Close · answered"**
  vs **"Merge to main"**. Capture both review cards to `.ultraflow/shots/`.

## Open decision (ask the human if build stalls on it)

Exact button wording per outcome is a judgement call — the labels above are a
proposal. If the human wants different words (e.g. "Got it" vs "Close ·
answered"), that's a one-line map change; surface via `ask_human` rather than
guessing if it feels load-bearing.
