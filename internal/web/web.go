// Package web serves the board's REST API plus a live SSE event stream, and the
// static React build when present.
package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/coder/websocket"

	"ultraflow/internal/core"
	"ultraflow/internal/model"
	"ultraflow/internal/terminal"
)

// concurrencyController is the slice of the orchestrator the settings API needs:
// read the live parallel-agent limit and change it. Kept as an interface so web
// stays decoupled from the orchestrator package (no import cycle); may be nil in
// API-only/test setups, in which case the limit is still persisted but not
// applied live.
type concurrencyController interface {
	Limit() int
	SetLimit(int)
}

// reviser re-engages a reviewed/failed task's agent with the human's feedback
// (the "send it back" action). The orchestrator implements it; recovered from the
// conc value by a type assertion so New's signature stays put. nil in API-only
// setups, in which case the revise endpoint reports it's unavailable.
type reviser interface {
	Revise(taskID, feedback string) error
}

type server struct {
	svc     *core.Service
	term    *terminal.Manager
	conc    concurrencyController
	reviser reviser
}

// New returns the HTTP handler for the board. The frontend is served at the root
// from, in order of preference: staticDir (an explicit on-disk build, for dev);
// otherwise the embedded assets FS (a self-contained release binary); otherwise
// nothing (API-only). Pass "" and nil to run API-only. conc lets the settings
// API read and change the running orchestrator's concurrency limit; pass nil to
// disable the live change (the value is still persisted).
func New(svc *core.Service, term *terminal.Manager, staticDir string, assets fs.FS, conc concurrencyController) http.Handler {
	s := &server{svc: svc, term: term, conc: conc}
	// The orchestrator passed as conc also drives review send-backs; recover that
	// capability without widening New's signature (a fake/nil conc simply lacks it).
	if r, ok := conc.(reviser); ok {
		s.reviser = r
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/board", s.board)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("POST /api/settings/concurrency", s.setConcurrencyHandler)
	mux.HandleFunc("GET /api/projects", s.listProjects)
	mux.HandleFunc("POST /api/projects", s.createProject)
	mux.HandleFunc("POST /api/projects/pick", s.pickProject)
	mux.HandleFunc("DELETE /api/projects/{id}", s.deleteProject)
	mux.HandleFunc("GET /api/tasks", s.listTasks)
	mux.HandleFunc("POST /api/tasks", s.createTask)
	mux.HandleFunc("GET /api/tasks/{id}", s.getTask)
	mux.HandleFunc("GET /api/tasks/{id}/events", s.taskEvents)
	mux.HandleFunc("GET /api/tasks/{id}/diff", s.diff)
	mux.HandleFunc("POST /api/tasks/{id}/revise", s.revise)
	mux.HandleFunc("GET /api/tasks/{id}/shots", s.listShots)
	mux.HandleFunc("GET /api/tasks/{id}/shots/{name}", s.getShot)
	mux.HandleFunc("POST /api/tasks/{id}/retry", s.retryTask)
	mux.HandleFunc("POST /api/tasks/{id}/merge", s.mergeTask)
	mux.HandleFunc("POST /api/tasks/{id}/done", s.finishReview)
	mux.HandleFunc("GET /api/tasks/{id}/terminal", s.terminal)
	mux.HandleFunc("GET /api/human_requests", s.pendingRequests)
	mux.HandleFunc("POST /api/human_requests/{id}/answer", s.answer)
	mux.HandleFunc("GET /api/events", s.events)
	switch {
	case staticDir != "":
		mux.Handle("/", http.FileServer(http.Dir(staticDir)))
	case assets != nil:
		mux.Handle("/", http.FileServer(http.FS(assets)))
	}
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.svc.ListTasks()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tasks == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

// board returns everything the board needs in one round trip: tasks, the pending
// ask_human requests (the attention rail), and the latest activity line per task.
func (s *server) board(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.svc.ListTasks()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	reqs, err := s.svc.PendingRequests()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	activity, err := s.svc.LatestActivity()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	activityKind, err := s.svc.LatestActivityKind()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tasks == nil {
		tasks = []model.Task{}
	}
	if reqs == nil {
		reqs = []model.HumanRequest{}
	}
	projects, err := s.svc.ListProjects()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if projects == nil {
		projects = []model.Project{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks":        tasks,
		"requests":     reqs,
		"activity":     activity,
		"activityKind": activityKind,
		"projects":     projects,
	})
}

// currentConcurrency reports the limit the orchestrator is actually running
// with (the live source of truth). Falls back to the persisted value, then to
// the min, when there's no live orchestrator (API-only/tests).
func (s *server) currentConcurrency() int {
	if s.conc != nil {
		return s.conc.Limit()
	}
	if n, ok, err := s.svc.GetMaxConcurrent(); err == nil && ok {
		return n
	}
	return core.MinConcurrent
}

// getSettings returns the daemon-wide preferences the board can edit.
func (s *server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"maxConcurrent":    s.currentConcurrency(),
		"maxConcurrentMin": core.MinConcurrent,
		"maxConcurrentMax": core.MaxConcurrentCap,
	})
}

// setConcurrencyHandler validates and persists a new parallel-agent limit, then
// applies it to the live orchestrator so queued tasks can start immediately when
// it's raised. Returns the effective (clamped) value.
func (s *server) setConcurrencyHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Value int `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Value < core.MinConcurrent || body.Value > core.MaxConcurrentCap {
		http.Error(w, fmt.Sprintf("value must be between %d and %d", core.MinConcurrent, core.MaxConcurrentCap), http.StatusBadRequest)
		return
	}
	n, err := s.svc.SetMaxConcurrent(body.Value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.conc != nil {
		s.conc.SetLimit(n)
	}
	writeJSON(w, http.StatusOK, map[string]any{"maxConcurrent": n})
}

func (s *server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.svc.ListProjects()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if projects == nil {
		projects = []model.Project{}
	}
	writeJSON(w, http.StatusOK, projects)
}

func (s *server) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		RepoPath string `json:"repoPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	p, err := s.svc.CreateProject(strings.TrimSpace(body.Name), strings.TrimSpace(body.RepoPath))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *server) deleteProject(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteProject(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// pickProject opens the OS-native folder chooser on the daemon's machine (this is
// a local single-user tool) and registers the picked directory as a project,
// naming it after the folder. Returns 204 if the user cancels the dialog.
func (s *server) pickProject(w http.ResponseWriter, r *http.Request) {
	path, ok, err := pickFolder()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNoContent) // cancelled
		return
	}
	p, err := s.svc.CreateProject(filepath.Base(path), path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// pickFolder shows a native directory picker and returns the chosen absolute
// path. ok=false means the user cancelled. Currently macOS only (the user's
// platform); other OSes return an error so the UI can fall back.
func pickFolder() (path string, ok bool, err error) {
	if runtime.GOOS != "darwin" {
		return "", false, fmt.Errorf("native folder picker not supported on %s yet", runtime.GOOS)
	}
	out, err := exec.Command("osascript", "-e",
		`POSIX path of (choose folder with prompt "Select the project repo folder")`).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok &&
			(strings.Contains(string(ee.Stderr), "canceled") || strings.Contains(string(ee.Stderr), "-128")) {
			return "", false, nil // user pressed Cancel
		}
		return "", false, err
	}
	path = strings.TrimRight(strings.TrimSpace(string(out)), "/")
	if path == "" {
		return "", false, nil
	}
	return path, true, nil
}

func (s *server) retryTask(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.RetryTask(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// mergeTask lands a reviewed task's worktree branch into the project repo and
// finishes it. A merge that can't complete (e.g. a conflict) returns 409 with the
// git explanation; the task is left in review with its worktree intact.
func (s *server) mergeTask(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.MergeTask(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// finishReview marks a reviewed task done without a merge — for tasks that ran in
// place (no worktree to land), where "merge" doesn't apply.
func (s *server) finishReview(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.FinishReview(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// diff returns the changes a reviewed task made in its worktree (magnitude +
// unified patch) for the review diff viewer. 404 when the task has no worktree
// to diff (ran in place, or already merged and torn down).
func (s *server) diff(w http.ResponseWriter, r *http.Request) {
	d, err := s.svc.TaskDiff(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// revise re-engages the task's agent with the human's feedback (the review
// "send it back" action). 409 if the task isn't in a state that can be sent back;
// 503 if there's no live orchestrator to run the agent (API-only).
func (s *server) revise(w http.ResponseWriter, r *http.Request) {
	if s.reviser == nil {
		http.Error(w, "sending back to the agent isn't available here", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.reviser.Revise(r.PathValue("id"), body.Message); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// listShots returns the screenshot filenames the agent left for a task, if any.
// Absent dir / no worktree is not an error — it's simply an empty gallery. Same
// capture the daemon snapshots onto an ask_human request (see core.TaskShots).
func (s *server) listShots(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.TaskShots(r.PathValue("id")))
}

// getShot serves one screenshot image by name. The name is validated to a bare
// image filename (no path separators or "..") so it can't escape the shots dir.
func (s *server) getShot(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") || !core.IsImageFile(name) {
		http.Error(w, "bad screenshot name", http.StatusBadRequest)
		return
	}
	dir, err := s.svc.ShotsDir(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, filepath.Join(dir, name))
}

// terminal upgrades to a WebSocket bridged to the task's live PTY session: it
// replays the scrollback, streams new output to the browser (binary frames), and
// writes the browser's keystrokes back to the PTY. A text frame carries a resize
// control message. This is what makes the card's terminal a real, interactive
// terminal (input, output, Ctrl-C) rather than a read-only log.
func (s *server) terminal(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.term.Get(r.PathValue("id"))
	if !ok {
		http.Error(w, "no live terminal for this task", http.StatusNotFound)
		return
	}
	// This terminal drives an agent running with bypassPermissions, so only allow
	// connections from the local board itself (dev :5173, the built app :7787).
	// A remote page's Origin is its own host, which the browser won't let it forge,
	// so this blocks cross-site WebSocket hijacking.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost:*", "127.0.0.1:*", "[::1]:*"},
	})
	if err != nil {
		return
	}
	defer c.CloseNow()

	// Cancel when either direction fails, so a browser disconnect (which the reader
	// goroutine notices) also unblocks the writer loop below — otherwise the handler
	// leaks, blocked on a quiet session until the agent exits.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	scrollback, out, detach := sess.Attach()
	defer detach()
	if len(scrollback) > 0 {
		if err := c.Write(ctx, websocket.MessageBinary, scrollback); err != nil {
			return
		}
	}

	// Browser → PTY: binary frames are keystrokes; a text frame is a control
	// message (resize).
	go func() {
		defer cancel()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			switch typ {
			case websocket.MessageBinary:
				_ = sess.Write(data)
			case websocket.MessageText:
				var msg struct {
					Resize *struct {
						Rows uint16 `json:"rows"`
						Cols uint16 `json:"cols"`
					} `json:"resize"`
				}
				if json.Unmarshal(data, &msg) == nil && msg.Resize != nil {
					_ = sess.Resize(msg.Resize.Rows, msg.Resize.Cols)
				}
			}
		}
	}()

	// PTY → browser, until the session ends, the client falls behind, or it
	// disconnects (ctx cancelled by the reader goroutine).
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-out:
			if !ok {
				_ = c.Close(websocket.StatusNormalClosure, "session ended")
				return
			}
			if err := c.Write(ctx, websocket.MessageBinary, chunk); err != nil {
				return
			}
		}
	}
}

func (s *server) taskEvents(w http.ResponseWriter, r *http.Request) {
	evs, err := s.svc.TaskEvents(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if evs == nil {
		evs = []model.Event{}
	}
	writeJSON(w, http.StatusOK, evs)
}

func (s *server) createTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title   string `json:"title"`
		Body    string `json:"body"`
		Project string `json:"project"`
		Agent   string `json:"agent"`
		Flow    string `json:"flow"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	t, err := s.svc.CreateTaskFull(body.Title, body.Body, body.Project, body.Agent, body.Flow)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *server) getTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.svc.GetTask(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *server) pendingRequests(w http.ResponseWriter, r *http.Request) {
	reqs, err := s.svc.PendingRequests()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if reqs == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, reqs)
}

func (s *server) answer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.svc.AnswerHuman(r.PathValue("id"), body.Answer); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.svc.Broker.Subscribe()
	defer s.svc.Broker.Unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(msg)
			_, _ = w.Write([]byte("\n\n"))
			f.Flush()
		case <-time.After(30 * time.Second):
			_, _ = w.Write([]byte(": ping\n\n"))
			f.Flush()
		}
	}
}
