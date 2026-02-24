package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// --- Test helpers ---

// mockHandler is a configurable StepHandler for testing.
type mockHandler struct {
	fn func(ctx context.Context, inst *Instance, stepIndex int, config json.RawMessage) (*StepResult, error)
}

func (h *mockHandler) Execute(ctx context.Context, inst *Instance, stepIndex int, config json.RawMessage) (*StepResult, error) {
	return h.fn(ctx, inst, stepIndex, config)
}

// successHandler returns a handler that always succeeds.
func successHandler(stepType string) *mockHandler {
	return &mockHandler{fn: func(ctx context.Context, inst *Instance, stepIndex int, config json.RawMessage) (*StepResult, error) {
		now := time.Now()
		return &StepResult{
			StepIndex: stepIndex,
			StepType:  stepType,
			Status:    "completed",
			StartedAt: now,
			EndedAt:   &now,
		}, nil
	}}
}

// failingHandler returns a handler that reports a step failure.
func failingHandler(stepType string) *mockHandler {
	return &mockHandler{fn: func(ctx context.Context, inst *Instance, stepIndex int, config json.RawMessage) (*StepResult, error) {
		now := time.Now()
		return &StepResult{
			StepIndex: stepIndex,
			StepType:  stepType,
			Status:    "failed",
			Error:     "intentional failure",
			StartedAt: now,
			EndedAt:   &now,
		}, nil
	}}
}

// errorHandler returns a handler that returns an execution error.
func errorHandler(stepType string) *mockHandler {
	return &mockHandler{fn: func(ctx context.Context, inst *Instance, stepIndex int, config json.RawMessage) (*StepResult, error) {
		return nil, fmt.Errorf("execution error in %s", stepType)
	}}
}

// slowHandler returns a handler that blocks until context is done.
func slowHandler(stepType string) *mockHandler {
	return &mockHandler{fn: func(ctx context.Context, inst *Instance, stepIndex int, config json.RawMessage) (*StepResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
}

// contextUpdatingHandler simulates a spawn handler that updates instance context.
func contextUpdatingHandler() *mockHandler {
	return &mockHandler{fn: func(ctx context.Context, inst *Instance, stepIndex int, config json.RawMessage) (*StepResult, error) {
		inst.Context.ActiveAgent = "test-agent"
		inst.Context.ActiveBranch = "agent/test-agent/branch"
		now := time.Now()
		return &StepResult{
			StepIndex: stepIndex,
			StepType:  "spawn",
			Status:    "completed",
			StartedAt: now,
			EndedAt:   &now,
		}, nil
	}}
}

// mockLivenessChecker implements LivenessChecker for testing.
type mockLivenessChecker struct {
	alive bool
	err   error
}

func (m *mockLivenessChecker) IsAlive(agentName, repoName string) (bool, error) {
	return m.alive, m.err
}

// mockCompletionNotifier records completion calls.
type mockCompletionNotifier struct {
	mu        sync.Mutex
	instances []*Instance
}

func (m *mockCompletionNotifier) OnComplete(inst *Instance) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instances = append(m.instances, inst)
}

func (m *mockCompletionNotifier) getInstances() []*Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*Instance, len(m.instances))
	copy(cp, m.instances)
	return cp
}

// setupTestWorkflow writes a minimal workflow CUE file for testing.
// Returns the temp directory. Since we can't easily create CUE files in tests
// without the real filesystem structure, we test the executor using
// RunFromWorkflow which takes a pre-resolved workflow directly.

// runFromWorkflow is a test helper that bypasses CUE parsing and runs
// a workflow from pre-built steps. This tests the core execution logic
// without requiring CUE infrastructure.
func runFromWorkflow(ctx context.Context, e *Executor, name, repoName string, steps []Step) (string, error) {
	inst := &Instance{
		ID:           GenerateInstanceID(),
		WorkflowName: name,
		RepoName:     repoName,
		RepoRoot:     e.repoRoot,
		Status:       StatusPending,
		CurrentStep:  0,
		Params:       make(map[string]string),
		Context:      WorkflowContext{ActiveRepo: repoName},
		StepResults:  make([]StepResult, 0, len(steps)),
		StartedAt:    time.Now().UTC(),
	}

	if err := e.store.CreateInstance(inst); err != nil {
		return "", err
	}

	loopCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.running[inst.ID] = &runningInstance{cancel: cancel, inst: inst}
	e.mu.Unlock()

	go e.executeLoop(loopCtx, inst, steps)

	return inst.ID, nil
}

// waitForTerminal polls the store until the instance reaches a terminal status.
func waitForTerminal(t *testing.T, store *Store, repoName, instanceID string, timeout time.Duration) *Instance {
	t.Helper()
	deadline := time.After(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for instance %s to reach terminal state", instanceID)
		case <-ticker.C:
			inst, err := store.GetInstance(repoName, instanceID)
			if err != nil {
				t.Fatalf("get instance: %v", err)
			}
			if inst == nil {
				continue
			}
			switch inst.Status {
			case StatusCompleted, StatusFailed, StatusCancelled:
				return inst
			}
		}
	}
}

// --- Tests ---

func TestExecutor_FullExecution(t *testing.T) {
	store, err := NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	registry := map[string]StepHandler{
		"spawn":   successHandler("spawn"),
		"wait":    successHandler("wait"),
		"dismiss": successHandler("dismiss"),
	}

	notifier := &mockCompletionNotifier{}
	e := NewExecutor(store, registry, "/tmp/test-repo",
		WithCompletionNotifier(notifier),
	)

	steps := []Step{
		{Type: "spawn", Config: json.RawMessage(`{}`)},
		{Type: "wait", Config: json.RawMessage(`{}`)},
		{Type: "dismiss", Config: json.RawMessage(`{}`)},
	}

	id, err := runFromWorkflow(context.Background(), e, "test-workflow", "test-repo", steps)
	if err != nil {
		t.Fatal(err)
	}

	inst := waitForTerminal(t, store, "test-repo", id, 5*time.Second)

	if inst.Status != StatusCompleted {
		t.Errorf("expected completed, got %s (error: %s)", inst.Status, inst.Error)
	}
	if len(inst.StepResults) != 3 {
		t.Errorf("expected 3 step results, got %d", len(inst.StepResults))
	}
	if inst.CompletedAt == nil {
		t.Error("expected CompletedAt to be set")
	}

	// Verify completion notifier was called.
	completed := notifier.getInstances()
	if len(completed) != 1 {
		t.Errorf("expected 1 completion notification, got %d", len(completed))
	}
}

func TestExecutor_StepFailure(t *testing.T) {
	store, err := NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var notified bool
	var notifiedStep int
	var mu sync.Mutex

	registry := map[string]StepHandler{
		"spawn": successHandler("spawn"),
		"wait":  failingHandler("wait"),
	}

	e := NewExecutor(store, registry, "/tmp/test-repo",
		WithNotifyFunc(func(inst *Instance, stepIndex int, err error) {
			mu.Lock()
			notified = true
			notifiedStep = stepIndex
			mu.Unlock()
		}),
	)

	steps := []Step{
		{Type: "spawn", Config: json.RawMessage(`{}`)},
		{Type: "wait", Config: json.RawMessage(`{}`)},
		{Type: "dismiss", Config: json.RawMessage(`{}`)}, // should not execute
	}

	id, err := runFromWorkflow(context.Background(), e, "test-workflow", "test-repo", steps)
	if err != nil {
		t.Fatal(err)
	}

	inst := waitForTerminal(t, store, "test-repo", id, 5*time.Second)

	if inst.Status != StatusFailed {
		t.Errorf("expected failed, got %s", inst.Status)
	}
	// Only spawn + wait results (dismiss should not have run).
	if len(inst.StepResults) != 2 {
		t.Errorf("expected 2 step results, got %d", len(inst.StepResults))
	}

	mu.Lock()
	defer mu.Unlock()
	if !notified {
		t.Error("expected notification callback to be called")
	}
	if notifiedStep != 1 {
		t.Errorf("expected notification for step 1, got %d", notifiedStep)
	}
}

func TestExecutor_ExecutionError(t *testing.T) {
	store, err := NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	registry := map[string]StepHandler{
		"spawn": errorHandler("spawn"),
	}

	e := NewExecutor(store, registry, "/tmp/test-repo")

	steps := []Step{
		{Type: "spawn", Config: json.RawMessage(`{}`)},
	}

	id, err := runFromWorkflow(context.Background(), e, "test-workflow", "test-repo", steps)
	if err != nil {
		t.Fatal(err)
	}

	inst := waitForTerminal(t, store, "test-repo", id, 5*time.Second)

	if inst.Status != StatusFailed {
		t.Errorf("expected failed, got %s", inst.Status)
	}
}

func TestExecutor_StepTimeout(t *testing.T) {
	store, err := NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	registry := map[string]StepHandler{
		"spawn": slowHandler("spawn"),
	}

	e := NewExecutor(store, registry, "/tmp/test-repo")

	steps := []Step{
		{Type: "spawn", Timeout: "50ms", Config: json.RawMessage(`{}`)},
	}

	id, err := runFromWorkflow(context.Background(), e, "test-workflow", "test-repo", steps)
	if err != nil {
		t.Fatal(err)
	}

	inst := waitForTerminal(t, store, "test-repo", id, 5*time.Second)

	if inst.Status != StatusFailed {
		t.Errorf("expected failed, got %s", inst.Status)
	}
}

func TestExecutor_Cancellation(t *testing.T) {
	store, err := NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var killCalled bool
	var mu sync.Mutex

	registry := map[string]StepHandler{
		"spawn": contextUpdatingHandler(),
		"wait":  slowHandler("wait"),
	}

	e := NewExecutor(store, registry, "/tmp/test-repo",
		WithCancelKillFn(func(ctx context.Context, agentName, repoName string) error {
			mu.Lock()
			killCalled = true
			mu.Unlock()
			return nil
		}),
	)

	steps := []Step{
		{Type: "spawn", Config: json.RawMessage(`{}`)},
		{Type: "wait", Config: json.RawMessage(`{}`)},
	}

	id, err := runFromWorkflow(context.Background(), e, "test-workflow", "test-repo", steps)
	if err != nil {
		t.Fatal(err)
	}

	// Give spawn time to complete and wait to start.
	time.Sleep(50 * time.Millisecond)

	if err := e.Cancel(context.Background(), id); err != nil {
		t.Fatal(err)
	}

	inst := waitForTerminal(t, store, "test-repo", id, 5*time.Second)

	if inst.Status != StatusCancelled && inst.Status != StatusFailed {
		t.Errorf("expected cancelled or failed, got %s", inst.Status)
	}

	mu.Lock()
	defer mu.Unlock()
	if !killCalled {
		t.Error("expected CancelKillFn to be called")
	}
}

func TestExecutor_CancelNotRunning(t *testing.T) {
	store, err := NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	e := NewExecutor(store, nil, "/tmp/test-repo")

	err = e.Cancel(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error cancelling non-running instance")
	}
}

func TestExecutor_UnknownStepType(t *testing.T) {
	store, err := NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	registry := map[string]StepHandler{
		"spawn": successHandler("spawn"),
	}

	e := NewExecutor(store, registry, "/tmp/test-repo")

	steps := []Step{
		{Type: "spawn", Config: json.RawMessage(`{}`)},
		{Type: "unknown_type", Config: json.RawMessage(`{}`)},
	}

	id, err := runFromWorkflow(context.Background(), e, "test-workflow", "test-repo", steps)
	if err != nil {
		t.Fatal(err)
	}

	inst := waitForTerminal(t, store, "test-repo", id, 5*time.Second)

	if inst.Status != StatusFailed {
		t.Errorf("expected failed, got %s", inst.Status)
	}
}

func TestExecutor_IsRunning(t *testing.T) {
	store, err := NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	registry := map[string]StepHandler{
		"wait": slowHandler("wait"),
	}

	e := NewExecutor(store, registry, "/tmp/test-repo")

	steps := []Step{
		{Type: "wait", Config: json.RawMessage(`{}`)},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	id, err := runFromWorkflow(ctx, e, "test-workflow", "test-repo", steps)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(20 * time.Millisecond)

	if !e.IsRunning(id) {
		t.Error("expected instance to be running")
	}

	cancel()

	// Wait for cleanup.
	time.Sleep(100 * time.Millisecond)

	if e.IsRunning(id) {
		t.Error("expected instance to not be running after cancel")
	}
}

func TestExecutor_Telemetry(t *testing.T) {
	store, err := NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var mu sync.Mutex
	var events []string

	registry := map[string]StepHandler{
		"spawn": successHandler("spawn"),
	}

	e := NewExecutor(store, registry, "/tmp/test-repo",
		WithTelemetry(func(event string, data map[string]string) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		}),
	)

	steps := []Step{
		{Type: "spawn", Config: json.RawMessage(`{}`)},
	}

	id, err := runFromWorkflow(context.Background(), e, "test-workflow", "test-repo", steps)
	if err != nil {
		t.Fatal(err)
	}

	waitForTerminal(t, store, "test-repo", id, 5*time.Second)

	mu.Lock()
	defer mu.Unlock()

	// Should have at least: step_start, step_end, workflow_complete.
	if len(events) < 3 {
		t.Errorf("expected at least 3 telemetry events, got %d: %v", len(events), events)
	}
}

func TestExecutor_RecoverOnStartup_AgentAlive(t *testing.T) {
	store, err := NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Create a "running" instance in the store (simulating a crash).
	inst := &Instance{
		ID:           "wf-recovery-test",
		WorkflowName: "nonexistent-workflow", // Will fail on re-parse, which is fine
		RepoName:     "test-repo",
		RepoRoot:     "/tmp/test-repo",
		Status:       StatusRunning,
		CurrentStep:  1,
		Params:       map[string]string{},
		Context:      WorkflowContext{ActiveAgent: "crashed-agent", ActiveRepo: "test-repo"},
		StepResults:  []StepResult{},
		StartedAt:    time.Now().UTC(),
	}
	if err := store.CreateInstance(inst); err != nil {
		t.Fatal(err)
	}

	liveness := &mockLivenessChecker{alive: true}
	e := NewExecutor(store, nil, "/tmp/test-repo",
		WithLivenessChecker(liveness),
	)

	// RecoverOnStartup will try to re-parse the workflow, which will fail.
	// But it shouldn't panic — the error is logged via telemetry.
	err = e.RecoverOnStartup(context.Background())
	if err != nil {
		t.Fatalf("RecoverOnStartup should not return error: %v", err)
	}

	// Instance should be failed (because workflow re-parse fails).
	recovered, err := store.GetInstance("test-repo", "wf-recovery-test")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != StatusFailed {
		t.Errorf("expected failed (re-parse failure), got %s", recovered.Status)
	}
}

func TestExecutor_RecoverOnStartup_AgentDead(t *testing.T) {
	store, err := NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	inst := &Instance{
		ID:           "wf-dead-agent",
		WorkflowName: "test-workflow",
		RepoName:     "test-repo",
		RepoRoot:     "/tmp/test-repo",
		Status:       StatusRunning,
		CurrentStep:  1,
		Params:       map[string]string{},
		Context:      WorkflowContext{ActiveAgent: "dead-agent", ActiveRepo: "test-repo"},
		StepResults:  []StepResult{},
		StartedAt:    time.Now().UTC(),
	}
	if err := store.CreateInstance(inst); err != nil {
		t.Fatal(err)
	}

	liveness := &mockLivenessChecker{alive: false}
	e := NewExecutor(store, nil, "/tmp/test-repo",
		WithLivenessChecker(liveness),
	)

	err = e.RecoverOnStartup(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	recovered, err := store.GetInstance("test-repo", "wf-dead-agent")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != StatusFailed {
		t.Errorf("expected failed, got %s", recovered.Status)
	}
	if recovered.Error == "" {
		t.Error("expected error message for dead agent")
	}
}

func TestValidateParams(t *testing.T) {
	tests := []struct {
		name     string
		declared map[string]Param
		provided map[string]string
		wantErr  string
		wantKeys []string
	}{
		{
			name: "all provided",
			declared: map[string]Param{
				"repo": {Type: "string", Required: true},
			},
			provided: map[string]string{"repo": "myrepo"},
			wantKeys: []string{"repo"},
		},
		{
			name: "default applied",
			declared: map[string]Param{
				"branch": {Type: "string", Default: "main"},
			},
			provided: map[string]string{},
			wantKeys: []string{"branch"},
		},
		{
			name: "required missing",
			declared: map[string]Param{
				"repo": {Type: "string", Required: true},
			},
			provided: map[string]string{},
			wantErr:  "required parameter",
		},
		{
			name: "unknown param",
			declared: map[string]Param{
				"repo": {Type: "string", Required: true},
			},
			provided: map[string]string{"repo": "myrepo", "extra": "bad"},
			wantErr:  "unknown parameter",
		},
		{
			name:     "empty declarations",
			declared: map[string]Param{},
			provided: map[string]string{},
			wantKeys: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := validateParams(tt.declared, tt.provided)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, k := range tt.wantKeys {
				if _, ok := result[k]; !ok {
					t.Errorf("expected key %q in result", k)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
