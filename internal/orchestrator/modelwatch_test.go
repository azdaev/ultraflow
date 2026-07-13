package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeSessionModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := "/Users/amady/.ultraflow/worktrees/abc123"
	proj := filepath.Join(home, ".claude", "projects", encodeClaudeCwd(dir))
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// The last line carrying a real model wins; synthetic placeholders are skipped,
	// and lines without a model (user turns, tool results) are ignored.
	writeLines(t, filepath.Join(proj, "sess.jsonl"), []string{
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","message":{"model":"claude-sonnet-4-5","usage":{"input_tokens":1}}}`,
		`{"type":"assistant","message":{"model":"<synthetic>","content":"x"}}`,
		`{"type":"assistant","message":{"model":"claude-opus-4-8","usage":{"input_tokens":2}}}`,
	})

	got, ok := claudeSessionModel(dir)
	if !ok || got != "claude-opus-4-8" {
		t.Fatalf("claudeSessionModel = %q,%v; want claude-opus-4-8,true", got, ok)
	}

	if _, ok := claudeSessionModel("/nope/does/not/exist"); ok {
		t.Fatal("claudeSessionModel on a missing dir: ok=true, want false")
	}
}

func TestCodexSessionModel(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	// The task's worktree — codex records this (canonicalized) as session_meta cwd.
	work := t.TempDir()
	canon, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}

	sess := filepath.Join(codexHome, "sessions", "2026", "07", "12")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}

	// A rollout for a DIFFERENT worktree — must be ignored even though it's present.
	writeLines(t, filepath.Join(sess, "rollout-other.jsonl"), []string{
		`{"type":"session_meta","payload":{"cwd":"/some/other/worktree"}}`,
		`{"type":"turn_context","payload":{"cwd":"/some/other/worktree","model":"gpt-5-codex"}}`,
	})
	// This task's rollout: the last turn_context model wins (a mid-session switch).
	writeLines(t, filepath.Join(sess, "rollout-mine.jsonl"), []string{
		`{"type":"session_meta","payload":{"cwd":"` + canon + `"}}`,
		`{"type":"turn_context","payload":{"cwd":"` + canon + `","model":"gpt-5"}}`,
		`{"type":"message","payload":{"text":"hi"}}`,
		`{"type":"turn_context","payload":{"cwd":"` + canon + `","model":"gpt-5.6-sol"}}`,
	})

	got, ok := codexSessionModel(work)
	if !ok || got != "gpt-5.6-sol" {
		t.Fatalf("codexSessionModel = %q,%v; want gpt-5.6-sol,true", got, ok)
	}

	// A worktree with no matching rollout reports not-found rather than picking an
	// unrelated session's model.
	if _, ok := codexSessionModel(t.TempDir()); ok {
		t.Fatal("codexSessionModel with no matching rollout: ok=true, want false")
	}
}

func TestCodexSessionModelDoesNotFallBackToOlderSession(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	work := t.TempDir()
	canon, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	sess := filepath.Join(codexHome, "sessions", "2026", "07", "13")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}

	old := filepath.Join(sess, "rollout-old.jsonl")
	writeLines(t, old, []string{
		`{"type":"session_meta","payload":{"cwd":"` + canon + `"}}`,
		`{"type":"turn_context","payload":{"model":"gpt-old"}}`,
	})
	newest := filepath.Join(sess, "rollout-new.jsonl")
	writeLines(t, newest, []string{
		`{"type":"session_meta","payload":{"cwd":"` + canon + `"}}`,
	})
	now := time.Now()
	if err := os.Chtimes(old, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newest, now, now); err != nil {
		t.Fatal(err)
	}

	if got, ok := codexSessionModel(work); ok || got != "" {
		t.Fatalf("codexSessionModel = %q,%v; want empty,false until the new session writes its model", got, ok)
	}
}
