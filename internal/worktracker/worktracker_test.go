package worktracker

import (
	"testing"
)

// TestNoopTrackerInterface verifies NoopTracker satisfies WorkTracker.
func TestNoopTrackerInterface(t *testing.T) {
	var _ WorkTracker = (*NoopTracker)(nil)
}

// TestMockTrackerInterface verifies MockTracker satisfies WorkTracker.
func TestMockTrackerInterface(t *testing.T) {
	var _ WorkTracker = (*MockTracker)(nil)
}

// TestBeadsTrackerInterface verifies BeadsTracker satisfies WorkTracker.
func TestBeadsTrackerInterface(t *testing.T) {
	var _ WorkTracker = (*BeadsTracker)(nil)
}

func TestNoopTracker(t *testing.T) {
	n := &NoopTracker{}

	task, err := n.GetTask("anything")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task != nil {
		t.Fatalf("GetTask: expected nil, got %v", task)
	}

	created, err := n.CreateTask(CreateTaskOpts{Title: "test"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created == nil {
		t.Fatal("CreateTask: expected non-nil")
	}

	if err := n.CloseTask("x"); err != nil {
		t.Fatalf("CloseTask: %v", err)
	}
	if err := n.UpdateTask("x", UpdateTaskOpts{}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	ready, err := n.ListReady()
	if err != nil {
		t.Fatalf("ListReady: %v", err)
	}
	if ready != nil {
		t.Fatalf("ListReady: expected nil, got %v", ready)
	}

	if err := n.AddDependency("a", "b"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
}

func TestMockTracker(t *testing.T) {
	m := NewMockTracker()

	// Pre-populate a task.
	m.AddTask(Task{
		ID:     "task-1",
		Title:  "Test Task",
		Status: "open",
		Parent: "epic-1",
	})

	// GetTask
	task, err := m.GetTask("task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task == nil || task.ID != "task-1" {
		t.Fatalf("GetTask: unexpected result %v", task)
	}

	// GetTask miss
	task, err = m.GetTask("nonexistent")
	if err != nil {
		t.Fatalf("GetTask miss: %v", err)
	}
	if task != nil {
		t.Fatalf("GetTask miss: expected nil, got %v", task)
	}

	// CreateTask
	created, err := m.CreateTask(CreateTaskOpts{
		Title:    "New Task",
		TaskType: "feature",
		Priority: 2,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.Title != "New Task" || created.Status != "open" {
		t.Fatalf("CreateTask: unexpected result %v", created)
	}

	// UpdateTask
	status := "in_progress"
	if err := m.UpdateTask("task-1", UpdateTaskOpts{Status: &status}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	task, _ = m.GetTask("task-1")
	if task.Status != "in_progress" {
		t.Fatalf("UpdateTask: status not updated, got %q", task.Status)
	}

	// CloseTask
	if err := m.CloseTask("task-1"); err != nil {
		t.Fatalf("CloseTask: %v", err)
	}
	task, _ = m.GetTask("task-1")
	if task.Status != "closed" {
		t.Fatalf("CloseTask: status not closed, got %q", task.Status)
	}

	// ListReady — task-1 is closed, so only "New Task" is open and unblocked.
	ready, err := m.ListReady()
	if err != nil {
		t.Fatalf("ListReady: %v", err)
	}
	if len(ready) != 1 || ready[0].Title != "New Task" {
		t.Fatalf("ListReady: unexpected %v", ready)
	}

	// ListByStatus
	closed, err := m.ListByStatus("closed")
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(closed) != 1 || closed[0].ID != "task-1" {
		t.Fatalf("ListByStatus: unexpected %v", closed)
	}

	// ListByParent
	children, err := m.ListByParent("epic-1")
	if err != nil {
		t.Fatalf("ListByParent: %v", err)
	}
	if len(children) != 1 || children[0].ID != "task-1" {
		t.Fatalf("ListByParent: unexpected %v", children)
	}

	// AddDependency
	m.AddTask(Task{ID: "task-2", Title: "Dependent", Status: "open"})
	if err := m.AddDependency("task-2", "task-1"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	task, _ = m.GetTask("task-2")
	if len(task.BlockedBy) != 1 || task.BlockedBy[0] != "task-1" {
		t.Fatalf("AddDependency: blocked_by not set %v", task.BlockedBy)
	}

	// Verify calls were recorded.
	calls := m.Calls()
	if len(calls) == 0 {
		t.Fatal("expected recorded calls")
	}
}

func TestDefaultTracker(t *testing.T) {
	// Default should be noop.
	d := Default()
	if _, ok := d.(*NoopTracker); !ok {
		t.Fatalf("expected default to be *NoopTracker, got %T", d)
	}

	// Set and get.
	m := NewMockTracker()
	SetDefault(m)
	if Default() != m {
		t.Fatal("SetDefault did not take effect")
	}

	// Reset.
	SetDefault(&NoopTracker{})
}
