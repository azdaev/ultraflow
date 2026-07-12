// Package mcpserver exposes Ultraflow over MCP: external task input plus the
// ask_human tool that agents call (then yield their turn) when they need the human.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"ultraflow/internal/core"
	"ultraflow/internal/model"
	"ultraflow/internal/terminal"
)

// New builds the Ultraflow MCP server backed by svc. term lets finish_task end
// the agent's live session so a completed task frees its concurrency slot.
func New(svc *core.Service, term *terminal.Manager) *mcp.Server {
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
		Description: "Ask the human for input. Posts your question to the Ultraflow board and returns " +
			"immediately — then STOP: end your turn and wait. The human's answer arrives as your next " +
			"input, and you resume from there. Call this whenever a decision is irreversible, visual, or " +
			"architectural, or you need review — do NOT guess, and do NOT keep working after asking.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a askArgs) (*mcp.CallToolResult, any, error) {
		if _, err := svc.AskHuman(a.TaskID, a.Question, a.Options, a.Context); err != nil {
			return nil, nil, err
		}
		return text("Asked the human on the board: " + a.Question + "\n" +
			"END YOUR TURN NOW and wait — do not guess or keep working. Their answer will be " +
			"delivered to you as your next input; resume once it arrives."), nil, nil
	})

	type finishArgs struct {
		TaskID  string `json:"task_id" jsonschema:"the id of the task you are working on (given at start)"`
		Summary string `json:"summary" jsonschema:"one line: what you did, so the human can review"`
		Report  string `json:"report" jsonschema:"the full result of the task written as Markdown for the human to read on the review screen: for a question or audit, the answer and findings; for a code change, what you did and why and anything to check. Always write it — this is where the human reads your work, not the terminal."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "finish_task",
		Description: "Call this ONCE when the task is fully complete. It sends the work to review and " +
			"ENDS your session — you do not need to keep the terminal open or wait. Do not call it before " +
			"the task is actually done. ALWAYS pass `report`: a Markdown writeup of the result — it is shown " +
			"natively on the review screen and is how the human reads your work (the terminal is not kept). " +
			"For a question or audit task the report IS the deliverable. If you changed anything VISUAL, also " +
			"save screenshots (PNG) into .ultraflow/shots/ in your working directory so they show on review — " +
			"and you can embed them inline in the report with Markdown image syntax `![caption](shot.png)` " +
			"(reference the bare filename; it resolves to the saved screenshot).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a finishArgs) (*mcp.CallToolResult, any, error) {
		summary := a.Summary
		if summary == "" {
			summary = "agent reported the task complete"
		}
		// Report first so the one-line summary stays the task's latest non-empty
		// event — that's what the board card's activity strip shows, and a full
		// Markdown report there would swamp it.
		if a.Report != "" {
			svc.AppendTaskEvent(a.TaskID, "report", a.Report)
		}
		svc.AppendTaskEvent(a.TaskID, "result", summary)
		if err := svc.UpdateStatus(a.TaskID, model.StatusReview); err != nil {
			return nil, nil, err
		}
		// If the branch fell behind main while the agent worked, warn on the card
		// (roadmap M4). Async so the git check doesn't slow this tool's response.
		go svc.NoteFreshness(a.TaskID)
		// End the live session so the slot frees. Close asynchronously: closing kills
		// this agent's own process, and we want this tool call to return first.
		if sess, ok := term.Get(a.TaskID); ok {
			go sess.Close()
		}
		return text("Recorded complete and sent to review. Your session is ending — you can stop now."), nil, nil
	})

	return s
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
