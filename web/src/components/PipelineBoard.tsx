import { api, type Project, type Task } from "../api";
import { groupColumns, projectMap } from "../util";
import { Column } from "./Column";

interface Props {
  tasks: Task[];
  activity: Record<string, string>;
  now: number;
  onOpen: (taskId: string) => void;
  projects: Project[];
  showChip?: boolean; // filter layout shows chips; swimlanes don't
  compact?: boolean; // swimlane rows use tighter column gaps
  // Default project for inline-added backlog tasks: the column's project in
  // swimlanes, the active filter in the filter layout, or "" for none.
  addProject?: string;
}

// PipelineBoard is the four-stage pipeline (Backlog · Running · Review · Done),
// the shared core reused by both the filter and swimlane layouts.
export function PipelineBoard({
  tasks,
  activity,
  now,
  onOpen,
  projects,
  showChip,
  compact,
  addProject,
}: Props) {
  const cols = groupColumns(tasks);
  const byName = projectMap(projects);
  const addTask = (title: string) =>
    api
      .createTask({
        title,
        body: "",
        project: addProject ?? "",
        agent: "claude",
        flow: "solo",
      })
      .then(() => {});
  return (
    <div className={`flex ${compact ? "gap-4" : "gap-6"}`}>
      <Column title="Backlog" tasks={cols.backlog} activity={activity} now={now} onOpen={onOpen} accent="muted" projectsByName={byName} showChip={showChip} onAdd={addTask} />
      <Column title="Running" tasks={cols.running} activity={activity} now={now} onOpen={onOpen} accent="steel" projectsByName={byName} showChip={showChip} />
      <Column title="Review" tasks={cols.review} activity={activity} now={now} onOpen={onOpen} accent="moss" projectsByName={byName} showChip={showChip} />
      <Column title="Done" tasks={cols.done} activity={activity} now={now} onOpen={onOpen} accent="moss" projectsByName={byName} showChip={showChip} />
    </div>
  );
}
