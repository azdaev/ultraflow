# Ultraflow — Spec

Local orchestrator for running **many coding agents in parallel** over one or more
projects, without juggling terminals. A kanban board shows per-task status; when an
agent needs the human, it surfaces **very explicitly** with a clear question and fast
context (diff / screenshot), and the human answers with one tap.

> This is the living top-level spec. Detailed per-subsystem docs live in `spec/` and
> are written as each subsystem is built. Keep this file short; push detail down.

## Why (the pain)

Running agents in parallel today means N terminals you must babysit. Adding a task
means opening a new terminal, launching an agent, pasting the task. There is no clear
"this one needs you now, here's the question" signal. Ultraflow fixes exactly that.

## Locked decisions

- **Scope:** local, single-user (just the owner). No auth, no multi-tenant, no cloud.
  State in local SQLite.
- **Stack:** Go backend (single daemon binary) + React / Tailwind / Motion frontend.
- **Agents run on the user's SUBSCRIPTION CLIs** in headless mode (`claude`, `codex`,
  `opencode`) — **not** model APIs. A concurrency limiter is required (N agents share
  one Max rate limit).
- **Multi-agent + configurable flows from day one** in the architecture; adapters are
  implemented incrementally (Claude Code → Codex → opencode).
- **Worktrees by default:** one `git worktree` per task; setup hook, port allocation
  for dev servers, freshness-vs-`main`, teardown.
- **No Telegram.** External input *and* agent questions both flow through Ultraflow's
  **own MCP server**.

## The core insight (this is the whole product)

The MCP tool **`ask_human(question, options[], context)`**. Because an MCP tool call is
request/response, the call **naturally blocks** the agent until the human answers on the
board — then returns the chosen answer to the agent, which continues. That blocking *is*
the human-in-the-loop protocol. Everything else is scaffolding around it.

The agent's system prompt instructs: *when a decision is irreversible, visual, or
architectural — do not guess, call `ask_human`.*

**What to surface (not raw diff):** the human rarely wants to read code. The fast
context should favor (1) a plain-language summary of what changed / why the
question, (2) the *magnitude* of change as `+N −N` line counts (a trusted signal
at a glance), and (3) a screenshot for anything visual. Keep the raw diff reachable
but secondary — it is the exception, not the default surface.

## Architecture (one Go binary)

1. **MCP server** — the heart. Streamable-HTTP MCP endpoint the agents connect to.
   Tools: `create_task`, `list_tasks`, `get_task` (external input) and `ask_human`
   (agent → human, blocking). See `spec/mcp-protocol.md`.
2. **Agent adapter** — Go interface over a subscription CLI (start in worktree, stream
   events, resume). Impls: ClaudeCode (first), Codex, OpenCode. See `spec/agents.md`.
3. **Flow engine** — a flow is a graph of steps `{role, agent, prompt, gate?}`,
   configured in YAML per project / task-type. Presets + custom. See `spec/flows.md`.
4. **Worktree manager** — git worktree per task, setup hook, port allocation, freshness,
   teardown, concurrency limiter. See `spec/worktrees.md`.
5. **Web board** — React/Tailwind/Motion. Kanban, `needs_human` highlighting, question
   card with options + diff/screenshot, answer posts back into the blocked MCP call.
   Live updates via SSE. See `spec/web.md`. Designed in Paper first.

**State:** SQLite — `projects, tasks, flows, runs, events (incl. human_requests),
sessions`. See `spec/data-model.md`.

## Task lifecycle

`backlog → planning → running → needs_human → running → review → merging → done`
(plus `failed`, `cancelled`). `needs_human` carries the question + options + context.

**`needs_human` is a general checkpoint, not just a final gate.** It can fire at
any milestone — a Paper mockup, frontend-only (backend still ahead), an
architecture direction — where the agent wants approval before continuing. The
default answer is *approve → continue to the next step*; merging to `main` is only
one possible outcome, reserved for when the task is actually complete. The board
shows the stage (what's done / what's next) so the human knows what they're
approving. See `spec/web.md` → "Review is a checkpoint, NOT necessarily a merge".

**Failure self-heals; it is a card state, not a destination.** This covers both a
failed step (build/test error, crashed tool) **and a stale/conflicting branch** —
when the task's branch falls behind `main`, the agent **auto-rebases** and resolves
what it can, same policy. The default is **not** to dump a red card and a raw log
on the human — the human will essentially never read a stacktrace, same as
they don't read raw diffs. Instead the agent **auto-retries**: it reads the error,
diagnoses, and re-runs, up to N attempts (per-flow, e.g. 3). During this the task
**stays in `running`** with a `fixing itself · k/N` sub-state on its card; the raw
log is reachable but collapsed/secondary. Only if self-heal **exhausts N attempts**
does it escalate — as an ordinary `needs_human` item in the attention rail, phrased
in plain language ("tried 3×, stuck on X — replan, or guide me?"), not as a special
failure screen. So `failed` is a terminal state reserved for *gave-up-and-you-said-
stop*; the common case never leaves `running`. (There is no dedicated "log & retry"
screen — that was cut; the raw log is a disclosure on the card, nothing more.)

## Roadmap

See `spec/roadmap.md`. Current: **M0 — walking skeleton** (daemon + SQLite + MCP with
`create_task` & `ask_human` + Claude Code adapter + minimal board; prove the full loop).
