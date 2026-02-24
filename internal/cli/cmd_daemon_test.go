package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chazu/procyon-park/internal/daemon"
)

func TestDaemonCmd_NoSubcommand(t *testing.T) {
	resetFlags(t)
	code := ExecuteArgs([]string{"daemon"})
	if code != ExitError {
		t.Fatalf("expected exit code %d, got %d", ExitError, code)
	}
}

func TestDaemonRunCmd_Help(t *testing.T) {
	resetFlags(t)
	code := ExecuteArgs([]string{"daemon", "run", "--help"})
	if code != ExitSuccess {
		t.Fatalf("expected exit code 0 for help, got %d", code)
	}
}

func TestDaemonStopCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	dataDir := t.TempDir()
	code := ExecuteArgs([]string{"daemon", "stop", "--data-dir", dataDir})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestDaemonStatusCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	dataDir := t.TempDir()
	code := ExecuteArgs([]string{"daemon", "status", "--data-dir", dataDir})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestDaemonStatusCmd_StalePID(t *testing.T) {
	resetFlags(t)
	dataDir := t.TempDir()
	pidPath := filepath.Join(dataDir, "daemon.pid")

	deadPID := 2147483647
	if daemon.IsProcessAlive(deadPID) {
		t.Skip("dead PID is somehow alive")
	}
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", deadPID)), 0644)

	code := ExecuteArgs([]string{"daemon", "status", "--data-dir", dataDir})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code for stale PID")
	}

	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatal("stale PID file should be removed")
	}
}

func TestDaemonStatusCmd_RunningProcess(t *testing.T) {
	resetFlags(t)
	dataDir := t.TempDir()
	pidPath := filepath.Join(dataDir, "daemon.pid")

	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644)

	code := ExecuteArgs([]string{"daemon", "status", "--data-dir", dataDir})
	if code != ExitSuccess {
		t.Fatalf("expected exit code 0 (daemon running), got %d", code)
	}
}

func TestDaemonStopCmd_StalePID(t *testing.T) {
	resetFlags(t)
	dataDir := t.TempDir()
	pidPath := filepath.Join(dataDir, "daemon.pid")

	deadPID := 2147483647
	if daemon.IsProcessAlive(deadPID) {
		t.Skip("dead PID is somehow alive")
	}
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", deadPID)), 0644)

	code := ExecuteArgs([]string{"daemon", "stop", "--data-dir", dataDir})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code for stale PID")
	}
}

func TestDaemonRestartCmd_Help(t *testing.T) {
	resetFlags(t)
	code := ExecuteArgs([]string{"daemon", "restart", "--help"})
	if code != ExitSuccess {
		t.Fatalf("expected exit code 0 for help, got %d", code)
	}
}

func TestDaemonCmd_UnknownSubcommand(t *testing.T) {
	resetFlags(t)
	code := ExecuteArgs([]string{"daemon", "foo"})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code for unknown subcommand")
	}
}

func TestDaemonCmd_DataDirFlag(t *testing.T) {
	resetFlags(t)
	dataDir := t.TempDir()
	code := ExecuteArgs([]string{"daemon", "status", "--data-dir", dataDir})
	// Expect failure (no daemon), but the flag should be accepted.
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit (no daemon running)")
	}
}
