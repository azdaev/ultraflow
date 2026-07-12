import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";

// The imperative handle a parent uses to drive the terminal without owning its
// WebSocket: `interrupt` sends a bare Esc to the agent (the soft "stop what you're
// doing" a Claude/Codex TUI understands) and refocuses so the user can keep typing.
export interface AgentTerminalHandle {
  interrupt: () => void;
}

// Translate a macOS text-editing combo that xterm.js won't emit on its own into
// the terminal byte sequence a readline-style TUI (Claude, Codex, any emacs-mode
// line editor) expects. xterm forwards plain keys and a fixed set of control keys
// via onData, but silently swallows Cmd/Option editing chords — so Cmd+Backspace,
// Cmd+←/→, Option+Backspace, Option+←/→ do nothing. Returns null when the event
// isn't one we remap (let xterm handle it as before).
function macEditSeq(e: KeyboardEvent): string | null {
  // Cmd combos: line-wise editing. Require metaKey and no Option so we don't
  // clobber word-wise (Option) or shadow copy/paste chords handled elsewhere.
  if (e.metaKey && !e.altKey && !e.ctrlKey) {
    switch (e.key) {
      case "Backspace":
        return "\x15"; // Ctrl-U — delete to line start ("cmd + delete")
      case "ArrowLeft":
        return "\x01"; // Ctrl-A — move to line start
      case "ArrowRight":
        return "\x05"; // Ctrl-E — move to line end
    }
  }
  // Option combos: word-wise editing. Require altKey and no Cmd; leave
  // Option+letter (compose / special chars) untouched.
  if (e.altKey && !e.metaKey && !e.ctrlKey) {
    switch (e.key) {
      case "Backspace":
        return "\x1b\x7f"; // ESC DEL — delete previous word
      case "ArrowLeft":
        return "\x1bb"; // ESC b — move word left
      case "ArrowRight":
        return "\x1bf"; // ESC f — move word right
    }
  }
  return null;
}

// AgentTerminal is a real, interactive terminal bound to the task's live agent
// PTY over a WebSocket: it renders the actual CLI output and sends keystrokes
// back (including Esc and Ctrl-C). xterm.js is the emulator; the Go daemon bridges
// the PTY (see internal/terminal + the /api/tasks/{id}/terminal WS endpoint).
export const AgentTerminal = forwardRef<AgentTerminalHandle, { taskId: string }>(
  function AgentTerminal({ taskId }, ref) {
    const elRef = useRef<HTMLDivElement>(null);
    // Kept in refs so the imperative handle can reach the live socket/terminal
    // without re-running the setup effect.
    const wsRef = useRef<WebSocket | null>(null);
    const termRef = useRef<Terminal | null>(null);
    const encRef = useRef(new TextEncoder());

    // send writes raw bytes to the PTY if the socket is up (no-op otherwise).
    const send = (data: string) => {
      const ws = wsRef.current;
      if (ws && ws.readyState === WebSocket.OPEN) ws.send(encRef.current.encode(data));
    };

    useImperativeHandle(ref, () => ({
      // \x1b is Esc — the agent's interrupt. Refocus after so the user can keep
      // typing into the terminal without a second click.
      interrupt: () => {
        send("\x1b");
        termRef.current?.focus();
      },
    }));

    useEffect(() => {
      const el = elRef.current;
      if (!el) return;

      const term = new Terminal({
        fontFamily: "'JetBrains Mono', ui-monospace, monospace",
        fontSize: 12,
        lineHeight: 1.2,
        cursorBlink: true,
        theme: { background: "#17171A", foreground: "#ECECEA", cursor: "#F5501E" },
      });
      termRef.current = term;
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(el);
      fit.fit();

      // This handler runs ONLY while the terminal is focused (xterm listens on its
      // own textarea), so "focused" is implicit here — that's what makes Esc
      // routing correct: when you're typing in the terminal, its keys are yours.
      //   • Esc — you're in the terminal, so it's the agent's interrupt: forward it
      //     to the PTY (a Claude/Codex TUI reads Esc as "stop the current turn").
      //     stopPropagation keeps it from ALSO reaching the card's window-level Esc
      //     handler, so interrupting doesn't slam the card shut at the same time.
      //     (When the terminal is NOT focused this handler never runs, the keydown
      //     bubbles to the window, and Esc closes the card as one would expect.)
      //   • Ctrl/Cmd-C with a selection — you're copying output, not asking to
      //     SIGINT the agent. Copy it and stay out of the PTY. With no selection,
      //     Ctrl-C falls through as a real interrupt (as the header advertises).
      term.attachCustomKeyEventHandler((e) => {
        if (e.type !== "keydown") return true;
        if (e.key === "Escape") {
          e.stopPropagation();
          return true;
        }
        const copyCombo = (e.ctrlKey || e.metaKey) && (e.key === "c" || e.key === "C");
        if (copyCombo && term.hasSelection() && navigator.clipboard) {
          navigator.clipboard.writeText(term.getSelection()).catch(() => {});
          return false;
        }
        // Cmd/Option editing chords xterm won't forward — translate to the byte
        // sequence the agent's line editor reads, and preventDefault so the browser
        // doesn't also act on Cmd+←/→ (history nav) or Cmd+Backspace.
        const seq = macEditSeq(e);
        if (seq !== null) {
          e.preventDefault();
          send(seq);
          return false;
        }
        return true;
      });

      const enc = encRef.current;
      let ws: WebSocket | null = null;
      let disposed = false;
      let attempts = 0;
      let reconnectTimer: number | undefined;

      const sendResize = () => {
        try {
          fit.fit();
        } catch {
          return;
        }
        if (ws && ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ resize: { rows: term.rows, cols: term.cols } }));
        }
      };

      const connect = () => {
        const proto = location.protocol === "https:" ? "wss" : "ws";
        const sock = new WebSocket(`${proto}://${location.host}/api/tasks/${taskId}/terminal`);
        sock.binaryType = "arraybuffer";
        ws = sock;
        wsRef.current = sock;

        sock.onopen = () => {
          // A reconnect re-attaches from scratch and the daemon replays the whole
          // scrollback — clear first so it repaints instead of doubling up.
          if (attempts > 0) {
            term.reset();
            term.write("\x1b[2m— reconnected —\x1b[0m\r\n");
          }
          attempts = 0;
          sendResize();
        };
        sock.onmessage = (e) => {
          if (typeof e.data === "string") term.write(e.data);
          else term.write(new Uint8Array(e.data));
        };
        sock.onclose = (e) => {
          if (disposed) return;
          // A clean close (code 1000) is the agent's session actually ending — final.
          // Anything else is a transient drop while the agent is still running, so
          // reconnect and replay rather than falsely declaring the session over.
          if (e.code === 1000) {
            term.write("\r\n\x1b[2m— session ended —\x1b[0m\r\n");
            return;
          }
          if (attempts >= 5) {
            term.write("\r\n\x1b[31m— lost the agent terminal —\x1b[0m\r\n");
            return;
          }
          const delay = Math.min(1000 * 2 ** attempts, 8000);
          attempts++;
          term.write("\r\n\x1b[2m— connection dropped, reconnecting… —\x1b[0m\r\n");
          reconnectTimer = window.setTimeout(connect, delay);
        };
        // onerror is always followed by onclose, which owns the reconnect decision.
      };

      connect();

      const data = term.onData((d) => {
        if (ws && ws.readyState === WebSocket.OPEN) ws.send(enc.encode(d));
      });

      const ro = new ResizeObserver(() => sendResize());
      ro.observe(el);

      return () => {
        disposed = true;
        if (reconnectTimer) window.clearTimeout(reconnectTimer);
        ro.disconnect();
        data.dispose();
        if (ws) {
          ws.onclose = null; // don't let teardown look like a drop and reconnect
          ws.close();
        }
        wsRef.current = null;
        termRef.current = null;
        term.dispose();
      };
    }, [taskId]);

    return <div ref={elRef} className="h-full w-full" />;
  },
);
