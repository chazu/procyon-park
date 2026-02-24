package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/chazu/maggie/vm"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// ---------------------------------------------------------------------------
// PID File Tests
// ---------------------------------------------------------------------------

func TestPIDFileAcquireRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	pf := NewPIDFile(path)

	// Acquire should succeed
	if err := pf.Acquire(); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	// PID file should contain our PID
	pid, err := pf.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if pid != os.Getpid() {
		t.Fatalf("expected PID %d, got %d", os.Getpid(), pid)
	}

	// Release should succeed
	if err := pf.Release(); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	// File should be gone
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("PID file still exists after Release")
	}
}

func TestPIDFileDoubleRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	pf := NewPIDFile(path)

	if err := pf.Acquire(); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	if err := pf.Release(); err != nil {
		t.Fatalf("first Release failed: %v", err)
	}

	// Second release should not error (idempotent)
	if err := pf.Release(); err != nil {
		t.Fatalf("second Release failed: %v", err)
	}
}

func TestPIDFileBlocksSecondAcquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	pf1 := NewPIDFile(path)
	pf2 := NewPIDFile(path)

	if err := pf1.Acquire(); err != nil {
		t.Fatalf("first Acquire failed: %v", err)
	}
	defer pf1.Release()

	// Second acquire should fail (our process is alive)
	err := pf2.Acquire()
	if err == nil {
		t.Fatal("expected error from second Acquire, got nil")
	}
}

func TestPIDFileStalePIDRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")

	// Write a PID file with a dead PID (PID 1 is init, but a very high PID
	// is almost certainly not running)
	deadPID := 2147483647 // max PID on most systems, very unlikely to be alive
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", deadPID)), 0644); err != nil {
		t.Fatalf("write stale PID: %v", err)
	}

	// Verify the PID is actually dead (skip test if somehow it's alive)
	if IsProcessAlive(deadPID) {
		t.Skip("dead PID is somehow alive, skipping stale detection test")
	}

	pf := NewPIDFile(path)
	if err := pf.Acquire(); err != nil {
		t.Fatalf("Acquire should succeed over stale PID: %v", err)
	}
	defer pf.Release()

	pid, err := pf.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if pid != os.Getpid() {
		t.Fatalf("expected our PID %d, got %d", os.Getpid(), pid)
	}
}

func TestPIDFileCorruptContentRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")

	// Write garbage to the PID file
	if err := os.WriteFile(path, []byte("not-a-pid\n"), 0644); err != nil {
		t.Fatalf("write corrupt PID: %v", err)
	}

	pf := NewPIDFile(path)
	if err := pf.Acquire(); err != nil {
		t.Fatalf("Acquire should succeed over corrupt PID file: %v", err)
	}
	defer pf.Release()
}

func TestIsProcessAlive(t *testing.T) {
	// Our own PID should be alive
	if !IsProcessAlive(os.Getpid()) {
		t.Fatal("our own PID should be alive")
	}

	// A very high PID should not be alive
	if IsProcessAlive(2147483647) {
		t.Skip("max PID is alive, skipping")
	}
}

func TestPIDFileReadPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	pf := NewPIDFile(path)

	if pf.Path() != path {
		t.Fatalf("expected path %q, got %q", path, pf.Path())
	}

	// Read should fail before Acquire
	_, err := pf.Read()
	if err == nil {
		t.Fatal("Read should fail when PID file doesn't exist")
	}
}

// ---------------------------------------------------------------------------
// VMWorker Tests
// ---------------------------------------------------------------------------

func TestVMWorkerDo(t *testing.T) {
	v := vm.NewVM()
	w := NewVMWorker(v)
	defer w.Stop()

	result, err := w.Do(func(v *vm.VM) interface{} {
		return 42
	})
	if err != nil {
		t.Fatalf("Do failed: %v", err)
	}
	if result != 42 {
		t.Fatalf("expected 42, got %v", result)
	}
}

func TestVMWorkerSerializesAccess(t *testing.T) {
	v := vm.NewVM()
	w := NewVMWorker(v)
	defer w.Stop()

	// Run 100 concurrent operations that all increment a counter
	var mu sync.Mutex
	var order []int
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			w.Do(func(v *vm.VM) interface{} {
				mu.Lock()
				order = append(order, n)
				mu.Unlock()
				return nil
			})
		}(i)
	}

	wg.Wait()

	if len(order) != 100 {
		t.Fatalf("expected 100 executions, got %d", len(order))
	}
}

func TestVMWorkerRecoversPanic(t *testing.T) {
	v := vm.NewVM()
	w := NewVMWorker(v)
	defer w.Stop()

	// Panic in a Do call should be caught
	_, err := w.Do(func(v *vm.VM) interface{} {
		panic("test panic")
	})
	if err == nil {
		t.Fatal("expected error from panic, got nil")
	}

	// Worker should still be functional after panic
	result, err := w.Do(func(v *vm.VM) interface{} {
		return "still alive"
	})
	if err != nil {
		t.Fatalf("post-panic Do failed: %v", err)
	}
	if result != "still alive" {
		t.Fatalf("expected 'still alive', got %v", result)
	}
}

func TestVMWorkerStop(t *testing.T) {
	v := vm.NewVM()
	w := NewVMWorker(v)
	w.Stop()

	// Do should return error after Stop
	_, err := w.Do(func(v *vm.VM) interface{} {
		return nil
	})
	if err == nil {
		t.Fatal("expected error after Stop, got nil")
	}
}

func TestVMWorkerVM(t *testing.T) {
	v := vm.NewVM()
	w := NewVMWorker(v)
	defer w.Stop()

	if w.VM() != v {
		t.Fatal("VM() should return the underlying VM")
	}
}

// ---------------------------------------------------------------------------
// DaemonServer Tests
// ---------------------------------------------------------------------------

func TestDaemonServerLifecycle(t *testing.T) {
	v := vm.NewVM()
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}

	pidPath := filepath.Join(t.TempDir(), "test.pid")
	d := New(v, store, Config{
		PIDPath:         pidPath,
		ShutdownTimeout: 5,
	})

	// Run in a goroutine, then cancel via context
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Give the daemon a moment to start
	time.Sleep(50 * time.Millisecond)

	// PID file should exist
	pid, err := NewPIDFile(pidPath).Read()
	if err != nil {
		t.Fatalf("PID file not created: %v", err)
	}
	if pid != os.Getpid() {
		t.Fatalf("PID file contains %d, expected %d", pid, os.Getpid())
	}

	// Worker should be accessible
	if d.Worker() == nil {
		t.Fatal("Worker should not be nil after Run starts")
	}

	// Cancel to trigger shutdown
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}

	// PID file should be cleaned up
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatal("PID file should be removed after shutdown")
	}
}

func TestDaemonServerPIDConflict(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "test.pid")

	// Write our own PID to simulate a running daemon
	os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0644)

	v := vm.NewVM()
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}

	d := New(v, store, Config{PIDPath: pidPath})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = d.Run(ctx)
	if err == nil {
		t.Fatal("expected error when PID file is held, got nil")
	}
}

func TestDaemonServerShutdownIdempotent(t *testing.T) {
	v := vm.NewVM()
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}

	d := New(v, store, Config{
		PIDPath: filepath.Join(t.TempDir(), "test.pid"),
	})

	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	cancel()
	time.Sleep(50 * time.Millisecond)

	// Multiple shutdown calls should not panic
	d.Shutdown()
	d.Shutdown()
}

func TestDaemonServerShutdownCh(t *testing.T) {
	v := vm.NewVM()
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}

	d := New(v, store, Config{
		PIDPath: filepath.Join(t.TempDir(), "test.pid"),
	})

	// ShutdownCh should not be closed initially
	select {
	case <-d.ShutdownCh():
		t.Fatal("ShutdownCh should not be closed before shutdown")
	default:
	}

	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	cancel()
	time.Sleep(100 * time.Millisecond)

	// ShutdownCh should be closed after shutdown
	select {
	case <-d.ShutdownCh():
	case <-time.After(time.Second):
		t.Fatal("ShutdownCh should be closed after shutdown")
	}
}

// ---------------------------------------------------------------------------
// Signal Handling Tests
// ---------------------------------------------------------------------------

func TestDaemonServerSignalShutdown(t *testing.T) {
	v := vm.NewVM()
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}

	d := New(v, store, Config{
		PIDPath:         filepath.Join(t.TempDir(), "test.pid"),
		ShutdownTimeout: 5,
	})

	ctx := context.Background()
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	// Send SIGINT to ourselves
	proc, _ := os.FindProcess(os.Getpid())
	proc.Signal(syscall.SIGINT)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not shut down after SIGINT")
	}
}

func TestDaemonServerNoPIDFile(t *testing.T) {
	v := vm.NewVM()
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}

	// No PIDPath — daemon should still work
	d := New(v, store, Config{})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
