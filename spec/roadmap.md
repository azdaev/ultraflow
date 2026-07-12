# Roadmap

Multi-agent + configurable flows are day-one *architecture*. Adapters and UI polish
land incrementally so we can debug the core loop before fanning out.

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
- [ ] Teardown on merge (kept until then so the diff survives review).
- [ ] Ports / dev-server allocation, diff+screenshot captured into ask_human
      context, freshness-vs-main / auto-rebase.

**Presentation honesty (M0):** only the implemented agent (Claude) and flow
(Solo) are selectable; the designed-but-unwired flows/adapters show disabled as
"· soon". Task creation normalizes any other choice down to claude/solo so a
card can never claim a task ran an agent or multi-step flow it didn't.

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
