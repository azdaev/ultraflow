# PLAN — Image input in all composers (incl. clipboard paste)

Task: allow attaching pictures in every composer, in every state, including from the
clipboard. Use a lib for the drop/select UX.

## Goal & the 4 composer surfaces

Users can attach one or more images (file-picker, drag-and-drop, **and clipboard paste**)
in every place they type instructions to an agent:

1. **Composer.tsx** — the full "New task" modal (body field). New/backlog task.
2. **Column.tsx → AddTask** — the inline "+ Add task" quick draft. New/backlog task.
3. **AnswerBox.tsx** — the live `ask_human` answer (needs_human state; rendered in
   RailCard + TaskDetail).
4. **ReviseBox.tsx** — the review/failed "send it back to the agent" box.

`AgentTerminal.tsx`'s textarea (raw PTY input) and `Settings.tsx` are NOT composers — skip.

## Key insight — how images reach the agent (no prompt-plumbing changes)

The agent is Claude Code, invoked with the task text as its prompt (`buildPrompt` /
`stepHeader` embed `t.Body`; revise → `ResumeCommand` with the message; answer → delivered
to the parked agent's stdin). Claude's **Read tool renders image files by absolute path**.

So the mechanism is uniform and requires ZERO changes to prompt construction:
**save uploaded images to disk, then append their absolute paths to the outgoing text**
(body / answer / revise message). The paths ride along in the existing text fields the agent
already receives. We phrase it so the agent knows to open them, e.g.:

```
Attached image(s) — view them with your Read tool:
- /abs/.ultraflow/attachments/<id>.png
```

Storage is a daemon-owned dir independent of any worktree (new-task composers have no
worktree yet), mirroring the existing `.ultraflow/{worktrees,shots}` convention:
`.ultraflow/attachments/<random-id>.<ext>` (CWD-relative, like `wtRoot`). We do NOT copy
them into the worktree — keeping them external means they never pollute the review diff.

## Backend changes (Go)

1. **`internal/web/web.go`**
   - Add `attachDir string` param to `New(...)` (mirrors `staticDir`). Store on `server`.
   - `POST /api/uploads` — `s.uploadImages`: accept multipart form (`files`), reject
     non-images via `core.IsImageFile`, cap size (~10MB each), write each to
     `<attachDir>/<NewID()><ext>`, `MkdirAll` the dir. Return JSON
     `[{name, path, url}]` where `path` is absolute (`filepath.Abs`) for the prompt and
     `url` = `/api/uploads/<savedName>` for browser preview.
   - `GET /api/uploads/:name` — `s.serveUpload`: same filename validation as `getShot`
     (no `/`, `\`, `..`; must pass `IsImageFile`), `http.ServeFile` from `attachDir`.
   - Register both routes.
2. **`cmd/ultraflow/main.go`** — add `-attachments` flag (default `.ultraflow/attachments`),
   pass into `web.New`. (Also update the one existing `web.New` call.)
3. Reuse existing `core.IsImageFile` and `core.NewID` — no new store/service code needed.

## Frontend changes (React/TS)

Lib: **`react-dropzone`** (v14, React 19 compatible) for the click-to-select + drag-and-drop
zone. Clipboard **paste** is a native `onPaste` handler reading `e.clipboardData.files`
(dropzone doesn't cover paste) — this satisfies "including from clipboard".

1. **`web/src/api.ts`** — add:
   - `export interface Attachment { name: string; path: string; url: string }`
   - `uploadImages(files: File[]): Promise<Attachment[]>` → POST `/api/uploads` as
     `FormData` (no JSON `Content-Type`; let the browser set the multipart boundary).
2. **New `web/src/components/ImageAttach.tsx`** — shared, reused by all 4 surfaces:
   - `useImageAttach()` hook → `{ attachments, addFiles, remove, clear, pasteProps, dropzone }`,
     manages upload state + busy/error.
   - `<ImageAttachStrip>` — the dropzone affordance ("Add image / paste / drop") plus a row
     of thumbnail previews (from `attachment.url`) each with a remove ✕. Compact variant for
     the small AnswerBox/AddTask, standard for Composer/ReviseBox.
   - Helper `withAttachments(text, attachments)` → appends the "Attached image(s)…" block
     shown above (returns `text` unchanged when none).
3. Wire into each surface — hold `attachments` state, render the strip, add `onPaste` to the
   text field/container, and on submit send `withAttachments(text, attachments)`; `clear()`
   after success:
   - **Composer.tsx** — strip under the body `<textarea>`; body → `withAttachments(body, …)`.
   - **Column.tsx (AddTask)** — compact strip under the input; the quick-add `onAdd(title)`
     only passes a title, so when attachments exist, **hand off to the full Composer**
     (`expand`) which owns body — OR extend `onAdd` to carry a body. Simplest: if attachments
     present, route through Composer. Decide during build; default = compact strip + include
     paths by extending the quick-create to accept a body. (Confirm with human if it balloons.)
   - **AnswerBox.tsx** — compact strip above the free-reply row; `send(withAttachments(free,…))`.
     Option chips stay plain (a chip is a canned answer; attachments only make sense with the
     free reply).
   - **ReviseBox.tsx** — strip under the textarea; `revise(withAttachments(msg, …))`.

## Install / build notes

- Node isn't on PATH — prepend the nvm bin dir before `npm`/`vite` (see memory "Node via nvm").
- `cd web && npm i react-dropzone`.

## Verification

- **Go**: `go build ./...` + a `web_test.go` case for `uploadImages` (post a tiny PNG, assert
  200 + file written + abs path in response) and rejection of a non-image.
- **Frontend/e2e** (see memory "Verify frontend SSE"): build web, run daemon on `$PORT`
  (49976) with `-max-concurrent 0`, headless Chrome:
  1. Open Composer, drop/select a PNG → thumbnail appears; submit → GET the new task,
     assert its body contains the `/…/.ultraflow/attachments/…png` path and the file exists.
  2. Simulate a clipboard **paste** (dispatch a paste event carrying a File) into the body →
     thumbnail appears. Confirms the paste path.
  3. Spot-check AnswerBox + ReviseBox render the strip (seed a needs_human + a review task).
- **Screenshots** → save PNGs under `.ultraflow/shots/` for each composer showing the attach
  strip + a thumbnail (board review screen shows them).

## Open question (raise via ask_human only if it grows)

AddTask quick-create currently carries only a title. Cleanest UX is a compact strip inline;
if threading a body through the quick-create path proves invasive, fall back to auto-expanding
into the Composer when an image is attached. Not blocking — resolve in build step.
