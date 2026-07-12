package agent

import (
	"context"
	"slices"
	"testing"
)

func TestClaudeInteractiveCommandsRouteQuestionsThroughUltraflow(t *testing.T) {
	c := NewClaude("http://127.0.0.1:9876/mcp")

	for _, tc := range []struct {
		name   string
		resume bool
	}{
		{name: "fresh"},
		{name: "resumed", resume: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				args    []string
				cleanup func()
				err     error
			)
			if tc.resume {
				built, clean, buildErr := c.ResumeCommand(context.Background(), t.TempDir(), "prompt")
				cleanup, err = clean, buildErr
				if err == nil {
					args = built.Args
				}
			} else {
				built, clean, buildErr := c.Command(context.Background(), t.TempDir(), "prompt")
				cleanup, err = clean, buildErr
				if err == nil {
					args = built.Args
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()

			assertClaudeQuestionRoutingArgs(t, args[1:])
			if got := slices.Contains(args, "--continue"); got != tc.resume {
				t.Fatalf("--continue present = %v, want %v; args: %q", got, tc.resume, args)
			}
		})
	}
}

func TestClaudeHeadlessArgsRouteQuestionsThroughUltraflow(t *testing.T) {
	args := claudeHeadlessArgs("prompt", "/tmp/mcp.json")
	assertClaudeQuestionRoutingArgs(t, args)
	if !slices.Contains(args, "--output-format") {
		t.Fatalf("headless args lost stream output configuration: %q", args)
	}
}

func assertClaudeQuestionRoutingArgs(t *testing.T, args []string) {
	t.Helper()
	assertFlagValue(t, args, "--mcp-config")
	if got := assertFlagValue(t, args, "--disallowedTools"); got != claudeNativeQuestionTool {
		t.Fatalf("--disallowedTools = %q, want %q; args: %q", got, claudeNativeQuestionTool, args)
	}
}

func assertFlagValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatalf("missing %s value in args: %q", flag, args)
	return ""
}
