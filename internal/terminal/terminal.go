// Package terminal runs an agent inside a real pseudo-terminal (PTY) and lets
// the board attach to it live: the browser sees the actual CLI output and can
// type into it (including Ctrl-C), exactly like a local terminal. This is the
// interactive counterpart to the headless adapter — we reuse the standard
// building blocks (creack/pty here, xterm.js in the browser) rather than
// reinventing a terminal emulator.
package terminal

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// scrollbackCap bounds the replay buffer a newly-attached client receives, so a
// long-running session doesn't grow memory without bound.
const scrollbackCap = 256 * 1024

// subBuffer is how many output chunks a single attached client may fall behind
// before it's dropped (it can reconnect and replay the scrollback).
const subBuffer = 512

// Session is one PTY-backed agent run for a task. Output is fanned out to every
// attached client and also kept as bounded scrollback for late joiners; input
// (keystrokes) is written straight to the PTY.
type Session struct {
	pty *os.File
	cmd *exec.Cmd

	mu         sync.Mutex
	scrollback []byte
	subs       map[chan []byte]struct{}
	closed     bool
	done       chan struct{}
	// lastActivity is the last time the session produced output or received input.
	// The orchestrator's idle watcher reads it (via IdleFor) to tell an agent that
	// went quiet at its prompt from one still working — a working Claude TUI streams
	// a spinner/output continuously, so silence means the turn has ended. Input
	// counts too, so a just-answered agent isn't judged idle before it can respond.
	lastActivity time.Time
}

// Manager owns the live sessions, keyed by task id.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewManager() *Manager { return &Manager{sessions: make(map[string]*Session)} }

// Start launches cmd attached to a new PTY and registers it under taskID. The
// returned Session's Wait blocks until the process exits. Any prior session for
// the same task is closed first.
func (m *Manager) Start(taskID string, cmd *exec.Cmd) (*Session, error) {
	f, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	s := &Session{pty: f, cmd: cmd, subs: make(map[chan []byte]struct{}), done: make(chan struct{}), lastActivity: time.Now()}

	m.mu.Lock()
	if old := m.sessions[taskID]; old != nil {
		old.Close()
	}
	m.sessions[taskID] = s
	m.mu.Unlock()

	go s.pump()
	go func() { <-s.done; m.remove(taskID, s) }() // drop it from the map when it ends
	return s, nil
}

// Get returns the live session for a task, if any.
func (m *Manager) Get(taskID string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[taskID]
	return s, ok && !s.isClosed()
}

func (m *Manager) remove(taskID string, s *Session) {
	m.mu.Lock()
	if m.sessions[taskID] == s {
		delete(m.sessions, taskID)
	}
	m.mu.Unlock()
}

// CloseAll terminates every live session — called on daemon shutdown so agent
// processes don't leak past the daemon that spawned them.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()
	for _, s := range sessions {
		s.Close()
	}
}

// pump reads PTY output forever, appending to scrollback and fanning it out to
// attached clients until the PTY closes (the process exited).
func (s *Session) pump() {
	b := make([]byte, 4096)
	for {
		n, err := s.pty.Read(b)
		if n > 0 {
			s.broadcast(b[:n])
		}
		if err != nil {
			break
		}
	}
	s.markClosed()
}

func (s *Session) broadcast(p []byte) {
	// Copy: b is reused by the read loop.
	chunk := make([]byte, len(p))
	copy(chunk, p)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastActivity = time.Now()
	s.scrollback = append(s.scrollback, chunk...)
	if len(s.scrollback) > scrollbackCap {
		s.scrollback = s.scrollback[len(s.scrollback)-scrollbackCap:]
	}
	for ch := range s.subs {
		select {
		case ch <- chunk:
		default:
			// Client can't keep up; drop it. It reconnects and replays scrollback.
			delete(s.subs, ch)
			close(ch)
		}
	}
}

// Attach subscribes a client: it returns the current scrollback to replay plus a
// channel of subsequent output. detach must be called when the client leaves.
// The channel is closed when the session ends or the client is dropped.
func (s *Session) Attach() (scrollback []byte, out <-chan []byte, detach func()) {
	ch := make(chan []byte, subBuffer)
	s.mu.Lock()
	sb := make([]byte, len(s.scrollback))
	copy(sb, s.scrollback)
	if s.closed {
		close(ch)
	} else {
		s.subs[ch] = struct{}{}
	}
	s.mu.Unlock()

	return sb, ch, func() {
		s.mu.Lock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
}

// Write sends keystrokes to the PTY (stdin of the agent). It counts as activity:
// a human typing — or an answer being delivered — means the agent isn't idle, and
// gives it room to start responding before the idle watcher could judge it stalled.
func (s *Session) Write(p []byte) error {
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()
	_, err := s.pty.Write(p)
	return err
}

// IdleFor reports how long the session has produced no output and received no
// input. The orchestrator uses it to detect an agent that ended its turn and is
// idling at its prompt without having called finish_task.
func (s *Session) IdleFor() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.lastActivity)
}

// Done is closed when the session ends (process exit or Close), so watchers can
// stop without polling.
func (s *Session) Done() <-chan struct{} { return s.done }

// WriteTo delivers p to the stdin of task id's live session, exactly as if the
// human had typed it into the terminal. It reports whether a live session
// existed: a false return means the agent is no longer running, so the input had
// nowhere to go. This is how AnswerHuman feeds a board reply back to a parked
// interactive agent idling at its prompt.
func (m *Manager) WriteTo(id string, p []byte) (bool, error) {
	s, ok := m.Get(id)
	if !ok {
		return false, nil
	}
	return true, s.Write(p)
}

// Resize updates the PTY window size so the CLI reflows to the browser terminal.
func (s *Session) Resize(rows, cols uint16) error {
	return pty.Setsize(s.pty, &pty.Winsize{Rows: rows, Cols: cols})
}

// Wait blocks until the agent process exits. It deliberately does NOT close the
// session: after the process exits the PTY can still hold unread trailing output
// (e.g. the agent's final lines), so closure is left to pump, which reads to EOF
// first and then marks the session closed. Callers use Wait only to learn the
// exit result.
func (s *Session) Wait() error {
	return s.cmd.Wait()
}

// Close terminates the process and tears the session down.
func (s *Session) Close() {
	if p := s.cmd.Process; p != nil {
		// Agents run detached in their own session/process group (Setsid, via
		// pty.Start), so grandchildren (bash, test runners) don't share our group
		// and would survive a bare Process.Kill of the leader. The leader's PID is
		// the group id, so signalling -pid SIGKILLs the whole group — reaping the
		// grandchildren too. Fall back to a single-PID kill if that fails (e.g. the
		// group is already gone).
		if err := syscall.Kill(-p.Pid, syscall.SIGKILL); err != nil {
			_ = p.Kill()
		}
	}
	s.markClosed()
}

func (s *Session) markClosed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.done)
	_ = s.pty.Close()
	for ch := range s.subs {
		delete(s.subs, ch)
		close(ch)
	}
}

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
