import { flowOf, activeStep } from "../util";
import type { TaskStatus } from "../api";

interface Props {
  flow: string;
  status: TaskStatus;
  size?: "sm" | "lg";
}

// FlowStepper renders the task's flow as connected segments, with the active
// step highlighted. A gate/review segment glows safety-orange ONLY when the task
// is actually parked at that human checkpoint (needs_human) — orange is reserved
// strictly for "the human is needed here"; otherwise a gate reads as neutral ink.
export function FlowStepper({ flow, status, size = "sm" }: Props) {
  const def = flowOf(flow);
  const active = activeStep(status, def.steps);
  const gap = size === "lg" ? "gap-1.5" : "gap-1";
  const needsHuman = status === "needs_human";

  return (
    <div className={`flex items-center ${gap}`} aria-label={`flow: ${def.label}`}>
      {def.steps.map((step, i) => {
        const done = i < active;
        const current = i === active;
        const isGate = step === "gate" || step === "review" || step === "visual";
        return (
          <div key={step} className="flex items-center gap-1">
            <Segment
              label={step}
              done={done}
              current={current}
              isGate={isGate}
              needsHuman={needsHuman}
              size={size}
            />
          </div>
        );
      })}
    </div>
  );
}

function Segment({
  label,
  done,
  current,
  isGate,
  needsHuman,
  size,
}: {
  label: string;
  done: boolean;
  current: boolean;
  isGate: boolean;
  needsHuman: boolean;
  size: "sm" | "lg";
}) {
  const bar =
    size === "lg" ? "h-1.5 w-10 rounded-full" : "h-1 w-6 rounded-full";
  // When a task is parked for the human, whatever step it sits on IS the live
  // gate — light it safety-orange. This is the ONLY path to orange, so it can
  // never appear on a task that isn't needs_human. Upcoming gate steps read as
  // neutral ink; agent steps as steel.
  const liveHuman = current && needsHuman;
  let color = "bg-hairline";
  if (done) color = "bg-ink/35";
  if (current) color = liveHuman ? "bg-accent" : isGate ? "bg-ink" : "bg-steel";

  const labelColor = current
    ? liveHuman
      ? "text-accent font-semibold"
      : isGate
        ? "text-ink font-semibold"
        : "text-steel font-semibold"
    : done
      ? "text-muted"
      : "text-muted/60";

  return (
    <div className="flex flex-col items-center gap-1">
      <div className={`${bar} ${color} transition-colors`} />
      {size === "lg" && (
        <span className={`text-[11px] leading-none ${labelColor}`}>{label}</span>
      )}
    </div>
  );
}
