import { motion } from "motion/react";
import type { Project, Task } from "../api";
import { CheckIcon } from "./icons";

interface Props {
  projects: Project[];
  tasks: Task[];
  selected: string | null; // null = "All projects"
  onSelect: (project: string | null) => void;
  attentionCount: number; // requests needing an answer (+ failed)
  onOpenAttention: () => void;
}

// Toolbar sits under the TopBar: project filter chips on the left, a single
// attention indicator on the right. The indicator is the compact replacement for
// the old full-width attention rail — it stays calm ("Nothing needs you", moss)
// until something needs the human, then turns safety-orange and becomes the
// jump-to link into that task's detail (where the answer box lives).
export function Toolbar({ projects, tasks, selected, onSelect, attentionCount, onOpenAttention }: Props) {
  return (
    <div className="flex w-full items-center justify-between gap-3 px-5 pb-3 pt-3.5">
      <div className="flex flex-wrap items-center gap-2">
        <Chip label="All projects" active={selected === null} count={tasks.length} onClick={() => onSelect(null)} />
        {projects.map((p) => (
          <Chip
            key={p.id}
            label={p.name}
            color={p.color}
            active={selected === p.name}
            count={tasks.filter((t) => t.project === p.name).length}
            // Clicking the active chip again clears back to "All" — the expected
            // toggle-off from Linear/Notion, so the filter isn't one-way.
            onClick={() => onSelect(selected === p.name ? null : p.name)}
          />
        ))}
      </div>

      <AttentionIndicator count={attentionCount} onClick={onOpenAttention} />
    </div>
  );
}

function Chip({
  label,
  color,
  active,
  count,
  onClick,
}: {
  label: string;
  color?: string;
  active: boolean;
  count: number;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className={`flex items-center gap-1.75 rounded-full px-3 py-1.5 transition ${
        active
          ? "bg-ink"
          : "border-[0.75px] border-hairline bg-surface hover:border-ink/25"
      }`}
    >
      {color && <span className="size-1.75 shrink-0 rounded-full" style={{ backgroundColor: color }} />}
      <span className={`text-xs font-medium ${active ? "text-white" : "text-ink"}`}>{label}</span>
      <span className={`font-mono text-[11px] font-medium ${active ? "text-white/60" : "text-faint"}`}>{count}</span>
    </button>
  );
}

function AttentionIndicator({ count, onClick }: { count: number; onClick: () => void }) {
  const needsYou = count > 0;
  return (
    <motion.button
      layout
      onClick={needsYou ? onClick : undefined}
      aria-disabled={!needsYou}
      animate={{ scale: needsYou ? [1, 1.04, 1] : 1 }}
      transition={{ duration: 0.28 }}
      className={`flex items-center gap-1.75 rounded-full py-1.25 pl-2.25 pr-2.75 transition-colors ${
        needsYou
          ? "cursor-pointer bg-accent-tint hover:brightness-[0.98]"
          : "cursor-default bg-moss-pill"
      }`}
    >
      {needsYou ? (
        <span className="size-2 shrink-0 rounded-full bg-accent" />
      ) : (
        <CheckIcon className="text-moss" />
      )}
      <span className={`text-xs font-medium ${needsYou ? "text-accent" : "text-moss"}`}>
        {needsYou ? `${count} need${count === 1 ? "s" : ""} you` : "Nothing needs you"}
      </span>
    </motion.button>
  );
}
