# PLAN — Make the final Gate explain what is being approved

## Confirmed problem

Task `8f0dc538c98ae346` did produce useful reports. Its final Critic report says the
original modal-closing bug was confirmed, names the removed callback behavior,
notes the related name-edit fix, and lists passing lint/build/Playwright checks.
The confusing part is the Gate handoff: `openGate` replaces that task-specific
result with the generic question “Review the plan, build and critic…” and generic
flow mechanics. The attention card therefore asks for approval before explaining
the result; the full report is only discoverable after opening the task.

## Implementation

1. **Give the Gate a task-specific handoff** in `internal/orchestrator/flow.go`.
   When opening a built-in flow gate, read the task’s latest `result` event and use
   it as the request’s concise context. Phrase the question around the task title
   and state the consequences plainly: Approve completes the flow and moves the
   work to final Review; Request changes or typed feedback returns it to Build.
   Fall back cleanly to the current generic copy for custom/legacy flows with no
   result event.

2. **Make the Critic’s report explicitly human-facing** in
   `internal/flow/presets.go`. Extend the Critic prompt so its `finish_task` report
   is the final Gate brief, with four concrete points: whether the reported problem
   was reproduced/confirmed, root cause, work actually performed, and verification
   plus any remaining caveats. Require plain product language rather than an
   internal “review completed” note. This makes the existing full report panel a
   reliable explanation instead of relying on agent style.

3. **Clarify the Gate decision surface** in
   `web/src/components/TaskDetail.tsx` and `web/src/components/RailCard.tsx`.
   Present the captured result before the decision, label the full panel as the
   result being approved, and add short routing copy beside the actions so the
   distinction between Gate approval and the later merge action is explicit.
   Keep the raw diff secondary/collapsed; it remains available for technical
   inspection but is not the explanation the human must decipher.

4. **Update documentation and regression coverage** in `spec/flows.md` and
   `internal/orchestrator/flow_test.go` (plus a small prompt assertion near the
   preset tests if useful). Assert that after Plan → Build → Critic the pending
   Gate request contains the latest Critic result, the task-specific question and
   unambiguous routing copy; also cover the no-result fallback and preserve exact
   Approve/Request changes routing.

## Verification

- Run `go test ./internal/flow ./internal/orchestrator ./internal/core` and then
  `go test ./...`.
- Run `cd web && npm run build`.
- Start the seeded board, park a Plan → Build → Critic task at Gate, and verify the
  attention card and task drawer answer, without reading the diff: what problem was
  found, what changed, whether checks passed, and what each decision does.
- Confirm Approve moves to Review without merging, while Request changes/free-form
  feedback loops to Build. Capture Gate screenshots in `.ultraflow/shots/` during
  the Build step because this changes visible UI copy/layout.
