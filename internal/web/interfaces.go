package web

import (
	"ultraflow/internal/core"
	"ultraflow/internal/model"
	"ultraflow/internal/worktree"
)

// Service is the business-logic contract the HTTP layer depends on: exactly the
// core.Service methods the handlers call. Declaring it here (consumer side)
// instead of holding *core.Service inverts the dependency — the transport owns
// what it needs and *core.Service satisfies it, so handler tests can substitute a
// fake. *core.Service is the production implementation, injected in main.
type Service interface {
	// Board snapshot + live SSE stream
	ListTasks() ([]model.Task, error)
	PendingRequests() ([]model.HumanRequest, error)
	LatestActivity() (text, kind map[string]string, err error)
	RunsProgress(tasks []model.Task) map[string]model.RunProgress
	ContextTokens() map[string]int
	Models() map[string]string
	Subscribe() chan []byte
	Unsubscribe(ch chan []byte)

	// Tasks
	CreateTaskFull(title, body, project, agent, flowKey string) (model.Task, error)
	GetTask(id string) (model.Task, error)
	RetryTask(id string) error
	CancelTask(id string) (bool, error)
	DeleteTask(id string) error
	ArchiveClosed() (int, error)
	MergeTask(id string) error
	FinishReview(id string) error
	TaskDiff(id string) (worktree.DiffResult, error)
	TaskEvents(taskID string) ([]model.Event, error)
	ShotsDir(taskID string) (string, error)
	TaskShots(taskID string) []string
	AnswerHuman(reqID, answer string) error

	// Projects
	ListProjects() ([]model.Project, error)
	CreateProject(name, repoPath string) (model.Project, error)
	DeleteProject(id string) error

	// Settings
	GetMaxConcurrent() (n int, ok bool, err error)
	SetMaxConcurrent(n int) (int, error)
	ContextCap() int
	SetContextCap(n int) (int, error)
	TelegramSettings() (core.TelegramSettings, bool, error)
	SetTelegramSettings(cfg core.TelegramSettings) error
}
