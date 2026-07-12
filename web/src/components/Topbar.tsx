interface Props {
  needCount: number; // needs_human only — drives the orange pill
  failedCount: number; // gave-up failures — red, never orange
  running: number;
  queued: number;
  connected: boolean;
  onNewTask: () => void;
  onOpenSettings: () => void;
  onOpenChangelog: () => void;
}

export function Topbar({
  needCount,
  failedCount,
  running,
  queued,
  connected,
  onNewTask,
  onOpenSettings,
  onOpenChangelog,
}: Props) {
  return (
    <header className="sticky top-0 z-30 flex items-center gap-4 border-b border-hairline bg-board/85 px-6 py-3 backdrop-blur">
      {/* wordmark */}
      <div className="flex items-center gap-2">
        <span className="grid h-6 w-6 place-items-center rounded-[6px] bg-accent text-white">
          <span className="h-2 w-2 rounded-[2px] bg-white" />
        </span>
        <span className="text-[16px] font-bold tracking-tight text-ink">Ultraflow</span>
      </div>

      {/* centered quick add */}
      <button
        onClick={onNewTask}
        className="mx-auto flex w-full max-w-md items-center gap-2 rounded-lg border border-hairline bg-surface px-3 py-2 text-left text-[14px] text-muted transition hover:border-ink/30"
      >
        <span className="text-muted/70">+</span>
        <span className="flex-1">Add a task…</span>
        <kbd className="rounded border border-hairline px-1.5 py-0.5 font-mono text-[11px] text-muted">
          N
        </kbd>
      </button>

      {/* right cluster */}
      <div className="flex items-center gap-3">
        {/* orange is reserved strictly for needs_human */}
        {needCount > 0 && (
          <span className="flex items-center gap-1.5 rounded-full bg-accent px-2.5 py-1 text-[12px] font-semibold text-white">
            {needCount} need you
          </span>
        )}
        {/* failures are red, a distinct family — never the orange decision hue */}
        {failedCount > 0 && (
          <span className="flex items-center gap-1.5 rounded-full border border-rust/40 bg-rust-tint px-2.5 py-1 text-[12px] font-semibold text-rust">
            {failedCount} failed
          </span>
        )}
        <span className="flex items-center gap-1.5 rounded-full border border-hairline bg-surface px-2.5 py-1 font-mono text-[12px] text-ink">
          <span className="h-1.5 w-1.5 rounded-full bg-steel" />
          {running} run
          {queued > 0 && <span className="text-muted">· {queued} queued</span>}
        </span>
        <span
          title={connected ? "live" : "reconnecting…"}
          className={`h-2 w-2 rounded-full ${connected ? "bg-moss" : "bg-muted/50"}`}
        />
        <button
          onClick={onOpenChangelog}
          title="What's new"
          className="grid h-8 w-8 place-items-center rounded-lg border border-hairline bg-surface text-muted transition hover:border-ink/30 hover:text-ink"
        >
          <SparkIcon />
        </button>
        <button
          onClick={onOpenSettings}
          title="Settings"
          className="grid h-8 w-8 place-items-center rounded-lg border border-hairline bg-surface text-muted transition hover:border-ink/30 hover:text-ink"
        >
          <GearIcon />
        </button>
      </div>
    </header>
  );
}

function SparkIcon() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M12 3v4M12 17v4M3 12h4M17 12h4M5.6 5.6l2.8 2.8M15.6 15.6l2.8 2.8M18.4 5.6l-2.8 2.8M8.4 15.6l-2.8 2.8" />
    </svg>
  );
}

function GearIcon() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
    </svg>
  );
}
