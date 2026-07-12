// Package mcpserver exposes Ultraflow over MCP: external task input plus the
// blocking ask_human tool that agents call when they need the human.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"ultraflow/internal/core"
)

// New builds the Ultraflow MCP server backed by svc.
func New(svc *core.Service) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "ultraflow", Version: "0.1.0"}, nil)

	type createArgs struct {
		Title   string `json:"title" jsonschema:"short task title"`
		Body    string `json:"body" jsonschema:"full task description / acceptance criteria"`
		Project string `json:"project" jsonschema:"project name or repo path"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_task",
		Description: "Add a task to the Ultraflow board backlog.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a createArgs) (*mcp.CallToolResult, any, error) {
		t, err := svc.CreateTask(a.Title, a.Body, a.Project)
		if err != nil {
			return nil, nil, err
		}
		return text(fmt.Sprintf("Created task %s: %s", t.ID, t.Title)), nil, nil
	})

	type noArgs struct{}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_tasks",
		Description: "List all tasks on the board with their status.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
		tasks, err := svc.ListTasks()
		if err != nil {
			return nil, nil, err
		}
		b, _ := json.MarshalIndent(tasks, "", "  ")
		return text(string(b)), nil, nil
	})

	type getArgs struct {
		ID string `json:"id" jsonschema:"the task id"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_task",
		Description: "Get one task by id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a getArgs) (*mcp.CallToolResult, any, error) {
		t, err := svc.GetTask(a.ID)
		if err != nil {
			return nil, nil, err
		}
		b, _ := json.MarshalIndent(t, "", "  ")
		return text(string(b)), nil, nil
	})

	type askArgs struct {
		TaskID   string   `json:"task_id" jsonschema:"the id of the task you are working on (given at start)"`
		Question string   `json:"question" jsonschema:"a clear, specific question for the human"`
		Options  []string `json:"options" jsonschema:"suggested answers so the human can reply in one tap"`
		Context  string   `json:"context" jsonschema:"context to help the human decide: a diff, plan, or screenshot description"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "ask_human",
		Description: "Ask the human for input and BLOCK until they answer on the Ultraflow board. " +
			"Call this whenever a decision is irreversible, visual, or architectural, or you need review — do NOT guess. " +
			"Returns the human's chosen answer.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a askArgs) (*mcp.CallToolResult, any, error) {
		ans, err := svc.AskHuman(ctx, a.TaskID, a.Question, a.Options, a.Context)
		if err != nil {
			return nil, nil, err
		}
		return text(ans), nil, nil
	})

	return s
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
