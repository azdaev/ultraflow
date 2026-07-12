import { useEffect, useMemo, useState } from "react";
import { api, type TaskDiff } from "../api";

// Files whose changes are likely visual — used only to nudge "you changed UI but
// left no screenshots", never to block anything.
const VISUAL_EXT = /\.(tsx?|jsx?|css|scss|sass|less|html|vue|svelte)$/i;

// ReviewPanel is what makes the review screen useful: the actual work the agent
// did. Screenshots it captured (for visual changes) up top, then the code diff
// (magnitude first, raw patch collapsible — the human rarely reads code). Shown
// for a task that has a worktree and isn't live. `sig` changes when a new event
// lands so a rework's fresh diff/shots reload.
export function ReviewPanel({ taskId, sig }: { taskId: string; sig?: string }) {
  const [diff, setDiff] = useState<TaskDiff | null>(null);
  const [shots, setShots] = useState<string[]>([]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    setErr(null);
    api
      .diff(taskId)
      .then((d) => alive && setDiff(d))
      .catch((e) => alive && setErr(e instanceof Error ? e.message : "couldn't load the diff"));
    api
      .shots(taskId)
      .then((s) => alive && setShots(s))
      .catch(() => alive && setShots([]));
    return () => {
      alive = false;
    };
  }, [taskId, sig]);

  const visualChanged = useMemo(
    () => !!diff && diff.files.some((f) => VISUAL_EXT.test(f.path)),
    [diff],
  );

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto">
      {/* Screenshots — the "show me the visual change" surface. */}
      {shots.length > 0 && (
        <section>
          <h3 className="eyebrow mb-2 text-muted">Screenshots</h3>
          <div className="grid grid-cols-2 gap-2">
            {shots.map((name) => (
              <a
                key={name}
                href={api.shotUrl(taskId, name)}
                target="_blank"
                rel="noreferrer"
                className="block overflow-hidden rounded-lg border border-hairline bg-surface transition hover:border-ink/25"
              >
                <img
                  src={api.shotUrl(taskId, name)}
                  alt={name}
                  className="max-h-64 w-full object-contain bg-[#17171A]"
                />
                <span className="block truncate px-2 py-1 font-mono text-[10px] text-muted">
                  {name}
                </span>
              </a>
            ))}
          </div>
        </section>
      )}

      {/* Nudge: visual code changed but the agent left no screenshots. */}
      {visualChanged && shots.length === 0 && (
        <p className="rounded-lg border border-hairline bg-board px-3 py-2 text-[12px] leading-relaxed text-ink/80">
          <span className="font-semibold">Visual changes, no screenshots.</span> Send
          it back below and ask the agent to add them.
        </p>
      )}

      {/* Diff — magnitude first, raw patch is a collapsible disclosure. */}
      <section className="min-h-0">
        <div className="mb-2 flex items-baseline justify-between">
          <h3 className="eyebrow text-muted">Changes</h3>
          {diff && (
            <span className="font-mono text-[12px]">
              <span className="text-moss">+{diff.added}</span>{" "}
              <span className="text-rust">−{diff.removed}</span>
              <span className="text-muted">
                {" "}
                · {diff.files.length} file{diff.files.length === 1 ? "" : "s"}
              </span>
            </span>
          )}
        </div>

        {err && <p className="text-[12px] text-muted">{err}</p>}

        {diff && diff.files.length === 0 && !err && (
          <p className="text-[12px] text-muted">No file changes in the worktree.</p>
        )}

        {diff && diff.patch && (
          <details className="group rounded-lg border border-hairline bg-[#17171A]">
            <summary className="cursor-pointer list-none px-3 py-2 font-mono text-[11px] text-muted hover:text-ink">
              <span className="group-open:hidden">▸ Show diff</span>
              <span className="hidden group-open:inline">▾ Hide diff</span>
            </summary>
            <DiffBody patch={diff.patch} truncated={diff.truncated} />
          </details>
        )}
      </section>
    </div>
  );
}

// DiffBody renders a unified patch with light per-line coloring (added green,
// removed rust, hunk headers steel), horizontally scrollable so long lines don't
// break the layout.
function DiffBody({ patch, truncated }: { patch: string; truncated: boolean }) {
  const lines = patch.split("\n");
  return (
    <div className="max-h-[50vh] overflow-auto border-t border-white/10 p-3">
      <pre className="font-mono text-[11px] leading-[1.5]">
        {lines.map((line, i) => {
          let color = "text-[#ECECEA]";
          if (line.startsWith("+") && !line.startsWith("+++")) color = "text-moss";
          else if (line.startsWith("-") && !line.startsWith("---")) color = "text-[#E4795F]";
          else if (line.startsWith("@@")) color = "text-steel";
          else if (line.startsWith("diff ") || line.startsWith("index ")) color = "text-muted";
          return (
            <div key={i} className={`whitespace-pre ${color}`}>
              {line || " "}
            </div>
          );
        })}
      </pre>
      {truncated && (
        <p className="mt-2 font-mono text-[10px] text-muted">
          — diff truncated (too large to show in full) —
        </p>
      )}
    </div>
  );
}
