package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/chazu/procyon-park/internal/workflow"
)

// ---------------------------------------------------------------------------
// Mock work tracker
// ---------------------------------------------------------------------------

type mockWorkTracker struct {
	created   []string
	deleted   []string
	createErr error
	deleteErr error
}

func (m *mockWorkTracker) CreateTask(title, description, taskType string) (string, error) {
	if m.createErr != nil {
		return "", m.createErr
	}
	id := fmt.Sprintf("task-%d", len(m.created)+1)
	m.created = append(m.created, id)
	return id, nil
}

func (m *mockWorkTracker) DeleteTask(taskID string) error {
	m.deleted = append(m.deleted, taskID)
	return m.deleteErr
}

// ---------------------------------------------------------------------------
// Tests: SpawnHandler
// ---------------------------------------------------------------------------

func TestSpawnHandler_Success(t *testing.T) {
	tracker := &mockWorkTracker{}

	handler := &SpawnHandler{
		Spawn: func(ctx context.Context, cfg workflow.SpawnConfig, inst *workflow.Instance) (*SpawnResult, error) {
			return &SpawnResult{
				AgentName: "Bramble",
				Branch:    "agent/Bramble/task-1",
				Repo:      "test-repo",
				TaskID:    "task-1",
			}, nil
		},
		WorkTracker: tracker,
	}

	instance := &workflow.Instance{
		ID:       "wf-test",
		RepoName: "test-repo",
		Context:  workflow.WorkflowContext{},
	}

	config := json.RawMessage(`{"role":"cub","task":{"title":"Do the thing","description":"details","taskType":"task"}}`)
	result, err := handler.Execute(context.Background(), instance, 0, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "completed" {
		t.Errorf("expected status completed, got %s", result.Status)
	}
	if result.StepType != "spawn" {
		t.Errorf("expected step type spawn, got %s", result.StepType)
	}

	// Verify context was updated.
	if instance.Context.ActiveAgent != "Bramble" {
		t.Errorf("expected ActiveAgent=Bramble, got %q", instance.Context.ActiveAgent)
	}
	if instance.Context.ActiveBranch != "agent/Bramble/task-1" {
		t.Errorf("expected ActiveBranch=agent/Bramble/task-1, got %q", instance.Context.ActiveBranch)
	}
	if instance.Context.ActiveRepo != "test-repo" {
		t.Errorf("expected ActiveRepo=test-repo, got %q", instance.Context.ActiveRepo)
	}

	// Verify task was created.
	if len(tracker.created) != 1 {
		t.Errorf("expected 1 task created, got %d", len(tracker.created))
	}

	// Verify output.
	var output SpawnOutput
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.AgentName != "Bramble" {
		t.Errorf("expected output agent=Bramble, got %q", output.AgentName)
	}
}

func TestSpawnHandler_SpawnFailure_CleansUpTask(t *testing.T) {
	tracker := &mockWorkTracker{}

	handler := &SpawnHandler{
		Spawn: func(ctx context.Context, cfg workflow.SpawnConfig, inst *workflow.Instance) (*SpawnResult, error) {
			return nil, fmt.Errorf("spawn failed: no names available")
		},
		WorkTracker: tracker,
	}

	instance := &workflow.Instance{
		ID:       "wf-test",
		RepoName: "test-repo",
		Context:  workflow.WorkflowContext{},
	}

	config := json.RawMessage(`{"role":"cub","task":{"title":"Do the thing"}}`)
	result, err := handler.Execute(context.Background(), instance, 0, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "failed" {
		t.Errorf("expected status failed, got %s", result.Status)
	}

	// Verify orphaned task was cleaned up.
	if len(tracker.created) != 1 {
		t.Fatalf("expected 1 task created, got %d", len(tracker.created))
	}
	if len(tracker.deleted) != 1 {
		t.Errorf("expected 1 task deleted (cleanup), got %d", len(tracker.deleted))
	}
	if tracker.deleted[0] != tracker.created[0] {
		t.Errorf("expected deleted task to match created task")
	}
}

func TestSpawnHandler_InvalidConfig(t *testing.T) {
	handler := &SpawnHandler{
		Spawn: func(ctx context.Context, cfg workflow.SpawnConfig, inst *workflow.Instance) (*SpawnResult, error) {
			t.Fatal("spawn should not be called with invalid config")
			return nil, nil
		},
	}

	instance := &workflow.Instance{ID: "wf-test"}
	config := json.RawMessage(`{invalid json}`)

	result, err := handler.Execute(context.Background(), instance, 0, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("expected status failed, got %s", result.Status)
	}
}

func TestSpawnHandler_NoWorkTracker(t *testing.T) {
	handler := &SpawnHandler{
		Spawn: func(ctx context.Context, cfg workflow.SpawnConfig, inst *workflow.Instance) (*SpawnResult, error) {
			return &SpawnResult{
				AgentName: "Fizz",
				Branch:    "agent/Fizz/task-2",
				Repo:      "test-repo",
			}, nil
		},
		// No WorkTracker — should still work.
	}

	instance := &workflow.Instance{
		ID:       "wf-test",
		RepoName: "test-repo",
		Context:  workflow.WorkflowContext{},
	}

	config := json.RawMessage(`{"role":"cub","task":{"title":"No tracker test"}}`)
	result, err := handler.Execute(context.Background(), instance, 0, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("expected completed, got %s", result.Status)
	}
	if instance.Context.ActiveAgent != "Fizz" {
		t.Errorf("expected ActiveAgent=Fizz, got %q", instance.Context.ActiveAgent)
	}
}

func TestSpawnHandler_WorkTrackerCreateFails(t *testing.T) {
	tracker := &mockWorkTracker{
		createErr: fmt.Errorf("beads unavailable"),
	}

	handler := &SpawnHandler{
		Spawn: func(ctx context.Context, cfg workflow.SpawnConfig, inst *workflow.Instance) (*SpawnResult, error) {
			t.Fatal("spawn should not be called when task creation fails")
			return nil, nil
		},
		WorkTracker: tracker,
	}

	instance := &workflow.Instance{ID: "wf-test"}
	config := json.RawMessage(`{"role":"cub","task":{"title":"Fail task"}}`)

	result, err := handler.Execute(context.Background(), instance, 0, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("expected failed, got %s", result.Status)
	}
}
