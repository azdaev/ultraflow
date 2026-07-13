package flow

import (
	"strconv"
	"strings"
)

// Caption renders the card's one-line description of where a run is in its flow,
// e.g. "Build · step 2 of 4 · critic + your gate next". It leads with the current
// step, its position, and a short look-ahead so the human knows what's coming
// (especially an upcoming human gate). An empty cursor means the flow is complete.
func (f Flow) Caption(cursor string) string {
	if cursor == "" {
		return "Flow complete"
	}
	step, ok := f.Step(cursor)
	if !ok {
		return ""
	}
	idx := f.IndexOf(cursor)
	head := titleRole(step.Role) + " · step " + strconv.Itoa(idx+1) + " of " + strconv.Itoa(len(f.Steps))

	if step.Gate {
		return head + " · your approval needed"
	}
	if look := f.lookAhead(step); look != "" {
		return head + " · " + look
	}
	return head + " · final step"
}

// lookAhead phrases what follows a work step: the next step's role, plus a flag
// when a human gate is the step after that ("critic + your gate next"). Empty when
// nothing follows (the step is terminal).
func (f Flow) lookAhead(step Step) string {
	next := step.DefaultNext()
	if next == "" {
		return ""
	}
	ns, ok := f.Step(next)
	if !ok {
		return ""
	}
	if ns.Gate {
		return "your gate next"
	}
	// A work step follows; note if a gate is right behind it, since that's the
	// beat the human most wants to see coming.
	if after, ok := f.Step(ns.DefaultNext()); ok && after.Gate {
		return strings.ToLower(ns.Role) + " + your gate next"
	}
	return strings.ToLower(ns.Role) + " next"
}

func titleRole(role string) string {
	if role == "" {
		return "Step"
	}
	return strings.ToUpper(role[:1]) + role[1:]
}
