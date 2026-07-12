import { AnimatePresence } from "motion/react";
import type { Project, Task } from "../api";
import { projectMap } from "../util";
import { Card } from "./Card";
import { DotsIcon, PlusIcon } from "./icons";

// ColumnKind carries a column's fixed identity: its label and the semantic colour
// of its header dot + count pill. One entry per pipeline stage.
export interface ColumnKind {
  label: string;
  dot: string; // hex
  pillBg: string; // hex
  pillText: string; // hex
}

export const COLUMNS: Record<"backlog" | "running" | "review" | "done", ColumnKind> = {
  backlog: { label: "Backlog", dot: "#6E6E68", pillBg: "#E4E4E1", pillText: "#6E6E68" },
  running: { label: "Running", dot: "#2F6DB0", pillBg: "#E9F0F7", pillText: "#2F6DB0" },
  review: { label: "Review", dot: "#B45309", pillBg: "#F6ECD9", pillText: "#B45309" },
  done: { label: "Done", dot: "#4F7A4D", pillBg: "#E8EFE7", pillText: "#4F7A4D" },
};

interface Props {
  kind: ColumnKind;
  tasks: Task[];
  now: number;
  activity: Record<string, string>;
  activityKind: Record<string, string>;
  context: Record<string, number>;
  models: Record<string, string>;
  projects: Project[];
  onOpen: (taskId: string) => void;
  onAddTask?: () => void; // backlog only — the dashed "Add task" row
}

// Column is one pipeline stage: a coloured header (dot + caps label + count),
// then its cards, with the backlog column capped by the "Add task" row.
export function Column({ kind, tasks, now, activity, activityKind, context, models, projects, onOpen, onAddTask }: Props) {
  const pm = projectMap(projects);
  return (
    <div className="flex min-w-0 flex-col gap-2.25">
      <div className="flex flex-col gap-2.25 pb-3">
        <div className="flex items-center gap-2 px-0.5">
          <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: kind.dot }} />
          <span className="eyebrow shrink-0 text-[11px] leading-[14px]">{kind.label}</span>
          <span
            className="flex h-[17px] min-w-[19px] shrink-0 items-center justify-center rounded-full px-1.5 font-mono text-[10px] font-semibold leading-3"
            style={{ backgroundColor: kind.pillBg, color: kind.pillText }}
          >
            {tasks.length}
          </span>
          <span className="grow basis-0" />
          <DotsIcon className="text-[#B4B4AD]" />
        </div>
        <div className="h-px w-full shrink-0 bg-hairline" />
      </div>

      <div className="flex flex-col gap-2.5">
        <AnimatePresence initial={false}>
          {tasks.map((t, i) => (
            <Card
              key={t.id}
              task={t}
              activity={activity[t.id]}
              activityKind={activityKind[t.id]}
              contextTokens={context[t.id]}
              model={models[t.id]}
              now={now}
              index={i}
              project={pm.get(t.project)}
              onOpen={onOpen}
            />
          ))}
        </AnimatePresence>

        {onAddTask && (
          <button
            onClick={onAddTask}
            className="flex w-full items-center gap-1.75 rounded-xl border border-dashed border-[#D6D6D0] px-3 py-2.5 text-faint transition hover:border-ink/30 hover:text-muted"
          >
            <PlusIcon />
            <span className="text-[12.5px] font-medium leading-4">Add task</span>
          </button>
        )}
      </div>
    </div>
  );
}
