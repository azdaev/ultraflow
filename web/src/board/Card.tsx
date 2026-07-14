import { motion } from "motion/react";
import { api, type Project, type Task } from "../api";
import {
  agentColor,
  agentLabel,
  friendlyModel,
  ago,
  CANCELLABLE,
  CLOSED,
  copyText,
  DELETABLE,
  DEV_LINK_STATUSES,
  elapsed,
  flowOf,
} from "../util";
import { useRun } from "../runsContext";
import { ContextMenu, useContextMenu, type MenuItem } from "../components/ContextMenu";
import { FlowStepper } from "../components/FlowStepper";
import { AcceptAction } from "../components/ReviewActions";
import { AgentMark, CheckCircleIcon, ClockIcon, PromptIcon } from "./icons";

// The meter's fallback scale when no context budget is set: the model window, a
// neutral reference for how full the context is. With a budget configured the
// meter scales to that instead, so "near cap" lines up with the real /compact point.
const CONTEXT_WINDOW = 200_000;
// At/above this fraction the meter reads "near cap" and turns amber-rust — close
// to the point where the daemon's optional /compact would kick in.
const NEAR_CAP = 0.88;

interface Props {
  task: Task;
  activity?: string;
  activityKind?: string;
  now: number;
  contextTokens?: number;
  contextCap?: number; // configured budget (0/undefined = off → meter uses the model window)
  model?: string;
  project?: Project;
  onOpen: (taskId: string) => void;
  index?: number; // position in its column, for a subtle mount stagger
}

// Card is the single task card, its layout varying by status. Backlog cards are
// quiet (waiting), running cards carry a live activity strip + context meter,
// review cards carry the merge/approve action + diff counts, and done cards are
// muted. Every field maps to real board state — nothing here is decorative.
export function Card({ task, activity, activityKind, now, contextTokens, contextCap, model, project, onOpen, index = 0 }: Props) {
  const run = useRun(task.id);
  const status = task.status;
  const needsHuman = status === "needs_human";
  const closed = CLOSED.has(status);
  const isRunning = status === "running" || status === "needs_human" || status === "merging";
  const isReview = status === "review";
  const showMeter = (isRunning || isReview) && contextTokens != null && contextTokens > 0;
  // Multi-step flows show their live position right on the card (solo has one step,
  // so nothing to track). Only while in-flight/review — a quiet backlog card
  // doesn't need a not-started stepper.
  const showStepper = (isRunning || isReview) && flowOf(task.flow).steps.length > 1;
  const menu = useContextMenu();

  // Right-click actions mirror the card's primary controls, keyed off status, so
  // they're reachable without aiming at the small inline buttons. Ported verbatim
  // from the previous TaskCard so behaviour is unchanged.
  const items: MenuItem[] = [{ label: "Open details", onSelect: () => onOpen(task.id) }];
  if (isReview) {
    // Mirror the accept button: only a merge outcome (or a legacy worktree task
    // with no declared outcome) lands a branch; everything else just closes.
    const lands = task.outcome === "merge" || (!task.outcome && !!task.worktree);
    items.push(
      lands
        ? { label: "Merge → done", onSelect: () => api.merge(task.id).catch(() => {}) }
        : { label: "Mark done", onSelect: () => api.markDone(task.id).catch(() => {}) },
    );
  }
  if (status === "failed") {
    items.push({ label: "Retry", onSelect: () => api.retry(task.id).catch(() => {}) });
  }
  if (CANCELLABLE.has(status)) {
    items.push({ label: "Stop task", danger: true, onSelect: () => api.cancel(task.id).catch(() => {}) });
  }
  items.push({ separator: true });
  items.push({ label: "Copy task ID", onSelect: () => copyText(task.id) });
  if (task.worktree) {
    items.push({ label: "Copy worktree path", onSelect: () => copyText(task.worktree) });
  }
  if (DELETABLE.has(status)) {
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
        transition={{ type: "spring", stiffness: 320, damping: 30, delay: Math.min(index * 0.03, 0.18) }}
        className={`flex w-full flex-col gap-2.25 rounded-xl border-[0.75px] p-3 text-left transition ${
          closed
            ? "border-cardline bg-chip"
            : needsHuman
              ? "border-accent-line bg-surface hover:border-accent/40"
              : "border-hairline bg-surface hover:border-ink/20"
        }`}
      >
        <StatusRow task={task} now={now} />

        <h3
          className={`line-clamp-2 text-[13.5px] font-semibold leading-[135%] ${
            closed ? "font-medium text-muted" : "text-ink"
          }`}
        >
          {task.title}
        </h3>

        {task.project && !closed && <ProjectChip name={task.project} color={project?.color} />}

        {isRunning && activity && <ActivityStrip text={activity} kind={activityKind} />}

        {task.port > 0 && DEV_LINK_STATUSES.has(status) && <DevServerLink port={task.port} />}

        {isReview && <AcceptAction task={task} note={activity} />}

        {showStepper && <FlowStepper flow={task.flow} status={status} run={run} />}

        <div className="h-[0.75px] w-full shrink-0 bg-cardline" />

        <AgentFooter agent={run?.agent ?? task.agent} model={model} flow={task.flow} closed={closed} />

        {showMeter && <ContextMeter tokens={contextTokens} cap={contextCap ?? 0} />}
      </motion.button>
      <ContextMenu menu={menu} items={items} />
    </>
  );
}

// --- status row (top line) ---

function StatusRow({ task, now }: { task: Task; now: number }) {
  const s = task.status;

  // Backlog family: clock + "waiting" label on the left, age on the right.
  if (s === "backlog" || s === "queued") {
    return (
      <Row right={ago(task.createdAt, now)}>
        <span className="flex items-center gap-1.25 text-faint">
          <ClockIcon />
          <span className="text-[11px] leading-[14px]">Waiting for a slot</span>
        </span>
      </Row>
    );
  }

  // Closed family: ringed check (done) or muted dot (stopped) + label + "N ago".
  if (s === "done") {
    return (
      <Row right={ago(task.updatedAt, now)} rightMuted>
        <span className="flex items-center gap-1.25 text-muted">
          <CheckCircleIcon className="text-moss" />
          <span className="text-[11px] font-medium leading-[14px]">Done</span>
        </span>
      </Row>
    );
  }
  if (s === "cancelled") {
    return (
      <Row right={ago(task.updatedAt, now)} rightMuted>
        <span className="flex items-center gap-1.5 text-faint">
          <span className="size-2 rounded-full bg-faint" />
          <span className="text-[11px] font-medium leading-[14px]">Stopped</span>
        </span>
      </Row>
    );
  }
  if (s === "failed") {
    return (
      <Row right={elapsed(task.updatedAt, now)}>
        <span className="flex items-center gap-1.5 text-rust">
          <span className="size-2 rounded-full bg-rust" />
          <span className="text-[11px] font-semibold leading-[14px]">Failed</span>
        </span>
      </Row>
    );
  }
  if (s === "needs_human") {
    return (
      <Row right={elapsed(task.updatedAt, now)}>
        <span className="flex items-center gap-1.5 text-accent">
          <span className="size-2 rounded-full bg-accent" />
          <span className="text-[11px] font-semibold leading-[14px]">Needs you</span>
        </span>
      </Row>
    );
  }
  // Self-heal sub-state: an errored agent auto-diagnosing and retrying. It stays
  // running (never a red card) — surface "fixing itself · k/N" with a spinner.
  if (s === "running" && task.attempt > 0) {
    return (
      <Row right={elapsed(task.updatedAt, now)}>
        <span className="flex items-center gap-1.5 text-steel">
          <motion.span
            className="size-2.5 rounded-full border-[1.5px] border-steel border-t-transparent"
            animate={{ rotate: 360 }}
            transition={{ repeat: Infinity, ease: "linear", duration: 0.9 }}
          />
          <span className="text-[11px] font-semibold leading-[14px]">
            Fixing itself · {task.attempt}/{task.maxAttempts}
          </span>
        </span>
      </Row>
    );
  }
  if (s === "merging") {
    return (
      <Row right={elapsed(task.updatedAt, now)}>
        <span className="flex items-center gap-1.5 text-moss">
          <span className="size-2 rounded-full bg-moss" />
          <span className="text-[11px] font-semibold leading-[14px]">Merging</span>
        </span>
      </Row>
    );
  }
  // Plain running / review: the column names the stage, so the card just shows the
  // live timer on the left (mono, faint) — matching the design.
  return (
    <div className="flex items-center">
      <span className="font-mono text-[11px] leading-[14px] text-faint">{elapsed(task.updatedAt, now)}</span>
    </div>
  );
}

function Row({
  children,
  right,
  rightMuted,
}: {
  children: React.ReactNode;
  right: string;
  rightMuted?: boolean;
}) {
  return (
    <div className="flex items-center justify-between gap-2">
      {children}
      <span className={`shrink-0 font-mono text-[11px] leading-[14px] ${rightMuted ? "text-muted" : "text-faint"}`}>
        {right}
      </span>
    </div>
  );
}

// --- sub-parts ---

function ProjectChip({ name, color }: { name: string; color?: string }) {
  return (
    <span className="flex w-fit items-center gap-1.5 rounded-full border-[0.75px] border-chip-line bg-chip px-2 py-0.5">
      <span className="size-1.75 shrink-0 rounded-full" style={{ backgroundColor: color ?? "var(--color-faint)" }} />
      <span className="max-w-[160px] truncate text-[11px] font-medium leading-[14px] text-muted">{name}</span>
    </span>
  );
}

function ActivityStrip({ text, kind }: { text: string; kind?: string }) {
  // A stale-branch note (review) or merge failure is the exception that wants a
  // warmer tone; the ordinary running line is steel on a tinted strip.
  const warn = kind === "stale" || kind === "merge_failed";
  return (
    <div
      className={`flex w-full items-center gap-1.75 rounded-lg px-2.25 py-1.5 ${
        warn ? "bg-amber-tint" : "bg-steel-strip"
      }`}
    >
      <PromptIcon className={warn ? "text-amber" : "text-steel"} />
      <span className={`line-clamp-1 grow basis-0 font-mono text-[10.5px] leading-[14px] ${warn ? "text-amber" : "text-steel"}`}>
        {text}
      </span>
    </div>
  );
}

function AgentFooter({ agent, model, flow, closed }: { agent: string; model?: string; flow: string; closed: boolean }) {
  // Done cards fade the agent mark (design uses a muted claude tint). We derive it
  // from the agent's own colour so codex/others fade consistently too. The label
  // shows the real model the agent ran (e.g. "Opus 4.8") once detected, falling
  // back to the generic provider name until the first transcript reading.
  return (
    <div className="flex items-center justify-between gap-2">
      <span className="flex items-center gap-1.75">
        <AgentMark size={closed ? 12 : 13} color={closed ? "var(--color-faint)" : agentColor(agent)} agent={agent} />
        <span className={`text-xs leading-4 ${closed ? "text-faint" : "text-muted"}`}>{model ? friendlyModel(model) : agentLabel(agent)}</span>
      </span>
      <span className="font-mono text-[10px] leading-3 tracking-[0.04em] text-faint">
        {flowOf(flow).label.toUpperCase()}
      </span>
    </div>
  );
}

function ContextMeter({ tokens, cap }: { tokens: number; cap: number }) {
  // Scale to the configured budget when set (so the bar fills as /compact nears),
  // else the model window as a neutral reference.
  const scale = cap > 0 ? cap : CONTEXT_WINDOW;
  const pct = Math.min(1, tokens / scale);
  const near = pct >= NEAR_CAP;
  const k = Math.round(tokens / 1000);
  const scaleLabel = scale >= 1_000_000 ? `${scale / 1_000_000}M` : `${Math.round(scale / 1000)}K`;
  return (
    <div className="flex w-full flex-col gap-1.25">
      <div className="flex items-center justify-between gap-2">
        <span className="font-mono text-[9px] leading-3 tracking-[0.08em] text-faint">CONTEXT</span>
        <span className={`font-mono text-[9.5px] leading-3 ${near ? "text-nearcap" : "text-faint"}`}>
          {k}K/{scaleLabel}{near ? " · near cap" : ""}
        </span>
      </div>
      <div className="h-1 w-full shrink-0 rounded-full bg-board">
        <motion.div
          className={`h-1 rounded-full ${near ? "bg-nearcap" : "bg-steel"}`}
          initial={false}
          animate={{ width: `${Math.round(pct * 100)}%` }}
          transition={{ type: "spring", stiffness: 260, damping: 30 }}
        />
      </div>
    </div>
  );
}

// DevServerLink opens the task's live dev server (http://localhost:PORT). Like the
// actions above it renders as a role="link" span and stops propagation.
function DevServerLink({ port }: { port: number }) {
  const url = `http://localhost:${port}`;
  const open = (e: React.MouseEvent | React.KeyboardEvent) => {
    e.stopPropagation();
    window.open(url, "_blank", "noopener");
  };
  return (
    <span
      role="link"
      tabIndex={0}
      onClick={open}
      onKeyDown={(e) => {
        // preventDefault so Space activates the link instead of scrolling the page.
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          open(e);
        }
      }}
      title={`Open this task's dev server (${url})`}
      className="inline-flex w-fit items-center gap-1.5 rounded-lg border-[0.75px] border-hairline bg-board px-2.25 py-1 font-mono text-[10.5px] leading-[14px] text-steel transition hover:border-steel/40 hover:text-ink"
    >
      <span className="size-1.5 rounded-full bg-steel" />
      localhost:{port} ↗
    </span>
  );
}
