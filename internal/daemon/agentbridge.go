// agentbridge.go registers JSON-RPC handlers for agent lifecycle operations.
// Provides agent.spawn, agent.dismiss, and agent.status.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chazu/procyon-park/internal/dismiss"
	"github.com/chazu/procyon-park/internal/prune"
	"github.com/chazu/procyon-park/internal/respawn"
	"github.com/chazu/procyon-park/internal/spawn"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// RegisterAgentHandlers wires the agent.* JSON-RPC methods.
// Must be called before the IPCServer is started.
func RegisterAgentHandlers(srv *IPCServer, store *tuplestore.TupleStore) {
	srv.Handle("agent.spawn", handleAgentSpawn(store))
	srv.Handle("agent.dismiss", handleAgentDismiss(store))
	srv.Handle("agent.status", handleAgentStatus(store))
	srv.Handle("agent.list", handleAgentList(store))
	srv.Handle("agent.respawn", handleAgentRespawn(store))
	srv.Handle("agent.prune", handleAgentPrune(store))
	srv.Handle("agent.show", handleAgentShow(store))
	srv.Handle("agent.stuck", handleAgentStuck(store))
}

// spawnParams are the JSON-RPC parameters for agent.spawn.
type spawnParams struct {
	Role         string `json:"role"`
	TaskID       string `json:"task_id"`
	BaseBranch   string `json:"base_branch"`
	RepoName     string `json:"repo_name"`
	RepoRoot     string `json:"repo_root"`
	WorktreeBase string `json:"worktree_base,omitempty"`
	EpicID       string `json:"epic_id,omitempty"`
	AgentCmd     string `json:"agent_cmd,omitempty"`
	PrimeText    string `json:"prime_text,omitempty"`
	PromptWaitMs int    `json:"prompt_wait_ms,omitempty"`
}

// handleAgentSpawn returns a Handler that runs the spawn sequence.
func handleAgentSpawn(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p spawnParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		var promptWait time.Duration
		if p.PromptWaitMs > 0 {
			promptWait = time.Duration(p.PromptWaitMs) * time.Millisecond
		}

		agentCmd := p.AgentCmd
		if agentCmd == "" {
			agentCmd = spawn.DefaultAgentCmd
		}

		result, err := spawn.Spawn(context.Background(), spawn.Params{
			Role:         p.Role,
			TaskID:       p.TaskID,
			BaseBranch:   p.BaseBranch,
			RepoName:     p.RepoName,
			RepoRoot:     p.RepoRoot,
			WorktreeBase: p.WorktreeBase,
			EpicID:       p.EpicID,
			AgentCmd:     agentCmd,
			PrimeText:    p.PrimeText,
			PromptWait:   promptWait,
		}, store)
		if err != nil {
			return nil, fmt.Errorf("agent.spawn: %w", err)
		}

		return result, nil
	}
}

// dismissParams are the JSON-RPC parameters for agent.dismiss.
type dismissParams struct {
	AgentName string `json:"agent_name"`
	RepoName  string `json:"repo_name"`
	RepoRoot  string `json:"repo_root"`
}

// handleAgentDismiss returns a Handler that runs the dismiss sequence.
func handleAgentDismiss(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p dismissParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		err := dismiss.Dismiss(context.Background(), dismiss.Params{
			AgentName: p.AgentName,
			RepoName:  p.RepoName,
			RepoRoot:  p.RepoRoot,
		}, store)
		if err != nil {
			return nil, fmt.Errorf("agent.dismiss: %w", err)
		}

		return map[string]string{"status": "dismissed"}, nil
	}
}

// statusParams are the JSON-RPC parameters for agent.status.
type statusParams struct {
	AgentName string `json:"agent_name"`
	RepoName  string `json:"repo_name"`
}

// handleAgentStatus returns a Handler that checks agent liveness.
func handleAgentStatus(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p statusParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		status, err := dismiss.GetActualStatus(store, p.AgentName, p.RepoName)
		if err != nil {
			return nil, fmt.Errorf("agent.status: %w", err)
		}

		return status, nil
	}
}

// respawnParams are the JSON-RPC parameters for agent.respawn.
type respawnParams struct {
	AgentName    string `json:"agent_name"`
	RepoName     string `json:"repo_name"`
	RepoRoot     string `json:"repo_root"`
	WorktreeBase string `json:"worktree_base,omitempty"`
	AgentCmd     string `json:"agent_cmd,omitempty"`
	PrimeText    string `json:"prime_text,omitempty"`
}

// handleAgentRespawn returns a Handler that preserves work and respawns an agent.
func handleAgentRespawn(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p respawnParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		agentCmd := p.AgentCmd
		if agentCmd == "" {
			agentCmd = spawn.DefaultAgentCmd
		}

		result, err := respawn.Respawn(context.Background(), respawn.Params{
			AgentName:    p.AgentName,
			RepoName:     p.RepoName,
			RepoRoot:     p.RepoRoot,
			WorktreeBase: p.WorktreeBase,
			AgentCmd:     agentCmd,
			PrimeText:    p.PrimeText,
		}, store)
		if err != nil {
			return nil, fmt.Errorf("agent.respawn: %w", err)
		}

		return result, nil
	}
}

// listParams are the JSON-RPC parameters for agent.list.
type listParams struct {
	RepoName string `json:"repo_name"`
}

// handleAgentList returns a Handler that lists all registered agents for a repo.
func handleAgentList(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p listParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		if p.RepoName == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "repo_name is required"}
		}

		cat := "agent"
		tuples, err := store.FindAll(&cat, &p.RepoName, nil, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("agent.list: %w", err)
		}

		type agentEntry struct {
			Name    string `json:"name"`
			Payload string `json:"payload"`
		}
		agents := make([]agentEntry, 0, len(tuples))
		for _, t := range tuples {
			name, _ := t["identity"].(string)
			payload, _ := t["payload"].(string)
			agents = append(agents, agentEntry{Name: name, Payload: payload})
		}

		return agents, nil
	}
}

// pruneParams are the JSON-RPC parameters for agent.prune.
type pruneParams struct {
	RepoName     string `json:"repo_name"`
	RepoRoot     string `json:"repo_root"`
	WorktreeBase string `json:"worktree_base"`
	BranchAgeMs  int    `json:"branch_age_ms,omitempty"`
}

// handleAgentPrune returns a Handler that garbage-collects dead agent resources.
func handleAgentPrune(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p pruneParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		var branchAge time.Duration
		if p.BranchAgeMs > 0 {
			branchAge = time.Duration(p.BranchAgeMs) * time.Millisecond
		}

		result, err := prune.Prune(context.Background(), prune.Params{
			RepoName:     p.RepoName,
			RepoRoot:     p.RepoRoot,
			WorktreeBase: p.WorktreeBase,
			BranchAge:    branchAge,
		}, store)
		if err != nil {
			return nil, fmt.Errorf("agent.prune: %w", err)
		}

		return result, nil
	}
}

// showParams are the JSON-RPC parameters for agent.show.
type showParams struct {
	AgentName string `json:"agent_name"`
	RepoName  string `json:"repo_name"`
}

// showResult combines registration data with liveness info.
type showResult struct {
	Name          string `json:"name"`
	Role          string `json:"role"`
	Status        string `json:"status"`
	ActualStatus  string `json:"actual_status"`
	SessionExists bool   `json:"session_exists"`
	TmuxSession   string `json:"tmux_session"`
	Worktree      string `json:"worktree"`
	Branch        string `json:"branch"`
	Task          string `json:"task"`
	EpicID        string `json:"epic_id,omitempty"`
}

// handleAgentShow returns a Handler that retrieves detailed agent information.
func handleAgentShow(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p showParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		if p.AgentName == "" || p.RepoName == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "agent_name and repo_name are required"}
		}

		// Look up the agent registration tuple.
		cat := "agent"
		tuples, err := store.FindAll(&cat, &p.RepoName, &p.AgentName, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("agent.show: %w", err)
		}
		if len(tuples) == 0 {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: fmt.Sprintf("agent %q not found in repo %q", p.AgentName, p.RepoName)}
		}

		payload, _ := tuples[0]["payload"].(string)
		var info struct {
			Role        string `json:"role"`
			Status      string `json:"status"`
			TmuxSession string `json:"tmuxSession"`
			Worktree    string `json:"worktree"`
			Branch      string `json:"branch"`
			Task        string `json:"task"`
			EpicID      string `json:"epicId"`
		}
		json.Unmarshal([]byte(payload), &info)

		// Get liveness info.
		actualStatus, err := dismiss.GetActualStatus(store, p.AgentName, p.RepoName)
		if err != nil {
			return nil, fmt.Errorf("agent.show: %w", err)
		}

		return &showResult{
			Name:          p.AgentName,
			Role:          info.Role,
			Status:        info.Status,
			ActualStatus:  actualStatus.ActualStatus,
			SessionExists: actualStatus.SessionExists,
			TmuxSession:   info.TmuxSession,
			Worktree:      info.Worktree,
			Branch:        info.Branch,
			Task:          info.Task,
			EpicID:        info.EpicID,
		}, nil
	}
}

// stuckParams are the JSON-RPC parameters for agent.stuck.
type stuckParams struct {
	AgentName string `json:"agent_name"`
	RepoName  string `json:"repo_name"`
}

// handleAgentStuck returns a Handler that marks an agent as stuck.
func handleAgentStuck(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p stuckParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		if p.AgentName == "" || p.RepoName == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "agent_name and repo_name are required"}
		}

		// Look up the agent tuple to get existing payload.
		cat := "agent"
		tuples, err := store.FindAll(&cat, &p.RepoName, &p.AgentName, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("agent.stuck: %w", err)
		}
		if len(tuples) == 0 {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: fmt.Sprintf("agent %q not found in repo %q", p.AgentName, p.RepoName)}
		}

		// Parse existing payload and update status.
		payload, _ := tuples[0]["payload"].(string)
		var payloadMap map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &payloadMap); err != nil {
			payloadMap = make(map[string]interface{})
		}
		payloadMap["status"] = "stuck"
		updatedPayload, _ := json.Marshal(payloadMap)

		// Preserve lifecycle and instance from original tuple.
		origInstance, _ := tuples[0]["instance"].(string)
		origLifecycle, _ := tuples[0]["lifecycle"].(string)
		if origLifecycle == "" {
			origLifecycle = "session"
		}

		// Remove old tuple and write updated one.
		store.FindAndDelete(&cat, &p.RepoName, &p.AgentName, nil, nil)
		store.Insert(cat, p.RepoName, p.AgentName, origInstance, string(updatedPayload), origLifecycle, nil, nil, nil)

		return map[string]string{"status": "stuck"}, nil
	}
}
