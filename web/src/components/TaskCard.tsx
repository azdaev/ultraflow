import { motion } from "motion/react";
import type { Project, Task } from "../api";
import { agentColor, agentLabel, ago, elapsed, flowOf } from "../util";
import { FlowStepper } from "./FlowStepper";
import { ProjectChip } from "./ProjectChip";

interface Props {
  task: Task;
  activity?: string;
  now: number;
  onOpen: (taskId: string) => void;
  project?: Project; // for the chip color
  showChip?: boolean; // filter layout shows chips; swimlanes name via the lane
}

export function TaskCard({ task, activity, now, onOpen, project, showChip }: Props) {
  const needsHuman = task.status === "needs_human";

  return (
    <motion.button
      layout
      onClick={() => onOpen(task.id)}
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
          {task.status === "done" ? ago(task.updatedAt, now) : elapsed(task.updatedAt, now)}
        </span>
      </div>

      <h3
        className={`mt-2 text-[15px] font-semibold leading-snug ${
          task.status === "done" ? "text-muted" : "text-ink"
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

      {/* flow stepper */}
      <div className="mt-3">
        <FlowStepper flow={task.flow} status={task.status} />
      </div>

      {/* footer meta */}
      <div className="mt-3 flex items-center justify-between border-t border-hairline pt-2.5">
        <span className="flex items-center gap-1.5">
          <span
            className="h-2 w-2 rounded-full"
            style={{ backgroundColor: agentColor(task.agent) }}
          />
          <span className="text-[12px] text-muted">{agentLabel(task.agent)}</span>
        </span>
        <span className="font-mono text-[11px] text-muted">{flowOf(task.flow).label}</span>
      </div>
    </motion.button>
  );
}

function StatusLabel({ task, now }: { task: Task; now: number }) {
  switch (task.status) {
    case "running":
    case "planning":
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
