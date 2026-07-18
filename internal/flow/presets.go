package flow

import (
	"maps"
	"os"
	"path/filepath"
)

// presets are the flows the engine ships wired and runnable. They double as the
// starting templates a project copies into its own .ultraflow/flows.yaml. The
// keys and step ids are kept in lockstep with the frontend's FLOWS table
// (web/src/util.ts) so the card's stepper draws the same spine the engine walks.
//
// Only flows listed here are Wired(): task creation normalizes anything else down
// to solo and the composer shows the rest as "· soon", so a card can never claim
// a multi-step flow that didn't run (spec/roadmap "presentation honesty").
var presets = map[string]Flow{
	"solo": {
		Key:   "solo",
		Label: "Solo",
		Start: "build",
		Steps: []Step{
			{ID: "build", Role: "build"},
		},
	},
	"plan-build": {
		Key:   "plan-build",
		Label: "Plan → Build",
		Start: "plan",
		Steps: []Step{
			{ID: "plan", Role: "plan", Prompt: planPrompt, Next: []string{"build"}},
			{ID: "build", Role: "build", Prompt: buildPrompt},
		},
	},
	"plan-build-critic-gate": {
		Key:   "plan-build-critic-gate",
		Label: "Plan → Build → Critic → Gate",
		Start: "plan",
		Steps: []Step{
			{ID: "plan", Role: "plan", Prompt: planPrompt, Next: []string{"build"}},
			{ID: "build", Role: "build", Prompt: buildPrompt, Next: []string{"critic"}},
			{ID: "critic", Role: "critic", Prompt: criticPrompt, Next: []string{"gate"}},
			{
				ID:     "gate",
				Role:   "gate",
				Gate:   true,
				Prompt: "Review the plan, build and critic in this task's worktree. Approve to accept the work (it goes to review, ready to merge), or send it back to the build step for changes.",
				Routes: []Route{
					{Answer: "Approve", Next: ""},              // accept → finish → review
					{Answer: "Request changes", Next: "build"}, // loop back to rebuild
					{Answer: "", Next: "build"},                // free-form feedback → rebuild
				},
			},
		},
	},
}

// Step instructions shared by the multi-step presets. Each is appended to the
// task's own title/body by the orchestrator's buildStepPrompt, so a step agent
// gets both the overall task and its specific role.
const (
	planPrompt = "This is the PLAN step. Investigate the codebase and write a concise implementation plan " +
		"for the task: the approach, the files you'll change, and how you'll verify it. Do NOT write the " +
		"implementation yet. Leave your plan where the next step can pick it up — a short PLAN.md in the " +
		"working directory is ideal — then finish this step."

	buildPrompt = "This is the BUILD step. Implement the task. The plan from the previous step is already " +
		"here in this working directory (e.g. PLAN.md) — follow it, write the actual code, and make it work. " +
		"Run whatever build/tests apply so you hand the critic something that runs."

	criticPrompt = "This is the CRITIC step. Critically review the implementation already in this working " +
		"directory against the task's intent: correctness, edge cases, missed requirements, and quality. Fix " +
		"the problems you find directly. If something genuinely needs a human decision, call ask_human; " +
		"otherwise leave the work in good shape for the human's gate review. Your finish_task report is the " +
		"final brief the human will read before approving. In plain product language, state: (1) whether the " +
		"reported problem was reproduced or otherwise confirmed, (2) its root cause, (3) the work actually " +
		"performed, and (4) how the result was verified, including any remaining caveats. Do not submit a " +
		"generic internal note such as 'review completed'."
)

// Load returns the flows available for a project: the built-in presets, with any
// flows declared in <repoPath>/.ultraflow/flows.yaml layered on top (a project
// flow with a preset's key overrides it). A missing or unreadable file is not an
// error — the project simply gets the presets. Kept side-effect free so callers
// can resolve per-task without caching.
func Load(repoPath string) map[string]Flow {
	out := make(map[string]Flow, len(presets))
	maps.Copy(out, presets)
	if repoPath == "" {
		return out
	}
	data, err := os.ReadFile(filepath.Join(repoPath, ".ultraflow", "flows.yaml"))
	if err != nil {
		return out
	}
	flows, err := ParseYAML(data)
	if err != nil {
		return out // a malformed project file falls back to presets rather than breaking
	}
	for _, f := range flows {
		if f.Key != "" {
			out[f.Key] = f
		}
	}
	return out
}

// ResolveFor resolves a flow key for a specific project, honoring the project's
// own .ultraflow/flows.yaml overrides and falling back to the wired presets (then
// solo). repoPath may be "" for a task with no registered git project.
func ResolveFor(repoPath, key string) Flow {
	flows := Load(repoPath)
	if f, ok := flows[key]; ok {
		return f
	}
	return presets["solo"]
}
