import { useCallback, useState } from "react";
import { useDropzone } from "react-dropzone";
import { api, errMsg, type Attachment } from "../api";

// ImageAttach is the shared image-attachment affordance reused by every composer
// (New task, inline Add, ask_human answer, review send-back). It uploads picked /
// dropped / pasted images to /api/uploads and holds the resulting Attachments;
// on submit the surface appends their on-disk paths to the outgoing text via
// withAttachments, so the agent's Read tool can open them — no prompt plumbing.

// useImageAttach owns the attachment list plus upload state. A surface calls
// addFiles (from the dropzone or a paste), renders <ImageAttachStrip>, spreads
// pasteProps on its text field, and sends withAttachments(text, attachments) on
// submit, then clear() on success.
export function useImageAttach() {
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const addFiles = useCallback(async (files: File[]) => {
    const imgs = files.filter((f) => f.type.startsWith("image/"));
    if (imgs.length === 0) return;
    setBusy(true);
    setError(null);
    try {
      const uploaded = await api.uploadImages(imgs);
      setAttachments((a) => [...a, ...uploaded]);
    } catch (e) {
      setError(errMsg(e, "couldn't attach image"));
    } finally {
      setBusy(false);
    }
  }, []);

  const remove = useCallback((url: string) => {
    setAttachments((a) => a.filter((x) => x.url !== url));
  }, []);

  const clear = useCallback(() => {
    setAttachments([]);
    setError(null);
  }, []);

  // Clipboard paste — dropzone doesn't cover it, so we read image files off the
  // paste event ourselves. preventDefault only when an image is present, so a
  // normal text paste into the field is untouched.
  const pasteProps = {
    onPaste: (e: React.ClipboardEvent) => {
      const files = Array.from(e.clipboardData.files);
      if (files.some((f) => f.type.startsWith("image/"))) {
        e.preventDefault();
        void addFiles(files);
      }
    },
  };

  return { attachments, addFiles, remove, clear, busy, error, pasteProps };
}

export type ImageAttach = ReturnType<typeof useImageAttach>;

// withAttachments appends the "view them with your Read tool" block listing each
// attachment's absolute path to the outgoing text. Returns text unchanged when
// there's nothing attached.
export function withAttachments(text: string, attachments: Attachment[]): string {
  const t = text.trim();
  if (attachments.length === 0) return t;
  const lines = attachments.map((a) => `- ${a.path}`).join("\n");
  const block = `Attached image(s) — view them with your Read tool:\n${lines}`;
  return t ? `${t}\n\n${block}` : block;
}

// ImageAttachStrip is the visible affordance: a click/drag dropzone plus a row of
// thumbnail previews, each removable. compact trims it for the small surfaces
// (inline Add, ask_human answer).
export function ImageAttachStrip({
  attach,
  compact,
}: {
  attach: ImageAttach;
  compact?: boolean;
}) {
  const { attachments, addFiles, remove, busy, error } = attach;
  const { getRootProps, getInputProps, isDragActive } = useDropzone({
    accept: { "image/*": [] },
    onDrop: (accepted) => void addFiles(accepted),
  });
  const thumb = compact ? "h-10 w-10" : "h-14 w-14";

  return (
    <div className={compact ? "mt-1.5" : "mt-2"}>
      <div
        {...getRootProps()}
        className={`flex cursor-pointer items-center gap-2 rounded-lg border border-dashed px-3 text-[12px] transition ${
          compact ? "py-1.5" : "py-2"
        } ${
          isDragActive
            ? "border-ink/40 bg-board text-ink"
            : "border-hairline text-muted/80 hover:border-ink/25 hover:text-ink"
        }`}
      >
        <input {...getInputProps()} />
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" className="shrink-0">
          <path
            d="M4 16l4-4a2 2 0 013 0l3 3m-2-2l1-1a2 2 0 013 0l3 3M4 6h16a1 1 0 011 1v10a1 1 0 01-1 1H4a1 1 0 01-1-1V7a1 1 0 011-1z"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
        <span>
          {busy
            ? "Uploading…"
            : isDragActive
              ? "Drop the image here"
              : "Add image — click, drop, or paste"}
        </span>
      </div>

      {attachments.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-2">
          {attachments.map((a) => (
            <div key={a.url} className={`group relative ${thumb}`}>
              <img
                src={a.url}
                alt={a.name}
                title={a.name}
                className="h-full w-full rounded-lg border border-hairline object-cover"
              />
              <button
                type="button"
                onClick={() => remove(a.url)}
                title="Remove"
                className="absolute -right-1.5 -top-1.5 flex h-4 w-4 items-center justify-center rounded-full border border-hairline bg-surface text-[10px] leading-none text-muted shadow-sm transition hover:text-rust"
              >
                ✕
              </button>
            </div>
          ))}
        </div>
      )}
      {error && <p className="mt-1 text-[12px] text-rust">{error}</p>}
    </div>
  );
}
