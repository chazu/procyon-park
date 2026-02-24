package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chazu/procyon-park/internal/daemon"
)

// ---------------------------------------------------------------------------
// parseDaemonArgs Tests
// ---------------------------------------------------------------------------

func TestParseDaemonArgs_ValidSubcommands(t *testing.T) {
	for _, subcmd := range []string{"run", "stop", "status"} {
		t.Run(subcmd, func(t *testing.T) {
			got, _, err := parseDaemonArgs([]string{subcmd})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != subcmd {
				t.Fatalf("expected subcmd %q, got %q", subcmd, got)
			}
		})
	}
}

func TestParseDaemonArgs_DefaultDataDir(t *testing.T) {
	_, dataDir, err := parseDaemonArgs([]string{"run"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := defaultDataDir()
	if dataDir != expected {
		t.Fatalf("expected default data dir %q, got %q", expected, dataDir)
	}
}

func TestParseDaemonArgs_CustomDataDir(t *testing.T) {
	_, dataDir, err := parseDaemonArgs([]string{"run", "--data-dir", "/tmp/test-pp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dataDir != "/tmp/test-pp" {
		t.Fatalf("expected /tmp/test-pp, got %q", dataDir)
	}
}

func TestParseDaemonArgs_MissingSubcommand(t *testing.T) {
	_, _, err := parseDaemonArgs([]string{})
	if err == nil {
		t.Fatal("expected error for missing subcommand")
	}
}

func TestParseDaemonArgs_UnknownSubcommand(t *testing.T) {
	_, _, err := parseDaemonArgs([]string{"restart"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}

func TestParseDaemonArgs_DataDirMissingValue(t *testing.T) {
	_, _, err := parseDaemonArgs([]string{"run", "--data-dir"})
	if err == nil {
		t.Fatal("expected error for --data-dir without value")
	}
}

func TestParseDaemonArgs_UnknownFlag(t *testing.T) {
	_, _, err := parseDaemonArgs([]string{"run", "--foo"})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

// ---------------------------------------------------------------------------
// handleDaemon Dispatch Tests (stop/status with no daemon running)
// ---------------------------------------------------------------------------

func TestHandleDaemonStop_NoDaemon(t *testing.T) {
	dataDir := t.TempDir()
	code := handleDaemon([]string{"stop", "--data-dir", dataDir})
	if code != 1 {
		t.Fatalf("expected exit code 1 (no daemon), got %d", code)
	}
}

func TestHandleDaemonStatus_NoDaemon(t *testing.T) {
	dataDir := t.TempDir()
	code := handleDaemon([]string{"status", "--data-dir", dataDir})
	if code != 1 {
		t.Fatalf("expected exit code 1 (no daemon), got %d", code)
	}
}

func TestHandleDaemonStatus_StalePID(t *testing.T) {
	dataDir := t.TempDir()
	pidPath := filepath.Join(dataDir, "daemon.pid")

	// Write a dead PID
	deadPID := 2147483647
	if daemon.IsProcessAlive(deadPID) {
		t.Skip("dead PID is somehow alive")
	}
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", deadPID)), 0644)

	code := handleDaemon([]string{"status", "--data-dir", dataDir})
	if code != 1 {
		t.Fatalf("expected exit code 1 (stale PID), got %d", code)
	}

	// PID file should be cleaned up
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatal("stale PID file should be removed")
	}
}

func TestHandleDaemonStatus_RunningProcess(t *testing.T) {
	dataDir := t.TempDir()
	pidPath := filepath.Join(dataDir, "daemon.pid")

	// Write our own PID to simulate a running daemon
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644)

	code := handleDaemon([]string{"status", "--data-dir", dataDir})
	if code != 0 {
		t.Fatalf("expected exit code 0 (daemon running), got %d", code)
	}
}

func TestHandleDaemon_InvalidArgs(t *testing.T) {
	code := handleDaemon([]string{})
	if code != 1 {
		t.Fatalf("expected exit code 1 for missing subcommand, got %d", code)
	}
}

// ---------------------------------------------------------------------------
// run() Dispatch Tests
// ---------------------------------------------------------------------------

func TestRunVersion(t *testing.T) {
	for _, arg := range []string{"version", "--version"} {
		t.Run(arg, func(t *testing.T) {
			code := run([]string{arg})
			if code != 0 {
				t.Fatalf("expected exit code 0 for %s, got %d", arg, code)
			}
		})
	}
}

func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"help", "--help"} {
		t.Run(arg, func(t *testing.T) {
			code := run([]string{arg})
			if code != 0 {
				t.Fatalf("expected exit code 0 for %s, got %d", arg, code)
			}
		})
	}
}

func TestRunDaemonDispatch(t *testing.T) {
	// 'pp daemon' with no subcommand should return error exit code
	code := run([]string{"daemon"})
	if code != 1 {
		t.Fatalf("expected exit code 1 for 'pp daemon' with no subcommand, got %d", code)
	}
}

func TestRunDaemonStatusDispatch(t *testing.T) {
	// 'pp daemon status' with empty data dir should return 1 (no daemon)
	dataDir := t.TempDir()
	code := run([]string{"daemon", "status", "--data-dir", dataDir})
	if code != 1 {
		t.Fatalf("expected exit code 1 (no daemon running), got %d", code)
	}
}

// ---------------------------------------------------------------------------
// daemonUsage Tests
// ---------------------------------------------------------------------------

func TestDaemonUsage(t *testing.T) {
	usage := daemonUsage()
	if len(usage) == 0 {
		t.Fatal("daemonUsage should return non-empty string")
	}
}
