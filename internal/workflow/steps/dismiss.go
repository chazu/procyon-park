package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chazu/procyon-park/internal/workflow"
)

// KillFn is the callback signature for dismissing an agent.
// It receives the agent name and repo name, performing the full dismiss sequence.
// Merge conflict errors are non-fatal (returned as a warning in output).
type KillFn func(ctx context.Context, agentName, repoName string) error

// DismissOutput is the structured output recorded in StepResult.
type DismissOutput struct {
	AgentName    string `json:"agentName"`
	MergeWarning string `json:"mergeWarning,omitempty"`
}

// DismissHandler executes a dismiss step by resolving the agent from the
// instance context and calling KillFn.
type DismissHandler struct {
	Kill KillFn
}

// Execute runs the dismiss step: resolve agent, call KillFn, clear context.
func (h *DismissHandler) Execute(ctx context.Context, instance *workflow.Instance, stepIndex int, config json.RawMessage) (*workflow.StepResult, error) {
	now := time.Now()
	result := &workflow.StepResult{
		StepIndex: stepIndex,
		StepType:  "dismiss",
		Status:    "running",
		StartedAt: now,
	}

	// Resolve the agent to dismiss from instance context.
	agentName := instance.Context.ActiveAgent
	if agentName == "" {
		return failResult(result, fmt.Errorf("dismiss: no active agent in context")), nil
	}

	repoName := instance.Context.ActiveRepo
	if repoName == "" {
		repoName = instance.RepoName
	}

	// Call the kill function.
	var mergeWarning string
	if err := h.Kill(ctx, agentName, repoName); err != nil {
		// Merge conflict errors are non-fatal — record as warning.
		if isMergeConflict(err) {
			mergeWarning = err.Error()
		} else {
			return failResult(result, fmt.Errorf("dismiss: %w", err)), nil
		}
	}

	// Build output.
	output := DismissOutput{
		AgentName:    agentName,
		MergeWarning: mergeWarning,
	}
	outputJSON, _ := json.Marshal(output)

	// Clear agent-specific context fields.
	instance.Context.ActiveAgent = ""
	instance.Context.ActiveBranch = ""

	endTime := time.Now()
	result.Status = "completed"
	result.Output = outputJSON
	result.EndedAt = &endTime
	return result, nil
}

// isMergeConflict checks whether an error is a merge conflict.
func isMergeConflict(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "merge") && strings.Contains(msg, "conflict")
}
