import { useEffect, useRef, useState } from "react";
import { api, errMsg, type Project } from "../api";
import { AGENTS, FLOWS } from "../util";
import { Modal } from "./Modal";
import { ImageAttachStrip, useImageAttach, withAttachments } from "./ImageAttach";

interface Props {
  open: boolean;
  onClose: () => void;
  projects: Project[];
  // When opened from the inline "+ Add task" via its "More…" button, the draft
  // the user already typed is carried over so nothing is retyped.
  initialTitle?: string;
  initialProject?: string;
}

// Composer is the expanded New Task surface (project · flow · agent). It creates
// a backlog task; the orchestrator starts it when a slot frees.
export function Composer({ open, onClose, projects, initialTitle, initialProject }: Props) {
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [project, setProject] = useState("");
  const [flow, setFlow] = useState("solo");
  const [agent, setAgent] = useState("claude");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const attach = useImageAttach();
  // A finished submit clears the fields on the NEXT open, not on close — so the
  // panel fades out still showing what you submitted, and an Esc-close keeps
  // your draft for next time.
  const submitted = useRef(false);

  useEffect(() => {
    if (!open) return;
    // Opened from an inline draft: seed the carried-over title/project and start
    // with a clean body. Otherwise keep the existing draft, only clearing after
    // a prior submit so an Esc-close still preserves what you typed.
    if (initialTitle || initialProject) {
      setTitle(initialTitle ?? "");
      setProject(initialProject ?? "");
      setBody("");
      attach.clear();
      submitted.current = false;
    } else if (submitted.current) {
      setTitle("");
      setBody("");
      setProject("");
      attach.clear();
      submitted.current = false;
    }
    setErr(null);
    setBusy(false);
    // Seeds from the initials captured at open time; re-running on their change
    // would fight typing, so we intentionally key only on `open`.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "Enter") submit();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, title, body, project, flow, agent]);

  async function submit() {
    // attach.busy: an image is still uploading — block so its path isn't dropped
    // from the body (a paste-then-Enter would otherwise submit without it).
    if (!title.trim() || !project || busy || attach.busy) return;
    setBusy(true);
    setErr(null);
    try {
      await api.createTask({ title, body: withAttachments(body, attach.attachments), project, agent, flow });
      submitted.current = true;
      onClose();
    } catch (e) {
      setErr(errMsg(e, "failed to create task"));
      setBusy(false);
    }
  }

  return (
    <Modal open={open} onClose={onClose} className="max-w-xl">
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
          {...attach.pasteProps}
          placeholder="Details, acceptance criteria… (optional)"
          rows={3}
          className="mt-2 w-full resize-none rounded-lg border border-hairline bg-surface px-3 py-2.5 text-[14px] outline-none placeholder:text-muted/60 focus:border-ink/40"
        />
        <ImageAttachStrip attach={attach} />

        <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
          <Field label="Project">
            <Select value={project} onChange={setProject}>
              {/* A project is required — the placeholder can't be submitted, so a
                  task never lands with no project (stranded on main). */}
              <option value="" disabled>
                {projects.length ? "Select a project…" : "No projects — add one in Settings"}
              </option>
              {projects.map((p) => (
                <option key={p.id} value={p.name}>
                  {p.name}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="Flow">
            <Select value={flow} onChange={setFlow}>
              {Object.values(FLOWS)
                .filter((f) => f.available)
                .map((f) => (
                  <option key={f.key} value={f.key}>
                    {f.label}
                  </option>
                ))}
              <SoonGroup labels={Object.values(FLOWS).filter((f) => !f.available).map((f) => f.label)} />
            </Select>
          </Field>
          <Field label="Agent">
            <Select value={agent} onChange={setAgent}>
              {AGENTS.filter((a) => a.available).map((a) => (
                <option key={a.key} value={a.key}>
                  {a.label}
                </option>
              ))}
              <SoonGroup labels={AGENTS.filter((a) => !a.available).map((a) => a.label)} />
            </Select>
          </Field>
        </div>

        {err && <p className="mt-3 text-[13px] text-rust">{err}</p>}

        <div className="mt-5 flex items-center justify-between">
          <span className="text-[12px] text-muted">Runs in a fresh worktree · starts when a slot frees</span>
          <button
            onClick={submit}
            disabled={busy || attach.busy || !title.trim() || !project}
            className="rounded-lg bg-accent px-4 py-2.5 text-[14px] font-semibold text-white transition hover:brightness-105 disabled:opacity-50"
          >
            {busy ? "Adding…" : "Add task ⌘↵"}
          </button>
        </div>
    </Modal>
  );
}

// SoonGroup tucks the not-yet-available choices into a single disabled "Coming
// soon" section at the bottom of a select, so the picker leads with what you can
// actually run instead of a wall of greyed-out "· soon" rows that read as broken.
function SoonGroup({ labels }: { labels: string[] }) {
  if (labels.length === 0) return null;
  return (
    <optgroup label="Coming soon">
      {labels.map((label) => (
        <option key={label} value="" disabled>
          {label}
        </option>
      ))}
    </optgroup>
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
