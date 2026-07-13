import { useEffect, useState } from "react";
import { api, errMsg } from "../api";
import { CheckIcon, MergeIcon } from "../board/icons";

// The two review accept-actions — Merge (worktree task) and Approve (ran in place)
// — shared by the board Card and the review drawer so "accept the work" is the same
// control wherever the human reviews it.

// MossAction is the shared review-action pill. It renders as a role="button" span
// (so it's valid nested inside the card's <button>) that stops click propagation so
// it doesn't also open the drawer, runs an async action with a busy state, and shows
// a trailing error. Success deliberately leaves busy set — the card transitions out
// of review, so it never re-enables. The two actions differ only in icon, label,
// what they fire, and their trailing meta.
function MossAction({
  icon,
  label,
  busyLabel,
  errFallback,
  run,
  trailing,
}: {
  icon: React.ReactNode;
  label: string;
  busyLabel: string;
  errFallback: string;
  run: () => Promise<unknown>;
  trailing?: React.ReactNode;
}) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function go(e: React.MouseEvent | React.KeyboardEvent) {
    e.stopPropagation();
    if (busy) return;
    setBusy(true);
    setErr(null);
    try {
      await run();
    } catch (e) {
      setErr(errMsg(e, errFallback));
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center gap-2.25">
        <span
          role="button"
          tabIndex={0}
          aria-disabled={busy}
          onClick={go}
          onKeyDown={(e) => {
            // preventDefault so Space runs the action instead of scrolling the page.
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              go(e);
            }
          }}
          className={`inline-flex items-center gap-1.5 rounded-lg bg-moss px-3 py-1.75 text-[12.5px] font-semibold leading-4 text-white transition hover:brightness-105 ${
            busy ? "opacity-60" : ""
          }`}
        >
          {icon}
          {busy ? busyLabel : label}
        </span>
        {trailing}
      </div>
      {err && <p className="line-clamp-2 text-[12px] text-rust">{err}</p>}
    </div>
  );
}

// MergeAction lands a reviewed task's branch. Diff counts (+added −removed) are
// fetched once so the human sees the change magnitude alongside the button.
export function MergeAction({ taskId }: { taskId: string }) {
  const [diff, setDiff] = useState<{ added: number; removed: number } | null>(null);

  useEffect(() => {
    let live = true;
    api
      .diff(taskId)
      .then((d) => live && setDiff({ added: d.added, removed: d.removed }))
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [taskId]);

  return (
    <MossAction
      icon={<MergeIcon className="text-white" />}
      label="Merge to main"
      busyLabel="Merging…"
      errFallback="merge failed"
      run={() => api.merge(taskId)}
      trailing={
        diff && (
          <span className="flex items-center gap-1.25 font-mono text-[11px] leading-[14px]">
            <span className="text-moss">+{diff.added}</span>
            <span className="text-diff-minus">−{diff.removed}</span>
          </span>
        )
      }
    />
  );
}

// ApproveAction finishes a reviewed task with no worktree to merge (ran in place).
export function ApproveAction({ taskId, note }: { taskId: string; note?: string }) {
  return (
    <MossAction
      icon={<CheckIcon className="text-white" />}
      label="Approve & close"
      busyLabel="Finishing…"
      errFallback="couldn't mark done"
      run={() => api.markDone(taskId)}
      trailing={
        <span className="text-[11px] leading-[14px] text-faint">
          {note ? `${note} · no diff` : "no diff"}
        </span>
      }
    />
  );
}
