# Plan: Tab title shows running agent count

## Goal

The browser tab currently always reads `Ultraflow` (static `<title>` in `web/index.html`).
The user wants the tab title to convey live state — specifically the number of
currently working agents — so a backgrounded tab tells you at a glance whether work
is in flight.

## Diagnosis

- `web/index.html:7` hardcodes `<title>Ultraflow</title>`; nothing ever updates
  `document.title` afterwards.
- `web/src/App.tsx:98` already computes `const running = tasks.filter((t) => IN_FLIGHT.has(t.status)).length` — this is exactly the "working agents" count the TopBar shows. `queued` (line 99) and `attention.length` (line 82) are also already in scope.
- `IN_FLIGHT` = `running | needs_human | merging` (`web/src/util.ts:65`) — the same set the board labels "running". Reusing it keeps the tab count consistent with the on-screen count.

## Implementation (single, minimal change — no new files)

1. In `web/src/App.tsx`, add one `useEffect` that keeps `document.title` in sync:
   - `running > 0` → `` `${running} running · Ultraflow` `` (e.g. `3 running · Ultraflow`) — plain text, mirroring the on-screen "N running" pill (no emoji/symbol; the human rejected the `▶` marker).
   - `running === 0` → plain `Ultraflow` (idle, matches the original).
   - Also surface things needing the human: if `attention.length > 0`, prefix a count marker, e.g. `` `(${attention.length}!) ${running} running · Ultraflow` `` — a backgrounded tab should shout when it needs an answer.
   - Depend on `[running, attention.length]` so it re-runs on every board change.
   No cleanup needed (title is global, not per-mount); the effect simply overwrites.

2. Leave `web/index.html`'s `<title>Ultraflow</title>` as the initial/SSR value — it's the correct idle title and the pre-hydration flash.

## Files changed

- `web/src/App.tsx` — add the `document.title` sync effect (~5 lines).

## Verification

- `cd web && npm run build` (prepend the nvm bin dir to PATH — node/npm aren't on PATH by default) to confirm it type-checks and bundles.
- Run the daemon on reserved `PORT=52571` against a seeded/live DB; open `http://localhost:52571` and confirm:
  - idle board → tab reads `Ultraflow`.
  - with N tasks in `running`/`needs_human`/`merging` → tab reads `N running · Ultraflow` and updates live as tasks start/finish (SSE-driven, no reload).
  - a task parked on `ask_human` bumps the attention marker.
- Capture a screenshot of the browser tab (or the board with the tab visible) into `.ultraflow/shots/` for the review screen.

## Format

`N running · Ultraflow` with a `(K!)` prefix when cards need the human. The human rejected the earlier `▶ N · Ultraflow` marker, so the title is plain text that mirrors the on-screen "N running" pill. UI copy is English elsewhere, so English here.
