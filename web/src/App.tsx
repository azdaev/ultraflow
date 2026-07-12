import { useEffect, useMemo, useState } from "react";
import { motion } from "motion/react";
import { useBoard, useNow } from "./useBoard";
import { useLayout } from "./useSettings";
import type { Task } from "./api";
import { Topbar } from "./components/Topbar";
import { AttentionRail } from "./components/AttentionRail";
import { FilterBoard } from "./components/FilterBoard";
import { SwimlanesBoard } from "./components/SwimlanesBoard";
import { Composer } from "./components/Composer";
import { Settings } from "./components/Settings";
import { TaskDetail } from "./components/TaskDetail";
import type { AttentionItem } from "./components/RailCard";

export function App() {
  const { tasks, requests, activity, activityKind, projects, connected } = useBoard();
  const [layout, setLayout] = useLayout();
  const now = useNow(1000);
  // The composer carries an optional draft: the inline "+ Add task" hands off its
  // typed title and column project via "More…"; ⌘N and the Topbar open it blank.
  const [composer, setComposer] = useState({ open: false, title: "", project: "" });
  const openComposer = (title = "", project = "") =>
    setComposer({ open: true, title, project });
  const closeComposer = () => setComposer((c) => ({ ...c, open: false }));
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [openTaskId, setOpenTaskId] = useState<string | null>(null);

  // ⌘N / Ctrl+N opens the composer.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "n") {
        e.preventDefault();
        openComposer();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const byId = useMemo(() => {
    const m = new Map<string, Task>();
    for (const t of tasks) m.set(t.id, t);
    return m;
  }, [tasks]);

  const attention = useMemo<AttentionItem[]>(() => {
    const items: AttentionItem[] = requests.map((r) => ({
      type: "needs_human" as const,
      request: r,
      task: byId.get(r.taskId),
    }));
    for (const t of tasks) {
      if (t.status === "failed") {
        items.push({ type: "failed", task: t, activity: activity[t.id] });
      } else if (t.status === "review" && activityKind[t.id] === "merge_failed") {
        // A merge that couldn't land returns the card to review; raise it here so
        // it isn't a silent dead-end with only a tiny inline error.
        items.push({ type: "merge_failed", task: t, message: activity[t.id] });
      }
    }
    return items;
  }, [requests, tasks, byId, activity, activityKind]);

  // Orange strictly for needs_human; failures counted (and shown) separately.
  const needCount = requests.length;
  const failedCount = tasks.filter((t) => t.status === "failed").length;

  const running = tasks.filter((t) =>
    ["running", "needs_human", "planning", "merging"].includes(t.status),
  ).length;
  const queued = tasks.filter((t) =>
    ["queued", "backlog"].includes(t.status),
  ).length;

  const openTask = openTaskId ? (byId.get(openTaskId) ?? null) : null;
  const openRequest = openTaskId
    ? requests.find((r) => r.taskId === openTaskId)
    : undefined;

  return (
    <div className="flex min-h-full flex-col">
      <Topbar
        needCount={needCount}
        failedCount={failedCount}
        running={running}
        queued={queued}
        connected={connected}
        onNewTask={() => openComposer()}
        onOpenSettings={() => setSettingsOpen(true)}
      />

      <AttentionRail items={attention} now={now} onOpen={setOpenTaskId} />

      <main className="flex flex-1 px-6 py-6">
        {tasks.length === 0 ? (
          <EmptyBoard onNewTask={() => openComposer()} />
        ) : layout === "swimlanes" ? (
          <SwimlanesBoard
            tasks={tasks}
            activity={activity}
            now={now}
            onOpen={setOpenTaskId}
            projects={projects}
            onExpandComposer={openComposer}
          />
        ) : (
          <FilterBoard
            tasks={tasks}
            activity={activity}
            now={now}
            onOpen={setOpenTaskId}
            projects={projects}
            onExpandComposer={openComposer}
          />
        )}
      </main>

      <Composer
        open={composer.open}
        onClose={closeComposer}
        projects={projects}
        initialTitle={composer.title}
        initialProject={composer.project}
      />
      <Settings
        open={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        projects={projects}
        layout={layout}
        setLayout={setLayout}
      />
      <TaskDetail
        task={openTask}
        request={openRequest}
        activitySig={openTaskId ? activity[openTaskId] : undefined}
        now={now}
        onClose={() => setOpenTaskId(null)}
      />
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
  show: {
    opacity: 1,
    y: 0,
    transition: { type: "spring", stiffness: 320, damping: 30 },
  },
} as const;

function EmptyBoard({ onNewTask }: { onNewTask: () => void }) {
  return (
    <motion.div
      variants={emptyContainer}
      initial="hidden"
      animate="show"
      className="mx-auto mt-[10vh] max-w-md text-center"
    >
      <motion.div
        variants={emptyItem}
        className="mx-auto mb-4 grid h-12 w-12 place-items-center rounded-xl bg-surface shadow-sm"
      >
        <span className="h-3 w-3 rounded-[3px] bg-accent" />
      </motion.div>
      <motion.h2 variants={emptyItem} className="text-[19px] font-semibold text-ink">
        No tasks yet
      </motion.h2>
      <motion.p variants={emptyItem} className="mt-1.5 text-[14px] text-muted">
        Add a task and an agent picks it up automatically. When it needs you, it
        shows up loud in the attention rail.
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
