import { useState } from "react";
import type { Project, Task } from "../api";
import { PipelineBoard } from "./PipelineBoard";

interface Props {
  tasks: Task[];
  activity: Record<string, string>;
  now: number;
  onOpen: (taskId: string) => void;
  projects: Project[];
}

// FilterBoard is one unified pipeline with a project switcher (All · …) and a
// colored project chip on every card. Scales to many projects. The attention
// rail above stays global regardless of the filter.
export function FilterBoard({ tasks, activity, now, onOpen, projects }: Props) {
  const [selected, setSelected] = useState<string>("all"); // "all" | project name

  // If the selected project is removed in Settings, fall back to "All" rather
  // than stranding the board on a filter with no chip to clear it.
  const effective =
    selected === "all" || projects.some((p) => p.name === selected)
      ? selected
      : "all";

  const shown =
    effective === "all" ? tasks : tasks.filter((t) => t.project === effective);

  return (
    <div className="flex flex-1 flex-col">
      {projects.length > 0 && (
        <div className="mb-4 flex flex-wrap items-center gap-2">
          <SwitchChip
            label="All"
            active={effective === "all"}
            onClick={() => setSelected("all")}
            count={tasks.length}
          />
          {projects.map((p) => (
            <SwitchChip
              key={p.id}
              label={p.name}
              color={p.color}
              active={effective === p.name}
              onClick={() => setSelected(p.name)}
              count={tasks.filter((t) => t.project === p.name).length}
            />
          ))}
        </div>
      )}
      <PipelineBoard
        tasks={shown}
        activity={activity}
        now={now}
        onOpen={onOpen}
        projects={projects}
        showChip
        addProject={effective === "all" ? "" : effective}
      />
    </div>
  );
}

function SwitchChip({
  label,
  color,
  active,
  onClick,
  count,
}: {
  label: string;
  color?: string;
  active: boolean;
  onClick: () => void;
  count: number;
}) {
  return (
    <button
      onClick={onClick}
      className={`flex items-center gap-2 rounded-full border px-3 py-1.5 text-[13px] font-medium transition ${
        active
          ? "border-ink/70 bg-ink text-white"
          : "border-hairline bg-surface text-ink hover:border-ink/30"
      }`}
    >
      {color && (
        <span
          className="h-2 w-2 shrink-0 rounded-full"
          style={{ backgroundColor: color }}
        />
      )}
      {label}
      <span
        className={`font-mono text-[11px] ${active ? "text-white/70" : "text-muted"}`}
      >
        {count}
      </span>
    </button>
  );
}
