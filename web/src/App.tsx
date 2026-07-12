import { useEffect, useMemo, useState } from "react";
import { useBoard, useNow } from "./useBoard";
import { useAttentionNotifications } from "./useNotifications";
import { api, type Task } from "./api";
import { Composer } from "./components/Composer";
import { Settings } from "./components/Settings";
import { Changelog } from "./components/Changelog";
import { TaskDetail } from "./components/TaskDetail";
import type { AttentionItem } from "./components/RailCard";
import { RunsContext } from "./runsContext";
import { BoardPage } from "./board/BoardPage";
import { installClickJournal, logUI } from "./journal";

export function App() {
  const { tasks, requests, activity, activityKind, projects, runs, context, models, paused } = useBoard();
  const now = useNow(1000);
  // The composer carries an optional draft: the inline "+ Add task" hands off its
  // typed title and column project via "More…"; the "n" key and the Topbar open it blank.
  const [composer, setComposer] = useState({ open: false, title: "", project: "" });
  const openComposer = (title = "", project = "") =>
    setComposer({ open: true, title, project });
  const closeComposer = () => setComposer((c) => ({ ...c, open: false }));
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [changelogOpen, setChangelogOpen] = useState(false);
  const [openTaskId, setOpenTaskId] = useState<string | null>(null);

  // Verbose activity journal (see ./journal): a delegated click listener records
  // every actionable click for a couple of days of after-the-fact analysis.
  useEffect(() => installClickJournal(), []);

  // Pressing "n" opens the composer (Linear/GitHub style). ⌘N is a browser-chrome
  // shortcut (new window) that the page can never intercept, so we use a plain key
  // and skip it while the user is typing in a field.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key.toLowerCase() !== "n") return;
      const el = e.target as HTMLElement | null;
      if (
        el &&
        (el.isContentEditable ||
          el.tagName === "INPUT" ||
          el.tagName === "TEXTAREA" ||
          el.tagName === "SELECT")
      )
        return;
      e.preventDefault();
      openComposer();
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

  // OS notifications for a backgrounded tab: a new rail item raises one, clicking
  // it focuses Ultraflow on that task, answering it clears it.
  useAttentionNotifications(attention, setOpenTaskId);

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
    <RunsContext.Provider value={runs}>
      <BoardPage
        tasks={tasks}
        requests={requests}
        activity={activity}
        activityKind={activityKind}
        context={context}
        models={models}
        projects={projects}
        now={now}
        running={running}
        queued={queued}
        paused={paused}
        onTogglePause={() => {
          logUI("toggle_pause", { paused: !paused });
          void api.setPaused(!paused).catch(() => {});
        }}
        onOpenTask={(id) => {
          logUI("open_task", { task: id });
          setOpenTaskId(id);
        }}
        onNewTask={openComposer}
        onOpenSettings={() => setSettingsOpen(true)}
        onOpenChangelog={() => setChangelogOpen(true)}
      />

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
      />
      <Changelog open={changelogOpen} onClose={() => setChangelogOpen(false)} />
      <TaskDetail
        task={openTask}
        request={openRequest}
        activitySig={openTaskId ? activity[openTaskId] : undefined}
        model={openTaskId ? models[openTaskId] : undefined}
        now={now}
        onClose={() => setOpenTaskId(null)}
      />
    </RunsContext.Provider>
  );
}
