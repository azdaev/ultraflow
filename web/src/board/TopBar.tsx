import { useEffect, useRef } from "react";
import { GearIcon, PauseIcon, PlayIcon, SearchIcon, SparkIcon } from "./icons";

interface Props {
  running: number;
  queued: number;
  query: string;
  onSearch: (q: string) => void;
  onSubmit: () => void; // Enter in the field: open the sole match, or create when none
  paused: boolean;
  onTogglePause: () => void;
  onNewTask: () => void;
  onOpenSettings: () => void;
  onOpenChangelog: () => void;
}

// TopBar is the sticky board header: the wordmark, a command-bar search field
// (filters live; Enter opens the one match or creates a task when none, with the
// trailing "N" pill as a real new-task button), the running/queued counter, a
// global pause-all toggle, and the settings gear.
export function TopBar({ running, queued, query, onSearch, onSubmit, paused, onTogglePause, onNewTask, onOpenSettings, onOpenChangelog }: Props) {
  const searchRef = useRef<HTMLInputElement>(null);

  // "/" jumps to search (the GitHub/Linear/Slack convention), the keyboard
  // counterpart to "n" for a new task. Skipped while a field is focused so the
  // slash still types normally into a text input.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey) return;
      const el = e.target as HTMLElement | null;
      if (el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.isContentEditable)) return;
      e.preventDefault();
      searchRef.current?.focus();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <header className="sticky top-0 z-30 flex w-full items-center gap-4 border-b-[0.75px] border-hairline bg-board/95 px-5 py-2.75 backdrop-blur-sm">
      {/* Brand wordmark (not interactive — "What's new" lives in its own button on
          the right, so the logo doesn't hijack clicks). */}
      <div className="flex grow basis-0 items-center gap-2.25">
        <img src="/logo.png" alt="Ultraflow" className="h-[22px] w-auto shrink-0" />
        <span className="text-[15px] font-bold leading-[18px] tracking-[-0.3px] text-ink">Ultraflow</span>
      </div>

      {/* Command-bar search field: filters live, Enter acts on the query, and the
          trailing "N" pill is a real new-task button — one control for find + add. */}
      <div className="flex w-110 shrink-0 items-center gap-2 rounded-[9px] border-[0.75px] border-hairline bg-surface px-3 py-2 transition focus-within:border-ink/25">
        <SearchIcon className="text-faint" />
        <input
          ref={searchRef}
          value={query}
          onChange={(e) => onSearch(e.target.value)}
          onKeyDown={(e) => {
            // Enter acts on the query (open the sole match, or create when none);
            // Escape clears it and drops focus back to the board, so it reads as
            // "leave search" rather than just emptying the field.
            if (e.key === "Enter") {
              onSubmit();
            } else if (e.key === "Escape") {
              onSearch("");
              e.currentTarget.blur();
            }
          }}
          placeholder="Add a task or search…"
          aria-label="Search tasks"
          className="grow basis-0 bg-transparent text-[13px] text-ink outline-none placeholder:text-faint"
        />
        {query ? (
          <button
            onClick={() => onSearch("")}
            aria-label="Clear search"
            className="shrink-0 rounded-[5px] border-[0.75px] border-hairline px-1.5 py-px font-mono text-[10px] leading-3 text-faint transition hover:border-ink/25 hover:text-muted"
          >
            Esc
          </button>
        ) : (
          <button
            onClick={onNewTask}
            title="New task (press N)"
            className="shrink-0 rounded-[5px] border-[0.75px] border-hairline px-1.5 py-px font-mono text-[10px] leading-3 text-faint transition hover:border-ink/25 hover:text-muted"
          >
            N
          </button>
        )}
      </div>

      <div className="flex grow basis-0 items-center justify-end gap-2.5">
        <div className="flex items-center gap-1.75 rounded-full border-[0.75px] border-hairline bg-surface px-2.75 py-1.25">
          <span className={`size-1.5 shrink-0 rounded-full ${paused ? "bg-amber-500" : "bg-steel"}`} />
          {paused ? (
            <span className="font-mono text-[11px] font-medium leading-[14px] text-amber-600">paused</span>
          ) : (
            <>
              <span className="font-mono text-[11px] font-medium leading-[14px] text-ink">{running} running</span>
              <span className="font-mono text-[11px] leading-[14px] text-faint">· {queued} queued</span>
            </>
          )}
        </div>
        <button
          onClick={onTogglePause}
          title={paused ? "Resume all agents" : "Pause all agents"}
          className={`grid size-8 shrink-0 place-items-center rounded-[9px] border-[0.75px] transition ${
            paused
              ? "border-amber-500/40 bg-amber-500/15 text-amber-600 hover:bg-amber-500/25"
              : "border-hairline bg-surface text-muted hover:border-ink/25 hover:text-ink"
          }`}
        >
          {paused ? <PlayIcon /> : <PauseIcon />}
        </button>
        <button
          onClick={onOpenChangelog}
          title="What's new"
          className="grid size-8 shrink-0 place-items-center rounded-[9px] border-[0.75px] border-hairline bg-surface text-muted transition hover:border-ink/25 hover:text-ink"
        >
          <SparkIcon />
        </button>
        <button
          onClick={onOpenSettings}
          title="Settings"
          className="grid size-8 shrink-0 place-items-center rounded-[9px] border-[0.75px] border-hairline bg-surface text-muted transition hover:border-ink/25 hover:text-ink"
        >
          <GearIcon />
        </button>
      </div>
    </header>
  );
}
