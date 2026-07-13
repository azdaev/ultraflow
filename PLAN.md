# Plan: restore automatic task-title compaction

## Root cause

The original feature from commit `15384fd` is still intact for solo tasks: `rename_task`, `Service.RenameTask`, persistence, and SSE updates all remain. Commit `9524a40` added a separate prompt path for multi-step flows, but `buildStepPrompt` never inherited the instruction to call `rename_task`. As a result, flow tasks keep the user's long raw title.

## Implementation

1. In `internal/orchestrator/flowprompt.go`, extract/reuse a small `rename_task` prompt contract and include it when generating the flow's initial (`fl.Start`) work-step prompt. Do not include it on later steps, gate-driven rebuild loops, restart prompts, or self-heal prompts, so the title is compacted once rather than repeatedly rewritten.
2. In `internal/orchestrator/orchestrator.go`, use the same contract in the existing solo-task prompt so solo and flow entry points cannot drift while preserving current solo behavior.
3. Add focused prompt tests under `internal/orchestrator` that assert:
   - solo prompts still request `rename_task` with the correct task ID;
   - a flow's first step requests it;
   - later/re-entered steps do not request it;
   - the full task text remains present for the agent (the existing service tests already cover moving a title-only request into `Body` during rename).

## Verification

- Run `go test ./internal/orchestrator ./internal/core ./internal/mcp`.
- Run `go test ./...` for regression coverage.
- No UI/layout changes are expected, so no screenshots are required.
