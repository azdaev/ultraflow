package orchestrator

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ultraflow/internal/model"
	"ultraflow/internal/terminal"
)

// Context-cap tuning. contextPoll is how often we re-read the agent's transcript;
// a local single-user tool doesn't need this tight. contextWorkingGrace is the max
// idle time that still counts as "actively working" — a working Claude TUI streams
// a spinner/timer continuously (see the idle-watcher note), so a small grace cleanly
// separates a mid-turn agent (safe to /compact) from one parked on ask_human or
// idling toward review (must NOT be disturbed).
const (
	contextPoll         = 15 * time.Second
	contextWorkingGrace = 8 * time.Second
)

// compactSubmitDelay mirrors service.answerSubmitDelay: the gap between typing
// "/compact" into the agent's PTY and sending the CR that submits it, so the TUI
// reads the CR as an Enter keystroke rather than as part of a paste. A var so
// tests can zero it.
var compactSubmitDelay = 250 * time.Millisecond

// watchContext enforces the daemon's context budget on a live claude agent: it
// polls the agent's own transcript and, when its context crosses the cap while it
// is actively working, injects `/compact` so the agent summarizes and carries on
// with a tighter working set — before the ~1M window bloats and quality/cost
// degrade. Started per attempt alongside watchIdle (claude only — codex's
// transcript format differs), it runs until the session ends. A no-op when no cap
// is configured (Settings → Context budget; 0 = off).
//
// It arms/disarms so it fires ONCE per crossing, not in a loop: after injecting a
// compact it disarms, and only re-arms once context has fallen back below the cap
// (i.e. the compaction took effect). It never disturbs the two states where input
// would do harm: an agent parked on ask_human (task is needs_human, not running)
// or one idling toward review (idle longer than the working grace).
func (o *Orchestrator) watchContext(sess *terminal.Session, taskID, dir string) {
	if dir == "" {
		return
	}
	ticker := time.NewTicker(contextPoll)
	defer ticker.Stop()
	armed := true
	for {
		select {
		case <-sess.Done():
			return
		case <-ticker.C:
			// A paused (SIGSTOP'd) agent is frozen: it can't grow its transcript and
			// must not be touched. Skip it wholesale — otherwise IdleFor's 0 (which
			// suppresses the idle watcher during a pause) would read as "actively
			// working" here and inject a "/compact" that fires into the PTY on resume.
			if sess.Suspended() {
				continue
			}
			tokens, ok := claudeContextTokens(dir)
			if !ok {
				continue // transcript not found yet, or unreadable — try again next tick
			}
			// Report the live context size to the board every tick, whether or not a
			// cap is set — this powers the card's context meter. The /compact
			// enforcement below stays gated on a configured cap, so reporting and
			// capping are decoupled: the meter works with the budget off.
			o.svc.PublishContext(taskID, tokens)

			capTokens := o.svc.ContextCap()
			if capTokens <= 0 {
				continue // reporting only; the cap feature is off
			}
			if tokens < capTokens {
				armed = true // context is back under the cap: ready to fire on the next crossing
				continue
			}
			if !armed {
				continue // already compacted for this crossing; wait for it to take effect
			}
			// Only inject into an agent that is actively working. Parked on ask_human,
			// the injected text would consume the prompt and corrupt the human-answer
			// flow; idling toward review, it would revive a task on its way out. A
			// working TUI keeps IdleFor near zero (its spinner streams), so this gate
			// excludes both without needing to model them explicitly.
			if sess.IdleFor() > contextWorkingGrace {
				continue
			}
			if cur, err := o.svc.GetTask(taskID); err != nil || cur.Status != model.StatusRunning {
				continue
			}
			o.compact(sess, taskID, tokens, capTokens)
			armed = false
		}
	}
}

// compact types "/compact" + Enter into the agent's live PTY. As with a board
// answer, the text and the submitting CR go as two writes with a gap between them
// so the TUI reads the CR as a keystroke, not a pasted newline. A thread event
// records it so the human sees why the context was trimmed.
func (o *Orchestrator) compact(sess *terminal.Session, taskID string, tokens, capTokens int) {
	o.svc.AppendTaskEvent(taskID, "status",
		fmt.Sprintf("context reached ~%dk tokens (cap %dk) — compacting to keep it focused",
			tokens/1000, capTokens/1000))
	if err := sess.Write([]byte("/compact")); err != nil {
		log.Printf("task %s: context compact: %v", taskID, err)
		return
	}
	time.Sleep(compactSubmitDelay)
	if err := sess.Write([]byte("\r")); err != nil {
		log.Printf("task %s: context compact submit: %v", taskID, err)
	}
}

// claudeContextTokens returns the live context size of the claude session running
// in dir, read from Claude Code's own JSONL transcript, and whether it could be
// read. Claude writes one transcript per session under
// ~/.claude/projects/<encoded-cwd>/<session>.jsonl, encoding the cwd by replacing
// every "/" and "." with "-" (hyphens and case are preserved). Because each task
// runs in its own worktree (a unique cwd), the newest transcript in that directory
// is this task's session — no session-id bookkeeping needed. A missing directory
// (e.g. an encoding we didn't anticipate) just returns false, so the cap safely
// does nothing rather than acting on the wrong task.
func claudeContextTokens(dir string) (int, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, false
	}
	proj := filepath.Join(home, ".claude", "projects", encodeClaudeCwd(dir))
	f, ok := newestJSONL(proj)
	if !ok {
		return 0, false
	}
	return lastContextTokens(f)
}

// encodeClaudeCwd maps an absolute working directory to Claude Code's transcript
// directory name: "/" and "." both become "-". Matches observed layouts like
// /Users/x/.superset/wt → -Users-x--superset-wt.
func encodeClaudeCwd(dir string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(dir)
}

// newestJSONL returns the most-recently-modified *.jsonl file in dir. When a task
// resumes (`claude --continue`) it reuses the same transcript, so "newest" stays
// the right session across a restart.
func newestJSONL(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var newest string
	var newestMod time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if newest == "" || info.ModTime().After(newestMod) {
			newest, newestMod = filepath.Join(dir, e.Name()), info.ModTime()
		}
	}
	return newest, newest != ""
}

// lastContextTokens returns the context size of the most recent turn in a Claude
// Code transcript: the last line carrying a usage block, summed as input +
// cache-creation + cache-read tokens — exactly what was sent to the model that
// turn (so it tracks the true context size, and drops after a /compact). Lines
// without usage (user messages, tool results, handshakes) are skipped. Reading the
// whole file each poll is fine for a local single-user tool.
func lastContextTokens(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	tokens, found := 0, false
	for sc.Scan() {
		var line struct {
			Message struct {
				Usage struct {
					Input       int `json:"input_tokens"`
					CacheCreate int `json:"cache_creation_input_tokens"`
					CacheRead   int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		u := line.Message.Usage
		if t := u.Input + u.CacheCreate + u.CacheRead; t > 0 {
			tokens, found = t, true
		}
	}
	return tokens, found
}
