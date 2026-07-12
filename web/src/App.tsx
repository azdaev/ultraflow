import { useEffect, useMemo, useState } from "react";
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
  const { tasks, requests, activity, projects, connected } = useBoard();
  const [layout, setLayout] = useLayout();
  const now = useNow(1000);
  const [composerOpen, setComposerOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [openTaskId, setOpenTaskId] = useState<string | null>(null);

  // ⌘N / Ctrl+N opens the composer.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "n") {
        e.preventDefault();
        setComposerOpen(true);
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
      }
    }
    return items;
  }, [requests, tasks, byId, activity]);

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
        onNewTask={() => setComposerOpen(true)}
        onOpenSettings={() => setSettingsOpen(true)}
      />

      <AttentionRail items={attention} now={now} onOpen={setOpenTaskId} />

      <main className="flex flex-1 px-6 py-6">
        {tasks.length === 0 ? (
          <EmptyBoard onNewTask={() => setComposerOpen(true)} />
        ) : layout === "swimlanes" ? (
          <SwimlanesBoard
            tasks={tasks}
            activity={activity}
            now={now}
            onOpen={setOpenTaskId}
            projects={projects}
          />
        ) : (
          <FilterBoard
            tasks={tasks}
            activity={activity}
            now={now}
            onOpen={setOpenTaskId}
            projects={projects}
          />
        )}
      </main>

      <Composer
        open={composerOpen}
        onClose={() => setComposerOpen(false)}
        projects={projects}
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

function EmptyBoard({ onNewTask }: { onNewTask: () => void }) {
  return (
    <div className="mx-auto mt-[10vh] max-w-md text-center">
      <div className="mx-auto mb-4 grid h-12 w-12 place-items-center rounded-xl bg-surface shadow-sm">
        <span className="h-3 w-3 rounded-[3px] bg-accent" />
      </div>
      <h2 className="text-[19px] font-semibold text-ink">No tasks yet</h2>
      <p className="mt-1.5 text-[14px] text-muted">
        Add a task and an agent picks it up automatically. When it needs you, it
        shows up loud in the attention rail.
      </p>
      <button
        onClick={onNewTask}
        className="mt-4 rounded-lg bg-accent px-4 py-2.5 text-[14px] font-semibold text-white transition hover:brightness-105"
      >
        Add your first task
      </button>
    </div>
  );
}
