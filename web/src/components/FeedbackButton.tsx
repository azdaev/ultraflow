import { useEffect, useRef, useState } from "react";
import { api, errMsg } from "../api";
import { Modal } from "./Modal";
import { MessageIcon } from "../board/icons";

// FeedbackButton is the always-present "leave feedback" affordance: a small
// fixed pill anchored bottom-right, above the board but below any modal, that
// opens a one-field form for a quick note. On success it swaps to a thank-you
// and auto-closes, so leaving feedback is a near-instant, low-friction action.
export function FeedbackButton() {
  const [open, setOpen] = useState(false);
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [sent, setSent] = useState(false);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => () => {
    if (closeTimer.current) clearTimeout(closeTimer.current);
  }, []);

  function close() {
    setOpen(false);
    // Give the modal's exit animation room to finish before resetting state.
    setTimeout(() => {
      setMsg("");
      setErr(null);
      setSent(false);
    }, 200);
  }

  async function send() {
    const m = msg.trim();
    if (!m || busy) return;
    setBusy(true);
    setErr(null);
    try {
      await api.sendFeedback(m, window.location.pathname);
      setSent(true);
      closeTimer.current = setTimeout(close, 1200);
    } catch (e) {
      setErr(errMsg(e, "couldn't send that"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <button
        onClick={() => setOpen(true)}
        title="Leave feedback"
        className="fixed bottom-5 right-5 z-40 flex items-center gap-1.5 rounded-full border-[0.75px] border-hairline bg-surface px-3.5 py-2 text-[13px] font-medium text-muted shadow-[0_8px_24px_-8px_rgba(23,23,26,0.25)] transition hover:border-ink/25 hover:text-ink"
      >
        <MessageIcon />
        Feedback
      </button>

      <Modal open={open} onClose={close} title="Leave feedback" className="max-w-md">
        {sent ? (
          <p className="py-4 text-center text-[14px] text-ink">Thanks — got it.</p>
        ) : (
          <>
            <textarea
              autoFocus
              value={msg}
              onChange={(e) => setMsg(e.target.value)}
              onKeyDown={(e) => {
                if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
                  e.preventDefault();
                  send();
                }
              }}
              rows={4}
              placeholder="What's working, what's annoying, what's missing?"
              className="w-full resize-y rounded-lg border border-hairline bg-board px-3 py-2 text-[13px] leading-relaxed outline-none placeholder:text-muted/50 focus:border-ink/40"
            />
            {err && <p className="mt-1.5 text-[12px] text-rust">{err}</p>}
            <div className="mt-3 flex items-center justify-end">
              <button
                onClick={send}
                disabled={busy || !msg.trim()}
                className="rounded-lg bg-ink px-3 py-1.5 text-[13px] font-semibold text-surface transition hover:brightness-125 disabled:opacity-40"
              >
                {busy ? "Sending…" : "Send"}
              </button>
            </div>
          </>
        )}
      </Modal>
    </>
  );
}
