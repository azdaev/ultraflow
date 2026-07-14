import { useState } from "react";
import { motion } from "motion/react";
import { api, errMsg, type HumanRequest, type Task } from "../api";
import type { AttentionItem } from "../useNotifications";
import { ago, copyText } from "../util";
import { AnswerBox } from "./AnswerBox";
import { ContextMenu, useContextMenu, type MenuItem } from "./ContextMenu";

interface Props {
  item: AttentionItem;
  now: number;
  onOpen: (taskId: string) => void;
}

// RailCard is one boxed card in the "Needs you" grid. Every variant is a self-
// contained white card that reads top-down: a muted meta line (badge · which task ·
// how long), then the ONE thing that matters (the question, or the failure), then
// its action. A tinted border + soft shadow carries the state colour so the body
// stays calm. Cards size to their own content and sit in equal-width grid tracks,
// so a long question never stretches its neighbours. Right-click mirrors the
// primary action so it's reachable without hunting buttons.
export function RailCard({ item, now, onOpen }: Props) {
  const menu = useContextMenu();
  const taskId = item.type === "needs_human" ? item.request.taskId : item.task.id;

  const menuItems: MenuItem[] = [{ label: "Open thread", onSelect: () => onOpen(taskId) }];
  if (item.type === "merge_failed") {
    menuItems.push({ label: "Try merge again", onSelect: () => api.merge(taskId).catch(() => {}) });
  } else if (item.type === "failed") {
    menuItems.push({ label: "Retry", onSelect: () => api.retry(taskId).catch(() => {}) });
  }
  menuItems.push({ separator: true }, { label: "Copy task ID", onSelect: () => copyText(taskId) });
  if (item.type === "failed") {
    // A gave-up task lingers in the rail; let the human dismiss it for good.
    menuItems.push({ separator: true }, { label: "Remove task", danger: true, onSelect: () => api.remove(taskId).catch(() => {}) });
  }

  const failed = item.type !== "needs_human";

  return (
    <motion.div
      layout
      onContextMenu={menu.openMenu}
      initial={{ opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, y: -4 }}
      transition={{ type: "spring", stiffness: 360, damping: 32 }}
      className={`flex min-w-0 flex-col gap-2.5 rounded-[13px] border bg-surface px-4 py-3.5 ${
        failed
          ? "border-[#eaa99a] [box-shadow:#a9432b1a_0_6px_18px,#17171a0d_0_1px_4px]"
          : "border-accent-line [box-shadow:#f5501f1f_0_6px_18px,#17171a0d_0_1px_4px]"
      }`}
    >
      {item.type === "needs_human" ? (
        <NeedsHumanRow request={item.request} task={item.task} now={now} onOpen={onOpen} />
      ) : (
        <FailedRow item={item} now={now} onOpen={onOpen} />
      )}
      <ContextMenu menu={menu} items={menuItems} />
    </motion.div>
  );
}

// The muted top line shared by every card: badge · which task · how long it's waited.
// It's deliberately quiet — the state colour lives in the badge and border; the body
// below is the hero. The title opens the thread.
function MetaLine({
  badge,
  badgeClass,
  title,
  time,
  timeClass,
  onOpen,
}: {
  badge: string;
  badgeClass: string;
  title: string;
  time: string;
  timeClass?: string;
  onOpen: () => void;
}) {
  return (
    <div className="flex items-center gap-2">
      <span className={`shrink-0 rounded-md px-2 py-0.5 text-[11px]/[14px] font-semibold ${badgeClass}`}>{badge}</span>
      <button onClick={onOpen} className="min-w-0 truncate text-left text-[12px]/[16px] text-faint hover:text-ink">
        {title}
      </button>
      <span className="grow basis-0" />
      <span className={`shrink-0 font-mono text-[12px]/[16px] font-medium ${timeClass ?? "text-faint"}`}>{time}</span>
    </div>
  );
}

function NeedsHumanRow({
  request,
  task,
  now,
  onOpen,
}: {
  request: HumanRequest;
  task?: Task;
  now: number;
  onOpen: (id: string) => void;
}) {
  const shots = request.shots ?? [];
  const files = request.files ?? [];
  // Screenshots mean a genuine *visual* review that can't be judged from a rail
  // card, so this variant routes to the full page instead of answering inline. A
  // plain diff (added/removed, captured for nearly every ask) answers fine inline.
  const isVisual = shots.length > 0;

  return (
    <>
      <MetaLine
        badge={isVisual ? "Visual" : "Checkpoint"}
        badgeClass="bg-accent-tint text-accent"
        title={task?.title ?? "task"}
        time={ago(request.createdAt, now)}
        timeClass="text-[#c98a6e]"
        onOpen={() => onOpen(request.taskId)}
      />

      {/* The hero: full card width so it wraps and stays legible instead of squishing. */}
      <p className="text-[15.5px]/[21px] font-semibold tracking-[-0.01em] text-ink">{request.question}</p>

      {isVisual ? (
        <>
          {(request.added > 0 || request.removed > 0 || shots.length > 0) && (
            <div className="flex items-center gap-2 font-mono text-[12px]/[16px]">
              {request.added > 0 && <span className="text-moss">+{request.added}</span>}
              {request.removed > 0 && <span className="text-diff-minus">−{request.removed}</span>}
              <span className="font-sans text-faint">
                {shots.length > 0
                  ? `${shots.length} shot${shots.length === 1 ? "" : "s"} to review`
                  : `${files.length} file${files.length === 1 ? "" : "s"} changed`}
              </span>
            </div>
          )}
          <button
            onClick={() => onOpen(request.taskId)}
            className="mt-0.5 inline-flex h-9 items-center justify-center gap-1.75 rounded-lg bg-accent text-[13px] font-semibold text-white transition hover:brightness-105"
          >
            <EyeIcon />
            Review the full page
          </button>
        </>
      ) : (
        <>
          {request.context && (
            <p className="line-clamp-2 text-[12.5px]/[17px] text-muted">{request.context}</p>
          )}
          <AnswerBox request={request} slim />
        </>
      )}
    </>
  );
}

// FailedRow covers a gave-up task (retry) and a merge that couldn't land (try
// again). The error is reference, not the hero: clamped to two lines in a quiet
// mono box, with the full log one click away.
function FailedRow({
  item,
  now,
  onOpen,
}: {
  item: Extract<AttentionItem, { type: "failed" | "merge_failed" }>;
  now: number;
  onOpen: (id: string) => void;
}) {
  const merge = item.type === "merge_failed";
  const task = item.task;
  const error =
    (merge ? item.message : item.activity) ||
    (merge ? "Merge couldn't land — your repo was left clean." : "Self-heal exhausted — the agent couldn't recover.");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function act() {
    if (busy) return;
    setBusy(true);
    setErr(null);
    try {
      await (merge ? api.merge(task.id) : api.retry(task.id));
    } catch (e) {
      setErr(errMsg(e, merge ? "merge failed" : "retry failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <MetaLine
        badge={merge ? "Merge" : "Failed"}
        badgeClass="bg-rust-tint text-rust"
        title={task.title}
        time={ago(task.updatedAt, now)}
        onOpen={() => onOpen(task.id)}
      />

      <p className="text-[15.5px]/[20px] font-semibold tracking-[-0.01em] text-ink">
        {merge ? "Merge couldn't land" : "Agent stopped — it gave up"}
      </p>

      <div className="rounded-md bg-rust-tint px-2.5 py-1.5">
        <p className="line-clamp-2 break-words font-mono text-[11px]/[16px] text-rust" title={error}>
          {error}
        </p>
      </div>

      <div className="mt-0.5 flex items-center gap-2">
        <button
          onClick={act}
          disabled={busy}
          className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-ink px-3.5 text-[13px] font-semibold text-surface transition hover:bg-ink/85 disabled:opacity-50"
        >
          <RetryIcon />
          {busy ? (merge ? "Merging…" : "Re-queuing…") : merge ? "Try merge again" : "Retry"}
        </button>
        <button
          onClick={() => onOpen(task.id)}
          className="inline-flex h-9 items-center rounded-lg border border-hairline bg-surface px-3.5 text-[13px] font-medium text-ink transition hover:border-ink/30"
        >
          View log
        </button>
        {err && <span className="text-[12px] text-rust">{err}</span>}
      </div>
    </>
  );
}

function EyeIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" xmlns="http://www.w3.org/2000/svg" className="shrink-0">
      <path d="M1.5 8s2.5-4.5 6.5-4.5S14.5 8 14.5 8s-2.5 4.5-6.5 4.5S1.5 8 1.5 8z" fill="none" stroke="#FFFFFF" strokeWidth="1.4" />
      <circle cx="8" cy="8" r="1.9" fill="#FFFFFF" />
    </svg>
  );
}

function RetryIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 16 16" xmlns="http://www.w3.org/2000/svg" className="shrink-0">
      <path d="M13 8a5 5 0 1 1-1.5-3.5M13 2v3h-3" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
