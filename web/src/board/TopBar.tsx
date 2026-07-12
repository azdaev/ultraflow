import { GearIcon, SearchIcon, SparkIcon } from "./icons";

interface Props {
  running: number;
  queued: number;
  onNewTask: () => void;
  onOpenSettings: () => void;
  onOpenChangelog: () => void;
}

// TopBar is the sticky board header: the wordmark, a search field that doubles as
// the "new task" entry point (⌘/`n` open the composer), the live running/queued
// counter, and the settings gear. Values wire straight to board state — no chrome.
export function TopBar({ running, queued, onNewTask, onOpenSettings, onOpenChangelog }: Props) {
  return (
    <header className="sticky top-0 z-30 flex w-full items-center gap-4 border-b-[0.75px] border-hairline bg-board/95 px-5 py-2.75 backdrop-blur-sm">
      {/* Brand wordmark (not interactive — "What's new" lives in its own button on
          the right, so the logo doesn't hijack clicks). */}
      <div className="flex grow basis-0 items-center gap-2.25">
        <span className="grid size-6 shrink-0 place-items-center rounded-[7px] bg-accent">
          <span className="size-2 rounded-[3px] bg-white" />
        </span>
        <span className="text-[15px] font-bold leading-[18px] tracking-[-0.3px] text-ink">Ultraflow</span>
      </div>

      {/* Search / new-task field — a button so clicking it opens the composer. */}
      <button
        onClick={onNewTask}
        className="flex w-110 shrink-0 items-center gap-2 rounded-[9px] border-[0.75px] border-hairline bg-surface px-3 py-2 transition hover:border-ink/25"
      >
        <SearchIcon className="text-faint" />
        <span className="grow basis-0 text-left text-[13px] text-faint">Add a task or search…</span>
        <span className="rounded-[5px] border-[0.75px] border-hairline px-1.5 py-px font-mono text-[10px] leading-3 text-faint">
          ⌘N
        </span>
      </button>

      <div className="flex grow basis-0 items-center justify-end gap-2.5">
        <div className="flex items-center gap-1.75 rounded-full border-[0.75px] border-hairline bg-surface px-2.75 py-1.25">
          <span className="size-1.5 shrink-0 rounded-full bg-steel" />
          <span className="font-mono text-[11px] font-medium leading-[14px] text-ink">{running} running</span>
          <span className="font-mono text-[11px] leading-[14px] text-faint">· {queued} queued</span>
        </div>
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
