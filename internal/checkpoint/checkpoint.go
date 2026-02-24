// Package checkpoint provides periodic image checkpointing with crash recovery
// for the Maggie VM daemon. It wraps the VM's atomic save mechanism with a
// configurable timer and startup recovery logic.
package checkpoint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chazu/maggie/vm"
)

// DefaultInterval is the default checkpoint interval.
const DefaultInterval = 5 * time.Minute

// SaveFunc is the signature for the atomic image save operation.
// It takes a path and writes the image atomically (.tmp → fsync → .prev → rename → fsync dir).
type SaveFunc func(path string) error

// Manager coordinates periodic image checkpointing for the daemon.
// It runs a background goroutine that triggers saves at a configurable interval.
type Manager struct {
	path     string
	interval time.Duration
	saveFn   SaveFunc

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}

	// lastErr records the most recent checkpoint error (nil if last succeeded).
	lastErr error
	// lastSave records the time of the most recent successful checkpoint.
	lastSave time.Time
	// count tracks the total number of successful checkpoints.
	count int
}

// NewManager creates a Manager that will periodically save the VM image to path.
// The saveFn is called to perform the actual atomic write (typically vm.SaveImageAtomic).
// The interval controls how often checkpoints occur. Use DefaultInterval for the default.
func NewManager(path string, interval time.Duration, saveFn SaveFunc) *Manager {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Manager{
		path:     path,
		interval: interval,
		saveFn:   saveFn,
	}
}

// NewManagerForVM creates a Manager wired directly to a VM instance.
func NewManagerForVM(v *vm.VM, path string, interval time.Duration) *Manager {
	return NewManager(path, interval, v.SaveImageAtomic)
}

// Start begins periodic checkpointing in a background goroutine.
// Returns an error if the manager is already running.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return errors.New("checkpoint manager already running")
	}
	m.running = true
	m.stopCh = make(chan struct{})
	m.doneCh = make(chan struct{})
	go m.loop()
	return nil
}

// Stop halts periodic checkpointing and waits for the background goroutine to exit.
func (m *Manager) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	m.running = false
	close(m.stopCh)
	m.mu.Unlock()
	<-m.doneCh
}

// CheckpointNow triggers an immediate checkpoint, blocking until complete.
// Safe to call concurrently with the periodic timer.
func (m *Manager) CheckpointNow() error {
	return m.doCheckpoint()
}

// LastError returns the error from the most recent checkpoint attempt.
func (m *Manager) LastError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErr
}

// LastSave returns the time of the most recent successful checkpoint.
func (m *Manager) LastSave() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastSave
}

// Count returns the total number of successful checkpoints.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count
}

// Path returns the configured image path.
func (m *Manager) Path() string {
	return m.path
}

func (m *Manager) loop() {
	defer close(m.doneCh)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.doCheckpoint()
		case <-m.stopCh:
			return
		}
	}
}

func (m *Manager) doCheckpoint() error {
	err := m.saveFn(m.path)
	m.mu.Lock()
	m.lastErr = err
	if err == nil {
		m.lastSave = time.Now()
		m.count++
	}
	m.mu.Unlock()
	return err
}

// RecoverImage inspects the checkpoint directory for crash artifacts and restores
// the best available image. It should be called before loading the image on startup.
//
// Recovery logic:
//  1. Remove any .tmp file (partial write from interrupted checkpoint).
//  2. If the main image exists and is a valid Maggie image, use it (no action needed).
//  3. If the main image is missing but .prev exists, promote .prev to main.
//  4. If .prev is also missing, return ErrNoImage (caller should fall back to embedded).
//
// Returns the path to the recovered image, or an error.
func RecoverImage(path string) (string, error) {
	tmpPath := path + ".tmp"
	prevPath := path + ".prev"

	// Always remove partial temp file.
	os.Remove(tmpPath)

	// Check main image.
	if isValidImage(path) {
		return path, nil
	}

	// Main is missing or corrupt — try .prev.
	if isValidImage(prevPath) {
		if err := os.Rename(prevPath, path); err != nil {
			return "", fmt.Errorf("recover: rename .prev to main: %w", err)
		}
		// Fsync directory to make the rename durable.
		syncDir(filepath.Dir(path))
		return path, nil
	}

	return "", ErrNoImage
}

// ErrNoImage is returned by RecoverImage when no valid image file is found.
var ErrNoImage = errors.New("no valid image file found")

// isValidImage checks that the file exists and starts with the Maggie image magic number.
func isValidImage(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	var magic [4]byte
	n, err := f.Read(magic[:])
	if err != nil || n < 4 {
		return false
	}
	return magic == vm.ImageMagic
}

// syncDir fsyncs a directory to ensure rename durability.
func syncDir(dirPath string) {
	d, err := os.Open(dirPath)
	if err == nil {
		d.Sync()
		d.Close()
	}
}
