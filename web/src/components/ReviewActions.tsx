import { useEffect, useState } from "react";
import { api, errMsg, type Task } from "../api";
import { CheckIcon, MergeIcon } from "../board/icons";

// The review accept-action — one pill, shared by the board Card and the review
// drawer, whose label says the TRUE task outcome so not every finished task shows
// a scary "Merge to main". The agent declares an `outcome` at finish_task; only
// "merge" actually lands a branch (api.merge) — every other outcome just closes
// the task (api.markDone). Legacy/undeclared tasks fall back to the worktree+diff
// heuristic: a worktree that changed nothing is closed, not merged.

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
          className={`inline-flex items-center gap-1.5 rounded-lg bg-moss-solid px-3 py-1.75 text-[12.5px] font-semibold leading-4 text-white transition hover:brightness-105 ${
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

// spec is one resolved accept-action: its icon/label/wording plus what it fires.
// `merge` marks the branch-landing action (shows +added −removed); everything else
// closes the task and shows a faint note saying why there is nothing to land.
type Spec = {
  label: string;
  busyLabel: string;
  errFallback: string;
  run: (taskId: string) => Promise<unknown>;
  note?: string;
  merge?: boolean;
};

// close is the shared shape of every non-merge outcome: finish via api.markDone
// with a check icon; only the label/wording differs per outcome.
const close = (label: string, busyLabel: string, note: string): Spec => ({
  label,
  busyLabel,
  errFallback: "couldn't finish",
  run: api.markDone,
  note,
});

// OUTCOMES maps the agent's declared outcome to its accept-action. Keyed by the
// same enum finish_task accepts (see internal/mcp/server.go). An unknown/empty
// outcome isn't here — the component falls back to the worktree+diff heuristic.
const OUTCOMES: Record<string, Spec> = {
  merge: {
    label: "Merge to main",
    busyLabel: "Merging…",
    errFallback: "merge failed",
    run: api.merge,
    merge: true,
  },
  answer: close("Close · answered", "Closing…", "answered · no merge"),
  design: close("Approve design", "Approving…", "design · no merge"),
  applied: close("Mark done", "Finishing…", "already applied outside the repo"),
  none: close("Acknowledge", "Finishing…", "nothing to change"),
};

// AcceptAction is the single review accept control. It resolves a Spec from the
// task's declared outcome, falling back — when the agent declared none — to the
// old rule, but tightened: a worktree whose diff is empty is closed, not merged,
// so a question answered inside a worktree no longer shows "Merge to main".
export function AcceptAction({
  task,
  note,
  landing,
}: {
  task: Task;
  note?: string;
  landing?: "local" | "pr"; // the project's landing mode — "pr" relabels the merge
}) {
  const declared = OUTCOMES[task.outcome];
  // Legacy task with no declared outcome but a live worktree: we need the diff to
  // decide whether there is anything to land.
  const ambiguous = !declared && !!task.worktree;
  const [diff, setDiff] = useState<{ added: number; removed: number } | null>(null);
  const needDiff = (declared?.merge ?? false) || ambiguous;

  useEffect(() => {
    if (!needDiff) return;
    let live = true;
    api
      .diff(task.id)
      .then((d) => live && setDiff({ added: d.added, removed: d.removed }))
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [task.id, needDiff]);

  let spec: Spec;
  if (declared) {
    spec = declared;
  } else if (!task.worktree) {
    // Ran in place — nothing to merge, same as the old ApproveAction.
    spec = close("Approve & close", "Finishing…", note ? `${note} · no diff` : "no diff");
  } else {
    // Legacy worktree task: land it only if it actually changed something. While
    // the diff loads we optimistically assume a merge (the common case); an empty
    // diff downgrades it to a close.
    const empty = diff !== null && diff.added === 0 && diff.removed === 0;
    spec = empty ? close("Approve & close", "Finishing…", "no diff") : OUTCOMES.merge;
  }

  // In PR landing mode the merge happens on GitHub, not in the local checkout —
  // the label must not promise "to main" when the truth is "via a PR".
  const pr = spec.merge && landing === "pr";
  return (
    <MossAction
      icon={
        spec.merge ? <MergeIcon className="text-white" /> : <CheckIcon className="text-white" />
      }
      label={pr ? "Merge via PR" : spec.label}
      busyLabel={pr ? "Opening PR…" : spec.busyLabel}
      errFallback={spec.errFallback}
      run={() => spec.run(task.id)}
      trailing={
        spec.merge ? (
          diff && (
            <span className="flex items-center gap-1.25 font-mono text-[11px] leading-[14px]">
              <span className="text-moss">+{diff.added}</span>
              <span className="text-diff-minus">−{diff.removed}</span>
            </span>
          )
        ) : (
          <span className="text-[11px] leading-[14px] text-faint">{spec.note}</span>
        )
      }
    />
  );
}
