// Integration tests for the workflow engine. Tests exercise real step handlers
// (spawn, wait, evaluate, dismiss, gate) with mock callbacks, verifying the full
// execution lifecycle including state persistence, crash recovery, cancellation,
// timeouts, and tuplespace interactions.
package workflow_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chazu/procyon-park/internal/tuplestore"
	"github.com/chazu/procyon-park/internal/workflow"
	"github.com/chazu/procyon-park/internal/workflow/steps"
)

// --- Test helpers ---

type testSpawnTracker struct {
	mu      sync.Mutex
	calls   []workflow.SpawnConfig
	results *steps.SpawnResult
	err     error
}

func (t *testSpawnTracker) spawnFn(ctx context.Context, cfg workflow.SpawnConfig, inst *workflow.Instance) (*steps.SpawnResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, cfg)
	if t.err != nil {
		return nil, t.err
	}
	if t.results != nil {
		return t.results, nil
	}
	return &steps.SpawnResult{
		AgentName: "test-agent",
		Branch:    "agent/test-agent/branch",
		Repo:      inst.RepoName,
		TaskID:    "task-001",
	}, nil
}

func (t *testSpawnTracker) getCalls() []workflow.SpawnConfig {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := make([]workflow.SpawnConfig, len(t.calls))
	copy(cp, t.calls)
	return cp
}

type testKillTracker struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (t *testKillTracker) killFn(ctx context.Context, agentName, repoName string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, agentName)
	return t.err
}

func (t *testKillTracker) getCalls() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := make([]string, len(t.calls))
	copy(cp, t.calls)
	return cp
}

type testTupleFinder struct {
	store *tuplestore.TupleStore
}

func (f *testTupleFinder) FindTaskDoneEvent(agentName string) (json.RawMessage, error) {
	cat := "event"
	identity := "task_done"
	tuples, err := f.store.FindAll(&cat, nil, &identity, nil, nil)
	if err != nil {
		return nil, err
	}
	for _, t := range tuples {
		payload, ok := t["payload"].(string)
		if !ok {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			continue
		}
		if data["agent"] == agentName {
			return json.RawMessage(payload), nil
		}
	}
	return nil, nil
}

type testWorkTracker struct{}

func (t *testWorkTracker) CreateTask(title, description, taskType string) (string, error) {
	return "task-001", nil
}
func (t *testWorkTracker) DeleteTask(taskID string) error { return nil }

type mockLiveness struct {
	alive bool
}

func (m *mockLiveness) IsAlive(agentName, repoName string) (bool, error) {
	return m.alive, nil
}

type mockNotifier struct {
	mu        sync.Mutex
	instances []*workflow.Instance
}

func (m *mockNotifier) OnComplete(inst *workflow.Instance) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instances = append(m.instances, inst)
}

func (m *mockNotifier) getInstances() []*workflow.Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*workflow.Instance, len(m.instances))
	copy(cp, m.instances)
	return cp
}

func newIntegrationDeps(t *testing.T) (
	wfStore *workflow.Store,
	ts *tuplestore.TupleStore,
	spawnTracker *testSpawnTracker,
	killTracker *testKillTracker,
	registry map[string]workflow.StepHandler,
) {
	t.Helper()

	var err error
	wfStore, err = workflow.NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { wfStore.Close() })

	ts, err = tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ts.Close() })

	spawnTracker = &testSpawnTracker{}
	killTracker = &testKillTracker{}
	finder := &testTupleFinder{store: ts}

	registry = map[string]workflow.StepHandler{
		"spawn": &steps.SpawnHandler{
			Spawn:       spawnTracker.spawnFn,
			WorkTracker: &testWorkTracker{},
		},
		"wait": &steps.WaitHandler{
			Finder:       finder,
			PollInterval: 10 * time.Millisecond,
		},
		"evaluate": &steps.EvaluateHandler{},
		"dismiss": &steps.DismissHandler{
			Kill: killTracker.killFn,
		},
		"gate": &steps.GateHandler{
			Store:        wfStore,
			Tuples:       ts,
			PollInterval: 10 * time.Millisecond,
		},
	}
	return
}

func writeTaskDoneEvent(t *testing.T, store *tuplestore.TupleStore, agentName, taskID string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]interface{}{
		"task":   taskID,
		"agent":  agentName,
		"branch": "agent/" + agentName + "/branch",
	})
	_, err := store.Insert("event", "test-repo", "task_done", "local", string(payload), "session", nil, nil, nil)
	if err != nil {
		t.Fatalf("write task_done event: %v", err)
	}
}

func writeGateResponseTuple(t *testing.T, store *tuplestore.TupleStore, repoName, instanceID string, stepIndex int, decision string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{
		"decision": decision,
		"approver": "test-user",
	})
	identity := fmt.Sprintf("%s-%d", instanceID, stepIndex)
	_, err := store.Insert("gate_response", repoName, identity, "local", string(payload), "session", nil, nil, nil)
	if err != nil {
		t.Fatalf("write gate_response: %v", err)
	}
}

func mustWaitForTerminal(t *testing.T, store *workflow.Store, repoName, instanceID string, timeout time.Duration) *workflow.Instance {
	t.Helper()
	inst, err := workflow.ExportWaitForTerminal(store, repoName, instanceID, timeout)
	if err != nil {
		t.Fatalf("timed out waiting for instance %s: %v", instanceID, err)
	}
	return inst
}

// --- Integration Tests ---

// TestIntegration_FullSpawnWaitDismiss runs spawn→wait→dismiss with real step
// handlers, verifying instance state transitions and step results.
func TestIntegration_FullSpawnWaitDismiss(t *testing.T) {
	wfStore, ts, spawnTracker, killTracker, registry := newIntegrationDeps(t)

	notifier := &mockNotifier{}
	e := workflow.NewExecutor(wfStore, registry, "/tmp/test-repo",
		workflow.WithCompletionNotifier(notifier),
	)

	stps := []workflow.Step{
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"Do the thing","description":"test task"}}`)},
		{Type: "wait", Config: json.RawMessage(`{"timeout":"5s"}`)},
		{Type: "dismiss", Config: json.RawMessage(`{}`)},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, err := workflow.ExportRunFromWorkflow(ctx, e, "integration-test", "test-repo", stps)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
	writeTaskDoneEvent(t, ts, "test-agent", "task-001")

	inst := mustWaitForTerminal(t, wfStore, "test-repo", id, 5*time.Second)

	if inst.Status != workflow.StatusCompleted {
		t.Errorf("expected completed, got %s (error: %s)", inst.Status, inst.Error)
	}
	if len(inst.StepResults) != 3 {
		t.Fatalf("expected 3 step results, got %d", len(inst.StepResults))
	}

	expectedTypes := []string{"spawn", "wait", "dismiss"}
	for i, sr := range inst.StepResults {
		if sr.StepType != expectedTypes[i] {
			t.Errorf("step %d: expected type %s, got %s", i, expectedTypes[i], sr.StepType)
		}
		if sr.Status != "completed" {
			t.Errorf("step %d: expected completed, got %s (error: %s)", i, sr.Status, sr.Error)
		}
	}

	calls := spawnTracker.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 spawn call, got %d", len(calls))
	}
	if calls[0].Role != "cub" {
		t.Errorf("expected role 'cub', got %q", calls[0].Role)
	}
	if calls[0].Task.Title != "Do the thing" {
		t.Errorf("expected title 'Do the thing', got %q", calls[0].Task.Title)
	}

	killCalls := killTracker.getCalls()
	if len(killCalls) != 1 {
		t.Fatalf("expected 1 kill call, got %d", len(killCalls))
	}
	if killCalls[0] != "test-agent" {
		t.Errorf("expected kill for 'test-agent', got %q", killCalls[0])
	}

	if inst.Context.ActiveAgent != "" {
		t.Errorf("expected activeAgent cleared after dismiss, got %q", inst.Context.ActiveAgent)
	}
	if inst.CompletedAt == nil {
		t.Error("expected CompletedAt to be set")
	}

	completed := notifier.getInstances()
	if len(completed) != 1 {
		t.Errorf("expected 1 completion notification, got %d", len(completed))
	}
}

// TestIntegration_SpawnWaitEvaluateDismiss runs the full 4-step sequence verifying
// that evaluate validates wait output via CUE subsumption.
func TestIntegration_SpawnWaitEvaluateDismiss(t *testing.T) {
	wfStore, ts, _, _, registry := newIntegrationDeps(t)

	e := workflow.NewExecutor(wfStore, registry, "/tmp/test-repo")

	stps := []workflow.Step{
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"build it"}}`)},
		{Type: "wait", Config: json.RawMessage(`{"timeout":"5s"}`)},
		{Type: "evaluate", Config: json.RawMessage(`{"expect":{"agentName": "test-agent"}}`)},
		{Type: "dismiss", Config: json.RawMessage(`{}`)},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, err := workflow.ExportRunFromWorkflow(ctx, e, "eval-test", "test-repo", stps)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
	writeTaskDoneEvent(t, ts, "test-agent", "task-001")

	inst := mustWaitForTerminal(t, wfStore, "test-repo", id, 5*time.Second)

	if inst.Status != workflow.StatusCompleted {
		t.Errorf("expected completed, got %s (error: %s)", inst.Status, inst.Error)
	}
	if len(inst.StepResults) != 4 {
		t.Fatalf("expected 4 step results, got %d", len(inst.StepResults))
	}

	evalResult := inst.StepResults[2]
	if evalResult.StepType != "evaluate" || evalResult.Status != "completed" {
		t.Errorf("evaluate: type=%s status=%s error=%s", evalResult.StepType, evalResult.Status, evalResult.Error)
	}
}

// TestIntegration_EvaluateFailure verifies evaluate fails when wait output doesn't
// match the expected schema.
func TestIntegration_EvaluateFailure(t *testing.T) {
	wfStore, ts, _, _, registry := newIntegrationDeps(t)

	e := workflow.NewExecutor(wfStore, registry, "/tmp/test-repo")

	stps := []workflow.Step{
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"build"}}`)},
		{Type: "wait", Config: json.RawMessage(`{"timeout":"5s"}`)},
		{Type: "evaluate", Config: json.RawMessage(`{"expect":{"nonexistentField": "required"}}`)},
		{Type: "dismiss", Config: json.RawMessage(`{}`)},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, err := workflow.ExportRunFromWorkflow(ctx, e, "eval-fail-test", "test-repo", stps)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
	writeTaskDoneEvent(t, ts, "test-agent", "task-001")

	inst := mustWaitForTerminal(t, wfStore, "test-repo", id, 5*time.Second)

	if inst.Status != workflow.StatusFailed {
		t.Errorf("expected failed, got %s", inst.Status)
	}
	if len(inst.StepResults) > 3 {
		t.Errorf("expected at most 3 step results, got %d", len(inst.StepResults))
	}
}

// TestIntegration_HumanGateApprove runs a workflow with a human gate that gets
// approved via tuplespace tuples.
func TestIntegration_HumanGateApprove(t *testing.T) {
	wfStore, ts, _, _, registry := newIntegrationDeps(t)

	e := workflow.NewExecutor(wfStore, registry, "/tmp/test-repo")

	stps := []workflow.Step{
		{Type: "gate", Config: json.RawMessage(`{"gateType":"human","approvers":["alice"],"prompt":"Deploy?","timeout":"5s"}`)},
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"deploy"}}`)},
		{Type: "wait", Config: json.RawMessage(`{"timeout":"5s"}`)},
		{Type: "dismiss", Config: json.RawMessage(`{}`)},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, err := workflow.ExportRunFromWorkflow(ctx, e, "gate-test", "test-repo", stps)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
	writeGateResponseTuple(t, ts, "test-repo", id, 0, "approved")

	time.Sleep(200 * time.Millisecond)
	writeTaskDoneEvent(t, ts, "test-agent", "task-001")

	inst := mustWaitForTerminal(t, wfStore, "test-repo", id, 5*time.Second)

	if inst.Status != workflow.StatusCompleted {
		t.Errorf("expected completed, got %s (error: %s)", inst.Status, inst.Error)
	}
	if len(inst.StepResults) != 4 {
		t.Fatalf("expected 4 step results, got %d", len(inst.StepResults))
	}

	gateResult := inst.StepResults[0]
	if gateResult.StepType != "gate" || gateResult.Status != "completed" {
		t.Errorf("gate: type=%s status=%s error=%s", gateResult.StepType, gateResult.Status, gateResult.Error)
	}
}

// TestIntegration_HumanGateReject runs a workflow with a human gate that gets
// rejected, verifying the workflow fails.
func TestIntegration_HumanGateReject(t *testing.T) {
	wfStore, ts, _, _, registry := newIntegrationDeps(t)

	e := workflow.NewExecutor(wfStore, registry, "/tmp/test-repo")

	stps := []workflow.Step{
		{Type: "gate", Config: json.RawMessage(`{"gateType":"human","approvers":["bob"],"timeout":"5s"}`)},
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"deploy"}}`)},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, err := workflow.ExportRunFromWorkflow(ctx, e, "gate-reject-test", "test-repo", stps)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
	writeGateResponseTuple(t, ts, "test-repo", id, 0, "rejected")

	inst := mustWaitForTerminal(t, wfStore, "test-repo", id, 5*time.Second)

	if inst.Status != workflow.StatusFailed {
		t.Errorf("expected failed, got %s", inst.Status)
	}
	if len(inst.StepResults) != 1 {
		t.Errorf("expected 1 step result (only gate), got %d", len(inst.StepResults))
	}
}

// TestIntegration_TimerGate runs a workflow with a short timer gate.
func TestIntegration_TimerGate(t *testing.T) {
	wfStore, ts, _, _, registry := newIntegrationDeps(t)

	e := workflow.NewExecutor(wfStore, registry, "/tmp/test-repo")

	stps := []workflow.Step{
		{Type: "gate", Config: json.RawMessage(`{"gateType":"timer","duration":"50ms"}`)},
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"post-timer"}}`)},
		{Type: "wait", Config: json.RawMessage(`{"timeout":"5s"}`)},
		{Type: "dismiss", Config: json.RawMessage(`{}`)},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, err := workflow.ExportRunFromWorkflow(ctx, e, "timer-gate-test", "test-repo", stps)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)
	writeTaskDoneEvent(t, ts, "test-agent", "task-001")

	inst := mustWaitForTerminal(t, wfStore, "test-repo", id, 5*time.Second)

	if inst.Status != workflow.StatusCompleted {
		t.Errorf("expected completed, got %s (error: %s)", inst.Status, inst.Error)
	}
	if len(inst.StepResults) != 4 {
		t.Fatalf("expected 4 step results, got %d", len(inst.StepResults))
	}
	if inst.StepResults[0].StepType != "gate" || inst.StepResults[0].Status != "completed" {
		t.Errorf("gate: type=%s status=%s error=%s",
			inst.StepResults[0].StepType, inst.StepResults[0].Status, inst.StepResults[0].Error)
	}
}

// TestIntegration_Cancellation starts a workflow, cancels during wait, verifies cleanup.
func TestIntegration_Cancellation(t *testing.T) {
	wfStore, _, spawnTracker, killTracker, registry := newIntegrationDeps(t)

	var cancelKillCalled bool
	var cancelKillAgent string
	var mu sync.Mutex

	e := workflow.NewExecutor(wfStore, registry, "/tmp/test-repo",
		workflow.WithCancelKillFn(func(ctx context.Context, agentName, repoName string) error {
			mu.Lock()
			cancelKillCalled = true
			cancelKillAgent = agentName
			mu.Unlock()
			return nil
		}),
	)

	stps := []workflow.Step{
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"long task"}}`)},
		{Type: "wait", Config: json.RawMessage(`{"timeout":"30s"}`)},
		{Type: "dismiss", Config: json.RawMessage(`{}`)},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, err := workflow.ExportRunFromWorkflow(ctx, e, "cancel-test", "test-repo", stps)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
	if len(spawnTracker.getCalls()) != 1 {
		t.Fatalf("expected spawn to have been called")
	}

	if err := e.Cancel(context.Background(), id); err != nil {
		t.Fatal(err)
	}

	inst := mustWaitForTerminal(t, wfStore, "test-repo", id, 5*time.Second)

	if inst.Status != workflow.StatusCancelled && inst.Status != workflow.StatusFailed {
		t.Errorf("expected cancelled or failed, got %s", inst.Status)
	}

	mu.Lock()
	defer mu.Unlock()
	if !cancelKillCalled {
		t.Error("expected CancelKillFn to be called")
	}
	if cancelKillAgent != "test-agent" {
		t.Errorf("expected cancel kill for 'test-agent', got %q", cancelKillAgent)
	}

	if len(killTracker.getCalls()) > 0 {
		t.Error("dismiss handler should not have been called during cancellation")
	}
}

// TestIntegration_StepTimeout verifies per-step timeout causes workflow failure.
func TestIntegration_StepTimeout(t *testing.T) {
	wfStore, _, _, _, registry := newIntegrationDeps(t)

	e := workflow.NewExecutor(wfStore, registry, "/tmp/test-repo")

	stps := []workflow.Step{
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"timeout test"}}`)},
		{Type: "wait", Timeout: "100ms", Config: json.RawMessage(`{"timeout":"30s"}`)},
		{Type: "dismiss", Config: json.RawMessage(`{}`)},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, err := workflow.ExportRunFromWorkflow(ctx, e, "timeout-test", "test-repo", stps)
	if err != nil {
		t.Fatal(err)
	}

	inst := mustWaitForTerminal(t, wfStore, "test-repo", id, 5*time.Second)

	if inst.Status != workflow.StatusFailed {
		t.Errorf("expected failed, got %s (error: %s)", inst.Status, inst.Error)
	}
}

// TestIntegration_WaitTimeout verifies wait handler timeout when no event arrives.
func TestIntegration_WaitTimeout(t *testing.T) {
	wfStore, _, _, _, registry := newIntegrationDeps(t)

	e := workflow.NewExecutor(wfStore, registry, "/tmp/test-repo")

	stps := []workflow.Step{
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"wait-timeout"}}`)},
		{Type: "wait", Config: json.RawMessage(`{"timeout":"100ms"}`)},
		{Type: "dismiss", Config: json.RawMessage(`{}`)},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, err := workflow.ExportRunFromWorkflow(ctx, e, "wait-timeout-test", "test-repo", stps)
	if err != nil {
		t.Fatal(err)
	}

	inst := mustWaitForTerminal(t, wfStore, "test-repo", id, 5*time.Second)

	if inst.Status != workflow.StatusFailed {
		t.Errorf("expected failed, got %s", inst.Status)
	}
	if len(inst.StepResults) >= 2 {
		if inst.StepResults[1].StepType != "wait" || inst.StepResults[1].Status != "failed" {
			t.Errorf("wait step: type=%s status=%s", inst.StepResults[1].StepType, inst.StepResults[1].Status)
		}
	}
}

// TestIntegration_CrashRecovery_AgentDead simulates daemon restart with dead agent.
func TestIntegration_CrashRecovery_AgentDead(t *testing.T) {
	wfStore, _, _, _, _ := newIntegrationDeps(t)

	now := time.Now().UTC()
	inst := &workflow.Instance{
		ID:           "wf-crash-dead",
		WorkflowName: "crash-recovery-test",
		RepoName:     "test-repo",
		RepoRoot:     "/tmp/test-repo",
		Status:       workflow.StatusRunning,
		CurrentStep:  1,
		Params:       map[string]string{},
		Context: workflow.WorkflowContext{
			ActiveAgent: "dead-agent",
			ActiveRepo:  "test-repo",
		},
		StepResults: []workflow.StepResult{},
		StartedAt:   now,
	}
	if err := wfStore.CreateInstance(inst); err != nil {
		t.Fatal(err)
	}

	liveness := &mockLiveness{alive: false}
	e := workflow.NewExecutor(wfStore, nil, "/tmp/test-repo",
		workflow.WithLivenessChecker(liveness),
	)

	if err := e.RecoverOnStartup(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	recovered, err := wfStore.GetInstance("test-repo", "wf-crash-dead")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != workflow.StatusFailed {
		t.Errorf("expected failed, got %s", recovered.Status)
	}
	if recovered.Error == "" {
		t.Error("expected error message for dead agent")
	}
}

// TestIntegration_CrashRecovery_AgentAlive simulates daemon restart with live agent.
func TestIntegration_CrashRecovery_AgentAlive(t *testing.T) {
	wfStore, _, _, _, _ := newIntegrationDeps(t)

	now := time.Now().UTC()
	inst := &workflow.Instance{
		ID:           "wf-crash-alive",
		WorkflowName: "nonexistent-workflow",
		RepoName:     "test-repo",
		RepoRoot:     "/tmp/test-repo",
		Status:       workflow.StatusRunning,
		CurrentStep:  1,
		Params:       map[string]string{},
		Context: workflow.WorkflowContext{
			ActiveAgent: "surviving-agent",
			ActiveRepo:  "test-repo",
		},
		StepResults: []workflow.StepResult{
			{StepIndex: 0, StepType: "spawn", Status: "completed", StartedAt: now, EndedAt: &now},
		},
		StartedAt: now,
	}
	if err := wfStore.CreateInstance(inst); err != nil {
		t.Fatal(err)
	}

	liveness := &mockLiveness{alive: true}
	e := workflow.NewExecutor(wfStore, nil, "/tmp/test-repo",
		workflow.WithLivenessChecker(liveness),
	)

	err := e.RecoverOnStartup(context.Background())
	if err != nil {
		t.Fatalf("RecoverOnStartup should not return error: %v", err)
	}

	recovered, err := wfStore.GetInstance("test-repo", "wf-crash-alive")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != workflow.StatusFailed {
		t.Errorf("expected failed (re-parse failure), got %s", recovered.Status)
	}
}

// TestIntegration_GateStatePersistence verifies gate state survives a simulated crash.
func TestIntegration_GateStatePersistence(t *testing.T) {
	wfStore, ts, _, _, _ := newIntegrationDeps(t)

	gs := &workflow.GateState{
		InstanceID: "wf-gate-persist",
		StepIndex:  0,
		StartedAt:  time.Now().UTC().Add(-time.Minute),
		State:      "approved",
		PromptSent: true,
	}
	if err := wfStore.SaveGateState(gs); err != nil {
		t.Fatal(err)
	}

	gateHandler := &steps.GateHandler{
		Store:        wfStore,
		Tuples:       ts,
		PollInterval: 10 * time.Millisecond,
	}

	inst := &workflow.Instance{
		ID:           "wf-gate-persist",
		WorkflowName: "gate-persist-test",
		RepoName:     "test-repo",
		Status:       workflow.StatusRunning,
		CurrentStep:  0,
		Params:       map[string]string{},
		Context:      workflow.WorkflowContext{},
		StepResults:  []workflow.StepResult{},
		StartedAt:    time.Now().UTC(),
	}

	config := json.RawMessage(`{"gateType":"human","approvers":["alice"],"timeout":"5s"}`)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := gateHandler.Execute(ctx, inst, 0, config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" {
		t.Errorf("expected completed (from persisted approval), got %s (error: %s)", result.Status, result.Error)
	}
}

// TestIntegration_SpawnFailure verifies spawn failure fails the workflow.
func TestIntegration_SpawnFailure(t *testing.T) {
	wfStore, _, _, _, registry := newIntegrationDeps(t)

	failTracker := &testSpawnTracker{err: fmt.Errorf("agent pool exhausted")}
	registry["spawn"] = &steps.SpawnHandler{
		Spawn:       failTracker.spawnFn,
		WorkTracker: &testWorkTracker{},
	}

	e := workflow.NewExecutor(wfStore, registry, "/tmp/test-repo")

	stps := []workflow.Step{
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"fail spawn"}}`)},
		{Type: "wait", Config: json.RawMessage(`{"timeout":"5s"}`)},
		{Type: "dismiss", Config: json.RawMessage(`{}`)},
	}

	id, err := workflow.ExportRunFromWorkflow(context.Background(), e, "spawn-fail-test", "test-repo", stps)
	if err != nil {
		t.Fatal(err)
	}

	inst := mustWaitForTerminal(t, wfStore, "test-repo", id, 5*time.Second)

	if inst.Status != workflow.StatusFailed {
		t.Errorf("expected failed, got %s", inst.Status)
	}
	if len(inst.StepResults) != 1 {
		t.Errorf("expected 1 step result, got %d", len(inst.StepResults))
	}
	if inst.StepResults[0].Status != "failed" {
		t.Errorf("spawn result: expected failed, got %s", inst.StepResults[0].Status)
	}
}

// TestIntegration_HumanGateTimeout verifies human gate timeout.
func TestIntegration_HumanGateTimeout(t *testing.T) {
	wfStore, _, _, _, registry := newIntegrationDeps(t)

	e := workflow.NewExecutor(wfStore, registry, "/tmp/test-repo")

	stps := []workflow.Step{
		{Type: "gate", Config: json.RawMessage(`{"gateType":"human","approvers":["alice"],"timeout":"50ms"}`)},
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"gated"}}`)},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, err := workflow.ExportRunFromWorkflow(ctx, e, "gate-timeout-test", "test-repo", stps)
	if err != nil {
		t.Fatal(err)
	}

	inst := mustWaitForTerminal(t, wfStore, "test-repo", id, 5*time.Second)

	if inst.Status != workflow.StatusFailed {
		t.Errorf("expected failed (gate timeout), got %s", inst.Status)
	}
	if len(inst.StepResults) < 1 {
		t.Fatal("expected at least 1 step result")
	}
	if inst.StepResults[0].Status != "failed" {
		t.Errorf("gate: expected failed, got %s", inst.StepResults[0].Status)
	}
}

// TestIntegration_TelemetryEvents verifies telemetry hooks fire for step transitions.
func TestIntegration_TelemetryEvents(t *testing.T) {
	wfStore, ts, _, _, registry := newIntegrationDeps(t)

	var mu sync.Mutex
	var events []string

	e := workflow.NewExecutor(wfStore, registry, "/tmp/test-repo",
		workflow.WithTelemetry(func(event string, data map[string]string) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		}),
	)

	stps := []workflow.Step{
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"telemetry"}}`)},
		{Type: "wait", Config: json.RawMessage(`{"timeout":"5s"}`)},
		{Type: "dismiss", Config: json.RawMessage(`{}`)},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, err := workflow.ExportRunFromWorkflow(ctx, e, "telemetry-test", "test-repo", stps)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
	writeTaskDoneEvent(t, ts, "test-agent", "task-001")

	mustWaitForTerminal(t, wfStore, "test-repo", id, 5*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(events) < 7 {
		t.Errorf("expected at least 7 telemetry events, got %d: %v", len(events), events)
	}
}

// TestIntegration_CompletionNotifierBothPaths verifies completion callback fires
// for both success and failure.
func TestIntegration_CompletionNotifierBothPaths(t *testing.T) {
	wfStore, ts, _, _, registry := newIntegrationDeps(t)

	notifier := &mockNotifier{}
	e := workflow.NewExecutor(wfStore, registry, "/tmp/test-repo",
		workflow.WithCompletionNotifier(notifier),
	)

	stps := []workflow.Step{
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"success"}}`)},
		{Type: "wait", Config: json.RawMessage(`{"timeout":"5s"}`)},
		{Type: "dismiss", Config: json.RawMessage(`{}`)},
	}

	id1, err := workflow.ExportRunFromWorkflow(context.Background(), e, "notify-success", "test-repo", stps)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
	writeTaskDoneEvent(t, ts, "test-agent", "task-001")
	mustWaitForTerminal(t, wfStore, "test-repo", id1, 5*time.Second)

	// Run a failing workflow.
	failTracker := &testSpawnTracker{err: fmt.Errorf("fail on purpose")}
	registry2 := map[string]workflow.StepHandler{
		"spawn": &steps.SpawnHandler{Spawn: failTracker.spawnFn, WorkTracker: &testWorkTracker{}},
	}
	e2 := workflow.NewExecutor(wfStore, registry2, "/tmp/test-repo",
		workflow.WithCompletionNotifier(notifier),
	)

	failStps := []workflow.Step{
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"fail"}}`)},
	}

	id2, err := workflow.ExportRunFromWorkflow(context.Background(), e2, "notify-fail", "test-repo", failStps)
	if err != nil {
		t.Fatal(err)
	}
	mustWaitForTerminal(t, wfStore, "test-repo", id2, 5*time.Second)

	completed := notifier.getInstances()
	if len(completed) != 2 {
		t.Errorf("expected 2 completion notifications, got %d", len(completed))
	}
}

// TestIntegration_IsRunning verifies IsRunning during and after execution.
func TestIntegration_IsRunning(t *testing.T) {
	wfStore, ts, _, _, registry := newIntegrationDeps(t)

	e := workflow.NewExecutor(wfStore, registry, "/tmp/test-repo")

	stps := []workflow.Step{
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"running check"}}`)},
		{Type: "wait", Config: json.RawMessage(`{"timeout":"5s"}`)},
		{Type: "dismiss", Config: json.RawMessage(`{}`)},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, err := workflow.ExportRunFromWorkflow(ctx, e, "running-test", "test-repo", stps)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
	if !e.IsRunning(id) {
		t.Error("expected instance to be running during wait")
	}

	writeTaskDoneEvent(t, ts, "test-agent", "task-001")
	mustWaitForTerminal(t, wfStore, "test-repo", id, 5*time.Second)

	time.Sleep(50 * time.Millisecond)
	if e.IsRunning(id) {
		t.Error("expected instance to not be running after completion")
	}
}

// TestIntegration_ListInstances verifies listing and filtering instances.
func TestIntegration_ListInstances(t *testing.T) {
	wfStore, ts, _, _, registry := newIntegrationDeps(t)

	e := workflow.NewExecutor(wfStore, registry, "/tmp/test-repo")

	stps := []workflow.Step{
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"list test"}}`)},
		{Type: "wait", Config: json.RawMessage(`{"timeout":"5s"}`)},
		{Type: "dismiss", Config: json.RawMessage(`{}`)},
	}

	id, err := workflow.ExportRunFromWorkflow(context.Background(), e, "list-test", "test-repo", stps)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
	writeTaskDoneEvent(t, ts, "test-agent", "task-001")
	mustWaitForTerminal(t, wfStore, "test-repo", id, 5*time.Second)

	instances, err := e.ListInstances(workflow.StatusCompleted, "test-repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 {
		t.Fatalf("expected 1 completed instance, got %d", len(instances))
	}

	running, err := e.ListInstances(workflow.StatusRunning, "test-repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 0 {
		t.Errorf("expected 0 running instances, got %d", len(running))
	}
}
