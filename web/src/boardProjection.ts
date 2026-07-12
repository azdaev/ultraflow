import type { BoardSnapshot, HumanRequest, Project, Task } from "./api";

export interface BoardProjection {
  tasks: Task[];
  requests: HumanRequest[];
  activity: Record<string, string>;
  activityKind: Record<string, string>;
  projects: Project[];
}

export const emptyBoardProjection: BoardProjection = {
  tasks: [], requests: [], activity: {}, activityKind: {}, projects: [],
};

type TaskPatch = { taskId: string } & Partial<Pick<Task, "status" | "updatedAt" | "worktree" | "attempt" | "port">>;

export type BoardEvent =
  | { kind: "snapshot"; data: BoardSnapshot }
  | { kind: "task_created"; data: Task }
  | { kind: "task_updated"; data: TaskPatch }
  | { kind: "task_deleted"; data: { id: string } }
  | { kind: "project_created"; data: Project }
  | { kind: "project_deleted"; data: { id: string } }
  | { kind: "human_request"; data: HumanRequest }
  | { kind: "human_answered" | "human_cancelled"; data: { id?: string; taskId?: string } }
  | { kind: "event"; data: { taskId: string; kind: string; data: string } };

export function decodeBoardEvent(value: unknown): BoardEvent | null {
  if (!value || typeof value !== "object" || !("kind" in value) || !("data" in value)) return null;
  const event = value as { kind: string; data: unknown };
  const known = new Set(["task_created", "task_updated", "task_deleted", "project_created", "project_deleted", "human_request", "human_answered", "human_cancelled", "event"]);
  return known.has(event.kind) ? event as BoardEvent : null;
}

// reduceBoardEvent is the board projection module's interface. Snapshot recovery
// and live events now converge on the same state and can be tested without React.
export function reduceBoardEvent(state: BoardProjection, event: BoardEvent): BoardProjection {
  switch (event.kind) {
    case "snapshot": {
      const b = event.data;
      return { tasks:b.tasks, requests:b.requests, activity:b.activity ?? {}, activityKind:b.activityKind ?? {}, projects:b.projects ?? [] };
    }
    case "task_created": return state.tasks.some(t => t.id === event.data.id) ? state : { ...state, tasks:[event.data, ...state.tasks] };
    case "task_updated": return { ...state, tasks:state.tasks.map(t => t.id === event.data.taskId ? { ...t, ...withoutTaskID(event.data) } : t) };
    case "task_deleted": return { ...state, tasks:state.tasks.filter(t => t.id !== event.data.id), requests:state.requests.filter(r => r.taskId !== event.data.id) };
    case "project_created": return state.projects.some(p => p.id === event.data.id) ? state : { ...state, projects:[...state.projects, event.data] };
    case "project_deleted": return { ...state, projects:state.projects.filter(p => p.id !== event.data.id) };
    case "human_request": return state.requests.some(r => r.id === event.data.id) ? state : { ...state, requests:[...state.requests, event.data] };
    case "human_answered": return { ...state, requests:state.requests.filter(r => r.id !== event.data.id) };
    case "human_cancelled": return { ...state, requests:state.requests.filter(r => event.data.id ? r.id !== event.data.id : r.taskId !== event.data.taskId) };
    case "event": return event.data.data ? { ...state, activity:{ ...state.activity, [event.data.taskId]:event.data.data }, activityKind:{ ...state.activityKind, [event.data.taskId]:event.data.kind } } : state;
  }
}

function withoutTaskID({ taskId: _, ...patch }: TaskPatch) { return patch; }
