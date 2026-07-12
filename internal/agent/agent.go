// Package agent defines the adapter interface over subscription coding CLIs and
// its concrete implementations. The interface is multi-agent from day one;
// implementations are added incrementally (Claude Code first).
package agent

import "context"

// Event is a streamed update from a running agent.
type Event struct {
	Kind string `json:"kind"` // log, message, tool, error
	Text string `json:"text"`
}

// Agent drives one subscription CLI in headless mode.
type Agent interface {
	// Name is the adapter identifier (claude, codex, opencode).
	Name() string
	// Run executes the agent in dir with prompt, streaming events on out until
	// the run finishes. out is closed by the caller.
	Run(ctx context.Context, dir, prompt string, out chan<- Event) error
}
