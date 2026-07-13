package core

import (
	"time"

	"ultraflow/internal/model"
)

// eventBus is the live pub/sub the Service fans task changes out through (the SSE
// broker). Declared here so core owns the contract; internal/broker satisfies it.
type eventBus interface {
	Publish(msg []byte)
	Subscribe() chan []byte
	Unsubscribe(ch chan []byte)
}

// Repo is the persistence contract the Service depends on: exactly the store
// methods the business logic calls, no more. Declaring it here (consumer side)
// instead of importing *store.Store inverts the dependency — core owns the
// contract, the store package satisfies it, and tests can substitute a fake.
// *store.Store is the production implementation, injected in main.
type Repo interface {
	// Tasks
	CreateTask(t model.Task) error
	ListTasks() ([]model.Task, error)
	BacklogTasks() ([]model.Task, error)
	GetTask(id string) (model.Task, error)
	DeleteTask(id string) error
	UpdateTaskStatus(id string, st model.TaskStatus) (time.Time, error)
	SwapStatusFrom(id string, from []model.TaskStatus, to model.TaskStatus) (bool, error)
	UpdateTaskTitleBody(id, title, body string) (time.Time, error)
	SetWorktree(id, wt string) error
	SetTaskAttempt(id string, attempt int) (time.Time, error)
	SetPort(id string, port int) error
	SetResume(id string, v bool) error
	RecoverInFlight() (int64, error)

	// Multi-step flow runs
	CreateRun(taskID, flow, cursor string) error
	GetRun(taskID string) (model.Run, bool, error)
	AdvanceRun(taskID, completedStep, next string) error
	SetRunCursor(taskID, cursor string) error
	SetRunPhase(taskID string, phase model.RunPhase) error
	SetRunTurnDone(taskID string, done bool) (bool, error)
	RunsForTasks(ids []string) (map[string]model.Run, error)

	// Projects
	CreateProject(p model.Project) error
	ListProjects() ([]model.Project, error)
	ProjectByName(name string) (model.Project, error)
	ProjectCount() (int, error)
	DeleteProject(id string) error

	// Human requests (the ask_human protocol)
	CreateHumanRequest(r model.HumanRequest) error
	GetHumanRequest(id string) (model.HumanRequest, error)
	AnswerHumanRequest(id, answer string) (bool, error)
	CancelTaskRequests(taskID string) (int64, error)
	PendingHumanRequests() ([]model.HumanRequest, error)

	// Events + activity
	AppendEvent(e model.Event) (int64, error)
	TaskEvents(taskID string) ([]model.Event, error)
	LatestActivity() (map[string]model.ActivityLine, error)

	// Settings
	GetSetting(key string) (value string, ok bool, err error)
	SetSetting(key, value string) error
}
