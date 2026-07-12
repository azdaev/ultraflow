# Tech Debt

Known issues and deferred cleanups. Newest first.

---

## Architecture review (2026-07-12) — deferred deepening candidates

A codebase-wide review applied the safe, behavior-preserving cleanups (helper
extraction in the orchestrator, collapsing the duplicate `LatestActivity` query,
surfacing swallowed errors, a `commitPending` bug, frontend dead-code + an
`errMsg` helper, doc reconciliation). The larger structural candidates it chose
NOT to auto-apply — the `Service` nil-able-dependency seam, board prop-drilling,
the dead headless `Agent.Run` pipeline, Go↔TS status/type/port duplication, the
phantom `planning` status, and `internal/mcp` test coverage — are written up with
rationale in **`spec/architecture-review.md`**.

## Fixed 250ms sleep to submit a board answer into the agent's terminal

**Where:** `internal/core/service.go` — `AnswerHuman` and the `answerSubmitDelay`
package var.

**What it is:** When the human answers an `ask_human` checkpoint, we type the
answer into the parked agent's PTY and then, after a hard-coded
`time.Sleep(250ms)`, write a lone `\r` to submit it. The two-write split is
necessary and correct — interactive TUIs (Claude Code, Codex) treat text and a
trailing CR arriving glued in one read as a *paste* and keep the CR as a literal
newline, so the answer sat typed-but-unsent (the original bug). The **fixed
delay** is the debt: it's a guessed constant tuned to beat the TUIs'
paste-detection window.

**Why it's a smell:**

- **Fragile.** If a TUI widens its paste window (or the machine is under load and
  the two writes still land in one read), 250ms may not be enough and the bug
  returns silently. If it shrinks, we're needlessly slow.
- **Blocking.** The sleep runs inside the synchronous answer HTTP handler, so the
  POST hangs 250ms. Harmless at one answer at a time, but it's latency baked into
  the request path.
- **Guessed, not measured.** The number isn't derived from either CLI's actual
  behavior; it's "big enough, probably."

**More robust options (deferred):**

- **A — Bracketed paste, explicitly.** Wrap the text in `\e[200~ … \e[201~` and
  send the submitting `\r` outside the markers. This tells the TUI unambiguously
  "this chunk is a paste, this CR is a keystroke," removing the timing guess. Best
  structural fix if both CLIs honor bracketed paste on PTY stdin (needs
  verifying).
- **B — Ack-driven submit.** Watch the session's output for the typed text
  echoing back, then send `\r`, instead of sleeping a fixed time. Robust but adds
  a read/parse loop and its own timeout.
- **C — Make the delay non-blocking + configurable.** At minimum, move the sleep
  off the HTTP handler (goroutine) and make `answerSubmitDelay` an env/flag so it
  can be tuned without a rebuild. Cheapest mitigation; doesn't remove the guess.

**Recommendation:** A if bracketed paste works against both CLIs; otherwise B.
The current 250ms sleep ships as an accepted stopgap.

## macOS TCC permission-prompt storm when a task starts

**Symptom:** Starting a task can trigger a burst of macOS privacy prompts —
Downloads, Photos (`~/Pictures`), Apple Music / media (`~/Music`), iCloud Drive
(`~/Library/Mobile Documents`).

**Cause:** three design choices combine to trip macOS TCC (its per-process
privacy layer), which prompts the first time a given process touches a protected
folder:

1. **The daemon runs under launchd** (`deploy/com.ultraflow.daemon.plist`), so
   spawned `claude`/`codex` processes are not children of Terminal.app and do not
   inherit any folder-access grants Terminal already has. macOS sees fresh,
   ungranted processes → prompts.
2. **Agents run unsandboxed with the full environment and full MCP set.**
   `internal/agent/claude.go` uses `--permission-mode bypassPermissions` with no
   `--strict-mcp-config` (intentional — agents keep the human's MCP servers);
   `internal/agent/codex.go` uses `--dangerously-bypass-approvals-and-sandbox`.
   Both pass `os.Environ()` unscrubbed. So a task boots *all* the human's MCP
   servers (qmd, paper, plane, playwright, claude-in-chrome, context7…) and runs
   shell/glob/grep with no sandbox; anything that walks near `$HOME` reaches a
   protected folder. Many servers booting at once = many first-touch prompts.
3. **Worktrees resolve under `$HOME` in the current deployment.** The
   `-worktrees` root defaults to `.ultraflow/worktrees` relative to the daemon's
   launch dir (`cmd/ultraflow/main.go`); here that lands at
   `~/.ultraflow/worktrees`, right beside Downloads / Pictures / Music / iCloud,
   so a stray recursive scan can wander into a protected sibling.

**Not a bug in isolation** — it's the launchd daemon spawning ungranted,
unsandboxed agents that boot the whole MCP fleet and roam the filesystem.

**Fix options (keeping the "full MCP set" feature):**

- **A — Grant the daemon Full Disk Access once.** Add
  `/Users/amady/Code/ultraflow/ultraflow` to System Settings → Privacy &
  Security → Full Disk Access. Children inherit the grant; prompts stop.
  One-time, but broad.
- **C — Move worktrees out of `$HOME`** (e.g.
  `-worktrees /Users/Shared/ultraflow/worktrees`), so a stray scan has no
  protected siblings. One flag in the plist; durable structural fix.
- **B — Curate / re-add `--strict-mcp-config`.** Fewer servers booting = fewer
  scanners. Trades away the deliberately-added "full MCP set" behavior, so it's a
  real trade-off, not a free win.

**Recommendation:** A now (stops prompts without changing behavior), C as the
durable fix. No code changed for this task — diagnosis only.
