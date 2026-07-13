import { useEffect, useState } from "react";
import { api, errMsg, type HumanRequest } from "../api";
import { ImageAttachStrip, useImageAttach, withAttachments } from "./ImageAttach";

interface Props {
  request: HumanRequest;
}

// AnswerBox renders the live ask_human decision UI: one-tap option chips plus a
// free-reply row. Both post to /api/human_requests/{id}/answer, which unblocks
// the parked agent. Orange is the decision family.
export function AnswerBox({ request }: Props) {
  const [busy, setBusy] = useState<string | null>(null);
  const [free, setFree] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const attach = useImageAttach();
  const options = request.options ?? [];

  async function send(answer: string) {
    const a = answer.trim();
    if (!a || busy) return;
    setBusy(a);
    setErr(null);
    try {
      await api.answer(request.id, a);
      attach.clear();
      // The SSE human_answered event removes this request from the board.
    } catch (e) {
      setErr(errMsg(e, "failed to send"));
      setBusy(null);
    }
  }

  // Number keys pick the matching option (1 → first, 2 → second…) so the core
  // decision is keyboard-fast the way you'd triage in Linear/Superhuman. Skipped
  // while a field is focused so the digits still reach the free-reply box; re-bound
  // on busy so a settled answer can't fire twice (mirrors the buttons' disabled).
  useEffect(() => {
    const opts = request.options ?? [];
    if (opts.length === 0 || busy) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const el = e.target as HTMLElement | null;
      if (el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.isContentEditable)) return;
      const n = Number(e.key);
      if (!Number.isInteger(n) || n < 1 || n > opts.length) return;
      e.preventDefault();
      send(opts[n - 1]);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [request.id, busy]);

  return (
    <div className="mt-1">
      <div className="flex flex-wrap gap-2">
        {options.map((opt, i) => (
          <button
            key={opt + i}
            onClick={() => send(opt)}
            disabled={!!busy}
            className={`inline-flex items-center gap-1.5 rounded-lg px-3 py-2 text-[13px] font-semibold transition disabled:opacity-50 ${
              i === 0
                ? "bg-accent text-white hover:brightness-105"
                : "border border-hairline bg-surface text-ink hover:border-ink/30"
            } ${busy === opt ? "opacity-60" : ""}`}
          >
            {i < 9 && (
              <kbd
                className={`grid h-4 min-w-4 place-items-center rounded px-1 font-mono text-[10px] font-medium ${
                  i === 0 ? "bg-white/20 text-white/90" : "bg-board text-faint"
                }`}
              >
                {i + 1}
              </kbd>
            )}
            {opt}
          </button>
        ))}
      </div>

      <ImageAttachStrip attach={attach} compact />

      <form
        className="mt-2 flex items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          // Still uploading — block so the image's path isn't dropped from the answer.
          if (attach.busy) return;
          send(withAttachments(free, attach.attachments));
        }}
      >
        <input
          value={free}
          onChange={(e) => setFree(e.target.value)}
          {...attach.pasteProps}
          placeholder={options.length ? "Other… type a different answer" : "Type your answer"}
          disabled={!!busy}
          className="min-w-0 flex-1 rounded-lg border border-hairline bg-surface px-3 py-2 text-[13px] outline-none placeholder:text-muted/70 focus:border-accent/60"
        />
        <button
          type="submit"
          disabled={!!busy || attach.busy || (!free.trim() && attach.attachments.length === 0)}
          className="shrink-0 rounded-lg border border-hairline bg-surface px-3 py-2 text-[13px] font-semibold text-ink transition hover:border-ink/30 disabled:opacity-40"
        >
          Send
        </button>
      </form>
      {err && <p className="mt-1 text-[12px] text-rust">{err}</p>}
    </div>
  );
}
