package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	reg, err := New(path)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return reg
}

// initGitRepo creates a minimal git repo at the given path.
func initGitRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	ctx := context.Background()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", path}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	run("init", "-b", "main")
	// Create an initial commit so branches work.
	readme := filepath.Join(path, "README.md")
	os.WriteFile(readme, []byte("test"), 0644)
	run("add", ".")
	run("commit", "-m", "init")
}

func TestCRUD(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "myrepo")
	initGitRepo(t, repoDir)

	// Add.
	repo, err := reg.Add(ctx, "myrepo", repoDir)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if repo.Name != "myrepo" {
		t.Errorf("name: got %q, want %q", repo.Name, "myrepo")
	}
	if repo.MainBranch != "main" {
		t.Errorf("main branch: got %q, want %q", repo.MainBranch, "main")
	}
	if repo.BBSScope != "myrepo" {
		t.Errorf("bbs scope: got %q, want %q", repo.BBSScope, "myrepo")
	}
	if repo.AddedAt.IsZero() {
		t.Error("added_at should not be zero")
	}

	// Get.
	got, err := reg.Get("myrepo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Path != repo.Path {
		t.Errorf("path: got %q, want %q", got.Path, repo.Path)
	}

	// List.
	repos, err := reg.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("list: got %d repos, want 1", len(repos))
	}

	// Update.
	err = reg.Update("myrepo", func(r *Repo) {
		r.HubNodeID = "node-123"
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = reg.Get("myrepo")
	if got.HubNodeID != "node-123" {
		t.Errorf("hub node id: got %q, want %q", got.HubNodeID, "node-123")
	}

	// Remove.
	err = reg.Remove("myrepo")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	repos, _ = reg.List()
	if len(repos) != 0 {
		t.Errorf("list after remove: got %d, want 0", len(repos))
	}
}

func TestDuplicatePath(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "dup")
	initGitRepo(t, repoDir)

	_, err := reg.Add(ctx, "first", repoDir)
	if err != nil {
		t.Fatalf("first add: %v", err)
	}

	_, err = reg.Add(ctx, "second", repoDir)
	if err == nil {
		t.Fatal("expected error for duplicate path")
	}
}

func TestNameCollision(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	// Create two repos with the same base name in different parent dirs.
	base := t.TempDir()
	dir1 := filepath.Join(base, "alpha", "myrepo")
	dir2 := filepath.Join(base, "beta", "myrepo")
	initGitRepo(t, dir1)
	initGitRepo(t, dir2)

	repo1, err := reg.Add(ctx, "myrepo", dir1)
	if err != nil {
		t.Fatalf("add first: %v", err)
	}
	if repo1.Name != "myrepo" {
		t.Errorf("first name: got %q, want %q", repo1.Name, "myrepo")
	}

	repo2, err := reg.Add(ctx, "myrepo", dir2)
	if err != nil {
		t.Fatalf("add second: %v", err)
	}
	// Should get myrepo@beta (appended parent dir).
	if repo2.Name != "myrepo@beta" {
		t.Errorf("second name: got %q, want %q", repo2.Name, "myrepo@beta")
	}
}

func TestNameCollisionTriple(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	base := t.TempDir()
	// Three repos all named "lib" under dirs with same parent name.
	dir1 := filepath.Join(base, "same", "lib")
	dir2 := filepath.Join(base, "same2", "lib")
	dir3 := filepath.Join(base, "same3", "lib")
	initGitRepo(t, dir1)
	initGitRepo(t, dir2)
	initGitRepo(t, dir3)

	r1, _ := reg.Add(ctx, "lib", dir1)
	if r1.Name != "lib" {
		t.Errorf("r1: got %q, want %q", r1.Name, "lib")
	}

	r2, _ := reg.Add(ctx, "lib", dir2)
	if r2.Name != "lib@same2" {
		t.Errorf("r2: got %q, want %q", r2.Name, "lib@same2")
	}

	r3, _ := reg.Add(ctx, "lib", dir3)
	if r3.Name != "lib@same3" {
		t.Errorf("r3: got %q, want %q", r3.Name, "lib@same3")
	}
}

func TestAutoName(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "auto-named")
	initGitRepo(t, repoDir)

	repo, err := reg.Add(ctx, "", repoDir)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if repo.Name != "auto-named" {
		t.Errorf("auto name: got %q, want %q", repo.Name, "auto-named")
	}
}

func TestRemoveNotFound(t *testing.T) {
	reg := newTestRegistry(t)
	err := reg.Remove("nonexistent")
	if err == nil {
		t.Fatal("expected error for removing nonexistent repo")
	}
}

func TestGetNotFound(t *testing.T) {
	reg := newTestRegistry(t)
	_, err := reg.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for getting nonexistent repo")
	}
}

func TestResolvePath(t *testing.T) {
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "resolve-test")
	initGitRepo(t, repoDir)

	resolved, err := ResolvePath(ctx, repoDir)
	if err != nil {
		t.Fatalf("resolve path: %v", err)
	}
	// Should be an absolute path.
	if !filepath.IsAbs(resolved) {
		t.Errorf("resolved path not absolute: %s", resolved)
	}
}

func TestResolvePathSymlink(t *testing.T) {
	ctx := context.Background()

	base := t.TempDir()
	realDir := filepath.Join(base, "real-repo")
	initGitRepo(t, realDir)

	linkDir := filepath.Join(base, "link-repo")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	resolved, err := ResolvePath(ctx, linkDir)
	if err != nil {
		t.Fatalf("resolve symlink: %v", err)
	}

	// Both should resolve to the same canonical path.
	realResolved, _ := ResolvePath(ctx, realDir)
	if resolved != realResolved {
		t.Errorf("symlink resolved to %q, real resolved to %q", resolved, realResolved)
	}
}

func TestResolvePathWorktree(t *testing.T) {
	ctx := context.Background()

	base := t.TempDir()
	mainRepo := filepath.Join(base, "main-repo")
	initGitRepo(t, mainRepo)

	// Create a worktree.
	wtPath := filepath.Join(base, "worktree")
	cmd := exec.CommandContext(ctx, "git", "-C", mainRepo,
		"worktree", "add", "-b", "wt-branch", wtPath, "main")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("create worktree: %s: %v", out, err)
	}
	t.Cleanup(func() {
		exec.Command("git", "-C", mainRepo, "worktree", "remove", "--force", wtPath).Run()
	})

	// Resolving worktree should give main repo path.
	resolved, err := ResolvePath(ctx, wtPath)
	if err != nil {
		t.Fatalf("resolve worktree: %v", err)
	}
	mainResolved, _ := filepath.EvalSymlinks(mainRepo)
	if resolved != mainResolved {
		t.Errorf("worktree resolved to %q, expected main repo %q", resolved, mainResolved)
	}
}

func TestDetectMainBranch(t *testing.T) {
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "branch-test")
	initGitRepo(t, repoDir)

	// Without origin/HEAD set, should default to "main".
	branch := DetectMainBranch(ctx, repoDir)
	if branch != "main" {
		t.Errorf("default main branch: got %q, want %q", branch, "main")
	}
}

func TestDetectBeads(t *testing.T) {
	dir := t.TempDir()

	// No .beads directory.
	if detectBeads(dir) {
		t.Error("expected no beads")
	}

	// Create .beads directory.
	os.Mkdir(filepath.Join(dir, ".beads"), 0755)
	if !detectBeads(dir) {
		t.Error("expected beads detected")
	}
}

func TestStalenessPathMissing(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "stale")
	initGitRepo(t, repoDir)

	_, err := reg.Add(ctx, "stale", repoDir)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Remove the repo directory.
	os.RemoveAll(repoDir)

	warnings, err := reg.CheckStaleness(ctx)
	if err != nil {
		t.Fatalf("check staleness: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].Warning != "path does not exist" {
		t.Errorf("warning: got %q", warnings[0].Warning)
	}
}

func TestStalenessNotGit(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "notgit")
	initGitRepo(t, repoDir)

	_, err := reg.Add(ctx, "notgit", repoDir)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Remove .git.
	os.RemoveAll(filepath.Join(repoDir, ".git"))

	warnings, err := reg.CheckStaleness(ctx)
	if err != nil {
		t.Fatalf("check staleness: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].Warning != "not a git repository" {
		t.Errorf("warning: got %q", warnings[0].Warning)
	}
}

func TestStalenessOK(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "ok")
	initGitRepo(t, repoDir)

	_, err := reg.Add(ctx, "ok", repoDir)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	warnings, err := reg.CheckStaleness(ctx)
	if err != nil {
		t.Fatalf("check staleness: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestAtomicWrite(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "atomic")
	initGitRepo(t, repoDir)

	_, err := reg.Add(ctx, "atomic", repoDir)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Verify the file is valid JSON.
	data, err := os.ReadFile(reg.Path())
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var repos []Repo
	if err := json.Unmarshal(data, &repos); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
}

func TestConcurrentLocking(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	ctx := context.Background()

	// Create multiple repos to add.
	const n = 5
	repoDirs := make([]string, n)
	for i := 0; i < n; i++ {
		d := filepath.Join(t.TempDir(), fmt.Sprintf("repo-%d", i))
		initGitRepo(t, d)
		repoDirs[i] = d
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			reg, _ := New(path)
			_, errs[idx] = reg.Add(ctx, fmt.Sprintf("repo-%d", idx), repoDirs[idx])
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent add %d: %v", i, err)
		}
	}

	// Verify all repos are in the file.
	reg, _ := New(path)
	repos, err := reg.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(repos) != n {
		t.Errorf("expected %d repos, got %d", n, len(repos))
	}
}

func TestResolveByName(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "resolve-name")
	initGitRepo(t, repoDir)

	_, err := reg.Add(ctx, "myapp", repoDir)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	got, err := reg.Resolve(ctx, "myapp")
	if err != nil {
		t.Fatalf("resolve by name: %v", err)
	}
	if got.Name != "myapp" {
		t.Errorf("name: got %q, want %q", got.Name, "myapp")
	}
}

func TestResolveByPath(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "resolve-path")
	initGitRepo(t, repoDir)

	_, err := reg.Add(ctx, "pathrepo", repoDir)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	got, err := reg.Resolve(ctx, repoDir)
	if err != nil {
		t.Fatalf("resolve by path: %v", err)
	}
	if got.Name != "pathrepo" {
		t.Errorf("name: got %q, want %q", got.Name, "pathrepo")
	}
}

func TestResolveNotFound(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	_, err := reg.Resolve(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for resolve not found")
	}
}

func TestEmptyRegistryLoad(t *testing.T) {
	reg := newTestRegistry(t)

	repos, err := reg.List()
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if repos != nil {
		t.Errorf("expected nil for empty registry, got %v", repos)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "roundtrip")
	initGitRepo(t, repoDir)
	os.Mkdir(filepath.Join(repoDir, ".beads"), 0755)

	added, err := reg.Add(ctx, "roundtrip", repoDir)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Re-load and verify all fields.
	got, err := reg.Get("roundtrip")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Name != added.Name {
		t.Errorf("name: got %q, want %q", got.Name, added.Name)
	}
	if got.Path != added.Path {
		t.Errorf("path: got %q, want %q", got.Path, added.Path)
	}
	if !got.AddedAt.Equal(added.AddedAt) {
		t.Errorf("added_at: got %v, want %v", got.AddedAt, added.AddedAt)
	}
	if got.HasBeads != true {
		t.Error("expected has_beads=true")
	}
	if got.MainBranch != "main" {
		t.Errorf("main_branch: got %q, want %q", got.MainBranch, "main")
	}
	if got.BBSScope != "roundtrip" {
		t.Errorf("bbs_scope: got %q, want %q", got.BBSScope, "roundtrip")
	}
}

// TestDefaultPath just verifies DefaultPath doesn't panic.
func TestDefaultPath(t *testing.T) {
	p, err := DefaultPath()
	if err != nil {
		t.Fatalf("default path: %v", err)
	}
	if !filepath.IsAbs(p) {
		t.Errorf("expected absolute path, got %q", p)
	}
}

// TestStalenessMainBranchChanged verifies main branch change detection.
func TestStalenessMainBranchChanged(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "branch-change")
	initGitRepo(t, repoDir)

	_, err := reg.Add(ctx, "branchchange", repoDir)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Manually change the stored main branch to simulate drift.
	err = reg.Update("branchchange", func(r *Repo) {
		r.MainBranch = "develop"
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	warnings, err := reg.CheckStaleness(ctx)
	if err != nil {
		t.Fatalf("check staleness: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].Name != "branchchange" {
		t.Errorf("warning name: got %q", warnings[0].Name)
	}
}

// TestUpdateNotFound verifies update returns error for missing repo.
func TestUpdateNotFound(t *testing.T) {
	reg := newTestRegistry(t)
	err := reg.Update("ghost", func(r *Repo) {})
	if err == nil {
		t.Fatal("expected error for updating nonexistent repo")
	}
}

// TestMultipleAddRemove exercises add/remove cycles.
func TestMultipleAddRemove(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	dirs := make([]string, 3)
	for i := range dirs {
		d := filepath.Join(t.TempDir(), fmt.Sprintf("cycle-%d", i))
		initGitRepo(t, d)
		dirs[i] = d
	}

	// Add all.
	for i, d := range dirs {
		_, err := reg.Add(ctx, fmt.Sprintf("cycle-%d", i), d)
		if err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}

	repos, _ := reg.List()
	if len(repos) != 3 {
		t.Fatalf("expected 3 repos, got %d", len(repos))
	}

	// Remove middle.
	if err := reg.Remove("cycle-1"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	repos, _ = reg.List()
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos after remove, got %d", len(repos))
	}

	// Verify remaining names.
	names := map[string]bool{}
	for _, r := range repos {
		names[r.Name] = true
	}
	if !names["cycle-0"] || !names["cycle-2"] {
		t.Errorf("unexpected remaining repos: %v", names)
	}
}

