# Plan — Codex tasks show the wrong (Claude) logo

## Problem
Every task card renders the same brand glyph — the Claude four-line asterisk —
and only tints it by the agent's colour. So a Codex task shows the **Claude
shape** in Codex green (`#10a37f`), which reads as "the wrong logo". The backend
already dispatches Codex correctly; the bug is purely in the frontend glyph.

## Root cause
- `web/src/board/icons.tsx:13-22` — `AgentMark({ size, color })` hardcodes the
  Claude asterisk. It ignores which agent it's for; the comment even says so
  ("the Claude wordmark glyph, reused for every agent").
- `web/src/board/Card.tsx:276` — the only call site, inside `AgentFooter`,
  passes `color={agentColor(agent)}` but never tells `AgentMark` which agent it
  is, so it can't pick a shape.
- No Codex/OpenAI logo asset exists anywhere in `web/`.

The `agent` string (`"claude"` / `"codex"`) is already in hand at the call site
(`Card.tsx:270`, from `run?.agent ?? task.agent`) and drives colour+label via
`AGENTS` in `web/src/util.ts:128-140`. We just need it to drive the glyph too.

## Approach (minimal — one glyph added, one prop threaded)
1. **`web/src/board/icons.tsx`** — teach `AgentMark` to select a glyph by agent:
   - Add an `agent?: string` prop.
   - Keep the existing Claude asterisk as the `"claude"` glyph and as the default
     for unknown agents (default task agent is claude).
   - Add a Codex glyph = the **OpenAI logomark** (the monochrome "blossom" knot),
     as a single `<path fill={color}>` so the existing colour-tint (`agentColor`,
     and the muted `#C99180` on closed cards) keeps working unchanged. Build step
     drops in the complete official single-path OpenAI mark on a 24×24 viewBox,
     rendered with `fill={color}` and no stroke.
   - Switch shape on the `agent` key (small `switch`/map). A local switch in
     `icons.tsx` keeps the change minimal; only fold glyph choice into the
     `AGENTS` registry (`util.ts`) if it stays clean.
2. **`web/src/board/Card.tsx:276`** — pass `agent={agent}` to `AgentMark`
   alongside the existing `size`/`color`. No other call sites exist.

Colour/label already work per-agent, so no change to `util.ts` colours or
`index.css` is required.

## Files to change
- `web/src/board/icons.tsx` — add Codex/OpenAI glyph + agent-based dispatch in `AgentMark`.
- `web/src/board/Card.tsx` — thread `agent` into the `AgentMark` call at line 276.

## Verification
- Build the frontend to typecheck (node via nvm — prepend the nvm bin dir):
  `cd web && npm run build`.
- Run the dev server on `$PORT` (56226); with both a Claude task and a Codex task
  on the board, confirm the Codex card shows the OpenAI blossom mark (green), the
  Claude card shows the asterisk, and a closed Codex card fades correctly.
- Capture before/after screenshots of the board cards into `.ultraflow/shots/`
  for the review screen.

## Notes / open question
- Exact Codex glyph is a visual call. Default assumption: the official OpenAI
  logomark, tinted with `--color-codex`. If the human prefers a simpler custom
  mark, it's a one-path swap — flag on review rather than block.
