// primebridge.go registers the system.prime JSON-RPC handler.
// It wires the prime pipeline to the daemon's IPC dispatch.
package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/chazu/procyon-park/internal/prime"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// RegisterPrimeHandlers wires the system.* JSON-RPC methods.
// Must be called before the IPCServer is started.
func RegisterPrimeHandlers(srv *IPCServer, store *tuplestore.TupleStore) {
	srv.Handle("system.prime", handleSystemPrime(store))
}

// primeParams are the JSON-RPC parameters for system.prime.
type primeParams struct {
	Role      string `json:"role"`
	AgentName string `json:"agent_name,omitempty"`
	Repo      string `json:"repo,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Worktree  string `json:"worktree,omitempty"`
}

// handleSystemPrime returns a Handler that runs the prime pipeline and
// returns rendered instructions as a JSON string.
func handleSystemPrime(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p primeParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		if p.Role == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "role is required"}
		}

		cfg := prime.PipelineConfig{
			Role: p.Role,
			Data: prime.TemplateData{
				Role:      p.Role,
				AgentName: p.AgentName,
				Repo:      p.Repo,
				TaskID:    p.TaskID,
				Branch:    p.Branch,
				Worktree:  p.Worktree,
				EnvPrefix: "PP",
			},
			Scope:  p.Repo,
			TaskID: p.TaskID,
			Store:  store,
		}

		instructions, err := prime.RunPipeline(cfg)
		if err != nil {
			return nil, fmt.Errorf("system.prime: %w", err)
		}

		return instructions, nil
	}
}
