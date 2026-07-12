import type { Project, Task } from "../api";
import { groupColumns } from "../util";
import { Column, COLUMNS } from "./Column";

interface Props {
  tasks: Task[]; // already filtered by the selected project
  now: number;
  activity: Record<string, string>;
  activityKind: Record<string, string>;
  context: Record<string, number>;
  projects: Project[];
  onOpen: (taskId: string) => void;
  onAddTask: () => void;
}

// Board is the fixed four-column pipeline. groupColumns is the single source of
// truth for status→column; failed tasks (which groupColumns drops, since they had
// no column in the old rail-based layout) are surfaced at the top of Running so a
// crashed task stays visible and right-clickable (Retry/Remove) rather than
// vanishing now that the attention rail is gone.
export function Board({ tasks, now, activity, activityKind, context, projects, onOpen, onAddTask }: Props) {
  const cols = groupColumns(tasks);
  const failed = tasks.filter((t) => t.status === "failed");
  const running = [...failed, ...cols.running];

  const shared = { now, activity, activityKind, context, projects, onOpen };

  return (
    <div className="grid grid-cols-4 items-start gap-4 px-5 pb-10">
      <Column kind={COLUMNS.backlog} tasks={cols.backlog} onAddTask={onAddTask} {...shared} />
      <Column kind={COLUMNS.running} tasks={running} {...shared} />
      <Column kind={COLUMNS.review} tasks={cols.review} {...shared} />
      <Column kind={COLUMNS.done} tasks={cols.done} {...shared} />
    </div>
  );
}
