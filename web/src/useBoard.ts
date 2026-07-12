import { useCallback, useEffect, useRef, useState } from "react";
import { api, type HumanRequest, type Project, type Task } from "./api";

export interface BoardState {
  tasks: Task[];
  requests: HumanRequest[];
  activity: Record<string, string>;
  activityKind: Record<string, string>;
  projects: Project[];
  connected: boolean;
  reload: () => void;
}

// SSE envelope from the broker: { kind, data }.
interface Envelope {
  kind: string;
  data: unknown;
}

export function useBoard(): BoardState {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [requests, setRequests] = useState<HumanRequest[]>([]);
  const [activity, setActivity] = useState<Record<string, string>>({});
  const [activityKind, setActivityKind] = useState<Record<string, string>>({});
  const [projects, setProjects] = useState<Project[]>([]);
  const [connected, setConnected] = useState(false);

  const reload = useCallback(async () => {
    try {
      const b = await api.board();
      setTasks(b.tasks);
      setRequests(b.requests);
      setActivity(b.activity ?? {});
      setActivityKind(b.activityKind ?? {});
      setProjects(b.projects ?? []);
    } catch {
      /* transient; SSE reconnect will resync */
    }
  }, []);

  const reloadRef = useRef(reload);
  reloadRef.current = reload;

  useEffect(() => {
    reloadRef.current();

    const es = new EventSource("/api/events");
    es.onopen = () => {
      setConnected(true);
      // Resync the full snapshot on every (re)connect so we never miss events
      // dropped while disconnected.
      reloadRef.current();
    };
    es.onerror = () => setConnected(false);
    es.onmessage = (e) => {
      let env: Envelope;
      try {
        env = JSON.parse(e.data);
      } catch {
        return;
      }
      applyEvent(env, { setTasks, setRequests, setActivity, setActivityKind, setProjects });
    };

    return () => es.close();
  }, []);

  return { tasks, requests, activity, activityKind, projects, connected, reload };
}

type Setters = {
  setTasks: React.Dispatch<React.SetStateAction<Task[]>>;
  setRequests: React.Dispatch<React.SetStateAction<HumanRequest[]>>;
  setActivity: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  setActivityKind: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  setProjects: React.Dispatch<React.SetStateAction<Project[]>>;
};

function applyEvent(
  env: Envelope,
  { setTasks, setRequests, setActivity, setActivityKind, setProjects }: Setters,
) {
  switch (env.kind) {
    case "task_created": {
      const t = env.data as Task;
      setTasks((prev) => (prev.some((x) => x.id === t.id) ? prev : [t, ...prev]));
      break;
    }
    case "task_updated": {
      // Partial patch: a status transition carries the fresh updatedAt (so live
      // timers reset), while a worktree assignment carries just `worktree`.
      // Apply whichever fields are present, leaving the rest untouched.
      const d = env.data as {
        taskId: string;
        status?: Task["status"];
        updatedAt?: string;
        worktree?: string;
        attempt?: number;
        port?: number;
      };
      setTasks((prev) =>
        prev.map((t) => {
          if (t.id !== d.taskId) return t;
          const next = { ...t };
          if (d.status !== undefined) next.status = d.status;
          if (d.updatedAt !== undefined) next.updatedAt = d.updatedAt;
          if (d.worktree !== undefined) next.worktree = d.worktree;
          if (d.attempt !== undefined) next.attempt = d.attempt;
          if (d.port !== undefined) next.port = d.port;
          return next;
        }),
      );
      break;
    }
    case "task_deleted": {
      // A Removed/Archived task: drop it and any pending checkpoint it still had,
      // so the card and its rail entry leave the board together.
      const d = env.data as { id: string };
      setTasks((prev) => prev.filter((t) => t.id !== d.id));
      setRequests((prev) => prev.filter((r) => r.taskId !== d.id));
      break;
    }
    case "project_created": {
      const p = env.data as Project;
      setProjects((prev) =>
        prev.some((x) => x.id === p.id) ? prev : [...prev, p],
      );
      break;
    }
    case "project_deleted": {
      const d = env.data as { id: string };
      setProjects((prev) => prev.filter((p) => p.id !== d.id));
      break;
    }
    case "human_request": {
      const req = env.data as HumanRequest;
      setRequests((prev) =>
        prev.some((r) => r.id === req.id) ? prev : [...prev, req],
      );
      break;
    }
    case "human_answered":
    case "human_cancelled": {
      // Both retire a pending request: answered by the human, or cancelled
      // because the asking agent went away. Either way it leaves the rail.
      const d = env.data as { id: string };
      setRequests((prev) => prev.filter((r) => r.id !== d.id));
      break;
    }
    case "event": {
      const ev = env.data as { taskId: string; kind: string; data: string };
      if (ev.data) {
        setActivity((prev) => ({ ...prev, [ev.taskId]: ev.data }));
        // Track the latest event's kind in lockstep so the attention rail can
        // tell a "merge_failed" event from an ordinary status line.
        setActivityKind((prev) => ({ ...prev, [ev.taskId]: ev.kind }));
      }
      break;
    }
  }
}

// useNow returns a wall-clock timestamp that ticks every `ms`, for live timers.
export function useNow(ms = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(id);
  }, [ms]);
  return now;
}
