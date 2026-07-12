package terminal

import (
	"os/exec"
	"strings"
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
