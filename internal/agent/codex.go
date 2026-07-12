package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Codex drives the `codex` CLI (OpenAI Codex) headlessly and interactively,
// wired to Ultraflow's MCP server so the agent can call ask_human. It is the
// second adapter after Claude and follows the same contract: an interactive PTY
// session the human can watch and type into, plus a headless streaming Run.
//
// Two codex-specific wrinkles are handled here so the agent runs unattended:
//   - MCP wiring uses an inline `-c mcp_servers.ultraflow.url=…` config override
//     rather than a temp file, so nothing is left on disk and the user's global
//     ~/.codex/config.toml is untouched.
//   - The interactive TUI asks "Do you trust this directory?" on first run in a
//     new folder — a blocking prompt that even --dangerously-bypass… does not
//     skip. Each worktree is a brand-new path, so we pre-trust it in config.toml
//     before launching (see ensureTrusted); otherwise the agent stalls forever.
type Codex struct {
	mcpURL string
}

func NewCodex(mcpURL string) *Codex { return &Codex{mcpURL: mcpURL} }

func (c *Codex) Name() string { return "codex" }

// interactiveArgs are the flags shared by the fresh and send-back interactive
// sessions: bypass all approval/sandbox prompts (the agent runs unattended in an
// isolated worktree, so it must never stall — the direct analogue of Claude's
// bypassPermissions), and register the Ultraflow MCP server inline.
func (c *Codex) interactiveArgs() []string {
	return []string{
		// Unattended: run without approval prompts or sandboxing. The worktree is
		// the isolation boundary, mirroring the Claude adapter's bypassPermissions.
		"--dangerously-bypass-approvals-and-sandbox",
		// Point Codex at the Ultraflow daemon's MCP (for ask_human / finish_task)
		// via a runtime config override — no temp file, global config untouched.
		"-c", fmt.Sprintf("mcp_servers.ultraflow.url=%q", c.mcpURL),
	}
}

// Command builds an INTERACTIVE codex session (a real TUI, not headless) for
// running inside a PTY so the human can watch and type. The positional prompt is
// auto-submitted by codex and then the session stays live. Cleanup is a no-op
// (no temp files), kept for parity with the interactiveAgent contract.
func (c *Codex) Command(ctx context.Context, dir, prompt string) (*exec.Cmd, func(), error) {
	if err := ensureTrusted(dir); err != nil {
		return nil, func() {}, fmt.Errorf("pre-trusting the codex workdir: %w", err)
	}
	args := append(c.interactiveArgs(), prompt) // positional prompt: seeded and auto-run
	cmd := exec.CommandContext(ctx, "codex", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	// The user's real login-shell PATH (nvm's codex/node live there — the reason
	// codex died with exit 127 under launchd's bare PATH) plus a TERM for the
	// PTY's TUI rendering. See agentEnv.
	cmd.Env = agentEnv()
	return cmd, func() {}, nil
}

// ResumeCommand re-engages a task's codex agent after review. Unlike Claude's
// `--continue`, codex's `resume --last` cannot take a prompt (the flag conflicts
// with the positional), so rather than restoring the prior conversation this
// starts a FRESH session in the same worktree. That's still coherent: every file
// the agent wrote is right there in the worktree, and buildRevisePrompt restates
// the feedback and finish contract self-containedly. Same trust/MCP/PTY wiring as
// Command.
func (c *Codex) ResumeCommand(ctx context.Context, dir, prompt string) (*exec.Cmd, func(), error) {
	return c.Command(ctx, dir, prompt)
}

// Run executes codex headlessly (`codex exec --json`) and streams parsed events.
// Not used by the solo flow (which runs interactively via Command), but required
// by the Agent interface and available for future non-interactive flows. exec
// mode does not show the trust prompt, so no pre-trust is needed here; we do
// allow running outside a git repo so a non-git shared workdir still works.
func (c *Codex) Run(ctx context.Context, dir, prompt string, out chan<- Event) error {
	cmd := exec.CommandContext(ctx, "codex", "exec",
		"--json",
		"--dangerously-bypass-approvals-and-sandbox",
		"--skip-git-repo-check",
		"-c", fmt.Sprintf("mcp_servers.ultraflow.url=%q", c.mcpURL),
		prompt,
	)
	if dir != "" {
		cmd.Dir = dir
	}
	// Real login-shell PATH so nvm's codex/node resolve under launchd. See agentEnv.
	cmd.Env = agentEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var errTail tailBuffer
	errTail.max = 2000
	cmd.Stderr = io.MultiWriter(os.Stderr, &errTail)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting codex (is the CLI installed and logged in?): %w", err)
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if ev, ok := parseCodexLine(sc.Bytes()); ok {
			out <- ev
		}
	}
	if err := cmd.Wait(); err != nil {
		if tail := strings.TrimSpace(errTail.String()); tail != "" {
			return fmt.Errorf("%w — %s", err, truncate(tail, 300))
		}
		return err
	}
	return nil
}

// trustMu serializes the read-check-append in ensureTrusted so two agents
// starting at once can't both append a duplicate [projects."…"] table (which
// codex would reject) or interleave a write.
var trustMu sync.Mutex

// ensureTrusted makes sure codex's interactive TUI will not stop on its
// "Do you trust the contents of this directory?" prompt for dir, by recording
// the directory as trusted in $CODEX_HOME/config.toml (default ~/.codex) — the
// same mechanism codex uses when the user answers the prompt by hand. It is
// idempotent: if the directory is already listed it does nothing.
func ensureTrusted(dir string) error {
	if dir == "" {
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	// codex canonicalizes its working root, so resolve symlinks to match the path
	// it actually records (e.g. macOS /var → /private/var); otherwise the trust
	// entry wouldn't match and the prompt would still appear.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}

	home := os.Getenv("CODEX_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		home = filepath.Join(h, ".codex")
	}
	cfgPath := filepath.Join(home, "config.toml")
	header := fmt.Sprintf("[projects.%q]", abs) // matches codex's own `[projects."/abs/path"]`

	trustMu.Lock()
	defer trustMu.Unlock()

	if data, err := os.ReadFile(cfgPath); err == nil && strings.Contains(string(data), header) {
		return nil // already trusted
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(cfgPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(fmt.Sprintf("\n%s\ntrust_level = \"trusted\"\n", header))
	return err
}

// parseCodexLine turns one `codex exec --json` event line into a friendly Event
// for the activity strip. Codex emits a small set of typed items on
// item.completed; anything without surfacing value (turn.started, thread.started,
// usage) returns ok=false.
func parseCodexLine(line []byte) (Event, bool) {
	var m struct {
		Type string `json:"type"`
		Item struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Command string `json:"command"`
			Path    string `json:"path"`
			Name    string `json:"name"`
			Server  string `json:"server"`
			Tool    string `json:"tool"`
			Message string `json:"message"`
		} `json:"item"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return Event{}, false
	}
	if m.Type != "item.completed" {
		return Event{}, false
	}
	it := m.Item
	switch it.Type {
	case "agent_message":
		if t := strings.TrimSpace(it.Text); t != "" {
			return Event{Kind: "message", Text: truncate(t, 240)}, true
		}
	case "command_execution":
		if c := strings.TrimSpace(it.Command); c != "" {
			return Event{Kind: "tool", Text: "Bash " + truncate(c, 120)}, true
		}
	case "file_change":
		if p := strings.TrimSpace(it.Path); p != "" {
			return Event{Kind: "tool", Text: "Edit " + p}, true
		}
	case "mcp_tool_call":
		name := strings.TrimSpace(it.Tool)
		if name == "" {
			name = strings.TrimSpace(it.Name)
		}
		if strings.Contains(name, "ask_human") {
			return Event{Kind: "tool", Text: "asking you…"}, true
		}
		if name != "" {
			return Event{Kind: "tool", Text: name}, true
		}
	case "error":
		if t := strings.TrimSpace(it.Message); t != "" {
			return Event{Kind: "error", Text: truncate(t, 400)}, true
		}
	}
	return Event{}, false
}
