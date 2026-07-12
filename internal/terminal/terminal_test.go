package terminal

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSessionStreamsAndCloses: output reaches an attached client and the session
// is removed from the manager once the process exits.
func TestSessionStreamsAndCloses(t *testing.T) {
	m := NewManager()
	sess, err := m.Start("t1", exec.Command("bash", "-c", "echo hello-terminal"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	sb, out, detach := sess.Attach()
	defer detach()

	got := string(sb)
	done := make(chan struct{})
	go func() {
		for c := range out {
			got += string(c)
		}
		close(done)
	}()

	if err := sess.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	<-done // the channel closes when the session ends

	if !strings.Contains(got, "hello-terminal") {
		t.Fatalf("expected agent output, got %q", got)
	}
	if _, ok := m.Get("t1"); ok {
		t.Fatal("session should be gone from the manager after it ends")
	}
}

// TestCloseReapsGrandchildren: Close kills the whole process group, not just the
// leader — a grandchild the agent detached (bash, a test runner) must not survive
// Close. Guards against the leak that a bare Process.Kill(leaderPID) leaves behind.
func TestCloseReapsGrandchildren(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	// The bash leader spawns a long-lived grandchild, records its PID, then waits.
	script := "sleep 300 & echo $! > " + pidFile + "; wait"

	m := NewManager()
	sess, err := m.Start("kg", exec.Command("bash", "-c", script))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	var gpid int
	for i := 0; i < 200; i++ {
		if s := strings.TrimSpace(readFile(pidFile)); s != "" {
			gpid, _ = strconv.Atoi(s)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if gpid == 0 {
		t.Fatal("grandchild never recorded its pid")
	}
	if err := syscall.Kill(gpid, 0); err != nil {
		t.Fatalf("grandchild %d not alive before Close: %v", gpid, err)
	}

	sess.Close()

	alive := true
	for i := 0; i < 200; i++ {
		if err := syscall.Kill(gpid, 0); err != nil {
			alive = false
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if alive {
		t.Fatalf("grandchild %d survived Close — process group was not killed", gpid)
	}
}

func readFile(p string) string { b, _ := os.ReadFile(p); return string(b) }

// TestSessionInput: keystrokes written to the session reach the process (a PTY
// echoes them), proving the terminal is interactive, not read-only.
func TestSessionInput(t *testing.T) {
	m := NewManager()
	sess, err := m.Start("in", exec.Command("cat"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer sess.Close()

	_, out, detach := sess.Attach()
	defer detach()

	if err := sess.Write([]byte("ping\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	deadline := time.After(3 * time.Second)
	got := ""
	for {
		select {
		case c, ok := <-out:
			if !ok {
				t.Fatalf("channel closed before echo, got %q", got)
			}
			got += string(c)
			if strings.Contains(got, "ping") {
				return // input made it through
			}
		case <-deadline:
			t.Fatalf("input never echoed back, got %q", got)
		}
	}
}
