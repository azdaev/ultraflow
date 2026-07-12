# Plan: show the agent's MODEL name (e.g. "Opus 4.8") on the card, not "Claude Code" / "Codex"

## Decision (confirmed with human via ask_human)
- **Approach A — auto-detect the real model that ran, display-only.** No composer picker,
  no DB migration, no `--model` flag. We read the model each agent actually used from its
  own on-disk transcript and surface it via the SAME in-memory-map + snapshot + SSE
  mechanism the context meter already uses (`PublishContext`/`ContextTokens`).
- **Cover both Claude and Codex.**
- The card keeps the agent **logo** (`AgentMark`); only the **text label** next to it
  changes from `agentLabel(agent)` → friendly model name, falling back to the provider
  label while the model is not yet known (first few seconds, before the transcript exists).

## Why this is the smallest correct change
The orchestrator ALREADY polls Claude's JSONL transcript every 15s in
`internal/orchestrator/contextcap.go` (`watchContext` → `claudeContextTokens` →
`lastContextTokens`). Those exact transcript lines carry `message.model` right next to the
`message.usage` block we already parse. Codex writes rollout JSONL under
`~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`; line 0 is a `session_meta` with
`payload.cwd` (== the task worktree) and `turn_context` lines carry `payload.model`
(verified live on this machine: cwd matched the worktree + `"model":"gpt-5.6-sol"`).

So no new schema/field on `Task` is needed — model is ephemeral runtime state, exactly like
the live token count.

## Backend changes (Go)

### 1. Publish/store the model — mirror the context-token plumbing
`internal/core/service.go`
- Add `modelName map[string]string` guarded by a mutex (add `modelMu`, or reuse `ctxMu`)
  next to `ctxTokens` (struct ~L74, init in `NewService` ~L115).
- Add `PublishModel(taskID, name string)` mirroring `PublishContext` (~L125): store +
  `s.publish("model", map[string]any{"taskId": taskID, "model": name})`. De-dupe: only
  publish when the value actually changed, so we don't spam SSE every poll.
- Add `Models() map[string]string` mirroring `ContextTokens()` (~L134).
- No terminal-state cleanup needed (matches how `ctxTokens` is left in place).

### 2. Fold model map into the board snapshot
`internal/web/web.go` (~L193, the `board` JSON writer): add
`"models": s.svc.Models()` alongside `"context": s.svc.ContextTokens()`.

### 3. Detect the model per running agent — one small poller for both adapters
Start a detector in `runWithSelfHeal` next to `watchIdle` (orchestrator.go ~L440), started
per attempt so a fallback-to-sonnet retry re-detects:
```go
go o.watchModel(sess, taskID, dir, isClaude)
```
`watchModel` (new — put it in contextcap.go or a new `modelwatch.go`): poll every few
seconds until it reads a non-empty model, `PublishModel` it; keep polling cheaply so a
mid-session model change is caught (or return once found — model is stable within a session,
so returning-once is acceptable and simplest). Stops on `sess.Done()`. `dir == ""` → no-op.

Readers:
- **Claude:** reuse `encodeClaudeCwd(dir)` + `newestJSONL` (already in contextcap.go). New
  `claudeSessionModel(dir) (string, bool)`: scan the transcript for the last line with a
  non-empty `message.model`. (Could piggyback on `lastContextTokens` by adding
  `Model string `json:"model"`` under `message`, but a small dedicated reader keeps model
  detection independent of the cap poll so it still fires on the first reading and isn't
  entangled with arm/disarm logic.)
- **Codex:** new `codexSessionModel(dir) (string, bool)`: resolve `$CODEX_HOME` (default
  `~/.codex`), walk `sessions/**/rollout-*.jsonl` newest-first by mtime, read line 0
  (`session_meta`); if `payload.cwd` == `EvalSymlinks(dir)` return the last
  `turn_context.payload.model` in that file. Bound cost: stop at the first cwd match; only
  consider files modified in the last few hours. `EvalSymlinks(dir)` to match codex's
  canonicalized cwd (same trick as `ensureTrusted`).

Dispatch on the `isClaude` flag already available at the call site; non-claude → codex reader.

## Frontend changes (TS/React)

### 4. Friendly model name mapping — new helper in `web/src/util.ts`
Add `friendlyModel(raw: string): string` next to `agentLabel` (~L138). Degrades gracefully:
- `claude-opus-4-8` → "Opus 4.8"; `claude-sonnet-4-5` → "Sonnet 4.5";
  `claude-haiku-4-5-20251001` → "Haiku 4.5" (strip a trailing 8-digit date segment).
- bare CLI shorthands `opus`/`sonnet`/`haiku` (Claude's `--fallback-model`) → "Opus"/…
- `gpt-5` → "GPT-5", `gpt-5-codex` → "GPT-5 Codex", `gpt-5.6-sol` → "GPT-5.6 Sol"
  (uppercase GPT, title-case trailing words).
- unknown → return `raw` unchanged (never blank).

### 5. Thread the model map through the projection (mirror `context`)
- `web/src/api.ts`: add `models: Record<string, string>` to `BoardSnapshot` (~L95).
- `web/src/boardProjection.ts`: add `models` to `BoardProjection` + `emptyBoardProjection`
  (L12/16); add `{ kind: "model"; data: { taskId: string; model: string } }` to `BoardEvent`
  (L32); add `"model"` to the `known` set (L37); snapshot case `models:b.models ?? {}`
  (L47); reducer case
  `case "model": return { ...state, models:{ ...state.models, [event.data.taskId]:event.data.model } }` (L59).
- `web/src/useBoard.ts` / `web/src/board/Board.tsx`: carry `models` alongside `context`
  (Board.tsx L10/21/26 shared object).

### 6. Render on the card
- `web/src/board/Column.tsx` (~L65): pass `model={models[t.id]}` next to `contextTokens`.
- `web/src/board/Card.tsx`:
  - add `model?: string` to `Props` (~L32) and the destructure (~L42);
  - pass it into `AgentFooter` at ~L116 (`<AgentFooter agent={...} model={model} .../>`);
  - in `AgentFooter` (~L270–283) render `model ? friendlyModel(model) : agentLabel(agent)`
    as the text; keep `AgentMark` (logo) unchanged.

### 7. Consistency on the other identity surfaces (same fallback rule)
- `web/src/components/TaskDetail.tsx` (~L212–220, the "Agent" `<dl>` row): show
  `model ? friendlyModel(model) : agentLabel(task.agent)`. Thread `models[task.id]` in the
  same way context is, or accept a `model` prop from the opener.
- `web/src/components/RailCard.tsx` (~L91, `title · agentLabel(...)`): same swap — optional
  but cheap; include for consistency.

## Verification
- `go build ./...` and `go test ./...` (touches `internal/core`, `internal/orchestrator`,
  `internal/web`). Add unit tests for `codexSessionModel` (temp rollout: cwd match + model
  extraction) and `claudeSessionModel` (temp transcript), following the existing
  `contextcap` test style.
- Frontend: `cd web && npm run build` (node via nvm — prepend the nvm bin dir per project
  memory) to typecheck the projection/prop changes. Add a `friendlyModel` unit test covering
  the id shapes above (incl. unknown → raw).
- E2E (per the "Verify frontend SSE" memory): seed a DB, run the daemon on the reserved
  PORT with `-max-concurrent 0`, headless Chrome, and confirm the card footer flips from
  "Claude Code" to the model name once a transcript line appears; inject a "model" SSE event
  to confirm the reducer/render path.
- Capture a before/after screenshot of the board card footer into `.ultraflow/shots/`.

## Files to change (summary)
- `internal/core/service.go` — PublishModel/Models + map
- `internal/web/web.go` — snapshot `"models"`
- `internal/orchestrator/contextcap.go` (or new `modelwatch.go`) — watchModel +
  claudeSessionModel + codexSessionModel
- `internal/orchestrator/orchestrator.go` — start `watchModel` near `watchIdle`
- `web/src/util.ts` — `friendlyModel`
- `web/src/api.ts`, `web/src/boardProjection.ts`, `web/src/useBoard.ts`,
  `web/src/board/Board.tsx`, `web/src/board/Column.tsx`, `web/src/board/Card.tsx` — thread +
  render model
- `web/src/components/TaskDetail.tsx`, `web/src/components/RailCard.tsx` — consistency

## Notes / edge cases
- Fallback to the provider label until the first reading keeps the card from ever showing
  blank.
- Model reflects reality incl. Claude's `--fallback-model sonnet` kicking in — that's
  desired (it shows what actually ran).
- No DB migration, no composer change, no new `Task` field — purely ephemeral runtime state,
  consistent with the context-meter design.
