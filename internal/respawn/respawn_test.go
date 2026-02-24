package respawn

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/chazu/procyon-park/internal/git"
	"github.com/chazu/procyon-park/internal/tmux"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	bare := filepath.Join(dir, "bare.git")
	run(t, "git", "init", "--bare", bare)

	clone := filepath.Join(dir, "repo")
	run(t, "git", "clone", bare, clone)

	run(t, "git", "-C", clone, "config", "user.email", "test@test.com")
	run(t, "git", "-C", clone, "config", "user.name", "Test")

	writeFile(t, filepath.Join(clone, "README.md"), "# test\n")
	run(t, "git", "-C", clone, "add", "-A")
	run(t, "git", "-C", clone, "commit", "-m", "initial commit")
	run(t, "git", "-C", clone, "push", "origin", "main")

	return clone
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %s: %v", name, args, out, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tmuxAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
}

func seedNamePool(t *testing.T, store *tuplestore.TupleStore, repoName string, names []string) {
	t.Helper()
	for _, name := range names {
		_, err := store.Insert("cubName", repoName, name, "", "{}", "session", nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func registerTestAgent(t *testing.T, store *tuplestore.TupleStore, agentName, repoName string, info agentInfo) {
	t.Helper()
	payload, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Insert("agent", repoName, agentName, "local", string(payload), "session", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}

// setupDeadAgent creates a worktree and agent tuple for a dead agent (no tmux session).
func setupDeadAgent(t *testing.T, repo, repoName, agentName, taskID string, store *tuplestore.TupleStore) agentInfo {
	t.Helper()
	ctx := context.Background()

	branchName := "agent/" + agentName + "/" + taskID
	wtBase := filepath.Join(t.TempDir(), "worktrees")
	worktreePath := filepath.Join(wtBase, agentName)

	err := git.CreateWorktree(ctx, repo, worktreePath, branchName, "main")
	if err != nil {
		t.Fatalf("setup: CreateWorktree: %v", err)
	}

	// Make a commit so there's work to preserve.
	writeFile(t, filepath.Join(worktreePath, "work.txt"), "agent work for "+taskID)
	run(t, "git", "-C", worktreePath, "add", "-A")
	run(t, "git", "-C", worktreePath, "commit", "-m", taskID+": agent work")

	sessionName := tmux.SessionName(repoName, agentName)
	info := agentInfo{
		Role:        "cub",
		Status:      "active",
		TmuxSession: sessionName,
		Worktree:    worktreePath,
		Branch:      branchName,
		Task:        taskID,
	}
	registerTestAgent(t, store, agentName, repoName, info)

	return info
}

// ---------------------------------------------------------------------------
// Tests: parameter validation
// ---------------------------------------------------------------------------

func TestRespawn_RequiresAgentName(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	_, err := Respawn(context.Background(), Params{
		RepoName: "test-repo",
		RepoRoot: "/tmp/test",
	}, store)
	if err == nil {
		t.Fatal("expected error for missing agent_name")
	}
}

func TestRespawn_RequiresRepoName(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	_, err := Respawn(context.Background(), Params{
		AgentName: "Sprocket",
		RepoRoot:  "/tmp/test",
	}, store)
	if err == nil {
		t.Fatal("expected error for missing repo_name")
	}
}

func TestRespawn_RequiresRepoRoot(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	_, err := Respawn(context.Background(), Params{
		AgentName: "Sprocket",
		RepoName:  "test-repo",
	}, store)
	if err == nil {
		t.Fatal("expected error for missing repo_root")
	}
}

func TestRespawn_AgentNotFound(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	_, err := Respawn(context.Background(), Params{
		AgentName: "Ghost",
		RepoName:  "test-repo",
		RepoRoot:  "/tmp/test",
	}, store)
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
}

// ---------------------------------------------------------------------------
// Tests: active agent rejection
// ---------------------------------------------------------------------------

func TestRespawn_RejectsActiveAgent(t *testing.T) {
	tmuxAvailable(t)

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	agentName := "Nimble"
	repoName := "test-repo"
	sessionName := tmux.SessionName(repoName, agentName)

	// Create a live tmux session.
	err := tmux.CreateSession(sessionName, "/tmp", nil)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { tmux.KillSession(sessionName) })

	registerTestAgent(t, store, agentName, repoName, agentInfo{
		Role:        "cub",
		Status:      "active",
		TmuxSession: sessionName,
		Worktree:    "/tmp",
		Branch:      "agent/Nimble/task-1",
		Task:        "task-1",
	})

	_, err = Respawn(context.Background(), Params{
		AgentName: agentName,
		RepoName:  repoName,
		RepoRoot:  "/tmp",
	}, store)
	if err == nil {
		t.Fatal("expected error when agent is still active")
	}
	if !containsStr(err.Error(), "still active") {
		t.Fatalf("expected 'still active' error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: full respawn with work preservation
// ---------------------------------------------------------------------------

func TestRespawn_FullSequenceWithWIP(t *testing.T) {
	tmuxAvailable(t)

	repo := initTestRepo(t)
	repoName := "test-repo"
	agentName := "Crumble"
	taskID := "task-respawn"

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	// Seed name pool for the new agent.
	seedNamePool(t, store, repoName, []string{"Wobble"})

	// Set up a dead agent with committed work.
	info := setupDeadAgent(t, repo, repoName, agentName, taskID, store)

	// Add uncommitted (WIP) changes.
	writeFile(t, filepath.Join(info.Worktree, "wip.txt"), "work in progress")

	// Run respawn.
	result, err := Respawn(context.Background(), Params{
		AgentName: agentName,
		RepoName:  repoName,
		RepoRoot:  repo,
	}, store)
	if err != nil {
		t.Fatalf("Respawn: %v", err)
	}

	// Verify: WIP was committed.
	if !result.WorkPreserved {
		t.Error("expected work_preserved to be true (WIP was dirty)")
	}

	// Verify: old branch preserved.
	if result.OldBranch != info.Branch {
		t.Errorf("old_branch = %q, want %q", result.OldBranch, info.Branch)
	}

	// Verify: new agent was spawned.
	if result.SpawnResult == nil {
		t.Fatal("expected spawn_result to be non-nil")
	}
	if result.SpawnResult.TaskID != taskID {
		t.Errorf("new agent task = %q, want %q", result.SpawnResult.TaskID, taskID)
	}
	if result.SpawnResult.Role != "cub" {
		t.Errorf("new agent role = %q, want 'cub'", result.SpawnResult.Role)
	}

	// Verify: new tmux session exists.
	if !tmux.SessionExists(result.SpawnResult.TmuxSession) {
		t.Error("new agent tmux session should exist")
	}
	t.Cleanup(func() { tmux.KillSession(result.SpawnResult.TmuxSession) })

	// Verify: new worktree is valid.
	if !git.IsValidWorktree(result.SpawnResult.Worktree) {
		t.Error("new agent worktree should be valid")
	}

	// Verify: old agent is unregistered, new agent is registered.
	cat := "agent"
	rn := repoName
	agents, _ := store.FindAll(&cat, &rn, nil, nil, nil)
	if len(agents) != 1 {
		t.Errorf("expected 1 agent registered, got %d", len(agents))
	}
	if len(agents) == 1 {
		id, _ := agents[0]["identity"].(string)
		if id == agentName {
			t.Error("old agent should be unregistered")
		}
	}

	// Verify: new worktree has the preserved work (work.txt from old branch).
	workFile := filepath.Join(result.SpawnResult.Worktree, "work.txt")
	if _, err := os.Stat(workFile); os.IsNotExist(err) {
		t.Error("new worktree should contain work.txt from the preserved branch")
	}
}

func TestRespawn_FullSequenceCleanWorktree(t *testing.T) {
	tmuxAvailable(t)

	repo := initTestRepo(t)
	repoName := "test-repo"
	agentName := "Spark"
	taskID := "task-clean"

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	seedNamePool(t, store, repoName, []string{"Flint"})

	// Set up dead agent with NO uncommitted changes.
	setupDeadAgent(t, repo, repoName, agentName, taskID, store)

	result, err := Respawn(context.Background(), Params{
		AgentName: agentName,
		RepoName:  repoName,
		RepoRoot:  repo,
	}, store)
	if err != nil {
		t.Fatalf("Respawn: %v", err)
	}
	t.Cleanup(func() { tmux.KillSession(result.SpawnResult.TmuxSession) })

	// No WIP was committed since worktree was clean.
	if result.WorkPreserved {
		t.Error("expected work_preserved to be false (no WIP)")
	}

	// New agent should still have the committed work from the old branch.
	workFile := filepath.Join(result.SpawnResult.Worktree, "work.txt")
	if _, err := os.Stat(workFile); os.IsNotExist(err) {
		t.Error("new worktree should contain committed work from preserved branch")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
