package steps

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chazu/procyon-park/internal/workflow"
)

func makeEvaluateInstance(waitOutput json.RawMessage) *workflow.Instance {
	now := time.Now().UTC()
	end := now
	return &workflow.Instance{
		ID:           "wf-eval-test",
		WorkflowName: "test-wf",
		RepoName:     "test-repo",
		Status:       workflow.StatusRunning,
		CurrentStep:  1,
		Params:       map[string]string{},
		Context:      workflow.WorkflowContext{},
		StepResults: []workflow.StepResult{
			{
				StepIndex: 0,
				StepType:  "wait",
				Status:    "completed",
				Output:    waitOutput,
				StartedAt: now,
				EndedAt:   &end,
			},
		},
		StartedAt: now,
	}
}

func TestEvaluateHandler_Match(t *testing.T) {
	h := &EvaluateHandler{}

	instance := makeEvaluateInstance(json.RawMessage(`{"exitCode": 0, "branch": "main"}`))
	config := json.RawMessage(`{"expect": {"exitCode": 0}}`)

	result, err := h.Execute(context.Background(), instance, 1, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("status = %s, want completed; error = %s", result.Status, result.Error)
	}
}

func TestEvaluateHandler_Mismatch(t *testing.T) {
	h := &EvaluateHandler{}

	instance := makeEvaluateInstance(json.RawMessage(`{"exitCode": 1}`))
	config := json.RawMessage(`{"expect": {"exitCode": 0}}`)

	result, err := h.Execute(context.Background(), instance, 1, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("status = %s, want failed", result.Status)
	}
	if result.Error == "" {
		t.Error("expected error message for mismatch")
	}
}

func TestEvaluateHandler_NoWaitOutput(t *testing.T) {
	h := &EvaluateHandler{}

	instance := &workflow.Instance{
		ID:           "wf-eval-test",
		WorkflowName: "test-wf",
		RepoName:     "test-repo",
		Status:       workflow.StatusRunning,
		CurrentStep:  0,
		StepResults:  []workflow.StepResult{},
		StartedAt:    time.Now().UTC(),
	}
	config := json.RawMessage(`{"expect": {"exitCode": 0}}`)

	result, err := h.Execute(context.Background(), instance, 0, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("status = %s, want failed", result.Status)
	}
	if !strings.Contains(result.Error, "no previous wait step output") {
		t.Errorf("error = %q, want 'no previous wait step output'", result.Error)
	}
}

func TestEvaluateHandler_PreviousOutputFallback(t *testing.T) {
	h := &EvaluateHandler{}

	instance := &workflow.Instance{
		ID:           "wf-eval-test",
		WorkflowName: "test-wf",
		RepoName:     "test-repo",
		Status:       workflow.StatusRunning,
		CurrentStep:  0,
		Context: workflow.WorkflowContext{
			PreviousOutput: json.RawMessage(`{"exitCode": 0}`),
		},
		StepResults: []workflow.StepResult{},
		StartedAt:   time.Now().UTC(),
	}
	config := json.RawMessage(`{"expect": {"exitCode": 0}}`)

	result, err := h.Execute(context.Background(), instance, 0, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("status = %s, want completed; error = %s", result.Status, result.Error)
	}
}

func TestEvaluateHandler_StructMatch(t *testing.T) {
	h := &EvaluateHandler{}

	// Actual has all expected fields plus extras - should pass.
	instance := makeEvaluateInstance(json.RawMessage(`{"status": "success", "code": 200, "extra": true}`))
	config := json.RawMessage(`{"expect": {"status": "success", "code": 200}}`)

	result, err := h.Execute(context.Background(), instance, 1, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("status = %s, want completed; error = %s", result.Status, result.Error)
	}
}

func TestEvaluateHandler_InvalidConfig(t *testing.T) {
	h := &EvaluateHandler{}

	instance := makeEvaluateInstance(json.RawMessage(`{"exitCode": 0}`))
	config := json.RawMessage(`{invalid json`)

	result, err := h.Execute(context.Background(), instance, 1, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("status = %s, want failed", result.Status)
	}
}

func TestCueEvaluate_SimpleMatch(t *testing.T) {
	expect := json.RawMessage(`{"exitCode": 0}`)
	actual := json.RawMessage(`{"exitCode": 0}`)
	if err := cueEvaluate(expect, actual); err != nil {
		t.Errorf("cueEvaluate: %v", err)
	}
}

func TestCueEvaluate_SimpleMismatch(t *testing.T) {
	expect := json.RawMessage(`{"exitCode": 0}`)
	actual := json.RawMessage(`{"exitCode": 1}`)
	if err := cueEvaluate(expect, actual); err == nil {
		t.Error("expected error for mismatch")
	}
}

func TestCueEvaluate_SubsetMatch(t *testing.T) {
	// expect is a subset of actual — should pass.
	expect := json.RawMessage(`{"exitCode": 0}`)
	actual := json.RawMessage(`{"exitCode": 0, "branch": "main", "output": "ok"}`)
	if err := cueEvaluate(expect, actual); err != nil {
		t.Errorf("cueEvaluate subset: %v", err)
	}
}

func TestCueEvaluate_MissingField(t *testing.T) {
	// expect requires a field that actual doesn't have — should fail.
	expect := json.RawMessage(`{"exitCode": 0, "required": true}`)
	actual := json.RawMessage(`{"exitCode": 0}`)
	if err := cueEvaluate(expect, actual); err == nil {
		t.Error("expected error when actual is missing expected field")
	}
}
