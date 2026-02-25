// Package worktracker defines the WorkTracker interface for task management.
// It provides beads, noop, and mock implementations, plus Maggie VM primitives.
package worktracker

import (
	"sync"
)

// Task represents a work item from the issue tracker.
type Task struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	Type        string   `json:"type,omitempty"`
	Priority    int      `json:"priority,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	Parent      string   `json:"parent,omitempty"`
	Assignee    string   `json:"assignee,omitempty"`
	Notes       string   `json:"notes,omitempty"`
	BlockedBy   []string `json:"blocked_by,omitempty"`
	Blocks      []string `json:"blocks,omitempty"`
}

// CreateTaskOpts holds options for creating a new task.
type CreateTaskOpts struct {
	Title       string
	Description string
	TaskType    string // task, bug, feature
	Priority    int    // 0-4 (0=critical, 4=backlog)
	Labels      []string
	Parent      string
}

// UpdateTaskOpts holds options for updating an existing task.
// Pointer fields allow distinguishing "not set" from zero values.
type UpdateTaskOpts struct {
	Status      *string
	Assignee    *string
	Notes       *string
	Title       *string
	Description *string
}

// WorkTracker is the interface for task/issue management.
type WorkTracker interface {
	// GetTask retrieves a task by ID.
	GetTask(id string) (*Task, error)

	// CreateTask creates a new task and returns it.
	CreateTask(opts CreateTaskOpts) (*Task, error)

	// CloseTask marks a task as complete.
	CloseTask(id string) error

	// UpdateTask updates fields on an existing task.
	UpdateTask(id string, opts UpdateTaskOpts) error

	// ListReady returns tasks that are unblocked and ready for work.
	ListReady() ([]Task, error)

	// ListByStatus returns tasks matching the given status.
	ListByStatus(status string) ([]Task, error)

	// ListByParent returns tasks that are children of the given epic ID.
	ListByParent(epicID string) ([]Task, error)

	// AddDependency records that taskID depends on dependsOnID.
	AddDependency(taskID, dependsOnID string) error
}

// defaultTracker is the package-level default WorkTracker.
var (
	defaultTracker WorkTracker = &NoopTracker{}
	defaultMu      sync.RWMutex
)

// SetDefault sets the package-level default WorkTracker.
func SetDefault(t WorkTracker) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultTracker = t
}

// Default returns the package-level default WorkTracker.
func Default() WorkTracker {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultTracker
}
