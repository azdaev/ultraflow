package mcpserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"ultraflow/internal/core"
	"ultraflow/internal/model"
	"ultraflow/internal/store"
	"ultraflow/internal/terminal"
)

// newTestClient wires the real Ultraflow MCP server to an in-process client over
// an in-memory transport, so a test drives the tools exactly as an agent would —
// through the JSON-RPC layer, tool names, and arg schemas — not by calling the
// service directly.
func newTestClient(t *testing.T) (*mcp.ClientSession, *core.Service) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	svc := core.NewService(st)
	srv := New(svc, terminal.NewManager())

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := c.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close(); _ = st.Close() })
	return cs, svc
}

// callText calls a tool and returns its concatenated text content, failing the
// test on a transport or tool-level error.
func callText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("tool %s returned error: %+v", name, res.Content)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// create_task is the external input path: the tool must persist a real backlog task.
func TestCreateTaskTool(t *testing.T) {
	cs, svc := newTestClient(t)

	out := callText(t, cs, "create_task", map[string]any{
		"title":   "do a thing",
		"body":    "details",
		"project": "",
	})
	if !strings.Contains(out, "Created task") {
		t.Fatalf("create_task text = %q; want a creation confirmation", out)
	}

	tasks, err := svc.ListTasks()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "do a thing" {
		t.Fatalf("create_task didn't persist the task: %+v", tasks)
	}
	if tasks[0].Status != model.StatusBacklog {
		t.Fatalf("new task status = %q; want backlog", tasks[0].Status)
	}
}

// finish_task on a solo (no-run) task must send it to review, and must append the
// long report BEFORE the one-line summary so the summary stays the latest activity
// the board card strip shows — the ordering spec.md leans on as "the whole product".
func TestFinishTaskSendsToReviewSummaryLatest(t *testing.T) {
	cs, svc := newTestClient(t)

	task, err := svc.CreateTask("t", "", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The solo finish path only fires on a running task.
	if err := svc.UpdateStatus(task.ID, model.StatusRunning); err != nil {
		t.Fatalf("set running: %v", err)
	}

	callText(t, cs, "finish_task", map[string]any{
		"task_id": task.ID,
		"summary": "did the thing",
		"report":  "# Result\nfull writeup",
		"outcome": "answer",
	})

	got, err := svc.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.StatusReview {
		t.Fatalf("finish_task status = %q; want review", got.Status)
	}
	// The declared outcome must reach the store via the tool → CompleteTurn wiring.
	if got.Outcome != "answer" {
		t.Fatalf("finish_task outcome = %q; want answer", got.Outcome)
	}

	act, kind, err := svc.LatestActivity()
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if act[task.ID] != "did the thing" {
		t.Fatalf("latest activity = %q; want the one-line summary (report must be appended first)", act[task.ID])
	}
	if kind[task.ID] != "result" {
		t.Fatalf("latest activity kind = %q; want result", kind[task.ID])
	}
}
