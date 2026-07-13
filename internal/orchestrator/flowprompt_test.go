package orchestrator

import (
	"strings"
	"testing"

	"ultraflow/internal/flow"
	"ultraflow/internal/model"
)

func TestSoloPromptRequestsTaskRename(t *testing.T) {
	task := model.Task{ID: "task-solo", Title: "a long raw request", Body: "full instructions"}
	prompt := buildPrompt(task, 0)

	assertRenameContract(t, prompt, task.ID, true)
	assertTaskText(t, prompt, task)
}

func TestFlowEntryPromptRequestsTaskRename(t *testing.T) {
	task := model.Task{ID: "task-flow", Title: "a long raw flow request", Body: "full flow instructions"}
	fl := flow.Resolve("plan-build")
	entry, ok := fl.Step(fl.Start)
	if !ok {
		t.Fatalf("flow start step %q not found", fl.Start)
	}

	prompt := new(Orchestrator).buildStepPrompt(task, fl, entry, 0, "", true)
	assertRenameContract(t, prompt, task.ID, true)
	assertTaskText(t, prompt, task)
}

func TestFlowLaterAndReentryPromptsDoNotRequestTaskRename(t *testing.T) {
	task := model.Task{ID: "task-flow", Title: "a long raw flow request", Body: "full flow instructions"}
	fl := flow.Resolve("plan-build")
	entry, _ := fl.Step(fl.Start)
	later, _ := fl.Step("build")
	o := new(Orchestrator)

	tests := map[string]string{
		"later step":          o.buildStepPrompt(task, fl, later, 0, "", false),
		"gate re-entry":       o.buildStepPrompt(task, fl, entry, 0, "please revise", false),
		"blank gate re-entry": o.buildStepPrompt(task, fl, entry, 0, "", false),
		"restart":             o.buildStepRestartPrompt(task, entry),
		"self-heal":           o.buildStepSelfHealPrompt(task, entry, 1, 2),
		"human re-engagement": o.buildStepReengagePrompt(task, entry, "continue"),
	}
	for name, prompt := range tests {
		t.Run(name, func(t *testing.T) {
			assertRenameContract(t, prompt, task.ID, false)
		})
	}
}

func assertRenameContract(t *testing.T, prompt, taskID string, want bool) {
	t.Helper()
	hasContract := strings.Contains(prompt, renameTaskContract(taskID))
	if hasContract != want {
		t.Fatalf("rename contract presence = %v; want %v\nprompt:\n%s", hasContract, want, prompt)
	}
}

func assertTaskText(t *testing.T, prompt string, task model.Task) {
	t.Helper()
	if !strings.Contains(prompt, task.Title) {
		t.Fatalf("prompt lost task title %q", task.Title)
	}
	if !strings.Contains(prompt, task.Body) {
		t.Fatalf("prompt lost task body %q", task.Body)
	}
}
