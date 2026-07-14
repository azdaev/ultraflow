package devserver

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestHookServesThenStops is the end-to-end M1 check for the dev-server path: the
// per-project hook is booted on the reserved PORT, actually serves on it, and is
// killed (freeing the port) on Stop — even though it runs detached from any agent.
func TestHookServesThenStops(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	repo := t.TempDir()
	hookDir := filepath.Join(repo, ".ultraflow")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A hook that binds $PORT with a trivial HTTP server — the canonical "dev
	// server reads PORT from the env" contract.
	hook := filepath.Join(hookDir, HookName)
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexec python3 -m http.server \"$PORT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	path, ok := HookPath(repo)
	if !ok || path != hook {
		t.Fatalf("HookPath(%s) = %q,%v; want %q,true", repo, path, ok, hook)
	}

	port := freeTestPort(t)
	m := NewManager()
	if err := m.Start("task1", repo, hook, port); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.StopAll()

	if !reachable(port, 3*time.Second) {
		t.Fatalf("dev server never came up on port %d", port)
	}

	m.Stop("task1")
	if !unreachable(port, 3*time.Second) {
		t.Fatalf("dev server still serving on %d after Stop", port)
	}
}

// TestCommandServesThenStops covers the zero-configuration path used by the MCP
// tool: Ultraflow detaches the agent's ordinary dev command and supplies PORT.
func TestCommandServesThenStops(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	port := freeTestPort(t)
	m := NewManager()
	if err := m.StartCommand("task-command", t.TempDir(), `exec python3 -m http.server "$PORT"`, port); err != nil {
		t.Fatalf("start command: %v", err)
	}
	defer m.StopAll()
	if !reachable(port, 3*time.Second) {
		t.Fatalf("command dev server never came up on port %d", port)
	}
	m.Stop("task-command")
	if !unreachable(port, 3*time.Second) {
		t.Fatalf("command dev server still serving on %d after Stop", port)
	}
}

// unreachable polls until a TCP connect to the port is refused (the server is
// gone), tolerating the brief window between SIGKILL and the socket closing.
func unreachable(port int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 200*time.Millisecond)
		if err != nil {
			return true
		}
		c.Close()
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func freeTestPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// reachable polls a TCP connect to the port until it succeeds or the deadline
// passes.
func reachable(port int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 200*time.Millisecond)
		if err == nil {
			c.Close()
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
