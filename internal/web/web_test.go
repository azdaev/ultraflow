package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"ultraflow/internal/core"
	"ultraflow/internal/model"
	"ultraflow/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *core.Service) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	svc := core.NewService(st)
	return httptest.NewServer(New(svc, "", nil)), svc
}

func TestCreateAndBoard(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	// A not-yet-implemented agent/flow must be normalized down to what the
	// orchestrator actually runs, so the stored (and later displayed) values never
	// claim a task ran an adapter or multi-step flow it didn't.
	body := `{"title":"do X","body":"b","project":"p","agent":"codex","flow":"tdd"}`
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

func TestCreateRequiresTitle(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	res, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"  "}`))
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for blank title, got %d", res.StatusCode)
	}
}

// TestAnswerEndpoint drives the HTTP answer path against a blocked AskHuman.
func TestAnswerEndpoint(t *testing.T) {
	ts, svc := newTestServer(t)
	defer ts.Close()

	task, _ := svc.CreateTask("t", "", "")
	answered := make(chan string, 1)
	go func() {
		ans, _ := svc.AskHuman(context.Background(), task.ID, "q?", []string{"a"}, "")
		answered <- ans
	}()

	// Poll the pending endpoint until the request shows up.
	var reqID string
	for i := 0; i < 200 && reqID == ""; i++ {
		var reqs []model.HumanRequest
		getJSON(t, ts.URL+"/api/human_requests", &reqs)
		if len(reqs) == 1 {
			reqID = reqs[0].ID
		} else {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if reqID == "" {
		t.Fatal("request never appeared on /api/human_requests")
	}

	res, err := http.Post(ts.URL+"/api/human_requests/"+reqID+"/answer",
		"application/json", bytes.NewBufferString(`{"answer":"chosen"}`))
	if err != nil {
		t.Fatalf("answer post: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	res.Body.Close()

	select {
	case ans := <-answered:
		if ans != "chosen" {
			t.Fatalf("expected 'chosen', got %q", ans)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AskHuman did not return after HTTP answer")
	}

	// Events endpoint should record the exchange.
	var evs []model.Event
	getJSON(t, ts.URL+"/api/tasks/"+task.ID+"/events", &evs)
	if len(evs) == 0 {
		t.Fatal("expected events for the task")
	}
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
