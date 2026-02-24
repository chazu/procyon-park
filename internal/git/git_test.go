package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initBareRepo creates a bare repo and a working clone in a temp dir.
// Returns (clonePath, cleanup).
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	bare := filepath.Join(dir, "bare.git")
	run(t, "git", "init", "--bare", bare)

	clone := filepath.Join(dir, "repo")
	run(t, "git", "clone", bare, clone)

	// Configure git user for commits.
	run(t, "git", "-C", clone, "config", "user.email", "test@test.com")
	run(t, "git", "-C", clone, "config", "user.name", "Test")

	// Create an initial commit so HEAD exists.
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

func TestCreateAndRemoveWorktree(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	wtPath := filepath.Join(t.TempDir(), "wt")
	err := CreateWorktree(ctx, repo, wtPath, "test-branch", "main")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	if !IsValidWorktree(wtPath) {
		t.Fatal("expected worktree to be valid after creation")
	}

	// Verify the branch was created.
	if !branchExists(ctx, repo, "test-branch") {
		t.Fatal("expected test-branch to exist after CreateWorktree")
	}

	// Remove the worktree.
	err = RemoveWorktree(ctx, repo, wtPath)
	if err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}

	if IsValidWorktree(wtPath) {
		t.Fatal("expected worktree to be invalid after removal")
	}
}

func TestIsValidWorktree(t *testing.T) {
	// Not a worktree.
	if IsValidWorktree(t.TempDir()) {
		t.Fatal("empty dir should not be a valid worktree")
	}

	// A main repo has a .git directory, not a file.
	repo := initTestRepo(t)
	if IsValidWorktree(repo) {
		t.Fatal("main repo should not be detected as a worktree")
	}
}

func TestGenerateBranchName(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	// First call should return the base name.
	name, err := GenerateBranchName(ctx, repo, "Pip", "task-42")
	if err != nil {
		t.Fatalf("GenerateBranchName: %v", err)
	}
	if name != "agent/Pip/task-42" {
		t.Fatalf("expected agent/Pip/task-42, got %s", name)
	}

	// Create that branch so the next call gets a suffix.
	run(t, "git", "-C", repo, "branch", "agent/Pip/task-42")

	name2, err := GenerateBranchName(ctx, repo, "Pip", "task-42")
	if err != nil {
		t.Fatalf("GenerateBranchName (dedup): %v", err)
	}
	if name2 != "agent/Pip/task-42-2" {
		t.Fatalf("expected agent/Pip/task-42-2, got %s", name2)
	}
}

func TestIsProtectedBranch(t *testing.T) {
	cases := []struct {
		branch string
		want   bool
	}{
		{"main", true},
		{"Main", true},
		{"MAIN", true},
		{"master", true},
		{"Master", true},
		{"develop", true},
		{"release", true},
		{"feature/foo", false},
		{"agent/Pip/task-1", false},
	}
	for _, tc := range cases {
		got := IsProtectedBranch(tc.branch)
		if got != tc.want {
			t.Errorf("IsProtectedBranch(%q) = %v, want %v", tc.branch, got, tc.want)
		}
	}
}

func TestHasUncommittedChanges(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	// Clean repo should have no changes.
	dirty, err := HasUncommittedChanges(ctx, repo)
	if err != nil {
		t.Fatalf("HasUncommittedChanges: %v", err)
	}
	if dirty {
		t.Fatal("expected clean repo to have no uncommitted changes")
	}

	// Create a file.
	writeFile(t, filepath.Join(repo, "new.txt"), "hello")
	dirty, err = HasUncommittedChanges(ctx, repo)
	if err != nil {
		t.Fatalf("HasUncommittedChanges: %v", err)
	}
	if !dirty {
		t.Fatal("expected dirty repo to have uncommitted changes")
	}
}

func TestCommitAll(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	writeFile(t, filepath.Join(repo, "file.txt"), "content")
	err := CommitAll(ctx, repo, "add file")
	if err != nil {
		t.Fatalf("CommitAll: %v", err)
	}

	// Should be clean after commit.
	dirty, err := HasUncommittedChanges(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("expected clean repo after CommitAll")
	}

	// Verify commit message.
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "log", "-1", "--format=%s")
	out, _ := cmd.Output()
	if strings.TrimSpace(string(out)) != "add file" {
		t.Fatalf("unexpected commit message: %s", out)
	}
}

func TestMergeBranch(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	// Create a feature branch with a commit.
	run(t, "git", "-C", repo, "checkout", "-b", "feature")
	writeFile(t, filepath.Join(repo, "feature.txt"), "feature work")
	run(t, "git", "-C", repo, "add", "-A")
	run(t, "git", "-C", repo, "commit", "-m", "feature work")
	run(t, "git", "-C", repo, "checkout", "main")

	err := MergeBranch(ctx, repo, "feature", "main")
	if err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}

	// Verify the merge commit (--no-ff creates a merge commit).
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "log", "-1", "--format=%s")
	out, _ := cmd.Output()
	msg := strings.TrimSpace(string(out))
	if !strings.Contains(msg, "Merge") {
		t.Fatalf("expected merge commit, got: %s", msg)
	}
}

func TestListOrphanedWorktrees(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	baseDir := filepath.Join(t.TempDir(), "worktrees")
	os.MkdirAll(baseDir, 0o755)

	// Create a real worktree.
	wtPath := filepath.Join(baseDir, "agent1")
	err := CreateWorktree(ctx, repo, wtPath, "agent1-branch", "main")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// No orphans yet.
	orphaned, err := ListOrphanedWorktrees(ctx, baseDir)
	if err != nil {
		t.Fatalf("ListOrphanedWorktrees: %v", err)
	}
	if len(orphaned) != 0 {
		t.Fatalf("expected 0 orphans, got %d: %v", len(orphaned), orphaned)
	}

	// Remove the worktree from git's perspective but leave the directory.
	run(t, "git", "-C", repo, "worktree", "remove", "--force", wtPath)
	// Recreate the directory to simulate an orphan.
	os.MkdirAll(wtPath, 0o755)
	writeFile(t, filepath.Join(wtPath, ".git"), "gitdir: /nonexistent/.git/worktrees/fake")

	orphaned, err = ListOrphanedWorktrees(ctx, baseDir)
	if err != nil {
		t.Fatalf("ListOrphanedWorktrees: %v", err)
	}
	if len(orphaned) != 1 {
		t.Fatalf("expected 1 orphan, got %d: %v", len(orphaned), orphaned)
	}
}

func TestListOrphanedWorktrees_NonexistentDir(t *testing.T) {
	ctx := context.Background()
	orphaned, err := ListOrphanedWorktrees(ctx, "/nonexistent/path")
	if err != nil {
		t.Fatalf("expected nil error for nonexistent dir, got: %v", err)
	}
	if len(orphaned) != 0 {
		t.Fatal("expected empty list for nonexistent dir")
	}
}

func TestPruneWorktrees(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	// Prune on a clean repo should succeed.
	err := PruneWorktrees(ctx, repo)
	if err != nil {
		t.Fatalf("PruneWorktrees: %v", err)
	}
}
