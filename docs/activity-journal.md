# Activity journal — diagnosing the "signal: killed" churn

Notes to self. Written 2026-07-12 while chasing why finished tasks bounce back to
`review` and agents die mid-work with `agent exited before finishing: signal:
killed`.

## What we ruled out

- **Not the macOS OOM killer / jetsam.** `log show --last 12h --predicate
  'eventMessage CONTAINS "memorystatus" OR eventMessage CONTAINS "jetsam"'`
  returned **zero** kill events across the whole window of kills. Swap was heavily
  used (~4 GB of 5 GB) so the box *had* been under memory pressure, but nothing
  was jetsam-killed. RAM is 24 GB.
- **Not daemon restarts.** Only ~3 daemon restarts in the window vs dozens of
  kills; the kills are mostly spread out, one task at a time.
- **Not the idle-watcher misfiring** in the obvious way — but see below, an idle
  close *is* one of the legitimate SIGKILL paths.

## What's actually happening

`signal: killed` is the **daemon SIGKILLing its own agent PTY**, and in the common
case that is **normal**:

- `internal/orchestrator/flow.go` — when a flow step (Plan→Build→Critic→Gate) ends
  its turn, the daemon marks the turn done, appends "step ended its turn —
  advancing the flow", and `sess.Close()` → SIGKILLs the PTY to move to the next
  step. This is by design.
- `internal/orchestrator/orchestrator.go` `watchIdle` — a bare turn-end (agent idle
  at its prompt for `idleTimeout = 90s` without `finish_task`) sends the task to
  review and `sess.Close()` (SIGKILL) to free the slot.

Both surface at `orchestrator.go:~450` as `agent exited before finishing: signal:
killed` — **the same line a genuine crash would log.** So the existing log cannot
tell a normal step/idle close from a real premature kill. That ambiguity is the
whole reason a real problem (if there is one) has been impossible to see.

Suspicious signal still worth confirming with data: the redesign task 0292 was
killed every ~1–2 min (21:38, 21:39, 21:40, 21:42) — *faster* than the 90s idle
timeout, which a plain step/idle close shouldn't produce. The journal below is how
we tell.

## The journal (shipped 2026-07-12)

Verbose append-only JSONL, **on by default**, written next to the DB:
`~/.ultraflow/journal.jsonl`. Disable with `-journal off` in the daemon plist.
Code: `internal/journal/` + a `service.publish()` tap + agent start/exit logging +
`POST /api/journal` fed by `web/src/journal.ts`.

Categories:

- `bus` — every board fan-out: `task_updated` (status moves, port reservations),
  `event` (all task events), `context`, `runs`. taskId is under `.data.taskId`.
- `agent` — `start` and `exit`. `exit` carries `human_stop` (true = SIGINT/SIGTERM,
  a deliberate stop) and `err`; the reason for a kill is the `bus` event logged
  just before it ("advancing the flow" / "went idle" / an error line).
- `ui` — `click` (with a `label`) for every actionable click, plus `open_task`.
- `journal` — bookkeeping (`opened`).

## How to analyse (after a couple of days of real use)

```bash
J=~/.ultraflow/journal.jsonl

# Every agent exit and how it died. human_stop=false AND no "advancing the flow"
# / idle event just before it (see the task timeline) = a suspicious kill.
jq -c 'select(.cat=="agent" and .event=="exit")' "$J"

# Full timeline for one task (interleaves ui + bus + agent, chronological).
jq -c 'select(.task=="<id>" or .data.taskId=="<id>")' "$J"

# What the user clicked.
jq -c 'select(.cat=="ui")' "$J"

# Status transitions only, most useful for "task bounced back to review".
jq -c 'select(.event=="task_updated") | {ts, task:.data.taskId, status:.data.status}' "$J"

# Count exits vs how many were clean step/idle closes vs real errors.
jq -rc 'select(.cat=="agent" and .event=="exit") | .human_stop' "$J" | sort | uniq -c
```

The tell we're looking for: an `agent exit` whose immediately-preceding `bus`
events for that task are **not** "advancing the flow" and **not** "went idle" —
that's the daemon killing a genuinely-working agent, and whatever event precedes it
is the trigger to fix.

## Next step

Use it for a couple of days, then run the jq recipes above (or hand the file over)
to see what's really knocking finished tasks back to review.
