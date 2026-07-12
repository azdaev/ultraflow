# Plan — gate approval must not restart at Plan

## Finding

This appears fixed and released in `v0.10.5` by the multi-step lifecycle audit
(`c35fe67` / merge `d2addf4`). The flow cursor is persisted, gate approval routes
through `resumeGate`, and a terminal approval calls `FinishFlow` directly. The
existing `TestFlowWalksSharedWorktreeToGateThenApprove` asserts that approval
lands in Review and launches no extra work-step turn. It passes. Local Homebrew
metadata and the installed binary both report `0.10.5`.

## Build-step approach

1. Re-run the focused orchestrator acceptance test and the complete Go suite to
   confirm the checked-in fix remains green.
2. Inspect the installed daemon/process version if the symptom is still visible;
   an already-running pre-`0.10.5` daemon may need restarting even after Brew was
   upgraded.
3. Make no production-code change unless the focused test can be reproduced as
   failing. If a remaining restart-specific reproduction is found, fix it in
   `internal/orchestrator/flow.go` / `internal/core/flowrun.go` and add the exact
   regression to `internal/orchestrator/flow_test.go`.

## Verification

- `go test ./internal/orchestrator -run TestFlowWalksSharedWorktreeToGateThenApprove -count=10`
- `go test ./...`
- Confirm `brew info azdaev/tap/ultraflow` shows `0.10.5` and restart the running
  daemon before manual validation: run Plan → Build → Critic → Gate, approve,
  and verify the card enters Review without Plan (or any agent step) launching.

No visual changes are planned.
