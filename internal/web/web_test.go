package web

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
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
	return httptest.NewServer(New(svc, terminal.NewManager(), "", "", nil, nil)), svc
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
	return httptest.NewServer(New(svc, terminal.NewManager(), "", "", nil, conc)), svc
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

// TestFeedback drives POST /api/feedback: a valid note is accepted, and an
// empty one is rejected without landing a row.
func TestFeedback(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	res, err := http.Post(ts.URL+"/api/feedback", "application/json",
		bytes.NewBufferString(`{"text":"love this","path":"/board"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	res.Body.Close()

	res, err = http.Post(ts.URL+"/api/feedback", "application/json",
		bytes.NewBufferString(`{"text":"  ","path":"/board"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty text, got %d", res.StatusCode)
	}
	res.Body.Close()
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

// tinyPNG is a valid 1x1 PNG, the smallest real image to post at the upload API.
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

// postUpload posts one named file under the multipart `files` field to the given
// server and returns the response.
func postUpload(t *testing.T, url, filename string, data []byte) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("files", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	w.Close()
	res, err := http.Post(url+"/api/uploads", w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("post upload: %v", err)
	}
	return res
}

// TestUploadImages drives POST /api/uploads: a real PNG is saved to attachDir and
// echoed back with an absolute on-disk path plus a preview URL that serves the
// file, while a non-image is rejected without writing anything.
func TestUploadImages(t *testing.T) {
	attachDir := filepath.Join(t.TempDir(), "attachments")
	st, err := store.Open(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	svc := core.NewService(st)
	ts := httptest.NewServer(New(svc, terminal.NewManager(), "", attachDir, nil, nil))
	defer ts.Close()

	res := postUpload(t, ts.URL, "shot.png", tinyPNG)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("upload png: status %d", res.StatusCode)
	}
	var got []struct {
		Name string `json:"name"`
		Path string `json:"path"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	res.Body.Close()
	if len(got) != 1 {
		t.Fatalf("want 1 attachment, got %d", len(got))
	}
	if got[0].Name != "shot.png" {
		t.Fatalf("name = %q, want shot.png", got[0].Name)
	}
	if !filepath.IsAbs(got[0].Path) {
		t.Fatalf("path %q is not absolute", got[0].Path)
	}
	if data, err := os.ReadFile(got[0].Path); err != nil {
		t.Fatalf("saved file not written: %v", err)
	} else if !bytes.Equal(data, tinyPNG) {
		t.Fatalf("saved file contents differ from upload")
	}
	// The preview URL serves the same bytes back.
	pres, err := http.Get(ts.URL + got[0].URL)
	if err != nil {
		t.Fatalf("get preview: %v", err)
	}
	body, _ := io.ReadAll(pres.Body)
	pres.Body.Close()
	if pres.StatusCode != http.StatusOK || !bytes.Equal(body, tinyPNG) {
		t.Fatalf("preview status %d / bytes match=%v", pres.StatusCode, bytes.Equal(body, tinyPNG))
	}

	// A non-image is rejected.
	bad := postUpload(t, ts.URL, "notes.txt", []byte("hello"))
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-image upload: status %d, want 400", bad.StatusCode)
	}
	bad.Body.Close()
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
