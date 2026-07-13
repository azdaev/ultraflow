// Package port hands out distinct free TCP ports for per-task dev servers, so two
// worktrees running in parallel never collide on the same port. Binding :0 only
// promises a port that is free *right now* — not one we haven't already handed to
// another task that hasn't bound it yet — so the allocator also tracks what it has
// given out and skips those.
package port

import (
	"fmt"
	"net"
	"sync"
)

// Allocator tracks the ports currently reserved by live tasks.
type Allocator struct {
	mu   sync.Mutex
	used map[int]bool
}

func NewAllocator() *Allocator { return &Allocator{used: map[int]bool{}} }

// Allocate returns a free TCP port not already reserved by another live task. It
// asks the OS for a free ephemeral port (bind :0), retrying if that port is one
// we've already handed out.
func (a *Allocator) Allocate() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for attempt := 0; attempt < 100; attempt++ {
		p, err := freePort()
		if err != nil {
			return 0, err
		}
		if !a.used[p] {
			a.used[p] = true
			return p, nil
		}
	}
	return 0, fmt.Errorf("couldn't find a free port after 100 tries")
}

// Reserve marks a specific port as in use — used to re-register a port persisted
// on a task across a daemon restart, so a fresh allocation can't reuse it while
// that task's dev server is still up. Best-effort; a 0/negative port is ignored.
func (a *Allocator) Reserve(p int) {
	if p <= 0 {
		return
	}
	a.mu.Lock()
	a.used[p] = true
	a.mu.Unlock()
}

// Release returns a port to the pool when a task is torn down or merged.
func (a *Allocator) Release(p int) {
	if p <= 0 {
		return
	}
	a.mu.Lock()
	delete(a.used, p)
	a.mu.Unlock()
}

// EnvVars returns the environment entries that publish a reserved dev-server port
// to a task's agent and any dev server it starts: PORT (the conventional name most
// frameworks read) and ULTRAFLOW_PORT (our explicit name, in case a framework
// ignores PORT). The orchestrator injects these into the agent's env and the
// dev-server launcher into the hook's env — so the var-name contract lives here,
// in one place, instead of being re-spelled at each call site.
func EnvVars(p int) []string {
	return []string{
		fmt.Sprintf("PORT=%d", p),
		fmt.Sprintf("ULTRAFLOW_PORT=%d", p),
	}
}

// freePort asks the OS for an unused TCP port by binding :0 and reading back the
// assigned port, then releasing it.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
