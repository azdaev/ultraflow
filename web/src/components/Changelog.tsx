import { useEffect, useState } from "react";
import { Modal } from "./Modal";
import { Markdown } from "./Markdown";

// Changelog is the "What's new" panel: it fetches the public CHANGELOG.md
// (copied into the frontend build and served as a static asset at /changelog.md)
// and renders it with the shared Markdown component. Content is filled at each
// release cut by scripts/gen-changelog.sh.
export function Changelog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [text, setText] = useState<string | null>(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    if (!open) return;
    let alive = true;
    setText(null);
    setError(false);
    fetch("/changelog.md")
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error("no changelog"))))
      .then((t) => alive && setText(t))
      .catch(() => alive && setError(true));
    return () => {
      alive = false;
    };
  }, [open]);

  return (
    <Modal open={open} onClose={onClose} className="max-w-2xl">
      <div className="mb-5 flex items-center justify-between">
        <h2 className="text-[17px] font-semibold text-ink">What's new</h2>
        <button
          onClick={onClose}
          className="rounded-lg px-2 py-1 text-[13px] text-muted hover:bg-board"
        >
          Esc
        </button>
      </div>
      <div className="max-h-[65vh] overflow-y-auto pr-1">
        {error ? (
          <p className="text-[14px] text-muted">No changelog yet.</p>
        ) : text === null ? (
          <p className="text-[14px] text-muted">Loading…</p>
        ) : (
          <Markdown text={text} />
        )}
      </div>
    </Modal>
  );
}
