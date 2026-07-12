# Architecture Review — 2026-07-12

A codebase-wide pass for architectural friction, duplication, dead code, and
error-handling smells, informed by the *improve-codebase-architecture* skill's
vocabulary (deep vs **shallow** modules, **seams**, **locality**/**leverage**, the
**deletion test**). Four parallel explorers swept core/orchestrator, the agent /
terminal / web / mcp layers, the React frontend, and cross-cutting concerns; every
finding was verified against the actual code.

**Overall:** the codebase is unusually clean and thoroughly commented — the
lifecycle state machine, the ask_human protocol, and the self-heal loop are all
carefully reasoned in-place. Findings are refinements, not rescues.

The safe, behavior-preserving items were **applied this pass** (build + vet + all
73 Go tests + `tsc` + `vite build` green after each). The larger structural
candidates are **documented below, not applied**, because they touch the
concurrency-critical core or a documented design intent and deserve a human's call.

---

## Applied this pass

### Backend (Go)

1. **`interactiveAgentFor` helper** (`internal/orchestrator/orchestrator.go`).
   The `agents[t.Agent]` → nil-fallback → `interactiveAgent` type-assert block was
   copy-pasted in `start`, `Revise`, `Reengage`, and `Rebase` (four copies that had
   already drifted to different error strings). Collapsed to one method.

2. **`launchResume` helper** (same file). The `go func(){ acquire(); defer
   release(); ResumeCommand(); injectPort(); runWithSelfHeal() }` launch — the
   riskiest wiring in the package — existed in three near-identical copies
   (`Revise`/`Reengage`/`Rebase`). Unified into one, parameterized by prompt,
   running-message, port, and the build-error prefix.

3. **Collapsed the two `LatestActivity*` queries** (`internal/store`,
   `internal/core`, `internal/web`). `LatestActivity` and `LatestActivityKind` ran
   the *identical* `MAX(id)` self-join, differing only in the selected column — and
   the board ran both on every snapshot. Now one query returns an `ActivityLine
   {Data, Kind}`; the service splits it into the two maps the wire format still
   expects. Half the DB work, one source of truth. (`LatestActivity` even already
   selected `e.kind` and discarded it.)

4. **Swallowed errors surfaced** (`internal/core/service.go`). `NewID` now panics on
   a `crypto/rand` failure instead of silently returning an all-zero primary key;
   `RetryTask` propagates its `UpdateStatus` error instead of claiming success;
   `SetWorktree`/`SetPort` now log a failed persist (matching `SetAttempt`) rather
   than vanishing; `appendEvent` logs a failed event write.

5. **`commitPending` no longer swallows a real `git add` failure**
   (`internal/worktree/worktree.go`). It returned `nil` (success) on *any* `add -A`
   error, so a genuine staging failure let `Merge`/`Rebase` proceed as if the
   agent's edits were committed. `git add -A` succeeds on a clean tree, so a
   non-nil error is now surfaced (both callers already abort cleanly on it).

6. **`Claude.Run` reuses `writeMCPConfig`** (`internal/agent/claude.go`) instead of
   re-inlining the same temp-file MCP-config dance.

### Frontend (React / TypeScript)

7. **Removed dead code:** `AnswerBox`'s unused `compact` prop, `useBoard`'s unused
   public `reload`, `MenuItem.hint` (no item ever set it), and the never-called
   `focus` method on `AgentTerminalHandle`.

8. **`errMsg(e, fallback)` helper** in `api.ts`, replacing 12 hand-written copies of
   `e instanceof Error ? e.message : "…"` across 8 components.

9. **Consolidated task-status groupings** into `util.ts` (`CANCELLABLE`,
   `DELETABLE`, `DEV_LINK_STATUSES`, `CLOSED`) — the single TS home for status
   classification, co-located with `groupColumns`/`activeStep`.

### Docs

10. Reconciled `spec/roadmap.md` (migrations, WAL-checkpoint-on-close, kill hygiene,
    Gin/goccy, M1 ports/dev-server/freshness/rebase, and the merge-conflict /
    no-worktree UX dead-ends were all marked open but are **implemented**) and
    fixed `spec.md`'s five dead `See spec/*.md` pointers to files that were never
    written.

---

## Deferred — structural candidates (not applied)

These are **deepening opportunities**, presented for a human decision.

### A. `Service` is a wide, nil-able seam (`internal/core/service.go`)

`Service` holds six post-construction dependencies (`wt`, `term`, `reengage`,
`ports`, `dev`, plus `store`/`Broker`), each wired by a `UseX` setter and each
guarded by `if s.x != nil` across ~10 methods. This is **temporal coupling**: the
object isn't fully usable until the setters run, with no compile-time guarantee, and
every path pays a nil-check tax.

- **Deletion test:** the setters aren't pass-throughs — they carry the real
  API-only-vs-full-daemon distinction. But the *shape* (six independent optional
  deps on one struct) is the friction.
- **Direction:** group the optional collaborators into one `runtime` seam (worktree
  + term + reengage + ports + dev — the things absent in API-only/test setups)
  injected at construction, so `Service` has a `store`/`Broker` core plus one
  optional `runtime`, and the nil-checks collapse to one.
- Note the coupled nesting at `AnswerHuman`: the re-engage path is gated *inside*
  `if s.term != nil`, so a reengager wired without a terminal manager would never
  fire. A single `runtime` seam removes that accidental dependency.

### B. Prop-drilling on the board (`web/src`)

`activity, activityKind, now, onOpen, projects` are threaded verbatim through
`App → SwimlanesBoard(Lane) → PipelineBoard → Column → TaskCard`, and the same
Props shape is re-typed by hand in five components (which have already drifted).
`activity`/`activityKind` are whole maps passed down only to be indexed at the leaf.

- **Direction:** a small `BoardContext` (or a per-task selector) for the cross-
  cutting board data, plus one shared `BoardItemProps` type. Removes a 5-file edit
  for any board-data change and the biggest coupling in the frontend.

### C. The headless `Agent.Run` pipeline is dead today (`internal/agent`)

`Agent.Run` and both `Claude.Run` / `Codex.Run` (with `parseStreamLine`,
`parseCodexLine`, `summarizeTool`, `compactArgs`, the `Event` struct — ~150 lines)
have **no non-test callers**: the orchestrator only ever type-asserts to
`interactiveAgent` and uses `Command`/`ResumeCommand`. The code is deliberately
kept "for future non-interactive flows" (M2), so this is a **judgment call, not a
clear delete**: keep it as scaffolding for the flow engine, or remove it now and
reintroduce when M2 actually needs streaming. Flagged so the choice is explicit.

### D. Cross-language / cross-package duplication with drift risk

- **Status groupings** are defined in Go (`cancellableStatuses`,
  `deletableStatuses`, the port-holding states) and re-derived in TS. Consolidating
  the TS side (item 9) helps, but the Go↔TS split remains hand-synced — a shared
  source (an endpoint that reports the groupings, or codegen) would remove the
  drift entirely. Weigh against the added machinery.
- **`{path, added, removed}`** exists as `model.ChangedFile`, `worktree.DiffFile`,
  and TS `DiffFile`; `captureContext` copies field-by-field. `worktree.DiffFile`
  could *be* `model.ChangedFile` (same JSON), removing one type and the copy.
- **The `PORT`/`ULTRAFLOW_PORT` env contract** is written in `orchestrator.injectPort`,
  `devserver.Start`, and described again in `portInstruction` — three places to
  change the var name.
- **Default port `7787`** is a literal in six files (`main.go`, `vite.config.ts`,
  the plist, README, goreleaser, a `web.go` comment); the plist passes `-port 7787`
  explicitly, so it can silently diverge from the flag default.

### E. The phantom `planning` status

`model.StatusPlanning` is declared, guarded in `cancellableStatuses`/`RecoverInFlight`,
and rendered in the UI, but **no code ever transitions a task into it**. It's
aspirational (the M2 plan→build flow). Either wire it when M2 lands or drop it —
today it spreads a dead concept across both languages and the spec.

### F. Minor, low-priority

- `internal/mcp` has **no tests**, yet `finish_task` carries real
  report-vs-summary ordering + `→ review` logic; spec.md calls `ask_human` "the
  whole product". A small handler-level test would give it a net.
- `main.go` shutdown uses `srv.Close()` (abrupt) where the comment says "graceful";
  `http.Server.Shutdown(ctx)` drains in-flight SSE/API requests. And a
  `ListenAndServe` bind error `log.Fatal`s *before* `st.Close()` runs, so the WAL
  isn't checkpointed on that exit path.
- The two agent adapters diverge: `Claude.Run` passes `--fallback-model sonnet`,
  Codex has no resilience flag; their activity-event labelling also differs
  (Codex always says "Bash"/"Edit"; Claude distinguishes tools).
- Several near-identical UI pairs remain (`MergeAction`/`MarkDoneAction`,
  `MergeFailedCard`/`FailedCard`, the shots-grid + diff-magnitude blocks shared by
  `CheckpointContext`/`ReviewPanel`) — one parameterized control each.
