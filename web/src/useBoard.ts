import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { api, type HumanRequest, type Project, type RunProgress, type Task } from "./api";
import { decodeBoardEvent, emptyBoardProjection, reduceBoardEvent } from "./boardProjection";

export interface BoardState {
  tasks: Task[]; requests: HumanRequest[]; activity: Record<string, string>;
  activityKind: Record<string, string>; projects: Project[];
  // live flow progress per multi-step task, keyed by task id.
  runs: Record<string, RunProgress>;
  // latest context size (tokens) per task, keyed by task id.
  context: Record<string, number>;
  // latest model name per task, keyed by task id.
  models: Record<string, string>;
  connected: boolean; reload: () => void;
}

export function useBoard(): BoardState {
  const [board, dispatch] = useReducer(reduceBoardEvent, emptyBoardProjection);
  const [connected, setConnected] = useState(false);
  const reload = useCallback(async () => {
    try { dispatch({ kind:"snapshot", data:await api.board() }); }
    catch { /* transient; SSE reconnect will resync */ }
  }, []);
  const reloadRef = useRef(reload);
  reloadRef.current = reload;

  useEffect(() => {
    reloadRef.current();
    const es = new EventSource("/api/events");
    es.onopen = () => { setConnected(true); reloadRef.current(); };
    es.onerror = () => setConnected(false);
    es.onmessage = e => {
      try { const event = decodeBoardEvent(JSON.parse(e.data)); if (event) dispatch(event); }
      catch { /* malformed event: reconnect snapshot remains authoritative */ }
    };
    return () => es.close();
  }, []);
  return { ...board, connected, reload };
}

// useNow returns a wall-clock timestamp that ticks every `ms`, for live timers.
export function useNow(ms = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => { const id = setInterval(() => setNow(Date.now()), ms); return () => clearInterval(id); }, [ms]);
  return now;
}
