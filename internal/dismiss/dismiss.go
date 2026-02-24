// Package dismiss implements the agent dismiss sequence and liveness detection.
//
// The dismiss sequence tears down an agent environment in the reverse order
// of spawn, with work preservation as the primary invariant:
//
//  1. Look up agent in tuplespace to get session, worktree, branch info
//  2. Kill the tmux session
//  3. Merge the agent's branch to the target (feature branch if epic, else main) with --no-ff
//  4. If the merge fails, abort the entire dismiss — work is preserved on the branch
//  5. Release the agent's name back to the pool
//  6. Remove the worktree
//  7. Delete the branch (only after successful merge)
//  8. Unregister the agent from the tuplespace
//
// Entry points: [Dismiss] for full params and [GetActualStatus] for liveness detection.
package dismiss

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chazu/procyon-park/internal/git"
	"github.com/chazu/procyon-park/internal/tmux"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// Params holds the parameters for dismissing an agent.
type Params struct {
	// AgentName is the name of the agent to dismiss. Required.
	AgentName string

	// RepoName is the repository name used for tuplespace scoping. Required.
	RepoName string

	// RepoRoot is the absolute path to the main git repository. Required.
	RepoRoot string
}

// agentInfo holds the agent registration data retrieved from the tuplespace.
type agentInfo struct {
	Role        string `json:"role"`
	Status      string `json:"status"`
	TmuxSession string `json:"tmuxSession"`
	Worktree    string `json:"worktree"`
	Branch      string `json:"branch"`
	Task        string `json:"task"`
	EpicID      string `json:"epicId"`
}

// Dismiss executes the full agent dismiss sequence.
//
// The merge-before-cleanup invariant ensures that work is never lost: if the
// merge fails, the dismiss aborts and the agent's branch is left intact.
func Dismiss(ctx context.Context, p Params, store *tuplestore.TupleStore) error {
	if err := validateParams(p); err != nil {
		return fmt.Errorf("dismiss: %w", err)
	}

	// Step 1: Look up agent in tuplespace.
	info, err := lookupAgent(store, p.AgentName, p.RepoName)
	if err != nil {
		return fmt.Errorf("dismiss: %w", err)
	}

	// Step 2: Kill tmux session.
	if tmux.SessionExists(info.TmuxSession) {
		if err := tmux.KillSession(info.TmuxSession); err != nil {
			return fmt.Errorf("dismiss: kill session %s: %w", info.TmuxSession, err)
		}
	}

	// Step 3: Merge branch to target.
	target := mergeTarget(info.EpicID)
	if err := git.MergeBranch(ctx, p.RepoRoot, info.Branch, target); err != nil {
		// Step 4: Merge failed — abort dismiss to preserve work.
		return fmt.Errorf("dismiss: merge %s into %s failed (work preserved on branch): %w",
			info.Branch, target, err)
	}

	// Merge succeeded — proceed with cleanup. From here, errors are best-effort.

	// Step 5: Release name back to pool.
	releaseName(store, p.AgentName, p.RepoName)

	// Step 6: Remove worktree.
	if info.Worktree != "" {
		if err := git.RemoveWorktree(ctx, p.RepoRoot, info.Worktree); err != nil {
			// Best-effort: log but don't fail.
			_ = err
		}
	}

	// Step 7: Delete branch (safe — merge was successful).
	if info.Branch != "" {
		if err := git.DeleteBranch(ctx, p.RepoRoot, info.Branch); err != nil {
			// Best-effort: branch may already be gone.
			_ = err
		}
	}

	// Step 8: Unregister agent from tuplespace.
	unregisterAgent(store, p.AgentName, p.RepoName)

	return nil
}

// ActualStatus represents the real status of an agent after liveness checking.
type ActualStatus struct {
	// StoredStatus is the status recorded in the tuplespace.
	StoredStatus string `json:"stored_status"`

	// ActualStatus is the status after checking liveness.
	// If the stored status is "active" but the tmux session is gone,
	// this will be "dead".
	ActualStatus string `json:"actual_status"`

	// SessionExists indicates whether the tmux session is running.
	SessionExists bool `json:"session_exists"`
}

// GetActualStatus checks the real status of an agent by comparing the stored
// tuplespace status with tmux session liveness.
//
// If the stored status is "active" but the tmux session no longer exists,
// the actual status is "dead". Otherwise, the actual status matches the
// stored status.
func GetActualStatus(store *tuplestore.TupleStore, agentName, repoName string) (*ActualStatus, error) {
	info, err := lookupAgent(store, agentName, repoName)
	if err != nil {
		return nil, fmt.Errorf("status: %w", err)
	}

	sessionAlive := tmux.SessionExists(info.TmuxSession)

	actual := info.Status
	if info.Status == "active" && !sessionAlive {
		actual = "dead"
	}

	return &ActualStatus{
		StoredStatus:  info.Status,
		ActualStatus:  actual,
		SessionExists: sessionAlive,
	}, nil
}

// validateParams checks that all required fields are present.
func validateParams(p Params) error {
	if p.AgentName == "" {
		return fmt.Errorf("agent_name is required")
	}
	if p.RepoName == "" {
		return fmt.Errorf("repo_name is required")
	}
	if p.RepoRoot == "" {
		return fmt.Errorf("repo_root is required")
	}
	return nil
}

// lookupAgent retrieves agent registration from the tuplespace.
func lookupAgent(store *tuplestore.TupleStore, agentName, repoName string) (*agentInfo, error) {
	cat := "agent"
	row, err := store.FindOne(&cat, &repoName, &agentName, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("lookup agent %s: %w", agentName, err)
	}
	if row == nil {
		return nil, fmt.Errorf("agent %q not found in repo %q", agentName, repoName)
	}

	payload, ok := row["payload"].(string)
	if !ok {
		return nil, fmt.Errorf("agent %q has invalid payload", agentName)
	}

	var info agentInfo
	if err := json.Unmarshal([]byte(payload), &info); err != nil {
		return nil, fmt.Errorf("agent %q payload parse: %w", agentName, err)
	}
	return &info, nil
}

// mergeTarget returns the branch to merge into. If the agent has an epic,
// the target is the epic's feature branch; otherwise it's main.
func mergeTarget(epicID string) string {
	if epicID != "" {
		return "feature/" + epicID
	}
	return "main"
}

// releaseName returns a name to the pool. Only standard pool names are returned;
// numeric fallback names (cub-N) are silently discarded.
func releaseName(store *tuplestore.TupleStore, name, repoName string) {
	if strings.HasPrefix(name, "cub-") {
		return
	}
	payload := "{}"
	lifecycle := "session"
	store.Insert("cubName", repoName, name, "", payload, lifecycle, nil, nil, nil)
}

// unregisterAgent removes an agent's registration from the tuplespace.
func unregisterAgent(store *tuplestore.TupleStore, agentName, repoName string) {
	cat := "agent"
	for {
		_, err := store.FindAndDelete(&cat, &repoName, &agentName, nil, nil)
		if err != nil {
			break
		}
		// Keep deleting in case of duplicates.
		remaining, _ := store.FindAll(&cat, &repoName, &agentName, nil, nil)
		if len(remaining) == 0 {
			break
		}
	}
}
