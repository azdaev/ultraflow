// Package telegram exposes a deliberately small, owner-only mobile surface for
// Ultraflow. It uses Bot API long polling, so the daemon never accepts an inbound
// internet connection.
package telegram

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"ultraflow/internal/core"
	"ultraflow/internal/model"
)

const callbackTTL = 24 * time.Hour

type service interface {
	ListTasks() ([]model.Task, error)
	GetTask(string) (model.Task, error)
	PendingRequests() ([]model.HumanRequest, error)
	AnswerHuman(string, string) error
}

// Config is intentionally environment-friendly: secrets never touch SQLite or
// the browser. UserID and ChatID are numeric allowlists, not mutable usernames.
type Config struct {
	Token  string
	UserID int64
	ChatID int64
	APIURL string // test override; empty uses Telegram's public Bot API
}

type Bot struct {
	cfg  Config
	svc  service
	http *http.Client

	mu       sync.Mutex
	actions  map[string]action
	notified map[string]bool
}

// Manager owns the optional bot and can replace it live when Settings changes.
type Manager struct {
	ctx    context.Context
	svc    service
	broker *core.Broker
	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewManager(ctx context.Context, svc service, broker *core.Broker) *Manager {
	return &Manager{ctx: ctx, svc: svc, broker: broker}
}

func (m *Manager) ApplyTelegram(s core.TelegramSettings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if !s.Enabled {
		log.Printf("telegram bot disabled")
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	log.Printf("telegram bot enabled for user %d, private chat %d", s.UserID, s.ChatID)
	go New(Config{Token: s.Token, UserID: s.UserID, ChatID: s.ChatID}, m.svc).Run(ctx, m.broker)
}

type action struct {
	RequestID string
	Answer    string
	Expires   time.Time
}

func New(cfg Config, svc service) *Bot {
	if cfg.APIURL == "" {
		cfg.APIURL = "https://api.telegram.org"
	}
	return &Bot{cfg: cfg, svc: svc, http: &http.Client{Timeout: 40 * time.Second}, actions: map[string]action{}, notified: map[string]bool{}}
}

// Run polls until ctx is cancelled. Telegram failures back off and never affect
// the orchestrator; this adapter is an optional notification surface.
func (b *Bot) Run(ctx context.Context, broker *core.Broker) {
	updates := broker.Subscribe()
	defer broker.Unsubscribe(updates)
	if reqs, err := b.svc.PendingRequests(); err == nil {
		for _, req := range reqs {
			b.notifyRequest(ctx, req)
		}
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-updates:
				if !ok {
					return
				}
				var envelope struct {
					Kind string          `json:"kind"`
					Data json.RawMessage `json:"data"`
				}
				if json.Unmarshal(msg, &envelope) == nil && envelope.Kind == "human_request" {
					var req model.HumanRequest
					if json.Unmarshal(envelope.Data, &req) == nil {
						b.notifyRequest(ctx, req)
					}
				}
			}
		}
	}()

	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		var result []update
		err := b.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 30, "allowed_updates": []string{"message", "callback_query"}}, &result)
		if err != nil {
			log.Printf("telegram: getUpdates: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		for _, u := range result {
			if u.ID >= offset {
				offset = u.ID + 1
			}
			b.handle(ctx, u)
		}
	}
}

type update struct {
	ID       int64     `json:"update_id"`
	Message  *message  `json:"message"`
	Callback *callback `json:"callback_query"`
}
type message struct {
	ID   int64  `json:"message_id"`
	Text string `json:"text"`
	From user   `json:"from"`
	Chat chat   `json:"chat"`
}
type callback struct {
	ID, Data string
	From     user     `json:"from"`
	Message  *message `json:"message"`
}
type user struct {
	ID int64 `json:"id"`
}
type chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

func (b *Bot) authorized(u user, c chat) bool {
	return u.ID == b.cfg.UserID && c.ID == b.cfg.ChatID && c.Type == "private"
}

func (b *Bot) handle(ctx context.Context, u update) {
	if u.Message != nil {
		if !b.authorized(u.Message.From, u.Message.Chat) {
			return
		}
		fields := strings.Fields(u.Message.Text)
		if len(fields) == 0 {
			return
		}
		switch fields[0] {
		case "/start", "/status":
			b.sendStatus(ctx)
		case "/tasks":
			b.sendTasks(ctx)
		}
		return
	}
	if u.Callback == nil || u.Callback.Message == nil || !b.authorized(u.Callback.From, u.Callback.Message.Chat) {
		return
	}
	b.mu.Lock()
	a, ok := b.actions[u.Callback.Data]
	if ok {
		delete(b.actions, u.Callback.Data)
	}
	b.mu.Unlock()
	if !ok || time.Now().After(a.Expires) {
		b.answerCallback(ctx, u.Callback.ID, "This button has expired or was already used.", true)
		return
	}
	reqStillPending := false
	if reqs, err := b.svc.PendingRequests(); err == nil {
		for _, req := range reqs {
			if req.ID == a.RequestID {
				reqStillPending = true
				break
			}
		}
	}
	if !reqStillPending {
		b.answerCallback(ctx, u.Callback.ID, "This question is no longer pending.", true)
		return
	}
	if err := b.svc.AnswerHuman(a.RequestID, a.Answer); err != nil {
		log.Printf("telegram: answer checkpoint: %v", err)
		b.answerCallback(ctx, u.Callback.ID, "Could not apply that answer.", true)
		return
	}
	b.answerCallback(ctx, u.Callback.ID, "Answered: "+a.Answer, false)
	// Remove every button from the original notification. Other capability tokens
	// are still rejected by the pending-state check, but the phone should visibly
	// show that the checkpoint is closed.
	_ = b.call(ctx, "editMessageReplyMarkup", map[string]any{
		"chat_id":      b.cfg.ChatID,
		"message_id":   u.Callback.Message.ID,
		"reply_markup": map[string]any{"inline_keyboard": []any{}},
	}, nil)
}

func (b *Bot) notifyRequest(ctx context.Context, req model.HumanRequest) {
	b.mu.Lock()
	if b.notified[req.ID] {
		b.mu.Unlock()
		return
	}
	b.notified[req.ID] = true
	b.mu.Unlock()
	t, err := b.svc.GetTask(req.TaskID)
	if err != nil {
		return
	}
	text := "Ultraflow needs your answer\n\nTask: " + clean(t.Title, 160) + "\nQuestion: " + clean(req.Question, 600)
	rows := make([][]map[string]string, 0, len(req.Options))
	for _, option := range req.Options {
		token := randomToken()
		b.mu.Lock()
		b.actions[token] = action{RequestID: req.ID, Answer: option, Expires: time.Now().Add(callbackTTL)}
		b.mu.Unlock()
		rows = append(rows, []map[string]string{{"text": clean(option, 48), "callback_data": token}})
	}
	payload := map[string]any{"chat_id": b.cfg.ChatID, "text": text}
	if len(rows) > 0 {
		payload["reply_markup"] = map[string]any{"inline_keyboard": rows}
	}
	if err := b.call(ctx, "sendMessage", payload, nil); err != nil {
		log.Printf("telegram: notify: %v", err)
	}
}

func (b *Bot) sendStatus(ctx context.Context) {
	tasks, err := b.svc.ListTasks()
	if err != nil {
		return
	}
	counts := map[model.TaskStatus]int{}
	for _, t := range tasks {
		counts[t.Status]++
	}
	text := fmt.Sprintf("Ultraflow status\n\n%d running · %d need you · %d in review · %d queued", counts[model.StatusRunning], counts[model.StatusNeedsHuman], counts[model.StatusReview], counts[model.StatusQueued]+counts[model.StatusBacklog])
	_ = b.call(ctx, "sendMessage", map[string]any{"chat_id": b.cfg.ChatID, "text": text}, nil)
}

func (b *Bot) sendTasks(ctx context.Context) {
	tasks, err := b.svc.ListTasks()
	if err != nil {
		return
	}
	lines := []string{"Ultraflow tasks"}
	for i, t := range tasks {
		if i == 15 {
			lines = append(lines, "…")
			break
		}
		lines = append(lines, fmt.Sprintf("• %s — %s", clean(t.Title, 100), t.Status))
	}
	if len(tasks) == 0 {
		lines = append(lines, "No tasks yet.")
	}
	_ = b.call(ctx, "sendMessage", map[string]any{"chat_id": b.cfg.ChatID, "text": strings.Join(lines, "\n")}, nil)
}

func (b *Bot) answerCallback(ctx context.Context, id, text string, alert bool) {
	_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text, "show_alert": alert}, nil)
}

func (b *Bot) call(ctx context.Context, method string, payload any, out any) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(b.cfg.APIURL, "/")+"/bot"+b.cfg.Token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return fmt.Errorf("Bot API %d: %s", resp.StatusCode, envelope.Description)
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

func randomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func clean(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > max {
		s = string([]rune(s)[:max-1]) + "…"
	}
	return s
}

// ConfigFromEnv validates the opt-in environment configuration.
func ConfigFromEnv(getenv func(string) string) (Config, bool, error) {
	token := strings.TrimSpace(getenv("ULTRAFLOW_TELEGRAM_BOT_TOKEN"))
	if token == "" {
		return Config{}, false, nil
	}
	uid, err := strconv.ParseInt(strings.TrimSpace(getenv("ULTRAFLOW_TELEGRAM_USER_ID")), 10, 64)
	if err != nil || uid == 0 {
		return Config{}, false, fmt.Errorf("ULTRAFLOW_TELEGRAM_USER_ID must be a numeric Telegram user ID")
	}
	cid, err := strconv.ParseInt(strings.TrimSpace(getenv("ULTRAFLOW_TELEGRAM_CHAT_ID")), 10, 64)
	if err != nil || cid == 0 {
		return Config{}, false, fmt.Errorf("ULTRAFLOW_TELEGRAM_CHAT_ID must be a numeric private chat ID")
	}
	return Config{Token: token, UserID: uid, ChatID: cid}, true, nil
}
