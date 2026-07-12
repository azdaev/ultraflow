# Roadmap

Multi-agent + configurable flows are day-one *architecture*. Adapters and UI polish
land incrementally so we can debug the core loop before fanning out.

## Now — terminal UX  ← shipped (v0.5.0)

The live terminal is now a calm, opt-in "peek at progress", not something to babysit.

- [x] **Big modal, not a sidebar.** Clicking a card opens a large near-fullscreen
      modal (overlay), with the terminal taking most of the area and task details
      secondary — replacing the old cramped right drawer.
- [x] **Drop the tool-event list.** The per-tool activity thread under the terminal
      duplicated what the terminal already shows; with the live terminal it was
      removed (only `error` events still surface, for failed cards with no terminal).
- [x] **DB / install / UX audit.** Confirmed: the SQLite DB in `~/.ultraflow`
      survives a `brew upgrade` (it lives outside the brew cellar); WAL keeps
      committed data safe across an unclean kill; `RecoverInFlight` requeues
      in-flight tasks on restart; install is brew-based. Remaining UX dead-ends are
      tracked under Hardening below.
- [x] **Session auto-closes on stage completion.** `finish_task` sends the task to
      review and frees its slot; a bare turn-end is caught by the idle-watcher
      (`watchIdle`) which sends the idle task to review and kills the session, so an
      interactive TUI can no longer pin a concurrency slot forever.

## M0 — Walking skeleton  ← current

Prove the full loop end to end with ONE agent:
task → agent in a worktree → `ask_human` → answer on board → agent continues → done.

- [x] Go module + package layout
- [x] SQLite store + schema (tasks, events, human_requests)
- [x] HTTP API + SSE for the board (`/api/board`, `/api/tasks`, `/api/events`,
      `/api/tasks/{id}/events`, `/api/tasks/{id}/retry`, answer endpoint)
- [x] MCP server: `create_task`, `list_tasks`, `get_task`, `ask_human`, `finish_task`.
      `ask_human` is **non-blocking**: it posts the question to the board and returns
      immediately, telling the agent to end its turn. The agent (a live interactive
      terminal) then idles at its prompt — a durable, timeout-proof wait — and the
      human's board answer is written straight into its stdin, resuming it. No
      HTTP/tool call is held open across human time, so nothing can time it out.
- [x] Claude Code adapter (spawn `claude` headless, stream-json parsed into
      friendly activity events, resume TBD)
- [x] Minimal orchestrator wiring (solo flow, concurrency limiter)
- [x] Refactor API onto **Gin** + **goccy/go-json** (fast serialization; via gin
      `go_json` build tag — see the `-tags go_json` build in the Makefile / goreleaser)
- [x] Frontend: React + Vite + Tailwind v4 + Motion board built from the Paper
      designs — attention rail (checkpoint + failure), pipeline columns with flow
      stepper + live activity strip, New Task composer, Task Detail thread drawer,
      live SSE. Verified end-to-end in a browser against a seeded DB.

Remaining M0 polish: the dedicated `sessions` table (agent-session resume already
works via `claude --continue` in the task's worktree).

## M1 — Worktree manager  ← done

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
- [x] Ports / dev-server allocation (`internal/port`, `internal/devserver`),
      diff+screenshot captured into ask_human context (`Service.captureContext`),
      freshness-vs-main / auto-rebase (`worktree.Freshness` / `worktree.Rebase`,
      `Service.MergeTask` rebase-then-merge, agent self-heal on conflict).

**Presentation honesty (M0):** only the implemented agent (Claude) and flow
(Solo) are selectable; the designed-but-unwired flows/adapters show disabled as
"· soon". Task creation normalizes any other choice down to claude/solo so a
card can never claim a task ran an agent or multi-step flow it didn't.

## Hardening — from the DB/kill/UX audit

Verified good: the DB lives in `~/.ultraflow` (outside the brew cellar) so it
**survives `brew upgrade`**; install is correctly **brew-based** and current;
WAL keeps committed data safe across an unclean kill; `RecoverInFlight` requeues
in-flight work on restart and spares `review`/`done`. The gaps the audit flagged
have since been closed:

- [x] **Schema migrations.** `store.migrate()` now runs a `user_version`-gated
      migration list in a transaction (`internal/store/store.go`), so a release that
      adds a column applies it to existing `~/.ultraflow` DBs on first open.
- [x] **Kill hygiene.** `Session.Close` kills the process *group*
      (`syscall.Kill(-p.Pid, SIGKILL)`, `internal/terminal/terminal.go`), so
      detached grandchildren die with the leader rather than orphaning onto a
      worktree a respawned daemon might reuse.
- [x] **DB close / WAL checkpoint on shutdown.** `cmd/ultraflow/main.go` calls
      `st.Close()`, which runs `wal_checkpoint(TRUNCATE)` on graceful exit.
- [x] **UX dead-ends.** (a) A merge conflict now appends a `merge_failed` event the
      board lifts into the attention rail (`Service.MergeTask`, `MergeFailedCard`).
      (b) A `review` task with no worktree can be closed via "Mark done"
      (`Service.FinishReview`). (c) Composer's disabled "· soon" options are
      deliberate presentation honesty, not a dead-end.

## M2 — Flow engine  ← core landed

YAML config; preset `plan → build → critic → human-gate`. Steps share a worktree,
human gates between steps. Flows are a **graph** (steps can loop back — e.g. TDD's
critic → redo-tests loop), not a strict line. Ship a set of **premade flow
templates** (Solo, Plan→Build, Plan→Build→Critic→Gate, TDD-with-critic-loop,
Frontend+visual-gate) that double as the starting points for new flows.

- [x] `internal/flow` graph model + presets (Solo, Plan→Build, Plan→Build→Critic→
      Gate) + per-project YAML (`.ultraflow/flows.yaml`, `flow.Load`). See
      `spec/flows.md`.
- [x] Orchestrator flow runner: walks the graph in ONE shared worktree (created
      once at task start), a work step spawns its agent and advances on the turn's
      end, a gate parks `needs_human` and the answer routes the graph. Solo keeps
      its unchanged path so the default can't regress.
- [x] Run persistence (`runs` table, migration 6) — cursor + completed steps; a
      restart resumes mid-flow via `RecoverInFlight` rather than restarting the task.
- [x] `finish_task` flow-aware (`core.CompleteTurn`): mid-flow steps advance
      without flashing to review; the terminal step / solo still goes to review.
- [x] Frontend: `FlowStepper` lights the LIVE active step + caption + sub-agent
      from real progress over SSE; only wired flows selectable, rest show "· soon".
- [ ] Remaining templates: TDD-with-critic-loop, Frontend+visual-gate (the engine
      runs graph loops already; these are additional presets).
- [ ] Composer: per-project flow picker sourced from `flow.Load` (today it lists
      the in-code presets).

**Failure self-heal:** on a step error the agent auto-diagnoses and retries up to
N times (per-flow) before escalating as a `needs_human` item — see spec.md
"Failure self-heals". No dedicated failure screen; it's a card sub-state.
*(Implemented per step in the flow runner, reusing the solo self-heal policy.)*

## M3 — More adapters

- [x] **Codex** — `internal/agent/codex.go` runs the `codex` CLI as an interactive
      PTY session wired to Ultraflow's MCP server; it's a real, selectable agent in
      the Composer (`implementedAgents = {claude, codex}`).
- [ ] **opencode** — interface already exists (`agent.Agent` + `interactiveAgent`);
      just add the impl.

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
- [x] **Release + install channel** — shipped via GoReleaser to a Homebrew tap:
      `brew install azdaev/tap/ultraflow`. Runs under launchd (`make live` =
      goreleaser release + brew upgrade + launchctl kickstart). (GoReleaser gotcha:
      add `/dist/` to `.gitignore` or its output dirties the tree and aborts the
      release.)
- [ ] Cross-platform folder picker (currently macOS `osascript` only) — a plain
      "paste repo path" field for non-mac users. Not blocking for a Mac audience.

## Context cap — auto-compact at a threshold  ← done

Agents ship ~1M context windows, which is often too much — quality and cost
degrade long before the window fills, and Claude Code's own auto-compact only
fires near the very top. Ultraflow now enforces a configurable context budget:
when a running agent's context crosses the cap, Ultraflow injects `/compact` into
its live session so it summarizes and carries on with a tighter working set.

- **How it's measured.** We own the launch + session lifecycle, so a monitor
  (`orchestrator.watchContext`, started per attempt alongside `watchIdle`) reads
  the agent's own transcript. Claude Code writes a JSONL transcript per session
  under `~/.claude/projects/<encoded-cwd>/`; because every task runs in its own
  worktree (a unique cwd), the newest transcript there is this task's. The live
  context size is the last turn's `usage.input_tokens +
  cache_creation_input_tokens + cache_read_input_tokens` — exactly what was sent
  to the model that turn.
- **How it compacts.** When context ≥ cap and the agent is actively working (not
  parked on `ask_human`, not idle/finishing — gated on `IdleFor` and task status),
  the monitor types `/compact` + Enter into the PTY (the same two-write,
  paste-safe pattern as a board answer) and disarms; it re-arms once context falls
  back below the cap, so it fires once per crossing, not in a loop. A thread event
  records each compaction.
- **The setting.** A daemon-wide `context_cap_tokens` setting (Settings → Context
  budget), mirroring the concurrency control: `0` disables it, otherwise it clamps
  to 50k–1M. Default off. Claude-only for now (codex's transcript format differs).
  Per-flow budgets come with the flow engine (M2).

## Ideas / later (not scheduled)

- **Per-flow context budgets.** Today the cap is daemon-wide; once flows land (M2)
  it becomes a per-flow setting so a "deep refactor" flow and a "quick fix" flow
  can carry different budgets.
