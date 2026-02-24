// agentbridge.go registers JSON-RPC handlers for agent lifecycle operations.
// Currently provides agent.spawn which orchestrates the full spawn sequence.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chazu/procyon-park/internal/spawn"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// RegisterAgentHandlers wires the agent.* JSON-RPC methods.
// Must be called before the IPCServer is started.
func RegisterAgentHandlers(srv *IPCServer, store *tuplestore.TupleStore) {
	srv.Handle("agent.spawn", handleAgentSpawn(store))
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
