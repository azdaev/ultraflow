# Plan: Restore the "Needs you" attention rail (ask_human)

## The bug (root cause — confirmed)

When an agent calls `ask_human`, the question **used to** show in a loud, full-width
band under the topbar (the **Attention rail**) where the human could read it and answer
inline — without opening the task. That band was **removed** in the Paper redesign
(commit `abe2f1c`, 2026-07-12), which deleted `web/src/components/AttentionRail.tsx` +
`RailCard.tsx` and replaced them with a tiny `AttentionIndicator` pill in
`web/src/board/Toolbar.tsx` that only shows a count and jumps into the card.

The backend and data flow are **fine** — `AskHuman` sets `needs_human`, publishes
`human_request`, the orchestrator refactor keeps the task parked (`turn.go` →
`turnParked`, does not fail it), and `App.tsx` already derives the full
`attention: AttentionItem[]` list (asks + failed + merge_failed). Only the visible
surface for reading/answering is gone. **This is a presentation-only restore.**

## Design source (draw-in-Paper-first ✔)

The design already exists in Paper — file **"Ultraflow — Board"**
(`https://app.paper.design/file/01KX9P6V1FN11E1WEGHH3N9J07/1-0`), artboard
**"Board v2 — Attention-first"**, node **`AttentionRail` (AS-0)**. Exact spec extracted
below (JSX pulled via Paper MCP). We implement against this rather than redrawing.

### Extracted spec (from Paper AS-0)

- **Rail band**: full width, `px-7 pt-4.5 pb-5.5`, `flex flex-col gap-3.5`,
  bg `#F7F1ED` (warm peach ground), `border-b` `#E9DED7`. Renders **only when
  there are attention items** (empty state is handled by the calm toolbar pill).
- **Header row**: 8px accent dot `#F5501E` · **"Needs you"** Onest bold 15px ink ·
  count badge (accent bg `#F5501E`, white JetBrains-Mono 11px) · spacer ·
  right meta "oldest waiting 12m · answer to unblock an agent" in `#9A8A80` 12px.
- **Cards row**: `flex gap-3.5`, each card `grow basis-0` (equal width),
  `py-3.75 px-4 rounded-[13px] gap-2.75`, white bg. Three variants:
  - **Checkpoint** (`needs_human`): border `#F3C9B6`, shadow
    `#F5501F1F 0 6px 18px, #17171A0D 0 1px 4px`. Top: `Checkpoint` badge
    (bg `#FDE7DE`, text `#C4400F`) · task title muted `#9A9A93` · timer mono
    `#C98A6E`. Then the **question** (Onest semibold 15.5px ink), a context line
    (`#6E6E68`), then **answer controls**: option chips (first = accent-filled
    primary, rest = white/`#DFDFDA` border) — this is the existing `AnswerBox`.
  - **Visual** (`needs_human` with a diff/shots): same shell; body shows diff stat
    `+180` moss `#4F7A4D` / `−24` rust `#A9432B` · "N sections changed", and a
    full-width accent CTA **"Review the full page"** (eye SVG) that opens TaskDetail.
    Heuristic: render this variant when `request.shots?.length` or `Added/Removed`
    are present; otherwise render the Checkpoint (chips) variant.
  - **Failed** (`failed` / `merge_failed`): border `#EAA99A`, rust shadow
    `#A9432B1A …`. Badge `Failed` (bg `#FADED7`, text `#BC2E1B`), error in a mono
    code box (bg `#FBF1EE`, text `#9B3620`), actions **Retry** (dark `#17171A`) +
    **View log** (opens thread). `merge_failed` → **Try merge again** + View log.

Design reference screenshot: see the "Needs you" band in the Paper mock (also embedded
in the finish report).

## Files to change

1. **`web/src/components/RailCard.tsx`** (recreate) — the three card variants above.
   Start from the git version at `abe2f1c~1:web/src/components/RailCard.tsx` (it already
   wires `AnswerBox`, `CheckpointContext`, `ContextMenu`, and the `open`/`retry`/`merge`/
   `remove` actions) and **restyle** to the Paper spec. Reuse the `AttentionItem` type
   from `useNotifications.ts` (identical shape) instead of redefining it.

2. **`web/src/components/AttentionRail.tsx`** (recreate) — the band + header + equal-width
   cards row. Base on `abe2f1c~1:web/src/components/AttentionRail.tsx`; restyle to spec;
   **render nothing when `items.length === 0`** (don't reintroduce the always-present
   empty band — the toolbar pill covers the calm state). Header meta: "oldest waiting …"
   derived from the earliest `request.createdAt`.

3. **`web/src/board/BoardPage.tsx`** — accept `attention: AttentionItem[]`, `now`, and
   action handlers; render `<AttentionRail>` **between the `Toolbar` and the `Board`**
   (full-width, matching the mock's "under topbar, above columns"). Keep the existing
   `Toolbar` (project chips + pill) — the pill stays as the always-present calm/summary
   anchor and OS-notification jump target; the rail is the read/answer surface when
   something is waiting.

4. **`web/src/App.tsx`** — pass the already-computed `attention` list + `now` down to
   `BoardPage` (currently only `attentionCount` is passed). Wire `onOpen={openTaskDetail}`;
   answering uses `api.answer`, retry `api.retry`, merge `api.merge`, remove `api.remove`
   (all already exist in `api.ts`).

5. **`web/src/index.css`** — add two tokens for the rail ground:
   `--color-attention-ground: #f7f1ed;` and `--color-attention-line: #e9ded7;`. Reuse
   existing tokens where within tolerance: card accent border ≈ `--color-accent-line`
   (`#f6c3ad`), badges ≈ `--color-accent-tint`, diff moss/rust ≈ `--color-moss`/`--color-rust`.
   Define the two card box-shadows inline (they're specific).

## Notes / decisions

- **No backend change.** `AskHuman`, the orchestrator (`turn.go`/`flow.go`), SSE, and the
  `attention` derivation in `App.tsx` are all intact — this is purely restoring the UI.
- **Keep the pill.** It gives the calm "Nothing needs you" state and stays the OS-notify
  jump target; the rail only appears when `attentionCount > 0`. (If the human prefers the
  mock exactly — no pill — that's a one-line removal, flag at review.)
- **Inline answer** is the whole point of the "separate place": the `needs_human` card
  embeds `AnswerBox` so the human answers without opening the task.

## Verification

- Build with the nvm node on PATH (`~/.nvm/versions/node/v24.13.0/bin`):
  `cd web && npm run build` (`tsc -b && vite build`).
- Run the dev server bound to `PORT=62441` (or `.ultraflow/dev.sh`). Seed a pending
  `ask_human` (per the "verify-frontend-sse" approach: seed DB / inject via `/mcp
  ask_human`) and confirm the rail appears under the toolbar with the question + working
  option chips; answering clears it. Also exercise a `failed` and a `merge_failed` card.
- Capture screenshots of the rail (checkpoint + failed states) into `.ultraflow/shots/`
  for the review screen.
