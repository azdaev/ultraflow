# Plan: restore model names on Codex and Claude flow cards

## Diagnosis

- The feature was merged in `3905a79`: transcript readers, in-memory `models`, board/SSE plumbing, and card rendering are all present and their current unit tests pass.
- The original implementation started `watchModel` only from the solo execution path (`runAgent`). Plan/Build/Critic/Gate tasks use the separate `runStepTurn` path, so neither Codex nor Claude ever published a model for those cards.
- `abfa46b` added the missing watcher to `runStepTurn`, but it landed after tag `v0.10.13`. The daemon currently serving `:7787` is `/opt/homebrew/Cellar/ultraflow/0.10.13`; its live `/api/board` returns `models: {}` even though current Codex and Claude transcripts contain valid model fields. Current `main`/`v0.10.14` already contains the functional wiring.

## Implementation

1. In `internal/orchestrator/orchestrator.go`, extract the shared per-session observer startup (context watcher for Claude plus model watcher for both agents) into one helper near `runAgent`.
2. In `internal/orchestrator/flow.go`, call that same helper from `runStepTurn`. This preserves the fix already on `main` while removing the duplicated wiring that allowed solo and flow behavior to diverge.
3. In `internal/orchestrator/flow_test.go`, make the flow model regression test cover both Codex rollout JSONL and Claude transcript JSONL. Keep the reader-level cases in `modelwatch_test.go`; the flow test should prove each adapter publishes into `Service.Models()` through the real `runStepTurn` path.
4. No frontend/API/schema change is needed: `models` snapshot/SSE projection and `friendlyModel` rendering already work. No visual layout changes are planned.

## Verification

- Run `go test ./internal/orchestrator ./internal/core ./internal/web`, then `go test ./...` and `go build ./...`.
- Build/run the current daemon on the reserved port `64526` with an isolated DB and verify `/api/board` receives model entries for short Codex and Claude flow turns backed by representative transcripts.
- Confirm the production remedy is an upgrade/restart from the installed `v0.10.13` daemon to a build containing `abfa46b` (current `v0.10.14` or later); without replacing the running binary, the code fix cannot affect the live board.
