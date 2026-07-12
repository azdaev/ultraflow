import { useEffect, useState } from "react";
import { motion } from "motion/react";
import { api, type Project } from "../api";
import { AGENTS, FLOWS } from "../util";

interface Props {
  open: boolean;
  onClose: () => void;
  projects: Project[];
}

// Composer is the expanded New Task surface (project · flow · agent). It creates
// a backlog task; the orchestrator starts it when a slot frees.
export function Composer({ open, onClose, projects }: Props) {
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [project, setProject] = useState("");
  const [flow, setFlow] = useState("solo");
  const [agent, setAgent] = useState("claude");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setErr(null);
      setBusy(false);
    }
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
      if ((e.metaKey || e.ctrlKey) && e.key === "Enter") submit();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, title, body, project, flow, agent]);

  async function submit() {
    if (!title.trim() || busy) return;
    setBusy(true);
    setErr(null);
    try {
      await api.createTask({ title, body, project, agent, flow });
      setTitle("");
      setBody("");
      setProject("");
      onClose();
    } catch (e) {
      setErr(e instanceof Error ? e.message : "failed to create task");
      setBusy(false);
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
        className="relative w-full max-w-xl rounded-2xl border border-hairline bg-surface p-5 shadow-[0_24px_60px_-20px_rgba(23,23,26,0.4)]"
      >
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-[17px] font-semibold text-ink">New task</h2>
          <button
            onClick={onClose}
            className="rounded-lg px-2 py-1 text-[13px] text-muted hover:bg-board"
          >
            Esc
          </button>
        </div>

        <input
          autoFocus
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="What should the agent do?"
          className="w-full rounded-lg border border-hairline bg-surface px-3 py-2.5 text-[16px] font-medium outline-none placeholder:text-muted/60 focus:border-ink/40"
        />
        <textarea
          value={body}
          onChange={(e) => setBody(e.target.value)}
          placeholder="Details, acceptance criteria… (optional)"
          rows={3}
          className="mt-2 w-full resize-none rounded-lg border border-hairline bg-surface px-3 py-2.5 text-[14px] outline-none placeholder:text-muted/60 focus:border-ink/40"
        />

        <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
          <Field label="Project">
            <Select value={project} onChange={setProject}>
              <option value="">No project</option>
              {projects.map((p) => (
                <option key={p.id} value={p.name}>
                  {p.name}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="Flow">
            <Select value={flow} onChange={setFlow}>
              {Object.values(FLOWS).map((f) => (
                <option key={f.key} value={f.key} disabled={!f.available}>
                  {f.available ? f.label : `${f.label} · soon`}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="Agent">
            <Select value={agent} onChange={setAgent}>
              {AGENTS.map((a) => (
                <option key={a.key} value={a.key} disabled={!a.available}>
                  {a.available ? a.label : `${a.label} · soon`}
                </option>
              ))}
            </Select>
          </Field>
        </div>

        {err && <p className="mt-3 text-[13px] text-rust">{err}</p>}

        <div className="mt-5 flex items-center justify-between">
          <span className="text-[12px] text-muted">Runs in a fresh worktree · starts when a slot frees</span>
          <button
            onClick={submit}
            disabled={busy || !title.trim()}
            className="rounded-lg bg-accent px-4 py-2.5 text-[14px] font-semibold text-white transition hover:brightness-105 disabled:opacity-50"
          >
            {busy ? "Adding…" : "Add task ⌘↵"}
          </button>
        </div>
      </motion.div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-[11px] font-semibold uppercase tracking-[0.07em] text-muted">
        {label}
      </span>
      {children}
    </label>
  );
}

function Select({
  value,
  onChange,
  children,
}: {
  value: string;
  onChange: (v: string) => void;
  children: React.ReactNode;
}) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="w-full rounded-lg border border-hairline bg-surface px-2.5 py-2 text-[13px] outline-none focus:border-ink/40"
    >
      {children}
    </select>
  );
}
