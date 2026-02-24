package dismiss

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

// registerTestAgent inserts an agent tuple into the store for testing.
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

// spawnTestAgent creates a worktree, tmux session, and agent tuple matching
// the real spawn output. Returns the agent info for verification.
func spawnTestAgent(t *testing.T, repo, repoName, agentName, taskID string, store *tuplestore.TupleStore) agentInfo {
	t.Helper()
	ctx := context.Background()

	branchName := "agent/" + agentName + "/" + taskID
	wtBase := filepath.Join(t.TempDir(), "worktrees")
	worktreePath := filepath.Join(wtBase, agentName)

	err := git.CreateWorktree(ctx, repo, worktreePath, branchName, "main")
	if err != nil {
		t.Fatalf("setup: CreateWorktree: %v", err)
	}

	// Make a commit in the worktree so there's something to merge.
	writeFile(t, filepath.Join(worktreePath, "work.txt"), "agent work for "+taskID)
	run(t, "git", "-C", worktreePath, "add", "-A")
	run(t, "git", "-C", worktreePath, "commit", "-m", taskID+": agent work")

	sessionName := tmux.SessionName(repoName, agentName)
	err = tmux.CreateSession(sessionName, worktreePath, nil)
	if err != nil {
		t.Fatalf("setup: CreateSession: %v", err)
	}

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

func TestDismiss_RequiresAgentName(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	err := Dismiss(context.Background(), Params{
		RepoName: "test-repo",
		RepoRoot: "/tmp/test",
	}, store)
	if err == nil {
		t.Fatal("expected error for missing agent_name")
	}
}

func TestDismiss_RequiresRepoName(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	err := Dismiss(context.Background(), Params{
		AgentName: "Sprocket",
		RepoRoot:  "/tmp/test",
	}, store)
	if err == nil {
		t.Fatal("expected error for missing repo_name")
	}
}

func TestDismiss_RequiresRepoRoot(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	err := Dismiss(context.Background(), Params{
		AgentName: "Sprocket",
		RepoName:  "test-repo",
	}, store)
	if err == nil {
		t.Fatal("expected error for missing repo_root")
	}
}

func TestDismiss_AgentNotFound(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	err := Dismiss(context.Background(), Params{
		AgentName: "Ghost",
		RepoName:  "test-repo",
		RepoRoot:  "/tmp/test",
	}, store)
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	if msg := err.Error(); !containsStr(msg, "not found") {
		t.Fatalf("expected 'not found' error, got: %s", msg)
	}
}

// ---------------------------------------------------------------------------
// Tests: full dismiss sequence
// ---------------------------------------------------------------------------

func TestDismiss_FullSequence(t *testing.T) {
	tmuxAvailable(t)

	repo := initTestRepo(t)
	repoName := "test-repo"
	agentName := "Sprocket"
	taskID := "task-42"

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	// Seed name pool so we can verify release.
	// (Sprocket is NOT in the pool — it was "allocated" during spawn.)

	info := spawnTestAgent(t, repo, repoName, agentName, taskID, store)

	// Verify preconditions.
	if !tmux.SessionExists(info.TmuxSession) {
		t.Fatal("precondition: tmux session should exist")
	}
	if !git.IsValidWorktree(info.Worktree) {
		t.Fatal("precondition: worktree should exist")
	}

	// Run dismiss.
	err := Dismiss(context.Background(), Params{
		AgentName: agentName,
		RepoName:  repoName,
		RepoRoot:  repo,
	}, store)
	if err != nil {
		t.Fatalf("Dismiss: %v", err)
	}

	// Verify: tmux session killed.
	if tmux.SessionExists(info.TmuxSession) {
		t.Error("tmux session should be killed after dismiss")
	}

	// Verify: branch merged (check that work.txt exists on main).
	cmd := exec.CommandContext(context.Background(), "git", "-C", repo, "log", "-1", "--format=%s")
	out, _ := cmd.Output()
	// The merge commit message should reference the source branch.
	if msg := string(out); !containsStr(msg, "Merge") {
		t.Errorf("expected merge commit on main, got: %s", msg)
	}

	// Verify: name returned to pool.
	cat := "cubName"
	rn := repoName
	names, _ := store.FindAll(&cat, &rn, nil, nil, nil)
	if len(names) != 1 {
		t.Errorf("expected name returned to pool, got %d names", len(names))
	}

	// Verify: agent unregistered.
	cat = "agent"
	agents, _ := store.FindAll(&cat, &rn, nil, nil, nil)
	if len(agents) != 0 {
		t.Errorf("expected agent unregistered, got %d agents", len(agents))
	}
}

func TestDismiss_MergeFailureAborts(t *testing.T) {
	tmuxAvailable(t)

	repo := initTestRepo(t)
	repoName := "test-repo"
	agentName := "Widget"
	taskID := "task-fail"

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	ctx := context.Background()

	// Create agent worktree.
	branchName := "agent/" + agentName + "/" + taskID
	wtPath := filepath.Join(t.TempDir(), "worktrees", agentName)
	err := git.CreateWorktree(ctx, repo, wtPath, branchName, "main")
	if err != nil {
		t.Fatalf("setup: CreateWorktree: %v", err)
	}

	// Create conflicting changes: modify the same file differently on both branches.
	writeFile(t, filepath.Join(wtPath, "conflict.txt"), "agent version")
	run(t, "git", "-C", wtPath, "add", "-A")
	run(t, "git", "-C", wtPath, "commit", "-m", "agent changes")

	// Create a conflicting commit on main.
	run(t, "git", "-C", repo, "checkout", "main")
	writeFile(t, filepath.Join(repo, "conflict.txt"), "main version")
	run(t, "git", "-C", repo, "add", "-A")
	run(t, "git", "-C", repo, "commit", "-m", "main changes")

	sessionName := tmux.SessionName(repoName, agentName)
	tmux.CreateSession(sessionName, wtPath, nil)
	t.Cleanup(func() {
		tmux.KillSession(sessionName)
		git.RemoveWorktree(context.Background(), repo, wtPath)
	})

	info := agentInfo{
		Role:        "cub",
		Status:      "active",
		TmuxSession: sessionName,
		Worktree:    wtPath,
		Branch:      branchName,
		Task:        taskID,
	}
	registerTestAgent(t, store, agentName, repoName, info)

	// Dismiss should fail due to merge conflict.
	err = Dismiss(ctx, Params{
		AgentName: agentName,
		RepoName:  repoName,
		RepoRoot:  repo,
	}, store)
	if err == nil {
		t.Fatal("expected dismiss to fail on merge conflict")
	}
	if !containsStr(err.Error(), "merge") {
		t.Fatalf("expected merge error, got: %v", err)
	}

	// Verify: agent is still registered (dismiss aborted).
	cat := "agent"
	rn := repoName
	agents, _ := store.FindAll(&cat, &rn, nil, nil, nil)
	if len(agents) != 1 {
		t.Errorf("expected agent still registered after failed dismiss, got %d", len(agents))
	}

	// Verify: name NOT returned to pool (cleanup didn't happen).
	cat = "cubName"
	names, _ := store.FindAll(&cat, &rn, nil, nil, nil)
	if len(names) != 0 {
		t.Errorf("expected no names in pool after failed dismiss, got %d", len(names))
	}

	// Abort the merge so cleanup works.
	exec.Command("git", "-C", repo, "merge", "--abort").Run()
}

func TestDismiss_EpicMergeTarget(t *testing.T) {
	tmuxAvailable(t)

	repo := initTestRepo(t)
	repoName := "test-repo"
	agentName := "Gizmo"
	taskID := "task-epic"
	epicID := "epic-7"

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	ctx := context.Background()

	// Create the feature branch for the epic.
	run(t, "git", "-C", repo, "branch", "feature/"+epicID)

	// Create agent worktree.
	branchName := "agent/" + agentName + "/" + taskID
	wtPath := filepath.Join(t.TempDir(), "worktrees", agentName)
	err := git.CreateWorktree(ctx, repo, wtPath, branchName, "main")
	if err != nil {
		t.Fatalf("setup: CreateWorktree: %v", err)
	}

	writeFile(t, filepath.Join(wtPath, "epic-work.txt"), "epic work")
	run(t, "git", "-C", wtPath, "add", "-A")
	run(t, "git", "-C", wtPath, "commit", "-m", "epic work")

	sessionName := tmux.SessionName(repoName, agentName)
	tmux.CreateSession(sessionName, wtPath, nil)

	info := agentInfo{
		Role:        "cub",
		Status:      "active",
		TmuxSession: sessionName,
		Worktree:    wtPath,
		Branch:      branchName,
		Task:        taskID,
		EpicID:      epicID,
	}
	registerTestAgent(t, store, agentName, repoName, info)

	// Dismiss should merge into feature/epic-7, not main.
	err = Dismiss(ctx, Params{
		AgentName: agentName,
		RepoName:  repoName,
		RepoRoot:  repo,
	}, store)
	if err != nil {
		t.Fatalf("Dismiss with epic: %v", err)
	}

	// Verify: current branch should be feature/epic-7 (MergeBranch checks out target).
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "branch", "--show-current")
	out, _ := cmd.Output()
	currentBranch := string(out)
	if !containsStr(currentBranch, "feature/"+epicID) {
		t.Errorf("expected to be on feature/%s after merge, got %s", epicID, currentBranch)
	}
}

func TestDismiss_SessionAlreadyDead(t *testing.T) {
	tmuxAvailable(t)

	repo := initTestRepo(t)
	repoName := "test-repo"
	agentName := "Bumble"
	taskID := "task-dead"

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	ctx := context.Background()

	// Create agent worktree with a commit.
	branchName := "agent/" + agentName + "/" + taskID
	wtPath := filepath.Join(t.TempDir(), "worktrees", agentName)
	err := git.CreateWorktree(ctx, repo, wtPath, branchName, "main")
	if err != nil {
		t.Fatalf("setup: CreateWorktree: %v", err)
	}
	writeFile(t, filepath.Join(wtPath, "work.txt"), "work")
	run(t, "git", "-C", wtPath, "add", "-A")
	run(t, "git", "-C", wtPath, "commit", "-m", "work")

	// Register agent with a session that doesn't exist.
	sessionName := tmux.SessionName(repoName, agentName)
	info := agentInfo{
		Role:        "cub",
		Status:      "active",
		TmuxSession: sessionName,
		Worktree:    wtPath,
		Branch:      branchName,
		Task:        taskID,
	}
	registerTestAgent(t, store, agentName, repoName, info)

	// Dismiss should succeed even though session is already dead.
	err = Dismiss(ctx, Params{
		AgentName: agentName,
		RepoName:  repoName,
		RepoRoot:  repo,
	}, store)
	if err != nil {
		t.Fatalf("Dismiss with dead session: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: liveness detection
// ---------------------------------------------------------------------------

func TestGetActualStatus_ActiveWithSession(t *testing.T) {
	tmuxAvailable(t)

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	agentName := "Fizz"
	repoName := "test-repo"
	sessionName := tmux.SessionName(repoName, agentName)

	// Create a real tmux session.
	err := tmux.CreateSession(sessionName, "/tmp", nil)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { tmux.KillSession(sessionName) })

	// Allow session to stabilize.
	time.Sleep(200 * time.Millisecond)

	registerTestAgent(t, store, agentName, repoName, agentInfo{
		Role:        "cub",
		Status:      "active",
		TmuxSession: sessionName,
		Worktree:    "/tmp/test-wt",
		Branch:      "agent/Fizz/task-1",
		Task:        "task-1",
	})

	status, err := GetActualStatus(store, agentName, repoName)
	if err != nil {
		t.Fatalf("GetActualStatus: %v", err)
	}
	if status.StoredStatus != "active" {
		t.Errorf("expected stored status 'active', got %q", status.StoredStatus)
	}
	if status.ActualStatus != "active" {
		t.Errorf("expected actual status 'active', got %q", status.ActualStatus)
	}
	if !status.SessionExists {
		t.Error("expected session_exists to be true")
	}
}

func TestGetActualStatus_ActiveButDead(t *testing.T) {
	tmuxAvailable(t)

	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	agentName := "Marble"
	repoName := "test-repo"
	sessionName := tmux.SessionName(repoName, agentName)

	// Register as active but with no tmux session.
	registerTestAgent(t, store, agentName, repoName, agentInfo{
		Role:        "cub",
		Status:      "active",
		TmuxSession: sessionName,
		Worktree:    "/tmp/test-wt",
		Branch:      "agent/Marble/task-2",
		Task:        "task-2",
	})

	status, err := GetActualStatus(store, agentName, repoName)
	if err != nil {
		t.Fatalf("GetActualStatus: %v", err)
	}
	if status.StoredStatus != "active" {
		t.Errorf("expected stored status 'active', got %q", status.StoredStatus)
	}
	if status.ActualStatus != "dead" {
		t.Errorf("expected actual status 'dead', got %q", status.ActualStatus)
	}
	if status.SessionExists {
		t.Error("expected session_exists to be false")
	}
}

func TestGetActualStatus_StoppedStaysStoped(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	agentName := "Nettle"
	repoName := "test-repo"
	sessionName := tmux.SessionName(repoName, agentName)

	registerTestAgent(t, store, agentName, repoName, agentInfo{
		Role:        "cub",
		Status:      "stopped",
		TmuxSession: sessionName,
		Worktree:    "/tmp/test-wt",
		Branch:      "agent/Nettle/task-3",
		Task:        "task-3",
	})

	status, err := GetActualStatus(store, agentName, repoName)
	if err != nil {
		t.Fatalf("GetActualStatus: %v", err)
	}
	// Non-active statuses are not changed to "dead".
	if status.ActualStatus != "stopped" {
		t.Errorf("expected actual status 'stopped', got %q", status.ActualStatus)
	}
}

func TestGetActualStatus_AgentNotFound(t *testing.T) {
	store, _ := tuplestore.NewMemoryStore()
	defer store.Close()

	_, err := GetActualStatus(store, "Ghost", "test-repo")
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
}

// ---------------------------------------------------------------------------
// Tests: merge target
// ---------------------------------------------------------------------------

func TestMergeTarget(t *testing.T) {
	if got := mergeTarget(""); got != "main" {
		t.Errorf("mergeTarget('') = %q, want 'main'", got)
	}
	if got := mergeTarget("epic-7"); got != "feature/epic-7" {
		t.Errorf("mergeTarget('epic-7') = %q, want 'feature/epic-7'", got)
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
