package spawn

import (
	"context"
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

// initTestRepo creates a bare repo + working clone with an initial commit.
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

// seedNamePool inserts N names into the tuplestore as cubName tuples.
func seedNamePool(t *testing.T, store *tuplestore.TupleStore, repoName string, names []string) {
	t.Helper()
	for _, name := range names {
		_, err := store.Insert("cubName", repoName, name, "", "{}", "session", nil, nil, nil)
		if err != nil {
			t.Fatalf("seed name %s: %v", name, err)
		}
	}
}

func tmuxAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
}

// ---------------------------------------------------------------------------
// Tests: parameter validation
// ---------------------------------------------------------------------------

func TestSpawn_ValidatesRole(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	_, err := Spawn(context.Background(), Params{
		Role:       "wizard",
		TaskID:     "task-1",
		BaseBranch: "main",
		RepoName:   "test-repo",
		RepoRoot:   "/tmp/test",
	}, store)

	if err == nil {
		t.Fatal("expected error for invalid role")
	}
	if msg := err.Error(); !contains(msg, "unknown role") {
		t.Fatalf("expected 'unknown role' error, got: %s", msg)
	}
}

func TestSpawn_RequiresRole(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	_, err := Spawn(context.Background(), Params{
		TaskID:     "task-1",
		BaseBranch: "main",
		RepoName:   "test-repo",
		RepoRoot:   "/tmp/test",
	}, store)

	if err == nil {
		t.Fatal("expected error for missing role")
	}
}

func TestSpawn_RequiresTaskID(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	_, err := Spawn(context.Background(), Params{
		Role:       "cub",
		BaseBranch: "main",
		RepoName:   "test-repo",
		RepoRoot:   "/tmp/test",
	}, store)

	if err == nil {
		t.Fatal("expected error for missing task_id")
	}
}

// ---------------------------------------------------------------------------
// Tests: name allocation
// ---------------------------------------------------------------------------

func TestAllocateName_FromPool(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	seedNamePool(t, store, "test-repo", []string{"Bramble", "Rustle", "Marble"})

	name, err := allocateName(store, "test-repo")
	if err != nil {
		t.Fatalf("allocateName: %v", err)
	}

	// Should be one of the seeded names.
	valid := map[string]bool{"Bramble": true, "Rustle": true, "Marble": true}
	if !valid[name] {
		t.Fatalf("expected a pool name, got %q", name)
	}

	// Pool should now have 2 names.
	cat := "cubName"
	repo := "test-repo"
	remaining, _ := store.FindAll(&cat, &repo, nil, nil, nil)
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining names, got %d", len(remaining))
	}
}

func TestAllocateName_FallbackWhenExhausted(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	// No names seeded — pool is empty.
	name, err := allocateName(store, "test-repo")
	if err != nil {
		t.Fatalf("allocateName: %v", err)
	}

	if name != "cub-1" {
		t.Fatalf("expected fallback 'cub-1', got %q", name)
	}
}

// ---------------------------------------------------------------------------
// Tests: full spawn sequence (requires git + tmux)
// ---------------------------------------------------------------------------

func TestSpawn_FullSequence(t *testing.T) {
	tmuxAvailable(t)

	repo := initTestRepo(t)
	wtBase := filepath.Join(t.TempDir(), "worktrees")

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	seedNamePool(t, store, "test-repo", []string{"Sprocket"})

	ctx := context.Background()
	result, err := Spawn(ctx, Params{
		Role:         "cub",
		TaskID:       "task-42",
		BaseBranch:   "main",
		RepoName:     "test-repo",
		RepoRoot:     repo,
		WorktreeBase: wtBase,
		PromptWait:   100 * time.Millisecond,
	}, store)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Cleanup tmux session and worktree after test.
	t.Cleanup(func() {
		tmux.KillSession(result.TmuxSession)
		git.RemoveWorktree(context.Background(), repo, result.Worktree)
	})

	// Verify result fields.
	if result.AgentName != "Sprocket" {
		t.Errorf("expected agent name 'Sprocket', got %q", result.AgentName)
	}
	if result.Branch != "agent/Sprocket/task-42" {
		t.Errorf("expected branch 'agent/Sprocket/task-42', got %q", result.Branch)
	}
	if result.TmuxSession != "pp-test-repo-Sprocket" {
		t.Errorf("expected session 'pp-test-repo-Sprocket', got %q", result.TmuxSession)
	}
	if result.Role != "cub" {
		t.Errorf("expected role 'cub', got %q", result.Role)
	}
	if result.TaskID != "task-42" {
		t.Errorf("expected task 'task-42', got %q", result.TaskID)
	}

	// Verify worktree exists.
	if !git.IsValidWorktree(result.Worktree) {
		t.Error("worktree should be valid")
	}

	// Verify tmux session exists.
	if !tmux.SessionExists(result.TmuxSession) {
		t.Error("tmux session should exist")
	}

	// Verify agent registered in tuplespace.
	cat := "agent"
	repoName := "test-repo"
	agentName := "Sprocket"
	agents, _ := store.FindAll(&cat, &repoName, &agentName, nil, nil)
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent registration, got %d", len(agents))
	}
}

func TestSpawn_EpicAwareBranching(t *testing.T) {
	tmuxAvailable(t)

	repo := initTestRepo(t)
	wtBase := filepath.Join(t.TempDir(), "worktrees")

	// Create the feature branch that the epic base branch refers to.
	run(t, "git", "-C", repo, "branch", "feature/epic-7")

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	seedNamePool(t, store, "test-repo", []string{"Gizmo"})

	ctx := context.Background()
	result, err := Spawn(ctx, Params{
		Role:         "cub",
		TaskID:       "task-99",
		BaseBranch:   "main",
		RepoName:     "test-repo",
		RepoRoot:     repo,
		WorktreeBase: wtBase,
		EpicID:       "epic-7",
		PromptWait:   100 * time.Millisecond,
	}, store)
	if err != nil {
		t.Fatalf("Spawn with epic: %v", err)
	}

	t.Cleanup(func() {
		tmux.KillSession(result.TmuxSession)
		git.RemoveWorktree(context.Background(), repo, result.Worktree)
	})

	// EpicID should be preserved in result.
	if result.EpicID != "epic-7" {
		t.Errorf("expected epicID 'epic-7', got %q", result.EpicID)
	}

	// Branch should still follow agent convention (not feature/).
	if result.Branch != "agent/Gizmo/task-99" {
		t.Errorf("expected branch 'agent/Gizmo/task-99', got %q", result.Branch)
	}
}

func TestSpawn_EnvironmentInjection(t *testing.T) {
	tmuxAvailable(t)

	repo := initTestRepo(t)
	wtBase := filepath.Join(t.TempDir(), "worktrees")

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	seedNamePool(t, store, "test-repo", []string{"Nibbles"})

	ctx := context.Background()
	result, err := Spawn(ctx, Params{
		Role:         "cub",
		TaskID:       "task-env",
		BaseBranch:   "main",
		RepoName:     "test-repo",
		RepoRoot:     repo,
		WorktreeBase: wtBase,
		PromptWait:   100 * time.Millisecond,
	}, store)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	t.Cleanup(func() {
		tmux.KillSession(result.TmuxSession)
		git.RemoveWorktree(context.Background(), repo, result.Worktree)
	})

	// Send echo command to verify env vars are set.
	if err := tmux.SendKeys(result.TmuxSession, "echo PP_AGENT_NAME=$PP_AGENT_NAME PP_TASK=$PP_TASK"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	out, err := tmux.CapturePane(result.TmuxSession, 20)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}

	if !contains(out, "PP_AGENT_NAME=Nibbles") {
		t.Errorf("expected PP_AGENT_NAME=Nibbles in pane, got:\n%s", out)
	}
	if !contains(out, "PP_TASK=task-env") {
		t.Errorf("expected PP_TASK=task-env in pane, got:\n%s", out)
	}
}

func TestSpawn_CleanupOnFailure(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	seedNamePool(t, store, "test-repo", []string{"Widget"})

	// Use a nonexistent repo root to trigger a failure during worktree creation.
	_, err := Spawn(context.Background(), Params{
		Role:       "cub",
		TaskID:     "task-fail",
		BaseBranch: "main",
		RepoName:   "test-repo",
		RepoRoot:   "/nonexistent/repo",
		PromptWait: 100 * time.Millisecond,
	}, store)

	if err == nil {
		t.Fatal("expected spawn to fail with nonexistent repo")
	}

	// Name should be returned to the pool.
	cat := "cubName"
	repo := "test-repo"
	names, _ := store.FindAll(&cat, &repo, nil, nil, nil)
	if len(names) != 1 {
		t.Fatalf("expected name returned to pool on failure, got %d names", len(names))
	}
}

func TestSpawnInRepo_Convenience(t *testing.T) {
	tmuxAvailable(t)

	repo := initTestRepo(t)

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	seedNamePool(t, store, "test-repo", []string{"Fizz"})

	ctx := context.Background()
	result, err := SpawnInRepo(ctx, "cub", "task-conv", "test-repo", repo, "main", store)
	if err != nil {
		t.Fatalf("SpawnInRepo: %v", err)
	}

	t.Cleanup(func() {
		tmux.KillSession(result.TmuxSession)
		git.RemoveWorktree(context.Background(), repo, result.Worktree)
	})

	if result.AgentName != "Fizz" {
		t.Errorf("expected 'Fizz', got %q", result.AgentName)
	}
	if result.Role != "cub" {
		t.Errorf("expected 'cub', got %q", result.Role)
	}
}

// contains is a helper for substring checks.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsString(s, substr))
}

func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
