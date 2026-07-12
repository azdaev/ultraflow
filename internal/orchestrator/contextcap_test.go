package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncodeClaudeCwd(t *testing.T) {
	// "/" and "." both map to "-"; hyphens and case are preserved (matches the
	// real ~/.claude/projects layout).
	cases := map[string]string{
		"/Users/amady/.ultraflow/worktrees/c67fb5743fc32baa": "-Users-amady--ultraflow-worktrees-c67fb5743fc32baa",
		"/Users/x/.superset/wt-0c02":                         "-Users-x--superset-wt-0c02",
	}
	for in, want := range cases {
		if got := encodeClaudeCwd(in); got != want {
			t.Errorf("encodeClaudeCwd(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestLastContextTokens(t *testing.T) {
	// A transcript with two assistant turns plus lines that carry no usage. The
	// context size is the LAST usage line, summed across input + cache tokens.
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":100,"cache_creation_input_tokens":200,"cache_read_input_tokens":300}}}`,
		`{"type":"assistant","message":{"content":"no usage here"}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":1000,"cache_creation_input_tokens":2000,"cache_read_input_tokens":250000}}}`,
	}
	path := filepath.Join(t.TempDir(), "sess.jsonl")
	writeLines(t, path, lines)

	got, ok := lastContextTokens(path)
	if !ok {
		t.Fatal("lastContextTokens: ok=false, want true")
	}
	if want := 1000 + 2000 + 250000; got != want {
		t.Fatalf("lastContextTokens = %d; want %d (the last usage line)", got, want)
	}
}

func TestLastContextTokensNoUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sess.jsonl")
	writeLines(t, path, []string{`{"type":"user","message":{"role":"user","content":"hi"}}`})
	if _, ok := lastContextTokens(path); ok {
		t.Fatal("lastContextTokens on a usage-free transcript: ok=true, want false")
	}
}

// TestClaudeContextTokens exercises the full path: encode the cwd, find the newest
// transcript under a fake $HOME, and read its last usage.
func TestClaudeContextTokens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := "/Users/amady/.ultraflow/worktrees/abc123"
	proj := filepath.Join(home, ".claude", "projects", encodeClaudeCwd(dir))
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLines(t, filepath.Join(proj, "sess.jsonl"), []string{
		`{"type":"assistant","message":{"usage":{"input_tokens":5000,"cache_creation_input_tokens":0,"cache_read_input_tokens":195000}}}`,
	})

	got, ok := claudeContextTokens(dir)
	if !ok || got != 200_000 {
		t.Fatalf("claudeContextTokens = %d,%v; want 200000,true", got, ok)
	}

	// An unknown cwd (no transcript dir) reports not-found rather than erroring, so
	// the cap safely does nothing.
	if _, ok := claudeContextTokens("/nope/does/not/exist"); ok {
		t.Fatal("claudeContextTokens on a missing dir: ok=true, want false")
	}
}

func writeLines(t *testing.T, path string, lines []string) {
	t.Helper()
	data := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
