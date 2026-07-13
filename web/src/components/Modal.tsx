import { useEffect, useId } from "react";
import { AnimatePresence, motion } from "motion/react";

interface Props {
  open: boolean;
  onClose: () => void;
  className?: string; // panel width, e.g. "max-w-xl"
  title?: string; // renders the standard header (title + Esc button) when set
  children: React.ReactNode;
}

// Modal is the shared centered-overlay shell used by the New Task and Settings
// surfaces. AnimatePresence keeps it mounted through its exit, so the scrim
// fades and the panel eases back down on close instead of snapping away. The
// enter springs in; the exit is a smaller, softer translate (enters should feel
// livelier than exits).
export function Modal({ open, onClose, className = "", title, children }: Props) {
  const titleId = useId();
  // Escape-to-dismiss belongs to the shell so every consumer gets it for free.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  // Lock body scroll while open so the board behind doesn't scroll through the
  // scrim (the classic modal "scroll bleed"). Restores the prior value so nested
  // or quickly-reopened modals don't clobber each other.
  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [open]);

  return (
    <AnimatePresence>
      {open && (
        <motion.div
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          // Drop pointer-events the instant the exit starts so clicks during the
          // fade-out reach the board instead of the still-mounted scrim.
          exit={{ opacity: 0, pointerEvents: "none" }}
          transition={{ duration: 0.18, ease: [0.2, 0, 0, 1] }}
          className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-ink/25 p-4 pt-[8vh] backdrop-blur-sm"
        >
          <div className="absolute inset-0" onClick={onClose} />
          <motion.div
            role="dialog"
            aria-modal="true"
            aria-labelledby={title ? titleId : undefined}
            initial={{ opacity: 0, y: 12, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 8, scale: 0.98, transition: { duration: 0.15, ease: [0.4, 0, 1, 1] } }}
            transition={{ type: "spring", stiffness: 320, damping: 30 }}
            className={`relative w-full rounded-2xl border border-hairline bg-surface p-5 shadow-[0_24px_60px_-20px_rgba(23,23,26,0.4)] ${className}`}
          >
            {title && (
              <div className="mb-5 flex items-center justify-between">
                <h2 id={titleId} className="text-[17px] font-semibold text-ink">{title}</h2>
                <button
                  onClick={onClose}
                  className="rounded-lg px-2 py-1 text-[13px] text-muted hover:bg-board"
                >
                  Esc
                </button>
              </div>
            )}
            {children}
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
