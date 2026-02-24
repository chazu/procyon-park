package daemon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// PIDFile manages a PID file for singleton daemon enforcement.
type PIDFile struct {
	path string
}

// NewPIDFile creates a PIDFile manager for the given path.
func NewPIDFile(path string) *PIDFile {
	return &PIDFile{path: path}
}

// Acquire creates the PID file with the current process ID.
// Returns an error if another live daemon holds the PID file.
// Stale PID files (where the process is dead) are automatically removed.
func (p *PIDFile) Acquire() error {
	// Try exclusive creation first
	f, err := os.OpenFile(p.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("pidfile: create %s: %w", p.path, err)
		}
		// File exists — check if the process is still alive
		pid, readErr := p.Read()
		if readErr != nil {
			// Can't read existing PID file; remove and retry
			os.Remove(p.path)
			return p.Acquire()
		}
		if IsProcessAlive(pid) {
			return fmt.Errorf("daemon already running (PID %d)", pid)
		}
		// Stale PID file — remove and retry
		os.Remove(p.path)
		return p.Acquire()
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%d\n", os.Getpid())
	return err
}

// Release removes the PID file. Safe to call multiple times.
func (p *PIDFile) Release() error {
	err := os.Remove(p.path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("pidfile: remove %s: %w", p.path, err)
	}
	return nil
}

// Read returns the PID stored in the PID file.
func (p *PIDFile) Read() (int, error) {
	data, err := os.ReadFile(p.path)
	if err != nil {
		return 0, fmt.Errorf("pidfile: read %s: %w", p.path, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("pidfile: parse %s: %w", p.path, err)
	}
	return pid, nil
}

// Path returns the PID file path.
func (p *PIDFile) Path() string {
	return p.path
}

// IsProcessAlive checks whether a process with the given PID exists.
// Uses signal 0 (the standard Unix liveness check).
func IsProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
