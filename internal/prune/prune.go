// Package prune implements garbage collection of dead agent resources.
//
// The prune sequence:
//
//  1. Run git worktree prune on the repository
//  2. Find all registered agents in the tuplespace
//  3. For each agent, check tmux liveness
//  4. Dead agents: remove worktree, optionally delete branch (if older than threshold),
//     release name, unregister from tuplespace
//  5. Find orphaned worktrees (filesystem directories not in tuplestore)
//  6. Remove orphaned worktrees
//  7. Return a summary of actions taken
//
// Branch deletion uses a configurable age threshold (default 24h) to avoid
// deleting branches that may contain recent unmerged work.
package prune

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chazu/procyon-park/internal/git"
	"github.com/chazu/procyon-park/internal/tmux"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// DefaultBranchAge is the default minimum age for a branch to be eligible for
// deletion during pruning. Branches newer than this are preserved.
const DefaultBranchAge = 24 * time.Hour

// Params holds the parameters for pruning agent resources.
type Params struct {
	// RepoName is the repository name used for tuplespace scoping. Required.
	RepoName string

	// RepoRoot is the absolute path to the main git repository. Required.
	RepoRoot string

	// WorktreeBase is the directory where agent worktrees are stored.
	// Used for orphan detection. Required.
	WorktreeBase string

	// BranchAge is the minimum age for a branch to be deleted.
	// Branches newer than this threshold are preserved even if the agent is dead.
	// Defaults to 24 hours if zero.
	BranchAge time.Duration
}

// Result summarizes the actions taken during pruning.
type Result struct {
	// DeadAgents lists the names of agents that were found dead and cleaned up.
	DeadAgents []string `json:"dead_agents"`

	// OrphanedWorktrees lists the paths of orphaned worktrees that were removed.
	OrphanedWorktrees []string `json:"orphaned_worktrees"`

	// PreservedBranches lists branches that were kept because they're too recent.
	PreservedBranches []string `json:"preserved_branches"`

	// DeletedBranches lists branches that were deleted.
	DeletedBranches []string `json:"deleted_branches"`

	// Errors lists non-fatal errors encountered during pruning.
	Errors []string `json:"errors,omitempty"`
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

// Prune performs garbage collection of dead agent resources.
//
// It finds dead agents (registered but without a live tmux session), cleans up
// their worktrees, conditionally deletes old branches, and removes orphaned
// worktrees not associated with any registered agent.
func Prune(ctx context.Context, p Params, store *tuplestore.TupleStore) (*Result, error) {
	if err := validateParams(p); err != nil {
		return nil, fmt.Errorf("prune: %w", err)
	}

	if p.BranchAge == 0 {
		p.BranchAge = DefaultBranchAge
	}

	result := &Result{}

	// Step 1: Run git worktree prune to clean stale references.
	if err := git.PruneWorktrees(ctx, p.RepoRoot); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("git worktree prune: %v", err))
	}

	// Step 2-4: Find and clean up dead agents.
	pruneDeadAgents(ctx, p, store, result)

	// Step 5-6: Find and remove orphaned worktrees.
	pruneOrphanedWorktrees(ctx, p, store, result)

	return result, nil
}

// pruneDeadAgents finds all registered agents, checks liveness, and cleans up dead ones.
func pruneDeadAgents(ctx context.Context, p Params, store *tuplestore.TupleStore, result *Result) {
	cat := "agent"
	agents, err := store.FindAll(&cat, &p.RepoName, nil, nil, nil)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("list agents: %v", err))
		return
	}

	for _, agentRow := range agents {
		agentName, _ := agentRow["identity"].(string)
		if agentName == "" {
			continue
		}

		payload, ok := agentRow["payload"].(string)
		if !ok {
			continue
		}

		var info agentInfo
		if err := json.Unmarshal([]byte(payload), &info); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("parse agent %s: %v", agentName, err))
			continue
		}

		// Check liveness: if tmux session exists, agent is alive — skip.
		if tmux.SessionExists(info.TmuxSession) {
			continue
		}

		// Agent is dead. Clean up.
		result.DeadAgents = append(result.DeadAgents, agentName)

		// Remove worktree (best-effort).
		if info.Worktree != "" && git.IsValidWorktree(info.Worktree) {
			if err := git.RemoveWorktree(ctx, p.RepoRoot, info.Worktree); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("remove worktree %s: %v", agentName, err))
			}
		}

		// Conditionally delete branch based on age.
		if info.Branch != "" && !git.IsProtectedBranch(info.Branch) {
			age, err := git.BranchAge(ctx, p.RepoRoot, info.Branch)
			if err != nil {
				// Can't determine age (branch may already be gone) — preserve it.
				result.Errors = append(result.Errors, fmt.Sprintf("branch age %s: %v", info.Branch, err))
			} else if age >= p.BranchAge {
				if err := git.DeleteBranch(ctx, p.RepoRoot, info.Branch); err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("delete branch %s: %v", info.Branch, err))
				} else {
					result.DeletedBranches = append(result.DeletedBranches, info.Branch)
				}
			} else {
				result.PreservedBranches = append(result.PreservedBranches, info.Branch)
			}
		}

		// Release name back to pool.
		releaseName(store, agentName, p.RepoName)

		// Unregister agent.
		unregisterAgent(store, agentName, p.RepoName)
	}
}

// pruneOrphanedWorktrees finds worktrees on the filesystem that have no matching
// agent registration in the tuplespace.
func pruneOrphanedWorktrees(ctx context.Context, p Params, store *tuplestore.TupleStore, result *Result) {
	orphans, err := git.ListOrphanedWorktrees(ctx, p.WorktreeBase)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("list orphaned worktrees: %v", err))
		return
	}

	for _, orphan := range orphans {
		if err := git.RemoveWorktree(ctx, p.RepoRoot, orphan); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("remove orphan %s: %v", orphan, err))
			continue
		}
		result.OrphanedWorktrees = append(result.OrphanedWorktrees, orphan)
	}
}

// validateParams checks that all required fields are present.
func validateParams(p Params) error {
	if p.RepoName == "" {
		return fmt.Errorf("repo_name is required")
	}
	if p.RepoRoot == "" {
		return fmt.Errorf("repo_root is required")
	}
	if p.WorktreeBase == "" {
		return fmt.Errorf("worktree_base is required")
	}
	return nil
}

// releaseName returns a name to the pool. Numeric fallback names (cub-N)
// are silently discarded.
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
