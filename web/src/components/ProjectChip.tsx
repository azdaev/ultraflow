import type { Project } from "../api";

interface Props {
  name: string;
  project?: Project; // registered project (carries the color); may be undefined
}

// ProjectChip is a small colored tag naming a task's project. In the filter
// layout every card carries one (swimlanes name the project via the lane
// header instead). Project colors are drawn from a palette distinct from the
// reserved status hues, so a chip never reads as a status.
export function ProjectChip({ name, project }: Props) {
  if (!name) return null;
  const color = project?.color ?? "var(--color-muted)";
  return (
    <span className="inline-flex items-center gap-1.5 rounded-full border border-hairline bg-board px-2 py-0.5 text-[11px] font-medium text-muted">
      <span
        className="h-2 w-2 shrink-0 rounded-full"
        style={{ backgroundColor: color }}
      />
      <span className="max-w-[120px] truncate">{name}</span>
    </span>
  );
}
