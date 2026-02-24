package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/chazu/procyon-park/internal/workflow"
)

// ---------------------------------------------------------------------------
// Tests: DismissHandler
// ---------------------------------------------------------------------------

func TestDismissHandler_Success(t *testing.T) {
	var killedAgent, killedRepo string

	handler := &DismissHandler{
		Kill: func(ctx context.Context, agentName, repoName string) error {
			killedAgent = agentName
			killedRepo = repoName
			return nil
		},
	}

	instance := &workflow.Instance{
		ID:       "wf-test",
		RepoName: "test-repo",
		Context: workflow.WorkflowContext{
			ActiveAgent:  "Bramble",
			ActiveBranch: "agent/Bramble/task-1",
			ActiveRepo:   "test-repo",
		},
	}

	config := json.RawMessage(`{}`)
	result, err := handler.Execute(context.Background(), instance, 2, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "completed" {
		t.Errorf("expected completed, got %s", result.Status)
	}
	if result.StepType != "dismiss" {
		t.Errorf("expected step type dismiss, got %s", result.StepType)
	}

	// Verify kill was called with correct args.
	if killedAgent != "Bramble" {
		t.Errorf("expected kill agent=Bramble, got %q", killedAgent)
	}
	if killedRepo != "test-repo" {
		t.Errorf("expected kill repo=test-repo, got %q", killedRepo)
	}

	// Verify context was cleared.
	if instance.Context.ActiveAgent != "" {
		t.Errorf("expected ActiveAgent cleared, got %q", instance.Context.ActiveAgent)
	}
	if instance.Context.ActiveBranch != "" {
		t.Errorf("expected ActiveBranch cleared, got %q", instance.Context.ActiveBranch)
	}

	// Verify output.
	var output DismissOutput
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.AgentName != "Bramble" {
		t.Errorf("expected output agent=Bramble, got %q", output.AgentName)
	}
	if output.MergeWarning != "" {
		t.Errorf("expected no merge warning, got %q", output.MergeWarning)
	}
}

func TestDismissHandler_MergeConflictNonFatal(t *testing.T) {
	handler := &DismissHandler{
		Kill: func(ctx context.Context, agentName, repoName string) error {
			return fmt.Errorf("dismiss: merge agent/X into main failed (work preserved on branch): merge conflict in README.md")
		},
	}

	instance := &workflow.Instance{
		ID:       "wf-test",
		RepoName: "test-repo",
		Context: workflow.WorkflowContext{
			ActiveAgent:  "X",
			ActiveBranch: "agent/X/task-5",
			ActiveRepo:   "test-repo",
		},
	}

	config := json.RawMessage(`{}`)
	result, err := handler.Execute(context.Background(), instance, 2, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still complete — merge conflicts are non-fatal.
	if result.Status != "completed" {
		t.Errorf("expected completed despite merge conflict, got %s", result.Status)
	}

	var output DismissOutput
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.MergeWarning == "" {
		t.Error("expected merge warning to be set")
	}
}

func TestDismissHandler_NonMergeError(t *testing.T) {
	handler := &DismissHandler{
		Kill: func(ctx context.Context, agentName, repoName string) error {
			return fmt.Errorf("kill session failed: tmux not found")
		},
	}

	instance := &workflow.Instance{
		ID:       "wf-test",
		RepoName: "test-repo",
		Context: workflow.WorkflowContext{
			ActiveAgent: "Y",
			ActiveRepo:  "test-repo",
		},
	}

	config := json.RawMessage(`{}`)
	result, err := handler.Execute(context.Background(), instance, 2, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "failed" {
		t.Errorf("expected failed for non-merge error, got %s", result.Status)
	}
}

func TestDismissHandler_NoActiveAgent(t *testing.T) {
	handler := &DismissHandler{
		Kill: func(ctx context.Context, agentName, repoName string) error {
			t.Fatal("kill should not be called without active agent")
			return nil
		},
	}

	instance := &workflow.Instance{
		ID:      "wf-test",
		Context: workflow.WorkflowContext{},
	}

	config := json.RawMessage(`{}`)
	result, err := handler.Execute(context.Background(), instance, 2, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "failed" {
		t.Errorf("expected failed, got %s", result.Status)
	}
}

func TestDismissHandler_FallsBackToInstanceRepoName(t *testing.T) {
	var gotRepo string

	handler := &DismissHandler{
		Kill: func(ctx context.Context, agentName, repoName string) error {
			gotRepo = repoName
			return nil
		},
	}

	instance := &workflow.Instance{
		ID:       "wf-test",
		RepoName: "fallback-repo",
		Context: workflow.WorkflowContext{
			ActiveAgent: "Z",
			// ActiveRepo is empty — should fall back to instance.RepoName.
		},
	}

	config := json.RawMessage(`{}`)
	result, err := handler.Execute(context.Background(), instance, 2, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "completed" {
		t.Errorf("expected completed, got %s", result.Status)
	}
	if gotRepo != "fallback-repo" {
		t.Errorf("expected repo=fallback-repo, got %q", gotRepo)
	}
}
