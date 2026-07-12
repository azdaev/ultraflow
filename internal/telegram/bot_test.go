package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ultraflow/internal/model"
)

type fakeService struct {
	tasks   []model.Task
	reqs    []model.HumanRequest
	answers []string
}

func (f *fakeService) ListTasks() ([]model.Task, error) { return f.tasks, nil }
func (f *fakeService) GetTask(id string) (model.Task, error) {
	for _, t := range f.tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return model.Task{}, nil
}
func (f *fakeService) PendingRequests() ([]model.HumanRequest, error) { return f.reqs, nil }
func (f *fakeService) AnswerHuman(id, answer string) error {
	f.answers = append(f.answers, id+":"+answer)
	f.reqs = nil
	return nil
}

func TestOwnerCallbackAnswersOnce(t *testing.T) {
	var methods []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer ts.Close()
	f := &fakeService{
		tasks: []model.Task{{ID: "task", Title: "Choose safely"}},
		reqs:  []model.HumanRequest{{ID: "req", TaskID: "task", Question: "Ship?", Options: []string{"Yes"}, Status: "pending"}},
	}
	b := New(Config{Token: "secret", UserID: 7, ChatID: 9, APIURL: ts.URL}, f)
	b.notifyRequest(context.Background(), f.reqs[0])
	var token string
	for k := range b.actions {
		token = k
	}
	u := update{Callback: &callback{ID: "cb", Data: token, From: user{ID: 7}, Message: &message{Chat: chat{ID: 9, Type: "private"}}}}
	b.handle(context.Background(), u)
	b.handle(context.Background(), u)
	if got := strings.Join(f.answers, ","); got != "req:Yes" {
		t.Fatalf("answers = %q", got)
	}
	if len(methods) != 4 {
		t.Fatalf("API calls = %v; want send + answer + edit + stale acknowledgement", methods)
	}
}

func TestUnauthorizedUpdateIsIgnored(t *testing.T) {
	f := &fakeService{reqs: []model.HumanRequest{{ID: "req"}}}
	b := New(Config{UserID: 7, ChatID: 9}, f)
	b.actions["token"] = action{RequestID: "req", Answer: "Yes", Expires: time.Now().Add(time.Hour)}
	b.handle(context.Background(), update{Callback: &callback{ID: "cb", Data: "token", From: user{ID: 8}, Message: &message{Chat: chat{ID: 9, Type: "private"}}}})
	if len(f.answers) != 0 {
		t.Fatalf("unauthorized callback applied: %v", f.answers)
	}
	if _, ok := b.actions["token"]; !ok {
		t.Fatal("unauthorized callback consumed token")
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := map[string]string{"ULTRAFLOW_TELEGRAM_BOT_TOKEN": "token", "ULTRAFLOW_TELEGRAM_USER_ID": "12", "ULTRAFLOW_TELEGRAM_CHAT_ID": "34"}
	cfg, ok, err := ConfigFromEnv(func(k string) string { return env[k] })
	if err != nil || !ok || cfg.UserID != 12 || cfg.ChatID != 34 {
		t.Fatalf("config = %+v,%v,%v", cfg, ok, err)
	}
	delete(env, "ULTRAFLOW_TELEGRAM_USER_ID")
	if _, _, err := ConfigFromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatal("missing user id accepted")
	}
}

func TestCommandResponseDoesNotLeakTaskBody(t *testing.T) {
	var payload map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer ts.Close()
	f := &fakeService{tasks: []model.Task{{Title: "Public title", Body: "SECRET BODY", Status: model.StatusRunning}}}
	b := New(Config{Token: "x", UserID: 1, ChatID: 2, APIURL: ts.URL}, f)
	b.handle(context.Background(), update{Message: &message{Text: "/tasks", From: user{ID: 1}, Chat: chat{ID: 2, Type: "private"}}})
	text, _ := payload["text"].(string)
	if strings.Contains(text, "SECRET") || !strings.Contains(text, "Public title") {
		t.Fatalf("unexpected response: %q", text)
	}
}
