package flow

import "testing"

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
// (next ""), a rejection loops back to build, and an unmatched/blank answer takes
// the default (approve).
func TestGateRouting(t *testing.T) {
	f := Resolve("plan-build-critic-gate")
	gate, ok := f.Step("gate")
	if !ok || !gate.Gate {
		t.Fatal("gate step missing or not a gate")
	}
	cases := map[string]string{
		"Approve":              "",
		"approve, looks great": "",
		"Request changes":      "build",
		"please request changes here": "build",
		"":                     "", // default → approve
		"gibberish":            "", // unmatched → default (first route = approve)
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
	if len(opts) != 2 || opts[0] != "Approve" {
		t.Fatalf("gate options: got %v", opts)
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
