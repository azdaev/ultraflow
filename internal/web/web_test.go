package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"ultraflow/internal/core"
	"ultraflow/internal/model"
	"ultraflow/internal/store"
	"ultraflow/internal/terminal"
)

func newTestServer(t *testing.T) (*httptest.Server, *core.Service) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	svc := core.NewService(st)
	return httptest.NewServer(New(svc, terminal.NewManager(), "", nil, nil)), svc
}

// fakeConc is a stand-in orchestrator that records the limit the settings API
// applies, so the test can assert the live-apply wiring without a real one.
type fakeConc struct{ limit int }

func (f *fakeConc) Limit() int     { return f.limit }
func (f *fakeConc) SetLimit(n int) { f.limit = n }

func newTestServerConc(t *testing.T, conc concurrencyController) (*httptest.Server, *core.Service) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	svc := core.NewService(st)
	return httptest.NewServer(New(svc, terminal.NewManager(), "", nil, conc)), svc
}

// TestSettingsConcurrency drives GET /api/settings and POST
// /api/settings/concurrency: the GET reflects the live limit, a valid POST
// persists and applies it to the orchestrator, and an out-of-range POST is
// rejected without touching the orchestrator.
func TestSettingsConcurrency(t *testing.T) {
	conc := &fakeConc{limit: 3}
	ts, svc := newTestServerConc(t, conc)
	defer ts.Close()

	var got struct {
		MaxConcurrent    int `json:"maxConcurrent"`
		MaxConcurrentMin int `json:"maxConcurrentMin"`
		MaxConcurrentMax int `json:"maxConcurrentMax"`
	}
	getJSON(t, ts.URL+"/api/settings", &got)
	if got.MaxConcurrent != 3 || got.MaxConcurrentMin != 1 || got.MaxConcurrentMax != 8 {
		t.Fatalf("GET settings = %+v; want {3 1 8}", got)
	}

	res, err := http.Post(ts.URL+"/api/settings/concurrency",
		"application/json", bytes.NewBufferString(`{"value":5}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	res.Body.Close()

	if conc.limit != 5 {
		t.Fatalf("orchestrator limit not applied: got %d, want 5", conc.limit)
	}
	if n, ok, _ := svc.GetMaxConcurrent(); !ok || n != 5 {
		t.Fatalf("value not persisted: got %d ok=%v, want 5", n, ok)
	}

	// Out of range → 400, and the orchestrator must be left untouched.
	res, _ = http.Post(ts.URL+"/api/settings/concurrency",
		"application/json", bytes.NewBufferString(`{"value":99}`))
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for out-of-range, got %d", res.StatusCode)
	}
	res.Body.Close()
	if conc.limit != 5 {
		t.Fatalf("rejected value must not change the orchestrator: got %d", conc.limit)
	}
}

func TestCreateAndBoard(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	// A not-yet-implemented agent/flow must be normalized down to what the
	// orchestrator actually runs, so the stored (and later displayed) values never
	// claim a task ran an adapter or multi-step flow it didn't.
	body := `{"title":"do X","body":"b","project":"p","agent":"opencode","flow":"tdd"}`
	res, err := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", res.StatusCode)
	}
	var created model.Task
	json.NewDecoder(res.Body).Decode(&created)
	res.Body.Close()
	if created.Agent != "claude" || created.Flow != "solo" {
		t.Fatalf("unimplemented agent/flow should normalize to claude/solo, got %s/%s", created.Agent, created.Flow)
	}

	var snap struct {
		Tasks    []model.Task         `json:"tasks"`
		Requests []model.HumanRequest `json:"requests"`
		Activity map[string]string    `json:"activity"`
	}
	getJSON(t, ts.URL+"/api/board", &snap)
	if len(snap.Tasks) != 1 || snap.Tasks[0].ID != created.ID {
		t.Fatalf("board missing created task: %+v", snap.Tasks)
	}
}

// TestAddProjectByPath drives the paste-the-path fallback (POST /api/projects):
// a valid git repo path is accepted and named after its folder, while a
// non-existent path and a directory that isn't a git repo are both rejected.
func TestAddProjectByPath(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	// A directory with a .git entry is a valid repo; its basename becomes the name.
	repo := filepath.Join(t.TempDir(), "my-repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	res, err := http.Post(ts.URL+"/api/projects", "application/json",
		bytes.NewBufferString(`{"path":`+strconv.Quote(repo)+`}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 for a git repo path, got %d", res.StatusCode)
	}
	var p model.Project
	json.NewDecoder(res.Body).Decode(&p)
	res.Body.Close()
	if p.Name != "my-repo" || p.RepoPath != repo {
		t.Fatalf("project = %+v; want name=my-repo path=%s", p, repo)
	}

	// A directory that isn't a git repo → 400.
	plain := t.TempDir()
	res, _ = http.Post(ts.URL+"/api/projects", "application/json",
		bytes.NewBufferString(`{"path":`+strconv.Quote(plain)+`}`))
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a non-git dir, got %d", res.StatusCode)
	}
	res.Body.Close()

	// A path that doesn't exist → 400.
	res, _ = http.Post(ts.URL+"/api/projects", "application/json",
		bytes.NewBufferString(`{"path":"/no/such/folder/anywhere"}`))
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a missing path, got %d", res.StatusCode)
	}
	res.Body.Close()
}

func TestCreateRequiresTitle(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	res, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"  "}`))
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for blank title, got %d", res.StatusCode)
	}
}

func TestCreateRequiresProject(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	res, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"do X","project":"  "}`))
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for blank project, got %d", res.StatusCode)
	}
}

// TestAnswerEndpoint drives the HTTP answer path: a posted question shows up on
// the pending endpoint, and answering it returns the task to running.
func TestAnswerEndpoint(t *testing.T) {
	ts, svc := newTestServer(t)
	defer ts.Close()

	task, _ := svc.CreateTask("t", "", "")
	req, err := svc.AskHuman(task.ID, "q?", []string{"a"}, "")
	if err != nil {
		t.Fatalf("ask: %v", err)
	}

	// The request appears on the pending endpoint.
	var reqs []model.HumanRequest
	getJSON(t, ts.URL+"/api/human_requests", &reqs)
	if len(reqs) != 1 || reqs[0].ID != req.ID {
		t.Fatalf("expected the request on /api/human_requests, got %d", len(reqs))
	}

	res, err := http.Post(ts.URL+"/api/human_requests/"+req.ID+"/answer",
		"application/json", bytes.NewBufferString(`{"answer":"chosen"}`))
	if err != nil {
		t.Fatalf("answer post: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	res.Body.Close()

	// Task returns to running and the request leaves the rail.
	if got, _ := svc.GetTask(task.ID); got.Status != model.StatusRunning {
		t.Fatalf("expected running after answer, got %s", got.Status)
	}
	if reqs2, _ := svc.PendingRequests(); len(reqs2) != 0 {
		t.Fatalf("expected no pending after answer, got %d", len(reqs2))
	}

	// Events endpoint should record the exchange.
	var evs []model.Event
	getJSON(t, ts.URL+"/api/tasks/"+task.ID+"/events", &evs)
	if len(evs) == 0 {
		t.Fatal("expected events for the task")
	}
}

// TestReviewEndpoints exercises the review surface's HTTP wiring: shots lists
// empty (no worktree) without erroring, diff 404s when there's nothing to diff,
// and revise reports unavailable when there's no orchestrator behind the server.
func TestReviewEndpoints(t *testing.T) {
	ts, svc := newTestServer(t) // nil conc → no reviser
	defer ts.Close()
	task, _ := svc.CreateTask("t", "", "")

	// shots: empty gallery, HTTP 200 (absent dir is not an error).
	var names []string
	getJSON(t, ts.URL+"/api/tasks/"+task.ID+"/shots", &names)
	if len(names) != 0 {
		t.Fatalf("expected no shots, got %v", names)
	}

	// diff: 404 for a task with no worktree.
	res, err := http.Get(ts.URL + "/api/tasks/" + task.ID + "/diff")
	if err != nil {
		t.Fatalf("diff get: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("diff without a worktree should 404, got %d", res.StatusCode)
	}

	// revise: 503 when the server has no orchestrator (reviser) to run the agent.
	res, err = http.Post(ts.URL+"/api/tasks/"+task.ID+"/revise",
		"application/json", bytes.NewBufferString(`{"message":"redo it"}`))
	if err != nil {
		t.Fatalf("revise post: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("revise without an orchestrator should 503, got %d", res.StatusCode)
	}
}

// TestCancelDeleteArchive covers the everyday task-management routes: remove a
// not-started task, refuse to remove a live one, stop (cancel) a live one, and
// clear closed tasks in bulk.
func TestCancelDeleteArchive(t *testing.T) {
	ts, svc := newTestServer(t)
	defer ts.Close()

	// A backlog task can be removed outright.
	t1, _ := svc.CreateTask("remove me", "", "")
	if code := deleteTask(t, ts.URL, t1.ID); code != http.StatusOK {
		t.Fatalf("delete backlog task: got %d, want 200", code)
	}
	if _, err := svc.GetTask(t1.ID); err == nil {
		t.Fatal("deleted task should be gone from the store")
	}

	// A live task can't be removed — it must be stopped first.
	t2, _ := svc.CreateTask("running one", "", "")
	svc.UpdateStatus(t2.ID, model.StatusRunning)
	if code := deleteTask(t, ts.URL, t2.ID); code != http.StatusConflict {
		t.Fatalf("delete of a running task should 409, got %d", code)
	}

	// Stopping it moves it to cancelled (no live terminal here, so it's a no-op kill).
	res, err := http.Post(ts.URL+"/api/tasks/"+t2.ID+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatalf("cancel post: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("cancel a running task: got %d, want 200", res.StatusCode)
	}
	if got, _ := svc.GetTask(t2.ID); got.Status != model.StatusCancelled {
		t.Fatalf("stopped task status = %s, want cancelled", got.Status)
	}

	// Cancelling a task that isn't live → 409.
	res, _ = http.Post(ts.URL+"/api/tasks/"+t2.ID+"/cancel", "application/json", nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("cancel of a non-live task should 409, got %d", res.StatusCode)
	}
	res.Body.Close()

	// A done task plus the cancelled one are both cleared by archive_closed.
	t3, _ := svc.CreateTask("finished", "", "")
	svc.UpdateStatus(t3.ID, model.StatusDone)
	res, err = http.Post(ts.URL+"/api/archive_closed", "application/json", nil)
	if err != nil {
		t.Fatalf("archive post: %v", err)
	}
	var out struct {
		Removed int `json:"removed"`
	}
	json.NewDecoder(res.Body).Decode(&out)
	res.Body.Close()
	if out.Removed != 2 {
		t.Fatalf("archive_closed removed %d, want 2 (the done + cancelled tasks)", out.Removed)
	}
	if _, err := svc.GetTask(t2.ID); err == nil {
		t.Fatal("archived cancelled task should be gone")
	}
	if _, err := svc.GetTask(t3.ID); err == nil {
		t.Fatal("archived done task should be gone")
	}
}

// deleteTask issues DELETE /api/tasks/{id} and returns the status code.
func deleteTask(t *testing.T, base, id string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, base+"/api/tasks/"+id, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete %s: %v", id, err)
	}
	res.Body.Close()
	return res.StatusCode
}

func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("get %s: status %d", url, res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}
