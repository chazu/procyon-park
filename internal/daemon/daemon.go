package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/chazu/maggie/vm"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// Config holds daemon configuration values.
type Config struct {
	// DataDir is the directory for daemon state (PID file, socket, image, DB).
	// Defaults to ~/.procyon-park.
	DataDir string

	// SocketPath overrides the default Unix socket path.
	SocketPath string

	// PIDPath overrides the default PID file path.
	PIDPath string

	// ShutdownTimeout is the maximum time to wait for in-flight requests
	// during graceful shutdown, in seconds. Defaults to 30.
	ShutdownTimeout int
}

// DaemonServer is the core daemon process. It holds the Maggie VM,
// TupleStore, and manages the daemon lifecycle (PID file, signals,
// graceful shutdown).
type DaemonServer struct {
	config  Config
	vmInst  *vm.VM
	worker  *VMWorker
	store   *tuplestore.TupleStore
	pidFile *PIDFile

	// shutdownOnce ensures Shutdown runs exactly once.
	shutdownOnce sync.Once
	// shutdownCh is closed when shutdown begins.
	shutdownCh chan struct{}
}

// New creates a DaemonServer with the given VM, TupleStore, and config.
// The VM and store must already be initialized. Call Run() to start the daemon.
func New(vmInst *vm.VM, store *tuplestore.TupleStore, cfg Config) *DaemonServer {
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 30
	}
	return &DaemonServer{
		config:     cfg,
		vmInst:     vmInst,
		store:      store,
		shutdownCh: make(chan struct{}),
	}
}

// Run starts the daemon: acquires the PID file, starts the VMWorker,
// installs signal handlers, and blocks until shutdown completes.
// Returns an error if the daemon cannot start (e.g., PID file held by another process).
func (d *DaemonServer) Run(ctx context.Context) error {
	// Acquire PID file
	if d.config.PIDPath != "" {
		d.pidFile = NewPIDFile(d.config.PIDPath)
		if err := d.pidFile.Acquire(); err != nil {
			return fmt.Errorf("daemon: %w", err)
		}
	}

	// Start the VMWorker
	d.worker = NewVMWorker(d.vmInst)

	log.Printf("daemon started, PID %d", os.Getpid())

	// Install signal handlers
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Block until signal or context cancellation
	select {
	case sig := <-sigCh:
		log.Printf("received %s, shutting down...", sig)
		// Double-signal escape hatch: second signal forces immediate exit
		go func() {
			<-sigCh
			log.Fatal("forced shutdown (double signal)")
		}()
	case <-ctx.Done():
		log.Println("context cancelled, shutting down...")
	}

	d.Shutdown()
	return nil
}

// Shutdown performs graceful shutdown: stops the VMWorker, closes the store,
// and releases the PID file. Safe to call multiple times.
func (d *DaemonServer) Shutdown() {
	d.shutdownOnce.Do(func() {
		close(d.shutdownCh)

		if d.worker != nil {
			d.worker.Stop()
		}

		if d.store != nil {
			if err := d.store.Close(); err != nil {
				log.Printf("warning: store close: %v", err)
			}
		}

		if d.pidFile != nil {
			if err := d.pidFile.Release(); err != nil {
				log.Printf("warning: pid file release: %v", err)
			}
		}

		log.Println("daemon stopped")
	})
}

// Worker returns the VMWorker for submitting VM operations.
func (d *DaemonServer) Worker() *VMWorker {
	return d.worker
}

// Store returns the TupleStore.
func (d *DaemonServer) Store() *tuplestore.TupleStore {
	return d.store
}

// ShutdownCh returns a channel that is closed when shutdown begins.
func (d *DaemonServer) ShutdownCh() <-chan struct{} {
	return d.shutdownCh
}
