// Command ultraflow is the local orchestrator daemon: it hosts the MCP server,
// the board's HTTP API, and the agent orchestrator in one process.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	webassets "ultraflow/web"

	"ultraflow/internal/core"
	mcpserver "ultraflow/internal/mcp"
	"ultraflow/internal/orchestrator"
	"ultraflow/internal/store"
	"ultraflow/internal/terminal"
	"ultraflow/internal/web"
	"ultraflow/internal/worktree"
)

func main() {
	var (
		dbPath    = flag.String("db", "ultraflow.db", "SQLite database path")
		port      = flag.Int("port", 7787, "HTTP port")
		staticDir = flag.String("static", "web/dist", "static frontend build dir")
		workdir   = flag.String("workdir", ".", "fallback working dir for tasks with no registered git project")
		wtRoot    = flag.String("worktrees", ".ultraflow/worktrees", "root dir for per-task git worktrees")
		maxConc   = flag.Int("max-concurrent", 3, "max concurrent agents (subscription rate-limit guard)")
	)
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	svc := core.NewService(st)

	// A previous run may have exited with tasks mid-flight; their agent goroutines
	// are gone, so requeue them (and drop orphaned human checkpoints) before the
	// orchestrator starts, otherwise they'd be stuck with no recovery path.
	if n, err := svc.RecoverInFlight(); err != nil {
		log.Printf("startup recovery failed: %v", err)
	} else if n > 0 {
		log.Printf("recovered %d task(s) left in-flight by a previous run → backlog", n)
	}

	// Live PTY sessions: the orchestrator runs each agent in a terminal, the web
	// layer attaches the browser to it over a WebSocket, and finish_task closes it.
	term := terminal.NewManager()

	mcpSrv := mcpserver.New(svc, term)
	mcpURL := fmt.Sprintf("http://localhost:%d/mcp", *port)

	// One worktree manager, shared: the orchestrator creates per-task worktrees,
	// the service merges and tears them down (same root, so they agree on paths).
	wt := worktree.New(*wtRoot)
	svc.UseWorktrees(wt)
	orch := orchestrator.New(svc, *workdir, wt, term, mcpURL, *maxConc)

	// The -max-concurrent flag is only the default; a value the human set in
	// Settings (persisted) wins across restarts, so apply it over the flag here.
	if n, ok, err := svc.GetMaxConcurrent(); err != nil {
		log.Printf("couldn't read persisted max_concurrent: %v", err)
	} else if ok {
		orch.SetLimit(n)
		log.Printf("max concurrent agents set to %d (from Settings)", n)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go orch.Run(ctx)

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil)
	// staticDir (the -static flag) wins for dev; otherwise a release binary built
	// with `-tags embed` serves the frontend it baked in; otherwise API-only.
	webMux := web.New(svc, term, resolveStatic(*staticDir), webassets.Assets(), orch)

	root := http.NewServeMux()
	root.Handle("/mcp", mcpHandler)
	root.Handle("/mcp/", mcpHandler)
	root.Handle("/", webMux)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("ultraflow listening on http://localhost%s  (mcp: %s)", addr, mcpURL)

	srv := &http.Server{Addr: addr, Handler: root}
	go func() {
		<-ctx.Done()
		term.CloseAll() // don't leak agent processes past the daemon
		_ = srv.Close()
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}

	// Graceful path only (ListenAndServe returned ErrServerClosed): checkpoint the
	// WAL and close the DB now that the server and agents are down.
	if err := st.Close(); err != nil {
		log.Printf("store close: %v", err)
	}
}

// resolveStatic returns an absolute path if the static dir exists, else "" so the
// daemon runs API-only until the frontend is built.
func resolveStatic(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(abs); err != nil {
		return ""
	}
	return abs
}
