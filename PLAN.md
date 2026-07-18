# Plan: quick feedback button

Reference implementation studied: `/Users/amady/Code/twelves/frontend/src/components/FeedbackButton.jsx`
+ `FeedbackModal.jsx` + `api/routes/feedback.py`. Same shape here, trimmed for a
local single-user daemon (no auth, no per-IP rate limit, no email fallback —
text lands straight in Ultraflow's own SQLite DB, readable later with
`sqlite3 ultraflow.db "select * from feedback"`).

## Backend (Go)

1. **`internal/store/store.go`** — migration 11:
   ```go
   const feedbackSchema = `
   CREATE TABLE IF NOT EXISTS feedback (
     id         INTEGER PRIMARY KEY AUTOINCREMENT,
     message    TEXT NOT NULL,
     path       TEXT NOT NULL DEFAULT '',
     created_at TIMESTAMP NOT NULL
   );
   `
   ```
   appended to the `migrations` slice. Add `AddFeedback(message, path string) error`
   on `*Store` — a single `INSERT`, mirroring `SetSetting`'s shape.

2. **`internal/core/service.go`** — `AddFeedback(message, path string) error`:
   trims `message`, rejects empty (400 upstream), caps length defensively
   (~4000 runes, matching twelves' `MAX`), delegates to `s.store.AddFeedback`.

3. **`internal/web/interfaces.go`** — add `AddFeedback(message, path string) error`
   to the `Service` interface.

4. **`internal/web/web.go`** — `r.POST("/api/feedback", s.feedbackHandler)`
   registered next to the other POST routes. Handler decodes `{text, path}`,
   400s on empty/too-long text, calls `svc.AddFeedback`, returns `writeOK`.

No test-fake update needed — `internal/web/web_test.go` exercises the real
`core.Service`, so it picks up the new method for free. Add one small
`web_test.go` case (POST /api/feedback → 200, empty text → 400) and one
`store_test.go` / `service_test.go` case for the round trip, following the
existing test style in those files.

## Frontend (React)

5. **`web/src/api.ts`** — `sendFeedback: (text: string, path?: string) =>
   fetch("/api/feedback", {...}).then(r => json<{status:string}>(r))`,
   alongside the other `api.*` entries.

6. **`web/src/board/icons.tsx`** — add a small `MessageIcon` (message-square,
   stroke-based, matching the existing icon set's style/viewBox convention —
   no new dependency, unlike twelves' `lucide-react`).

7. **`web/src/components/FeedbackButton.tsx`** (new) — a small fixed pill,
   Tailwind-styled to match the existing surface/hairline/muted tokens used
   elsewhere (see `TopBar.tsx`), anchored bottom-right, `z-40` (above the
   board, below modals). Click opens local state → renders the modal inline
   (own component, not a separate file — Ultraflow's other one-field flows
   like `ReviseBox` keep textarea + submit together, and there's no separate
   "modals/" folder here to mirror twelves' split).

   Modal body: reuses the shared `Modal` component (`title="Leave feedback"`,
   no footer prop — buttons go in `children` like `Settings.tsx` does). One
   textarea ("What's working, what's annoying, what's missing?" / "Send"),
   matching Ultraflow's existing English-first copy (see `TopBar`, `ReviseBox`).
   ⌘/Ctrl+Enter submits, matching `ReviseBox`. On success: swap to a short
   thank-you and auto-close after ~1.2s (setTimeout + cleanup on unmount,
   matching the reference). On error: inline message, button re-enabled.

8. **`web/src/App.tsx`** — mount `<FeedbackButton />` once at the top level,
   alongside `<Settings>`/`<Changelog>` (always present, not gated by any
   prop) so it's visible on every screen.

## Verification

- `go build ./...` and `go test ./internal/store/... ./internal/core/... ./internal/web/...`.
- `cd web && npm run build` (typecheck) — see `Node via nvm` memory, prepend
  nvm bin dir.
- Start the dev server via `start_dev_server`, click the button, submit
  feedback, confirm it lands in the DB (`sqlite3 <db> "select * from feedback"`)
  and the button/modal look right in both light and dark theme (screenshot
  both for the review step, since this is a visual change).
