import { api, type Project, type Task } from "../api";
import { copyText } from "../util";
import { ContextMenu, useContextMenu, type MenuItem } from "./ContextMenu";
import { PipelineBoard } from "./PipelineBoard";

interface Props {
  tasks: Task[];
  activity: Record<string, string>;
  now: number;
  onOpen: (taskId: string) => void;
  projects: Project[];
  onExpandComposer?: (title: string, project: string) => void;
}

// SwimlanesBoard stacks a horizontal lane per project, each a full pipeline row
// under a lane header (swatch, name, repo path, task count). Cards carry no
// chip — the lane names the project. Tasks with no registered project fall into
// a trailing "Unassigned" lane. Best for a few projects.
export function SwimlanesBoard({ tasks, activity, now, onOpen, projects, onExpandComposer }: Props) {
  const registered = new Set(projects.map((p) => p.name));
  const orphans = tasks.filter((t) => !registered.has(t.project));

  return (
    <div className="flex flex-1 flex-col gap-7">
      {projects.map((p) => (
        <Lane
          key={p.id}
          swatch={p.color}
          name={p.name}
          repoPath={p.repoPath}
          project={p}
          tasks={tasks.filter((t) => t.project === p.name)}
          activity={activity}
          now={now}
          onOpen={onOpen}
          projects={projects}
          addProject={p.name}
          onExpandComposer={onExpandComposer}
        />
      ))}
      {orphans.length > 0 && (
        <Lane
          swatch="var(--color-muted)"
          name="Unassigned"
          repoPath="no project — add one in Settings"
          tasks={orphans}
          activity={activity}
          now={now}
          onOpen={onOpen}
          projects={projects}
          addProject=""
          onExpandComposer={onExpandComposer}
        />
      )}
      {projects.length === 0 && orphans.length === 0 && (
        <p className="text-[13px] text-muted">No projects yet.</p>
      )}
    </div>
  );
}

function Lane({
  swatch,
  name,
  repoPath,
  project,
  tasks,
  activity,
  now,
  onOpen,
  projects,
  addProject,
  onExpandComposer,
}: {
  swatch: string;
  name: string;
  repoPath: string;
  project?: Project; // absent for the trailing "Unassigned" lane
  tasks: Task[];
  activity: Record<string, string>;
  now: number;
  onOpen: (taskId: string) => void;
  projects: Project[];
  addProject: string;
  onExpandComposer?: (title: string, project: string) => void;
}) {
  const menu = useContextMenu();

  // Only real (registered) projects get a lane menu — "Unassigned" has no repo
  // to copy or registration to remove.
  const items: MenuItem[] = project
    ? [
        { label: "Copy repo path", onSelect: () => copyText(project.repoPath) },
        { separator: true },
        { label: "Remove project", danger: true, onSelect: () => api.deleteProject(project.id).catch(() => {}) },
      ]
    : [];

  return (
    <section>
      <div
        onContextMenu={project ? menu.openMenu : undefined}
        className="mb-3 flex items-baseline gap-2.5 border-b border-hairline pb-2"
      >
        <span
          className="h-3 w-3 shrink-0 translate-y-0.5 rounded-[4px]"
          style={{ backgroundColor: swatch }}
        />
        <h2 className="text-[15px] font-semibold text-ink">{name}</h2>
        <span className="truncate font-mono text-[11px] text-muted">{repoPath}</span>
        <span className="ml-auto shrink-0 font-mono text-[11px] text-muted">
          {tasks.length} {tasks.length === 1 ? "task" : "tasks"}
        </span>
        {project && <ContextMenu menu={menu} items={items} />}
      </div>
      <PipelineBoard
        tasks={tasks}
        activity={activity}
        now={now}
        onOpen={onOpen}
        projects={projects}
        compact
        addProject={addProject}
        onExpandComposer={onExpandComposer}
      />
    </section>
  );
}
