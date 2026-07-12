import { useState } from "react";
import { motion } from "motion/react";
import { api, type HumanRequest, type Task } from "../api";
import { agentLabel, ago } from "../util";
import { AnswerBox } from "./AnswerBox";

export type AttentionItem =
  | { type: "needs_human"; request: HumanRequest; task?: Task }
  | { type: "failed"; task: Task; activity?: string };

interface Props {
  item: AttentionItem;
  now: number;
  onOpen: (taskId: string) => void;
}

export function RailCard({ item, now, onOpen }: Props) {
  return (
    <motion.div
      layout
      initial={{ opacity: 0, y: 8, scale: 0.98 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      exit={{ opacity: 0, scale: 0.97 }}
      transition={{ type: "spring", stiffness: 340, damping: 30 }}
      className="w-[380px] shrink-0"
    >
      {item.type === "needs_human" ? (
        <CheckpointCard item={item} now={now} onOpen={onOpen} />
      ) : (
        <FailedCard item={item} now={now} onOpen={onOpen} />
      )}
    </motion.div>
  );
}

function CheckpointCard({
  item,
  now,
  onOpen,
}: {
  item: Extract<AttentionItem, { type: "needs_human" }>;
  now: number;
  onOpen: (id: string) => void;
}) {
  const { request, task } = item;
  return (
    <div className="flex h-full flex-col rounded-xl border border-accent-line bg-surface shadow-[0_6px_20px_-8px_rgba(245,80,30,0.35)]">
      <div className="flex items-center justify-between border-b border-accent-line/60 px-4 py-2.5">
        <span className="flex items-center gap-2">
          <span className="h-2 w-2 rounded-full bg-accent" />
          <span className="text-[12px] font-semibold uppercase tracking-[0.08em] text-accent">
            Needs you
          </span>
        </span>
        <span className="font-mono text-[11px] text-muted">
          waiting {ago(request.createdAt, now)}
        </span>
      </div>

      <div className="flex flex-1 flex-col px-4 py-3">
        <button
          onClick={() => onOpen(request.taskId)}
          className="text-left text-[12px] text-muted hover:text-ink"
        >
          {task?.title ?? "task"} · {agentLabel(task?.agent ?? "claude")}
        </button>
        <p className="mt-1 text-[16px] font-semibold leading-snug text-ink">
          {request.question}
        </p>
        {request.context && (
          <p className="mt-1.5 rounded-lg bg-board px-2.5 py-1.5 text-[12px] leading-relaxed text-muted">
            {request.context}
          </p>
        )}
        <div className="mt-auto pt-2">
          <AnswerBox request={request} />
        </div>
      </div>
    </div>
  );
}

function FailedCard({
  item,
  now,
  onOpen,
}: {
  item: Extract<AttentionItem, { type: "failed" }>;
  now: number;
  onOpen: (id: string) => void;
}) {
  const { task, activity } = item;
  const [busy, setBusy] = useState(false);

  async function retry() {
    if (busy) return;
    setBusy(true);
    try {
      await api.retry(task.id);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex h-full flex-col rounded-xl border border-rust/40 bg-surface shadow-[0_6px_20px_-10px_rgba(169,67,43,0.4)]">
      <div className="flex items-center justify-between border-b border-rust/25 px-4 py-2.5">
        <span className="flex items-center gap-2">
          <span className="h-2 w-2 rounded-full bg-rust" />
          <span className="text-[12px] font-semibold uppercase tracking-[0.08em] text-rust">
            Gave up
          </span>
        </span>
        <span className="font-mono text-[11px] text-muted">
          {ago(task.updatedAt, now)}
        </span>
      </div>

      <div className="flex flex-1 flex-col px-4 py-3">
        <button
          onClick={() => onOpen(task.id)}
          className="text-left text-[16px] font-semibold leading-snug text-ink hover:underline"
        >
          {task.title}
        </button>
        <p className="mt-1.5 rounded-lg bg-rust-tint px-2.5 py-1.5 font-mono text-[12px] leading-relaxed text-rust">
          {activity || "self-heal exhausted — the agent couldn't recover"}
        </p>
        <div className="mt-auto flex items-center gap-2 pt-3">
          <button
            onClick={retry}
            disabled={busy}
            className="rounded-lg bg-ink px-3 py-2 text-[13px] font-semibold text-white transition hover:bg-ink/85 disabled:opacity-50"
          >
            {busy ? "Re-queuing…" : "Retry"}
          </button>
          <button
            onClick={() => onOpen(task.id)}
            className="rounded-lg border border-hairline bg-surface px-3 py-2 text-[13px] font-semibold text-ink transition hover:border-ink/30"
          >
            View thread
          </button>
        </div>
      </div>
    </div>
  );
}
