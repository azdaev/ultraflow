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

export interface BoardSnapshot {
  tasks: Task[];
  requests: HumanRequest[];
  activity: Record<string, string>;
  // kind of each task's latest activity line, parallel to `activity`. Lets the
  // board lift a "merge_failed" event into the attention rail.
  activityKind: Record<string, string>;
  projects: Project[];
}

export interface Settings {
  maxConcurrent: number;
  maxConcurrentMin: number;
  maxConcurrentMax: number;
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

async function json<T>(res: Response): Promise<T> {
  if (!res.ok) throw new Error((await res.text()) || res.statusText);
  return res.json() as Promise<T>;
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
};
