import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";

// AgentTerminal is a real, interactive terminal bound to the task's live agent
// PTY over a WebSocket: it renders the actual CLI output and sends keystrokes
// back (including Ctrl-C). xterm.js is the emulator; the Go daemon bridges the
// PTY (see internal/terminal + the /api/tasks/{id}/terminal WS endpoint).
export function AgentTerminal({ taskId }: { taskId: string }) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    const term = new Terminal({
      fontFamily: "'JetBrains Mono', ui-monospace, monospace",
      fontSize: 12,
      lineHeight: 1.2,
      cursorBlink: true,
      theme: { background: "#17171A", foreground: "#ECECEA", cursor: "#F5501E" },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    fit.fit();

    const proto = location.protocol === "https:" ? "wss" : "ws";
    const ws = new WebSocket(`${proto}://${location.host}/api/tasks/${taskId}/terminal`);
    ws.binaryType = "arraybuffer";
    const enc = new TextEncoder();

    const sendResize = () => {
      try {
        fit.fit();
      } catch {
        return;
      }
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ resize: { rows: term.rows, cols: term.cols } }));
      }
    };

    ws.onopen = sendResize;
    ws.onmessage = (e) => {
      if (typeof e.data === "string") term.write(e.data);
      else term.write(new Uint8Array(e.data));
    };
    ws.onclose = () => term.write("\r\n\x1b[2m— session ended —\x1b[0m\r\n");
    ws.onerror = () => term.write("\r\n\x1b[31m— couldn't connect to the agent terminal —\x1b[0m\r\n");

    const data = term.onData((d) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(enc.encode(d));
    });

    const ro = new ResizeObserver(() => sendResize());
    ro.observe(el);

    return () => {
      ro.disconnect();
      data.dispose();
      ws.close();
      term.dispose();
    };
  }, [taskId]);

  return <div ref={ref} className="h-full w-full" />;
}
