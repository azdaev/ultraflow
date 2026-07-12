# Tech Debt

Known issues and deferred cleanups. Newest first.

---

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
