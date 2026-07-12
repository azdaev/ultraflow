// Package devserver runs a task's dev server as a background process detached
// from the agent, so it stays up after the agent finishes and the human can open
// the task's live app from the Review card.
//
// Why detached: the agent runs in a PTY whose whole process group is SIGKILLed
// when finish_task ends the session (see terminal.Session.Close). A dev server
// the agent started as a child would die with it. Instead Ultraflow launches the
// project's dev-server hook itself, in its own session (Setsid), and reaps it only
// on merge/teardown — so the same port keeps serving through review.
package devserver

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
)

// HookName is the per-project script Ultraflow runs to boot a task's dev server.
// It lives at <repo>/.ultraflow/dev.sh (read directly by the daemon, so it works
// even when a project gitignores .ultraflow/). It is run with cwd set to the
// task's worktree and PORT / ULTRAFLOW_PORT in the environment, so a project can
// encapsulate "how do I start my dev server on $PORT" in one place.
const HookName = "dev.sh"

// HookPath returns the dev-server hook path for a project repo, and whether it
// exists. repoPath is the project's registered repo (not the worktree), so the
// hook is found regardless of gitignore.
func HookPath(repoPath string) (string, bool) {
	p := filepath.Join(repoPath, ".ultraflow", HookName)
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p, true
	}
	return "", false
}

// Manager owns the running dev-server processes, keyed by task id.
type Manager struct {
	mu   sync.Mutex
	proc map[string]*exec.Cmd
}

func NewManager() *Manager { return &Manager{proc: make(map[string]*exec.Cmd)} }

// Start launches hookPath (via sh) for taskID with cwd=dir and the given port
// exported as PORT and ULTRAFLOW_PORT. Output is appended to <dir>/.ultraflow/
// devserver.log so a failing hook can be diagnosed. The process runs in its own
// session (Setsid) so it survives the agent's PTY teardown; any prior dev server
// for the task is stopped first. A no-op-safe error is returned if it can't spawn.
func (m *Manager) Start(taskID, dir, hookPath string, port int) error {
	m.Stop(taskID) // replace any prior server for this task

	cmd := exec.Command("sh", hookPath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", port),
		fmt.Sprintf("ULTRAFLOW_PORT=%d", port),
	)
	// Own session/process group, so ending the agent's PTY session doesn't take the
	// dev server with it, and Stop can kill the whole tree via the group id.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	_ = os.MkdirAll(filepath.Join(dir, ".ultraflow"), 0o755) // for the log below
	if logf, err := os.Create(filepath.Join(dir, ".ultraflow", "devserver.log")); err == nil {
		cmd.Stdout = logf
		cmd.Stderr = logf
		defer logf.Close()
	}

	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the process when it exits on its own, so a hook that crashes doesn't
	// linger as a zombie and the map entry is a best-effort liveness hint.
	go cmd.Wait()

	m.mu.Lock()
	m.proc[taskID] = cmd
	m.mu.Unlock()
	return nil
}

// Stop kills a task's dev server (its whole process group) if one is running.
// Safe to call when none is.
func (m *Manager) Stop(taskID string) {
	m.mu.Lock()
	cmd := m.proc[taskID]
	delete(m.proc, taskID)
	m.mu.Unlock()
	kill(cmd)
}

// StopAll kills every running dev server — called on daemon shutdown so servers
// don't leak past the daemon that spawned them.
func (m *Manager) StopAll() {
	m.mu.Lock()
	cmds := make([]*exec.Cmd, 0, len(m.proc))
	for _, c := range m.proc {
		cmds = append(cmds, c)
	}
	m.proc = make(map[string]*exec.Cmd)
	m.mu.Unlock()
	for _, c := range cmds {
		kill(c)
	}
}

// kill SIGKILLs a command's whole process group (the negative pid), falling back
// to the single process if the group signal fails.
func kill(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
