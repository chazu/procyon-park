// repobridge.go registers JSON-RPC handlers for repository management.
// Provides repo.register, repo.unregister, repo.list, repo.status.
//
// Repositories are stored as tuples in the TupleStore with category "repo"
// and lifecycle "furniture" so they survive session cleanup.
package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chazu/procyon-park/internal/tuplestore"
)

// RegisterRepoHandlers wires the repo.* JSON-RPC methods.
// Must be called before the IPCServer is started.
func RegisterRepoHandlers(srv *IPCServer, store *tuplestore.TupleStore) {
	srv.Handle("repo.register", handleRepoRegister(store))
	srv.Handle("repo.unregister", handleRepoUnregister(store))
	srv.Handle("repo.list", handleRepoList(store))
	srv.Handle("repo.status", handleRepoStatus(store))
}

// repoRegisterParams are the JSON-RPC parameters for repo.register.
type repoRegisterParams struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// handleRepoRegister stores a repo tuple.
func handleRepoRegister(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p repoRegisterParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}
		if p.Name == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "name is required"}
		}
		if p.Path == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "path is required"}
		}

		absPath, err := filepath.Abs(p.Path)
		if err != nil {
			return nil, fmt.Errorf("resolve path: %w", err)
		}

		// Check if already registered.
		cat := "repo"
		existing, err := store.FindAll(&cat, nil, &p.Name, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("repo.register: check existing: %w", err)
		}
		if len(existing) > 0 {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: fmt.Sprintf("repository %q is already registered", p.Name)}
		}

		payload := fmt.Sprintf(`{"path":%q}`, absPath)
		id, err := store.Insert("repo", "", p.Name, "local", payload, "furniture", nil, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("repo.register: %w", err)
		}

		return map[string]interface{}{
			"id":   id,
			"name": p.Name,
			"path": absPath,
		}, nil
	}
}

// repoUnregisterParams are the JSON-RPC parameters for repo.unregister.
type repoUnregisterParams struct {
	Name string `json:"name"`
}

// handleRepoUnregister removes a repo tuple.
func handleRepoUnregister(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p repoUnregisterParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}
		if p.Name == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "name is required"}
		}

		cat := "repo"
		row, err := store.FindAndDelete(&cat, nil, &p.Name, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("repo.unregister: %w", err)
		}
		if row == nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: fmt.Sprintf("repository %q not found", p.Name)}
		}

		return map[string]string{"status": "unregistered", "name": p.Name}, nil
	}
}

// handleRepoList returns all registered repositories.
func handleRepoList(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		cat := "repo"
		rows, err := store.FindAll(&cat, nil, nil, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("repo.list: %w", err)
		}

		type repoEntry struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		repos := make([]repoEntry, 0, len(rows))
		for _, row := range rows {
			name, _ := row["identity"].(string)
			path := extractRepoPath(row)
			repos = append(repos, repoEntry{Name: name, Path: path})
		}

		return repos, nil
	}
}

// repoStatusParams are the optional JSON-RPC parameters for repo.status.
type repoStatusParams struct {
	Name string `json:"name"`
}

// handleRepoStatus returns status for one or all repos.
func handleRepoStatus(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p repoStatusParams
		if len(params) > 0 && string(params) != "null" {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
			}
		}

		cat := "repo"
		var namePtr *string
		if p.Name != "" {
			namePtr = &p.Name
		}
		rows, err := store.FindAll(&cat, nil, namePtr, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("repo.status: %w", err)
		}

		if p.Name != "" && len(rows) == 0 {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: fmt.Sprintf("repository %q not found", p.Name)}
		}

		type repoStatus struct {
			Name   string `json:"name"`
			Path   string `json:"path"`
			Status string `json:"status"`
		}
		statuses := make([]repoStatus, 0, len(rows))
		for _, row := range rows {
			name, _ := row["identity"].(string)
			path := extractRepoPath(row)
			status := checkRepoStatus(path)
			statuses = append(statuses, repoStatus{Name: name, Path: path, Status: status})
		}

		return statuses, nil
	}
}

// extractRepoPath gets the path from a repo tuple's payload.
func extractRepoPath(row map[string]interface{}) string {
	payloadStr, _ := row["payload"].(string)
	if payloadStr == "" {
		return ""
	}
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		return ""
	}
	return payload.Path
}

// checkRepoStatus returns a simple status string for a repo path.
func checkRepoStatus(path string) string {
	if path == "" {
		return "unknown"
	}
	info, err := os.Stat(path)
	if err != nil {
		return "missing"
	}
	if !info.IsDir() {
		return "invalid"
	}
	// Check for .git directory.
	gitPath := filepath.Join(path, ".git")
	if _, err := os.Stat(gitPath); err != nil {
		return "not-git"
	}
	return "ok"
}
