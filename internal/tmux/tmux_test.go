package tmux

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func tmuxAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
}

func TestSessionName(t *testing.T) {
	got := SessionName("myrepo", "Sprocket")
	want := "pp-myrepo-Sprocket"
	if got != want {
		t.Errorf("SessionName = %q, want %q", got, want)
	}
}

func TestCreateKillSession(t *testing.T) {
	tmuxAvailable(t)

	name := "pp-test-rascal-create"
	t.Cleanup(func() { KillSession(name) })

	if err := CreateSession(name, t.TempDir(), nil); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if !SessionExists(name) {
		t.Fatal("session should exist after creation")
	}

	if err := KillSession(name); err != nil {
		t.Fatalf("KillSession: %v", err)
	}

	if SessionExists(name) {
		t.Fatal("session should not exist after kill")
	}
}

func TestCreateSessionWithEnv(t *testing.T) {
	tmuxAvailable(t)

	name := "pp-test-rascal-env"
	t.Cleanup(func() { KillSession(name) })

	env := map[string]string{
		"TEST_VAR_A": "hello",
		"TEST_VAR_B": "world",
	}
	if err := CreateSession(name, t.TempDir(), env); err != nil {
		t.Fatalf("CreateSession with env: %v", err)
	}

	if !SessionExists(name) {
		t.Fatal("session should exist")
	}
}

func TestSessionExistsNonexistent(t *testing.T) {
	tmuxAvailable(t)

	if SessionExists("pp-test-nonexistent-session-xyz") {
		t.Fatal("nonexistent session should not exist")
	}
}

func TestSendKeysAndCapture(t *testing.T) {
	tmuxAvailable(t)

	name := "pp-test-rascal-keys"
	t.Cleanup(func() { KillSession(name) })

	if err := CreateSession(name, t.TempDir(), nil); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Send a command that produces known output.
	if err := SendKeys(name, "echo RASCAL_TEST_MARKER"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}

	// Give the shell a moment to execute.
	time.Sleep(300 * time.Millisecond)

	out, err := CapturePane(name, 20)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}

	if !strings.Contains(out, "RASCAL_TEST_MARKER") {
		t.Errorf("captured pane should contain marker, got:\n%s", out)
	}
}

func TestSendKeysLiteral(t *testing.T) {
	tmuxAvailable(t)

	name := "pp-test-rascal-literal"
	t.Cleanup(func() { KillSession(name) })

	if err := CreateSession(name, t.TempDir(), nil); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// SendKeysLiteral should NOT send Enter, so the text stays on the prompt.
	if err := SendKeysLiteral(name, "literal_text_here"); err != nil {
		t.Fatalf("SendKeysLiteral: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	out, err := CapturePane(name, 5)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}

	if !strings.Contains(out, "literal_text_here") {
		t.Errorf("literal text should appear in pane, got:\n%s", out)
	}
}

func TestListSessions(t *testing.T) {
	tmuxAvailable(t)

	name := "pp-test-rascal-list"
	t.Cleanup(func() { KillSession(name) })

	if err := CreateSession(name, t.TempDir(), nil); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sessions, err := ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	found := false
	for _, s := range sessions {
		if s == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListSessions should include %q, got %v", name, sessions)
	}
}

func TestKillNonexistentSession(t *testing.T) {
	tmuxAvailable(t)

	err := KillSession("pp-test-nonexistent-kill-xyz")
	if err == nil {
		t.Fatal("KillSession on nonexistent session should return error")
	}
}

func TestCapturePaneVisibleOnly(t *testing.T) {
	tmuxAvailable(t)

	name := "pp-test-rascal-capture0"
	t.Cleanup(func() { KillSession(name) })

	if err := CreateSession(name, t.TempDir(), nil); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// lines=0 captures just the visible pane.
	out, err := CapturePane(name, 0)
	if err != nil {
		t.Fatalf("CapturePane(0): %v", err)
	}
	// Should return something (at least a prompt or empty lines).
	_ = out
}
