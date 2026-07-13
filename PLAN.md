# Plan: Custom dropdown component (Radix UI Select)

## Decision (confirmed with human)

Replace the native `<select>` dropdowns with a custom, accessible dropdown built on
**`@radix-ui/react-select`** (new dependency, chosen by the human over reusing the
already-present `@floating-ui/react`). Add icons: a chevron on the trigger, a check
on the selected row, and the existing `AgentMark` glyph next to each agent.

## Scope

The only native `<select>` elements in the app are the **three in the New Task
composer** — `web/src/components/Composer.tsx`: **Project · Flow · Agent** (plus a
disabled "Coming soon" `<optgroup>`). Settings and the Toolbar already use custom
controls (segmented buttons / chips), so nothing else needs changing.

## Files to change

1. **`web/package.json`** — add `@radix-ui/react-select` (install with the nvm node
   on PATH: `~/.nvm/versions/node/v24.13.0/bin` — node/npm are not on PATH otherwise).

2. **`web/src/board/icons.tsx`** — add a small stroke-based `ChevronIcon`
   (24×24, `currentColor`), matching the existing inline-SVG icon style. Reuse the
   existing `CheckIcon` for the selected-row indicator.

3. **`web/src/components/Select.tsx`** (new) — a thin, styled wrapper around the Radix
   Select primitives so callers keep a simple `value / onChange / options` API:
   - Props: `value`, `onChange`, `placeholder?`, and
     `options: { value: string; label: string; icon?: ReactNode; disabled?: boolean }[]`,
     plus optional grouped sections (for the "Coming soon" group) — model it as
     `groups?: { label?: string; options: Option[] }[]` or accept a `group?` field per
     option; pick whichever is smallest given the three call sites.
   - Composition: `Select.Root` → `Select.Trigger` (+ `Select.Value placeholder` +
     `Select.Icon` = `ChevronIcon`) → `Select.Portal` → `Select.Content`
     (`position="popper"`, `sideOffset={6}`) → `Select.Viewport` → `Select.Item`
     (`Select.ItemText` + `Select.ItemIndicator` = `CheckIcon`) → `Select.Group` +
     `Select.Label` for the "Coming soon" section.
   - Styling (match `ContextMenu.tsx` + the current select's classes, all design tokens):
     - Trigger: `w-full rounded-lg border border-hairline bg-surface px-2.5 py-2
       text-[13px] outline-none focus:border-ink/40` + right-aligned chevron, flex row.
     - Content: `z-[70]` (must sit above the `z-50` Modal; ContextMenu uses `z-[60]`),
       `rounded-xl border border-hairline bg-surface p-1
       shadow-[0_16px_44px_-16px_rgba(23,23,26,0.45)]`.
     - Item: `flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-[13px] font-medium
       text-ink outline-none data-[highlighted]:bg-board
       data-[disabled]:text-muted/50 data-[disabled]:cursor-not-allowed`, with the
       check indicator in a fixed-width leading/trailing slot.
     - Group label: reuse the muted uppercase eyebrow style
       (`text-[11px] font-semibold uppercase tracking-[0.07em] text-muted`).
   - Animation: drive off Radix `data-state`/`data-side` attributes with small CSS
     keyframes (fade + slight zoom/translate) added to **`web/src/index.css`**, keeping
     the app's "pop in" feel consistent with the ContextMenu. Respect
     `prefers-reduced-motion`.

4. **`web/src/components/Composer.tsx`** — swap the three native selects for the new
   `Select`; delete the local `Select` and `SoonGroup` helpers and the `<option>` markup.
   - **Project**: options from `projects`; `placeholder="Select a project…"` (empty when
     no projects). A project stays required — submit is already blocked on `!project`,
     and Radix keeps the value empty until a real item is chosen, so the "stranded on
     main" guard is preserved.
   - **Flow**: available `FLOWS` as items; unavailable ones in a disabled "Coming soon"
     group.
   - **Agent**: available `AGENTS` as items, each with a leading `AgentMark` icon tinted
     by `agent.color`; unavailable ones in a disabled "Coming soon" group.
   - Keep the `Field` label wrapper unchanged.

## Notes / risks

- Radix renders `Select.Content` in a portal; verified the Modal is a custom overlay at
  `z-50` (not a Radix Dialog), so a portaled `z-[70]` content layers correctly and there's
  no focus-trap conflict.
- Radix requires every `Select.Item` to have a non-empty `value` and disallows `value=""`;
  the empty/placeholder state is handled by `Select.Value`'s `placeholder`, not a
  `value=""` item — so the old disabled `<option value="">` pattern is dropped cleanly.
- Accessibility (listbox roles, keyboard nav, typeahead) comes from Radix for free.

## Verification

- `cd web && npm install` then `npm run build` (`tsc -b && vite build`) using the nvm node.
- Run the dev server bound to `PORT=52416` (or `.ultraflow/dev.sh`), open the New Task
  composer, and exercise each dropdown: open, keyboard nav, typeahead, select, placeholder,
  disabled "Coming soon" rows, and the agent icons.
- Capture screenshots of the composer with each dropdown open and save PNGs under
  `.ultraflow/shots/` for the review screen.
