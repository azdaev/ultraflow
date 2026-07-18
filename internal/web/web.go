// Package web serves the board's REST API plus a live SSE event stream, and the
// static React build when present.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"ultraflow/internal/core"
	"ultraflow/internal/journal"
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

// rebaser re-engages a reviewed task's agent to resolve a stale-branch rebase
// whose conflicts the mechanical auto-rebase couldn't handle (the merge path
// escalation for core.ErrRebaseConflict). Same orchestrator, recovered the same
// way as reviser; nil in API-only setups.
type rebaser interface {
	Rebase(taskID string) error
}

// pauser holds or releases ALL agents (the "pause all" action): pausing both blocks
// new starts and freezes running agents, resuming brings them back. The orchestrator
// implements it; recovered from conc by type assertion like reviser/rebaser so New's
// signature is untouched. nil in API-only/test setups, in which case the pause
// endpoint reports unavailable and `paused` reads false.
type pauser interface {
	Paused() bool
	SetPaused(bool)
}

type server struct {
	svc       Service
	term      *terminal.Manager
	conc      concurrencyController
	reviser   reviser
	rebaser   rebaser
	pauser    pauser
	attachDir string
}

// New returns the HTTP handler for the board. The frontend is served at the root
// from, in order of preference: staticDir (an explicit on-disk build, for dev);
// otherwise the embedded assets FS (a self-contained release binary); otherwise
// nothing (API-only). Pass "" and nil to run API-only. conc lets the settings
// API read and change the running orchestrator's concurrency limit; pass nil to
// disable the live change (the value is still persisted).
// attachDir is where images uploaded from a composer are saved (see
// uploadImages); it's created on first upload.
func New(svc Service, term *terminal.Manager, staticDir, attachDir string, assets fs.FS, conc concurrencyController) http.Handler {
	s := &server{svc: svc, term: term, conc: conc, attachDir: attachDir}
	// The orchestrator passed as conc also drives review send-backs; recover that
	// capability without widening New's signature (a fake/nil conc simply lacks it).
	if r, ok := conc.(reviser); ok {
		s.reviser = r
	}
	if r, ok := conc.(rebaser); ok {
		s.rebaser = r
	}
	if p, ok := conc.(pauser); ok {
		s.pauser = p
	}
	// Release mode silences gin's debug banner/route dump; the daemon was previously
	// silent on startup and we keep it that way.
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/api/board", s.board)
	r.GET("/api/settings", s.getSettings)
	r.POST("/api/settings/concurrency", s.setConcurrencyHandler)
	r.POST("/api/settings/context-cap", s.setContextCapHandler)
	r.POST("/api/settings/pause", s.setPauseHandler)
	r.POST("/api/settings/telegram", s.setTelegramHandler)
	r.GET("/api/projects", s.listProjects)
	r.POST("/api/projects", s.createProject)
	r.POST("/api/projects/pick", s.pickProject)
	r.DELETE("/api/projects/:id", s.deleteProject)
	r.GET("/api/tasks", s.listTasks)
	r.POST("/api/tasks", s.createTask)
	r.POST("/api/archive_closed", s.archiveClosed)
	r.GET("/api/tasks/:id", s.getTask)
	r.DELETE("/api/tasks/:id", s.deleteTask)
	r.POST("/api/tasks/:id/cancel", s.cancelTask)
	r.GET("/api/tasks/:id/events", s.taskEvents)
	r.GET("/api/tasks/:id/diff", s.diff)
	r.POST("/api/tasks/:id/revise", s.revise)
	r.GET("/api/tasks/:id/shots", s.listShots)
	r.GET("/api/tasks/:id/shots/:name", s.getShot)
	r.POST("/api/tasks/:id/retry", s.retryTask)
	r.POST("/api/tasks/:id/merge", s.mergeTask)
	r.POST("/api/tasks/:id/done", s.finishReview)
	r.GET("/api/tasks/:id/terminal", s.terminal)
	r.GET("/api/human_requests", s.pendingRequests)
	r.POST("/api/human_requests/:id/answer", s.answer)
	r.POST("/api/uploads", s.uploadImages)
	r.GET("/api/uploads/:name", s.serveUpload)
	r.GET("/api/events", s.events)
	r.POST("/api/journal", s.journalUI)
	r.POST("/api/feedback", s.feedbackHandler)
	// The React build (and its assets) is everything that isn't an API route; serve
	// it as the fallback so client-side paths reach index.html.
	switch {
	case staticDir != "":
		r.NoRoute(gin.WrapH(http.FileServer(http.Dir(staticDir))))
	case assets != nil:
		r.NoRoute(gin.WrapH(http.FileServer(http.FS(assets))))
	}
	return r
}

// writeJSON renders v as JSON through gin, which under the `go_json` build tag
// serializes via goccy/go-json (the fast path). Kept as a helper so every handler
// emits the same shape the encoding/json version did.
func writeJSON(c *gin.Context, code int, v any) {
	c.JSON(code, v)
}

// writeErr sends a plain-text error with the given status — the error-path mirror
// of writeJSON, so a handler bails in one line (writeErr(c, code, msg)) instead of
// repeating the c.Writer / http.Error boilerplate. Arg order matches writeJSON.
func writeErr(c *gin.Context, code int, msg string) {
	http.Error(c.Writer, msg, code)
}

// writeOK is the 200 acknowledgement the action handlers (retry, cancel, delete,
// merge, …) return when there's nothing else to say. One helper keeps the success
// envelope the frontend reads in a single place.
func writeOK(c *gin.Context) {
	writeJSON(c, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) listTasks(c *gin.Context) {
	tasks, err := s.svc.ListTasks()
	if err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	if tasks == nil {
		writeJSON(c, http.StatusOK, []any{})
		return
	}
	writeJSON(c, http.StatusOK, tasks)
}

// board returns everything the board needs in one round trip: tasks, the pending
// ask_human requests (the attention rail), and the latest activity line per task.
func (s *server) board(c *gin.Context) {
	tasks, err := s.svc.ListTasks()
	if err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	reqs, err := s.svc.PendingRequests()
	if err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	activity, activityKind, err := s.svc.LatestActivity()
	if err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
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
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	if projects == nil {
		projects = []model.Project{}
	}
	writeJSON(c, http.StatusOK, map[string]any{
		"tasks":        tasks,
		"requests":     reqs,
		"activity":     activity,
		"activityKind": activityKind,
		"projects":     projects,
		// Live flow progress per multi-step task, so the card's stepper can light the
		// active step and caption it. Solo tasks (no run) are simply absent.
		"runs": s.svc.RunsProgress(tasks),
		// Latest per-task context size (tokens) for the card's context meter, so a
		// fresh load isn't blank until the next transcript poll. Live updates arrive
		// as "context" SSE events. Absent for tasks with no reading yet.
		"context": s.svc.ContextTokens(),
		// The configured per-agent context budget (0 = off), so the meter can scale
		// to the real /compact threshold. Live changes arrive as "settings" events.
		"contextCap": s.svc.ContextCap(),
		// Latest per-task model name the agent is actually running (e.g.
		// "claude-opus-4-8"), for the card's agent footer. Live updates arrive as
		// "model" SSE events. Absent until the agent's first transcript line.
		"models": s.svc.Models(),
		// Whether all agents are currently held, so a fresh load / SSE reconnect
		// snapshot carries the pause state (live toggles arrive as "paused" events).
		"paused": s.paused(),
	})
}

// paused reports whether all agents are currently held. False when there's no live
// orchestrator (API-only/tests) — pause is a transient runtime state, not persisted.
func (s *server) paused() bool {
	return s.pauser != nil && s.pauser.Paused()
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
func (s *server) getSettings(c *gin.Context) {
	tg, _, _ := s.svc.TelegramSettings()
	writeJSON(c, http.StatusOK, map[string]any{
		"maxConcurrent":    s.currentConcurrency(),
		"maxConcurrentMin": core.MinConcurrent,
		"maxConcurrentMax": core.MaxConcurrentCap,
		// Per-agent context budget in tokens (0 = off). When a running agent crosses
		// it, Ultraflow injects /compact so it summarizes and continues on a tighter
		// working set.
		"contextCap":    s.svc.ContextCap(),
		"contextCapMin": core.MinContextCap,
		"contextCapMax": core.MaxContextCap,
		// nativePicker is true where the daemon can open an OS folder dialog
		// (macOS only, see pickFolder). Off it, the board falls back to a
		// paste-the-path field that POSTs to /api/projects.
		"nativePicker": runtime.GOOS == "darwin",
		"telegram": map[string]any{
			"enabled": tg.Enabled, "hasToken": tg.Token != "", "userId": tg.UserID, "chatId": tg.ChatID,
		},
		// Whether all agents are currently held (the "pause all" toggle).
		"paused": s.paused(),
	})
}

func (s *server) setTelegramHandler(c *gin.Context) {
	var body struct {
		Enabled bool   `json:"enabled"`
		Token   string `json:"token"`
		UserID  int64  `json:"userId"`
		ChatID  int64  `json:"chatId"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		writeErr(c, http.StatusBadRequest, err.Error())
		return
	}
	old, _, _ := s.svc.TelegramSettings()
	token := strings.TrimSpace(body.Token)
	if token == "" {
		token = old.Token
	}
	cfg := core.TelegramSettings{Enabled: body.Enabled, Token: token, UserID: body.UserID, ChatID: body.ChatID}
	if err := s.svc.SetTelegramSettings(cfg); err != nil {
		writeErr(c, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"enabled": cfg.Enabled, "hasToken": cfg.Token != "", "userId": cfg.UserID, "chatId": cfg.ChatID})
}

// setConcurrencyHandler validates and persists a new parallel-agent limit, then
// applies it to the live orchestrator so queued tasks can start immediately when
// it's raised. Returns the effective (clamped) value.
func (s *server) setConcurrencyHandler(c *gin.Context) {
	var body struct {
		Value int `json:"value"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		writeErr(c, http.StatusBadRequest, err.Error())
		return
	}
	if body.Value < core.MinConcurrent || body.Value > core.MaxConcurrentCap {
		writeErr(c, http.StatusBadRequest, fmt.Sprintf("value must be between %d and %d", core.MinConcurrent, core.MaxConcurrentCap))
		return
	}
	n, err := s.svc.SetMaxConcurrent(body.Value)
	if err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	if s.conc != nil {
		s.conc.SetLimit(n)
	}
	writeJSON(c, http.StatusOK, map[string]any{"maxConcurrent": n})
}

// feedbackHandler records a quick human feedback note left from the board.
func (s *server) feedbackHandler(c *gin.Context) {
	var body struct {
		Text string `json:"text"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		writeErr(c, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeErr(c, http.StatusBadRequest, "text is required")
		return
	}
	if err := s.svc.AddFeedback(body.Text, body.Path); err != nil {
		writeErr(c, http.StatusBadRequest, err.Error())
		return
	}
	writeOK(c)
}

// setContextCapHandler validates and persists the per-agent context budget. 0
// disables it; any other value must fall in the allowed band. The new value takes
// effect on the next transcript poll of each running agent — no restart needed.
func (s *server) setContextCapHandler(c *gin.Context) {
	var body struct {
		Value int `json:"value"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		writeErr(c, http.StatusBadRequest, err.Error())
		return
	}
	if body.Value != 0 && (body.Value < core.MinContextCap || body.Value > core.MaxContextCap) {
		writeErr(c, http.StatusBadRequest, fmt.Sprintf("value must be 0 (off) or between %d and %d", core.MinContextCap, core.MaxContextCap))
		return
	}
	n, err := s.svc.SetContextCap(body.Value)
	if err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"contextCap": n})
}

// setPauseHandler holds or releases ALL agents (the "pause all" toggle): pausing
// blocks new starts and freezes running agents, resuming brings them back. The
// orchestrator broadcasts the new state over SSE so every open board syncs. 503s
// when there's no live orchestrator (API-only), where pause has no meaning.
func (s *server) setPauseHandler(c *gin.Context) {
	if s.pauser == nil {
		http.Error(c.Writer, "pausing agents isn't available", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Paused bool `json:"paused"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		http.Error(c.Writer, err.Error(), http.StatusBadRequest)
		return
	}
	s.pauser.SetPaused(body.Paused)
	writeJSON(c, http.StatusOK, map[string]any{"paused": body.Paused})
}

func (s *server) listProjects(c *gin.Context) {
	projects, err := s.svc.ListProjects()
	if err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	if projects == nil {
		projects = []model.Project{}
	}
	writeJSON(c, http.StatusOK, projects)
}

// createProject registers a project from a pasted absolute path — the fallback
// used where the native folder picker isn't available (non-macOS, see
// pickFolder). The path is validated to be an existing git repo and its basename
// becomes the project name, matching the native picker flow (pickProject).
func (s *server) createProject(c *gin.Context) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		writeErr(c, http.StatusBadRequest, err.Error())
		return
	}
	path := strings.TrimRight(strings.TrimSpace(body.Path), "/")
	if err := validateRepoPath(path); err != nil {
		writeErr(c, http.StatusBadRequest, err.Error())
		return
	}
	p, err := s.svc.CreateProject(filepath.Base(path), path)
	if err != nil {
		writeErr(c, http.StatusConflict, err.Error())
		return
	}
	writeJSON(c, http.StatusCreated, p)
}

// validateRepoPath checks a pasted project path points at an existing directory
// that is a git repository. The .git entry may be a directory (normal clone) or
// a file (worktree/submodule), so a plain Stat covers both.
func validateRepoPath(path string) error {
	if path == "" {
		return fmt.Errorf("paste the path to the project's git repo folder")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("please paste an absolute path (starting with /)")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("no folder at %s", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a folder", path)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return fmt.Errorf("%s isn't a git repo (no .git found)", path)
	}
	return nil
}

func (s *server) deleteProject(c *gin.Context) {
	if err := s.svc.DeleteProject(c.Param("id")); err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(c)
}

// pickProject opens the OS-native folder chooser on the daemon's machine (this is
// a local single-user tool) and registers the picked directory as a project,
// naming it after the folder. Returns 204 if the user cancels the dialog.
func (s *server) pickProject(c *gin.Context) {
	path, ok, err := pickFolder()
	if err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		c.Writer.WriteHeader(http.StatusNoContent) // cancelled
		return
	}
	p, err := s.svc.CreateProject(filepath.Base(path), path)
	if err != nil {
		writeErr(c, http.StatusConflict, err.Error())
		return
	}
	writeJSON(c, http.StatusCreated, p)
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

func (s *server) retryTask(c *gin.Context) {
	if err := s.svc.RetryTask(c.Param("id")); err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(c)
}

// cancelTask stops a running/queued/parked task: the service flips it to
// `cancelled` (guarded, so it can't clobber a task that just finished) and frees
// its runtime, then we kill the live agent's process group here — this handler
// owns the terminal manager. 409 if the task isn't in a stoppable state.
func (s *server) cancelTask(c *gin.Context) {
	id := c.Param("id")
	stopped, err := s.svc.CancelTask(id)
	if err != nil {
		writeErr(c, http.StatusConflict, err.Error())
		return
	}
	// Kill the agent AFTER the status is `cancelled`, so the orchestrator's self-heal
	// loop reads that state on the resulting exit and stands down instead of retrying.
	if stopped && s.term != nil {
		if sess, ok := s.term.Get(id); ok {
			sess.Close()
		}
	}
	writeOK(c)
}

// deleteTask removes a not-live task (backlog or a terminal done/failed/cancelled)
// for good, tearing down any leftover worktree. 409 if the task is still live or
// in review — it must be stopped or finished first.
func (s *server) deleteTask(c *gin.Context) {
	if err := s.svc.DeleteTask(c.Param("id")); err != nil {
		writeErr(c, http.StatusConflict, err.Error())
		return
	}
	writeOK(c)
}

// archiveClosed removes every closed (done or cancelled) task in one sweep — the
// board's "Clear" affordance so the Done column doesn't grow without bound.
// journalUI records a front-end interaction (a click, a drag, a panel opened) in
// the activity journal. The body is a free JSON object; "event" names it and the
// rest are recorded as fields. Fire-and-forget: always 204, never errors the UI.
func (s *server) journalUI(c *gin.Context) {
	var body map[string]any
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil || body == nil {
		c.Status(http.StatusNoContent)
		return
	}
	event, _ := body["event"].(string)
	delete(body, "event")
	if event == "" {
		event = "interaction"
	}
	journal.Log("ui", event, body)
	c.Status(http.StatusNoContent)
}

func (s *server) archiveClosed(c *gin.Context) {
	n, err := s.svc.ArchiveClosed()
	if err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"removed": n})
}

// mergeTask lands a reviewed task's worktree branch into the project repo and
// finishes it. Before merging it auto-rebases the branch onto the latest main so
// what lands is what was reviewed. If that rebase hits conflicts the auto-rebase
// can't resolve, the task's agent is re-engaged to resolve them (self-heal) and we
// report "rebasing" instead of a dead-end. Any other merge failure returns 409
// with the git explanation; the task is left in review with its worktree intact.
func (s *server) mergeTask(c *gin.Context) {
	id := c.Param("id")
	err := s.svc.MergeTask(id)
	if errors.Is(err, core.ErrRebaseConflict) {
		if s.rebaser == nil {
			writeErr(c, http.StatusServiceUnavailable, "the branch is behind main and needs the agent to rebase, which isn't available here")
			return
		}
		if rerr := s.rebaser.Rebase(id); rerr != nil {
			writeErr(c, http.StatusConflict, rerr.Error())
			return
		}
		writeJSON(c, http.StatusOK, map[string]string{"status": "rebasing"})
		return
	}
	if err != nil {
		writeErr(c, http.StatusConflict, err.Error())
		return
	}
	writeOK(c)
}

// finishReview marks a reviewed task done without a merge — for tasks that ran in
// place (no worktree to land), where "merge" doesn't apply.
func (s *server) finishReview(c *gin.Context) {
	if err := s.svc.FinishReview(c.Param("id")); err != nil {
		writeErr(c, http.StatusConflict, err.Error())
		return
	}
	writeOK(c)
}

// diff returns the changes a reviewed task made in its worktree (magnitude +
// unified patch) for the review diff viewer. 404 when the task has no worktree
// to diff (ran in place, or already merged and torn down).
func (s *server) diff(c *gin.Context) {
	d, err := s.svc.TaskDiff(c.Param("id"))
	if err != nil {
		writeErr(c, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(c, http.StatusOK, d)
}

// revise re-engages the task's agent with the human's feedback (the review
// "send it back" action). 409 if the task isn't in a state that can be sent back;
// 503 if there's no live orchestrator to run the agent (API-only).
func (s *server) revise(c *gin.Context) {
	if s.reviser == nil {
		writeErr(c, http.StatusServiceUnavailable, "sending back to the agent isn't available here")
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		writeErr(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.reviser.Revise(c.Param("id"), body.Message); err != nil {
		writeErr(c, http.StatusConflict, err.Error())
		return
	}
	writeOK(c)
}

// listShots returns the screenshot filenames the agent left for a task, if any.
// Absent dir / no worktree is not an error — it's simply an empty gallery. Same
// capture the daemon snapshots onto an ask_human request (see core.TaskShots).
func (s *server) listShots(c *gin.Context) {
	writeJSON(c, http.StatusOK, s.svc.TaskShots(c.Param("id")))
}

// safeImageName reports whether name is a bare, renderable image filename safe to
// join into a served directory: no path separators or ".." that could escape it,
// and a real image extension. It is the single path-traversal guard behind both
// getShot (the shots dir) and serveUpload (the attachments dir) — the two
// user-supplied filenames the board serves back off disk.
func safeImageName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "/\\") && !strings.Contains(name, "..") && core.IsImageFile(name)
}

// getShot serves one screenshot image by name, guarded by safeImageName so the
// name can't escape the shots dir.
func (s *server) getShot(c *gin.Context) {
	name := c.Param("name")
	if !safeImageName(name) {
		writeErr(c, http.StatusBadRequest, "bad screenshot name")
		return
	}
	dir, err := s.svc.ShotsDir(c.Param("id"))
	if err != nil {
		writeErr(c, http.StatusNotFound, "not found")
		return
	}
	http.ServeFile(c.Writer, c.Request, filepath.Join(dir, name))
}

// terminal upgrades to a WebSocket bridged to the task's live PTY session: it
// replays the scrollback, streams new output to the browser (binary frames), and
// writes the browser's keystrokes back to the PTY. A text frame carries a resize
// control message. This is what makes the card's terminal a real, interactive
// terminal (input, output, Ctrl-C) rather than a read-only log.
func (s *server) terminal(c *gin.Context) {
	sess, ok := s.term.Get(c.Param("id"))
	if !ok {
		writeErr(c, http.StatusNotFound, "no live terminal for this task")
		return
	}
	// This terminal drives an agent running with bypassPermissions, so only allow
	// connections from the local board itself (dev :5173, the built app :7787).
	// A remote page's Origin is its own host, which the browser won't let it forge,
	// so this blocks cross-site WebSocket hijacking.
	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost:*", "127.0.0.1:*", "[::1]:*"},
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	// Cancel when either direction fails, so a browser disconnect (which the reader
	// goroutine notices) also unblocks the writer loop below — otherwise the handler
	// leaks, blocked on a quiet session until the agent exits.
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	scrollback, out, detach := sess.Attach()
	defer detach()
	if len(scrollback) > 0 {
		if err := conn.Write(ctx, websocket.MessageBinary, scrollback); err != nil {
			return
		}
	}

	// Browser → PTY: binary frames are keystrokes; a text frame is a control
	// message (resize).
	go func() {
		defer cancel()
		for {
			typ, data, err := conn.Read(ctx)
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
				_ = conn.Close(websocket.StatusNormalClosure, "session ended")
				return
			}
			if err := conn.Write(ctx, websocket.MessageBinary, chunk); err != nil {
				return
			}
		}
	}
}

func (s *server) taskEvents(c *gin.Context) {
	evs, err := s.svc.TaskEvents(c.Param("id"))
	if err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	if evs == nil {
		evs = []model.Event{}
	}
	writeJSON(c, http.StatusOK, evs)
}

func (s *server) createTask(c *gin.Context) {
	var body struct {
		Title   string `json:"title"`
		Body    string `json:"body"`
		Project string `json:"project"`
		Agent   string `json:"agent"`
		Flow    string `json:"flow"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		writeErr(c, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		writeErr(c, http.StatusBadRequest, "title is required")
		return
	}
	// A project is mandatory: a task with no project runs in the shared workdir
	// and its code is stranded uncommitted on main. The composer enforces this in
	// the UI; guard the endpoint too so no path can create a project-less task.
	if strings.TrimSpace(body.Project) == "" {
		writeErr(c, http.StatusBadRequest, "project is required")
		return
	}
	t, err := s.svc.CreateTaskFull(body.Title, body.Body, body.Project, body.Agent, body.Flow)
	if err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(c, http.StatusCreated, t)
}

func (s *server) getTask(c *gin.Context) {
	t, err := s.svc.GetTask(c.Param("id"))
	if err != nil {
		writeErr(c, http.StatusNotFound, "not found")
		return
	}
	writeJSON(c, http.StatusOK, t)
}

func (s *server) pendingRequests(c *gin.Context) {
	reqs, err := s.svc.PendingRequests()
	if err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	if reqs == nil {
		writeJSON(c, http.StatusOK, []any{})
		return
	}
	writeJSON(c, http.StatusOK, reqs)
}

func (s *server) answer(c *gin.Context) {
	var body struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		writeErr(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.svc.AnswerHuman(c.Param("id"), body.Answer); err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(c)
}

// maxUploadBytes caps a single uploaded image at 10MB — enough for a screenshot
// or pasted photo, small enough that a stray large file can't fill the disk.
const maxUploadBytes = 10 << 20

// uploadImages accepts a multipart form (field `files`) of images posted from any
// composer, saves each to attachDir under a fresh random name, and returns
// [{name, path, url}] for each: `path` is the absolute on-disk path we append to
// the outgoing text so the agent's Read tool can open it, `url` a board-relative
// link for the thumbnail preview. Non-images and oversized files are rejected.
func (s *server) uploadImages(c *gin.Context) {
	if s.attachDir == "" {
		writeErr(c, http.StatusServiceUnavailable, "image uploads aren't available here")
		return
	}
	// Bound the whole request so a client can't stream an unbounded body; each file
	// is additionally checked against maxUploadBytes below.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<20)
	form, err := c.MultipartForm()
	if err != nil {
		writeErr(c, http.StatusBadRequest, err.Error())
		return
	}
	files := form.File["files"]
	if len(files) == 0 {
		writeErr(c, http.StatusBadRequest, "no files uploaded")
		return
	}
	if err := os.MkdirAll(s.attachDir, 0o755); err != nil {
		writeErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	type uploaded struct {
		Name string `json:"name"`
		Path string `json:"path"`
		URL  string `json:"url"`
	}
	out := make([]uploaded, 0, len(files))
	for _, fh := range files {
		if !core.IsImageFile(fh.Filename) {
			writeErr(c, http.StatusBadRequest, fmt.Sprintf("%s isn't an image", fh.Filename))
			return
		}
		if fh.Size > maxUploadBytes {
			writeErr(c, http.StatusBadRequest, fmt.Sprintf("%s is too large (max 10MB)", fh.Filename))
			return
		}
		saved := core.NewID() + strings.ToLower(filepath.Ext(fh.Filename))
		dst := filepath.Join(s.attachDir, saved)
		if err := saveUpload(fh, dst); err != nil {
			writeErr(c, http.StatusInternalServerError, err.Error())
			return
		}
		abs, err := filepath.Abs(dst)
		if err != nil {
			abs = dst
		}
		out = append(out, uploaded{Name: fh.Filename, Path: abs, URL: "/api/uploads/" + saved})
	}
	writeJSON(c, http.StatusOK, out)
}

// saveUpload copies one multipart file to dst.
func saveUpload(fh *multipart.FileHeader, dst string) error {
	src, err := fh.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, src); err != nil {
		return err
	}
	return nil
}

// serveUpload serves an uploaded image by its saved name for the composer's
// thumbnail preview, guarded by safeImageName so the name can't escape attachDir.
func (s *server) serveUpload(c *gin.Context) {
	name := c.Param("name")
	if s.attachDir == "" || !safeImageName(name) {
		writeErr(c, http.StatusBadRequest, "bad upload name")
		return
	}
	http.ServeFile(c.Writer, c.Request, filepath.Join(s.attachDir, name))
}

func (s *server) events(c *gin.Context) {
	// gin's ResponseWriter is itself an http.Flusher, which is what keeps the SSE
	// stream pushing each event instead of buffering the whole response.
	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	w.Flush()

	ch := s.svc.Subscribe()
	defer s.svc.Unsubscribe(ch)

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(msg)
			_, _ = w.Write([]byte("\n\n"))
			w.Flush()
		case <-time.After(30 * time.Second):
			_, _ = w.Write([]byte(": ping\n\n"))
			w.Flush()
		}
	}
}
