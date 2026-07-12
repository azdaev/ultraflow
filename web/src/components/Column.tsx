import { useRef, useState } from "react";
import { AnimatePresence } from "motion/react";
import { errMsg } from "../api";
import type { Project, Task } from "../api";
import { TaskCard } from "./TaskCard";
import { ImageAttachStrip, useImageAttach, withAttachments } from "./ImageAttach";

interface Props {
  title: string;
  tasks: Task[];
  activity: Record<string, string>;
  activityKind: Record<string, string>;
  now: number;
  onOpen: (taskId: string) => void;
  accent?: "steel" | "moss" | "muted";
  projectsByName?: Map<string, Project>;
  showChip?: boolean;
  // When provided, an inline "+ Add task" affordance renders under the cards
  // (Trello-style) and quick-creates in this column's project. Absent when the
  // board has no concrete project (the "All" filter / "Unassigned" lane): the
  // add then routes through onExpand so a project gets chosen first.
  onAdd?: (title: string, body?: string) => Promise<void>;
  // Hands the current draft off to the full composer (project · flow · agent ·
  // body), carrying whatever title has been typed so far. It's the "More…"
  // affordance when quick-add exists, and the primary action when it doesn't.
  onExpand?: (title: string) => void;
  // When provided, a "Clear" action renders in the header (once there are cards)
  // — used on the Done column to archive closed tasks so it can't grow unbounded.
  onClear?: () => void;
}

const dotColor: Record<string, string> = {
  steel: "bg-steel",
  moss: "bg-moss",
  muted: "bg-muted",
};

// Column is a pure pipeline stage. Cards live directly on the concrete ground
// (no boxed column). Columns grow to fill the full width.
export function Column({
  title,
  tasks,
  activity,
  activityKind,
  now,
  onOpen,
  accent = "muted",
  projectsByName,
  showChip,
  onAdd,
  onExpand,
  onClear,
}: Props) {
  return (
    <div className="flex min-w-0 flex-1 basis-0 flex-col">
      <div className="mb-3 flex items-center gap-2 px-0.5">
        <span className={`h-2 w-2 rounded-full ${dotColor[accent]}`} />
        <h2 className="eyebrow text-ink">{title}</h2>
        <span className="font-mono text-[11px] text-muted">{tasks.length}</span>
        {onClear && tasks.length > 0 && (
          <button
            onClick={onClear}
            title="Remove all closed (done & cancelled) tasks"
            className="ml-auto rounded px-1.5 py-0.5 text-[11px] font-medium text-muted transition hover:text-rust"
          >
            Clear
          </button>
        )}
      </div>

      <div className="flex flex-col gap-2.5">
        <AnimatePresence mode="popLayout">
          {tasks.map((t) => (
            <TaskCard
              key={t.id}
              task={t}
              activity={activity[t.id]}
              activityKind={activityKind[t.id]}
              now={now}
              onOpen={onOpen}
              project={projectsByName?.get(t.project)}
              showChip={showChip}
            />
          ))}
        </AnimatePresence>
        {tasks.length === 0 && !onAdd && !onExpand && (
          <div className="rounded-xl border border-dashed border-hairline px-3 py-6 text-center text-[12px] text-muted/70">
            Nothing here
          </div>
        )}
        {(onAdd || onExpand) && <AddTask onAdd={onAdd} onExpand={onExpand} />}
      </div>
    </div>
  );
}

// AddTask is the inline "+ Add task" affordance: a subtle button that expands
// into a small draft card — a title input over a footer that pairs an "Add"
// action with a "More…" hand-off to the full composer. Enter (or Add) creates
// via onAdd, Esc cancels, and after a successful create the input stays focused
// so several can be added in a row. "More…" carries the typed title into the
// composer instead of throwing the draft away. When onAdd is absent (no project
// to attach), the primary action hands off to the composer so a project is
// chosen before the task exists.
function AddTask({
  onAdd,
  onExpand,
}: {
  onAdd?: (title: string, body?: string) => Promise<void>;
  onExpand?: (title: string) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const attach = useImageAttach();

  function cancel() {
    setEditing(false);
    setTitle("");
    attach.clear();
    setErr(null);
  }

  async function submit() {
    const t = title.trim();
    // attach.busy: an image is still uploading — block so its path isn't dropped.
    if (!t || busy || attach.busy) return;
    // No quick-create here (no project to attach): hand the draft to the
    // composer so a project is chosen before the task is created.
    if (!onAdd) {
      expand();
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      // Attachments ride along as the task body (their on-disk paths), so an
      // image dropped into the quick-add reaches the agent's Read tool.
      await onAdd(t, withAttachments("", attach.attachments));
      setTitle("");
      attach.clear();
      inputRef.current?.focus();
    } catch (e) {
      setErr(errMsg(e, "failed to add task"));
    } finally {
      setBusy(false);
    }
  }

  function expand() {
    onExpand?.(title.trim());
    cancel();
  }

  if (!editing) {
    return (
      <button
        onClick={() => setEditing(true)}
        className="flex items-center gap-1.5 rounded-xl border border-dashed border-hairline px-3 py-2 text-left text-[13px] text-muted/80 transition hover:border-ink/25 hover:text-ink"
      >
        <span className="text-[15px] leading-none">+</span> Add task
      </button>
    );
  }

  return (
    // Blur of the whole card cancels (a click outside dismisses), but not while
    // an action inside runs or focus moves to another control in the card — a
    // relatedTarget still inside means the user hit More/Add, not "away".
    <div
      className="rounded-xl border border-hairline bg-surface p-2 shadow-[0_1px_2px_rgba(23,23,26,0.04)] transition focus-within:border-ink/30"
      onBlur={(e) => {
        if (busy || attach.busy) return;
        if (e.currentTarget.contains(e.relatedTarget as Node)) return;
        cancel();
      }}
    >
      <input
        ref={inputRef}
        autoFocus
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        {...attach.pasteProps}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            submit();
          } else if (e.key === "Escape") {
            e.preventDefault();
            cancel();
          }
        }}
        placeholder="What should the agent do?"
        className="w-full rounded-lg bg-transparent px-1.5 py-1 text-[13px] outline-none placeholder:text-muted/50"
      />
      {/* Inline image attach only when there's a project to attach to (onAdd);
          without one the card hands off to the composer, which has its own. */}
      {onAdd && (
        <div className="px-1.5">
          <ImageAttachStrip attach={attach} compact />
        </div>
      )}
      {err && <p className="mt-1 px-1.5 text-[11px] text-rust">{err}</p>}
      <div className="mt-1.5 flex items-center justify-between gap-2 border-t border-hairline pt-2">
        {/* "More…" only when quick-add exists; without it the primary button is
            already the composer hand-off, so a second one would be redundant. */}
        {onAdd && onExpand ? (
          <button
            onMouseDown={(e) => e.preventDefault()}
            onClick={expand}
            className="rounded-lg px-2 py-1 text-[12px] font-medium text-muted transition hover:bg-board hover:text-ink"
          >
            More…
          </button>
        ) : (
          <span />
        )}
        <button
          onMouseDown={(e) => e.preventDefault()}
          onClick={submit}
          disabled={busy || attach.busy || !title.trim()}
          className="rounded-lg bg-ink px-3 py-1 text-[12px] font-semibold text-white transition hover:brightness-110 disabled:opacity-40"
        >
          {busy ? "Adding…" : onAdd ? "Add" : "Add…"}
        </button>
      </div>
    </div>
  );
}
