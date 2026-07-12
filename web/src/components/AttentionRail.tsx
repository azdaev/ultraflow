import { AnimatePresence, motion } from "motion/react";
import { RailCard, type AttentionItem } from "./RailCard";

interface Props {
  items: AttentionItem[];
  now: number;
  onOpen: (taskId: string) => void;
}

// AttentionRail is the one place to look: a loud full-width band under the topbar
// unifying everything that needs the human's action — checkpoints (orange) and
// failures (red). Empty → "all caught up".
export function AttentionRail({ items, now, onOpen }: Props) {
  const count = items.length;
  // Orange is reserved strictly for needs_human. A rail holding only failures
  // must NOT tint orange or claim "need you" — failures are the red family.
  const needCount = items.filter((i) => i.type === "needs_human").length;
  const failedCount = count - needCount;

  return (
    <section
      className={`border-b px-6 py-4 ${
        needCount > 0
          ? "border-accent-line/60 bg-accent-tint/50"
          : failedCount > 0
            ? "border-rust/25 bg-rust-tint/40"
            : "border-hairline bg-board"
      }`}
    >
      <div className="mb-2.5 flex items-center gap-2">
        <h2 className="eyebrow text-ink">Attention</h2>
        {needCount > 0 && (
          <span className="rounded-full bg-accent px-2 py-0.5 font-mono text-[11px] font-semibold text-white">
            {needCount} need you
          </span>
        )}
        {failedCount > 0 && (
          <span className="rounded-full border border-rust/40 bg-rust-tint px-2 py-0.5 font-mono text-[11px] font-semibold text-rust">
            {failedCount} failed
          </span>
        )}
      </div>

      {count > 0 && (
        <div className="flex items-stretch gap-3 overflow-x-auto pb-1">
          <AnimatePresence mode="popLayout">
            {items.map((item) => (
              <RailCard
                key={item.type === "needs_human" ? item.request.id : "fail-" + item.task.id}
                item={item}
                now={now}
                onOpen={onOpen}
              />
            ))}
          </AnimatePresence>
        </div>
      )}

      {count === 0 && (
        <motion.p
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          className="text-[13px] text-muted"
        >
          No checkpoints or failures waiting. Agents are running on their own.
        </motion.p>
      )}
    </section>
  );
}
