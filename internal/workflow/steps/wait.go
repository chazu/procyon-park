package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chazu/procyon-park/internal/workflow"
)

// Default wait configuration values.
const (
	DefaultPollInterval = 5 * time.Second
	DefaultWaitTimeout  = 10 * time.Minute
)

// TupleFinder abstracts tuplespace queries for testability.
type TupleFinder interface {
	// FindTaskDoneEvent searches for a task_done event matching the given agent name.
	// Returns the event payload as JSON, or nil if not found.
	FindTaskDoneEvent(agentName string) (json.RawMessage, error)
}

// WaitOutput is the structured output recorded in StepResult.
type WaitOutput struct {
	AgentName string          `json:"agentName"`
	Event     json.RawMessage `json:"event"`
}

// WaitHandler polls the tuplespace for a task_done event from the active agent.
type WaitHandler struct {
	Finder       TupleFinder
	PollInterval time.Duration
}

// Execute polls for a task_done event matching the active agent, respecting
// the configured timeout (from step config or DefaultWaitTimeout).
func (h *WaitHandler) Execute(ctx context.Context, instance *workflow.Instance, stepIndex int, config json.RawMessage) (*workflow.StepResult, error) {
	now := time.Now()
	result := &workflow.StepResult{
		StepIndex: stepIndex,
		StepType:  "wait",
		Status:    "running",
		StartedAt: now,
	}

	// Parse the wait config for timeout.
	var cfg workflow.WaitConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return failResult(result, fmt.Errorf("wait: parse config: %w", err)), nil
	}

	// Resolve timeout.
	timeout := DefaultWaitTimeout
	if cfg.Timeout != "" {
		parsed, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			return failResult(result, fmt.Errorf("wait: parse timeout %q: %w", cfg.Timeout, err)), nil
		}
		timeout = parsed
	}

	// Resolve the agent to wait for.
	agentName := instance.Context.ActiveAgent
	if agentName == "" {
		return failResult(result, fmt.Errorf("wait: no active agent in context")), nil
	}

	// Resolve poll interval.
	pollInterval := h.PollInterval
	if pollInterval == 0 {
		pollInterval = DefaultPollInterval
	}

	// Create a deadline context for the timeout.
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Poll loop.
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Check immediately before first tick.
	if event, err := h.Finder.FindTaskDoneEvent(agentName); err != nil {
		return failResult(result, fmt.Errorf("wait: poll: %w", err)), nil
	} else if event != nil {
		return h.completeWait(result, instance, agentName, event), nil
	}

	for {
		select {
		case <-deadlineCtx.Done():
			return failResult(result, fmt.Errorf("wait: timeout after %s waiting for agent %q", timeout, agentName)), nil
		case <-ticker.C:
			event, err := h.Finder.FindTaskDoneEvent(agentName)
			if err != nil {
				return failResult(result, fmt.Errorf("wait: poll: %w", err)), nil
			}
			if event != nil {
				return h.completeWait(result, instance, agentName, event), nil
			}
		}
	}
}

// completeWait finalizes a successful wait by updating the instance context.
func (h *WaitHandler) completeWait(result *workflow.StepResult, instance *workflow.Instance, agentName string, event json.RawMessage) *workflow.StepResult {
	// Set PreviousOutput so downstream steps can access the event data.
	instance.Context.PreviousOutput = event

	output := WaitOutput{
		AgentName: agentName,
		Event:     event,
	}
	outputJSON, _ := json.Marshal(output)

	endTime := time.Now()
	result.Status = "completed"
	result.Output = outputJSON
	result.EndedAt = &endTime
	return result
}
