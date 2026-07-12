# Plan — use the real Ultraflow logo

## Task
Attachment `23cb81f1e80684b6.png` is the Ultraflow logo: a solid‑black "rewind/flow"
glyph — a small rounded left‑pointing triangle beside a larger rounded left‑pointing
trapezoid/triangle. Replace the current **placeholder** brand mark with this logo and
use it as the favicon.

## Current state (what to replace)
- `web/src/board/TopBar.tsx:23-25` — brand mark is a placeholder: an `bg-accent`
  (safety‑orange) rounded square containing a small white square. Sits left of the
  "Ultraflow" wordmark; the whole button opens the changelog.
- `web/index.html` — has `<title>Ultraflow</title>` but **no favicon** (browser shows
  a blank/default icon).
- Icons live in `web/src/board/icons.tsx` as inline SVGs on a 24×24 viewBox, tinted via
  `currentColor` — the established pattern to follow.

## Approach (smallest change that solves it)
1. **Add a `LogoIcon`** to `web/src/board/icons.tsx`, matching the file's inline‑SVG
   convention (`{ size?, className? }`, `fill="currentColor"`, `aria-hidden`). Trace the
   two rounded shapes from the PNG as `<path>` elements on a viewBox sized to the glyph.
   Default `fill="currentColor"` so it inherits `text-ink`.
2. **Swap the placeholder** in `TopBar.tsx`: replace the `bg-accent` square + inner white
   square (lines 23‑25) with `<LogoIcon />` rendered in `text-ink` (the logo is black;
   this also honors the design rule that `--color-accent` orange is reserved for
   `needs_human` only). Keep it inside the existing changelog button, keep `gap-2.25` and
   the "Ultraflow" wordmark, size the mark to ~size‑6 (24px) to match today's footprint.
3. **Favicon**: add an SVG favicon so the tab shows the logo. Simplest self‑contained
   route: put `web/public/favicon.svg` (same paths as `LogoIcon`, `fill="#17171a"`) and
   add `<link rel="icon" type="image/svg+xml" href="/favicon.svg" />` to
   `web/index.html`. Vite serves `web/public/` at the web root, and `web/embed.go`
   embeds the built `dist/` for the Go binary, so the favicon ships automatically.

## Files to change
- `web/src/board/icons.tsx` — add `LogoIcon`.
- `web/src/board/TopBar.tsx` — use `LogoIcon`, drop the placeholder square.
- `web/index.html` — add favicon `<link>`.
- `web/public/favicon.svg` — new asset (traced logo).

## Verification
- `cd web && npm run build` (Node via nvm — prepend the nvm bin dir) — typecheck + build clean.
- Run the dev server bound to `$PORT` (54669) and load `http://localhost:54669`:
  confirm the TopBar shows the black rewind glyph left of "Ultraflow", and the browser
  tab shows the logo favicon. Verify it reads correctly at 24px.
- Save a screenshot of the TopBar to `.ultraflow/shots/` for the review screen.

## Notes / open question
- Rendering the mark in **ink (near‑black)** matches the provided logo and frees the
  reserved orange. If the human wants the accent color instead, that's a one‑line tweak
  (`text-accent`) — flag on review if unsure.
