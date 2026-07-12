# Ultraflow — Product UX Audit

A product-level pass over the board's UX, focused on friction ("неудобности") a
real single-user operator hits during the daily loop: add tasks → let agents run
→ answer checkpoints → review → merge. Audited against `main`
(`da6e054`) — `web/src` and the daemon routes in `internal/web/web.go`.

The build quality is high, and several sharp gaps have already been closed on
`main` — this audit credits those first, then ranks what still bites.

## Already fixed on `main` (don't re-flag)

- **Review is no longer blind.** `ReviewPanel.tsx` now shows the agent's
  screenshots plus a magnitude-first diff (`+N −N · files`, raw patch as a
  collapsible disclosure) — exactly the "fast context" the spec calls for.
- **Review isn't a merge-or-nothing dead-end.** `ReviseBox.tsx` lets you send a
  task back to the agent in plain language; it reworks in the same worktree and
  returns to review.
- **Right-click actions** (`ContextMenu.tsx`) mirror a card's controls
  (merge / mark done / retry / copy id / copy worktree) without aiming at tiny
  inline buttons.
- **Inline "＋ Add task" now has a "More…" hand-off** that carries the typed
  draft into the full composer, and the column's project seeds the composer.

## Remaining friction, ranked by (how often it bites × how core it is)

---

### P0 — the core promise still has one hole

**1. A blocked agent waits *silently* — still no notification.**
Confirmed absent: no `document.title` badge, no `Notification` API, no favicon
change, no sound anywhere in `web/src`. The only signal that an agent is parked
on `ask_human` is the orange rail — visible **only if you're already staring at
the tab**.

This is now the single biggest gap in the value prop. Ultraflow's whole pitch is
"run agents in parallel, walk away, we'll tell you which one needs you." A
parallel runner *lives in a background tab* by definition. Today a checkpoint can
sit unanswered for an hour — holding that agent's concurrency slot and, via the
shared subscription limit, starving the others — while you have no idea.

*Fix (cheap, client-side, off existing SSE):* set `document.title` to
`(N) Ultraflow` when `needCount > 0`; fire a `Notification` (with opt-in) on each
new `human_request`; optional soft chime. Everything needed is already on the
SSE stream `useBoard` consumes.

---

### P1 — everyday capabilities that are missing

**2. You still cannot cancel, stop, or delete a task.**
Task routes are get / events / diff / revise / shots / retry / merge / done /
terminal — **no cancel, no delete, no stop.** The context menu doesn't offer them
either. `StatusCancelled` exists in `internal/model/model.go` but is **assigned
nowhere** — a dead enum; no task can ever reach it.

Consequences, all daily:
- A typo'd or mis-scoped task can't be removed — it *will* get picked up and burn
  a subscription slot.
- A running agent has no Stop; the only interrupt is typing Ctrl-C into the
  terminal, discoverable solely via the tiny "Ctrl-C to interrupt" caption.
- The **Done column grows without bound** — no archive / clear, so the board only
  gets noisier over a week.

*Fix:* `DELETE /api/tasks/{id}` (remove a not-started task; tear down its worktree
if any) and `POST /api/tasks/{id}/cancel` (kill the process group — that
machinery already exists, commit `dbd8713` — and move to `cancelled`). Add
"Remove" / "Stop" to the context menu keyed on status, and an "Archive done"
affordance on the Done column header. Then render `cancelled` somewhere (right
now it would appear in no column and no rail — an invisible state).

**3. A merged / done task's work vanishes from the UI.**
`ReviewPanel` is gated on `(review | failed) && worktree` (`TaskDetail.tsx`).
Once you merge, the worktree is torn down, so opening a `done` task shows only
"No live session" — no diff, no screenshots, no transcript. Same blank for a
`failed` task that has no worktree. The terminal is a live PTY with no persisted
scrollback, so *after the fact you can't see what any finished task did* — to
audit it, learn from it, or debug a bad merge.

*Fix:* persist the captured diff/summary (and ideally a transcript tail) at
finish time and render it read-only for `done`/`failed`, so the detail modal
stays useful post-hoc.

**4. Review magnitude lives only inside the modal; card-level merge is blind.**
The diff-stat (`+N −N`) is in `ReviewPanel`, but the **review card on the board**
(and the right-click "Merge → done") shows none of it — you can land a branch on
`main` from the card without ever seeing what changed. And the context-menu merge
does `api.merge(...).catch(() => {})` — a swallowed error, so a failure only
resurfaces indirectly via the attention rail's merge_failed card.

*Fix:* put the `+N −N · files` glance on the review card itself; surface
context-menu action failures (toast or inline) instead of swallowing them.

---

### P2 — friction & rough edges

**5. A task is immutable before it starts.** Send-back covers *review/failed*
rework, but a task sitting in backlog can't have its title/body/agent/flow
edited — you'd delete and recreate (and #2 says you can't delete). Make the
backlog card editable inline until it starts.

**6. Project onboarding is buried; first-run funnels into a shared workdir.**
Projects live only inside Settings (unlabeled gear). The empty-board CTA
(`App.tsx`) is "Add your first task" → Composer, where Project defaults to **"No
project"**, which runs in the daemon's own directory ("shared workdir (M0)") —
*not* an isolated worktree, silently dropping the core guarantee. If no projects
exist, the empty state's primary CTA should be "Add a project", and the
Composer's Project select should offer an inline "＋ Add project…".

**7. The ⌘N / topbar composer forgets your last project.** It resets `project` to
`""` every open. (The inline path now seeds the column's project — good — but the
blank-open path still doesn't.) Default to the last-used project.

**8. Inverted status vocabulary.** `TaskCard.tsx` labels a `backlog` task
**"Queued"** and a `queued` task **"Ready · waiting for a slot"** — the two words
are swapped relative to intuition. Pick one axis and label consistently.

**9. Checkpoint options aren't keyboard-answerable.** `AnswerBox.tsx` renders
option chips but no hotkeys. For a "answer with one tap" tool, `1/2/3` (+ Enter →
primary) would make triaging several checkpoints genuinely fast.

**10. Smaller items.** The attention rail is a fixed-380px `overflow-x-auto` row
— extra checkpoints scroll off-screen with no "＋N more" on the one surface that
promises you won't miss one (`AttentionRail.tsx`). "Remove project"
(`Settings.tsx`) deletes on one click with no confirm and no stated blast radius.

---

## Suggested sequencing

1. **#1 notifications** — cheapest fix, closes the last hole in the core loop.
2. **#2 cancel / delete / stop / archive** — the biggest missing everyday
   capability; two small daemon routes over machinery that already exists.
3. **#3 persist finished work** + **#4 card-level magnitude** — round out the now
   much-improved review flow.
4. Then the P2 polish (#5–#10).

#1, #4, #7, #8, #9, #10 are frontend-only or nearly so; #2, #3, #5, #6 touch the
daemon. None are large.
