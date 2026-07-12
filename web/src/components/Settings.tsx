import { useEffect, useState } from "react";
import { motion } from "motion/react";
import { api, type Project } from "../api";
import type { BoardLayout } from "../useSettings";

interface Props {
  open: boolean;
  onClose: () => void;
  projects: Project[];
  layout: BoardLayout;
  setLayout: (l: BoardLayout) => void;
}

// Settings manages the board layout preference and the registered projects.
// Selection controls use ink (never orange — that stays reserved for
// needs_human). Adding a project opens the OS-native folder picker via the
// daemon; the new project arrives over SSE.
export function Settings({ open, onClose, projects, layout, setLayout }: Props) {
  const [picking, setPicking] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setErr(null);
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  async function chooseFolder() {
    if (picking) return;
    setPicking(true);
    setErr(null);
    try {
      await api.pickProject(); // new project (if any) arrives via SSE
    } catch (e) {
      setErr(e instanceof Error ? e.message : "couldn't open the folder picker");
    } finally {
      setPicking(false);
    }
  }

  async function remove(p: Project) {
    setErr(null);
    try {
      await api.deleteProject(p.id); // removal arrives via SSE
    } catch (e) {
      setErr(e instanceof Error ? e.message : "couldn't remove the project");
    }
  }

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-ink/25 p-4 pt-[8vh] backdrop-blur-sm">
      <div className="absolute inset-0" onClick={onClose} />
      <motion.div
        initial={{ opacity: 0, y: 12, scale: 0.98 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        transition={{ type: "spring", stiffness: 320, damping: 30 }}
        className="relative w-full max-w-lg rounded-2xl border border-hairline bg-surface p-5 shadow-[0_24px_60px_-20px_rgba(23,23,26,0.4)]"
      >
        <div className="mb-5 flex items-center justify-between">
          <h2 className="text-[17px] font-semibold text-ink">Settings</h2>
          <button
            onClick={onClose}
            className="rounded-lg px-2 py-1 text-[13px] text-muted hover:bg-board"
          >
            Esc
          </button>
        </div>

        {/* board layout */}
        <h3 className="eyebrow mb-2.5 text-muted">Board layout</h3>
        <div className="grid grid-cols-2 gap-3">
          <LayoutOption
            active={layout === "swimlanes"}
            onClick={() => setLayout("swimlanes")}
            title="Swimlanes"
            desc="A lane per project. Best for a few."
          />
          <LayoutOption
            active={layout === "filter"}
            onClick={() => setLayout("filter")}
            title="Filter + chips"
            desc="One board, switch & tag. Scales."
          />
        </div>

        {/* projects */}
        <h3 className="eyebrow mb-2.5 mt-6 text-muted">Projects</h3>
        <div className="flex flex-col gap-2">
          {projects.length === 0 && (
            <p className="rounded-lg border border-dashed border-hairline px-3 py-4 text-center text-[13px] text-muted/80">
              No projects yet. Choose a folder to add one.
            </p>
          )}
          {projects.map((p) => (
            <div
              key={p.id}
              className="flex items-center gap-2.5 rounded-lg border border-hairline bg-board px-3 py-2"
            >
              <span
                className="h-3 w-3 shrink-0 rounded-[4px]"
                style={{ backgroundColor: p.color }}
              />
              <div className="min-w-0 flex-1">
                <div className="text-[14px] font-medium text-ink">{p.name}</div>
                <div className="truncate font-mono text-[11px] text-muted">
                  {p.repoPath}
                </div>
              </div>
              <button
                onClick={() => remove(p)}
                className="shrink-0 rounded-md px-2 py-1 text-[12px] font-medium text-muted transition hover:bg-surface hover:text-rust"
              >
                Remove
              </button>
            </div>
          ))}
        </div>

        {/* add project — native folder picker */}
        <button
          onClick={chooseFolder}
          disabled={picking}
          className="mt-3 flex w-full items-center justify-center gap-2 rounded-lg border border-dashed border-ink/25 bg-surface px-3 py-3 text-[14px] font-semibold text-ink transition hover:border-ink/50 hover:bg-board disabled:opacity-60"
        >
          <FolderIcon />
          {picking ? "Choose a folder in Finder…" : "Choose a folder…"}
        </button>
        <p className="mt-2 text-[12px] text-muted">
          Opens your file browser — pick the project's git repo folder. Its name
          becomes the project name.
        </p>

        {err && <p className="mt-3 text-[13px] text-rust">{err}</p>}
      </motion.div>
    </div>
  );
}

function LayoutOption({
  active,
  onClick,
  title,
  desc,
}: {
  active: boolean;
  onClick: () => void;
  title: string;
  desc: string;
}) {
  return (
    <button
      onClick={onClick}
      className={`flex flex-col items-start rounded-xl border-2 px-3.5 py-3 text-left transition ${
        active ? "border-ink bg-board" : "border-hairline bg-surface hover:border-ink/30"
      }`}
    >
      <span className="flex items-center gap-2">
        <span
          className={`grid h-4 w-4 place-items-center rounded-full border-2 ${
            active ? "border-ink" : "border-hairline"
          }`}
        >
          {active && <span className="h-2 w-2 rounded-full bg-ink" />}
        </span>
        <span className="text-[14px] font-semibold text-ink">{title}</span>
      </span>
      <span className="mt-1.5 text-[12px] leading-snug text-muted">{desc}</span>
    </button>
  );
}

function FolderIcon() {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M4 5h5l2 2h9a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1z" />
    </svg>
  );
}
