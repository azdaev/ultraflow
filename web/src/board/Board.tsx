import { useEffect, useRef } from "react";
import { api, type Project, type Task } from "../api";
import { groupColumns } from "../util";
import { Column, COLUMNS } from "./Column";
import type { CardEnter } from "./Card";

// How far a card slides in from when it changes columns. A partial slide (well under
// a column's width) reads as directional movement without looking like the card flew
// in from off-screen.
const SLIDE = 44;

interface Props {
  tasks: Task[]; // already filtered by the selected project
  now: number;
  activity: Record<string, string>;
  activityKind: Record<string, string>;
  context: Record<string, number>;
  contextCap: number;
  models: Record<string, string>;
  projects: Project[];
  onOpen: (taskId: string) => void;
  onAddTask: () => void;
}

// Board is the fixed four-column pipeline. groupColumns is the single source of
// truth for status→column; failed tasks (which groupColumns drops, since they had
// no column in the old rail-based layout) are surfaced at the top of Running so a
// crashed task stays visible and right-clickable (Retry/Remove) rather than
// vanishing now that the attention rail is gone.
export function Board({ tasks, now, activity, activityKind, context, contextCap, models, projects, onOpen, onAddTask }: Props) {
  const cols = groupColumns(tasks);
  const failed = tasks.filter((t) => t.status === "failed");
  const running = [...failed, ...cols.running];

  // Each task's current column index (0=Backlog … 3=Done) — the axis the entrance
  // animation reads for left/right direction.
  const colOf = new Map<string, number>();
  cols.backlog.forEach((t) => colOf.set(t.id, 0));
  running.forEach((t) => colOf.set(t.id, 1));
  cols.review.forEach((t) => colOf.set(t.id, 2));
  cols.done.forEach((t) => colOf.set(t.id, 3));

  // lastCol remembers which column each task was LAST painted in, so a status change
  // reads as a directional move. Two subtleties keep it honest:
  //  1. It's refreshed in an effect (after paint), never during render. A status
  //     change can trigger a couple of renders before the moved card's DOM actually
  //     mounts (e.g. the once-a-second clock tick coalescing in). Flipping lastCol
  //     synchronously would make that follow-up render read the card as already
  //     settled and mount it with no slide. Deferring keeps every pre-paint render
  //     (the mount included) seeing the move.
  //  2. It OVERLAYS current columns onto the prior map rather than replacing it, so a
  //     render that momentarily drops a task mid-transition doesn't erase its
  //     remembered column and make it re-enter as brand-new.
  const lastCol = useRef<Map<string, number>>(new Map());
  const prev = lastCol.current;
  const enterOf = (id: string): CardEnter => {
    const cur = colOf.get(id);
    const was = prev.get(id);
    if (was === undefined || cur === undefined) return "new"; // first sighting → fade up
    if (was === cur) return false; // same column → don't re-animate on the clock tick
    // Moved right (Backlog→…→Done) → slide in from the left (negative x); moved left
    // (a revise/retry) → slide in from the right. The sign encodes the direction.
    return cur > was ? -SLIDE : SLIDE;
  };
  useEffect(() => {
    const next = new Map(lastCol.current);
    colOf.forEach((c, id) => next.set(id, c));
    lastCol.current = next;
  });

  const shared = { now, activity, activityKind, context, contextCap, models, projects, onOpen, enterOf };

  return (
    <div className="grid grid-cols-4 items-start gap-4 px-5 pb-10">
      <Column kind={COLUMNS.backlog} tasks={cols.backlog} onAddTask={onAddTask} {...shared} />
      <Column kind={COLUMNS.running} tasks={running} {...shared} />
      <Column kind={COLUMNS.review} tasks={cols.review} {...shared} />
      <Column kind={COLUMNS.done} tasks={cols.done} onClear={() => api.archiveClosed().catch(() => {})} {...shared} />
    </div>
  );
}
