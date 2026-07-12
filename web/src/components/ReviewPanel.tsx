import { useEffect, useMemo, useRef, useState } from "react";
import { api, type TaskDiff } from "../api";
import { Markdown } from "./Markdown";

// Files whose changes are likely visual — used only to nudge "you changed UI but
// left no screenshots", never to block anything.
const VISUAL_EXT = /\.(tsx?|jsx?|css|scss|sass|less|html|vue|svelte)$/i;

// shotSrc resolves a Markdown image reference in a report to a loadable URL. A
// remote/data URL is left as-is; anything else is treated as one of the agent's
// saved screenshots (agents save to .ultraflow/shots/ and often reference it as
// `shot.png` or `.ultraflow/shots/shot.png`), so we serve it from the task's
// shots endpoint by its bare filename.
function shotSrc(taskId: string, src: string): string {
  if (/^(https?:|data:)/i.test(src)) return src;
  const name = src.replace(/[?#].*$/, "").replace(/^.*[\\/]/, "");
  return api.shotUrl(taskId, name);
}

// ReviewPanel is the review screen's content: the agent's Report (native Markdown
// writeup — for a question/audit task this IS the deliverable) and, when the task
// touched a worktree, the Changes (screenshots + code diff). Tabs keep both a tap
// apart. `report` is the agent's finish_task writeup; `hasWorktree` gates the diff
// side. `sig` changes when a new event lands so a rework's fresh diff/shots reload.
export function ReviewPanel({
  taskId,
  sig,
  report,
  hasWorktree,
}: {
  taskId: string;
  sig?: string;
  report?: string;
  hasWorktree: boolean;
}) {
  const tabs = useMemo(() => {
    const t: { key: "report" | "changes"; label: string; icon: string }[] = [];
    if (report) t.push({ key: "report", label: "Report", icon: "¶" });
    if (hasWorktree) t.push({ key: "changes", label: "Changes", icon: "±" });
    return t;
  }, [report, hasWorktree]);

  const [tab, setTab] = useState<"report" | "changes">(tabs[0]?.key ?? "report");
  // Until the human picks a tab, follow the preferred default (tabs[0] — Report
  // when there is one). Events load async, so on a code task the report often
  // arrives after first mount; without this the view would stick on Changes.
  const touched = useRef(false);
  useEffect(() => {
    if (!tabs.length) return;
    if (touched.current) {
      if (!tabs.some((t) => t.key === tab)) setTab(tabs[0].key); // stay valid
    } else if (tabs[0].key !== tab) {
      setTab(tabs[0].key);
    }
  }, [tabs, tab]);
  const pick = (k: "report" | "changes") => {
    touched.current = true;
    setTab(k);
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {tabs.length > 1 && (
        <div className="mb-3 flex shrink-0 gap-1 border-b border-hairline">
          {tabs.map((t) => (
            <button
              key={t.key}
              onClick={() => pick(t.key)}
              className={`-mb-px flex items-center gap-1.5 border-b-2 px-3 py-1.5 text-[13px] font-medium transition ${
                tab === t.key
                  ? "border-ink text-ink"
                  : "border-transparent text-muted hover:text-ink"
              }`}
            >
              <span className="font-mono text-[12px] opacity-60">{t.icon}</span>
              {t.label}
            </button>
          ))}
        </div>
      )}

      {tab === "report" && report && (
        <div className="min-h-0 flex-1 overflow-y-auto pr-1">
          <Markdown text={report} resolveImg={(src) => shotSrc(taskId, src)} />
        </div>
      )}

      {tab === "changes" && hasWorktree && <Changes taskId={taskId} sig={sig} />}
    </div>
  );
}

// Changes is the code-review surface: screenshots the agent captured (visual
// changes) up top, then the diff (magnitude first, raw patch collapsible).
function Changes({ taskId, sig }: { taskId: string; sig?: string }) {
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
export function DiffBody({ patch, truncated }: { patch: string; truncated: boolean }) {
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
