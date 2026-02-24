package cli

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ensureDaemon tries to connect to the daemon socket. If the connection fails,
// it starts the daemon in the background and waits up to 5 seconds for the
// socket to become available.
func ensureDaemon(socketPath string) error {
	// Try to connect to the existing daemon.
	conn, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		return nil
	}

	// Daemon not running — start it in the background.
	dataDir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	ppBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	cmd := exec.Command(ppBin, "daemon", "run", "--data-dir", dataDir)
	cmd.Stdout = nil
	cmd.Stderr = nil
	// Detach from the current process group so the daemon survives.
	cmd.SysProcAttr = daemonSysProcAttr()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	// Detach — don't wait for the daemon process.
	go cmd.Wait() //nolint:errcheck

	// Poll for socket availability.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("daemon did not start within 5 seconds")
}
