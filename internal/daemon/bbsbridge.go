// bbsbridge.go registers JSON-RPC handlers that bridge IPC requests to the
// TupleStore. Each method translates JSON-RPC params into TupleStore calls
// and returns the result (or a well-formed error).
package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/chazu/procyon-park/internal/tuplestore"
)

// ErrCodeInvalidParams is the standard JSON-RPC 2.0 error code for invalid method parameters.
const ErrCodeInvalidParams = -32602

// RegisterBBSHandlers wires the tuple.* JSON-RPC methods to the given store.
// Must be called before the IPCServer is started.
func RegisterBBSHandlers(srv *IPCServer, store *tuplestore.TupleStore) {
	srv.Handle("tuple.write", handleTupleWrite(store))
	srv.Handle("tuple.read", handleTupleRead(store))
	srv.Handle("tuple.take", handleTupleTake(store))
	srv.Handle("tuple.scan", handleTupleScan(store))
	srv.Handle("tuple.pulse", handleTuplePulse(store))
}

// --- param structs --------------------------------------------------------

// writeParams are the parameters accepted by tuple.write.
type writeParams struct {
	Category  string  `json:"category"`
	Scope     string  `json:"scope"`
	Identity  string  `json:"identity"`
	Instance  string  `json:"instance"`
	Payload   string  `json:"payload"`
	Lifecycle string  `json:"lifecycle"`
	TaskID    *string `json:"task_id"`
	AgentID   *string `json:"agent_id"`
	TTL       *int    `json:"ttl"`
}

// patternParams are used by tuple.read, tuple.take, and tuple.scan for
// wildcard matching. All fields are optional; nil/absent means "match any".
type patternParams struct {
	Category      *string `json:"category"`
	Scope         *string `json:"scope"`
	Identity      *string `json:"identity"`
	Instance      *string `json:"instance"`
	PayloadSearch *string `json:"payload_search"`
}

// --- handlers -------------------------------------------------------------

// handleTupleWrite inserts a tuple and returns its row id.
func handleTupleWrite(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p writeParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}
		if p.Category == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "category is required"}
		}
		if p.Payload == "" {
			p.Payload = "{}"
		}
		if p.Lifecycle == "" {
			p.Lifecycle = "session"
		}

		id, err := store.Insert(
			p.Category, p.Scope, p.Identity, p.Instance,
			p.Payload, p.Lifecycle,
			p.TaskID, p.AgentID, p.TTL,
		)
		if err != nil {
			return nil, fmt.Errorf("tuple.write: %w", err)
		}
		return map[string]interface{}{"id": id}, nil
	}
}

// handleTupleRead finds the oldest matching tuple without removing it.
// Returns the tuple or null if none matched.
func handleTupleRead(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		p, err := parsePatternParams(params)
		if err != nil {
			return nil, err
		}
		row, err := store.FindOne(p.Category, p.Scope, p.Identity, p.Instance, p.PayloadSearch)
		if err != nil {
			return nil, fmt.Errorf("tuple.read: %w", err)
		}
		return row, nil // nil → JSON null
	}
}

// handleTupleTake atomically finds and deletes the oldest matching tuple.
// Returns the removed tuple or null if none matched.
func handleTupleTake(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		p, err := parsePatternParams(params)
		if err != nil {
			return nil, err
		}
		row, err := store.FindAndDelete(p.Category, p.Scope, p.Identity, p.Instance, p.PayloadSearch)
		if err != nil {
			return nil, fmt.Errorf("tuple.take: %w", err)
		}
		return row, nil
	}
}

// handleTupleScan returns all tuples matching the pattern.
func handleTupleScan(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		p, err := parsePatternParams(params)
		if err != nil {
			return nil, err
		}
		rows, err := store.FindAll(p.Category, p.Scope, p.Identity, p.Instance, p.PayloadSearch)
		if err != nil {
			return nil, fmt.Errorf("tuple.scan: %w", err)
		}
		if rows == nil {
			rows = []map[string]interface{}{}
		}
		return rows, nil
	}
}

// handleTuplePulse drains notification tuples for a specific agent and returns them.
// Params: {"agent_id": "name"}. Returns an array of notification tuples (removed).
func handleTuplePulse(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}
		if p.AgentID == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "agent_id is required"}
		}

		cat := "notification"
		rows, err := store.FindAll(&cat, &p.AgentID, nil, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("tuple.pulse: scan: %w", err)
		}

		// Remove the notifications we found.
		if len(rows) > 0 {
			_, err := store.DeleteByPattern(&cat, &p.AgentID, nil, nil)
			if err != nil {
				return nil, fmt.Errorf("tuple.pulse: delete: %w", err)
			}
		}

		if rows == nil {
			rows = []map[string]interface{}{}
		}
		return rows, nil
	}
}

// --- helpers --------------------------------------------------------------

// parsePatternParams unmarshals pattern params from raw JSON.
// A nil/empty params object is valid (matches everything).
func parsePatternParams(raw json.RawMessage) (*patternParams, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return &patternParams{}, nil
	}
	var p patternParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
	}
	return &p, nil
}

// rpcError is returned by handlers to signal a specific JSON-RPC error code.
// The IPCServer converts it into a JSONRPCError response.
type rpcError struct {
	Code int
	Msg  string
}

func (e *rpcError) Error() string { return e.Msg }
