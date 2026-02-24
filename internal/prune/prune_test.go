package prune

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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

// ---------------------------------------------------------------------------
// Tests: parameter validation
// ---------------------------------------------------------------------------

func TestPrune_RequiresRepoName(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	_, err := Prune(context.Background(), Params{
		RepoRoot:     "/tmp/test",
		WorktreeBase: "/tmp/wt",
	}, store)
	if err == nil {
		t.Fatal("expected error for missing repo_name")
	}
}

func TestPrune_RequiresRepoRoot(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	_, err := Prune(context.Background(), Params{
		RepoName:     "test-repo",
		WorktreeBase: "/tmp/wt",
	}, store)
	if err == nil {
		t.Fatal("expected error for missing repo_root")
	}
}

func TestPrune_RequiresWorktreeBase(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	_, err := Prune(context.Background(), Params{
		RepoName: "test-repo",
		RepoRoot: "/tmp/test",
	}, store)
	if err == nil {
		t.Fatal("expected error for missing worktree_base")
	}
}

// ---------------------------------------------------------------------------
// Tests: dead agent cleanup
// ---------------------------------------------------------------------------

func TestPrune_CleansUpDeadAgent(t *testing.T) {
	tmuxAvailable(t)

	repo := initTestRepo(t)
	repoName := "test-repo"
	agentName := "Dusty"
	taskID := "task-dead"

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	ctx := context.Background()

	// Create agent worktree.
	branchName := "agent/" + agentName + "/" + taskID
	wtBase := filepath.Join(t.TempDir(), "worktrees")
	worktreePath := filepath.Join(wtBase, agentName)
	err := git.CreateWorktree(ctx, repo, worktreePath, branchName, "main")
	if err != nil {
		t.Fatalf("setup: CreateWorktree: %v", err)
	}

	writeFile(t, filepath.Join(worktreePath, "work.txt"), "work")
	run(t, "git", "-C", worktreePath, "add", "-A")
	run(t, "git", "-C", worktreePath, "commit", "-m", "work")

	// Register agent with dead session (no tmux session created).
	sessionName := tmux.SessionName(repoName, agentName)
	registerTestAgent(t, store, agentName, repoName, agentInfo{
		Role:        "cub",
		Status:      "active",
		TmuxSession: sessionName,
		Worktree:    worktreePath,
		Branch:      branchName,
		Task:        taskID,
	})

	// Prune with zero branch age (delete all branches).
	result, err := Prune(ctx, Params{
		RepoName:     repoName,
		RepoRoot:     repo,
		WorktreeBase: wtBase,
		BranchAge:    1 * time.Nanosecond, // effectively zero — delete immediately
	}, store)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	// Verify: dead agent was found.
	if len(result.DeadAgents) != 1 || result.DeadAgents[0] != agentName {
		t.Errorf("DeadAgents = %v, want [%s]", result.DeadAgents, agentName)
	}

	// Verify: branch was deleted.
	if len(result.DeletedBranches) != 1 {
		t.Errorf("DeletedBranches = %v, want 1", result.DeletedBranches)
	}

	// Verify: agent unregistered.
	cat := "agent"
	rn := repoName
	agents, _ := store.FindAll(&cat, &rn, nil, nil, nil)
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}

	// Verify: name returned to pool.
	cat = "cubName"
	names, _ := store.FindAll(&cat, &rn, nil, nil, nil)
	if len(names) != 1 {
		t.Errorf("expected 1 name in pool, got %d", len(names))
	}
}

// ---------------------------------------------------------------------------
// Tests: branch retention policy
// ---------------------------------------------------------------------------

func TestPrune_PreservesRecentBranches(t *testing.T) {
	tmuxAvailable(t)

	repo := initTestRepo(t)
	repoName := "test-repo"
	agentName := "Fresh"
	taskID := "task-recent"

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	ctx := context.Background()

	// Create agent worktree with a recent commit.
	branchName := "agent/" + agentName + "/" + taskID
	wtBase := filepath.Join(t.TempDir(), "worktrees")
	worktreePath := filepath.Join(wtBase, agentName)
	err := git.CreateWorktree(ctx, repo, worktreePath, branchName, "main")
	if err != nil {
		t.Fatalf("setup: CreateWorktree: %v", err)
	}

	writeFile(t, filepath.Join(worktreePath, "work.txt"), "recent work")
	run(t, "git", "-C", worktreePath, "add", "-A")
	run(t, "git", "-C", worktreePath, "commit", "-m", "recent work")

	sessionName := tmux.SessionName(repoName, agentName)
	registerTestAgent(t, store, agentName, repoName, agentInfo{
		Role:        "cub",
		Status:      "active",
		TmuxSession: sessionName,
		Worktree:    worktreePath,
		Branch:      branchName,
		Task:        taskID,
	})

	// Prune with 24h branch age — branch is seconds old, should be preserved.
	result, err := Prune(ctx, Params{
		RepoName:     repoName,
		RepoRoot:     repo,
		WorktreeBase: wtBase,
		BranchAge:    24 * time.Hour,
	}, store)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	// Dead agent found.
	if len(result.DeadAgents) != 1 {
		t.Errorf("DeadAgents = %v, want 1", result.DeadAgents)
	}

	// Branch preserved (too recent).
	if len(result.PreservedBranches) != 1 {
		t.Errorf("PreservedBranches = %v, want 1", result.PreservedBranches)
	}

	// No branches deleted.
	if len(result.DeletedBranches) != 0 {
		t.Errorf("DeletedBranches = %v, want 0", result.DeletedBranches)
	}
}

// ---------------------------------------------------------------------------
// Tests: alive agents are skipped
// ---------------------------------------------------------------------------

func TestPrune_SkipsAliveAgents(t *testing.T) {
	tmuxAvailable(t)

	repo := initTestRepo(t)
	repoName := "test-repo"
	agentName := "Active"

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	sessionName := tmux.SessionName(repoName, agentName)
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
		Branch:      "agent/Active/task-1",
		Task:        "task-1",
	})

	wtBase := filepath.Join(t.TempDir(), "worktrees")
	os.MkdirAll(wtBase, 0o755)

	result, err := Prune(context.Background(), Params{
		RepoName:     repoName,
		RepoRoot:     repo,
		WorktreeBase: wtBase,
		BranchAge:    1 * time.Nanosecond,
	}, store)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	// Active agent should NOT be pruned.
	if len(result.DeadAgents) != 0 {
		t.Errorf("DeadAgents = %v, want empty", result.DeadAgents)
	}

	// Agent should still be registered.
	cat := "agent"
	rn := repoName
	agents, _ := store.FindAll(&cat, &rn, nil, nil, nil)
	if len(agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(agents))
	}
}

// ---------------------------------------------------------------------------
// Tests: orphaned worktree cleanup
// ---------------------------------------------------------------------------

func TestPrune_CleansOrphanedWorktrees(t *testing.T) {
	tmuxAvailable(t)

	repo := initTestRepo(t)
	repoName := "test-repo"

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	ctx := context.Background()

	// Create a worktree, then forcibly remove it from git's tracking
	// so it becomes orphaned.
	wtBase := filepath.Join(t.TempDir(), "worktrees")
	orphanPath := filepath.Join(wtBase, "Orphan")
	err := git.CreateWorktree(ctx, repo, orphanPath, "agent/Orphan/task-x", "main")
	if err != nil {
		t.Fatalf("setup: CreateWorktree: %v", err)
	}

	// Remove from git's worktree list but leave directory.
	run(t, "git", "-C", repo, "worktree", "remove", "--force", orphanPath)

	// Re-create a fake worktree directory with a .git file pointing nowhere useful.
	os.MkdirAll(orphanPath, 0o755)
	writeFile(t, filepath.Join(orphanPath, ".git"), "gitdir: /nonexistent/.git/worktrees/Orphan")

	result, err := Prune(ctx, Params{
		RepoName:     repoName,
		RepoRoot:     repo,
		WorktreeBase: wtBase,
	}, store)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	// The orphaned worktree should be detected.
	// Note: removal may fail because it's a fake .git file, so we just check detection.
	if len(result.OrphanedWorktrees) == 0 && len(result.Errors) == 0 {
		t.Error("expected orphaned worktree to be detected")
	}
}

// ---------------------------------------------------------------------------
// Tests: empty state
// ---------------------------------------------------------------------------

func TestPrune_EmptyState(t *testing.T) {
	repo := initTestRepo(t)
	repoName := "test-repo"

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	wtBase := filepath.Join(t.TempDir(), "worktrees")
	os.MkdirAll(wtBase, 0o755)

	result, err := Prune(context.Background(), Params{
		RepoName:     repoName,
		RepoRoot:     repo,
		WorktreeBase: wtBase,
	}, store)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	if len(result.DeadAgents) != 0 {
		t.Errorf("DeadAgents should be empty, got %v", result.DeadAgents)
	}
	if len(result.OrphanedWorktrees) != 0 {
		t.Errorf("OrphanedWorktrees should be empty, got %v", result.OrphanedWorktrees)
	}
}
