import { useEffect } from "react";

let locks = 0;
let previousOverflow = "";

function acquireBodyScrollLock() {
  if (locks === 0) {
    previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
  }
  locks += 1;
}

function releaseBodyScrollLock() {
  if (locks === 0) return;
  locks -= 1;
  if (locks === 0) {
    document.body.style.overflow = previousOverflow;
    previousOverflow = "";
  }
}

// useBodyScrollLock freezes background page scroll while `active` is true, so an
// open overlay doesn't let the board behind it scroll through (the classic modal
// "scroll bleed"). A shared reference count keeps the body locked until the last
// stacked overlay closes and restores the pre-lock inline style exactly once.
// Shared by Modal and the TaskDetail drawer so every full-screen surface behaves
// the same.
export function useBodyScrollLock(active: boolean) {
  useEffect(() => {
    if (!active) return;
    acquireBodyScrollLock();
    return releaseBodyScrollLock;
  }, [active]);
}
