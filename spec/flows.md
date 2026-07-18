# Flow engine (M2)

A **flow** is a GRAPH of steps a task walks through. Where the M0 "solo" flow is
one agent run, a multi-step flow chains several agent turns — plan, build, critic,
a human gate — that all **share one worktree**. Flows are a graph, not a line:
successors are a set and a gate routes by the human's answer, so a flow can loop
(TDD critic → redo).

Code: `internal/flow` (the graph model + presets + YAML), `internal/orchestrator`
(the runner that walks it), `internal/store` + `internal/core` (run persistence),
`web/src/components/FlowStepper.tsx` (the card's live stepper).

## Model

A `Step` is `{id, role, agent, prompt, gate, next[], routes[]}`:

- **work step** (`gate:false`) — runs `agent` (or the task's agent) in the shared
  worktree, seeded with `prompt`; when the agent ends its turn it advances along
  `next[0]`. An empty `next` is terminal → the task goes to review.
- **gate step** (`gate:true`) — runs no agent. It parks the task as `needs_human`
  via the ordinary `ask_human` mechanism. The human's answer is routed through
  `routes[]`. Exact option matches win. A route whose answer is `""` is an explicit
  fallback for every non-exact, free-form reply; when it is absent, substring
  matching and then the first route remain the defaults for compatibility. A route
  whose next step is `""` finishes the flow (→ review). The built-in Gate therefore
  routes the exact `Approve` action to review and both `Request changes` and any
  typed feedback back to Build. When the preceding step left a `result` event,
  `openGate` carries the latest result into the checkpoint and asks a task-specific
  approval question. The decision surface states that approval moves to final
  Review without merging, while changes/feedback return to Build. Legacy or custom
  gates with no result keep their configured prompt and generic flow context.

`Flow = {key, label, start, steps[]}`. Presets ship in code and double as
templates; a project can override or add flows in `.ultraflow/flows.yaml` (parsed
by `flow.ParseYAML`, layered by `flow.Load`). Only **wired** flows (`flow.Wired`)
are selectable — task creation normalizes anything else to solo, and the composer
shows the rest as "· soon" (presentation honesty).

Shipped: `solo`, `plan-build`, `plan-build-critic-gate`.

## Runner (orchestrator)

`start()` creates the worktree **once**, then branches: a single-step flow keeps
the unchanged solo path (so the default can't regress); a multi-step flow enters
`runFlow`, which walks the graph from the persisted cursor:

1. **enter step** → persist the cursor (publishes live progress over SSE).
2. **gate** → `openGate` posts the checkpoint and the goroutine returns (freeing
   its concurrency slot). The answer re-enters via `AnswerHuman → Reengage →
   resumeGate`, routed by the answer (approve → finish; reject or free-form
   feedback → loop back, seeding the feedback into the rebuild).
3. **work step** → `runStep` runs one agent turn. Only `finish_task` with a
   non-empty report advances the graph. An idle or clean exit without that handoff
   fails the task; a crash self-heals in place up to the budget, then escalates as
   a `needs_human` item (resumed by `resumeStep`, which keeps walking the flow
   rather than returning to review).

`finish_task` is flow-aware via `core.CompleteTurn`: its report creates the durable
handoff; a solo task goes straight to review, while a mid-flow step only marks its
turn done (the card never flashes to review between steps).

The built-in Critic's `finish_task` report is also the human-facing Gate brief. It
must say whether the problem was confirmed, explain the root cause, describe the
work performed, and list verification plus caveats in plain product language. The
Gate review opens this result first; screenshots and the collapsed raw diff remain
available as secondary technical evidence.

## Persistence & resume

One `runs` row per multi-step task: `flow, cursor, completed[], phase, turn_done`.
The explicit persisted phase is `pending`, `active`, `waiting`, or `complete`.
Recovery cold-starts a pending step, but resumes an interrupted active step's
session with a compact continuation prompt. Gates and escalations remain waiting;
only an active step can accept `finish_task`, so a late call from an old agent
cannot advance a newer cursor. The cursor is persisted every step, so
`RecoverInFlight` resumes at that step — the run row is kept, not cleared. The
board reads `RunProgress` (index, total, sub-agent, gate, caption)
from the cursor + graph; the card's stepper lights the live step and captions it
("Build · step 2 of 4 · critic + your gate next").

## Failure self-heal

Per spec.md "Failure self-heals": a step error auto-diagnoses and retries up to N
(per-task budget) while the card stays `running` with a `fixing itself · k/N`
sub-state, before escalating to the human. Same policy as solo, applied per step.
