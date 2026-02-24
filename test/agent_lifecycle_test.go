// Phase 3 integration tests: agent lifecycle end-to-end.
//
// These tests exercise the full agent lifecycle through the Go packages
// (spawn, dismiss, respawn, prune) using real git repos, worktrees, and tmux
// sessions. They verify that resources are created and cleaned up correctly
// at each stage.
//
// Prerequisites: git and tmux must be available on the test machine.
package test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chazu/procyon-park/internal/dismiss"
	"github.com/chazu/procyon-park/internal/git"
	"github.com/chazu/procyon-park/internal/prune"
	"github.com/chazu/procyon-park/internal/respawn"
	"github.com/chazu/procyon-park/internal/spawn"
	"github.com/chazu/procyon-park/internal/tmux"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// ---------------------------------------------------------------------------
// Test fixtures: temp git repo with initial commit
// ---------------------------------------------------------------------------

// testRepo creates a temporary git repository with an initial commit on main.
// Returns (repoPath, worktreeBase, cleanup). The repo is suitable for creating
// worktrees and branches.
func testRepo(t *testing.T) (string, string) {
	t.Helper()
	base, err := os.MkdirTemp("/tmp", "pp-lifecycle-")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })

	repoPath := filepath.Join(base, "repo")
	wtBase := filepath.Join(base, "worktrees")
	os.MkdirAll(repoPath, 0o755)
	os.MkdirAll(wtBase, 0o755)

	// Init repo with main branch and initial commit.
	cmds := [][]string{
		{"git", "-C", repoPath, "init", "-b", "main"},
		{"git", "-C", repoPath, "config", "user.email", "test@test.com"},
		{"git", "-C", repoPath, "config", "user.name", "Test"},
		{"git", "-C", repoPath, "commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %s: %v", strings.Join(args, " "), out, err)
		}
	}

	return repoPath, wtBase
}

// seedNamePool inserts standard name pool entries into the store for the given repo.
func seedNamePool(t *testing.T, store *tuplestore.TupleStore, repoName string) {
	t.Helper()
	names := []string{"Bramble", "Rustle", "Marble", "Pip", "Jinx"}
	for _, name := range names {
		_, err := store.Insert("cubName", repoName, name, "", "{}", "session", nil, nil, nil)
		if err != nil {
			t.Fatalf("seed name %s: %v", name, err)
		}
	}
}

// lookupAgent returns the agent payload from the store, or nil if not found.
func lookupAgentPayload(t *testing.T, store *tuplestore.TupleStore, agentName, repoName string) map[string]interface{} {
	t.Helper()
	cat := "agent"
	row, err := store.FindOne(&cat, &repoName, &agentName, nil, nil)
	if err != nil || row == nil {
		return nil
	}
	payload, ok := row["payload"].(string)
	if !ok {
		return nil
	}
	var m map[string]interface{}
	json.Unmarshal([]byte(payload), &m)
	return m
}

// countAgents returns the number of registered agents in the store.
func countAgents(t *testing.T, store *tuplestore.TupleStore, repoName string) int {
	t.Helper()
	cat := "agent"
	agents, _ := store.FindAll(&cat, &repoName, nil, nil, nil)
	return len(agents)
}

// countNames returns the number of available names in the pool.
func countNames(t *testing.T, store *tuplestore.TupleStore, repoName string) int {
	t.Helper()
	cat := "cubName"
	names, _ := store.FindAll(&cat, &repoName, nil, nil, nil)
	return len(names)
}

// ---------------------------------------------------------------------------
// Test 1: Spawn creates worktree, branch, tmux session, and registration
// ---------------------------------------------------------------------------

func TestLifecycleSpawnCreatesResources(t *testing.T) {
	repoPath, wtBase := testRepo(t)
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	repoName := "lifecycle-test"
	seedNamePool(t, store, repoName)

	ctx := context.Background()
	result, err := spawn.Spawn(ctx, spawn.Params{
		Role:         "cub",
		TaskID:       "task-abc",
		BaseBranch:   "main",
		RepoName:     repoName,
		RepoRoot:     repoPath,
		WorktreeBase: wtBase,
	}, store)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() {
		tmux.KillSession(result.TmuxSession)
		git.RemoveWorktree(ctx, repoPath, result.Worktree)
		git.DeleteBranch(ctx, repoPath, result.Branch)
	})

	// Verify agent name came from pool.
	if result.AgentName == "" {
		t.Fatal("agent name is empty")
	}

	// Verify branch follows convention.
	if !strings.HasPrefix(result.Branch, "agent/"+result.AgentName+"/task-abc") {
		t.Fatalf("unexpected branch %q", result.Branch)
	}

	// Verify worktree exists.
	if !git.IsValidWorktree(result.Worktree) {
		t.Fatal("worktree not created")
	}

	// Verify tmux session exists.
	if !tmux.SessionExists(result.TmuxSession) {
		t.Fatal("tmux session not created")
	}

	// Verify agent registered in tuplespace.
	payload := lookupAgentPayload(t, store, result.AgentName, repoName)
	if payload == nil {
		t.Fatal("agent not registered in tuplespace")
	}
	if payload["status"] != "active" {
		t.Fatalf("expected status 'active', got %v", payload["status"])
	}
	if payload["branch"] != result.Branch {
		t.Fatalf("payload branch mismatch: %v vs %v", payload["branch"], result.Branch)
	}

	// Verify name pool shrank by 1.
	if n := countNames(t, store, repoName); n != 4 {
		t.Fatalf("expected 4 names in pool, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// Test 2: Agent status/liveness detection
// ---------------------------------------------------------------------------

func TestLifecycleAgentStatus(t *testing.T) {
	repoPath, wtBase := testRepo(t)
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	repoName := "status-test"
	seedNamePool(t, store, repoName)

	ctx := context.Background()
	result, err := spawn.Spawn(ctx, spawn.Params{
		Role:         "cub",
		TaskID:       "task-status",
		BaseBranch:   "main",
		RepoName:     repoName,
		RepoRoot:     repoPath,
		WorktreeBase: wtBase,
	}, store)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() {
		tmux.KillSession(result.TmuxSession)
		git.RemoveWorktree(ctx, repoPath, result.Worktree)
		git.DeleteBranch(ctx, repoPath, result.Branch)
	})

	// Check status while session is alive.
	status, err := dismiss.GetActualStatus(store, result.AgentName, repoName)
	if err != nil {
		t.Fatalf("GetActualStatus: %v", err)
	}
	if status.StoredStatus != "active" {
		t.Fatalf("expected stored status 'active', got %q", status.StoredStatus)
	}
	if status.ActualStatus != "active" {
		t.Fatalf("expected actual status 'active', got %q", status.ActualStatus)
	}
	if !status.SessionExists {
		t.Fatal("session should exist")
	}

	// Kill the tmux session to simulate a crash.
	tmux.KillSession(result.TmuxSession)
	time.Sleep(100 * time.Millisecond)

	// Check status — should detect dead agent.
	status2, err := dismiss.GetActualStatus(store, result.AgentName, repoName)
	if err != nil {
		t.Fatalf("GetActualStatus after kill: %v", err)
	}
	if status2.StoredStatus != "active" {
		t.Fatalf("stored status should still be 'active', got %q", status2.StoredStatus)
	}
	if status2.ActualStatus != "dead" {
		t.Fatalf("actual status should be 'dead', got %q", status2.ActualStatus)
	}
	if status2.SessionExists {
		t.Fatal("session should not exist after kill")
	}
}

// ---------------------------------------------------------------------------
// Test 3: Dismiss merges, cleans up worktree/branch/session, unregisters
// ---------------------------------------------------------------------------

func TestLifecycleDismiss(t *testing.T) {
	repoPath, wtBase := testRepo(t)
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	repoName := "dismiss-test"
	seedNamePool(t, store, repoName)

	ctx := context.Background()
	result, err := spawn.Spawn(ctx, spawn.Params{
		Role:         "cub",
		TaskID:       "task-dismiss",
		BaseBranch:   "main",
		RepoName:     repoName,
		RepoRoot:     repoPath,
		WorktreeBase: wtBase,
	}, store)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	agentName := result.AgentName
	branch := result.Branch
	worktree := result.Worktree
	session := result.TmuxSession

	// Create a file in the worktree and commit so the merge has something to do.
	testFile := filepath.Join(worktree, "test.txt")
	os.WriteFile(testFile, []byte("hello from "+agentName), 0o644)
	git.CommitAll(ctx, worktree, "test commit from "+agentName)

	// Dismiss the agent.
	err = dismiss.Dismiss(ctx, dismiss.Params{
		AgentName: agentName,
		RepoName:  repoName,
		RepoRoot:  repoPath,
	}, store)
	if err != nil {
		t.Fatalf("dismiss: %v", err)
	}

	// Verify tmux session killed.
	if tmux.SessionExists(session) {
		t.Fatal("tmux session should be gone after dismiss")
	}

	// Verify worktree removed.
	if git.IsValidWorktree(worktree) {
		t.Fatal("worktree should be removed after dismiss")
	}

	// Verify agent unregistered.
	if lookupAgentPayload(t, store, agentName, repoName) != nil {
		t.Fatal("agent should be unregistered after dismiss")
	}

	// Verify name returned to pool.
	if n := countNames(t, store, repoName); n != 5 {
		t.Fatalf("expected 5 names in pool (returned), got %d", n)
	}

	// Verify the commit was merged to main.
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "log", "--oneline", "main")
	out, _ := cmd.Output()
	if !strings.Contains(string(out), "test commit from "+agentName) {
		t.Fatal("agent's commit should be merged to main")
	}

	// Verify agent branch was deleted.
	cmd = exec.CommandContext(ctx, "git", "-C", repoPath, "branch", "--list", branch)
	out, _ = cmd.Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("branch %s should be deleted after dismiss", branch)
	}
}

// ---------------------------------------------------------------------------
// Test 4: Respawn preserves work and creates new agent from preserved branch
// ---------------------------------------------------------------------------

func TestLifecycleRespawn(t *testing.T) {
	repoPath, wtBase := testRepo(t)
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	repoName := "respawn-test"
	seedNamePool(t, store, repoName)

	ctx := context.Background()

	// Spawn the original agent.
	result, err := spawn.Spawn(ctx, spawn.Params{
		Role:         "cub",
		TaskID:       "task-respawn",
		BaseBranch:   "main",
		RepoName:     repoName,
		RepoRoot:     repoPath,
		WorktreeBase: wtBase,
	}, store)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	oldName := result.AgentName
	oldBranch := result.Branch
	oldWorktree := result.Worktree

	// Create a file in the worktree and commit work.
	testFile := filepath.Join(oldWorktree, "work.txt")
	os.WriteFile(testFile, []byte("important work"), 0o644)
	git.CommitAll(ctx, oldWorktree, "important work commit")

	// Kill the tmux session to simulate a crash.
	tmux.KillSession(result.TmuxSession)
	time.Sleep(100 * time.Millisecond)

	// Respawn the agent.
	respResult, err := respawn.Respawn(ctx, respawn.Params{
		AgentName:    oldName,
		RepoName:     repoName,
		RepoRoot:     repoPath,
		WorktreeBase: wtBase,
	}, store)
	if err != nil {
		t.Fatalf("respawn: %v", err)
	}
	t.Cleanup(func() {
		tmux.KillSession(respResult.SpawnResult.TmuxSession)
		git.RemoveWorktree(ctx, repoPath, respResult.SpawnResult.Worktree)
		git.DeleteBranch(ctx, repoPath, respResult.SpawnResult.Branch)
		// Old branch might still exist from respawn base.
		git.DeleteBranch(ctx, repoPath, oldBranch)
	})

	// Verify old branch was preserved (it's the base for the new spawn).
	if respResult.OldBranch != oldBranch {
		t.Fatalf("expected old branch %q, got %q", oldBranch, respResult.OldBranch)
	}

	// Verify new agent got spawned with a new name.
	newResult := respResult.SpawnResult
	if newResult.AgentName == "" {
		t.Fatal("new agent name is empty")
	}
	if newResult.TaskID != "task-respawn" {
		t.Fatalf("new agent should have same task, got %q", newResult.TaskID)
	}

	// Verify new tmux session exists.
	if !tmux.SessionExists(newResult.TmuxSession) {
		t.Fatal("new tmux session not created")
	}

	// Verify new worktree exists.
	if !git.IsValidWorktree(newResult.Worktree) {
		t.Fatal("new worktree not created")
	}

	// Verify old agent is unregistered but new one is registered.
	if lookupAgentPayload(t, store, oldName, repoName) != nil {
		t.Fatal("old agent should be unregistered")
	}
	if lookupAgentPayload(t, store, newResult.AgentName, repoName) == nil {
		t.Fatal("new agent should be registered")
	}

	// Verify work was preserved: the new worktree should have the committed file.
	workFile := filepath.Join(newResult.Worktree, "work.txt")
	content, err := os.ReadFile(workFile)
	if err != nil {
		t.Fatalf("work file not found in new worktree: %v", err)
	}
	if string(content) != "important work" {
		t.Fatalf("work file content mismatch: %q", string(content))
	}
}

// ---------------------------------------------------------------------------
// Test 5: Prune cleans up dead agents
// ---------------------------------------------------------------------------

func TestLifecyclePrune(t *testing.T) {
	repoPath, wtBase := testRepo(t)
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	repoName := "prune-test"
	seedNamePool(t, store, repoName)

	ctx := context.Background()

	// Spawn two agents.
	result1, err := spawn.Spawn(ctx, spawn.Params{
		Role:         "cub",
		TaskID:       "task-p1",
		BaseBranch:   "main",
		RepoName:     repoName,
		RepoRoot:     repoPath,
		WorktreeBase: wtBase,
	}, store)
	if err != nil {
		t.Fatalf("spawn 1: %v", err)
	}

	result2, err := spawn.Spawn(ctx, spawn.Params{
		Role:         "cub",
		TaskID:       "task-p2",
		BaseBranch:   "main",
		RepoName:     repoName,
		RepoRoot:     repoPath,
		WorktreeBase: wtBase,
	}, store)
	if err != nil {
		t.Fatalf("spawn 2: %v", err)
	}
	// Keep agent2 alive for cleanup.
	t.Cleanup(func() {
		tmux.KillSession(result2.TmuxSession)
		git.RemoveWorktree(ctx, repoPath, result2.Worktree)
		git.DeleteBranch(ctx, repoPath, result2.Branch)
	})

	// Kill agent1's session to simulate a crash.
	tmux.KillSession(result1.TmuxSession)
	time.Sleep(100 * time.Millisecond)

	// Verify we have 2 agents registered.
	if n := countAgents(t, store, repoName); n != 2 {
		t.Fatalf("expected 2 agents before prune, got %d", n)
	}

	// Prune with very short branch age so the dead agent's branch gets deleted.
	pruneResult, err := prune.Prune(ctx, prune.Params{
		RepoName:     repoName,
		RepoRoot:     repoPath,
		WorktreeBase: wtBase,
		BranchAge:    1 * time.Millisecond, // Delete any branch older than 1ms.
	}, store)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	// Verify dead agent was found.
	if len(pruneResult.DeadAgents) != 1 {
		t.Fatalf("expected 1 dead agent, got %d: %v", len(pruneResult.DeadAgents), pruneResult.DeadAgents)
	}
	if pruneResult.DeadAgents[0] != result1.AgentName {
		t.Fatalf("expected dead agent %q, got %q", result1.AgentName, pruneResult.DeadAgents[0])
	}

	// Dead agent should be unregistered.
	if lookupAgentPayload(t, store, result1.AgentName, repoName) != nil {
		t.Fatal("dead agent should be unregistered after prune")
	}

	// Live agent should still be registered.
	if lookupAgentPayload(t, store, result2.AgentName, repoName) == nil {
		t.Fatal("live agent should still be registered after prune")
	}

	// Only 1 agent should remain.
	if n := countAgents(t, store, repoName); n != 1 {
		t.Fatalf("expected 1 agent after prune, got %d", n)
	}

	// Dead agent's name should be returned to pool.
	// We started with 5, spawned 2 (pool=3), prune returned 1 (pool=4).
	if n := countNames(t, store, repoName); n != 4 {
		t.Fatalf("expected 4 names in pool after prune, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// Test 6: Epic-aware branching (spawn under epic, verify feature branch)
// ---------------------------------------------------------------------------

func TestLifecycleEpicBranching(t *testing.T) {
	repoPath, wtBase := testRepo(t)
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	repoName := "epic-test"
	seedNamePool(t, store, repoName)

	ctx := context.Background()

	// Create the feature branch that the epic-aware spawn expects.
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "branch", "feature/epic-42")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("create feature branch: %s: %v", out, err)
	}

	result, err := spawn.Spawn(ctx, spawn.Params{
		Role:         "cub",
		TaskID:       "task-epic",
		BaseBranch:   "main",
		RepoName:     repoName,
		RepoRoot:     repoPath,
		WorktreeBase: wtBase,
		EpicID:       "epic-42",
	}, store)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() {
		tmux.KillSession(result.TmuxSession)
		git.RemoveWorktree(ctx, repoPath, result.Worktree)
		git.DeleteBranch(ctx, repoPath, result.Branch)
	})

	// Verify epic ID propagated.
	if result.EpicID != "epic-42" {
		t.Fatalf("expected epic_id 'epic-42', got %q", result.EpicID)
	}

	// Verify agent registered with epicId in payload.
	payload := lookupAgentPayload(t, store, result.AgentName, repoName)
	if payload == nil {
		t.Fatal("agent not registered")
	}
	if payload["epicId"] != "epic-42" {
		t.Fatalf("expected epicId 'epic-42' in payload, got %v", payload["epicId"])
	}
}

// ---------------------------------------------------------------------------
// Test 7: Name pool exhaustion fallback
// ---------------------------------------------------------------------------

func TestLifecycleNamePoolExhaustion(t *testing.T) {
	repoPath, wtBase := testRepo(t)
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	repoName := "exhaust-test"
	// Do NOT seed the name pool — it starts empty.

	ctx := context.Background()
	result, err := spawn.Spawn(ctx, spawn.Params{
		Role:         "cub",
		TaskID:       "task-exhaust",
		BaseBranch:   "main",
		RepoName:     repoName,
		RepoRoot:     repoPath,
		WorktreeBase: wtBase,
	}, store)
	if err != nil {
		t.Fatalf("spawn with empty pool: %v", err)
	}
	t.Cleanup(func() {
		tmux.KillSession(result.TmuxSession)
		git.RemoveWorktree(ctx, repoPath, result.Worktree)
		git.DeleteBranch(ctx, repoPath, result.Branch)
	})

	// Agent should get a numeric fallback name.
	if !strings.HasPrefix(result.AgentName, "cub-") {
		t.Fatalf("expected numeric fallback name (cub-N), got %q", result.AgentName)
	}
}

// ---------------------------------------------------------------------------
// Test 8: Respawn refuses live agent
// ---------------------------------------------------------------------------

func TestLifecycleRespawnRefusesLiveAgent(t *testing.T) {
	repoPath, wtBase := testRepo(t)
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	repoName := "respawn-refuse-test"
	seedNamePool(t, store, repoName)

	ctx := context.Background()
	result, err := spawn.Spawn(ctx, spawn.Params{
		Role:         "cub",
		TaskID:       "task-live",
		BaseBranch:   "main",
		RepoName:     repoName,
		RepoRoot:     repoPath,
		WorktreeBase: wtBase,
	}, store)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() {
		tmux.KillSession(result.TmuxSession)
		git.RemoveWorktree(ctx, repoPath, result.Worktree)
		git.DeleteBranch(ctx, repoPath, result.Branch)
	})

	// Attempt to respawn a live agent — should fail.
	_, err = respawn.Respawn(ctx, respawn.Params{
		AgentName:    result.AgentName,
		RepoName:     repoName,
		RepoRoot:     repoPath,
		WorktreeBase: wtBase,
	}, store)
	if err == nil {
		t.Fatal("respawn should refuse to respawn a live agent")
	}
	if !strings.Contains(err.Error(), "still active") {
		t.Fatalf("expected 'still active' error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test 9: Spawn validation rejects bad params
// ---------------------------------------------------------------------------

func TestLifecycleSpawnValidation(t *testing.T) {
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	tests := []struct {
		name   string
		params spawn.Params
		errMsg string
	}{
		{
			name:   "empty role",
			params: spawn.Params{TaskID: "t", BaseBranch: "main", RepoName: "r", RepoRoot: "/tmp"},
			errMsg: "role is required",
		},
		{
			name:   "bad role",
			params: spawn.Params{Role: "wizard", TaskID: "t", BaseBranch: "main", RepoName: "r", RepoRoot: "/tmp"},
			errMsg: "unknown role",
		},
		{
			name:   "empty task",
			params: spawn.Params{Role: "cub", BaseBranch: "main", RepoName: "r", RepoRoot: "/tmp"},
			errMsg: "task_id is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := spawn.Spawn(ctx, tt.params, store)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.errMsg) {
				t.Fatalf("expected error containing %q, got: %v", tt.errMsg, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 10: Full lifecycle via daemon IPC (spawn → status → list → dismiss)
// ---------------------------------------------------------------------------

func TestLifecycleDaemonIPC(t *testing.T) {
	repoPath, wtBase := testRepo(t)

	// Start a daemon with agent handlers registered.
	td := startDaemon(t)

	repoName := "ipc-lifecycle"

	// Seed the name pool in the daemon's store.
	names := []string{"Alpha", "Beta", "Gamma"}
	for _, name := range names {
		td.store.Insert("cubName", repoName, name, "", "{}", "session", nil, nil, nil)
	}

	// 1. Spawn via IPC.
	spawnResp := rpcCall(t, td.sockPath, "agent.spawn", map[string]interface{}{
		"role":          "cub",
		"task_id":       "task-ipc",
		"base_branch":   "main",
		"repo_name":     repoName,
		"repo_root":     repoPath,
		"worktree_base": wtBase,
	}, 1)
	if spawnResp.Error != nil {
		t.Fatalf("agent.spawn error: %s", spawnResp.Error.Message)
	}

	var spawnResult spawn.Result
	if err := json.Unmarshal(spawnResp.Result, &spawnResult); err != nil {
		t.Fatalf("unmarshal spawn result: %v", err)
	}
	ctx := context.Background()
	t.Cleanup(func() {
		tmux.KillSession(spawnResult.TmuxSession)
		git.RemoveWorktree(ctx, repoPath, spawnResult.Worktree)
		git.DeleteBranch(ctx, repoPath, spawnResult.Branch)
	})

	if spawnResult.AgentName == "" {
		t.Fatal("spawn returned empty agent name")
	}

	// 2. Status via IPC — should be active.
	statusResp := rpcCall(t, td.sockPath, "agent.status", map[string]interface{}{
		"agent_name": spawnResult.AgentName,
		"repo_name":  repoName,
	}, 2)
	if statusResp.Error != nil {
		t.Fatalf("agent.status error: %s", statusResp.Error.Message)
	}
	var statusResult dismiss.ActualStatus
	json.Unmarshal(statusResp.Result, &statusResult)
	if statusResult.ActualStatus != "active" {
		t.Fatalf("expected active, got %q", statusResult.ActualStatus)
	}

	// 3. List via IPC — should have 1 agent.
	listResp := rpcCall(t, td.sockPath, "agent.list", map[string]interface{}{
		"repo_name": repoName,
	}, 3)
	if listResp.Error != nil {
		t.Fatalf("agent.list error: %s", listResp.Error.Message)
	}
	var listResult []map[string]interface{}
	json.Unmarshal(listResp.Result, &listResult)
	if len(listResult) != 1 {
		t.Fatalf("expected 1 agent in list, got %d", len(listResult))
	}

	// Create a commit in the agent's worktree so dismiss merge succeeds.
	os.WriteFile(filepath.Join(spawnResult.Worktree, "ipc-test.txt"), []byte("ipc test"), 0o644)
	git.CommitAll(ctx, spawnResult.Worktree, "ipc test commit")

	// 4. Dismiss via IPC.
	dismissResp := rpcCall(t, td.sockPath, "agent.dismiss", map[string]interface{}{
		"agent_name": spawnResult.AgentName,
		"repo_name":  repoName,
		"repo_root":  repoPath,
	}, 4)
	if dismissResp.Error != nil {
		t.Fatalf("agent.dismiss error: %s", dismissResp.Error.Message)
	}

	// 5. List again — should be empty.
	listResp2 := rpcCall(t, td.sockPath, "agent.list", map[string]interface{}{
		"repo_name": repoName,
	}, 5)
	var listResult2 []map[string]interface{}
	json.Unmarshal(listResp2.Result, &listResult2)
	if len(listResult2) != 0 {
		t.Fatalf("expected 0 agents after dismiss, got %d", len(listResult2))
	}

	td.shutdown(t)
}

// ---------------------------------------------------------------------------
// Test 11: Multiple agents can be spawned concurrently
// ---------------------------------------------------------------------------

func TestLifecycleMultipleAgents(t *testing.T) {
	repoPath, wtBase := testRepo(t)
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	repoName := "multi-test"
	// Seed with enough names for 3 agents.
	for _, name := range []string{"Able", "Baker", "Charlie"} {
		store.Insert("cubName", repoName, name, "", "{}", "session", nil, nil, nil)
	}

	ctx := context.Background()
	var results []*spawn.Result

	for i := 0; i < 3; i++ {
		result, err := spawn.Spawn(ctx, spawn.Params{
			Role:         "cub",
			TaskID:       fmt.Sprintf("task-m%d", i),
			BaseBranch:   "main",
			RepoName:     repoName,
			RepoRoot:     repoPath,
			WorktreeBase: wtBase,
		}, store)
		if err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
		results = append(results, result)
	}
	t.Cleanup(func() {
		for _, r := range results {
			tmux.KillSession(r.TmuxSession)
			git.RemoveWorktree(ctx, repoPath, r.Worktree)
			git.DeleteBranch(ctx, repoPath, r.Branch)
		}
	})

	// All 3 should be registered.
	if n := countAgents(t, store, repoName); n != 3 {
		t.Fatalf("expected 3 agents, got %d", n)
	}

	// All names should be unique.
	names := make(map[string]bool)
	for _, r := range results {
		if names[r.AgentName] {
			t.Fatalf("duplicate agent name: %s", r.AgentName)
		}
		names[r.AgentName] = true
	}

	// All sessions should exist.
	for _, r := range results {
		if !tmux.SessionExists(r.TmuxSession) {
			t.Fatalf("session %s not found", r.TmuxSession)
		}
	}

	// Pool should be empty.
	if n := countNames(t, store, repoName); n != 0 {
		t.Fatalf("expected 0 names in pool, got %d", n)
	}
}
