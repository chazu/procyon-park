package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/chazu/maggie/vm"
	"github.com/chazu/procyon-park/internal/registry"
	"github.com/chazu/procyon-park/internal/tuplestore"
	"github.com/chazu/procyon-park/internal/workflow"
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
	config    Config
	vmInst    *vm.VM
	worker    *VMWorker
	store     *tuplestore.TupleStore
	wfStore   *workflow.Store
	executor  *workflow.Executor
	pidFile   *PIDFile
	ipcServer *IPCServer

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

	// Initialize workflow engine.
	if err := d.initWorkflowEngine(); err != nil {
		d.worker.Stop()
		if d.pidFile != nil {
			d.pidFile.Release()
		}
		return fmt.Errorf("daemon: %w", err)
	}

	// Start the IPC server if a socket path is configured.
	socketPath := d.resolveSocketPath()
	if socketPath != "" {
		d.ipcServer = NewIPCServer(socketPath, d.shutdownCh)
		RegisterBBSHandlers(d.ipcServer, d.store)
		RegisterAgentHandlers(d.ipcServer, d.store)
		regPath := filepath.Join(d.config.DataDir, "repos.json")
		reg, err := registry.New(regPath)
		if err != nil {
			d.worker.Stop()
			if d.pidFile != nil {
				d.pidFile.Release()
			}
			return fmt.Errorf("daemon: init registry: %w", err)
		}
		RegisterRepoHandlers(d.ipcServer, reg)
		RegisterConfigHandlers(d.ipcServer, d.store)
		RegisterPrimeHandlers(d.ipcServer, d.store)
		RegisterWorkflowHandlers(d.ipcServer, d.executor, d.store)
		RegisterAnalyticsHandlers(d.ipcServer, d.store, d.config.DataDir)
		if err := d.ipcServer.Start(); err != nil {
			d.worker.Stop()
			if d.pidFile != nil {
				d.pidFile.Release()
			}
			return fmt.Errorf("daemon: %w", err)
		}
		log.Printf("daemon started, PID %d, socket %s", os.Getpid(), socketPath)
	} else {
		log.Printf("daemon started, PID %d", os.Getpid())
	}

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

		// Stop accepting new connections and drain in-flight requests.
		if d.ipcServer != nil {
			d.ipcServer.Stop(time.Duration(d.config.ShutdownTimeout) * time.Second)
		}

		if d.worker != nil {
			d.worker.Stop()
		}

		if d.wfStore != nil {
			if err := d.wfStore.Close(); err != nil {
				log.Printf("warning: workflow store close: %v", err)
			}
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

// IPCServer returns the IPC server, or nil if no socket path was configured.
func (d *DaemonServer) IPCServer() *IPCServer {
	return d.ipcServer
}

// ShutdownCh returns a channel that is closed when shutdown begins.
func (d *DaemonServer) ShutdownCh() <-chan struct{} {
	return d.shutdownCh
}

// Executor returns the workflow Executor, or nil if not initialized.
func (d *DaemonServer) Executor() *workflow.Executor {
	return d.executor
}

// initWorkflowEngine creates the workflow Store and Executor.
// Step handlers start as an empty registry; they are wired by the
// integration layer that connects spawn/dismiss/tuplestore callbacks.
func (d *DaemonServer) initWorkflowEngine() error {
	dbPath := filepath.Join(d.config.DataDir, "workflows.db")
	wfStore, err := workflow.NewStore(dbPath)
	if err != nil {
		return fmt.Errorf("workflow store: %w", err)
	}
	d.wfStore = wfStore

	// Step handlers start empty; callers can populate via SetStepHandlers
	// before or after Run. The executor still handles list/show/cancel/defs.
	registry := make(map[string]workflow.StepHandler)
	d.executor = workflow.NewExecutor(wfStore, registry, d.config.DataDir)

	return nil
}

// SetStepHandlers replaces the executor's step handler registry.
// Must be called before running any workflows. Typically called after
// New() but before Run() by the integration layer.
func (d *DaemonServer) SetStepHandlers(registry map[string]workflow.StepHandler) {
	d.executor = workflow.NewExecutor(d.wfStore, registry, d.config.DataDir)
}

// resolveSocketPath returns the Unix socket path to use.
// It uses Config.SocketPath if set, otherwise derives it from DataDir.
// Returns empty string if neither is configured.
func (d *DaemonServer) resolveSocketPath() string {
	if d.config.SocketPath != "" {
		return d.config.SocketPath
	}
	if d.config.DataDir != "" {
		return filepath.Join(d.config.DataDir, "daemon.sock")
	}
	return ""
}
