package worktracker

import "sync"

// MockTracker is a WorkTracker that records calls for testing.
type MockTracker struct {
	mu    sync.Mutex
	tasks map[string]*Task
	calls []MockCall
}

// MockCall records a method invocation on MockTracker.
type MockCall struct {
	Method string
	Args   []interface{}
}

// NewMockTracker creates a MockTracker with no initial tasks.
func NewMockTracker() *MockTracker {
	return &MockTracker{tasks: make(map[string]*Task)}
}

// Calls returns a copy of recorded method calls.
func (m *MockTracker) Calls() []MockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MockCall, len(m.calls))
	copy(out, m.calls)
	return out
}

// AddTask pre-populates a task for GetTask, ListReady, etc.
func (m *MockTracker) AddTask(t Task) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks[t.ID] = &t
}

func (m *MockTracker) record(method string, args ...interface{}) {
	m.calls = append(m.calls, MockCall{Method: method, Args: args})
}

func (m *MockTracker) GetTask(id string) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record("GetTask", id)
	t, ok := m.tasks[id]
	if !ok {
		return nil, nil
	}
	cp := *t
	return &cp, nil
}

func (m *MockTracker) CreateTask(opts CreateTaskOpts) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record("CreateTask", opts)
	t := &Task{
		ID:          "mock-" + opts.Title,
		Title:       opts.Title,
		Description: opts.Description,
		Type:        opts.TaskType,
		Priority:    opts.Priority,
		Labels:      opts.Labels,
		Parent:      opts.Parent,
		Status:      "open",
	}
	m.tasks[t.ID] = t
	return t, nil
}

func (m *MockTracker) CloseTask(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record("CloseTask", id)
	if t, ok := m.tasks[id]; ok {
		t.Status = "closed"
	}
	return nil
}

func (m *MockTracker) UpdateTask(id string, opts UpdateTaskOpts) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record("UpdateTask", id, opts)
	t, ok := m.tasks[id]
	if !ok {
		return nil
	}
	if opts.Status != nil {
		t.Status = *opts.Status
	}
	if opts.Assignee != nil {
		t.Assignee = *opts.Assignee
	}
	if opts.Notes != nil {
		t.Notes = *opts.Notes
	}
	if opts.Title != nil {
		t.Title = *opts.Title
	}
	if opts.Description != nil {
		t.Description = *opts.Description
	}
	return nil
}

func (m *MockTracker) ListReady() ([]Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record("ListReady")
	var out []Task
	for _, t := range m.tasks {
		if t.Status == "open" && len(t.BlockedBy) == 0 {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (m *MockTracker) ListByStatus(status string) ([]Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record("ListByStatus", status)
	var out []Task
	for _, t := range m.tasks {
		if t.Status == status {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (m *MockTracker) ListByParent(epicID string) ([]Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record("ListByParent", epicID)
	var out []Task
	for _, t := range m.tasks {
		if t.Parent == epicID {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (m *MockTracker) AddDependency(taskID, dependsOnID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record("AddDependency", taskID, dependsOnID)
	if t, ok := m.tasks[taskID]; ok {
		t.BlockedBy = append(t.BlockedBy, dependsOnID)
	}
	return nil
}
