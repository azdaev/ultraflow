import { useEffect, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { api, errMsg, type Project } from "../api";
import { Modal } from "./Modal";

interface Props {
  open: boolean;
  onClose: () => void;
  projects: Project[];
}

// Concurrency bounds mirror the server clamp (core.MinConcurrent/MaxConcurrentCap);
// GET /api/settings reports the live range so these are only the initial fallback.
const CONC_MIN = 1;
const CONC_MAX = 8;

// Context-budget presets, in tokens. 0 = off; the rest sit inside the server's
// 50k–1M band. 200k is the recommended default (see the section copy). Each
// running agent that crosses the chosen cap gets a /compact injected.
const CAP_PRESETS: { value: number; label: string }[] = [
  { value: 0, label: "Off" },
  { value: 100_000, label: "100k" },
  { value: 200_000, label: "200k" },
  { value: 300_000, label: "300k" },
  { value: 500_000, label: "500k" },
];

// Settings manages the parallel-agent limit, the context budget, and the
// registered projects. Selection controls use ink (never orange — that stays
// reserved for needs_human). Adding a project opens the OS-native folder picker
// via the daemon; the new project arrives over SSE.
export function Settings({ open, onClose, projects }: Props) {
  const [picking, setPicking] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // Whether the daemon can open a native folder dialog (macOS). null while the
  // setting loads; false → show the paste-the-path fallback field instead.
  const [nativePicker, setNativePicker] = useState<boolean | null>(null);
  const [pastePath, setPastePath] = useState("");

  // Parallel-agent limit. Loaded from the daemon when Settings opens; changes are
  // applied optimistically and POSTed, reflecting the server's clamped value.
  const [conc, setConc] = useState<number | null>(null);
  const [concMin, setConcMin] = useState(CONC_MIN);
  const [concMax, setConcMax] = useState(CONC_MAX);

  // Per-agent context budget in tokens (0 = off). null while it loads.
  const [cap, setCap] = useState<number | null>(null);
  const [telegramEnabled, setTelegramEnabled] = useState(false);
  const [telegramHasToken, setTelegramHasToken] = useState(false);
  const [telegramToken, setTelegramToken] = useState("");
  const [telegramUserId, setTelegramUserId] = useState("");
  const [telegramChatId, setTelegramChatId] = useState("");
  const [telegramSaving, setTelegramSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    setErr(null);
    let live = true;
    api
      .settings()
      .then((s) => {
        if (!live) return;
        setConc(s.maxConcurrent);
        setConcMin(s.maxConcurrentMin);
        setConcMax(s.maxConcurrentMax);
        setCap(s.contextCap);
        setNativePicker(s.nativePicker);
        setTelegramEnabled(s.telegram.enabled);
        setTelegramHasToken(s.telegram.hasToken);
        setTelegramToken("");
        setTelegramUserId(s.telegram.userId ? String(s.telegram.userId) : "");
        setTelegramChatId(s.telegram.chatId ? String(s.telegram.chatId) : "");
      })
      .catch(() => {
        /* leave the control disabled until it loads */
      });
    return () => {
      live = false;
    };
  }, [open]);

  async function changeConcurrency(next: number) {
    const clamped = Math.max(concMin, Math.min(concMax, next));
    if (conc === null || clamped === conc) return;
    const prev = conc;
    setConc(clamped); // optimistic
    setErr(null);
    try {
      const { maxConcurrent } = await api.setConcurrency(clamped);
      setConc(maxConcurrent); // server's clamped value wins
    } catch (e) {
      setConc(prev); // roll back on failure
      setErr(errMsg(e, "couldn't change parallel agents"));
    }
  }

  async function changeCap(next: number) {
    if (cap === null || next === cap) return;
    const prev = cap;
    setCap(next); // optimistic
    setErr(null);
    try {
      const { contextCap } = await api.setContextCap(next);
      setCap(contextCap); // server's clamped value wins
    } catch (e) {
      setCap(prev); // roll back on failure
      setErr(errMsg(e, "couldn't change the context budget"));
    }
  }

  async function chooseFolder() {
    if (picking) return;
    setPicking(true);
    setErr(null);
    try {
      await api.pickProject(); // new project (if any) arrives via SSE
    } catch (e) {
      setErr(errMsg(e, "couldn't open the folder picker"));
    } finally {
      setPicking(false);
    }
  }

  async function saveTelegram() {
    setTelegramSaving(true);
    setErr(null);
    try {
      const saved = await api.setTelegram({
        enabled: telegramEnabled,
        token: telegramToken.trim(),
        userId: Number(telegramUserId),
        chatId: Number(telegramChatId),
      });
      setTelegramHasToken(saved.hasToken);
      setTelegramToken("");
    } catch (e) {
      setErr(errMsg(e, "couldn't save Telegram settings"));
    } finally {
      setTelegramSaving(false);
    }
  }

  // addByPath registers a project from a pasted path (the fallback where no
  // native picker exists). The server validates it's a git repo; the new project
  // arrives via SSE.
  async function addByPath() {
    const path = pastePath.trim();
    if (picking || !path) return;
    setPicking(true);
    setErr(null);
    try {
      await api.addProject(path);
      setPastePath("");
    } catch (e) {
      setErr(errMsg(e, "couldn't add that folder"));
    } finally {
      setPicking(false);
    }
  }

  async function remove(p: Project) {
    setErr(null);
    try {
      await api.deleteProject(p.id); // removal arrives via SSE
    } catch (e) {
      setErr(errMsg(e, "couldn't remove the project"));
    }
  }

  // setLanding switches where the project's finished work lands (local merge vs
  // GitHub PR). The change arrives back over SSE (project_updated).
  async function setLanding(p: Project, landing: "local" | "pr") {
    if (p.landing === landing) return;
    setErr(null);
    try {
      await api.setProjectLanding(p.id, landing);
    } catch (e) {
      setErr(errMsg(e, "couldn't switch how this project lands"));
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="Settings" className="max-w-lg">
        {/* parallel agents */}
        <h3 className="eyebrow mb-2.5 text-muted">Parallel agents</h3>
        <div className="flex items-center justify-between rounded-lg border border-hairline bg-board px-3 py-2.5">
          <div className="min-w-0 pr-3">
            <div className="text-[14px] font-medium text-ink">
              How many run at once
            </div>
            <div className="text-[12px] leading-snug text-muted">
              All agents share one subscription rate limit, so higher isn't
              always faster.
            </div>
          </div>
          <Stepper
            value={conc}
            min={concMin}
            max={concMax}
            onChange={changeConcurrency}
          />
        </div>

        {/* context budget */}
        <h3 className="eyebrow mb-2.5 mt-6 text-muted">Context budget</h3>
        <div className="rounded-lg border border-hairline bg-board px-3 py-2.5">
          <div className="mb-2.5 flex items-start justify-between gap-3">
            <div className="min-w-0">
              <div className="text-[14px] font-medium text-ink">
                Compact when context gets big
              </div>
              <div className="text-[12px] leading-snug text-muted">
                Agents ship huge context windows, but quality and cost degrade long
                before they fill. Past this many tokens, Ultraflow has the agent
                summarize and carry on. 200k is a good balance; Off leaves it to the
                CLI. Claude only for now.
              </div>
            </div>
          </div>
          <PresetRow value={cap} presets={CAP_PRESETS} onChange={changeCap} />
        </div>

        {/* remote access */}
        <h3 className="eyebrow mb-2.5 mt-6 text-muted">Remote access</h3>
        <div className="rounded-lg border border-hairline bg-board px-3 py-3">
          <label className="flex items-start justify-between gap-4">
            <span>
              <span className="block text-[14px] font-medium text-ink">Telegram bot</span>
              <span className="block text-[12px] leading-snug text-muted">
                Get task updates and answer checkpoints from your phone, even away from this network.
              </span>
            </span>
            <input
              type="checkbox"
              checked={telegramEnabled}
              onChange={(e) => setTelegramEnabled(e.target.checked)}
              className="mt-1 h-4 w-4 accent-ink"
            />
          </label>
          <div className="mt-3 grid gap-2">
            <input
              type="password"
              value={telegramToken}
              onChange={(e) => setTelegramToken(e.target.value)}
              placeholder={telegramHasToken ? "Bot token saved — enter to replace" : "Bot token from @BotFather"}
              autoComplete="new-password"
              className="rounded-lg border border-hairline bg-surface px-3 py-2 text-[13px] text-ink placeholder:text-muted/60 focus:border-ink/40 focus:outline-none"
            />
            <div className="grid grid-cols-2 gap-2">
              <input type="text" inputMode="numeric" value={telegramUserId} onChange={(e) => setTelegramUserId(e.target.value)} placeholder="Your Telegram user ID" className="min-w-0 rounded-lg border border-hairline bg-surface px-3 py-2 text-[13px] text-ink placeholder:text-muted/60 focus:border-ink/40 focus:outline-none" />
              <input type="text" inputMode="numeric" value={telegramChatId} onChange={(e) => setTelegramChatId(e.target.value)} placeholder="Private chat ID" className="min-w-0 rounded-lg border border-hairline bg-surface px-3 py-2 text-[13px] text-ink placeholder:text-muted/60 focus:border-ink/40 focus:outline-none" />
            </div>
          </div>
          <div className="mt-3 flex items-center justify-between gap-3">
            <p className="text-[11px] leading-snug text-muted">Create a bot in @BotFather, send it /start, then add your numeric IDs. The token stays on this device.</p>
            <button onClick={() => void saveTelegram()} disabled={telegramSaving || (telegramEnabled && ((!telegramHasToken && !telegramToken.trim()) || !telegramUserId || !telegramChatId))} className="shrink-0 rounded-lg bg-ink px-3 py-2 text-[12px] font-semibold text-surface transition hover:bg-ink/85 disabled:opacity-40">
              {telegramSaving ? "Saving…" : "Save"}
            </button>
          </div>
        </div>

        {/* projects */}
        <h3 className="eyebrow mb-2.5 mt-6 text-muted">Projects</h3>
        <div className="flex flex-col gap-2">
          {projects.length === 0 && (
            <p className="rounded-lg border border-dashed border-hairline px-3 py-4 text-center text-[13px] text-muted/80">
              No projects yet. Choose a folder to add one.
            </p>
          )}
          {projects.map((p) => (
            <div
              key={p.id}
              className="flex items-center gap-2.5 rounded-lg border border-hairline bg-board px-3 py-2"
            >
              <span
                className="h-3 w-3 shrink-0 rounded-[4px]"
                style={{ backgroundColor: p.color }}
              />
              <div className="min-w-0 flex-1">
                <div className="text-[14px] font-medium text-ink">{p.name}</div>
                <div className="truncate font-mono text-[11px] text-muted">
                  {p.repoPath}
                </div>
              </div>
              {/* landing mode: where accepted work goes */}
              <div className="flex shrink-0 overflow-hidden rounded-md border border-hairline text-[11px] font-medium">
                {(["local", "pr"] as const).map((mode) => (
                  <button
                    key={mode}
                    onClick={() => setLanding(p, mode)}
                    title={
                      mode === "local"
                        ? "Merge into your checked-out branch — code on disk immediately"
                        : "Push the branch and merge a GitHub PR — your checkout is never touched"
                    }
                    className={
                      (p.landing ?? "local") === mode
                        ? "bg-ink px-2 py-1 text-board"
                        : "px-2 py-1 text-muted transition hover:bg-surface hover:text-ink"
                    }
                  >
                    {mode === "local" ? "Local" : "PR"}
                  </button>
                ))}
              </div>
              <button
                onClick={() => remove(p)}
                className="shrink-0 rounded-md px-2 py-1 text-[12px] font-medium text-muted transition hover:bg-surface hover:text-rust"
              >
                Remove
              </button>
            </div>
          ))}
        </div>

        {/* add project — native folder picker on macOS, paste-path fallback
            elsewhere (the daemon reports which via settings.nativePicker) */}
        {nativePicker !== false ? (
          <>
            <button
              onClick={chooseFolder}
              disabled={picking}
              className="mt-3 flex w-full items-center justify-center gap-2 rounded-lg border border-dashed border-ink/25 bg-surface px-3 py-3 text-[14px] font-semibold text-ink transition hover:border-ink/50 hover:bg-board disabled:opacity-60"
            >
              <FolderIcon />
              {picking ? "Choose a folder in Finder…" : "Choose a folder…"}
            </button>
            <p className="mt-2 text-[12px] text-muted">
              Opens your file browser — pick the project's git repo folder. Its
              name becomes the project name.
            </p>
          </>
        ) : (
          <>
            <form
              className="mt-3 flex gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                void addByPath();
              }}
            >
              <input
                type="text"
                value={pastePath}
                onChange={(e) => setPastePath(e.target.value)}
                placeholder="/home/you/code/my-repo"
                spellCheck={false}
                autoComplete="off"
                className="min-w-0 flex-1 rounded-lg border border-hairline bg-surface px-3 py-2.5 font-mono text-[13px] text-ink placeholder:text-muted/60 focus:border-ink/40 focus:outline-none"
              />
              <button
                type="submit"
                disabled={picking || pastePath.trim() === ""}
                className="shrink-0 rounded-lg border border-dashed border-ink/25 bg-surface px-4 py-2.5 text-[14px] font-semibold text-ink transition hover:border-ink/50 hover:bg-board disabled:opacity-40"
              >
                {picking ? "Adding…" : "Add"}
              </button>
            </form>
            <p className="mt-2 text-[12px] text-muted">
              Paste the absolute path to the project's git repo folder. Its name
              becomes the project name.
            </p>
          </>
        )}

        {err && <p className="mt-3 text-[13px] text-rust">{err}</p>}
    </Modal>
  );
}

// Stepper is a compact −/+ number control. value=null shows a placeholder while
// the current setting is still loading. The digit animates on change so a change
// registers even when the number lands one step away.
function Stepper({
  value,
  min,
  max,
  onChange,
}: {
  value: number | null;
  min: number;
  max: number;
  onChange: (n: number) => void;
}) {
  const atMin = value === null || value <= min;
  const atMax = value === null || value >= max;
  return (
    <div className="flex shrink-0 items-center gap-1 rounded-lg border border-hairline bg-surface p-1">
      <StepButton
        label="Fewer parallel agents"
        disabled={atMin}
        onClick={() => value !== null && onChange(value - 1)}
      >
        −
      </StepButton>
      <div className="relative h-6 w-7 overflow-hidden text-center">
        <AnimatePresence mode="popLayout" initial={false}>
          <motion.span
            key={value ?? "loading"}
            initial={{ opacity: 0, y: 6 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: -6 }}
            transition={{ type: "spring", stiffness: 420, damping: 32 }}
            className="absolute inset-0 grid place-items-center text-[15px] font-semibold tabular-nums text-ink"
          >
            {value ?? "–"}
          </motion.span>
        </AnimatePresence>
      </div>
      <StepButton
        label="More parallel agents"
        disabled={atMax}
        onClick={() => value !== null && onChange(value + 1)}
      >
        +
      </StepButton>
    </div>
  );
}

// PresetRow is a segmented control of context-budget presets. The active one gets
// an ink border/fill (never orange — that stays reserved for needs_human). value
// is null while the setting loads, leaving every option un-selected.
function PresetRow({
  value,
  presets,
  onChange,
}: {
  value: number | null;
  presets: { value: number; label: string }[];
  onChange: (n: number) => void;
}) {
  return (
    <div className="flex gap-1.5">
      {presets.map((p) => {
        const active = value === p.value;
        return (
          <button
            key={p.value}
            type="button"
            onClick={() => onChange(p.value)}
            aria-pressed={active}
            className={`flex-1 rounded-md border px-2 py-1.5 text-[13px] font-semibold tabular-nums transition ${
              active
                ? "border-ink bg-ink text-surface"
                : "border-hairline bg-surface text-ink hover:border-ink/40"
            }`}
          >
            {p.label}
          </button>
        );
      })}
    </div>
  );
}

function StepButton({
  label,
  disabled,
  onClick,
  children,
}: {
  label: string;
  disabled: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      disabled={disabled}
      onClick={onClick}
      className="grid h-7 w-7 place-items-center rounded-md text-[16px] font-semibold text-ink transition hover:bg-board disabled:opacity-30 disabled:hover:bg-transparent"
    >
      {children}
    </button>
  );
}

function FolderIcon() {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M4 5h5l2 2h9a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1z" />
    </svg>
  );
}
