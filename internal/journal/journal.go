// Package journal is a lightweight, append-only JSONL activity log for
// after-the-fact analysis: what the user clicked, how tasks moved between
// statuses, and what happened to each agent (start / exit / signal). It is a
// temporary diagnostic aid — enabled by default next to the DB, disable with
// `-journal off`. One flat file, one line per event, so a couple of days of use
// can be grepped/jq'd later without a schema.
//
// It is deliberately global and best-effort: a journal write never blocks or
// fails a real operation. If the file can't be opened, journaling is simply off.
package journal

import (
	"encoding/json"
	"maps"
	"os"
	"sync"
	"time"
)

var (
	mu      sync.Mutex
	out     *os.File
	enabled bool
)

// Open turns journaling on, appending to path (created if absent). A second call
// replaces the target. Passing an empty path is a no-op (stays off).
func Open(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	mu.Lock()
	if out != nil {
		_ = out.Close()
	}
	out = f
	enabled = true
	mu.Unlock() // release before Log — it takes the same lock (avoid self-deadlock)
	Log("journal", "opened", map[string]any{"path": path})
	return nil
}

// Enabled reports whether journaling is currently writing.
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return enabled
}

// Log appends one event: a category ("ui" | "bus" | "agent" | "flow" | …), an
// event name, and arbitrary fields. Reserved keys ts/cat/event are always set by
// Log and override any collision in fields. Never blocks the caller on error.
func Log(cat, event string, fields map[string]any) {
	mu.Lock()
	defer mu.Unlock()
	if !enabled || out == nil {
		return
	}
	rec := make(map[string]any, len(fields)+3)
	maps.Copy(rec, fields)
	rec["ts"] = time.Now().Format("2006-01-02T15:04:05.000")
	rec["cat"] = cat
	rec["event"] = event
	b, err := json.Marshal(rec)
	if err != nil {
		// A field that won't marshal shouldn't lose the event — record the shape.
		b, _ = json.Marshal(map[string]any{
			"ts": rec["ts"], "cat": cat, "event": event, "marshal_error": err.Error(),
		})
	}
	_, _ = out.Write(append(b, '\n'))
}
