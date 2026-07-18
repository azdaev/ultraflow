import { useEffect, useRef, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { api, type HumanRequest, type Project, type Task, type TaskEvent } from "../api";
import { agentColor, agentLabel, friendlyModel, ago, flowOf } from "../util";
import { FlowStepper } from "./FlowStepper";
import { useRun } from "../runsContext";
import { AnswerBox } from "./AnswerBox";
import { AcceptAction } from "./ReviewActions";
import { CheckpointContext } from "./CheckpointContext";
import { AgentTerminal, type AgentTerminalHandle } from "./AgentTerminal";
import { ReviewPanel } from "./ReviewPanel";
import { ReviseBox } from "./ReviseBox";
import { useBodyScrollLock } from "../useBodyScrollLock";

interface Props {
  task: Task | null;
  project?: Project; // the task's project, for its landing mode (merge vs PR)
  request?: HumanRequest;
  activitySig?: string; // changes when a new event lands → re-fetch thread
  model?: string; // real model the agent ran (e.g. "claude-opus-4-8"), if detected
  paused?: boolean; // all agents globally held: a live terminal is frozen, not working
  now: number;
  onClose: () => void;
}

// TaskDetail is a large, near-fullscreen modal: the live terminal takes most of
// the space (that IS the activity view — no duplicated tool-by-tool thread), with
// task details and the decision panel in a side rail.
export function TaskDetail({ task, project, request, activitySig, model, paused, now, onClose }: Props) {
  const run = useRun(task?.id ?? "");
  const [events, setEvents] = useState<TaskEvent[]>([]);
  const termRef = useRef<AgentTerminalHandle>(null);
  const taskId = task?.id;
  // A human gate parks the task with NO live agent — the work step's agent has
  // already exited, so there is no PTY to attach to (the server 404s the terminal
  // WS and the client loops "connection dropped, reconnecting…"). A mid-step
  // ask_human is different: that agent idles on a still-live session, so its
  // terminal stays real. `run.gate` is the signal that tells the two apart.
  const atGate = task?.status === "needs_human" && !!run?.gate;
  const gateResult = atGate
    ? request?.context.split("\n\n", 1)[0].replace(/^Latest result\s*/i, "").trim()
    : "";
  const live = task?.status === "running" || (task?.status === "needs_human" && !atGate);
  // Send-back is available whenever the agent has parked the task for a decision.
  const canRevise = task?.status === "review" || task?.status === "failed";
  const done = task?.status === "done";
  // The agent's Markdown writeup from finish_task (latest wins after a rework).
  // For a question/audit task this is the whole deliverable — there's no diff.
  const report = events.filter((e) => e.kind === "report").pop()?.data;
  // A one-line outcome for a finished task: the agent's last result summary, or
  // failing that the last status note (e.g. "merged and cleaned up the worktree"
  // / "marked done by human"). Gives a done task a human sentence even when the
  // worktree — and its diff/shots — are gone.
  const outcome =
    events.filter((e) => e.kind === "result").pop()?.data ??
    events.filter((e) => e.kind === "status").pop()?.data;
  // Review is a real screen: once the agent parks a task — at a final review OR at
  // a mid-flow gate — the big panel shows what it did (its report and, when it
  // touched a worktree, the diff) so the human can decide, instead of a dead
  // terminal. A question task has a report but no worktree; a code task may have
  // either or both. Excludes a merged `done` task torn down with no report.
  const showReview = (canRevise || atGate) && (!!task?.worktree || !!report);
  // Failures render from task.blocker (current state), never from the event log
  // (history) — an old error would otherwise linger as "why it failed" long
  // after a later turn resolved it.

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

  // Lock body scroll while the drawer is open so the board behind it can't scroll
  // through — same behavior as Modal (see useBodyScrollLock).
  useBodyScrollLock(!!task);

  return (
    <AnimatePresence>
      {task && (
        <div className="fixed inset-0 z-40 grid place-items-center p-4 sm:p-6">
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            onClick={onClose}
            className="absolute inset-0 bg-black/30 backdrop-blur-[2px]"
          />
          <motion.div
            initial={{ opacity: 0, scale: 0.97, y: 8 }}
            animate={{ opacity: 1, scale: 1, y: 0 }}
            exit={{ opacity: 0, scale: 0.98, y: 8 }}
            transition={{ type: "spring", stiffness: 320, damping: 30 }}
            className="relative flex h-[90vh] w-full max-w-[1200px] flex-col overflow-hidden rounded-2xl border border-hairline bg-board shadow-2xl"
          >
            {/* header */}
            <div className="flex items-start justify-between gap-4 border-b border-hairline bg-surface px-6 py-4">
              <div className="min-w-0">
                <p className="font-mono text-[11px] text-muted">
                  {task.id} · {task.status}
                </p>
                <h2 className="mt-1 truncate text-[19px] font-semibold leading-snug text-ink">
                  {task.title}
                </h2>
                {flowOf(task.flow).steps.length > 1 && (
                  <div className="mt-3">
                    <FlowStepper flow={task.flow} status={task.status} size="lg" run={run} />
                    {run && run.caption && (
                      <p className="mt-2 text-[12px] text-muted">{run.caption}</p>
                    )}
                  </div>
                )}
              </div>
              <button
                onClick={onClose}
                className="shrink-0 rounded-lg px-2.5 py-1 text-[13px] text-muted hover:bg-board"
              >
                Close
              </button>
            </div>

            {/* body: terminal dominates, details in a side rail */}
            <div className="flex min-h-0 flex-1">
              {/* main — the live terminal (that IS the activity view) */}
              <div className="flex min-w-0 flex-1 flex-col p-4">
                {live ? (
                  <>
                    <div className="mb-2 flex items-center justify-between gap-3">
                      <div className="flex items-center gap-2">
                        {/* Globally paused: the agent is SIGSTOP-frozen, so drop the
                            pinging "live" dot for a static amber one — an animation
                            implying active work would misrepresent a held agent. */}
                        {paused ? (
                          <span className="h-1.5 w-1.5 rounded-full bg-amber-500" />
                        ) : (
                          <span className="relative flex h-1.5 w-1.5">
                            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-moss opacity-60" />
                            <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-moss" />
                          </span>
                        )}
                        <h3 className="eyebrow text-muted">Terminal · {paused ? "paused" : "live"}</h3>
                      </div>
                      <div className="flex items-center gap-2.5">
                        {/* Interrupt sends Esc to the agent (a soft "stop the
                            current turn"), the discoverable equivalent of pressing
                            Esc while the terminal is focused — so you never need to
                            guess which key does what. */}
                        <button
                          onClick={() => termRef.current?.interrupt()}
                          title="Send Esc to the agent to stop what it's doing"
                          className="rounded-lg border border-hairline bg-surface px-2.5 py-1 text-[12px] font-medium text-ink transition hover:border-accent-line hover:text-accent"
                        >
                          Interrupt
                        </button>
                        <span className="text-[11px] leading-tight text-muted/70">
                          click in → Esc / Ctrl-C interrupt · Close to exit
                        </span>
                      </div>
                    </div>
                    {/* focus-within ring: when the terminal has focus your keys go
                        to the agent (Esc interrupts); click outside it and Esc
                        closes the card instead. The ring makes that state visible. */}
                    <div className="min-h-0 flex-1 overflow-hidden rounded-xl border border-hairline bg-terminal p-2 transition-colors focus-within:border-accent">
                      <AgentTerminal ref={termRef} taskId={task.id} />
                    </div>
                  </>
                ) : showReview ? (
                  <>
                    <div className="mb-2 flex items-center gap-2">
                      <span className="h-1.5 w-1.5 rounded-full bg-moss" />
                      <h3 className="eyebrow text-muted">
                        {atGate ? "Result being approved" : "What the agent did"}
                      </h3>
                    </div>
                    <ReviewPanel
                      taskId={task.id}
                      sig={activitySig}
                      report={report}
                      hasWorktree={!!task.worktree}
                      reportLabel={atGate ? "Result report" : "Report"}
                    />
                  </>
                ) : done ? (
                  <>
                    <div className="mb-2 flex items-center justify-between gap-3">
                      <div className="flex items-center gap-2">
                        <span className="h-1.5 w-1.5 rounded-full bg-moss" />
                        <h3 className="eyebrow text-muted">Completed</h3>
                      </div>
                      <span className="text-[11px] text-muted/70">
                        {ago(task.updatedAt, now)}
                      </span>
                    </div>
                    {outcome && (
                      <p className="mb-3 shrink-0 text-[13px] leading-relaxed text-ink/80">
                        {outcome}
                      </p>
                    )}
                    {report ? (
                      // Report-only: the merged worktree (and its diff/shots) is
                      // gone, so pass hasWorktree={false} — no Changes tab to 404.
                      <ReviewPanel
                        taskId={task.id}
                        sig={activitySig}
                        report={report}
                        hasWorktree={false}
                      />
                    ) : (
                      <div className="grid flex-1 place-items-center rounded-xl border border-dashed border-hairline text-center">
                        <p className="max-w-sm px-6 text-[13px] leading-relaxed text-muted">
                          This task finished and left no writeup. Its worktree, if
                          any, has been merged and cleaned up.
                        </p>
                      </div>
                    )}
                  </>
                ) : (
                  <div className="grid flex-1 place-items-center rounded-xl border border-dashed border-hairline text-center">
                    <div className="max-w-sm px-6">
                      <p className="text-[14px] font-medium text-ink">
                        {atGate ? "Waiting on your approval" : "No live session"}
                      </p>
                      <p className="mt-1 text-[13px] leading-relaxed text-muted">
                        {atGate ? (
                          "This step is done and has nothing to preview. Approve to continue, or send it back, in the panel on the right."
                        ) : (
                          <>
                            The terminal appears here while the agent is running.
                            This task is <span className="text-ink">{task.status}</span>.
                          </>
                        )}
                      </p>
                    </div>
                  </div>
                )}
              </div>

              {/* side rail — decision + details */}
              <aside className="w-[320px] shrink-0 overflow-y-auto border-l border-hairline bg-surface px-5 py-4">
                {request && (
                  <div className="mb-5 rounded-xl border border-accent-line bg-board p-4">
                    <div className="mb-1.5 flex items-center gap-2">
                      <span className="h-2 w-2 rounded-full bg-accent" />
                      <span className="text-[12px] font-semibold uppercase tracking-[0.08em] text-accent">
                        Needs you · waiting {ago(request.createdAt, now)}
                      </span>
                    </div>
                    <p className="text-[15px] font-semibold leading-snug text-ink">
                      {request.question}
                    </p>
                    {atGate && gateResult ? (
                      <div className="mt-3 rounded-lg bg-surface px-3 py-2.5">
                        <span className="eyebrow mb-1 block text-muted">Latest result</span>
                        <p className="text-[12.5px] leading-relaxed text-ink/80">{gateResult}</p>
                      </div>
                    ) : (
                      <div className="mt-2">
                        <CheckpointContext request={request} />
                      </div>
                    )}
                    {atGate && (
                      <p className="mt-3 rounded-lg border border-hairline bg-surface px-3 py-2 text-[12px] leading-relaxed text-ink/75">
                        <span className="font-semibold text-ink">What happens next:</span>{" "}
                        Approve moves this task to final Review without merging it. Request
                        changes or typed feedback sends it back to Build.
                      </p>
                    )}
                    <div className="mt-3">
                      <AnswerBox request={request} />
                    </div>
                  </div>
                )}

                {/* Accept the work right here — so reviewing the diff/report and
                    approving happen in one place, instead of closing the drawer to
                    reach the card's merge button. Only for review (not failed). */}
                {task.status === "review" && task.handoff && (
                  <div className="mb-5 rounded-xl border border-hairline bg-board p-4">
                    <h3 className="eyebrow mb-2 text-ink">Accept the work</h3>
                    <AcceptAction task={task} landing={project?.landing} />
                  </div>
                )}

                {task.status === "review" && !task.handoff && (
                  <div className="mb-5 rounded-xl border border-amber/35 bg-amber-tint p-4">
                    <div className="mb-1.5 flex items-center gap-2 text-amber">
                      <span className="size-2 rounded-full bg-amber" />
                      <h3 className="eyebrow">Incomplete handoff</h3>
                    </div>
                    <p className="text-[13px] leading-relaxed text-ink/75">
                      The agent stopped without submitting a report. Acceptance is
                      disabled; send it back below to complete the handoff.
                    </p>
                  </div>
                )}

                {canRevise && <ReviseBox taskId={task.id} />}

                {task.blocker && (
                  <div className="mb-5 rounded-xl border border-rust/40 bg-board p-4">
                    <h3 className="eyebrow mb-2 text-rust">
                      {task.blocker.kind === "merge"
                        ? "Merge blocked"
                        : "Why it failed"}
                    </h3>
                    <p className="font-mono text-[12px] leading-relaxed text-rust">
                      {task.blocker.detail}
                    </p>
                  </div>
                )}

                <h3 className="eyebrow mb-3 text-muted">Details</h3>
                <dl className="grid grid-cols-2 gap-y-2.5 text-[13px]">
                  <Detail label="Agent">
                    <span className="flex items-center gap-1.5">
                      <span
                        className="h-2 w-2 rounded-full"
                        style={{ backgroundColor: agentColor(task.agent) }}
                      />
                      {model ? friendlyModel(model) : agentLabel(task.agent)}
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
                  {task.port > 0 && (
                    <Detail label="Dev server" full>
                      <a
                        href={`http://localhost:${task.port}`}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="font-mono text-[12px] text-steel underline-offset-2 hover:underline"
                      >
                        http://localhost:{task.port} ↗
                      </a>
                    </Detail>
                  )}
                </dl>
                {task.body && (
                  // whitespace-pre-wrap: the body comes from a textarea, so keep the
                  // line breaks the user typed (acceptance criteria, bullet lists)
                  // instead of collapsing them into one run-on paragraph.
                  <p className="mt-3 whitespace-pre-wrap border-t border-hairline pt-3 text-[13px] leading-relaxed text-muted">
                    {task.body}
                  </p>
                )}
              </aside>
            </div>
          </motion.div>
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
