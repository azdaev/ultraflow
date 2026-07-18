package flow

import (
	"strings"
	"testing"
)

func TestResolveDefaultsToSolo(t *testing.T) {
	if got := Resolve("").Key; got != "solo" {
		t.Fatalf("blank flow: want solo, got %q", got)
	}
	if got := Resolve("nope-not-a-flow").Key; got != "solo" {
		t.Fatalf("unknown flow: want solo, got %q", got)
	}
	if Resolve("solo").Multi() {
		t.Fatal("solo should not be multi-step")
	}
	if !Resolve("plan-build-critic-gate").Multi() {
		t.Fatal("plan-build-critic-gate should be multi-step")
	}
}

func TestWired(t *testing.T) {
	for _, k := range []string{"solo", "plan-build", "plan-build-critic-gate"} {
		if !Wired(k) {
			t.Errorf("%q should be wired", k)
		}
	}
	if Wired("tdd") {
		t.Error("tdd is not wired yet")
	}
}

// TestWalkOrder walks the plan→build→critic→gate graph the way the orchestrator
// does — advancing along DefaultNext from each work step — and asserts it reaches
// the gate through exactly plan, build, critic in order.
func TestWalkOrder(t *testing.T) {
	f := Resolve("plan-build-critic-gate")
	cursor := f.Start
	var visited []string
	for range 10 {
		step, ok := f.Step(cursor)
		if !ok {
			t.Fatalf("unknown cursor %q", cursor)
		}
		visited = append(visited, step.ID)
		if step.Gate {
			break
		}
		cursor = step.DefaultNext()
		if cursor == "" {
			break
		}
	}
	want := []string{"plan", "build", "critic", "gate"}
	if len(visited) != len(want) {
		t.Fatalf("visited %v, want %v", visited, want)
	}
	for i := range want {
		if visited[i] != want[i] {
			t.Fatalf("step %d: got %q, want %q (%v)", i, visited[i], want[i], visited)
		}
	}
}

// TestGateRouting checks the terminal gate's routing: approve finishes the flow
// (next ""), while a rejection or free-form comment loops back to build.
func TestGateRouting(t *testing.T) {
	f := Resolve("plan-build-critic-gate")
	gate, ok := f.Step("gate")
	if !ok || !gate.Gate {
		t.Fatal("gate step missing or not a gate")
	}
	cases := map[string]string{
		"Approve":                                   "",
		"approve, looks great":                      "build", // comments use the fallback
		"I cannot approve until retry works":        "build", // option text must not imply a click
		"Request changes":                           "build",
		"please request changes and add unit tests": "build", // comments use the same destination
		"":                           "build", // explicit empty-answer fallback
		"please fix the empty state": "build", // unmatched free-form feedback
	}
	for answer, want := range cases {
		if got := gate.Route(answer); got != want {
			t.Errorf("Route(%q) = %q, want %q", answer, got, want)
		}
	}
}

// TestGateRoutingWithoutExplicitFallback preserves existing custom-flow behavior:
// if a gate does not declare an empty-answer route, an unmatched reply still uses
// its first route, then Next when there are no routes.
func TestGateRoutingWithoutExplicitFallback(t *testing.T) {
	withRoutes := Step{
		Gate: true,
		Routes: []Route{
			{Answer: "Approve", Next: "ship"},
			{Answer: "Request changes", Next: "build"},
		},
	}
	if got := withRoutes.Route("a free-form comment"); got != "ship" {
		t.Fatalf("unmatched custom gate route = %q, want first route %q", got, "ship")
	}
	if got := withRoutes.Route("approve, looks good"); got != "ship" {
		t.Fatalf("legacy substring route = %q, want %q", got, "ship")
	}

	withNext := Step{Gate: true, Next: []string{"continue"}}
	if got := withNext.Route("a free-form comment"); got != "continue" {
		t.Fatalf("unmatched route-less gate = %q, want Next %q", got, "continue")
	}
}

// TestGateRoutingExactWins covers a custom gate whose options overlap: an exact
// reply must hit its own route, not a shorter option that is its substring (here
// "yes" is declared before "yes, redeploy"). A substringing freeform reply still
// falls back to the substring pass.
func TestGateRoutingExactWins(t *testing.T) {
	gate := Step{
		Gate: true,
		Routes: []Route{
			{Answer: "yes", Next: "ship"},
			{Answer: " yes, redeploy ", Next: "redeploy"},
		},
	}
	cases := map[string]string{
		"yes":              "ship",     // exact → own route, not shadowed
		"yes, redeploy":    "redeploy", // exact → longer route, reachable now
		"Yes, Redeploy":    "redeploy", // case-insensitive exact
		"sure, yes please": "ship",     // no exact → substring pass ("yes")
		"nonsense":         "ship",     // unmatched → default (first route)
	}
	for answer, want := range cases {
		if got := gate.Route(answer); got != want {
			t.Errorf("Route(%q) = %q, want %q", answer, got, want)
		}
	}
}

func TestGateOptions(t *testing.T) {
	gate, _ := Resolve("plan-build-critic-gate").Step("gate")
	opts := gate.GateOptions()
	if len(opts) != 2 || opts[0] != "Approve" || opts[1] != "Request changes" {
		t.Fatalf("gate options: got %v", opts)
	}
}

func TestCriticPromptRequiresHumanFacingGateBrief(t *testing.T) {
	for _, want := range []string{
		"whether the reported problem was reproduced or otherwise confirmed",
		"root cause",
		"work actually performed",
		"how the result was verified",
		"remaining caveats",
		"plain product language",
	} {
		if !strings.Contains(criticPrompt, want) {
			t.Fatalf("critic prompt missing %q", want)
		}
	}
}

func TestCaption(t *testing.T) {
	f := Resolve("plan-build-critic-gate")
	// build is step 2 of 4, followed by critic then the gate.
	if got := f.Caption("build"); got != "Build · step 2 of 4 · critic + your gate next" {
		t.Fatalf("build caption: %q", got)
	}
	if got := f.Caption("plan"); got != "Plan · step 1 of 4 · build next" {
		t.Fatalf("plan caption: %q", got)
	}
	if got := f.Caption("gate"); got != "Gate · step 4 of 4 · your approval needed" {
		t.Fatalf("gate caption: %q", got)
	}
	if got := f.Caption(""); got != "Flow complete" {
		t.Fatalf("empty caption: %q", got)
	}
}

func TestParseYAMLAndOverride(t *testing.T) {
	data := []byte(`
flows:
  - key: mini
    label: Mini
    start: a
    steps:
      - id: a
        role: build
        next: [b]
      - id: b
        role: gate
        gate: true
        routes:
          - answer: Approve
            next: ""
`)
	flows, err := ParseYAML(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(flows) != 1 || flows[0].Key != "mini" {
		t.Fatalf("parsed %+v", flows)
	}
	if b, ok := flows[0].Step("b"); !ok || !b.Gate {
		t.Fatal("gate step not parsed")
	}
}

func TestParseYAMLRejectsDanglingNext(t *testing.T) {
	data := []byte(`
flows:
  - key: bad
    start: a
    steps:
      - id: a
        next: [nowhere]
`)
	if _, err := ParseYAML(data); err == nil {
		t.Fatal("expected validation error for dangling next")
	}
}
