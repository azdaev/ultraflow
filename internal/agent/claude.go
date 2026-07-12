package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Claude drives the `claude` CLI (Claude Code) headlessly, wired to Ultraflow's
// MCP server so the agent can call ask_human.
type Claude struct {
	mcpURL string
}

func NewClaude(mcpURL string) *Claude { return &Claude{mcpURL: mcpURL} }

func (c *Claude) Name() string { return "claude" }

// Command builds an INTERACTIVE claude session (a real TUI, not headless) for
// running inside a PTY so the human can watch and type. It seeds the initial
// task prompt but leaves the session live. Returns a cleanup that removes the
// temp MCP config; call it once the process has exited.
func (c *Claude) Command(ctx context.Context, dir, prompt string) (*exec.Cmd, func(), error) {
	cfgPath, err := c.writeMCPConfig()
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { os.Remove(cfgPath) }

	cmd := exec.CommandContext(ctx, "claude",
		prompt, // positional prompt: seed the task, then stay interactive
		// Add Ultraflow's ask_human server on top of the user's own MCP servers
		// (no --strict-mcp-config), so agents keep access to the human's full MCP set.
		"--mcp-config", cfgPath,
		"--permission-mode", "bypassPermissions",
	)
	if dir != "" {
		cmd.Dir = dir
	}
	// The user's real login-shell PATH (so nvm/homebrew/~/.local binaries
	// resolve) plus a TERM for the PTY's TUI rendering. See agentEnv.
	cmd.Env = agentEnv()
	return cmd, cleanup, nil
}

// ResumeCommand builds an interactive claude session that CONTINUES the task's
// prior conversation in dir (`claude --continue`) and seeds the human's review
// feedback as the next message. Because each task runs in its own worktree, only
// that task's conversation lives there, so --continue unambiguously resumes it —
// the agent keeps its memory of what it built. Same MCP wiring, PTY env, and
// cleanup contract as Command.
func (c *Claude) ResumeCommand(ctx context.Context, dir, prompt string) (*exec.Cmd, func(), error) {
	cfgPath, err := c.writeMCPConfig()
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { os.Remove(cfgPath) }

	cmd := exec.CommandContext(ctx, "claude",
		"--continue", prompt, // resume this worktree's conversation, send the feedback
		// Same as Command: keep the human's full MCP set alongside ask_human.
		"--mcp-config", cfgPath,
		"--permission-mode", "bypassPermissions",
	)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = agentEnv()
	return cmd, cleanup, nil
}

// writeMCPConfig writes a throwaway MCP config pointing Claude Code at the
// Ultraflow daemon, returning its path.
func (c *Claude) writeMCPConfig() (string, error) {
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"ultraflow": map[string]any{"type": "http", "url": c.mcpURL},
		},
	}
	f, err := os.CreateTemp("", "ultraflow-mcp-*.json")
	if err != nil {
		return "", err
	}
	if err := json.NewEncoder(f).Encode(cfg); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	return f.Name(), nil
}

func (c *Claude) Run(ctx context.Context, dir, prompt string, out chan<- Event) error {
	// Throwaway MCP config pointing Claude Code at the Ultraflow daemon (same wiring
	// as the interactive Command path).
	cfgPath, err := c.writeMCPConfig()
	if err != nil {
		return err
	}
	defer os.Remove(cfgPath)

	cmd := exec.CommandContext(ctx, "claude",
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		// Add Ultraflow's ask_human server on top of the user's own MCP servers
		// (no --strict-mcp-config), so agents keep access to the human's full MCP set.
		"--mcp-config", cfgPath,
		// Unattended: the agent runs in an isolated worktree, so it must not stall
		// on permission prompts (which in headless mode are auto-denied, leaving the
		// agent unable to run Bash/tests). This is the autonomous-orchestrator mode.
		"--permission-mode", "bypassPermissions",
		// Survive a momentarily overloaded primary model instead of failing the task.
		"--fallback-model", "sonnet",
	)
	if dir != "" {
		cmd.Dir = dir
	}
	// Resolve the user's real login-shell PATH so the CLI is found under launchd's
	// bare PATH (else `exit 127`). See agentEnv.
	cmd.Env = agentEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// Capture stderr (bounded) so a failure carries an explanation into the task
	// thread, while still echoing to the daemon log for live debugging.
	var errTail tailBuffer
	errTail.max = 2000
	cmd.Stderr = io.MultiWriter(os.Stderr, &errTail)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting claude (is the CLI installed and logged in?): %w", err)
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		for _, ev := range parseStreamLine(sc.Bytes()) {
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

// tailBuffer keeps only the last max bytes written to it — enough to explain a
// crash without unbounded memory if the agent is chatty on stderr.
type tailBuffer struct {
	b   []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.b) }

// parseStreamLine turns one Claude Code stream-json line into friendly Events
// (the activity strip / thread on the board). A single assistant message can
// carry several content blocks (e.g. a note plus a tool call), so it returns a
// slice — nil for lines with nothing worth surfacing (handshakes, empty deltas).
func parseStreamLine(line []byte) []Event {
	var m struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Result  string `json:"result"`
		Message struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return nil
	}

	var evs []Event
	switch m.Type {
	case "assistant":
		for _, part := range m.Message.Content {
			switch part.Type {
			case "tool_use":
				evs = append(evs, Event{Kind: "tool", Text: summarizeTool(part.Name, part.Input)})
			case "text":
				if t := strings.TrimSpace(part.Text); t != "" {
					evs = append(evs, Event{Kind: "message", Text: truncate(t, 240)})
				}
			}
		}
	case "result":
		if t := strings.TrimSpace(m.Result); t != "" {
			evs = append(evs, Event{Kind: "result", Text: truncate(t, 400)})
		}
	}
	return evs
}

// summarizeTool renders a compact "verb target" line for a tool call, e.g.
// "Edit internal/web/web.go" or "Bash go test ./...".
func summarizeTool(name string, input json.RawMessage) string {
	var in map[string]any
	_ = json.Unmarshal(input, &in)

	str := func(k string) string {
		if v, ok := in[k].(string); ok {
			return v
		}
		return ""
	}

	// Friendly labels for the common tools; MCP tools arrive as mcp__server__tool.
	label := name
	if strings.HasPrefix(name, "mcp__") {
		parts := strings.Split(name, "__")
		label = parts[len(parts)-1]
	}

	switch name {
	case "Bash":
		if cmd := str("command"); cmd != "" {
			return "Bash " + truncate(cmd, 120)
		}
	case "Edit", "Write", "Read", "NotebookEdit":
		if p := str("file_path"); p != "" {
			return label + " " + p
		}
	case "Grep":
		return "Grep " + str("pattern")
	case "Glob":
		return "Glob " + str("pattern")
	case "Task":
		return "Subagent " + str("description")
	}
	if strings.Contains(name, "ask_human") {
		return "asking you…"
	}
	if len(in) == 0 {
		return label
	}
	return label + " " + truncate(compactArgs(in), 100)
}

func compactArgs(in map[string]any) string {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys) // stable order so the activity line doesn't jitter
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, in[k]))
	}
	return strings.Join(parts, " ")
}

// truncate collapses newlines and caps a string at n runes (not bytes, so it
// never splits a multi-byte character), appending an ellipsis when it cuts.
func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
