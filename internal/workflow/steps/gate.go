package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chazu/procyon-park/internal/tuplestore"
	"github.com/chazu/procyon-park/internal/workflow"
)

// GateHandler pauses workflow execution. Two variants are supported:
//   - Human gate: writes gate_request tuples for each approver, polls for
//     gate_response tuples (approve/reject), with configurable timeout.
//   - Timer gate: pauses for a fixed duration with crash-recovery awareness.
//
// Both variants persist gate state via the workflow Store for crash recovery.
type GateHandler struct {
	Store      *workflow.Store
	Tuples     *tuplestore.TupleStore
	PollInterval time.Duration // defaults to 5s if zero
}

func (h *GateHandler) pollInterval() time.Duration {
	if h.PollInterval > 0 {
		return h.PollInterval
	}
	return 5 * time.Second
}

// Execute dispatches to the human or timer gate variant based on GateConfig.GateType.
func (h *GateHandler) Execute(ctx context.Context, instance *workflow.Instance, stepIndex int, config json.RawMessage) (*workflow.StepResult, error) {
	now := time.Now().UTC()
	result := &workflow.StepResult{
		StepIndex: stepIndex,
		StepType:  "gate",
		StartedAt: now,
	}

	var cfg workflow.GateConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("invalid gate config: %v", err)
		end := time.Now().UTC()
		result.EndedAt = &end
		return result, nil
	}

	switch cfg.GateType {
	case "human":
		return h.executeHumanGate(ctx, instance, stepIndex, cfg, result)
	case "timer":
		return h.executeTimerGate(ctx, instance, stepIndex, cfg, result)
	default:
		result.Status = "failed"
		result.Error = fmt.Sprintf("unknown gate type: %q", cfg.GateType)
		end := time.Now().UTC()
		result.EndedAt = &end
		return result, nil
	}
}

// executeHumanGate writes gate_request tuples for each approver and polls for
// gate_response tuples. Supports crash recovery via persistent gate state.
func (h *GateHandler) executeHumanGate(ctx context.Context, instance *workflow.Instance, stepIndex int, cfg workflow.GateConfig, result *workflow.StepResult) (*workflow.StepResult, error) {
	timeout := 30 * time.Minute // default
	if cfg.Timeout != "" {
		d, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			result.Status = "failed"
			result.Error = fmt.Sprintf("invalid timeout %q: %v", cfg.Timeout, err)
			end := time.Now().UTC()
			result.EndedAt = &end
			return result, nil
		}
		timeout = d
	}

	// Recover or create gate state.
	gs, err := h.Store.GetGateState(instance.ID, stepIndex)
	if err != nil {
		return nil, fmt.Errorf("gate: get state: %w", err)
	}

	if gs == nil {
		gs = &workflow.GateState{
			InstanceID: instance.ID,
			StepIndex:  stepIndex,
			StartedAt:  time.Now().UTC(),
			State:      "waiting",
			PromptSent: false,
		}
		if err := h.Store.SaveGateState(gs); err != nil {
			return nil, fmt.Errorf("gate: save initial state: %w", err)
		}
	}

	// If we already have a terminal state from a previous run, return it.
	if gs.State == "approved" || gs.State == "rejected" || gs.State == "timed_out" {
		return h.gateTerminalResult(result, gs)
	}

	// Write gate_request tuples if not already sent.
	if !gs.PromptSent {
		if err := h.sendGateRequests(instance, stepIndex, cfg); err != nil {
			return nil, fmt.Errorf("gate: send requests: %w", err)
		}
		gs.PromptSent = true
		if err := h.Store.SaveGateState(gs); err != nil {
			return nil, fmt.Errorf("gate: save prompt state: %w", err)
		}
	}

	// Calculate deadline from the original start time (crash-recovery-aware).
	deadline := gs.StartedAt.Add(timeout)

	// Poll for gate_response tuples.
	ticker := time.NewTicker(h.pollInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if time.Now().UTC().After(deadline) {
				gs.State = "timed_out"
				h.Store.SaveGateState(gs)
				return h.gateTerminalResult(result, gs)
			}

			response, err := h.pollGateResponse(instance, stepIndex)
			if err != nil {
				return nil, fmt.Errorf("gate: poll response: %w", err)
			}
			if response == nil {
				continue
			}

			gs.State = response.Decision
			h.Store.SaveGateState(gs)
			return h.gateTerminalResult(result, gs)
		}
	}
}

// gateResponse represents a gate approval or rejection.
type gateResponse struct {
	Decision string `json:"decision"` // "approved" or "rejected"
	Approver string `json:"approver"`
	Reason   string `json:"reason,omitempty"`
}

// sendGateRequests writes a gate_request tuple for each approver.
func (h *GateHandler) sendGateRequests(instance *workflow.Instance, stepIndex int, cfg workflow.GateConfig) error {
	for _, approver := range cfg.Approvers {
		payload, _ := json.Marshal(map[string]interface{}{
			"instanceId": instance.ID,
			"stepIndex":  stepIndex,
			"approver":   approver,
			"prompt":     cfg.Prompt,
			"workflow":   instance.WorkflowName,
		})
		identity := fmt.Sprintf("%s-%d", instance.ID, stepIndex)
		_, err := h.Tuples.Insert(
			"gate_request",          // category
			instance.RepoName,       // scope
			identity,                // identity
			"local",                 // instance
			string(payload),         // payload
			"session",               // lifecycle
			nil, nil, nil,           // taskID, agentID, ttl
		)
		if err != nil {
			return fmt.Errorf("write gate_request for %s: %w", approver, err)
		}
	}
	return nil
}

// pollGateResponse checks for a gate_response tuple matching this gate step.
// Atomically consumes the response (FindAndDelete).
func (h *GateHandler) pollGateResponse(instance *workflow.Instance, stepIndex int) (*gateResponse, error) {
	cat := "gate_response"
	scope := instance.RepoName
	identity := fmt.Sprintf("%s-%d", instance.ID, stepIndex)

	row, err := h.Tuples.FindAndDelete(&cat, &scope, &identity, nil, nil)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}

	payload, ok := row["payload"].(string)
	if !ok {
		return nil, fmt.Errorf("gate_response payload not a string")
	}

	var resp gateResponse
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		return nil, fmt.Errorf("unmarshal gate_response: %w", err)
	}
	return &resp, nil
}

// gateTerminalResult converts a terminal gate state to a StepResult.
func (h *GateHandler) gateTerminalResult(result *workflow.StepResult, gs *workflow.GateState) (*workflow.StepResult, error) {
	end := time.Now().UTC()
	result.EndedAt = &end

	output, _ := json.Marshal(map[string]string{"gateState": gs.State})
	result.Output = output

	switch gs.State {
	case "approved":
		result.Status = "completed"
	case "rejected":
		result.Status = "failed"
		result.Error = "gate rejected"
	case "timed_out":
		result.Status = "failed"
		result.Error = "gate timed out waiting for approval"
	default:
		result.Status = "failed"
		result.Error = fmt.Sprintf("unexpected gate state: %s", gs.State)
	}
	return result, nil
}

// executeTimerGate pauses for the configured duration. Uses gate state persistence
// to track the original start time so that after a crash, only the remaining
// time is waited (not the full duration again).
func (h *GateHandler) executeTimerGate(ctx context.Context, instance *workflow.Instance, stepIndex int, cfg workflow.GateConfig, result *workflow.StepResult) (*workflow.StepResult, error) {
	duration, err := time.ParseDuration(cfg.Duration)
	if err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("invalid duration %q: %v", cfg.Duration, err)
		end := time.Now().UTC()
		result.EndedAt = &end
		return result, nil
	}

	// Recover or create gate state for crash recovery.
	gs, err := h.Store.GetGateState(instance.ID, stepIndex)
	if err != nil {
		return nil, fmt.Errorf("gate: get timer state: %w", err)
	}

	if gs == nil {
		gs = &workflow.GateState{
			InstanceID: instance.ID,
			StepIndex:  stepIndex,
			StartedAt:  time.Now().UTC(),
			State:      "waiting",
		}
		if err := h.Store.SaveGateState(gs); err != nil {
			return nil, fmt.Errorf("gate: save timer state: %w", err)
		}
	}

	// Calculate remaining time from original start.
	elapsed := time.Since(gs.StartedAt)
	remaining := duration - elapsed
	if remaining <= 0 {
		// Already elapsed (crash recovery case).
		gs.State = "approved"
		h.Store.SaveGateState(gs)
		result.Status = "completed"
		output, _ := json.Marshal(map[string]string{"gateState": "approved", "duration": cfg.Duration})
		result.Output = output
		end := time.Now().UTC()
		result.EndedAt = &end
		return result, nil
	}

	timer := time.NewTimer(remaining)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		gs.State = "approved"
		h.Store.SaveGateState(gs)
		result.Status = "completed"
		output, _ := json.Marshal(map[string]string{"gateState": "approved", "duration": cfg.Duration})
		result.Output = output
		end := time.Now().UTC()
		result.EndedAt = &end
		return result, nil
	}
}
