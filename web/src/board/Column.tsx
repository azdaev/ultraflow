import { AnimatePresence } from "motion/react";
import type { Project, Task } from "../api";
import { projectMap } from "../util";
import { ContextMenu, useContextMenu } from "../components/ContextMenu";
import { Card, type CardEnter } from "./Card";
import { DotsIcon, PlusIcon } from "./icons";

// ColumnKind carries a column's fixed identity: its label and the semantic colour
// of its header dot + count pill. One entry per pipeline stage.
export interface ColumnKind {
  label: string;
  dot: string; // CSS color (token var) for the header dot
  pillBg: string; // CSS color (token var) for the count pill fill
  pillText: string; // CSS color (token var) for the count pill text
}

// Colors are token vars so the header dot + count pill flip with the theme.
export const COLUMNS: Record<"backlog" | "running" | "review" | "done", ColumnKind> = {
  backlog: { label: "Backlog", dot: "var(--color-muted)", pillBg: "var(--color-hairline)", pillText: "var(--color-muted)" },
  running: { label: "Running", dot: "var(--color-steel)", pillBg: "var(--color-steel-tint)", pillText: "var(--color-steel)" },
  review: { label: "Review", dot: "var(--color-nearcap)", pillBg: "var(--color-amber-tint)", pillText: "var(--color-nearcap)" },
  done: { label: "Done", dot: "var(--color-moss)", pillBg: "var(--color-moss-pill)", pillText: "var(--color-moss)" },
};

interface Props {
  kind: ColumnKind;
  tasks: Task[];
  now: number;
  activity: Record<string, string>;
  activityKind: Record<string, string>;
  context: Record<string, number>;
  contextCap: number;
  models: Record<string, string>;
  projects: Project[];
  onOpen: (taskId: string) => void;
  onAddTask?: () => void; // backlog only — the dashed "Add task" row
  onClear?: () => void; // done only — the header ⋯ menu's "Clear done" action
  enterOf?: (id: string) => CardEnter; // how each card animates in (new / moved-direction / still)
}

// Column is one pipeline stage: a coloured header (dot + caps label + count),
// then its cards, with the backlog column capped by the "Add task" row.
export function Column({ kind, tasks, now, activity, activityKind, context, contextCap, models, projects, onOpen, onAddTask, onClear, enterOf }: Props) {
  const pm = projectMap(projects);
  const menu = useContextMenu();
  // The ⋯ affordance is only shown where it actually does something (Done, with
  // cards to clear) — an inert menu button on every column would be a dead button.
  const canClear = !!onClear && tasks.length > 0;
  return (
    <div className="flex min-w-0 flex-col gap-2.25">
      <div className="flex flex-col gap-2.25 pb-3">
        <div className="flex items-center gap-2 px-0.5">
          <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: kind.dot }} />
          <span className="eyebrow shrink-0 text-[11px] leading-[14px]">{kind.label}</span>
          <span
            className="flex h-[17px] min-w-[19px] shrink-0 items-center justify-center rounded-full px-1.5 font-mono text-[10px] font-semibold leading-3"
            style={{ backgroundColor: kind.pillBg, color: kind.pillText }}
          >
            {tasks.length}
          </span>
          <span className="grow basis-0" />
          {canClear && (
            <button
              onClick={menu.openMenu}
              aria-label={`${kind.label} column actions`}
              className="grid size-5 shrink-0 place-items-center rounded-md text-faint transition hover:bg-board hover:text-muted"
            >
              <DotsIcon />
            </button>
          )}
        </div>
        <div className="h-px w-full shrink-0 bg-hairline" />
      </div>

      <div className="flex flex-col gap-2.5">
        <AnimatePresence initial={false}>
          {tasks.map((t, i) => (
            <Card
              key={t.id}
              task={t}
              activity={activity[t.id]}
              activityKind={activityKind[t.id]}
              contextTokens={context[t.id]}
              contextCap={contextCap}
              model={models[t.id]}
              now={now}
              index={i}
              enter={enterOf ? enterOf(t.id) : "new"}
              project={pm.get(t.project)}
              onOpen={onOpen}
            />
          ))}
        </AnimatePresence>

        {onAddTask && (
          <button
            onClick={onAddTask}
            className="flex w-full items-center gap-1.75 rounded-xl border border-dashed border-hairline px-3 py-2.5 text-faint transition hover:border-ink/30 hover:text-muted"
          >
            <PlusIcon />
            <span className="text-[12.5px] font-medium leading-4">Add task</span>
          </button>
        )}
      </div>

      {canClear && (
        <ContextMenu menu={menu} items={[{ label: "Clear done", danger: true, onSelect: () => onClear?.() }]} />
      )}
    </div>
  );
}
