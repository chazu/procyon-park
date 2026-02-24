// configbridge.go registers JSON-RPC handlers for configuration management.
// Provides config.get, config.set, config.list, config.path.
//
// Configuration key-value pairs are stored as tuples with category "config"
// and lifecycle "furniture".
package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chazu/procyon-park/internal/tuplestore"
)

// RegisterConfigHandlers wires the config.* JSON-RPC methods.
// Must be called before the IPCServer is started.
func RegisterConfigHandlers(srv *IPCServer, store *tuplestore.TupleStore) {
	srv.Handle("config.get", handleConfigGet(store))
	srv.Handle("config.set", handleConfigSet(store))
	srv.Handle("config.list", handleConfigList(store))
	srv.Handle("config.path", handleConfigPath())
}

// configGetParams are the JSON-RPC parameters for config.get.
type configGetParams struct {
	Key string `json:"key"`
}

// handleConfigGet returns the value for a config key.
func handleConfigGet(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p configGetParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}
		if p.Key == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "key is required"}
		}

		cat := "config"
		row, err := store.FindOne(&cat, nil, &p.Key, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("config.get: %w", err)
		}
		if row == nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: fmt.Sprintf("config key %q not found", p.Key)}
		}

		return extractConfigValue(row), nil
	}
}

// configSetParams are the JSON-RPC parameters for config.set.
type configSetParams struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// handleConfigSet upserts a config key-value pair.
func handleConfigSet(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p configSetParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}
		if p.Key == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "key is required"}
		}

		cat := "config"
		// Remove existing value if present (upsert).
		_, _ = store.FindAndDelete(&cat, nil, &p.Key, nil, nil)

		payload := fmt.Sprintf(`{"value":%q}`, p.Value)
		id, err := store.Insert("config", "", p.Key, "local", payload, "furniture", nil, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("config.set: %w", err)
		}

		return map[string]interface{}{
			"id":    id,
			"key":   p.Key,
			"value": p.Value,
		}, nil
	}
}

// handleConfigList returns all config key-value pairs.
func handleConfigList(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		cat := "config"
		rows, err := store.FindAll(&cat, nil, nil, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("config.list: %w", err)
		}

		type configEntry struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		entries := make([]configEntry, 0, len(rows))
		for _, row := range rows {
			key, _ := row["identity"].(string)
			value := extractConfigValue(row)
			entries = append(entries, configEntry{Key: key, Value: value})
		}

		return entries, nil
	}
}

// handleConfigPath returns the path to the config file.
func handleConfigPath() Handler {
	return func(params json.RawMessage) (interface{}, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("config.path: %w", err)
		}
		return filepath.Join(home, ".procyon-park", "config.toml"), nil
	}
}

// extractConfigValue gets the value string from a config tuple's payload.
func extractConfigValue(row map[string]interface{}) string {
	payloadStr, _ := row["payload"].(string)
	if payloadStr == "" {
		return ""
	}
	var payload struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		return payloadStr
	}
	return payload.Value
}
