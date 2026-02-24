package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/chazu/maggie/vm"
	"github.com/chazu/procyon-park/internal/daemon"
	"github.com/chazu/procyon-park/internal/tuplestore"
	"github.com/spf13/cobra"
)

var flagDataDir string

// DaemonCmd is the top-level daemon command.
var DaemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the background daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.Help()
		return NewExitErr(ExitUsage, fmt.Errorf("missing daemon subcommand"))
	},
}

var daemonRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the daemon in the foreground",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dataDir := resolveDataDir()
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return NewExitErr(ExitError, fmt.Errorf("create data dir: %w", err))
		}

		pidPath := filepath.Join(dataDir, "daemon.pid")
		dbPath := filepath.Join(dataDir, "tuples.db")

		store, err := tuplestore.NewStore(dbPath)
		if err != nil {
			return NewExitErr(ExitError, fmt.Errorf("open tuplestore: %w", err))
		}

		vmInst := vm.NewVM()
		cfg := daemon.Config{
			DataDir: dataDir,
			PIDPath: pidPath,
		}

		d := daemon.New(vmInst, store, cfg)
		if err := d.Run(context.Background()); err != nil {
			return NewExitErr(ExitError, err)
		}
		return nil
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running daemon (sends SIGTERM)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dataDir := resolveDataDir()
		pidPath := filepath.Join(dataDir, "daemon.pid")

		pf := daemon.NewPIDFile(pidPath)
		pid, err := pf.Read()
		if err != nil {
			return NewExitErr(ExitError, fmt.Errorf("no running daemon found (cannot read PID file: %v)", err))
		}

		if !daemon.IsProcessAlive(pid) {
			os.Remove(pidPath)
			return NewExitErr(ExitError, fmt.Errorf("no running daemon (stale PID %d)", pid))
		}

		proc, err := os.FindProcess(pid)
		if err != nil {
			return NewExitErr(ExitError, fmt.Errorf("find process %d: %w", pid, err))
		}

		if err := proc.Signal(syscall.SIGTERM); err != nil {
			return NewExitErr(ExitError, fmt.Errorf("send SIGTERM to PID %d: %w", pid, err))
		}

		fmt.Fprintf(os.Stdout, "Sent SIGTERM to daemon (PID %d)\n", pid)
		return nil
	},
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check if the daemon is running",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dataDir := resolveDataDir()
		pidPath := filepath.Join(dataDir, "daemon.pid")

		pf := daemon.NewPIDFile(pidPath)
		pid, err := pf.Read()
		if err != nil {
			fmt.Fprintln(os.Stdout, "Daemon is not running (no PID file)")
			return NewExitErr(ExitError, fmt.Errorf("daemon not running"))
		}

		if !daemon.IsProcessAlive(pid) {
			os.Remove(pidPath)
			fmt.Fprintf(os.Stdout, "Daemon is not running (stale PID %d, cleaned up)\n", pid)
			return NewExitErr(ExitError, fmt.Errorf("daemon not running"))
		}

		fmt.Fprintf(os.Stdout, "Daemon is running (PID %d)\n", pid)
		return nil
	},
}

var daemonRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the daemon (stop then auto-start on next command)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dataDir := resolveDataDir()
		pidPath := filepath.Join(dataDir, "daemon.pid")

		pf := daemon.NewPIDFile(pidPath)
		pid, err := pf.Read()
		if err == nil && daemon.IsProcessAlive(pid) {
			proc, err := os.FindProcess(pid)
			if err == nil {
				if err := proc.Signal(syscall.SIGTERM); err != nil {
					return NewExitErr(ExitError, fmt.Errorf("send SIGTERM to PID %d: %w", pid, err))
				}
				// Wait briefly for the daemon to stop.
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					if !daemon.IsProcessAlive(pid) {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				if daemon.IsProcessAlive(pid) {
					return NewExitErr(ExitError, fmt.Errorf("daemon (PID %d) did not stop within 5 seconds", pid))
				}
			}
		}

		// Start a fresh daemon via EnsureDaemon.
		socketPath := filepath.Join(dataDir, "daemon.sock")
		if err := ensureDaemon(socketPath); err != nil {
			return NewExitErr(ExitError, fmt.Errorf("restart: %w", err))
		}

		fmt.Fprintln(os.Stdout, "Daemon restarted")
		return nil
	},
}

func init() {
	pf := DaemonCmd.PersistentFlags()
	pf.StringVar(&flagDataDir, "data-dir", "", "data directory (default: ~/.procyon-park)")

	DaemonCmd.AddCommand(daemonRunCmd, daemonStopCmd, daemonStatusCmd, daemonRestartCmd)
}

// resolveDataDir returns the data directory from the --data-dir flag or the default.
func resolveDataDir() string {
	if flagDataDir != "" {
		return flagDataDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".procyon-park"
	}
	return filepath.Join(home, ".procyon-park")
}
