// querybridge.go registers JSON-RPC handlers for the unified telemetry query
// engine. These handlers bridge CLI commands to QueryEngine operations.
package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/chazu/procyon-park/internal/telemetry"
)

// RegisterQueryHandlers wires the telemetry.query JSON-RPC method.
// Must be called before the IPCServer is started.
func RegisterQueryHandlers(srv *IPCServer, engine *telemetry.QueryEngine) {
	srv.Handle("telemetry.query", handleTelemetryQuery(engine))
}

func handleTelemetryQuery(engine *telemetry.QueryEngine) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var cmd telemetry.QueryCommand
		if err := json.Unmarshal(params, &cmd); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}
		if cmd.Operation == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "operation is required"}
		}
		result, err := engine.HandleQuery(cmd)
		if err != nil {
			return nil, fmt.Errorf("telemetry.query: %w", err)
		}
		return result, nil
	}
}
