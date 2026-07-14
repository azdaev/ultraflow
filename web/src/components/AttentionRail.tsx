import { AnimatePresence } from "motion/react";
import type { AttentionItem } from "../useNotifications";
import { ago } from "../util";
import { RailCard } from "./RailCard";

interface Props {
  items: AttentionItem[];
  now: number;
  onOpen: (taskId: string) => void;
}

// AttentionRail is the "Needs you" band under the toolbar — the one place to look
// when something is waiting on the human. It restores inline answering: an
// ask_human checkpoint is read and answered right here without opening the card.
// It renders ONLY when something is waiting; the calm state is the toolbar pill.
//
// Cards live in an equal-width grid that fills the row and wraps to more rows as
// items pile up, so each card stays compact instead of a lone question stretching
// full width. The grid is height-capped and scrolls internally so a big pile never
// pushes the board off-screen.
export function AttentionRail({ items, now, onOpen }: Props) {
  if (items.length === 0) return null;

  // "oldest waiting" reads off the earliest item so the human knows how long an
  // agent has been parked. ISO timestamps sort lexically, so min() is the oldest.
  const oldest = items
    .map((i) => (i.type === "needs_human" ? i.request.createdAt : i.task.updatedAt))
    .reduce((a, b) => (a < b ? a : b));

  return (
    <section className="flex w-full shrink-0 flex-col gap-3.5 border-b border-attention-line bg-attention-ground px-7 pb-5.5 pt-4.5">
      <div className="flex items-center gap-2.5">
        <span className="size-2 shrink-0 rounded-full bg-accent" />
        <h2 className="text-[15px]/[18px] font-bold tracking-[-0.01em] text-ink">Needs you</h2>
        <span className="rounded-full bg-accent px-1.75 py-px font-mono text-[11px]/[14px] font-semibold text-white">
          {items.length}
        </span>
        <span className="grow basis-0" />
        <span className="hidden text-[12px]/[16px] text-[#9a8a80] sm:inline">
          oldest waiting {ago(oldest, now)} · answer to unblock an agent
        </span>
      </div>

      <div className="grid max-h-[min(48vh,540px)] grid-cols-[repeat(auto-fill,minmax(340px,1fr))] gap-3.5 overflow-y-auto py-0.5">
        <AnimatePresence mode="popLayout" initial={false}>
          {items.map((item) => (
            <RailCard
              key={item.type === "needs_human" ? item.request.id : `${item.type}:${item.task.id}`}
              item={item}
              now={now}
              onOpen={onOpen}
            />
          ))}
        </AnimatePresence>
      </div>
    </section>
  );
}
