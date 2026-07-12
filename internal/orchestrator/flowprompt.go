package orchestrator

import (
	"fmt"
	"strings"

	"ultraflow/internal/flow"
	"ultraflow/internal/model"
)

// The seed prompts a flow step's agent runs with. They differ from the solo
// buildPrompt in one crucial way: the finish contract says finishing ENDS THE
// STEP and the flow advances automatically — not that the whole task is done — so
// an intermediate step doesn't try to wrap up the entire task, and doesn't sit
// idle waiting after its slice of work.

// askHumanContract is the shared ask_human instruction block, parameterized by
// task id. Mirrors the solo prompt's wording so a step agent treats the human the
// same way (don't guess; ask; then stop).
func askHumanContract(taskID string) string {
	return fmt.Sprintf(`IMPORTANT: You have an MCP tool "ask_human". When a decision is irreversible, `+
		`visual, or architectural — or you need the human to review something — do NOT guess. Call `+
		`ask_human with task_id="%s", a clear question, suggested options, and helpful context. After you `+
		`call it, STOP and end your turn; the human's answer arrives as your next input.`, taskID)
}

// stepFinishContract tells a step agent how to end its step. Finishing advances
// the flow to the next step automatically — the runner owns that — so the agent
// must not try to complete the whole task, nor idle after its step's work.
func stepFinishContract(taskID string) string {
	return fmt.Sprintf(`WHEN THIS STEP IS DONE: call the MCP tool "finish_task" with task_id="%s", a one-line `+
		`summary, and a short `+"`report`"+` of what this step produced. That ENDS this step and the flow `+
		`automatically advances to the next step — do not try to finish the whole task, and do not sit idle `+
		`at the prompt; call finish_task and stop.`, taskID)
}

// stepHeader renders the common top of every step prompt: the task, the step's
// place in the flow, and its role instruction.
func stepHeader(t model.Task, fl flow.Flow, step flow.Step) string {
	body := strings.TrimSpace(t.Body)
	if body != "" {
		body = "\n\n" + body
	}
	return fmt.Sprintf(`You are working on an Ultraflow task, running as one STEP of a multi-step flow (%s).

Task ID: %s
Title: %s%s

── Your step: %s (%s) ──
%s

The steps of this flow SHARE ONE worktree — the work of earlier steps is already here
in this working directory, so build on it rather than starting over.`,
		fl.Label, t.ID, t.Title, body, step.Role, fl.Caption(step.ID), strings.TrimSpace(step.Prompt))
}

// buildStepPrompt is the fresh-conversation prompt for entering a work step. seed
// is optional guidance (e.g. a gate "send it back" note) folded in when present.
func (o *Orchestrator) buildStepPrompt(t model.Task, fl flow.Flow, step flow.Step, prt int, seed string) string {
	var b strings.Builder
	b.WriteString(stepHeader(t, fl, step))
	if s := strings.TrimSpace(seed); s != "" {
		b.WriteString("\n\nNote from the human's review: " + s)
	}
	b.WriteString("\n\n")
	b.WriteString(portInstruction(prt))
	b.WriteString(askHumanContract(t.ID))
	b.WriteString("\n\n")
	b.WriteString(screenshotInstruction)
	b.WriteString("\n\n")
	b.WriteString(stepFinishContract(t.ID))
	return b.String()
}

// buildStepReengagePrompt resumes a work step's conversation after the human
// answered its self-heal escalation, seeding the guidance.
func (o *Orchestrator) buildStepReengagePrompt(t model.Task, step flow.Step, guidance string) string {
	return fmt.Sprintf(`You got stuck on the %s step of this Ultraflow task and asked the human for help. They responded.

Task ID: %s
Title: %s

Their guidance:
%s

Use it to get unstuck and complete this step. Your earlier work is still here in this
working directory.

%s`, step.Role, t.ID, t.Title, strings.TrimSpace(guidance), stepFinishContract(t.ID))
}

// buildStepSelfHealPrompt resumes a work step's conversation after it crashed, so
// the agent diagnoses and retries within the same step.
func (o *Orchestrator) buildStepSelfHealPrompt(t model.Task, step flow.Step, retry, budget int) string {
	return fmt.Sprintf(`Your last attempt at the %s step of this Ultraflow task ended with an ERROR — the process exited unexpectedly.

Task ID: %s
Title: %s

This is self-heal retry %d of %d. Work out what went wrong, fix the root cause, and
complete this step — your earlier work is still here in this working directory. Don't
just repeat what failed; diagnose it first.

%s`, step.Role, t.ID, t.Title, retry, budget, stepFinishContract(t.ID))
}
