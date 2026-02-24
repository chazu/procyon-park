package cli

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDaemon_AlreadyRunning(t *testing.T) {
	// Create a listening socket to simulate a running daemon.
	sockPath := filepath.Join("/tmp", "ensure-daemon-test-"+t.Name()+".sock")
	os.Remove(sockPath)
	t.Cleanup(func() { os.Remove(sockPath) })

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	err = ensureDaemon(sockPath)
	if err != nil {
		t.Fatalf("ensureDaemon should succeed when daemon is already running: %v", err)
	}
}

func TestEnsureDaemon_NoDaemon_TimesOut(t *testing.T) {
	// Use a socket path where nothing is listening and no pp binary exists.
	// ensureDaemon should fail because it can't start the daemon.
	sockPath := filepath.Join(t.TempDir(), "daemon.sock")

	err := ensureDaemon(sockPath)
	if err == nil {
		t.Fatal("ensureDaemon should fail when daemon cannot be started")
	}
}
