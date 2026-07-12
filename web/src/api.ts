// Typed client for the Ultraflow board API (see internal/web/web.go).

export type TaskStatus =
  | "backlog"
  | "queued"
  | "planning"
  | "running"
  | "needs_human"
  | "review"
  | "merging"
  | "done"
  | "failed"
  | "cancelled";

export interface Project {
  id: string;
  name: string;
  repoPath: string;
  color: string;
  createdAt: string;
}

export interface Task {
  id: string;
  title: string;
  body: string;
  project: string;
  agent: string;
  flow: string;
  status: TaskStatus;
  worktree: string;
  // Self-heal sub-state: attempt is how many auto-retries the agent has spent on an
  // error (0 = the original run; k>0 renders "fixing itself · k/N" on the running
  // card), maxAttempts is the retry budget before it escalates to the human.
  attempt: number;
  maxAttempts: number;
  port: number; // dev-server port reserved for this task (0 = none)
  createdAt: string;
  updatedAt: string;
}

export interface HumanRequest {
  id: string;
  taskId: string;
  question: string;
  options: string[] | null;
  context: string;
  answer: string;
  status: string;
  // Fast context the daemon captured server-side at ask_human time: the
  // worktree's change magnitude (+added −removed across `files`) and the
  // screenshots the agent saved. The decision surfaces lead with these.
  added: number;
  removed: number;
  files: DiffFile[] | null;
  shots: string[] | null;
  createdAt: string;
  answeredAt?: string;
}

export interface TaskEvent {
  id: number;
  taskId: string;
  kind: string;
  data: string;
  createdAt: string;
}

// RunProgress is a multi-step task's LIVE position in its flow, derived
// server-side from the run cursor plus the flow graph. The card's stepper uses
// `index` to light the active step and `caption` for the one-line "what's now /
// what's next" summary. Solo tasks have no run, so they're absent from the map.
export interface RunProgress {
  flow: string;
  step: string; // current step id ("" when the flow is complete)
  index: number; // 0-based position in the flow's step order (-1 = none)
  total: number;
  agent: string; // the current step's sub-agent
  gate: boolean; // the current step is a human gate
  caption: string; // e.g. "Build · step 2 of 4 · critic + your gate next"
}

export interface BoardSnapshot {
  tasks: Task[];
  requests: HumanRequest[];
  activity: Record<string, string>;
  // kind of each task's latest activity line, parallel to `activity`. Lets the
  // board lift a "merge_failed" event into the attention rail.
  activityKind: Record<string, string>;
  projects: Project[];
  // live flow progress per multi-step task, keyed by task id.
  runs: Record<string, RunProgress>;
  // latest context size (tokens) per running/review task, for the card's context
  // meter. Live updates arrive as "context" events; absent until the first poll.
  context: Record<string, number>;
  // latest model name (e.g. "claude-opus-4-8") per task, for the card's agent
  // footer. Live updates arrive as "model" events; absent until the first poll.
  models: Record<string, string>;
}

export interface Settings {
  maxConcurrent: number;
  maxConcurrentMin: number;
  maxConcurrentMax: number;
  // per-agent context budget in tokens (0 = off). When a running agent's context
  // crosses this, Ultraflow injects /compact so it summarizes and continues.
  contextCap: number;
  contextCapMin: number;
  contextCapMax: number;
  // true where the daemon can open a native folder dialog (macOS). Off it, the
  // board shows a paste-the-path field instead (see addProject).
  nativePicker: boolean;
  telegram: TelegramSettings;
}

export interface TelegramSettings {
  enabled: boolean;
  hasToken: boolean;
  userId: number;
  chatId: number;
}

export interface DiffFile {
  path: string;
  added: number;
  removed: number;
}

// TaskDiff is a reviewed task's change set: the magnitude the board leads with
// plus the raw unified patch (secondary). truncated when the patch was capped.
export interface TaskDiff {
  base: string;
  added: number;
  removed: number;
  files: DiffFile[];
  patch: string;
  truncated: boolean;
}

// Attachment is one image uploaded from a composer (see api.uploadImages). `path`
// is the absolute on-disk path the agent's Read tool opens (appended to the
// outgoing text by withAttachments); `url` is a board-relative link for preview.
export interface Attachment {
  name: string;
  path: string;
  url: string;
}

// uploadName returns a filename with an image extension for an upload. Picked and
// dropped files already have a real name; a clipboard-pasted image is frequently a
// nameless blob, so we synthesize one from its MIME type — the server validates by
// extension and would otherwise reject it.
const MIME_EXT: Record<string, string> = {
  "image/png": ".png",
  "image/jpeg": ".jpg",
  "image/gif": ".gif",
  "image/webp": ".webp",
};
function uploadName(f: File): string {
  if (f.name && /\.(png|jpe?g|gif|webp)$/i.test(f.name)) return f.name;
  return `pasted-image${MIME_EXT[f.type] ?? ".png"}`;
}

async function json<T>(res: Response): Promise<T> {
  if (!res.ok) throw new Error((await res.text()) || res.statusText);
  return res.json() as Promise<T>;
}

// errMsg unwraps a caught value into a display string, falling back to `fallback`
// when it isn't an Error. Centralizes the `e instanceof Error ? e.message : "…"`
// idiom every action component's catch block would otherwise repeat.
export function errMsg(e: unknown, fallback = "something went wrong"): string {
  return e instanceof Error ? e.message : fallback;
}

export const api = {
  board: () => fetch("/api/board").then((r) => json<BoardSnapshot>(r)),

  createTask: (body: {
    title: string;
    body: string;
    project: string;
    agent: string;
    flow: string;
  }) =>
    fetch("/api/tasks", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }).then((r) => json<Task>(r)),

  // uploadImages saves images picked/dropped/pasted in a composer and returns one
  // Attachment per file. Sent as multipart FormData — we deliberately don't set a
  // Content-Type so the browser adds the multipart boundary itself.
  uploadImages: (files: File[]): Promise<Attachment[]> => {
    const fd = new FormData();
    // A pasted-from-clipboard image is often a nameless/extensionless blob, but the
    // server keys "is this an image?" off the filename extension — so give each file
    // a name with an extension derived from its MIME type when it lacks one.
    for (const f of files) fd.append("files", f, uploadName(f));
    return fetch("/api/uploads", { method: "POST", body: fd }).then((r) =>
      json<Attachment[]>(r),
    );
  },

  answer: (requestId: string, answer: string) =>
    fetch(`/api/human_requests/${requestId}/answer`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ answer }),
    }).then((r) => json<{ status: string }>(r)),

  retry: (taskId: string) =>
    fetch(`/api/tasks/${taskId}/retry`, { method: "POST" }).then((r) =>
      json<{ status: string }>(r),
    ),

  // cancel stops a running/queued/parked task: the daemon flips it to `cancelled`
  // and kills the live agent. Rejects (409) if the task isn't in a stoppable
  // state (already finished, in review, etc.).
  cancel: (taskId: string) =>
    fetch(`/api/tasks/${taskId}/cancel`, { method: "POST" }).then((r) =>
      json<{ status: string }>(r),
    ),

  // remove deletes a not-live task (backlog, or a terminal done/failed/cancelled)
  // for good, tearing down any leftover worktree. Rejects (409) if the task is
  // still live or in review — stop or finish it first.
  remove: (taskId: string) =>
    fetch(`/api/tasks/${taskId}`, { method: "DELETE" }).then((r) =>
      json<{ status: string }>(r),
    ),

  // archiveClosed clears every closed (done or cancelled) task in one sweep, so
  // the Done column doesn't grow without bound. Returns how many were removed.
  archiveClosed: () =>
    fetch(`/api/archive_closed`, { method: "POST" }).then((r) =>
      json<{ removed: number }>(r),
    ),

  // merge lands a reviewed task's worktree branch into the project repo and
  // finishes it. Rejects (409) with the git explanation if it can't complete.
  merge: (taskId: string) =>
    fetch(`/api/tasks/${taskId}/merge`, { method: "POST" }).then((r) =>
      json<{ status: string }>(r),
    ),

  // markDone finishes a reviewed task that has no worktree to merge (ran in
  // place). Rejects (409) if the task isn't in review.
  markDone: (taskId: string) =>
    fetch(`/api/tasks/${taskId}/done`, { method: "POST" }).then((r) =>
      json<{ status: string }>(r),
    ),

  taskEvents: (taskId: string) =>
    fetch(`/api/tasks/${taskId}/events`).then((r) => json<TaskEvent[]>(r)),

  // diff returns a reviewed task's changes vs its base branch. Rejects (404)
  // when the task has no worktree to diff (ran in place, or already merged).
  diff: (taskId: string) =>
    fetch(`/api/tasks/${taskId}/diff`).then((r) => json<TaskDiff>(r)),

  // revise re-engages the task's agent with the human's feedback (the review
  // "send it back" action): the agent reworks in the same worktree and the card
  // flips back to running. Rejects (409) if the task can't be sent back.
  revise: (taskId: string, message: string) =>
    fetch(`/api/tasks/${taskId}/revise`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message }),
    }).then((r) => json<{ status: string }>(r)),

  // shots lists the screenshot filenames the agent left for a task (empty when
  // none); shotUrl builds the URL to render one.
  shots: (taskId: string) =>
    fetch(`/api/tasks/${taskId}/shots`).then((r) => json<string[]>(r)),
  shotUrl: (taskId: string, name: string) =>
    `/api/tasks/${taskId}/shots/${encodeURIComponent(name)}`,

  projects: () => fetch("/api/projects").then((r) => json<Project[]>(r)),

  // pickProject opens the OS-native folder chooser on the daemon's machine and
  // registers the picked folder. Returns null when the user cancels the dialog
  // (HTTP 204), the created project on success.
  pickProject: async (): Promise<Project | null> => {
    const res = await fetch("/api/projects/pick", { method: "POST" });
    if (res.status === 204) return null;
    return json<Project>(res);
  },

  // addProject registers a project from a pasted absolute path — the fallback
  // where no native folder picker is available. The server validates the path is
  // an existing git repo and names the project after the folder. The created
  // project also arrives via SSE.
  addProject: (path: string) =>
    fetch("/api/projects", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path }),
    }).then((r) => json<Project>(r)),

  deleteProject: (id: string) =>
    fetch(`/api/projects/${id}`, { method: "DELETE" }).then((r) =>
      json<{ status: string }>(r),
    ),

  // settings returns the daemon-wide preferences the board can edit (currently
  // just the parallel-agent limit and its allowed range).
  settings: () => fetch("/api/settings").then((r) => json<Settings>(r)),

  // setConcurrency persists a new parallel-agent limit and applies it to the
  // running orchestrator. Returns the effective (clamped) value.
  setConcurrency: (value: number) =>
    fetch("/api/settings/concurrency", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ value }),
    }).then((r) => json<{ maxConcurrent: number }>(r)),

  // setContextCap persists the per-agent context budget in tokens (0 = off). The
  // new value is picked up on each running agent's next transcript poll. Returns
  // the effective (clamped) value.
  setContextCap: (value: number) =>
    fetch("/api/settings/context-cap", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ value }),
    }).then((r) => json<{ contextCap: number }>(r)),

  setTelegram: (value: { enabled: boolean; token: string; userId: number; chatId: number }) =>
	fetch("/api/settings/telegram", {
	  method: "POST",
	  headers: { "Content-Type": "application/json" },
	  body: JSON.stringify(value),
	}).then((r) => json<TelegramSettings>(r)),
};
