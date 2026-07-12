package orchestrator

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ultraflow/internal/terminal"
)

// modelPoll is how often watchModel re-reads the agent's transcript looking for
// the model it's actually running. A few seconds is plenty: the model is stable
// within a session, and PublishModel de-dupes, so a tight poll costs nothing but
// catches the first reading (and any mid-session model change, e.g. Claude's
// --fallback-model kicking in) quickly.
const modelPoll = 3 * time.Second

// codexRolloutMaxAge bounds the codex-transcript scan: only rollout files touched
// recently can belong to a task that's running right now, so older files are
// skipped without opening them.
const codexRolloutMaxAge = 12 * time.Hour

// watchModel detects the model an agent is actually running and surfaces it to
// the board, so the card footer shows "Opus 4.8" / "GPT-5.6 Sol" instead of the
// generic "Claude Code" / "Codex" provider label. It reads the model from the
// agent's own on-disk transcript — the same mechanism the context meter uses —
// and PublishModel's it. Started per attempt alongside watchIdle/watchContext so
// a fallback-to-sonnet retry re-detects. A no-op when dir is unknown; stops when
// the session ends.
func (o *Orchestrator) watchModel(sess *terminal.Session, taskID, dir string, isClaude bool) {
	if dir == "" {
		return
	}
	read := func() (string, bool) {
		if isClaude {
			return claudeSessionModel(dir)
		}
		return codexSessionModel(dir)
	}
	// Try once right away so the label flips as soon as the transcript exists,
	// then keep polling on a ticker for the rest of the session.
	if m, ok := read(); ok {
		o.svc.PublishModel(taskID, m)
	}
	ticker := time.NewTicker(modelPoll)
	defer ticker.Stop()
	for {
		select {
		case <-sess.Done():
			return
		case <-ticker.C:
			if m, ok := read(); ok {
				o.svc.PublishModel(taskID, m)
			}
		}
	}
}

// claudeSessionModel returns the model of the claude session running in dir, read
// from Claude Code's own JSONL transcript (the same file the context meter polls).
// It returns the model of the most recent line that carries one, so it reflects
// what actually ran incl. a mid-session --fallback-model switch. Synthetic
// placeholder values (e.g. "<synthetic>") are skipped so they never reach the UI.
func claudeSessionModel(dir string) (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	proj := filepath.Join(home, ".claude", "projects", encodeClaudeCwd(dir))
	f, ok := newestJSONL(proj)
	if !ok {
		return "", false
	}
	file, err := os.Open(f)
	if err != nil {
		return "", false
	}
	defer file.Close()

	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	model, found := "", false
	for sc.Scan() {
		var line struct {
			Message struct {
				Model string `json:"model"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if m := line.Message.Model; m != "" && !strings.HasPrefix(m, "<") {
			model, found = m, true
		}
	}
	return model, found
}

// codexSessionModel returns the model of the codex session running in dir. Codex
// writes one rollout JSONL per session under $CODEX_HOME/sessions/YYYY/MM/DD/
// rollout-*.jsonl; line 0 is a session_meta carrying payload.cwd, and each
// turn_context line carries payload.model. We walk the rollouts newest-first,
// match the one whose cwd is this task's worktree, and return the last model it
// recorded. Cost is bounded: files older than codexRolloutMaxAge are skipped, and
// the scan stops at the first cwd match.
func codexSessionModel(dir string) (string, bool) {
	canon := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		canon = resolved // codex records a canonicalized cwd
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		home = filepath.Join(h, ".codex")
	}
	root := filepath.Join(home, "sessions")

	type rollout struct {
		path string
		mod  time.Time
	}
	var rollouts []rollout
	cutoff := time.Now().Add(-codexRolloutMaxAge)
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			return nil
		}
		rollouts = append(rollouts, rollout{path, info.ModTime()})
		return nil
	})
	sort.Slice(rollouts, func(i, j int) bool { return rollouts[i].mod.After(rollouts[j].mod) })

	for _, r := range rollouts {
		if m, ok := codexRolloutModel(r.path, canon); ok {
			return m, true
		}
	}
	return "", false
}

// codexRolloutModel reads one rollout file: if its session_meta cwd matches
// wantCwd, it returns the last turn_context model in the file (and true).
// Otherwise it returns false so the caller tries the next rollout. Returning true
// only on a cwd match (even if no model line exists yet) is deliberate — this is
// the right session, so we shouldn't fall through to an unrelated one.
func codexRolloutModel(path, wantCwd string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()

	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	matched, model := false, ""
	first := true
	for sc.Scan() {
		var line struct {
			Type    string `json:"type"`
			Payload struct {
				Cwd   string `json:"cwd"`
				Model string `json:"model"`
			} `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			first = false
			continue
		}
		if first {
			first = false
			if line.Type == "session_meta" && line.Payload.Cwd != wantCwd {
				return "", false // different worktree — not this task's session
			}
			matched = true
		}
		if line.Type == "turn_context" && line.Payload.Model != "" {
			model = line.Payload.Model
		}
	}
	if !matched {
		return "", false
	}
	return model, model != ""
}
