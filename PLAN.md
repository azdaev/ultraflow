# PLAN — Fix broken editing hotkeys in the agent PTY terminal

Task: `cmd+delete` (and other macOS text-editing shortcuts) don't work in the
in-board agent terminal for Claude, and likely Codex too.

## Root cause

The terminal is xterm.js (`web/src/components/AgentTerminal.tsx`) bridged over a
WebSocket to a real PTY in Go (`internal/terminal/terminal.go`). Keystrokes reach
the PTY only through `term.onData` (AgentTerminal.tsx:145). xterm.js emits `onData`
for plain keys and a fixed set of control keys, but it does **not** translate
macOS **Cmd (metaKey)** or **Option (altKey)** editing combos into the byte
sequences a readline-style TUI expects — those keydowns are silently swallowed, so
`Cmd+Backspace`, `Cmd+←/→`, `Option+Backspace`, `Option+←/→` do nothing.

The existing `attachCustomKeyEventHandler` (AgentTerminal.tsx:70-82) already
special-cases Escape and Ctrl/Cmd-C copy. It's the right hook — we extend it to
translate the missing editing combos into control/escape bytes and write them to
the PTY ourselves.

## Approach (frontend-only, no backend change)

The PTY side is already correct — it writes whatever bytes it receives. The fix is
purely in `AgentTerminal.tsx`: intercept the modified editing combos in
`attachCustomKeyEventHandler`, send the equivalent terminal byte sequence via the
existing `send()` helper (AgentTerminal.tsx:27), `preventDefault()` (to stop
browser nav like Cmd+←), and return `false` so xterm doesn't also process it.

### Key → bytes mapping (readline / emacs sequences every Claude/Codex TUI reads)

| macOS shortcut        | Intent                     | Bytes sent      |
|-----------------------|----------------------------|-----------------|
| Cmd + Backspace       | delete to line start       | `\x15` (Ctrl-U) |
| Cmd + ←               | move to line start         | `\x01` (Ctrl-A) |
| Cmd + →               | move to line end           | `\x05` (Ctrl-E) |
| Option + Backspace    | delete previous word       | `\x1b\x7f`      |
| Option + ←            | move word left             | `\x1bb` (ESC b) |
| Option + →            | move word right            | `\x1bf` (ESC f) |

Notes / guardrails:
- These combos must be checked **only** when the intended modifier is set and the
  other editing modifiers aren't, e.g. Cmd combos require `metaKey && !altKey`,
  Option combos require `altKey && !metaKey`. Plain Backspace / Home / End / arrows
  keep flowing through xterm unchanged (already correct).
- Keep the existing Escape branch and the Ctrl/Cmd-C-with-selection copy branch
  **first** so copy still wins over any Cmd handling.
- On a handled combo: `e.preventDefault()`, `send(seq)`, `return false`.
- "delete" on Apple keyboards is `key === "Backspace"` (forward-delete is
  `"Delete"`); the task's "cmd + delete" == Cmd+Backspace → Ctrl-U. (Optionally
  also map Cmd+Delete forward-delete → `\x0b` Ctrl-K, but not required.)
- The mapping is agent-agnostic (raw terminal editing sequences), so it fixes
  Claude and Codex alike.

### Implementation shape

Add a small pure helper near the top of the file, e.g.:

```ts
// Translate a macOS editing combo xterm.js won't emit on its own into the
// terminal byte sequence a readline-style TUI expects. Returns null if the
// event isn't one we remap (let xterm handle it).
function macEditSeq(e: KeyboardEvent): string | null { ... }
```

and call it inside `attachCustomKeyEventHandler` after the Escape/copy branches:

```ts
const seq = macEditSeq(e);
if (seq !== null) { e.preventDefault(); send(seq); return false; }
```

Keeping it a pure `(event) -> string | null` function keeps the handler readable
and leaves the door open to a unit test, though the repo's `web/` has no test
runner today (no vitest) — don't add one just for this.

## Files to change

- `web/src/components/AgentTerminal.tsx` — add `macEditSeq` helper; extend
  `attachCustomKeyEventHandler` to use it. (~20 lines.)

No Go changes expected.

## Verification

1. Build the frontend so the daemon serves the new bundle:
   `cd web && npm run build` (node/npm via nvm — prepend the nvm bin dir; see
   memory `node-via-nvm`).
2. Run the daemon on the reserved port and open a task with a live agent terminal
   (`PORT=51705`). Focus the terminal and confirm each shortcut:
   - Type a line, `Cmd+Backspace` → whole line cleared.
   - `Cmd+←` / `Cmd+→` → caret jumps to start / end.
   - `Option+Backspace` → deletes the previous word only.
   - `Option+←` / `Option+→` → caret moves one word.
   - Regression check: plain Backspace, Enter, Ctrl-C (interrupt), and
     Cmd-C-with-selection (copy) still behave as before; Esc still interrupts
     without closing the card.
3. This is an interactive/visual change — capture a screenshot (or short GIF) of
   the terminal after a `Cmd+Backspace` line-clear into `.ultraflow/shots/` for the
   review screen.

## Risk / notes

- Purely additive to an existing handler; the only behavioral surface is the new
  combos. Low risk of regressing existing keys because we gate strictly on the
  modifier flags and fall through (`return true`) for everything else.
- If a future concern is Option-as-compose (typing special chars), note we
  deliberately remap only Option+Backspace/←/→, leaving Option+letter untouched.
