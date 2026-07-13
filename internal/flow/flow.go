// Package flow defines Ultraflow's flow engine model: a flow is a GRAPH of steps
// a task walks through, not a single solo agent run. Each step either spawns an
// agent in the task's shared worktree (a work step) or PARKS the task for a human
// decision (a gate). Steps can loop back — the successor of a step is a set, and a
// gate routes to a successor by the human's answer — so a flow expresses TDD-style
// critic→redo loops, not just a straight line.
//
// Flows are data: the in-code presets here double as templates, and a project can
// override or add its own in a .ultraflow/flows.yaml (see Load / ParseYAML). The
// orchestrator (internal/orchestrator) is what actually walks a resolved flow; the
// store persists the run cursor so a daemon restart resumes mid-flow.
package flow

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml"
)

// Step is one node in a flow graph. A work step (Gate=false) runs Agent in the
// task's worktree seeded with Prompt, then advances along Next. A gate step
// (Gate=true) runs no agent: it parks the task as needs_human and, on the human's
// answer, routes along Routes (falling back to Next / the first route).
type Step struct {
	ID     string   `yaml:"id" json:"id"`
	Role   string   `yaml:"role" json:"role"`     // human-facing label: plan, build, critic, gate…
	Agent  string   `yaml:"agent" json:"agent"`   // adapter to run; "" = inherit the task's agent
	Prompt string   `yaml:"prompt" json:"prompt"` // step instruction (a gate's is the question)
	Gate   bool     `yaml:"gate" json:"gate"`     // true = park for a human decision, no agent
	Next   []string `yaml:"next" json:"next"`     // successor step ids; empty = terminal (→ review)
	Routes []Route  `yaml:"routes" json:"routes"` // gate answer → successor; overrides Next for gates
}

// Route maps a gate's answer to the next step. Answer "" (or the first route) is
// the default taken on approve / an unmatched reply. Next "" means finish the flow
// (send the task to review) — an approve at a terminal gate.
type Route struct {
	Answer string `yaml:"answer" json:"answer"`
	Next   string `yaml:"next" json:"next"`
}

// Flow is a named graph of steps with a designated start.
type Flow struct {
	Key   string `yaml:"key" json:"key"`
	Label string `yaml:"label" json:"label"`
	Start string `yaml:"start" json:"start"`
	Steps []Step `yaml:"steps" json:"steps"`
}

// Step returns the step with the given id.
func (f Flow) Step(id string) (Step, bool) {
	for _, s := range f.Steps {
		if s.ID == id {
			return s, true
		}
	}
	return Step{}, false
}

// IndexOf returns a step's position in the declared order (for the UI stepper),
// or -1 if absent. Loops don't change presentation order — the declared order is
// the spine the stepper draws.
func (f Flow) IndexOf(id string) int {
	for i, s := range f.Steps {
		if s.ID == id {
			return i
		}
	}
	return -1
}

// Multi reports whether the flow has more than one step — i.e. it is an actual
// multi-step flow the engine must walk, versus a single-step solo run the
// orchestrator handles on its unchanged fast path.
func (f Flow) Multi() bool { return len(f.Steps) > 1 }

// DefaultNext is the successor a work step advances to when it finishes cleanly.
// Empty means the step is terminal and the flow is complete after it.
func (s Step) DefaultNext() string {
	if len(s.Next) > 0 {
		return s.Next[0]
	}
	return ""
}

// Route resolves a gate's answer to its successor step id. An exact
// (case-insensitive) match wins first; failing that it matches by substring (so
// "Approve" matches a reply of "approve, looks good"), then falls back to the first
// route, then Next, then "" (finish). A returned "" means: finish the flow (→ review).
func (s Step) Route(answer string) string {
	a := strings.ToLower(strings.TrimSpace(answer))
	// Exact match first, so a route whose answer is a substring of another (e.g.
	// "yes" declared before "yes, redeploy") can't shadow the longer one in a
	// custom flows.yaml gate.
	for _, r := range s.Routes {
		if r.Answer != "" && a == strings.ToLower(strings.TrimSpace(r.Answer)) {
			return r.Next
		}
	}
	for _, r := range s.Routes {
		if r.Answer == "" {
			continue
		}
		if strings.Contains(a, strings.ToLower(strings.TrimSpace(r.Answer))) {
			return r.Next
		}
	}
	// No explicit match: take the default (approve) path — the first route, else
	// the declared Next, else finish.
	if len(s.Routes) > 0 {
		return s.Routes[0].Next
	}
	return s.DefaultNext()
}

// GateOptions returns the answer labels a gate offers the human, in order. Falls
// back to a plain approve/reject pair when the gate declares no routes.
func (s Step) GateOptions() []string {
	var opts []string
	for _, r := range s.Routes {
		if r.Answer != "" {
			opts = append(opts, r.Answer)
		}
	}
	if len(opts) == 0 {
		return []string{"Approve", "Request changes"}
	}
	return opts
}

// Resolve returns the flow for a key, defaulting to Solo for a blank or unknown
// key so a task never fails to launch over a bad flow name (the presentation
// layer is what keeps unwired flows unselectable).
func Resolve(key string) Flow {
	if f, ok := presets[key]; ok {
		return f
	}
	return presets["solo"]
}

// Wired reports whether a flow key names a flow the engine can actually run
// today, so task creation can normalize anything else down to solo and the
// composer only offers what works (presentation honesty, spec/roadmap M2).
func Wired(key string) bool {
	_, ok := presets[key]
	return ok
}

// WiredKeys lists the runnable flow keys (presets), for callers that gate on the
// implemented set.
func WiredKeys() []string {
	keys := make([]string, 0, len(presets))
	for k := range presets {
		keys = append(keys, k)
	}
	return keys
}

// ParseYAML parses a list of flow definitions from YAML — the on-disk form a
// project uses to override or add flows. Each parsed flow is validated so a
// malformed graph (missing start, dangling successor) is rejected up front
// rather than stranding a task mid-run.
func ParseYAML(data []byte) ([]Flow, error) {
	var doc struct {
		Flows []Flow `yaml:"flows"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	for i := range doc.Flows {
		if err := doc.Flows[i].validate(); err != nil {
			return nil, fmt.Errorf("flow %q: %w", doc.Flows[i].Key, err)
		}
	}
	return doc.Flows, nil
}

// validate checks a flow is walkable: it has steps, a start that exists, and
// every successor (Next / Route target) points at a real step or "" (finish).
func (f Flow) validate() error {
	if len(f.Steps) == 0 {
		return fmt.Errorf("has no steps")
	}
	start := f.Start
	if start == "" {
		start = f.Steps[0].ID
	}
	if _, ok := f.Step(start); !ok {
		return fmt.Errorf("start step %q not found", start)
	}
	for _, s := range f.Steps {
		for _, n := range s.Next {
			if n == "" {
				continue
			}
			if _, ok := f.Step(n); !ok {
				return fmt.Errorf("step %q → unknown next %q", s.ID, n)
			}
		}
		for _, r := range s.Routes {
			if r.Next == "" {
				continue
			}
			if _, ok := f.Step(r.Next); !ok {
				return fmt.Errorf("gate %q → unknown route target %q", s.ID, r.Next)
			}
		}
	}
	return nil
}
