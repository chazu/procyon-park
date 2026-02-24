// workflowbridge.go registers JSON-RPC handlers for workflow operations.
// Provides workflow.run, workflow.list, workflow.show, workflow.cancel,
// workflow.approve, workflow.reject, and workflow.defs.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chazu/procyon-park/internal/tuplestore"
	"github.com/chazu/procyon-park/internal/workflow"
)

// RegisterWorkflowHandlers wires the workflow.* JSON-RPC methods.
// Must be called before the IPCServer is started.
func RegisterWorkflowHandlers(srv *IPCServer, executor *workflow.Executor, store *tuplestore.TupleStore) {
	srv.Handle("workflow.run", handleWorkflowRun(executor))
	srv.Handle("workflow.list", handleWorkflowList(executor))
	srv.Handle("workflow.show", handleWorkflowShow(executor))
	srv.Handle("workflow.cancel", handleWorkflowCancel(executor))
	srv.Handle("workflow.approve", handleWorkflowApprove(store))
	srv.Handle("workflow.reject", handleWorkflowReject(store))
	srv.Handle("workflow.defs", handleWorkflowDefs())
}

// workflowRunParams are the JSON-RPC parameters for workflow.run.
type workflowRunParams struct {
	Name     string            `json:"name"`
	RepoName string            `json:"repo_name"`
	Params   map[string]string `json:"params,omitempty"`
}

// handleWorkflowRun returns a Handler that runs a workflow.
func handleWorkflowRun(executor *workflow.Executor) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p workflowRunParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		if p.Name == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "name is required"}
		}
		if p.RepoName == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "repo_name is required"}
		}

		if p.Params == nil {
			p.Params = make(map[string]string)
		}

		instanceID, err := executor.Run(context.Background(), p.Name, p.RepoName, p.Params)
		if err != nil {
			return nil, fmt.Errorf("workflow.run: %w", err)
		}

		// Fetch the created instance for full details.
		inst, err := executor.GetInstance(p.RepoName, instanceID)
		if err != nil {
			// Return just the ID if we can't fetch details.
			return map[string]string{"instance_id": instanceID}, nil
		}

		return instanceToResponse(inst), nil
	}
}

// workflowListParams are the JSON-RPC parameters for workflow.list.
type workflowListParams struct {
	RepoName string `json:"repo_name,omitempty"`
	Status   string `json:"status,omitempty"`
}

// handleWorkflowList returns a Handler that lists workflow instances.
func handleWorkflowList(executor *workflow.Executor) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p workflowListParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		var status workflow.InstanceStatus
		if p.Status != "" {
			status = workflow.InstanceStatus(p.Status)
		}

		instances, err := executor.ListInstances(status, p.RepoName)
		if err != nil {
			return nil, fmt.Errorf("workflow.list: %w", err)
		}

		results := make([]map[string]interface{}, 0, len(instances))
		for _, inst := range instances {
			results = append(results, instanceToResponse(inst))
		}

		return results, nil
	}
}

// workflowShowParams are the JSON-RPC parameters for workflow.show.
type workflowShowParams struct {
	InstanceID string `json:"instance_id"`
	RepoName   string `json:"repo_name,omitempty"`
}

// handleWorkflowShow returns a Handler that shows workflow instance details.
func handleWorkflowShow(executor *workflow.Executor) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p workflowShowParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		if p.InstanceID == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "instance_id is required"}
		}

		inst, err := executor.GetInstance(p.RepoName, p.InstanceID)
		if err != nil {
			return nil, fmt.Errorf("workflow.show: %w", err)
		}
		if inst == nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: fmt.Sprintf("instance %q not found", p.InstanceID)}
		}

		resp := instanceToResponse(inst)
		resp["step_results"] = inst.StepResults
		resp["context"] = inst.Context
		resp["params"] = inst.Params
		resp["is_running"] = executor.IsRunning(inst.ID)

		return resp, nil
	}
}

// workflowCancelParams are the JSON-RPC parameters for workflow.cancel.
type workflowCancelParams struct {
	InstanceID string `json:"instance_id"`
}

// handleWorkflowCancel returns a Handler that cancels a running workflow.
func handleWorkflowCancel(executor *workflow.Executor) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p workflowCancelParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		if p.InstanceID == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "instance_id is required"}
		}

		if err := executor.Cancel(context.Background(), p.InstanceID); err != nil {
			return nil, fmt.Errorf("workflow.cancel: %w", err)
		}

		return map[string]string{"status": "cancelled"}, nil
	}
}

// workflowApproveParams are the JSON-RPC parameters for workflow.approve.
type workflowApproveParams struct {
	InstanceID string `json:"instance_id"`
	StepIndex  int    `json:"step_index"`
	RepoName   string `json:"repo_name"`
	Approver   string `json:"approver,omitempty"`
}

// handleWorkflowApprove returns a Handler that approves a human gate.
// It writes a gate_response tuple that the GateHandler polls for.
func handleWorkflowApprove(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p workflowApproveParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		if p.InstanceID == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "instance_id is required"}
		}

		return writeGateResponse(store, p.RepoName, p.InstanceID, p.StepIndex, "approved", p.Approver, "")
	}
}

// workflowRejectParams are the JSON-RPC parameters for workflow.reject.
type workflowRejectParams struct {
	InstanceID string `json:"instance_id"`
	StepIndex  int    `json:"step_index"`
	RepoName   string `json:"repo_name"`
	Approver   string `json:"approver,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// handleWorkflowReject returns a Handler that rejects a human gate.
func handleWorkflowReject(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p workflowRejectParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		if p.InstanceID == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "instance_id is required"}
		}

		return writeGateResponse(store, p.RepoName, p.InstanceID, p.StepIndex, "rejected", p.Approver, p.Reason)
	}
}

// workflowDefsParams are the JSON-RPC parameters for workflow.defs.
type workflowDefsParams struct {
	RepoRoot string `json:"repo_root,omitempty"`
}

// handleWorkflowDefs returns a Handler that lists available workflow definitions.
func handleWorkflowDefs() Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p workflowDefsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		defs, err := workflow.ListWorkflows(p.RepoRoot)
		if err != nil {
			return nil, fmt.Errorf("workflow.defs: %w", err)
		}

		return defs, nil
	}
}

// writeGateResponse writes a gate_response tuple for the given instance and step.
func writeGateResponse(store *tuplestore.TupleStore, repoName, instanceID string, stepIndex int, decision, approver, reason string) (interface{}, error) {
	payload, _ := json.Marshal(map[string]interface{}{
		"decision": decision,
		"approver": approver,
		"reason":   reason,
	})

	identity := fmt.Sprintf("%s-%d", instanceID, stepIndex)
	_, err := store.Insert(
		"gate_response",   // category
		repoName,          // scope (matches gate handler's poll scope)
		identity,          // identity
		"local",           // instance
		string(payload),   // payload
		"session",         // lifecycle
		nil, nil, nil,     // taskID, agentID, ttl
	)
	if err != nil {
		return nil, fmt.Errorf("write gate_response: %w", err)
	}

	return map[string]string{"status": decision}, nil
}

// instanceToResponse converts an Instance to a response map with snake_case keys.
func instanceToResponse(inst *workflow.Instance) map[string]interface{} {
	resp := map[string]interface{}{
		"instance_id":   inst.ID,
		"workflow_name": inst.WorkflowName,
		"repo_name":     inst.RepoName,
		"status":        string(inst.Status),
		"current_step":  inst.CurrentStep,
		"started_at":    inst.StartedAt.Format("2006-01-02T15:04:05Z"),
	}
	if inst.CompletedAt != nil {
		resp["completed_at"] = inst.CompletedAt.Format("2006-01-02T15:04:05Z")
	}
	if inst.Error != "" {
		resp["error"] = inst.Error
	}
	return resp
}
