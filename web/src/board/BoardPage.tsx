import { useMemo, useState } from "react";
import { motion } from "motion/react";
import type { HumanRequest, Project, Task } from "../api";
import { TopBar } from "./TopBar";
import { Toolbar } from "./Toolbar";
import { Board } from "./Board";

interface Props {
  tasks: Task[];
  requests: HumanRequest[];
  activity: Record<string, string>;
  activityKind: Record<string, string>;
  context: Record<string, number>;
  contextCap: number;
  models: Record<string, string>;
  projects: Project[];
  now: number;
  running: number;
  queued: number;
  paused: boolean;
  onTogglePause: () => void;
  onOpenTask: (taskId: string) => void;
  onNewTask: (title?: string, project?: string) => void;
  onOpenSettings: () => void;
  onOpenChangelog: () => void;
}

// BoardPage is the whole board surface: TopBar + Toolbar + Board. It owns just the
// project-filter selection; task data, modals, and notifications stay in App.
export function BoardPage({
  tasks,
  requests,
  activity,
  activityKind,
  context,
  contextCap,
  models,
  projects,
  now,
  running,
  queued,
  paused,
  onTogglePause,
  onOpenTask,
  onNewTask,
  onOpenSettings,
  onOpenChangelog,
}: Props) {
  const [selected, setSelected] = useState<string | null>(null);
  const [query, setQuery] = useState("");

  // If the selected project is removed in Settings, fall back to "All" rather than
  // stranding the board on a filter with no chip to clear it.
  const effective = selected !== null && projects.some((p) => p.name === selected) ? selected : null;
  // Project chip and the TopBar search compose: a task shows only if it matches
  // both. Search is a case-insensitive substring over title + body.
  const q = query.trim().toLowerCase();
  const filtered = tasks.filter(
    (t) =>
      (effective === null || t.project === effective) &&
      (q === "" || t.title.toLowerCase().includes(q) || t.body.toLowerCase().includes(q)),
  );

  // Enter in the command bar: with one match, jump straight into it; with none,
  // create a task from the query (same as the no-results "Create" button). Several
  // matches stay ambiguous, so Enter is a no-op and you keep refining.
  const onSubmit = () => {
    if (q === "") return;
    if (filtered.length === 1) onOpenTask(filtered[0].id);
    else if (filtered.length === 0) onNewTask(query.trim(), effective ?? "");
  };

  // The attention indicator is global (like the old rail): every task that needs a
  // human answer, plus any failed task. Clicking jumps to the first such task's
  // detail, where the answer box lives.
  const failed = useMemo(() => tasks.filter((t) => t.status === "failed"), [tasks]);
  const attentionCount = requests.length + failed.length;
  const openAttention = () => {
    const target = requests[0]?.taskId ?? failed[0]?.id;
    if (target) onOpenTask(target);
  };

  return (
    <div className="flex min-h-full flex-col">
      <TopBar
        running={running}
        queued={queued}
        query={query}
        onSearch={setQuery}
        onSubmit={onSubmit}
        paused={paused}
        onTogglePause={onTogglePause}
        onNewTask={() => onNewTask()}
        onOpenSettings={onOpenSettings}
        onOpenChangelog={onOpenChangelog}
      />
      <Toolbar
        projects={projects}
        tasks={tasks}
        selected={effective}
        onSelect={setSelected}
        attentionCount={attentionCount}
        onOpenAttention={openAttention}
      />

      {tasks.length === 0 ? (
        <EmptyBoard onNewTask={() => onNewTask()} />
      ) : q !== "" && filtered.length === 0 ? (
        // Search that finds nothing is a dead end unless it offers the obvious next
        // step: the thing isn't here, so make it. Hands the typed text off to the
        // composer as the new task's title (and the active project, if any).
        <div className="mx-auto mt-[12vh] flex flex-col items-center gap-3 px-6 text-center">
          <p className="text-[14px] text-muted">No tasks match “{query.trim()}”.</p>
          <button
            onClick={() => onNewTask(query.trim(), effective ?? "")}
            className="rounded-lg bg-accent px-4 py-2 text-[13px] font-semibold text-white transition hover:brightness-105"
          >
            Create “{query.trim()}”
          </button>
        </div>
      ) : (
        <Board
          tasks={filtered}
          now={now}
          activity={activity}
          activityKind={activityKind}
          context={context}
          contextCap={contextCap}
          models={models}
          projects={projects}
          onOpen={onOpenTask}
          onAddTask={() => onNewTask("", effective ?? "")}
        />
      )}
    </div>
  );
}

// The empty state is the first thing a new user sees, so it enters as staged
// chunks (icon → heading → copy → CTA) rather than one block popping in.
const emptyContainer = {
  hidden: {},
  show: { transition: { staggerChildren: 0.08, delayChildren: 0.05 } },
};
const emptyItem = {
  hidden: { opacity: 0, y: 8 },
  show: { opacity: 1, y: 0, transition: { type: "spring", stiffness: 320, damping: 30 } },
} as const;

function EmptyBoard({ onNewTask }: { onNewTask: () => void }) {
  return (
    <motion.div variants={emptyContainer} initial="hidden" animate="show" className="mx-auto mt-[12vh] max-w-md px-6 text-center">
      <motion.div variants={emptyItem} className="mx-auto mb-4 grid size-12 place-items-center rounded-xl bg-surface shadow-sm">
        <span className="size-3 rounded-[3px] bg-accent" />
      </motion.div>
      <motion.h2 variants={emptyItem} className="text-[19px] font-semibold text-ink">
        No tasks yet
      </motion.h2>
      <motion.p variants={emptyItem} className="mt-1.5 text-[14px] text-muted">
        Add a task and an agent picks it up automatically. When it needs you, it shows up in the toolbar — and you can answer
        right from the task.
      </motion.p>
      <motion.button
        variants={emptyItem}
        onClick={onNewTask}
        className="mt-4 rounded-lg bg-accent px-4 py-2.5 text-[14px] font-semibold text-white transition hover:brightness-105"
      >
        Add your first task
      </motion.button>
    </motion.div>
  );
}
