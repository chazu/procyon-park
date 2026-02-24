package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/chazu/procyon-park/internal/workflow"
)

// newTestWorkflowExecutor creates a workflow executor with a memory store
// and a mock step handler registry for testing.
func newTestWorkflowExecutor(t *testing.T) (*workflow.Executor, *workflow.Store) {
	t.Helper()
	store, err := workflow.NewMemoryStore()
	if err != nil {
		t.Fatalf("create workflow store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	registry := map[string]workflow.StepHandler{
		"spawn":   &immediateHandler{status: "completed"},
		"wait":    &immediateHandler{status: "completed"},
		"dismiss": &immediateHandler{status: "completed"},
	}
	executor := workflow.NewExecutor(store, registry, t.TempDir())
	return executor, store
}

// immediateHandler is a StepHandler that returns immediately with a fixed status.
type immediateHandler struct {
	status string
}

func (h *immediateHandler) Execute(ctx context.Context, inst *workflow.Instance, stepIndex int, config json.RawMessage) (*workflow.StepResult, error) {
	now := time.Now().UTC()
	return &workflow.StepResult{
		StepIndex: stepIndex,
		StepType:  "mock",
		Status:    h.status,
		StartedAt: now,
		EndedAt:   &now,
	}, nil
}

func TestWorkflowListEmpty(t *testing.T) {
	tupleStore := newTestStore(t)
	executor, _ := newTestWorkflowExecutor(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	params, _ := json.Marshal(map[string]string{})
	result, err := srv.handlers["workflow.list"](params)
	if err != nil {
		t.Fatalf("workflow.list: %v", err)
	}

	instances, ok := result.([]map[string]interface{})
	if !ok {
		t.Fatalf("expected []map, got %T", result)
	}
	if len(instances) != 0 {
		t.Fatalf("expected 0 instances, got %d", len(instances))
	}
}

func TestWorkflowShowNotFound(t *testing.T) {
	tupleStore := newTestStore(t)
	executor, _ := newTestWorkflowExecutor(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	params, _ := json.Marshal(map[string]interface{}{
		"instance_id": "wf-nonexistent",
	})
	_, err := srv.handlers["workflow.show"](params)
	if err == nil {
		t.Fatal("expected error for nonexistent instance")
	}
}

func TestWorkflowShowMissingID(t *testing.T) {
	tupleStore := newTestStore(t)
	executor, _ := newTestWorkflowExecutor(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	params, _ := json.Marshal(map[string]interface{}{})
	_, err := srv.handlers["workflow.show"](params)
	if err == nil {
		t.Fatal("expected error for missing instance_id")
	}
}

func TestWorkflowCancelNotRunning(t *testing.T) {
	tupleStore := newTestStore(t)
	executor, _ := newTestWorkflowExecutor(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	params, _ := json.Marshal(map[string]string{
		"instance_id": "wf-nonexistent",
	})
	_, err := srv.handlers["workflow.cancel"](params)
	if err == nil {
		t.Fatal("expected error for cancelling nonexistent instance")
	}
}

func TestWorkflowCancelMissingID(t *testing.T) {
	tupleStore := newTestStore(t)
	executor, _ := newTestWorkflowExecutor(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	params, _ := json.Marshal(map[string]interface{}{})
	_, err := srv.handlers["workflow.cancel"](params)
	if err == nil {
		t.Fatal("expected error for missing instance_id")
	}
}

func TestWorkflowRunMissingName(t *testing.T) {
	tupleStore := newTestStore(t)
	executor, _ := newTestWorkflowExecutor(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	params, _ := json.Marshal(map[string]interface{}{
		"repo_name": "test-repo",
	})
	_, err := srv.handlers["workflow.run"](params)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestWorkflowRunMissingRepo(t *testing.T) {
	tupleStore := newTestStore(t)
	executor, _ := newTestWorkflowExecutor(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	params, _ := json.Marshal(map[string]interface{}{
		"name": "test-workflow",
	})
	_, err := srv.handlers["workflow.run"](params)
	if err == nil {
		t.Fatal("expected error for missing repo_name")
	}
}

func TestWorkflowApproveWritesTuple(t *testing.T) {
	tupleStore := newTestStore(t)
	executor, _ := newTestWorkflowExecutor(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	params, _ := json.Marshal(map[string]interface{}{
		"instance_id": "wf-test123",
		"step_index":  0,
		"repo_name":   "test-repo",
		"approver":    "alice",
	})
	result, err := srv.handlers["workflow.approve"](params)
	if err != nil {
		t.Fatalf("workflow.approve: %v", err)
	}

	data, _ := json.Marshal(result)
	var resp map[string]string
	json.Unmarshal(data, &resp)
	if resp["status"] != "approved" {
		t.Errorf("expected status 'approved', got %q", resp["status"])
	}

	// Verify the gate_response tuple was written.
	cat := "gate_response"
	scope := "test-repo"
	identity := "wf-test123-0"
	tuples, err := tupleStore.FindAll(&cat, &scope, &identity, nil, nil)
	if err != nil {
		t.Fatalf("find gate_response: %v", err)
	}
	if len(tuples) != 1 {
		t.Fatalf("expected 1 gate_response tuple, got %d", len(tuples))
	}

	payload, _ := tuples[0]["payload"].(string)
	var payloadMap map[string]interface{}
	json.Unmarshal([]byte(payload), &payloadMap)
	if payloadMap["decision"] != "approved" {
		t.Errorf("expected decision 'approved', got %v", payloadMap["decision"])
	}
	if payloadMap["approver"] != "alice" {
		t.Errorf("expected approver 'alice', got %v", payloadMap["approver"])
	}
}

func TestWorkflowRejectWritesTuple(t *testing.T) {
	tupleStore := newTestStore(t)
	executor, _ := newTestWorkflowExecutor(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	params, _ := json.Marshal(map[string]interface{}{
		"instance_id": "wf-test456",
		"step_index":  1,
		"repo_name":   "test-repo",
		"reason":      "code quality",
		"approver":    "bob",
	})
	result, err := srv.handlers["workflow.reject"](params)
	if err != nil {
		t.Fatalf("workflow.reject: %v", err)
	}

	data, _ := json.Marshal(result)
	var resp map[string]string
	json.Unmarshal(data, &resp)
	if resp["status"] != "rejected" {
		t.Errorf("expected status 'rejected', got %q", resp["status"])
	}

	// Verify the gate_response tuple.
	cat := "gate_response"
	scope := "test-repo"
	identity := "wf-test456-1"
	tuples, err := tupleStore.FindAll(&cat, &scope, &identity, nil, nil)
	if err != nil {
		t.Fatalf("find gate_response: %v", err)
	}
	if len(tuples) != 1 {
		t.Fatalf("expected 1 gate_response tuple, got %d", len(tuples))
	}

	payload, _ := tuples[0]["payload"].(string)
	var payloadMap map[string]interface{}
	json.Unmarshal([]byte(payload), &payloadMap)
	if payloadMap["decision"] != "rejected" {
		t.Errorf("expected decision 'rejected', got %v", payloadMap["decision"])
	}
	if payloadMap["reason"] != "code quality" {
		t.Errorf("expected reason 'code quality', got %v", payloadMap["reason"])
	}
}

func TestWorkflowApproveMissingID(t *testing.T) {
	tupleStore := newTestStore(t)
	executor, _ := newTestWorkflowExecutor(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	params, _ := json.Marshal(map[string]interface{}{})
	_, err := srv.handlers["workflow.approve"](params)
	if err == nil {
		t.Fatal("expected error for missing instance_id")
	}
}

func TestWorkflowRejectMissingID(t *testing.T) {
	tupleStore := newTestStore(t)
	executor, _ := newTestWorkflowExecutor(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	params, _ := json.Marshal(map[string]interface{}{})
	_, err := srv.handlers["workflow.reject"](params)
	if err == nil {
		t.Fatal("expected error for missing instance_id")
	}
}

func TestWorkflowDefs(t *testing.T) {
	tupleStore := newTestStore(t)
	executor, _ := newTestWorkflowExecutor(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	params, _ := json.Marshal(map[string]interface{}{})
	result, err := srv.handlers["workflow.defs"](params)
	if err != nil {
		t.Fatalf("workflow.defs: %v", err)
	}

	// Should return a slice (possibly empty).
	defs, ok := result.([]workflow.WorkflowSummary)
	if !ok {
		t.Fatalf("expected []WorkflowSummary, got %T", result)
	}
	_ = defs // empty is fine for test
}

func TestWorkflowListWithInstances(t *testing.T) {
	tupleStore := newTestStore(t)
	_, wfStore := newTestWorkflowExecutor(t)

	// Create some test instances directly in the store.
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		inst := &workflow.Instance{
			ID:           fmt.Sprintf("wf-test%d", i),
			WorkflowName: "test-wf",
			RepoName:     "test-repo",
			Status:       workflow.StatusRunning,
			CurrentStep:  i,
			Params:       map[string]string{},
			StepResults:  []workflow.StepResult{},
			StartedAt:    now,
		}
		if err := wfStore.CreateInstance(inst); err != nil {
			t.Fatalf("create test instance: %v", err)
		}
	}

	executor := workflow.NewExecutor(wfStore, nil, t.TempDir())
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	// List all.
	params, _ := json.Marshal(map[string]interface{}{})
	result, err := srv.handlers["workflow.list"](params)
	if err != nil {
		t.Fatalf("workflow.list: %v", err)
	}

	instances, ok := result.([]map[string]interface{})
	if !ok {
		t.Fatalf("expected []map, got %T", result)
	}
	if len(instances) != 3 {
		t.Fatalf("expected 3 instances, got %d", len(instances))
	}

	// List filtered by repo.
	params, _ = json.Marshal(map[string]interface{}{"repo_name": "test-repo"})
	result, err = srv.handlers["workflow.list"](params)
	if err != nil {
		t.Fatalf("workflow.list filtered: %v", err)
	}
	instances, _ = result.([]map[string]interface{})
	if len(instances) != 3 {
		t.Fatalf("expected 3 instances for test-repo, got %d", len(instances))
	}

	// List filtered by status.
	params, _ = json.Marshal(map[string]interface{}{"status": "completed"})
	result, err = srv.handlers["workflow.list"](params)
	if err != nil {
		t.Fatalf("workflow.list status filter: %v", err)
	}
	instances, _ = result.([]map[string]interface{})
	if len(instances) != 0 {
		t.Fatalf("expected 0 completed instances, got %d", len(instances))
	}
}

func TestWorkflowShowWithInstance(t *testing.T) {
	tupleStore := newTestStore(t)
	_, wfStore := newTestWorkflowExecutor(t)

	now := time.Now().UTC()
	inst := &workflow.Instance{
		ID:           "wf-showtest",
		WorkflowName: "deploy",
		RepoName:     "my-repo",
		Status:       workflow.StatusRunning,
		CurrentStep:  1,
		Params:       map[string]string{"env": "prod"},
		Context: workflow.WorkflowContext{
			ActiveAgent: "worker-1",
			ActiveRepo:  "my-repo",
		},
		StepResults: []workflow.StepResult{
			{StepIndex: 0, StepType: "spawn", Status: "completed", StartedAt: now, EndedAt: &now},
		},
		StartedAt: now,
	}
	if err := wfStore.CreateInstance(inst); err != nil {
		t.Fatalf("create test instance: %v", err)
	}

	executor := workflow.NewExecutor(wfStore, nil, t.TempDir())
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	params, _ := json.Marshal(map[string]interface{}{
		"instance_id": "wf-showtest",
		"repo_name":   "my-repo",
	})
	result, err := srv.handlers["workflow.show"](params)
	if err != nil {
		t.Fatalf("workflow.show: %v", err)
	}

	resp, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}

	if resp["instance_id"] != "wf-showtest" {
		t.Errorf("expected instance_id 'wf-showtest', got %v", resp["instance_id"])
	}
	if resp["workflow_name"] != "deploy" {
		t.Errorf("expected workflow_name 'deploy', got %v", resp["workflow_name"])
	}
	if resp["status"] != "running" {
		t.Errorf("expected status 'running', got %v", resp["status"])
	}

	// Verify step_results are included.
	stepResults, ok := resp["step_results"].([]workflow.StepResult)
	if !ok {
		t.Fatalf("expected step_results, got %T", resp["step_results"])
	}
	if len(stepResults) != 1 {
		t.Fatalf("expected 1 step result, got %d", len(stepResults))
	}

	// Verify params are included.
	wfParams, ok := resp["params"].(map[string]string)
	if !ok {
		t.Fatalf("expected params map, got %T", resp["params"])
	}
	if wfParams["env"] != "prod" {
		t.Errorf("expected param env='prod', got %v", wfParams["env"])
	}
}

func TestWorkflowInvalidJSON(t *testing.T) {
	tupleStore := newTestStore(t)
	executor, _ := newTestWorkflowExecutor(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	methods := []string{
		"workflow.run", "workflow.list", "workflow.show",
		"workflow.cancel", "workflow.approve", "workflow.reject", "workflow.defs",
	}
	for _, method := range methods {
		_, err := srv.handlers[method](json.RawMessage(`{invalid`))
		if err == nil {
			t.Errorf("%s: expected error for invalid JSON", method)
		}
	}
}

func TestWorkflowHandlersRegistered(t *testing.T) {
	tupleStore := newTestStore(t)
	executor, _ := newTestWorkflowExecutor(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterWorkflowHandlers(srv, executor, tupleStore)

	expected := []string{
		"workflow.run", "workflow.list", "workflow.show",
		"workflow.cancel", "workflow.approve", "workflow.reject", "workflow.defs",
	}
	for _, method := range expected {
		if srv.handlers[method] == nil {
			t.Errorf("handler %s not registered", method)
		}
	}
}
