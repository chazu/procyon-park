// Package spawn implements the multi-step agent spawn orchestration.
//
// The spawn sequence creates a fully isolated agent environment:
//
//  1. Validate role (must be a known role like "cub")
//  2. Allocate a unique name from the name pool (via TupleStore)
//  3. Detect epic context (base branch becomes feature/{epicID} if task has parent epic)
//  4. Generate a branch name following agent/{name}/{taskID} convention
//  5. Create a git worktree on that branch
//  6. Create a tmux session with environment variables injected
//  7. Launch the agent command in the tmux session
//  8. Wait for the shell prompt to be ready
//  9. Send the priming instruction
//  10. Register the agent in the tuplespace
//
// Entry points: [Spawn] for full params and [SpawnInRepo] for repo-scoped convenience.
package spawn

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/chazu/procyon-park/internal/git"
	"github.com/chazu/procyon-park/internal/tmux"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// DefaultAgentCmd is a sentinel value that, when set as AgentCmd, causes Spawn
// to construct a default command: cd <worktree> && claude --dangerously-skip-permissions
const DefaultAgentCmd = "__default__"

// validRoles is the set of recognized agent roles.
var validRoles = map[string]bool{
	"cub":  true,
	"king": true,
}

// Params holds the parameters for spawning an agent.
type Params struct {
	// Role is the agent's role (e.g., "cub"). Required.
	Role string

	// TaskID is the beads issue ID the agent will work on. Required.
	TaskID string

	// BaseBranch is the git branch to base the worktree on (e.g., "main").
	// Overridden by EpicID if set. Required.
	BaseBranch string

	// RepoName is the repository name used for tuplespace scoping and tmux
	// session naming. Required.
	RepoName string

	// RepoRoot is the absolute path to the git repository. Required.
	RepoRoot string

	// WorktreeBase is the directory under which agent worktrees are created.
	// Defaults to {RepoRoot}/../worktrees/{RepoName} if empty.
	WorktreeBase string

	// EpicID, if set, overrides BaseBranch with feature/{EpicID}.
	// This enables epic-aware branching where sibling tasks share a feature branch.
	EpicID string

	// AgentCmd is the command to launch in the tmux session (e.g., "claude").
	// If empty, no command is launched (useful for testing).
	AgentCmd string

	// PrimeText is the priming instruction sent after the agent command starts.
	// If empty, no priming is sent.
	PrimeText string

	// PromptWait is how long to wait for the shell/agent prompt after launching
	// the command. Defaults to 2 seconds if zero.
	PromptWait time.Duration
}

// Result holds the outputs of a successful spawn.
type Result struct {
	AgentName   string `json:"agent_name"`
	Branch      string `json:"branch"`
	Worktree    string `json:"worktree"`
	TmuxSession string `json:"tmux_session"`
	RepoName    string `json:"repo_name"`
	TaskID      string `json:"task_id"`
	Role        string `json:"role"`
	EpicID      string `json:"epic_id,omitempty"`
}

// Spawn executes the full agent spawn sequence.
//
// On failure, it performs best-effort cleanup of any resources created
// during the sequence (tmux session, worktree, name).
func Spawn(ctx context.Context, p Params, store *tuplestore.TupleStore) (*Result, error) {
	if err := validateParams(p); err != nil {
		return nil, fmt.Errorf("spawn: %w", err)
	}

	if p.PromptWait == 0 {
		p.PromptWait = 2 * time.Second
	}

	// Step 1: Validate role (already done in validateParams)

	// Step 2: Allocate name from pool
	agentName, err := allocateName(store, p.RepoName)
	if err != nil {
		return nil, fmt.Errorf("spawn: allocate name: %w", err)
	}

	// Track cleanup actions for rollback on failure.
	var cleanups []func()
	defer func() {
		if err != nil {
			for i := len(cleanups) - 1; i >= 0; i-- {
				cleanups[i]()
			}
		}
	}()

	// Name release on failure.
	cleanups = append(cleanups, func() {
		releaseName(store, agentName, p.RepoName)
	})

	// Step 3: Detect epic — override base branch if epic is set.
	baseBranch := p.BaseBranch
	if p.EpicID != "" {
		baseBranch = "feature/" + p.EpicID
	}

	// Step 4: Generate branch name.
	branchName, err := git.GenerateBranchName(ctx, p.RepoRoot, agentName, p.TaskID)
	if err != nil {
		return nil, fmt.Errorf("spawn: generate branch: %w", err)
	}

	// Step 5: Create worktree.
	wtBase := p.WorktreeBase
	if wtBase == "" {
		wtBase = filepath.Join(filepath.Dir(p.RepoRoot), "worktrees", p.RepoName)
	}
	worktreePath := filepath.Join(wtBase, agentName)

	err = git.CreateWorktree(ctx, p.RepoRoot, worktreePath, branchName, baseBranch)
	if err != nil {
		return nil, fmt.Errorf("spawn: create worktree: %w", err)
	}
	cleanups = append(cleanups, func() {
		git.RemoveWorktree(context.Background(), p.RepoRoot, worktreePath)
	})

	// Step 6: Create tmux session with env vars.
	sessionName := tmux.SessionName(p.RepoName, agentName)
	env := map[string]string{
		"PP_AGENT_NAME": agentName,
		"PP_AGENT_ROLE": p.Role,
		"PP_REPO":       p.RepoName,
		"PP_TASK":       p.TaskID,
		"PP_WORKTREE":   worktreePath,
		"PP_BRANCH":     branchName,
	}
	if p.EpicID != "" {
		env["PP_EPIC_ID"] = p.EpicID
	}

	err = tmux.CreateSession(sessionName, worktreePath, env)
	if err != nil {
		return nil, fmt.Errorf("spawn: create tmux session: %w", err)
	}
	cleanups = append(cleanups, func() {
		tmux.KillSession(sessionName)
	})

	// Step 7: Launch agent command (if provided).
	agentCmd := p.AgentCmd
	if agentCmd == DefaultAgentCmd {
		agentCmd = fmt.Sprintf("cd %s && claude --dangerously-skip-permissions", worktreePath)
	}
	if agentCmd != "" {
		err = tmux.SendKeys(sessionName, agentCmd)
		if err != nil {
			return nil, fmt.Errorf("spawn: launch agent command: %w", err)
		}

		// Step 8: Wait for prompt/command to initialize.
		time.Sleep(p.PromptWait)
	}

	// Step 9: Send priming instruction (if provided).
	if p.PrimeText != "" {
		err = tmux.SendKeys(sessionName, p.PrimeText)
		if err != nil {
			return nil, fmt.Errorf("spawn: send priming: %w", err)
		}
	}

	// Step 10: Register agent in tuplespace.
	err = registerAgent(store, agentName, p, branchName, worktreePath, sessionName)
	if err != nil {
		return nil, fmt.Errorf("spawn: register agent: %w", err)
	}

	// Success — clear cleanups so they don't run.
	err = nil
	cleanups = nil

	return &Result{
		AgentName:   agentName,
		Branch:      branchName,
		Worktree:    worktreePath,
		TmuxSession: sessionName,
		RepoName:    p.RepoName,
		TaskID:      p.TaskID,
		Role:        p.Role,
		EpicID:      p.EpicID,
	}, nil
}

// SpawnInRepo is a convenience entry point that derives RepoName from the
// repository path's base directory name.
func SpawnInRepo(ctx context.Context, role, taskID, repoName, repoRoot, baseBranch string, store *tuplestore.TupleStore) (*Result, error) {
	return Spawn(ctx, Params{
		Role:       role,
		TaskID:     taskID,
		BaseBranch: baseBranch,
		RepoName:   repoName,
		RepoRoot:   repoRoot,
	}, store)
}

// validateParams checks that all required fields are present and the role is valid.
func validateParams(p Params) error {
	if p.Role == "" {
		return fmt.Errorf("role is required")
	}
	if !validRoles[p.Role] {
		return fmt.Errorf("unknown role %q (valid: %s)", p.Role, validRoleList())
	}
	if p.TaskID == "" {
		return fmt.Errorf("task_id is required")
	}
	if p.BaseBranch == "" {
		return fmt.Errorf("base_branch is required")
	}
	if p.RepoName == "" {
		return fmt.Errorf("repo_name is required")
	}
	if p.RepoRoot == "" {
		return fmt.Errorf("repo_root is required")
	}
	return nil
}

// validRoleList returns a comma-separated list of valid roles for error messages.
func validRoleList() string {
	roles := make([]string, 0, len(validRoles))
	for r := range validRoles {
		roles = append(roles, r)
	}
	return strings.Join(roles, ", ")
}

// allocateName atomically consumes a name from the cubName pool in the tuplespace.
// Falls back to a numeric name (cub-1, cub-2, ...) if the pool is exhausted.
func allocateName(store *tuplestore.TupleStore, repoName string) (string, error) {
	cat := "cubName"
	row, err := store.FindAndDelete(&cat, &repoName, nil, nil, nil)
	if err != nil {
		return "", err
	}
	if row != nil {
		if id, ok := row["identity"].(string); ok && id != "" {
			return id, nil
		}
	}
	// Pool exhausted — generate numeric fallback.
	return fallbackName(store, repoName)
}

// fallbackName generates a numeric name (cub-1, cub-2, ...) that doesn't
// conflict with existing agent registrations.
func fallbackName(store *tuplestore.TupleStore, repoName string) (string, error) {
	cat := "agent"
	agents, err := store.FindAll(&cat, &repoName, nil, nil, nil)
	if err != nil {
		return "", err
	}

	taken := make(map[string]bool)
	for _, a := range agents {
		if id, ok := a["identity"].(string); ok {
			taken[id] = true
		}
	}

	for n := 1; n < 1000; n++ {
		candidate := fmt.Sprintf("cub-%d", n)
		if !taken[candidate] {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("all numeric fallback names exhausted")
}

// releaseName returns a name to the pool. Only standard pool names are returned;
// numeric fallback names are silently discarded.
func releaseName(store *tuplestore.TupleStore, name, repoName string) {
	// Only return pool names (non-numeric). Check if it looks like cub-N.
	if strings.HasPrefix(name, "cub-") {
		return
	}
	payload := "{}"
	lifecycle := "session"
	store.Insert("cubName", repoName, name, "", payload, lifecycle, nil, nil, nil)
}

// registerAgent writes an agent tuple to the tuplespace.
func registerAgent(store *tuplestore.TupleStore, name string, p Params, branch, worktree, session string) error {
	cat := "agent"
	// Remove existing registration if any.
	existing, _ := store.FindAll(&cat, &p.RepoName, &name, nil, nil)
	for range existing {
		store.FindAndDelete(&cat, &p.RepoName, &name, nil, nil)
	}

	payload := map[string]interface{}{
		"role":        p.Role,
		"status":      "active",
		"tmuxSession": session,
		"worktree":    worktree,
		"branch":      branch,
		"task":        p.TaskID,
	}
	if p.EpicID != "" {
		payload["epicId"] = p.EpicID
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = store.Insert("agent", p.RepoName, name, "local", string(payloadJSON), "session", nil, nil, nil)
	return err
}
