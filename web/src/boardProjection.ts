import type { BoardSnapshot, HumanRequest, Project, RunProgress, Task } from "./api";

export interface BoardProjection {
  tasks: Task[];
  requests: HumanRequest[];
  activity: Record<string, string>;
  activityKind: Record<string, string>;
  projects: Project[];
  // live flow progress per multi-step task, keyed by task id.
  runs: Record<string, RunProgress>;
  // latest context size (tokens) per task, keyed by task id, for the context meter.
  context: Record<string, number>;
  // configured per-agent context budget in tokens (0 = off); the meter's scale.
  contextCap: number;
  // latest model name per task, keyed by task id, for the card's agent footer.
  models: Record<string, string>;
  // whether ALL agents are currently held (the "pause all" toggle).
  paused: boolean;
}

export const emptyBoardProjection: BoardProjection = {
  tasks: [], requests: [], activity: {}, activityKind: {}, projects: [], runs: {}, context: {}, contextCap: 0, models: {}, paused: false,
};

type TaskPatch = { taskId: string } & Partial<Pick<Task, "status" | "updatedAt" | "worktree" | "outcome" | "handoff" | "attempt" | "port" | "title" | "body">>;

export type BoardEvent =
  | { kind: "snapshot"; data: BoardSnapshot }
  | { kind: "task_created"; data: Task }
  | { kind: "task_updated"; data: TaskPatch }
  | { kind: "task_deleted"; data: { id: string } }
  | { kind: "project_created"; data: Project }
  | { kind: "project_deleted"; data: { id: string } }
  | { kind: "human_request"; data: HumanRequest }
  | { kind: "human_answered" | "human_cancelled"; data: { id?: string; taskId?: string } }
  | { kind: "event"; data: { taskId: string; kind: string; data: string } }
  | { kind: "run_updated"; data: { taskId: string; progress: RunProgress } }
  | { kind: "context"; data: { taskId: string; tokens: number } }
  | { kind: "model"; data: { taskId: string; model: string } }
  | { kind: "paused"; data: { paused: boolean } }
  | { kind: "settings"; data: { contextCap: number } };

export function decodeBoardEvent(value: unknown): BoardEvent | null {
  if (!value || typeof value !== "object" || !("kind" in value) || !("data" in value)) return null;
  const event = value as { kind: string; data: unknown };
  const known = new Set(["task_created", "task_updated", "task_deleted", "project_created", "project_deleted", "human_request", "human_answered", "human_cancelled", "event", "run_updated", "context", "model", "paused", "settings"]);
  return known.has(event.kind) ? event as BoardEvent : null;
}

// reduceBoardEvent is the board projection module's interface. Snapshot recovery
// and live events now converge on the same state and can be tested without React.
export function reduceBoardEvent(state: BoardProjection, event: BoardEvent): BoardProjection {
  switch (event.kind) {
    case "snapshot": {
      const b = event.data;
      return { tasks:b.tasks, requests:b.requests, activity:b.activity ?? {}, activityKind:b.activityKind ?? {}, projects:b.projects ?? [], runs:b.runs ?? {}, context:b.context ?? {}, contextCap:b.contextCap ?? 0, models:b.models ?? {}, paused:b.paused ?? false };
    }
    case "task_created": return state.tasks.some(t => t.id === event.data.id) ? state : { ...state, tasks:[event.data, ...state.tasks] };
    case "task_updated": return { ...state, tasks:state.tasks.map(t => t.id === event.data.taskId ? { ...t, ...withoutTaskID(event.data) } : t) };
    case "task_deleted": { const id = event.data.id; return { ...state, tasks:state.tasks.filter(t => t.id !== id), requests:state.requests.filter(r => r.taskId !== id), runs:withoutKey(state.runs, id), activity:withoutKey(state.activity, id), activityKind:withoutKey(state.activityKind, id), context:withoutKey(state.context, id), models:withoutKey(state.models, id) }; }
    case "project_created": return state.projects.some(p => p.id === event.data.id) ? state : { ...state, projects:[...state.projects, event.data] };
    case "project_deleted": return { ...state, projects:state.projects.filter(p => p.id !== event.data.id) };
    case "human_request": return state.requests.some(r => r.id === event.data.id) ? state : { ...state, requests:[...state.requests, event.data] };
    case "human_answered": return { ...state, requests:state.requests.filter(r => r.id !== event.data.id) };
    case "human_cancelled": return { ...state, requests:state.requests.filter(r => event.data.id ? r.id !== event.data.id : r.taskId !== event.data.taskId) };
    case "event": return event.data.data ? { ...state, activity:{ ...state.activity, [event.data.taskId]:event.data.data }, activityKind:{ ...state.activityKind, [event.data.taskId]:event.data.kind } } : state;
    case "run_updated": return { ...state, runs:{ ...state.runs, [event.data.taskId]:event.data.progress } };
    case "context": return { ...state, context:{ ...state.context, [event.data.taskId]:event.data.tokens } };
    case "model": return { ...state, models:{ ...state.models, [event.data.taskId]:event.data.model } };
    case "paused": return { ...state, paused:event.data.paused };
    case "settings": return { ...state, contextCap:event.data.contextCap };
  }
}

// withoutKey returns a copy of a record with one key removed (leaves it untouched
// if absent) — used to drop a deleted task's flow progress.
function withoutKey<T>(rec: Record<string, T>, key: string): Record<string, T> {
  if (!(key in rec)) return rec;
  const next = { ...rec };
  delete next[key];
  return next;
}

function withoutTaskID({ taskId: _, ...patch }: TaskPatch) { return patch; }
