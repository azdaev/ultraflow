# Web board — design

Designed in Paper first: **Ultraflow — Board** (file `01KX9P6V1FN11E1WEGHH3N9J07`).
Built in React + Tailwind + Motion against this direction.

## Visual system

- **Mood:** industrial (concrete × safety orange). Calm board, ONE loud color.
- **Palette:** board `#ECECEA`, surface `#FFFFFF`, ink `#17171A`, muted `#6E6E68`,
  hairline `#E2E2DE`. Accent **safety orange `#F5501E` reserved for `needs_human`
  only**. Status hues from the same scene: steel `#2F6DB0` (running), moss
  `#4F7A4D` (done/ready), rust `#A9432B` (failed).
- **Type:** Onest (UI, 400/500/600/700) + JetBrains Mono (ids, timers, branches,
  line counts, 500). Scale: board title 26/700, column header 12/600 caps
  tracking 0.09em, card title 15/600, question 17/600, meta 13/400, mono 11–12.

## Layout

- **Topbar:** wordmark (orange square mark) · centered "add task" input (⌘N, the
  fast backlog entry) · right cluster: global **"N need you"** orange pill +
  `running/max` agent counter.
- **Columns:** Backlog · Running · **Needs you** · Review · Done. Cards live
  directly on the concrete ground (hairline-bordered white), not boxed columns.
  "Needs you" has an orange header label + badge count.

## Card types

- **Backlog:** title + project chip + agent name. Minimal.
- **Running:** steel dot + "Running" + elapsed timer; title; a live **activity
  strip** (steel-tinted) showing what the agent is doing right now
  (`editing …` / `running go test …`); footer agent + branch.
- **Review:** moss check + "Ready to merge" + **`+N −N` line counts**; title;
  dark "Review & merge" button; footer.
- **Done:** compact — filled check + muted title + `+N −N` + "merged · Xago".
- **Needs you (the hero, two variants).** No colored spine — the signal is the
  orange dot + label, peach border, elevation, and orange CTA. (Left border was
  cut: felt heavy.)
  - **Text question:** status + waiting timer; task caption; **large question**;
    a **context box** = plain-language summary + `+N −N` counts (diff link is
    quiet/secondary — user rarely reads code) + Worktree link; **one-tap option
    chips** (primary filled orange) + a "type a different answer" free-reply row.
  - **Visual review:** the board card is a **compact trigger only** — a wide
    teaser crop (tagged `full page ↗`) + a **"Review the full page"** button. A
    thumbnail can't convey a real page, so the actual review opens a **full-size
    review surface** (`Visual Review` artboard): left = the rendered page large
    and scrollable (real width, e.g. 880px); right = decision panel with the
    question, a **stage indicator** (see below), `+N −N`, a **changed-sections**
    jump list, a note field for the agent, and the decision buttons. Decide
    *there*, after actually seeing it — not on the board. Common case for
    anything the user must see, not read.

### Review is a checkpoint, NOT necessarily a merge

A review can fire at **any milestone**, not just when all code is done: the agent
may have only made a Paper mockup, or only the frontend (backend still ahead), or
just settled an architecture direction. So the primary action is **"Looks good —
continue"** (approve → the agent proceeds to the next step), with a **stage
indicator** showing what's done and what's next (`Frontend done → Backend next`).
**Merge to main is a separate, final-only action** — surfaced when the task is
actually complete, not baked into every review. Mechanically these are all just
`ask_human` checkpoints: the human's answer tells the agent what to do next;
"merge" is only one possible answer.

## v2 — Attention-first (corrected model)

Second Paper frame `Board v2 — Attention-first`. Fixes the structural flaws found
when roasting v1. **This supersedes the "Needs you column" layout above.**

- **Attention ≠ pipeline stage.** `needs_human` is NOT a column. Two axes:
  - **Attention rail** — a loud full-width band under the topbar: everything that
    needs the human's *action*, unified: **checkpoints** (orange), **visual
    reviews** (orange, mini-preview + Review), and **failures** (red, error +
    Retry / View log). Empty → "You're all caught up". This is the one place to
    look. (Push/OS-notification still needed so it works when the tab is unfocused
    — the rail alone is passive; see open issue.)
  - **Pipeline board** — pure stages only: Backlog · Running · Review · Done. A
    task that needs you STAYS in its real stage (e.g. Running) with an orange
    "Needs you · answer above ↑" flag; it also mirrors into the rail.
- **Flows are visible.** Each card shows a 4-segment **flow stepper**
  (`plan → build → critic → gate`) with the active step, plus a caption
  ("Build · step 2 of 4 · critic + your gate next") and the current sub-agent
  ("claude · builder"). Fixes v1 hiding the whole flows/multi-agent pillar.
- **Rate-limit honesty.** Topbar meter: `N run · M queued · limit m:ss`. Columns
  distinguish **Running** (blue, live activity) from **Queued** (muted, "#1 ·
  waiting for a slot · limited by Max cap"). No more pretending queued == running.
- **Health signal** on cards: `tests 12/12`, `build ok` — so you don't approve
  blind. Review card shows "Passed critic · ready" + green test/build chip.
- **Grooming:** Backlog distinguishes **Ready** (green, "starts when a slot
  frees") from **Draft** (dashed, muted, "needs detail before it runs") — tasks
  don't auto-run the instant they're added.
- **Failures don't vanish:** they live in the attention rail with Retry + View log.

**Rail card craft (cleanup pass):** all three rail cards share a bottom action
lane (`justify-between` pins the CTA row to the card bottom) and a 36px control
height. Each card carries a context line under the question: the checkpoint shows
*why* it's asking (plain-language stakes), the visual shows magnitude + scope
(`+180 −24 · full landing page · 6 sections`), the failure shows the error line.
Orange stays the needs-decision family (checkpoint, visual); **failure is red**, a
distinct hue so "broken" never reads as "needs a choice". Free-reply is an explicit
`Other…` chip, not a `···` glyph. The board columns **grow to fill the full width**
so the pipeline aligns edge-to-edge with the rail (no dead gutter on the right).

Still open (from the roast, not yet designed): OS/push notification for
unfocused-tab, long-wait `ask_human` timeout handling, before/after + on-image
annotation for visual review, per-task token cost, swimlanes for many projects.
(Resolved since: merge-conflict / stale-vs-main → **auto-rebase self-heal**, a card
state not a screen; answer/thread history → the **Task Detail thread**.)

## Other screens (Paper frames)

- **Task Detail — Thread.** The context-immersion screen you land on from a board
  card or attention item. Left: task title + magnitude + a large **flow stepper**
  (plan done · build needs-you · critic · gate) + a **THREAD** timeline — dots +
  connector line, chronological (plan ready → wrote handler `+42 −8` → tests 8/8 →
  **asked you**, the last event orange and pointing "answer on the right →"). Right:
  a pinned **decision panel** (the live `ask_human`: question, plain-language
  context, option buttons + free-reply) over a **DETAILS** card (branch, worktree,
  flow, changed-files magnitude). This is where "fast context" actually pays off.
- **Flow Editor.** The configurable-flows pillar. Left: list of flows (Solo,
  Plan→Build, the active Plan→Build→Critic→Gate, custom) + New flow. Main: the flow
  as a **vertical sequence of step cards** connected by rails; each step = index,
  name, prompt preview, and an **agent selector** (claude/codex/opencode, colored
  dot). The **human gate** is a distinct orange `✋ you` step, not an agent. Ends
  with a dashed **Add a step**. Steps share one worktree; gates block between them.
  Flows are a **graph, not a strict line** — a step can loop back. The TDD preset
  (`Flow — TDD with critic loop` frame) shows it: *write tests → critic ⟲ redo →
  code → run → review-gate*, where the critic step loops back to "redo tests" until
  they hold (same send-back mechanic as `ask_human`). **Premade flow templates** are
  needed — the sidebar presets (Solo, Plan→Build, Plan→Build→Critic→Gate, TDD,
  Frontend+visual-gate) double as starting templates for "New flow".
- **New Task — Composer.** The expanded compose over the board (from the topbar
  quick-input): large title, description, and three selectors — **project · flow ·
  agent**. Footer: "runs in a fresh worktree" hint + **Add to backlog** (secondary)
  / **Start now ⌘↵** (primary).
- **Failure is a card state, not a screen** (`Failure states` frame). The default
  is a `fixing itself · k/N` sub-state on the **Running** card while the agent
  auto-retries; no navigation. Only a **gave-up** escalation surfaces — as a plain-
  language `needs_human` card in the rail ("tried 3×, stuck on X — replan / guide
  me?"). The raw log is a collapsed disclosure, never a destination. (The earlier
  dedicated "Failed — log & retry" screen was **cut** for this reason.)

## Projects & board layout (settings)

Projects are **first-class**: a `Project{name, repoPath, color}` registered in
Settings. `repoPath` is the local git repo that becomes the root for that
project's task worktrees (M1). Color is auto-assigned from a palette **distinct
from the reserved status hues** (never orange/steel/moss/rust), so a project chip
never reads as a status.

Multiple projects show two ways, chosen by the user in **Settings** (preference
saved on-device):
- **Swimlanes** — a horizontal lane per project, each with its own
  Backlog·Running·Review·Done row + a lane header (swatch, name, repo path,
  summary). Cards carry no project chip (the lane names the project). Best for a
  few projects; long with many.
- **Filter + chips** — one unified board with a project switcher (All · … in a
  sub-bar) and a colored **project chip** on every card. Scales to many; `All`
  still shows everything at once.

The **attention rail stays global** across projects in both layouts. Settings also
manages projects (list + Remove + **"Choose a folder…"** button that opens the
**OS-native folder picker** on the daemon's machine — this is a local single-user
tool, so `POST /api/projects/pick` runs `osascript … choose folder`; the folder's
basename becomes the project name, no manual path typing). Selection controls (the
layout radios) use ink, not orange — orange stays reserved. Layout choice is saved
on-device (localStorage). Designed in Paper: `Projects — A · Swimlanes`,
`B · Filter + chips`, `C · Chips only`, `Settings — Layout & Projects`.

**Queued vs running (rate-limit honesty).** When the orchestrator picks a backlog
task it marks it **`queued`** (shown in Backlog as "Ready · waiting for a slot"),
and only flips it to **`running`** once it actually acquires a concurrency slot —
the board never pretends a slot-blocked task is running. The topbar meter reads
`N run · M queued`.

## Notes for the React build

- Option chips map 1:1 to `ask_human` `options[]`; the free-reply row posts a
  custom answer. Both hit `POST /api/human_requests/{id}/answer`.
- Live updates via SSE (`GET /api/events`): `task_created`, `task_updated`,
  `human_request`, `human_answered`, `event`.
- Reserve orange strictly for `needs_human` so the eye always finds where the
  human is needed — verified in the mock: attention lands on the orange card
  instantly. Concretely: the **flow stepper's gate/review segment glows orange
  ONLY when the task is actually parked at that checkpoint** (`status ===
  needs_human`); on running/queued/review/done cards a gate reads as neutral ink,
  never orange. **Failures are red/rust**, and the topbar/rail show a separate
  red "N failed" count — orange never counts anything but `needs_human`.
- If the asking agent dies while parked in `ask_human`, the daemon **cancels**
  the request (SSE `human_cancelled`) so it leaves the rail, and marks the task
  `failed` (retry from the board) — a stale checkpoint is never left answerable.
