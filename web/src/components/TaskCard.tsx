import { useState } from "react";
import { motion } from "motion/react";
import { api, errMsg, type Project, type Task } from "../api";
import {
  agentColor,
  agentLabel,
  ago,
  CANCELLABLE,
  CLOSED,
  copyText,
  DELETABLE,
  DEV_LINK_STATUSES,
  elapsed,
  flowOf,
} from "../util";
import { FlowStepper } from "./FlowStepper";
import { ProjectChip } from "./ProjectChip";
import { ContextMenu, useContextMenu, type MenuItem } from "./ContextMenu";
import { useRun } from "../runsContext";

interface Props {
  task: Task;
  activity?: string;
  activityKind?: string; // kind of the latest activity line (e.g. "stale")
  now: number;
  onOpen: (taskId: string) => void;
  project?: Project; // for the chip color
  showChip?: boolean; // filter layout shows chips; swimlanes name via the lane
}

export function TaskCard({ task, activity, activityKind, now, onOpen, project, showChip }: Props) {
  const run = useRun(task.id);
  const needsHuman = task.status === "needs_human";
  // A reviewed branch that has fallen behind main. The auto-rebase runs at merge,
  // but we warn up front so the human knows the branch isn't current (roadmap M4).
  const stale = task.status === "review" && activityKind === "stale";
  const menu = useContextMenu();

  // Right-click actions mirror a card's primary controls, keyed off its status,
  // so they're reachable without aiming at the small inline buttons. SSE
  // reflects merge/mark-done/retry results, so we fire and let a conflict
  // resurface via the attention rail instead of blocking here.
  const items: MenuItem[] = [
    { label: "Open details", onSelect: () => onOpen(task.id) },
  ];
  if (task.status === "review") {
    items.push(
      task.worktree
        ? { label: "Merge → done", onSelect: () => api.merge(task.id).catch(() => {}) }
        : { label: "Mark done", onSelect: () => api.markDone(task.id).catch(() => {}) },
    );
  }
  if (task.status === "failed") {
    items.push({ label: "Retry", onSelect: () => api.retry(task.id).catch(() => {}) });
  }
  if (CANCELLABLE.has(task.status)) {
    items.push({ label: "Stop task", danger: true, onSelect: () => api.cancel(task.id).catch(() => {}) });
  }
  items.push({ separator: true });
  items.push({ label: "Copy task ID", onSelect: () => copyText(task.id) });
  if (task.worktree) {
    items.push({ label: "Copy worktree path", onSelect: () => copyText(task.worktree) });
  }
  if (DELETABLE.has(task.status)) {
    items.push({ separator: true });
    items.push({ label: "Remove task", danger: true, onSelect: () => api.remove(task.id).catch(() => {}) });
  }

  return (
    <>
    <motion.button
      layout
      onClick={() => onOpen(task.id)}
      onContextMenu={menu.openMenu}
      initial={{ opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ type: "spring", stiffness: 320, damping: 30 }}
      className={`block w-full rounded-xl border bg-surface p-3.5 text-left transition hover:border-ink/25 ${
        needsHuman ? "border-accent-line" : "border-hairline"
      }`}
    >
      {/* header line: status dot + label / timer */}
      <div className="flex items-center justify-between">
        <StatusLabel task={task} now={now} />
        <span className="font-mono text-[11px] text-muted">
          {CLOSED.has(task.status) ? ago(task.updatedAt, now) : elapsed(task.updatedAt, now)}
        </span>
      </div>

      <h3
        className={`mt-2 text-[15px] font-semibold leading-snug ${
          CLOSED.has(task.status) ? "text-muted" : "text-ink"
        }`}
      >
        {task.title}
      </h3>

      {showChip && task.project && (
        <div className="mt-2">
          <ProjectChip name={task.project} project={project} />
        </div>
      )}

      {/* needs-you flag: the card stays in its real stage, mirrored to the rail */}
      {needsHuman && (
        <div className="mt-2 flex items-center gap-1.5 text-[12px] font-semibold text-accent">
          <span className="h-1.5 w-1.5 rounded-full bg-accent" />
          Needs you · answer above ↑
        </div>
      )}

      {/* running activity strip */}
      {task.status === "running" && activity && (
        <div className="mt-2 flex items-center gap-2 rounded-lg bg-steel-tint px-2.5 py-1.5">
          <span className="relative flex h-1.5 w-1.5 shrink-0">
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-steel opacity-60" />
            <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-steel" />
          </span>
          <span className="truncate font-mono text-[11px] text-steel">{activity}</span>
        </div>
      )}

      {/* dev-server link: while a task is live or in review it holds a reserved
          port, so offer a one-click open of its running app (localhost:PORT). */}
      {task.port > 0 && DEV_LINK_STATUSES.has(task.status) && (
        <DevServerLink port={task.port} />
      )}

      {/* stale-branch warning: the branch fell behind main. Merging auto-rebases
          onto the latest main first, so what lands is what was reviewed. */}
      {stale && (
        <div className="mt-2 flex items-center gap-1.5 rounded-lg bg-amber-tint px-2.5 py-1.5 text-[12px] font-semibold text-amber">
          <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-amber" />
          <span className="truncate">{activity || "stale · behind main"}</span>
        </div>
      )}

      {/* review → finish. With a worktree, land the branch (merge). Without one
          (a non-git / shared-workdir project runs in place, nothing to land),
          offer a plain "Mark done" so the card isn't a dead-end. */}
      {task.status === "review" &&
        (task.worktree ? (
          <MergeAction taskId={task.id} />
        ) : (
          <MarkDoneAction taskId={task.id} />
        ))}

      {/* flow stepper — with the live step caption for a multi-step run */}
      <div className="mt-3">
        <FlowStepper flow={task.flow} status={task.status} run={run} />
        {run && run.caption && !CLOSED.has(task.status) && (
          <p className="mt-1.5 truncate text-[11px] text-muted">{run.caption}</p>
        )}
      </div>

      {/* footer meta */}
      <div className="mt-3 flex items-center justify-between border-t border-hairline pt-2.5">
        <span className="flex items-center gap-1.5">
          {/* the live sub-agent for the active step (a flow step can override the
              task's agent); falls back to the task's own agent for solo. */}
          <span
            className="h-2 w-2 rounded-full"
            style={{ backgroundColor: agentColor(run?.agent ?? task.agent) }}
          />
          <span className="text-[12px] text-muted">{agentLabel(run?.agent ?? task.agent)}</span>
        </span>
        <span className="font-mono text-[11px] text-muted">{flowOf(task.flow).label}</span>
      </div>
    </motion.button>
    <ContextMenu menu={menu} items={items} />
    </>
  );
}

// MergeAction is a review-card control. It lives inside the card's <button>, so
// it renders as a role="button" span and stops click propagation (a nested
// <button> is invalid, and the click must not open the drawer). SSE moves the
// card to "done" on success; a conflict surfaces the git message inline.
function MergeAction({ taskId }: { taskId: string }) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function merge(e: React.MouseEvent) {
    e.stopPropagation();
    if (busy) return;
    setBusy(true);
    setErr(null);
    try {
      await api.merge(taskId);
    } catch (e) {
      setErr(errMsg(e, "merge failed"));
      setBusy(false);
    }
  }

  return (
    <div className="mt-2.5">
      <span
        role="button"
        tabIndex={0}
        aria-disabled={busy}
        onClick={merge}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") merge(e as unknown as React.MouseEvent);
        }}
        className={`inline-flex items-center gap-1.5 rounded-lg bg-moss px-3 py-1.5 text-[13px] font-semibold text-white transition hover:brightness-105 ${
          busy ? "opacity-60" : ""
        }`}
      >
        {busy ? "Merging…" : "Merge → done"}
      </span>
      {err && <p className="mt-1.5 line-clamp-2 text-[12px] text-rust">{err}</p>}
    </div>
  );
}

// MarkDoneAction is the review control for a task with no worktree to merge (it
// ran in place). It just marks the task done. Same nested-control conventions as
// MergeAction: role="button" span, stop propagation so the drawer doesn't open.
function MarkDoneAction({ taskId }: { taskId: string }) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function markDone(e: React.MouseEvent) {
    e.stopPropagation();
    if (busy) return;
    setBusy(true);
    setErr(null);
    try {
      await api.markDone(taskId);
    } catch (e) {
      setErr(errMsg(e, "couldn't mark done"));
      setBusy(false);
    }
  }

  return (
    <div className="mt-2.5">
      <span
        role="button"
        tabIndex={0}
        aria-disabled={busy}
        onClick={markDone}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") markDone(e as unknown as React.MouseEvent);
        }}
        className={`inline-flex items-center gap-1.5 rounded-lg bg-moss px-3 py-1.5 text-[13px] font-semibold text-white transition hover:brightness-105 ${
          busy ? "opacity-60" : ""
        }`}
      >
        {busy ? "Finishing…" : "Mark done"}
      </span>
      {err && <p className="mt-1.5 line-clamp-2 text-[12px] text-rust">{err}</p>}
    </div>
  );
}

// DevServerLink opens the task's live dev server (http://localhost:PORT) in a new
// tab. It lives inside the card's <button>, so — like MergeAction — it renders as
// a role="link" span (a nested <a>/<button> is invalid) and stops click
// propagation so opening the server doesn't also open the task drawer.
function DevServerLink({ port }: { port: number }) {
  const url = `http://localhost:${port}`;
  const open = (e: React.MouseEvent | React.KeyboardEvent) => {
    e.stopPropagation();
    window.open(url, "_blank", "noopener");
  };
  return (
    <div className="mt-2.5">
      <span
        role="link"
        tabIndex={0}
        onClick={open}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") open(e);
        }}
        title={`Open this task's dev server (${url})`}
        className="inline-flex items-center gap-1.5 rounded-lg border border-hairline bg-board px-2.5 py-1 font-mono text-[11px] text-steel transition hover:border-steel/40 hover:text-ink"
      >
        <span className="h-1.5 w-1.5 rounded-full bg-steel" />
        localhost:{port} ↗
      </span>
    </div>
  );
}

function StatusLabel({ task, now }: { task: Task; now: number }) {
  switch (task.status) {
    case "running":
    case "planning":
      // Self-heal sub-state: an agent that errored is auto-diagnosing and retrying.
      // It STAYS running (never a red card) — surface "fixing itself · k/N" instead
      // of a plain "Running" so the human sees it working through the problem.
      if (task.status === "running" && task.attempt > 0) {
        return (
          <span className="flex items-center gap-1.5">
            <motion.span
              className="h-2.5 w-2.5 rounded-full border-[1.5px] border-steel border-t-transparent"
              animate={{ rotate: 360 }}
              transition={{ repeat: Infinity, ease: "linear", duration: 0.9 }}
            />
            <span className="text-[12px] font-semibold text-steel">
              Fixing itself · {task.attempt}/{task.maxAttempts}
            </span>
          </span>
        );
      }
      return (
        <Label dot="bg-steel" text="text-steel">
          {task.status === "planning" ? "Planning" : "Running"}
        </Label>
      );
    case "needs_human":
      return (
        <Label dot="bg-accent" text="text-accent">
          Needs you
        </Label>
      );
    case "queued":
      return (
        <Label dot="bg-moss" text="text-moss">
          Ready · waiting for a slot
        </Label>
      );
    case "review":
      return (
        <Label dot="bg-moss" text="text-moss">
          Ready to review
        </Label>
      );
    case "merging":
      return (
        <Label dot="bg-moss" text="text-moss">
          Merging
        </Label>
      );
    case "done":
      return (
        <Label dot="bg-moss" text="text-muted">
          Done
        </Label>
      );
    case "failed":
      return (
        <Label dot="bg-rust" text="text-rust">
          Failed
        </Label>
      );
    case "cancelled":
      return (
        <Label dot="bg-muted" text="text-muted">
          Stopped
        </Label>
      );
    default:
      return (
        <Label dot="bg-muted" text="text-muted">
          Queued · {ago(task.createdAt, now)}
        </Label>
      );
  }
}

function Label({
  dot,
  text,
  children,
}: {
  dot: string;
  text: string;
  children: React.ReactNode;
}) {
  return (
    <span className="flex items-center gap-1.5">
      <span className={`h-2 w-2 rounded-full ${dot}`} />
      <span className={`text-[12px] font-semibold ${text}`}>{children}</span>
    </span>
  );
}
