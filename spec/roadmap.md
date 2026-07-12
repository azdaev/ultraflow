# Roadmap

Multi-agent + configurable flows are day-one *architecture*. Adapters and UI polish
land incrementally so we can debug the core loop before fanning out.

## Now — terminal UX

The live terminal exists but the surrounding UX fights it. Fix so the terminal is
a calm, opt-in "peek at progress", not something to babysit.

- [ ] **Big modal, not a sidebar.** Clicking a card opens a large near-fullscreen
      modal (overlay), with the terminal taking most of the area and task details
      secondary — instead of the current cramped right drawer.
- [ ] **Drop the tool-event list.** The "read this file / used this tool" activity
      thread under the terminal duplicates what the terminal already shows and the
      two visually clash. With the live terminal, delete that rendering.
- [ ] **DB / install / UX audit.** Confirm the SQLite DB in `~/.ultraflow`
      survives a `brew upgrade` and app restart (data must persist across binary
      swaps); confirm the daemon shuts down cleanly on kill (no corruption, in-
      flight tasks recovered); confirm the install is via brew (not a raw local
      build); and sanity-check that the board UX is intuitive and smooth.
- [ ] **Session auto-closes on stage completion.** When the agent finishes its
      turn the session ends itself (→ review) and frees its slot — the human never
      opens the card to manually stop it. The terminal is only for optionally
      watching or stepping in; the whole point is not to babysit agents. (Also
      fixes the interactive session holding a concurrency slot forever.)

## M0 — Walking skeleton  ← current

Prove the full loop end to end with ONE agent:
task → agent in a worktree → `ask_human` → answer on board → agent continues → done.

- [x] Go module + package layout
- [x] SQLite store + schema (tasks, events, human_requests)
- [x] HTTP API + SSE for the board (`/api/board`, `/api/tasks`, `/api/events`,
      `/api/tasks/{id}/events`, `/api/tasks/{id}/retry`, answer endpoint)
- [x] MCP server: `create_task`, `list_tasks`, `get_task`, `ask_human` (blocking)
- [x] Claude Code adapter (spawn `claude` headless, stream-json parsed into
      friendly activity events, resume TBD)
- [x] Minimal orchestrator wiring (solo flow, concurrency limiter)
- [ ] Refactor API onto **Gin** + **goccy/go-json** (fast serialization; via gin `go_json` build tag)
- [x] Frontend: React + Vite + Tailwind v4 + Motion board built from the Paper
      designs — attention rail (checkpoint + failure), pipeline columns with flow
      stepper + live activity strip, New Task composer, Task Detail thread drawer,
      live SSE. Verified end-to-end in a browser against a seeded DB.

Remaining M0 polish: Gin/goccy refactor; agent-session resume; the sessions table.

## M1 — Worktree manager  ← in progress

- [x] Per-task git worktree off the project's `repoPath`, on branch
      `ultraflow/<taskID>` (`internal/worktree`). Agents run in isolated
      checkouts so parallel work can't collide. Idempotent create (a retry reuses
      + prunes the same branch/path). Graceful fallback: non-git project folder →
      run in it directly; no project → shared `-workdir`. Worktree path recorded
      on the task and shown live. Verified end-to-end with a live `claude` agent
      (task → worktree → agent runs in it → review).
- [x] Startup reconciliation: a daemon restart requeues tasks left mid-flight
      (queued/running/needs_human) to backlog and retires their orphaned human
      checkpoints, so nothing is stranded with no recovery path.
- [x] Merge + teardown: a reviewed task's worktree changes are committed and its
      branch merged into the project repo (`worktree.Merge`), then the worktree is
      torn down and the task marked done (`Service.MergeTask`, `POST
      /api/tasks/{id}/merge`, "Merge → done" button on review cards). A conflict
      aborts cleanly and returns the task to review with its worktree intact.
- [ ] Ports / dev-server allocation, diff+screenshot captured into ask_human
      context, freshness-vs-main / auto-rebase.

**Presentation honesty (M0):** only the implemented agent (Claude) and flow
(Solo) are selectable; the designed-but-unwired flows/adapters show disabled as
"· soon". Task creation normalizes any other choice down to claude/solo so a
card can never claim a task ran an agent or multi-step flow it didn't.

## Hardening — from the DB/kill/UX audit

Verified good: the DB lives in `~/.ultraflow` (outside the brew cellar) so it
**survives `brew upgrade`**; install is correctly **brew-based** and current;
WAL keeps committed data safe across an unclean kill; `RecoverInFlight` requeues
in-flight work on restart and spares `review`/`done`. Real gaps to fix:

- [ ] **Schema migrations (do before ANY schema change).** `store.migrate()` is
      `CREATE TABLE IF NOT EXISTS` only — no `user_version`, no `ALTER`. A future
      release that adds a column will NOT apply it to existing `~/.ultraflow` DBs,
      breaking upgraders at the first query. Add a `user_version`-gated migration
      runner *before* M2 touches the schema. (v0.5.0 changes no schema, so it's
      safe; this is a landmine for the next schema change.)
- [ ] **Kill hygiene.** Agents run detached (`Setsid` via creack/pty); on SIGKILL
      of the daemon `term.CloseAll` never runs, and `KeepAlive=true` respawns the
      daemon which re-runs `RecoverInFlight` and may re-spawn agents on the same
      worktree while old ones are still dying (collision window). Kill the process
      *group* in `Session.Close` (not a single PID) so grandchildren die too.
- [ ] **DB close / WAL checkpoint on shutdown.** No `db.Close()` anywhere; add one
      + `wal_checkpoint(TRUNCATE)` on graceful exit for hygiene (SQLite auto-
      checkpoints, so durability is fine — this is cleanliness, low priority).
- [ ] **UX dead-ends.** (a) A merge conflict silently returns the card to review
      with only a tiny inline error — no attention-rail entry, no persisted "why".
      (b) A `review` task with no worktree (non-git / shared-workdir) shows NO way
      to finish it — no button, dead end. (c) Composer shows many disabled "· soon"
      flow/agent options — reads as broken to a first-time user.

## M2 — Flow engine

YAML config; preset `plan → build → critic → human-gate`. Steps share a worktree,
human gates between steps. Flows are a **graph** (steps can loop back — e.g. TDD's
critic → redo-tests loop), not a strict line. Ship a set of **premade flow
templates** (Solo, Plan→Build, Plan→Build→Critic→Gate, TDD-with-critic-loop,
Frontend+visual-gate) that double as the starting points for new flows.

**Failure self-heal:** on a step error the agent auto-diagnoses and retries up to
N times (per-flow) before escalating as a `needs_human` item — see spec.md
"Failure self-heals". No dedicated failure screen; it's a card sub-state.

## M3 — More adapters

Codex + opencode (interface already exists; just add impls).

## M4 — Board polish & merge

Live SSE everywhere, diff review UX, merge management, stale-worktree warnings.

## Distribution (share with friends)

Ultraflow is BYO-subscription: each user installs and logs into their own agent
CLI (`claude` etc.); nothing ships secrets. Target audience is Mac developers.

- [x] **Single self-contained binary** — `go:embed` bakes `web/dist` into the
      binary behind the `embed` build tag (`make build` → `go build -tags embed`).
      Dev builds (no tag) still serve the frontend from disk, so a fresh checkout
      compiles without a prebuilt frontend. Verified: the embed binary serves the
      full UI + API from a directory with no `web/dist` alongside it.
- [ ] **Release + install channel** — `.goreleaser.yaml` is ready (universal mac
      binary + linux, frontend built in the `before` hook, Homebrew tap section).
      Needs: push the repo to GitHub, create a `homebrew-tap` repo, fill the
      `CHANGEME-github-user` placeholders, then `git tag v0.1.0 && goreleaser
      release --clean`. Result: `brew install <you>/tap/ultraflow`.
- [ ] Cross-platform folder picker (currently macOS `osascript` only) — a plain
      "paste repo path" field for non-mac users. Not blocking for a Mac audience.

## Ideas / later (not scheduled)

- **Context cap / auto-compact at a threshold.** Agents now ship ~1M context
  windows, which is often too much — quality/cost degrade long before it fills.
  Claude Code has no configurable auto-compact point. Ultraflow could enforce a
  per-agent context budget (e.g. compact/summarize around ~250k tokens) as a
  first-class, per-flow setting. Applies across adapters since we own the launch
  + session lifecycle.
