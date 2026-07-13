import type { Project, Task, TaskStatus } from "./api";

// --- pipeline grouping (shared by both board layouts) ---

export interface Columns {
  backlog: Task[];
  running: Task[];
  review: Task[];
  done: Task[];
}

// groupColumns maps task status onto the four pipeline stages. A task that needs
// the human STAYS in its real stage (Running) and is mirrored into the rail;
// a queued task (waiting for a concurrency slot) sits in Backlog as "Ready".
export function groupColumns(tasks: Task[]): Columns {
  const cols: Columns = { backlog: [], running: [], review: [], done: [] };
  for (const t of tasks) {
    switch (t.status) {
      case "backlog":
      case "queued":
        cols.backlog.push(t);
        break;
      case "running":
      case "needs_human":
      case "merging":
        cols.running.push(t);
        break;
      case "review":
        cols.review.push(t);
        break;
      case "done":
      case "cancelled":
        // A cancelled task is closed, like done — park it in the Done column so it
        // stays visible (and archivable) rather than vanishing off the board.
        cols.done.push(t);
        break;
      // failed: surfaced in the attention rail, not a column.
    }
  }
  return cols;
}

// --- status classification (the single TS source of truth, co-located with the
// grouping above; kept in sync with the daemon's groupings in
// internal/core/service.go). ---

// CANCELLABLE — a task with a live (or about-to-be-live) agent can be Stopped.
// Mirrors the daemon's cancellableStatuses.
export const CANCELLABLE = new Set<TaskStatus>(["queued", "running", "needs_human"]);

// DELETABLE — a not-live task (backlog, or terminal) can be Removed. Mirrors the
// daemon's deletableStatuses.
export const DELETABLE = new Set<TaskStatus>(["backlog", "done", "failed", "cancelled"]);

// DEV_LINK_STATUSES — stages where a task still holds its reserved dev port and the
// server may be up (the port is freed on merge/mark-done/failure).
export const DEV_LINK_STATUSES = new Set<TaskStatus>(["running", "needs_human", "merging", "review"]);

// CLOSED — a closed card (done or cancelled) reads muted; its work is finished, so
// it recedes rather than competing with live cards for attention.
export const CLOSED = new Set<TaskStatus>(["done", "cancelled"]);

// IN_FLIGHT — the daemon is actively working this task (an agent running, parked on
// a human checkpoint, or merging). Drives the TopBar "running" count.
export const IN_FLIGHT = new Set<TaskStatus>(["running", "needs_human", "merging"]);

// WAITING — accepted but not yet picked up by a free slot. Drives the "queued" count.
export const WAITING = new Set<TaskStatus>(["queued", "backlog"]);

// --- flows: presets that double as templates (see spec/flows / web.md). The
// backend tracks flow *name*, not per-step progress (that lands in M2), so the
// stepper derives an approximate active step from task status. ---

export interface FlowDef {
  key: string;
  label: string;
  steps: string[];
  // available=false means the flow is designed but the engine (M2) can't run it
  // yet; the orchestrator normalizes it to solo. The Composer shows it disabled so
  // the picker doesn't imply multi-step orchestration that doesn't exist.
  available: boolean;
}

export const FLOWS: Record<string, FlowDef> = {
  solo: { key: "solo", label: "Solo", steps: ["build"], available: true },
  "plan-build": { key: "plan-build", label: "Plan → Build", steps: ["plan", "build"], available: true },
  "plan-build-critic-gate": {
    key: "plan-build-critic-gate",
    label: "Plan → Build → Critic → Gate",
    steps: ["plan", "build", "critic", "gate"],
    available: true,
  },
  tdd: {
    key: "tdd",
    label: "TDD + critic loop",
    steps: ["tests", "critic", "code", "run", "review"],
    available: false,
  },
  "frontend-visual": {
    key: "frontend-visual",
    label: "Frontend + visual gate",
    steps: ["build", "visual"],
    available: false,
  },
};

export function flowOf(name: string): FlowDef {
  return FLOWS[name] ?? { key: name, label: name, steps: ["build"], available: true };
}

// activeStep maps a task's coarse status onto its flow steps. -1 = not started,
// steps.length = all done.
export function activeStep(status: TaskStatus, steps: string[]): number {
  switch (status) {
    case "backlog":
    case "queued":
      return -1;
    case "done":
    case "merging":
      return steps.length;
    case "review":
      return steps.length - 1;
    default:
      return 0; // running / needs_human / failed — mid-flight
  }
}

// --- agents ---

// available=false means the adapter isn't wired yet (M3); the orchestrator would
// normalize the task to Claude, so the Composer shows it disabled rather than
// letting a card claim it ran an agent it didn't.
export const AGENTS = [
  { key: "claude", label: "Claude Code", color: "var(--color-claude)", available: true },
  { key: "codex", label: "Codex", color: "var(--color-codex)", available: true },
  { key: "opencode", label: "opencode", color: "var(--color-opencode)", available: false },
];

export function agentColor(name: string): string {
  return AGENTS.find((a) => a.key === name)?.color ?? "var(--color-muted)";
}

export function agentLabel(name: string): string {
  return AGENTS.find((a) => a.key === name)?.label ?? name;
}

// friendlyModel turns a raw model id from an agent's transcript into a short
// display name for the card footer: "claude-opus-4-8" → "Opus 4.8",
// "gpt-5.6-sol" → "GPT-5.6 Sol". It degrades gracefully — an id it doesn't
// recognise is returned unchanged (never blank), so a new model still shows
// *something* sensible rather than falling back to the generic provider label.
export function friendlyModel(raw: string): string {
  const s = raw.trim();
  if (!s) return raw;

  // Claude: "claude-opus-4-8" / "claude-haiku-4-5-20251001" → "Opus 4.8".
  if (s.startsWith("claude-")) {
    let rest = s.slice("claude-".length);
    rest = rest.replace(/-\d{8}$/, ""); // strip a trailing date segment (…-20251001)
    const parts = rest.split("-");
    const family = titleCase(parts[0]);
    const version = parts.slice(1).join("."); // 4-8 → 4.8
    return version ? `${family} ${version}` : family;
  }

  // Claude CLI shorthands (--fallback-model): "opus" / "sonnet" / "haiku".
  if (s === "opus" || s === "sonnet" || s === "haiku") return titleCase(s);

  // OpenAI / Codex: "gpt-5" → "GPT-5", "gpt-5-codex" → "GPT-5 Codex",
  // "gpt-5.6-sol" → "GPT-5.6 Sol". First two tokens (GPT + version) hyphenate;
  // any trailing words are title-cased and space-joined.
  if (s.startsWith("gpt-") || s === "gpt") {
    const [, version, ...tail] = s.split("-");
    let out = version ? `GPT-${version}` : "GPT";
    if (tail.length) out += " " + tail.map(titleCase).join(" ");
    return out;
  }

  return raw;
}

function titleCase(s: string): string {
  return s ? s[0].toUpperCase() + s.slice(1) : s;
}

// --- projects ---

// projectMap indexes registered projects by name for O(1) chip/lane lookup.
// A task's `project` field holds the project name (or is blank / unregistered).
export function projectMap(projects: Project[]): Map<string, Project> {
  const m = new Map<string, Project>();
  for (const p of projects) m.set(p.name, p);
  return m;
}

// --- time ---

export function elapsed(fromISO: string, now: number): string {
  const start = new Date(fromISO).getTime();
  let s = Math.max(0, Math.floor((now - start) / 1000));
  const h = Math.floor(s / 3600);
  s -= h * 3600;
  const m = Math.floor(s / 60);
  s -= m * 60;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${String(s).padStart(2, "0")}s`;
  return `${s}s`;
}

export function ago(fromISO: string, now: number): string {
  const start = new Date(fromISO).getTime();
  const s = Math.max(0, Math.floor((now - start) / 1000));
  if (s < 60) return "just now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

// copyText writes to the clipboard, falling back to a hidden textarea when the
// async Clipboard API is unavailable (insecure origin / older browsers). Used
// by right-click menus to copy IDs and paths.
export function copyText(text: string): void {
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(text).catch(() => fallbackCopy(text));
    return;
  }
  fallbackCopy(text);
}

function fallbackCopy(text: string): void {
  const el = document.createElement("textarea");
  el.value = text;
  el.style.position = "fixed";
  el.style.opacity = "0";
  document.body.appendChild(el);
  el.select();
  try {
    document.execCommand("copy");
  } catch {
    /* best effort */
  }
  document.body.removeChild(el);
}
