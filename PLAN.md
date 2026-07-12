# Plan — route every Claude question through Ultraflow

## Finding

The screenshot shows Claude Code's built-in `AskUserQuestion` TUI. Ultraflow only
enters `needs_human` when the agent calls its MCP `ask_human`, so a native Claude
question remains trapped inside the PTY even though the task prompt explicitly
asks Claude to use the board tool.

Parsing terminal escape sequences or polling Claude's private JSONL format would
duplicate the question protocol and still leave hard problems around multi-question
forms and answer delivery. Claude CLI already exposes the right enforcement point:
`--disallowedTools AskUserQuestion` (confirmed in the installed CLI help). With the
native tool unavailable, Claude follows the existing prompt and uses Ultraflow's
`ask_human`, which already persists the request, sets `needs_human`, emits SSE/web
notifications, renders options, and resumes the PTY after the board answer.

## Implementation

1. In `internal/agent/claude.go`, add `--disallowedTools AskUserQuestion` to every
   Claude launch path: fresh interactive sessions, resumed interactive sessions,
   and the legacy/headless `Run` path. Prefer a small shared argument helper or
   constant so a future launch path cannot accidentally omit this invariant.
2. Add `internal/agent/claude_test.go` coverage that builds fresh and resumed
   commands and asserts the native question tool is denied while the Ultraflow MCP
   config remains present. Cover the headless argument construction too if it is
   extracted into the shared helper; no real Claude process or credentials should
   be required.
3. Update the nearby adapter comment to explain that Claude-native questions are
   deliberately disabled because only MCP `ask_human` participates in Ultraflow's
   durable attention lifecycle.

No database, service, or frontend change is needed: `Service.AskHuman` and the
existing board attention UI remain the single source of truth. Codex is unchanged;
it has no competing `AskUserQuestion` tool in this adapter and already receives the
same MCP/prompt contract.

## Verification

- Run `gofmt` on touched Go files and `go test ./internal/agent ./internal/orchestrator ./internal/core` (then `go test ./...` if practical).
- Inspect generated fresh/resume Claude command args in tests to ensure
  `--disallowedTools AskUserQuestion` is always present.
- Manual smoke test: start a Claude task that requires two architectural choices;
  verify Claude calls MCP `ask_human`, the card/toolbar immediately shows
  `needs_human`, the question appears on the board, and answering it resumes the
  same terminal session. Confirm no native terminal questionnaire appears.

This is backend/process configuration only, so no visual screenshot is required.
