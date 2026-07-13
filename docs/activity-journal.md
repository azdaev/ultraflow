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

- `internal/orchestrator/turn.go` owns both intentional idle endings. A flow step
  marks its turn done and appends "step ended its turn — advancing the flow"; a
  solo task goes to review. Both call `sess.Close()` → SIGKILL the PTY after the
  shared 90-second idle timeout. This is by design.

The turn runner classifies the durable state after the process exits and journals
an explicit `outcome` (`completed`, `crashed`, `stopped`, `parked`, and so on).
This separates an expected finish/idle close from a genuine crash even when both
surface from the operating system as `signal: killed`.

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
- `agent` — `start` and `exit`. `exit` carries the classified `outcome`,
  `human_stop` (true = SIGINT/SIGTERM, a deliberate stop), and `err`. The nearby
  `bus` event still gives the detailed reason ("advancing the flow" / "went idle"
  / an error line).
- `ui` — `click` (with a `label`) for every actionable click, plus `open_task`.
- `journal` — bookkeeping (`opened`).

## How to analyse (after a couple of days of real use)

```bash
J=~/.ultraflow/journal.jsonl

# Every agent exit and how it was classified. outcome="crashed" is suspicious;
# completed/stopped/parked are deliberate orchestration outcomes.
jq -c 'select(.cat=="agent" and .event=="exit")' "$J"

# Full timeline for one task (interleaves ui + bus + agent, chronological).
jq -c 'select(.task=="<id>" or .data.taskId=="<id>")' "$J"

# What the user clicked.
jq -c 'select(.cat=="ui")' "$J"

# Status transitions only, most useful for "task bounced back to review".
jq -c 'select(.event=="task_updated") | {ts, task:.data.taskId, status:.data.status}' "$J"

# Count exits by orchestration outcome.
jq -rc 'select(.cat=="agent" and .event=="exit") | .outcome' "$J" | sort | uniq -c
```

The tell we're looking for is an `agent exit` classified as `crashed`; inspect the
immediately preceding `bus` events for that task to identify the trigger.

## Next step

Use it for a couple of days, then run the jq recipes above (or hand the file over)
to see what's really knocking finished tasks back to review.
