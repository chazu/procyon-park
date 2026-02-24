// Package steps implements workflow step handlers for the agent lifecycle.
//
// Each handler implements the [workflow.StepHandler] interface and wraps
// lower-level operations through function callbacks, making them testable
// with mock implementations.
package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chazu/procyon-park/internal/workflow"
)

// SpawnResult holds the output of a spawn operation.
type SpawnResult struct {
	AgentName string `json:"agentName"`
	Branch    string `json:"branch"`
	Repo      string `json:"repo"`
	TaskID    string `json:"taskId"`
}

// SpawnFn is the callback signature for spawning an agent.
// The function receives the resolved spawn config and returns spawn results.
type SpawnFn func(ctx context.Context, cfg workflow.SpawnConfig, instance *workflow.Instance) (*SpawnResult, error)

// SpawnHandler executes a spawn step by creating a beads task (via WorkTracker),
// calling SpawnFn, and updating the instance context.
type SpawnHandler struct {
	Spawn       SpawnFn
	WorkTracker WorkTracker
}

// WorkTracker abstracts beads task creation and cleanup for testability.
type WorkTracker interface {
	CreateTask(title, description, taskType string) (taskID string, err error)
	DeleteTask(taskID string) error
}

// SpawnOutput is the structured output recorded in StepResult.
type SpawnOutput struct {
	AgentName string `json:"agentName"`
	Branch    string `json:"branch"`
	Repo      string `json:"repo"`
	TaskID    string `json:"taskId"`
}

// Execute runs the spawn step: parse config, create task, call SpawnFn, update context.
func (h *SpawnHandler) Execute(ctx context.Context, instance *workflow.Instance, stepIndex int, config json.RawMessage) (*workflow.StepResult, error) {
	now := time.Now()
	result := &workflow.StepResult{
		StepIndex: stepIndex,
		StepType:  "spawn",
		Status:    "running",
		StartedAt: now,
	}

	// Parse the spawn config.
	var cfg workflow.SpawnConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return failResult(result, fmt.Errorf("spawn: parse config: %w", err)), nil
	}

	// Create a beads task if a work tracker is configured.
	var taskID string
	if h.WorkTracker != nil && cfg.Task.Title != "" {
		var err error
		taskID, err = h.WorkTracker.CreateTask(cfg.Task.Title, cfg.Task.Description, cfg.Task.TaskType)
		if err != nil {
			return failResult(result, fmt.Errorf("spawn: create task: %w", err)), nil
		}
	}

	// Call the spawn function.
	spawnResult, err := h.Spawn(ctx, cfg, instance)
	if err != nil {
		// Clean up orphaned task on spawn failure.
		if taskID != "" && h.WorkTracker != nil {
			_ = h.WorkTracker.DeleteTask(taskID)
		}
		return failResult(result, fmt.Errorf("spawn: %w", err)), nil
	}

	// Update instance context with spawn results.
	instance.Context.ActiveAgent = spawnResult.AgentName
	instance.Context.ActiveBranch = spawnResult.Branch
	instance.Context.ActiveRepo = spawnResult.Repo
	if spawnResult.TaskID != "" {
		instance.Context.TaskID = spawnResult.TaskID
	} else if taskID != "" {
		instance.Context.TaskID = taskID
	}

	// Build output.
	output := SpawnOutput{
		AgentName: spawnResult.AgentName,
		Branch:    spawnResult.Branch,
		Repo:      spawnResult.Repo,
		TaskID:    instance.Context.TaskID,
	}
	outputJSON, _ := json.Marshal(output)

	endTime := time.Now()
	result.Status = "completed"
	result.Output = outputJSON
	result.EndedAt = &endTime
	return result, nil
}

// failResult marks a step result as failed with the given error.
func failResult(r *workflow.StepResult, err error) *workflow.StepResult {
	endTime := time.Now()
	r.Status = "failed"
	r.Error = err.Error()
	r.EndedAt = &endTime
	return r
}
