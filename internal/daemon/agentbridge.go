// agentbridge.go registers JSON-RPC handlers for agent lifecycle operations.
// Provides agent.spawn, agent.dismiss, and agent.status.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chazu/procyon-park/internal/dismiss"
	"github.com/chazu/procyon-park/internal/spawn"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// RegisterAgentHandlers wires the agent.* JSON-RPC methods.
// Must be called before the IPCServer is started.
func RegisterAgentHandlers(srv *IPCServer, store *tuplestore.TupleStore) {
	srv.Handle("agent.spawn", handleAgentSpawn(store))
	srv.Handle("agent.dismiss", handleAgentDismiss(store))
	srv.Handle("agent.status", handleAgentStatus(store))
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

		result, err := spawn.Spawn(context.Background(), spawn.Params{
			Role:         p.Role,
			TaskID:       p.TaskID,
			BaseBranch:   p.BaseBranch,
			RepoName:     p.RepoName,
			RepoRoot:     p.RepoRoot,
			WorktreeBase: p.WorktreeBase,
			EpicID:       p.EpicID,
			AgentCmd:     p.AgentCmd,
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
