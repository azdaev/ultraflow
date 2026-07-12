import { AnimatePresence } from "motion/react";
import type { Project, Task } from "../api";
import { TaskCard } from "./TaskCard";

interface Props {
  title: string;
  tasks: Task[];
  activity: Record<string, string>;
  now: number;
  onOpen: (taskId: string) => void;
  accent?: "steel" | "moss" | "muted";
  projectsByName?: Map<string, Project>;
  showChip?: boolean;
}

const dotColor: Record<string, string> = {
  steel: "bg-steel",
  moss: "bg-moss",
  muted: "bg-muted",
};

// Column is a pure pipeline stage. Cards live directly on the concrete ground
// (no boxed column). Columns grow to fill the full width.
export function Column({
  title,
  tasks,
  activity,
  now,
  onOpen,
  accent = "muted",
  projectsByName,
  showChip,
}: Props) {
  return (
    <div className="flex min-w-0 flex-1 basis-0 flex-col">
      <div className="mb-3 flex items-center gap-2 px-0.5">
        <span className={`h-2 w-2 rounded-full ${dotColor[accent]}`} />
        <h2 className="eyebrow text-ink">{title}</h2>
        <span className="font-mono text-[11px] text-muted">{tasks.length}</span>
      </div>

      <div className="flex flex-col gap-2.5">
        <AnimatePresence mode="popLayout">
          {tasks.map((t) => (
            <TaskCard
              key={t.id}
              task={t}
              activity={activity[t.id]}
              now={now}
              onOpen={onOpen}
              project={projectsByName?.get(t.project)}
              showChip={showChip}
            />
          ))}
        </AnimatePresence>
        {tasks.length === 0 && (
          <div className="rounded-xl border border-dashed border-hairline px-3 py-6 text-center text-[12px] text-muted/70">
            Nothing here
          </div>
        )}
      </div>
    </div>
  );
}
