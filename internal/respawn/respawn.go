// Package respawn implements crash recovery by preserving an agent's work and
// re-spawning a fresh agent to continue from the preserved branch.
//
// The respawn sequence:
//
//  1. Validate the agent exists in the tuplespace
//  2. Check that the agent is NOT actively running (must be dead or stopped)
//  3. Preserve work: commit any uncommitted changes and push the branch
//  4. Kill the tmux session (if still lingering)
//  5. Release the agent name back to the pool
//  6. Remove the old worktree
//  7. Unregister the old agent from the tuplespace
//  8. Spawn a new agent with BaseBranch set to the preserved branch
//
// The key invariant: work is always preserved on the pushed branch before any
// cleanup occurs. If preservation fails, the respawn aborts.
package respawn

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chazu/procyon-park/internal/git"
	"github.com/chazu/procyon-park/internal/spawn"
	"github.com/chazu/procyon-park/internal/tmux"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// Params holds the parameters for respawning an agent.
type Params struct {
	// AgentName is the name of the crashed/dead agent to respawn. Required.
	AgentName string

	// RepoName is the repository name used for tuplespace scoping. Required.
	RepoName string

	// RepoRoot is the absolute path to the main git repository. Required.
	RepoRoot string

	// WorktreeBase is the directory under which agent worktrees are created.
	// Passed through to spawn. Optional.
	WorktreeBase string

	// AgentCmd is the command to launch in the new tmux session. Optional.
	AgentCmd string

	// PrimeText is the priming instruction for the new agent. Optional.
	PrimeText string
}

// Result holds the outputs of a successful respawn.
type Result struct {
	// OldBranch is the branch that was preserved from the crashed agent.
	OldBranch string `json:"old_branch"`

	// WorkPreserved indicates whether WIP was committed during preservation.
	WorkPreserved bool `json:"work_preserved"`

	// SpawnResult is the result from spawning the new agent.
	SpawnResult *spawn.Result `json:"spawn_result"`
}

// agentInfo mirrors the agent payload stored in the tuplespace.
type agentInfo struct {
	Role        string `json:"role"`
	Status      string `json:"status"`
	TmuxSession string `json:"tmuxSession"`
	Worktree    string `json:"worktree"`
	Branch      string `json:"branch"`
	Task        string `json:"task"`
	EpicID      string `json:"epicId"`
}

// Respawn preserves an agent's work and spawns a fresh replacement.
//
// The agent must not be actively running (tmux session must be dead).
// If the agent has uncommitted work, it is committed and pushed before cleanup.
func Respawn(ctx context.Context, p Params, store *tuplestore.TupleStore) (*Result, error) {
	if err := validateParams(p); err != nil {
		return nil, fmt.Errorf("respawn: %w", err)
	}

	// Step 1: Look up agent in tuplespace.
	info, err := lookupAgent(store, p.AgentName, p.RepoName)
	if err != nil {
		return nil, fmt.Errorf("respawn: %w", err)
	}

	// Step 2: Verify agent is not actively running.
	if tmux.SessionExists(info.TmuxSession) {
		return nil, fmt.Errorf("respawn: agent %q is still active (session %s exists); dismiss it first",
			p.AgentName, info.TmuxSession)
	}

	// Step 3: Preserve work — commit WIP and push the branch.
	workPreserved, err := preserveWork(ctx, info)
	if err != nil {
		return nil, fmt.Errorf("respawn: preserve work: %w", err)
	}

	// Step 4: Kill tmux session if it somehow still lingers.
	// (Defensive — we checked above, but another process could have restarted it.)
	if tmux.SessionExists(info.TmuxSession) {
		tmux.KillSession(info.TmuxSession)
	}

	// Step 5: Release name back to pool.
	releaseName(store, p.AgentName, p.RepoName)

	// Step 6: Remove old worktree (best-effort).
	if info.Worktree != "" && git.IsValidWorktree(info.Worktree) {
		_ = git.RemoveWorktree(ctx, p.RepoRoot, info.Worktree)
	}

	// Step 7: Unregister old agent.
	unregisterAgent(store, p.AgentName, p.RepoName)

	// Step 8: Spawn a new agent with BaseBranch = preserved branch.
	spawnResult, err := spawn.Spawn(ctx, spawn.Params{
		Role:         info.Role,
		TaskID:       info.Task,
		BaseBranch:   info.Branch,
		RepoName:     p.RepoName,
		RepoRoot:     p.RepoRoot,
		WorktreeBase: p.WorktreeBase,
		EpicID:       info.EpicID,
		AgentCmd:     p.AgentCmd,
		PrimeText:    p.PrimeText,
	}, store)
	if err != nil {
		return nil, fmt.Errorf("respawn: spawn replacement: %w", err)
	}

	return &Result{
		OldBranch:     info.Branch,
		WorkPreserved: workPreserved,
		SpawnResult:   spawnResult,
	}, nil
}

// preserveWork commits any uncommitted changes in the agent's worktree and
// pushes the branch. Returns true if WIP was committed.
func preserveWork(ctx context.Context, info *agentInfo) (bool, error) {
	if info.Worktree == "" || !git.IsValidWorktree(info.Worktree) {
		// No worktree to preserve — branch may still have committed work.
		return false, nil
	}

	dirty, err := git.HasUncommittedChanges(ctx, info.Worktree)
	if err != nil {
		return false, fmt.Errorf("check uncommitted: %w", err)
	}

	if dirty {
		msg := fmt.Sprintf("%s: WIP preserved by respawn", info.Task)
		if err := git.CommitAll(ctx, info.Worktree, msg); err != nil {
			return false, fmt.Errorf("commit WIP: %w", err)
		}
	}

	// Push the branch to preserve all commits.
	if err := git.PushBranch(ctx, info.Worktree, info.Branch); err != nil {
		// Push failure is not fatal — work is still on the local branch.
		// The branch itself is preserved (we don't delete it).
		_ = err
	}

	return dirty, nil
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

// releaseName returns a name to the pool. Numeric fallback names (cub-N)
// are silently discarded since they aren't pool names.
func releaseName(store *tuplestore.TupleStore, name, repoName string) {
	if len(name) >= 4 && name[:4] == "cub-" {
		return
	}
	store.Insert("cubName", repoName, name, "", "{}", "session", nil, nil, nil)
}

// unregisterAgent removes all agent tuples for the given name.
func unregisterAgent(store *tuplestore.TupleStore, agentName, repoName string) {
	cat := "agent"
	for {
		_, err := store.FindAndDelete(&cat, &repoName, &agentName, nil, nil)
		if err != nil {
			break
		}
		remaining, _ := store.FindAll(&cat, &repoName, &agentName, nil, nil)
		if len(remaining) == 0 {
			break
		}
	}
}
