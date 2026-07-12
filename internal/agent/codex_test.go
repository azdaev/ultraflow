package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureTrusted checks that a worktree gets recorded as trusted in codex's
// config.toml (so the interactive TUI won't stall on its trust prompt), that the
// write is idempotent, and that an existing config is appended to, not clobbered.
func TestEnsureTrusted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	cfg := filepath.Join(home, "config.toml")
	if err := os.WriteFile(cfg, []byte("model = \"gpt-5\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := ensureTrusted(dir); err != nil {
		t.Fatalf("ensureTrusted: %v", err)
	}

	// EvalSymlinks matches what the adapter records (e.g. macOS /var → /private/var).
	abs := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		abs = r
	}
	want := "[projects.\"" + abs + "\"]"

	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, want) {
		t.Fatalf("config missing trust entry %q:\n%s", want, got)
	}
	if !strings.Contains(got, `trust_level = "trusted"`) {
		t.Fatalf("config missing trust_level:\n%s", got)
	}
	if !strings.Contains(got, `model = "gpt-5"`) {
		t.Fatalf("ensureTrusted clobbered existing config:\n%s", got)
	}

	// Idempotent: a second call must not add a duplicate table (codex rejects those).
	if err := ensureTrusted(dir); err != nil {
		t.Fatalf("ensureTrusted (2nd): %v", err)
	}
	data, _ = os.ReadFile(cfg)
	if n := strings.Count(string(data), want); n != 1 {
		t.Fatalf("expected exactly one trust entry, got %d", n)
	}
}

func TestParseCodexLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string // "" means the line should be dropped (ok=false)
		kind string
	}{
		{"agent message", `{"type":"item.completed","item":{"type":"agent_message","text":"done"}}`, "done", "message"},
		{"command", `{"type":"item.completed","item":{"type":"command_execution","command":"go test ./..."}}`, "Bash go test ./...", "tool"},
		{"file change", `{"type":"item.completed","item":{"type":"file_change","path":"main.go"}}`, "Edit main.go", "tool"},
		{"ask_human", `{"type":"item.completed","item":{"type":"mcp_tool_call","tool":"ultraflow__ask_human"}}`, "asking you…", "tool"},
		{"error", `{"type":"item.completed","item":{"type":"error","message":"boom"}}`, "boom", "error"},
		{"turn started dropped", `{"type":"turn.started"}`, "", ""},
		{"thread started dropped", `{"type":"thread.started","thread_id":"x"}`, "", ""},
		{"garbage dropped", `not json`, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev, ok := parseCodexLine([]byte(c.line))
			if c.want == "" {
				if ok {
					t.Fatalf("expected line dropped, got %+v", ev)
				}
				return
			}
			if !ok {
				t.Fatalf("expected an event, got none")
			}
			if ev.Text != c.want {
				t.Fatalf("text = %q, want %q", ev.Text, c.want)
			}
			if ev.Kind != c.kind {
				t.Fatalf("kind = %q, want %q", ev.Kind, c.kind)
			}
		})
	}
}
