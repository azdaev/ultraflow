# Plan: Dark theme

## Goal

Add a dark theme to the Ultraflow board web UI. Users get a dark palette that
mirrors the current "industrial concrete" light design, toggleable and remembered
across reloads.

## Diagnosis (how theming works today)

- The web UI is **Tailwind v4** with a `@theme` block in `web/src/index.css` that
  defines every color as a `--color-*` token (board, surface, ink, muted, hairline,
  steel, moss, rust, amber, accent, agent colors, …). Tailwind compiles utilities
  like `bg-surface` / `text-ink` / `border-hairline` to `var(--color-surface)` etc.
- **~95% of the UI already routes through these semantic tokens** (top usages:
  `text-muted` ×82, `border-hairline` ×56, `text-ink` ×54, `bg-surface` ×34,
  `bg-board` ×28…). This is the key win: **redefining the tokens under a dark scope
  re-skins almost the whole app for free**, because the utilities read the variables.
- **No theming infrastructure exists yet** — no `prefers-color-scheme`, no
  `localStorage`, no `data-theme`, no `color-scheme`. `main.tsx` just renders `<App/>`.
- Settings persist to the **backend** via `api.*`; but theme is a pure client-side
  visual preference, so **localStorage** is the right store (no backend/SSE needed).
- **Hardcoded, light-only colors** that will NOT adapt automatically (must be
  tokenized). ~18 arbitrary-hex Tailwind utilities + inline hex, in:
  - `web/src/board/Card.tsx` — `bg-[#FBFBFA]`, `border-[#E7E7E3]`, `text-[#8A8F86]`,
    `text-[#A6A6A0]`, `text-[#B0B0AA]`, `#C99180` (closed-agent mark).
  - `web/src/board/Column.tsx`, `web/src/components/AgentTerminal.tsx`,
    `CheckpointContext.tsx`, `Markdown.tsx`, `ReviewPanel.tsx`, `TaskDetail.tsx` —
    e.g. `bg-[#17171A]` (dark chips/terminals), `text-[#ECECEA]`, `text-[#B4B4AD]`,
    `text-[#6FA96C]`, `text-[#E4795F]`.
  - Note several of these (`#17171A`, `#ECECEA`) are already the *ink/board* tokens
    inlined — in dark mode ink and board roughly swap, so they must become tokens.
- A handful of Tailwind-default utilities (`text-white`, `bg-amber-500`,
  `text-amber-600`) are used on colored button fills; those read fine in dark and
  can stay, reviewed case-by-case.

## Approach (token override — the minimal, idiomatic path)

1. **Dark token set** in `web/src/index.css`. Keep the `@theme` block as the light
   defaults, then add an override scope that redefines the same `--color-*` names:
   ```css
   :root[data-theme="dark"] { --color-board:#17171a; --color-surface:#1e1e22; ... }
   @media (prefers-color-scheme: dark) {
     :root:not([data-theme="light"]) { /* same dark values */ }
   }
   ```
   Also set `color-scheme: dark` in that scope so native scrollbars/inputs match, and
   give the thin-scrollbar rule (`* { scrollbar-color }`) a dark variant.
   Palette derived from the existing industrial system (dark concrete ground, raised
   surfaces, inverted ink, and slightly-brightened steel/moss/rust/amber/accent so
   the state colors keep their meaning on a dark ground). **Safety-orange `accent`
   stays reserved for `needs_human`.**

2. **Tokenize the hardcoded hexes** listed above so they flip with the theme — map
   each to the nearest semantic token (e.g. `bg-[#17171A]` → `bg-ink` or a new
   `--color-terminal` token; `#FBFBFA/#E7E7E3` → `surface`/`hairline`;
   `#8A8F86/#A6A6A0/#B0B0AA` → `muted`/`faint`). Add 1–2 new tokens only where no
   existing one fits (e.g. terminal background).

3. **Toggle + persistence** (client-only):
   - A tiny theme module: read `localStorage.theme` (`"dark" | "light" | null`);
     null = follow system. Apply by setting `document.documentElement.dataset.theme`.
   - **Anti-flash**: inline a 3-line script in `web/index.html` `<head>` that sets
     `data-theme` from localStorage/system *before* paint (so no light flash on load).
   - A **sun/moon icon button in the TopBar** (`web/src/board/TopBar.tsx`), sitting
     with the pause / what's-new / gear controls, cycling light⇄dark and writing
     localStorage. Add a `MoonIcon`/`SunIcon` to `web/src/board/icons.tsx`.

## Files to change

- `web/src/index.css` — dark token override block, `color-scheme`, dark scrollbar.
- `web/index.html` — pre-paint anti-flash theme script.
- `web/src/board/TopBar.tsx` — theme toggle button + wire to theme module.
- `web/src/board/icons.tsx` — `SunIcon` / `MoonIcon`.
- New tiny `web/src/theme.ts` — get/set/apply theme + system-preference read.
- Tokenize hardcoded hexes in: `board/Card.tsx`, `board/Column.tsx`,
  `components/AgentTerminal.tsx`, `CheckpointContext.tsx`, `Markdown.tsx`,
  `ReviewPanel.tsx`, `TaskDetail.tsx`.

## Verification

- Build: prepend the nvm bin dir to PATH (node/npm aren't on PATH), then
  `cd web && npm run build` — must type-check and bundle clean.
- Run the daemon on reserved `PORT=52595` against a seeded/live DB, open
  `http://localhost:52595`, and check:
  - Toggle flips the whole board (columns, cards, topbar, terminal, review panel,
    settings modal, markdown) with no light-mode islands left behind.
  - Reload keeps the chosen theme; first paint shows no light flash.
  - With no stored pref, the OS dark setting is respected.
  - State colors (running steel / done moss / failed rust / needs_human orange /
    stale amber) stay legible and keep their meaning on the dark ground.
- Screenshots of light + dark board (and one detail/review screen) into
  `.ultraflow/shots/` for the review screen.

## Decision (confirmed by human)

**TopBar sun/moon toggle, default = follow system.** The human picked this over
system-only and Settings-modal placement. So Build implements the toggle exactly as
described in "Approach → 3": a sun/moon button in the TopBar controls row, choice
persisted in localStorage, and no stored pref = follow the OS `prefers-color-scheme`.
