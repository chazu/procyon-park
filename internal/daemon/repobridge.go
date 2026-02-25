// repobridge.go registers JSON-RPC handlers for repository management.
// Provides repo.register, repo.unregister, repo.list, repo.status.
//
// Repositories are stored in a JSON-based registry at ~/.procyon-park/repos.json.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chazu/procyon-park/internal/registry"
)

// RegisterRepoHandlers wires the repo.* JSON-RPC methods.
// Must be called before the IPCServer is started.
func RegisterRepoHandlers(srv *IPCServer, reg *registry.Registry) {
	srv.Handle("repo.register", handleRepoRegister(reg))
	srv.Handle("repo.unregister", handleRepoUnregister(reg))
	srv.Handle("repo.list", handleRepoList(reg))
	srv.Handle("repo.status", handleRepoStatus(reg))
}

// repoRegisterParams are the JSON-RPC parameters for repo.register.
type repoRegisterParams struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// handleRepoRegister adds a repo to the registry.
func handleRepoRegister(reg *registry.Registry) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p repoRegisterParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}
		if p.Path == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "path is required"}
		}

		ctx := context.Background()
		repo, err := reg.Add(ctx, p.Name, p.Path)
		if err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: err.Error()}
		}

		return repo, nil
	}
}

// repoUnregisterParams are the JSON-RPC parameters for repo.unregister.
type repoUnregisterParams struct {
	Name string `json:"name"`
}

// handleRepoUnregister removes a repo from the registry.
func handleRepoUnregister(reg *registry.Registry) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p repoUnregisterParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}
		if p.Name == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "name is required"}
		}

		if err := reg.Remove(p.Name); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: err.Error()}
		}

		return map[string]string{"status": "unregistered", "name": p.Name}, nil
	}
}

// handleRepoList returns all registered repositories.
func handleRepoList(reg *registry.Registry) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		repos, err := reg.List()
		if err != nil {
			return nil, fmt.Errorf("repo.list: %w", err)
		}
		if repos == nil {
			repos = []registry.Repo{}
		}
		return repos, nil
	}
}

// repoStatusParams are the optional JSON-RPC parameters for repo.status.
type repoStatusParams struct {
	Name string `json:"name"`
}

// repoStatusEntry is the JSON response for repo.status.
type repoStatusEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	MainBranch string `json:"main_branch"`
	HasBeads   bool   `json:"has_beads"`
	Status     string `json:"status"`
	Warning    string `json:"warning,omitempty"`
}

// handleRepoStatus returns status for one or all repos with staleness checks.
func handleRepoStatus(reg *registry.Registry) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p repoStatusParams
		if len(params) > 0 && string(params) != "null" {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
			}
		}

		ctx := context.Background()

		if p.Name != "" {
			repo, err := reg.Get(p.Name)
			if err != nil {
				return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: err.Error()}
			}
			warnings, _ := reg.CheckStaleness(ctx)
			warning := ""
			for _, w := range warnings {
				if w.Name == repo.Name {
					warning = w.Warning
					break
				}
			}
			status := "ok"
			if warning != "" {
				status = "stale"
			}
			return []repoStatusEntry{{
				Name:       repo.Name,
				Path:       repo.Path,
				MainBranch: repo.MainBranch,
				HasBeads:   repo.HasBeads,
				Status:     status,
				Warning:    warning,
			}}, nil
		}

		repos, err := reg.List()
		if err != nil {
			return nil, fmt.Errorf("repo.status: %w", err)
		}

		warnings, _ := reg.CheckStaleness(ctx)
		warningMap := map[string]string{}
		for _, w := range warnings {
			warningMap[w.Name] = w.Warning
		}

		entries := make([]repoStatusEntry, 0, len(repos))
		for _, repo := range repos {
			w := warningMap[repo.Name]
			status := "ok"
			if w != "" {
				status = "stale"
			}
			entries = append(entries, repoStatusEntry{
				Name:       repo.Name,
				Path:       repo.Path,
				MainBranch: repo.MainBranch,
				HasBeads:   repo.HasBeads,
				Status:     status,
				Warning:    w,
			})
		}
		return entries, nil
	}
}
