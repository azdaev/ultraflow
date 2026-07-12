import { useState } from "react";
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

  return (
    <div className="mt-1">
      <div className="flex flex-wrap gap-2">
        {options.map((opt, i) => (
          <button
            key={opt + i}
            onClick={() => send(opt)}
            disabled={!!busy}
            className={`rounded-lg px-3 py-2 text-[13px] font-semibold transition disabled:opacity-50 ${
              i === 0
                ? "bg-accent text-white hover:brightness-105"
                : "border border-hairline bg-surface text-ink hover:border-ink/30"
            } ${busy === opt ? "opacity-60" : ""}`}
          >
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
