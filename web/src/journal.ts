// Front-end half of the verbose activity journal (see internal/journal). It POSTs
// UI interactions to /api/journal, where they land in the same JSONL file as the
// backend's task/agent events — so a couple of days of real use can be replayed as
// one timeline. Fire-and-forget: a journal failure must never affect the UI.

export function logUI(event: string, fields: Record<string, unknown> = {}) {
  try {
    const body = JSON.stringify({ event, ...fields });
    // keepalive lets the POST survive a navigation/tab-close (e.g. a click that
    // then unloads). Errors are swallowed — journaling is best-effort.
    void fetch("/api/journal", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
      keepalive: true,
    }).catch(() => {});
  } catch {
    /* ignore — never let logging throw into a handler */
  }
}

// describe reduces a clicked element to a short, human-readable label: prefer an
// explicit data-j marker, then title/aria-label, then trimmed text, then a
// tag.class fallback. Kept short so the journal stays greppable.
function describe(el: HTMLElement): string {
  const j = el.getAttribute("data-j");
  if (j) return j;
  const title = el.getAttribute("title") || el.getAttribute("aria-label");
  if (title) return title.trim().slice(0, 60);
  const text = (el.textContent || "").replace(/\s+/g, " ").trim();
  if (text) return text.slice(0, 60);
  const cls = typeof el.className === "string" && el.className ? "." + el.className.split(/\s+/)[0] : "";
  return el.tagName.toLowerCase() + cls;
}

// installClickJournal wires a single delegated listener that logs every click on
// an actionable element (button / link / role=button / [data-j]). It walks up from
// the target to the nearest such element so clicks on inner icons/spans still
// resolve to the control the user meant. Returns a cleanup fn.
export function installClickJournal(): () => void {
  const onClick = (e: MouseEvent) => {
    let el = e.target as HTMLElement | null;
    for (let depth = 0; el && depth < 6; el = el.parentElement, depth++) {
      const actionable =
        el.tagName === "BUTTON" ||
        el.tagName === "A" ||
        el.getAttribute("role") === "button" ||
        el.hasAttribute("data-j");
      if (actionable) {
        logUI("click", { label: describe(el), tag: el.tagName.toLowerCase() });
        return;
      }
    }
  };
  document.addEventListener("click", onClick, { capture: true });
  return () => document.removeEventListener("click", onClick, { capture: true });
}
