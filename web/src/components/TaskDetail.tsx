import { useEffect, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { api, type HumanRequest, type Task, type TaskEvent } from "../api";
import { agentColor, agentLabel, ago, flowOf } from "../util";
import { FlowStepper } from "./FlowStepper";
import { AnswerBox } from "./AnswerBox";
import { AgentTerminal } from "./AgentTerminal";

interface Props {
  task: Task | null;
  request?: HumanRequest;
  activitySig?: string; // changes when a new event lands → re-fetch thread
  now: number;
  onClose: () => void;
}

// TaskDetail is the context-immersion drawer: flow stepper, the event THREAD,
// a DETAILS card, and — when the task is waiting — the live decision panel.
export function TaskDetail({ task, request, activitySig, now, onClose }: Props) {
  const [events, setEvents] = useState<TaskEvent[]>([]);
  const taskId = task?.id;

  useEffect(() => {
    if (!taskId) return;
    let live = true;
    api
      .taskEvents(taskId)
      .then((e) => live && setEvents(e))
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [taskId, activitySig]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <AnimatePresence>
      {task && (
        <div className="fixed inset-0 z-40">
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            onClick={onClose}
            className="absolute inset-0 bg-ink/20 backdrop-blur-[2px]"
          />
          <motion.aside
            initial={{ x: "100%" }}
            animate={{ x: 0 }}
            exit={{ x: "100%" }}
            transition={{ type: "spring", stiffness: 300, damping: 34 }}
            className="absolute right-0 top-0 flex h-full w-full max-w-[560px] flex-col border-l border-hairline bg-board shadow-2xl"
          >
            {/* header */}
            <div className="flex items-start justify-between gap-4 border-b border-hairline bg-surface px-5 py-4">
              <div className="min-w-0">
                <p className="font-mono text-[11px] text-muted">
                  {task.id} · {task.status}
                </p>
                <h2 className="mt-1 text-[19px] font-semibold leading-snug text-ink">
                  {task.title}
                </h2>
                <div className="mt-3">
                  <FlowStepper flow={task.flow} status={task.status} size="lg" />
                </div>
              </div>
              <button
                onClick={onClose}
                className="shrink-0 rounded-lg px-2.5 py-1 text-[13px] text-muted hover:bg-board"
              >
                Close
              </button>
            </div>

            {/* body */}
            <div className="flex-1 overflow-y-auto px-5 py-4">
              {/* live decision panel */}
              {request && (
                <div className="mb-5 rounded-xl border border-accent-line bg-surface p-4">
                  <div className="mb-1.5 flex items-center gap-2">
                    <span className="h-2 w-2 rounded-full bg-accent" />
                    <span className="text-[12px] font-semibold uppercase tracking-[0.08em] text-accent">
                      Needs you · waiting {ago(request.createdAt, now)}
                    </span>
                  </div>
                  <p className="text-[16px] font-semibold leading-snug text-ink">
                    {request.question}
                  </p>
                  {request.context && (
                    <p className="mt-1.5 rounded-lg bg-board px-2.5 py-1.5 text-[12px] leading-relaxed text-muted">
                      {request.context}
                    </p>
                  )}
                  <div className="mt-2">
                    <AnswerBox request={request} />
                  </div>
                </div>
              )}

              {/* details card */}
              <div className="mb-5 rounded-xl border border-hairline bg-surface p-4">
                <h3 className="eyebrow mb-3 text-muted">Details</h3>
                <dl className="grid grid-cols-2 gap-y-2.5 text-[13px]">
                  <Detail label="Agent">
                    <span className="flex items-center gap-1.5">
                      <span
                        className="h-2 w-2 rounded-full"
                        style={{ backgroundColor: agentColor(task.agent) }}
                      />
                      {agentLabel(task.agent)}
                    </span>
                  </Detail>
                  <Detail label="Flow">{flowOf(task.flow).label}</Detail>
                  <Detail label="Project">{task.project || "—"}</Detail>
                  <Detail label="Updated">{ago(task.updatedAt, now)}</Detail>
                  <Detail label="Worktree" full>
                    <span className="font-mono text-[12px] text-muted">
                      {task.worktree || "shared workdir (M0)"}
                    </span>
                  </Detail>
                </dl>
                {task.body && (
                  <p className="mt-3 border-t border-hairline pt-3 text-[13px] leading-relaxed text-muted">
                    {task.body}
                  </p>
                )}
              </div>

              {/* live terminal — a real interactive session while the agent runs */}
              {(task.status === "running" || task.status === "needs_human") && (
                <div className="mb-5">
                  <div className="mb-2 flex items-center gap-2">
                    <span className="relative flex h-1.5 w-1.5">
                      <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-moss opacity-60" />
                      <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-moss" />
                    </span>
                    <h3 className="eyebrow text-muted">Terminal · live — type to steer, Ctrl-C to interrupt</h3>
                  </div>
                  <div className="h-[380px] overflow-hidden rounded-xl border border-hairline bg-[#17171A] p-2">
                    <AgentTerminal taskId={task.id} />
                  </div>
                </div>
              )}

              {/* thread */}
              <h3 className="eyebrow mb-3 text-muted">Thread</h3>
              <Thread events={events} now={now} />
            </div>
          </motion.aside>
        </div>
      )}
    </AnimatePresence>
  );
}

function Detail({
  label,
  children,
  full,
}: {
  label: string;
  children: React.ReactNode;
  full?: boolean;
}) {
  return (
    <div className={full ? "col-span-2" : ""}>
      <dt className="text-[11px] uppercase tracking-[0.06em] text-muted/70">{label}</dt>
      <dd className="mt-0.5 text-ink">{children}</dd>
    </div>
  );
}

function Thread({ events, now }: { events: TaskEvent[]; now: number }) {
  if (events.length === 0) {
    return <p className="text-[13px] text-muted/70">No activity yet.</p>;
  }
  return (
    <ol className="relative ml-1 border-l border-hairline">
      {events.map((e) => (
        <li key={e.id} className="relative py-2 pl-5">
          <span
            className="absolute -left-[5px] top-3.5 h-2 w-2 rounded-full"
            style={{ backgroundColor: kindColor(e.kind) }}
          />
          <div className="flex items-baseline justify-between gap-3">
            <span className="text-[11px] font-semibold uppercase tracking-[0.06em] text-muted/70">
              {kindLabel(e.kind)}
            </span>
            <span className="shrink-0 font-mono text-[10px] text-muted/60">
              {ago(e.createdAt, now)}
            </span>
          </div>
          <p
            className={`mt-0.5 text-[13px] leading-relaxed ${
              e.kind === "tool"
                ? "font-mono text-[12px] text-muted"
                : e.kind === "error"
                  ? "font-mono text-[12px] text-rust"
                  : "text-ink"
            }`}
          >
            {e.data}
          </p>
        </li>
      ))}
    </ol>
  );
}

function kindColor(kind: string): string {
  switch (kind) {
    case "human_request":
    case "human_answer":
      return "var(--color-accent)";
    case "result":
      return "var(--color-moss)";
    case "tool":
      return "var(--color-steel)";
    case "error":
      return "var(--color-rust)";
    default:
      return "var(--color-muted)";
  }
}

function kindLabel(kind: string): string {
  switch (kind) {
    case "human_request":
      return "asked you";
    case "human_answer":
      return "you answered";
    case "tool":
      return "action";
    case "result":
      return "result";
    case "message":
      return "note";
    case "error":
      return "failed";
    default:
      return kind;
  }
}
