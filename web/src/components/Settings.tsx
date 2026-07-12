import { useEffect, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { api, type Project } from "../api";
import type { BoardLayout } from "../useSettings";
import { Modal } from "./Modal";

interface Props {
  open: boolean;
  onClose: () => void;
  projects: Project[];
  layout: BoardLayout;
  setLayout: (l: BoardLayout) => void;
}

// Concurrency bounds mirror the server clamp (core.MinConcurrent/MaxConcurrentCap);
// GET /api/settings reports the live range so these are only the initial fallback.
const CONC_MIN = 1;
const CONC_MAX = 8;

// Settings manages the board layout preference, the parallel-agent limit, and
// the registered projects. Selection controls use ink (never orange — that
// stays reserved for needs_human). Adding a project opens the OS-native folder
// picker via the daemon; the new project arrives over SSE.
export function Settings({ open, onClose, projects, layout, setLayout }: Props) {
  const [picking, setPicking] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // Whether the daemon can open a native folder dialog (macOS). null while the
  // setting loads; false → show the paste-the-path fallback field instead.
  const [nativePicker, setNativePicker] = useState<boolean | null>(null);
  const [pastePath, setPastePath] = useState("");

  // Parallel-agent limit. Loaded from the daemon when Settings opens; changes are
  // applied optimistically and POSTed, reflecting the server's clamped value.
  const [conc, setConc] = useState<number | null>(null);
  const [concMin, setConcMin] = useState(CONC_MIN);
  const [concMax, setConcMax] = useState(CONC_MAX);

  useEffect(() => {
    if (!open) return;
    setErr(null);
    let live = true;
    api
      .settings()
      .then((s) => {
        if (!live) return;
        setConc(s.maxConcurrent);
        setConcMin(s.maxConcurrentMin);
        setConcMax(s.maxConcurrentMax);
        setNativePicker(s.nativePicker);
      })
      .catch(() => {
        /* leave the control disabled until it loads */
      });
    return () => {
      live = false;
    };
  }, [open]);

  async function changeConcurrency(next: number) {
    const clamped = Math.max(concMin, Math.min(concMax, next));
    if (conc === null || clamped === conc) return;
    const prev = conc;
    setConc(clamped); // optimistic
    setErr(null);
    try {
      const { maxConcurrent } = await api.setConcurrency(clamped);
      setConc(maxConcurrent); // server's clamped value wins
    } catch (e) {
      setConc(prev); // roll back on failure
      setErr(e instanceof Error ? e.message : "couldn't change parallel agents");
    }
  }

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

  // addByPath registers a project from a pasted path (the fallback where no
  // native picker exists). The server validates it's a git repo; the new project
  // arrives via SSE.
  async function addByPath() {
    const path = pastePath.trim();
    if (picking || !path) return;
    setPicking(true);
    setErr(null);
    try {
      await api.addProject(path);
      setPastePath("");
    } catch (e) {
      setErr(e instanceof Error ? e.message : "couldn't add that folder");
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

  return (
    <Modal open={open} onClose={onClose} className="max-w-lg">
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

        {/* parallel agents */}
        <h3 className="eyebrow mb-2.5 mt-6 text-muted">Parallel agents</h3>
        <div className="flex items-center justify-between rounded-lg border border-hairline bg-board px-3 py-2.5">
          <div className="min-w-0 pr-3">
            <div className="text-[14px] font-medium text-ink">
              How many run at once
            </div>
            <div className="text-[12px] leading-snug text-muted">
              All agents share one subscription rate limit, so higher isn't
              always faster.
            </div>
          </div>
          <Stepper
            value={conc}
            min={concMin}
            max={concMax}
            onChange={changeConcurrency}
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

        {/* add project — native folder picker on macOS, paste-path fallback
            elsewhere (the daemon reports which via settings.nativePicker) */}
        {nativePicker !== false ? (
          <>
            <button
              onClick={chooseFolder}
              disabled={picking}
              className="mt-3 flex w-full items-center justify-center gap-2 rounded-lg border border-dashed border-ink/25 bg-surface px-3 py-3 text-[14px] font-semibold text-ink transition hover:border-ink/50 hover:bg-board disabled:opacity-60"
            >
              <FolderIcon />
              {picking ? "Choose a folder in Finder…" : "Choose a folder…"}
            </button>
            <p className="mt-2 text-[12px] text-muted">
              Opens your file browser — pick the project's git repo folder. Its
              name becomes the project name.
            </p>
          </>
        ) : (
          <>
            <form
              className="mt-3 flex gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                void addByPath();
              }}
            >
              <input
                type="text"
                value={pastePath}
                onChange={(e) => setPastePath(e.target.value)}
                placeholder="/home/you/code/my-repo"
                spellCheck={false}
                autoComplete="off"
                className="min-w-0 flex-1 rounded-lg border border-hairline bg-surface px-3 py-2.5 font-mono text-[13px] text-ink placeholder:text-muted/60 focus:border-ink/40 focus:outline-none"
              />
              <button
                type="submit"
                disabled={picking || pastePath.trim() === ""}
                className="shrink-0 rounded-lg border border-dashed border-ink/25 bg-surface px-4 py-2.5 text-[14px] font-semibold text-ink transition hover:border-ink/50 hover:bg-board disabled:opacity-40"
              >
                {picking ? "Adding…" : "Add"}
              </button>
            </form>
            <p className="mt-2 text-[12px] text-muted">
              Paste the absolute path to the project's git repo folder. Its name
              becomes the project name.
            </p>
          </>
        )}

        {err && <p className="mt-3 text-[13px] text-rust">{err}</p>}
    </Modal>
  );
}

// Stepper is a compact −/+ number control. value=null shows a placeholder while
// the current setting is still loading. The digit animates on change so a change
// registers even when the number lands one step away.
function Stepper({
  value,
  min,
  max,
  onChange,
}: {
  value: number | null;
  min: number;
  max: number;
  onChange: (n: number) => void;
}) {
  const atMin = value === null || value <= min;
  const atMax = value === null || value >= max;
  return (
    <div className="flex shrink-0 items-center gap-1 rounded-lg border border-hairline bg-surface p-1">
      <StepButton
        label="Fewer parallel agents"
        disabled={atMin}
        onClick={() => value !== null && onChange(value - 1)}
      >
        −
      </StepButton>
      <div className="relative h-6 w-7 overflow-hidden text-center">
        <AnimatePresence mode="popLayout" initial={false}>
          <motion.span
            key={value ?? "loading"}
            initial={{ opacity: 0, y: 6 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: -6 }}
            transition={{ type: "spring", stiffness: 420, damping: 32 }}
            className="absolute inset-0 grid place-items-center text-[15px] font-semibold tabular-nums text-ink"
          >
            {value ?? "–"}
          </motion.span>
        </AnimatePresence>
      </div>
      <StepButton
        label="More parallel agents"
        disabled={atMax}
        onClick={() => value !== null && onChange(value + 1)}
      >
        +
      </StepButton>
    </div>
  );
}

function StepButton({
  label,
  disabled,
  onClick,
  children,
}: {
  label: string;
  disabled: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      disabled={disabled}
      onClick={onClick}
      className="grid h-7 w-7 place-items-center rounded-md text-[16px] font-semibold text-ink transition hover:bg-board disabled:opacity-30 disabled:hover:bg-transparent"
    >
      {children}
    </button>
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
