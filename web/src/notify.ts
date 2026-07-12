// Web Notifications so a backgrounded Ultraflow tab still alerts the human when a
// task needs them. Everything here degrades to a silent no-op when the browser
// lacks support or the human denied permission — the in-page attention rail
// stays the primary surface either way.

export function requestNotificationPermission(): void {
  if (!("Notification" in window)) return;
  // Only the first, undecided visit prompts. A prior grant or denial is honoured
  // silently — re-prompting a denied user does nothing, and re-prompting a
  // granted one is noise.
  if (Notification.permission === "default") {
    Notification.requestPermission().catch(() => {});
  }
}

// notify raises one OS notification and returns it (or null when we can't). The
// tag dedupes: re-notifying the same tag replaces the prior toast rather than
// stacking. Clicking runs onClick (used to focus the tab and open the task).
export function notify(
  title: string,
  body: string,
  tag: string,
  onClick: () => void,
): Notification | null {
  if (!("Notification" in window) || Notification.permission !== "granted") {
    return null;
  }
  try {
    const n = new Notification(title, { body, tag });
    n.onclick = () => {
      window.focus();
      onClick();
      n.close();
    };
    return n;
  } catch {
    // Some browsers refuse the direct constructor (e.g. Android wants a service
    // worker). Fail silent rather than break the board.
    return null;
  }
}
