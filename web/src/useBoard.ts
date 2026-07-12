import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { api, type HumanRequest, type Project, type Task } from "./api";
import { decodeBoardEvent, emptyBoardProjection, reduceBoardEvent } from "./boardProjection";

export interface BoardState {
  tasks: Task[]; requests: HumanRequest[]; activity: Record<string, string>;
  activityKind: Record<string, string>; projects: Project[]; connected: boolean; reload: () => void;
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

export function useNow(ms = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => { const id = setInterval(() => setNow(Date.now()), ms); return () => clearInterval(id); }, [ms]);
  return now;
}
