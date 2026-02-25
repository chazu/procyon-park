package worktracker

// NoopTracker is a WorkTracker that does nothing. Used when no beads
// directory is present or when task tracking is not needed.
type NoopTracker struct{}

func (n *NoopTracker) GetTask(id string) (*Task, error)                    { return nil, nil }
func (n *NoopTracker) CreateTask(opts CreateTaskOpts) (*Task, error)       { return &Task{}, nil }
func (n *NoopTracker) CloseTask(id string) error                          { return nil }
func (n *NoopTracker) UpdateTask(id string, opts UpdateTaskOpts) error     { return nil }
func (n *NoopTracker) ListReady() ([]Task, error)                         { return nil, nil }
func (n *NoopTracker) ListByStatus(status string) ([]Task, error)         { return nil, nil }
func (n *NoopTracker) ListByParent(epicID string) ([]Task, error)         { return nil, nil }
func (n *NoopTracker) AddDependency(taskID, dependsOnID string) error     { return nil }
