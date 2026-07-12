import { useState } from "react";
import { api, errMsg } from "../api";

// ReviseBox turns review from a merge-or-nothing dead-end into a conversation:
// the human types what's wrong ("you made X shit, redo it") and the agent is
// re-launched in the same worktree to fix it, flipping the card back to running.
// Shown for a task in review or failed. Enter (⌘/Ctrl+Enter) sends.
export function ReviseBox({ taskId }: { taskId: string }) {
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function send() {
    const m = msg.trim();
    if (!m || busy) return;
    setBusy(true);
    setErr(null);
    try {
      await api.revise(taskId, m);
      setMsg("");
      // SSE flips the card to running; the terminal takes over this drawer so the
      // human watches the rework live. Nothing more to do here.
    } catch (e) {
      setErr(errMsg(e, "couldn't send it back"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mb-5 rounded-xl border border-hairline bg-board p-4">
      <h3 className="eyebrow mb-2 text-ink">Send it back to the agent</h3>
      <p className="mb-2 text-[12px] leading-relaxed text-muted">
        Tell the agent what to change. It reworks in the same worktree, keeping its
        memory and its code, then returns to review.
      </p>
      <textarea
        value={msg}
        onChange={(e) => setMsg(e.target.value)}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
            e.preventDefault();
            send();
          }
        }}
        rows={3}
        placeholder="e.g. the header spacing is off and the empty state is missing — redo those"
        className="w-full resize-y rounded-lg border border-hairline bg-surface px-3 py-2 text-[13px] leading-relaxed outline-none placeholder:text-muted/50 focus:border-ink/40"
      />
      {err && <p className="mt-1.5 text-[12px] text-rust">{err}</p>}
      <div className="mt-2 flex items-center justify-between">
        <span className="font-mono text-[10px] text-muted/70">⌘↵ to send</span>
        <button
          onClick={send}
          disabled={busy || !msg.trim()}
          className="rounded-lg bg-ink px-3 py-1.5 text-[13px] font-semibold text-surface transition hover:brightness-125 disabled:opacity-40"
        >
          {busy ? "Sending…" : "Send back"}
        </button>
      </div>
    </div>
  );
}
