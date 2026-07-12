import { useEffect, useRef } from "react";
import type { AttentionItem } from "./components/RailCard";
import { notify, requestNotificationPermission } from "./notify";

// A stable key + notification content for one attention item. needs_human keys on
// the request id (a re-ask is a fresh checkpoint); failures key on the task, which
// can only hold one at a time. The title is the task, the body the question/error
// — same information the rail card leads with.
function describe(item: AttentionItem): {
  key: string;
  taskId: string;
  title: string;
  body: string;
} {
  switch (item.type) {
    case "needs_human":
      return {
        key: `req:${item.request.id}`,
        taskId: item.request.taskId,
        title: item.task?.title ?? "A task needs you",
        body: item.request.question,
      };
    case "failed":
      return {
        key: `failed:${item.task.id}`,
        taskId: item.task.id,
        title: item.task.title,
        body: item.activity || "The agent gave up — needs you.",
      };
    case "merge_failed":
      return {
        key: `merge:${item.task.id}`,
        taskId: item.task.id,
        title: item.task.title,
        body: item.message || "Merge couldn't land — needs you.",
      };
  }
}

// useAttentionNotifications raises an OS notification whenever a NEW item enters
// the attention rail while the tab is unfocused, and clears it once the item
// leaves (checkpoint answered, failure retried). Clicking a notification focuses
// the tab and opens that task. It never fires on ordinary task_updated churn —
// its input is the rail set, which is exactly the needs_human + failure family.
export function useAttentionNotifications(
  attention: AttentionItem[],
  onOpen: (taskId: string) => void,
) {
  const seen = useRef(new Map<string, Notification | null>());
  const armed = useRef(false);
  const onOpenRef = useRef(onOpen);
  onOpenRef.current = onOpen;

  useEffect(() => {
    requestNotificationPermission();
    // Arm after the opening board snapshot has hydrated. Everything already on
    // the rail at load (or after a reconnect resync) is thus seeded silently —
    // only items that arrive *after* this point alert, so opening the board in a
    // background tab never dumps a burst of toasts for old, still-pending work.
    const t = setTimeout(() => {
      armed.current = true;
    }, 1000);
    return () => clearTimeout(t);
  }, []);

  useEffect(() => {
    const live = new Set<string>();
    for (const item of attention) {
      const { key, taskId, title, body } = describe(item);
      live.add(key);
      if (seen.current.has(key)) continue;

      // Notify only for genuinely new items (armed), and only when the human isn't
      // already looking: a focused tab has the rail on screen, so an OS
      // notification would just be noise. Anything before arming is seeded silently.
      const n =
        armed.current && !document.hasFocus()
          ? notify(title, body, key, () => onOpenRef.current(taskId))
          : null;
      seen.current.set(key, n);
    }

    // Anything that left the rail is resolved — close its notification so a
    // returning human doesn't find a stale toast for an already-answered ask.
    for (const [key, n] of seen.current) {
      if (!live.has(key)) {
        n?.close();
        seen.current.delete(key);
      }
    }
  }, [attention]);
}
