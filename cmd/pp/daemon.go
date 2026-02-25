// daemon.go implements the 'pp daemon' subcommands: run, stop, status.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/chazu/maggie/vm"
	"github.com/chazu/procyon-park/internal/daemon"
	"github.com/chazu/procyon-park/internal/telemetry"
	"github.com/chazu/procyon-park/internal/tuplestore"
	"github.com/chazu/procyon-park/internal/worktracker"
)

// defaultDataDir returns ~/.procyon-park.
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".procyon-park"
	}
	return filepath.Join(home, ".procyon-park")
}

// defaultPIDPath returns the PID file path within the data directory.
func defaultPIDPath(dataDir string) string {
	return filepath.Join(dataDir, "daemon.pid")
}

// defaultDBPath returns the tuplestore database path within the data directory.
func defaultDBPath(dataDir string) string {
	return filepath.Join(dataDir, "tuples.db")
}

// runDaemonRun starts the daemon in the foreground.
func runDaemonRun(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	pidPath := defaultPIDPath(dataDir)
	dbPath := defaultDBPath(dataDir)

	store, err := tuplestore.NewStore(dbPath)
	if err != nil {
		return fmt.Errorf("open tuplestore: %w", err)
	}

	vmInst := vm.NewVM()
	telemetry.Register(vmInst)
	worktracker.Register(vmInst)

	cfg := daemon.Config{
		DataDir: dataDir,
		PIDPath: pidPath,
	}

	d := daemon.New(vmInst, store, cfg)
	return d.Run(context.Background())
}

// runDaemonStop sends SIGTERM to the running daemon via PID file.
func runDaemonStop(dataDir string) error {
	pidPath := defaultPIDPath(dataDir)

	pf := daemon.NewPIDFile(pidPath)
	pid, err := pf.Read()
	if err != nil {
		return fmt.Errorf("no running daemon found (cannot read PID file: %v)", err)
	}

	if !daemon.IsProcessAlive(pid) {
		// Stale PID file — clean it up
		os.Remove(pidPath)
		return fmt.Errorf("no running daemon (stale PID %d)", pid)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to PID %d: %w", pid, err)
	}

	fmt.Fprintf(os.Stdout, "Sent SIGTERM to daemon (PID %d)\n", pid)
	return nil
}

// runDaemonStatus checks whether the daemon is running.
// Prints status to stdout. Returns exit code 0 if running, 1 if not.
func runDaemonStatus(dataDir string) int {
	pidPath := defaultPIDPath(dataDir)

	pf := daemon.NewPIDFile(pidPath)
	pid, err := pf.Read()
	if err != nil {
		fmt.Fprintln(os.Stdout, "Daemon is not running (no PID file)")
		return 1
	}

	if !daemon.IsProcessAlive(pid) {
		os.Remove(pidPath)
		fmt.Fprintf(os.Stdout, "Daemon is not running (stale PID %d, cleaned up)\n", pid)
		return 1
	}

	fmt.Fprintf(os.Stdout, "Daemon is running (PID %d)\n", pid)
	return 0
}

// parseDaemonArgs parses 'pp daemon <subcommand>' arguments.
// Returns the subcommand ("run", "stop", "status") and the data directory.
func parseDaemonArgs(args []string) (subcmd string, dataDir string, err error) {
	dataDir = defaultDataDir()

	if len(args) == 0 {
		return "", "", fmt.Errorf("missing daemon subcommand\n\nUsage: pp daemon <run|stop|status> [--data-dir PATH]")
	}

	subcmd = args[0]
	switch subcmd {
	case "run", "stop", "status":
	default:
		return "", "", fmt.Errorf("unknown daemon subcommand %q\n\nUsage: pp daemon <run|stop|status> [--data-dir PATH]", subcmd)
	}

	// Parse remaining flags
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--data-dir":
			if i+1 >= len(rest) {
				return "", "", fmt.Errorf("--data-dir requires a value")
			}
			dataDir = rest[i+1]
			i++
		default:
			return "", "", fmt.Errorf("unknown flag %q", rest[i])
		}
	}

	return subcmd, dataDir, nil
}

// daemonUsage returns the help text for daemon subcommands.
func daemonUsage() string {
	return `Usage: pp daemon <command> [options]

Commands:
  run      Start the daemon in the foreground
  stop     Stop the running daemon (sends SIGTERM)
  status   Check if the daemon is running

Options:
  --data-dir PATH   Data directory (default: ~/.procyon-park)
`
}

// handleDaemon dispatches the daemon subcommand. Returns the process exit code.
func handleDaemon(args []string) int {
	subcmd, dataDir, err := parseDaemonArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	switch subcmd {
	case "run":
		if err := runDaemonRun(dataDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	case "stop":
		if err := runDaemonStop(dataDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	case "status":
		return runDaemonStatus(dataDir)
	default:
		// unreachable due to parseDaemonArgs validation
		fmt.Fprintf(os.Stderr, "Error: unknown subcommand %q\n", subcmd)
		return 1
	}
}

