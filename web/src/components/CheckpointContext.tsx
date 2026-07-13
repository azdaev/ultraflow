import { useState } from "react";
import { api, type HumanRequest, type TaskDiff } from "../api";
import { DiffBody, DiffMagnitude, ShotsGrid } from "./ReviewPanel";

// CheckpointContext is the fast-context surface for an ask_human checkpoint: the
// daemon captures the worktree's change magnitude (+N −N + changed files) and the
// screenshots the agent saved at ask time, snapshotted onto the request. We lead
// with those (a plain summary, the magnitude, a screenshot gallery) and keep the
// raw diff as a quiet, lazily-loaded disclosure — see spec.md "What to surface".
export function CheckpointContext({ request }: { request: HumanRequest }) {
  const files = request.files ?? [];
  const shots = request.shots ?? [];
  const hasMagnitude = files.length > 0 || request.added > 0 || request.removed > 0;

  return (
    <div className="space-y-2.5">
      {request.context && (
        <p className="rounded-lg bg-board px-2.5 py-1.5 text-[12px] leading-relaxed text-muted">
          {request.context}
        </p>
      )}

      {hasMagnitude && (
        <div className="rounded-lg border border-hairline bg-board px-2.5 py-2">
          <div className="flex items-baseline justify-between">
            <span className="eyebrow text-muted">Changes</span>
            <DiffMagnitude added={request.added} removed={request.removed} files={files.length} />
          </div>
          {files.length > 0 && (
            <ul className="mt-1.5 space-y-0.5">
              {files.map((f) => (
                <li
                  key={f.path}
                  className="flex items-baseline justify-between gap-3 font-mono text-[11px]"
                >
                  <span className="min-w-0 flex-1 truncate text-ink/80" title={f.path}>
                    {f.path}
                  </span>
                  <span className="shrink-0 tabular-nums text-muted">
                    <span className="text-moss">+{f.added}</span>{" "}
                    <span className="text-diff-minus">−{f.removed}</span>
                  </span>
                </li>
              ))}
            </ul>
          )}
          <RawDiff taskId={request.taskId} />
        </div>
      )}

      {shots.length > 0 && (
        <div>
          <span className="eyebrow mb-1.5 block text-muted">Screenshots</span>
          <ShotsGrid taskId={request.taskId} shots={shots} maxH="max-h-40" />
        </div>
      )}
    </div>
  );
}

// RawDiff is the quiet secondary link: the human rarely reads code, so the raw
// unified patch is a collapsed disclosure that only fetches when opened.
function RawDiff({ taskId }: { taskId: string }) {
  const [diff, setDiff] = useState<TaskDiff | null>(null);
  const [state, setState] = useState<"idle" | "loading" | "error">("idle");

  function load() {
    if (diff || state === "loading") return;
    setState("loading");
    api
      .diff(taskId)
      .then((d) => {
        setDiff(d);
        setState("idle");
      })
      .catch(() => setState("error"));
  }

  return (
    <details
      className="group mt-2"
      onToggle={(e) => (e.currentTarget as HTMLDetailsElement).open && load()}
    >
      <summary className="cursor-pointer list-none font-mono text-[11px] text-muted hover:text-ink">
        <span className="group-open:hidden">▸ Show diff</span>
        <span className="hidden group-open:inline">▾ Hide diff</span>
      </summary>
      {state === "loading" && (
        <p className="mt-1 font-mono text-[11px] text-muted">loading…</p>
      )}
      {state === "error" && (
        <p className="mt-1 font-mono text-[11px] text-muted">couldn't load the diff</p>
      )}
      {diff && diff.patch && (
        <div className="mt-1.5 overflow-hidden rounded-lg border border-hairline bg-[#17171A]">
          <DiffBody patch={diff.patch} truncated={diff.truncated} />
        </div>
      )}
    </details>
  );
}
