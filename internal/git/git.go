// Package git provides Go primitives for git worktree and branch management.
// It wraps the git CLI to support agent isolation via worktrees, branch naming
// conventions, merge operations, and orphaned worktree detection.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// protectedBranches is the set of branch names that must never be committed to
// or deleted by agents. Checks are case-insensitive.
var protectedBranches = map[string]bool{
	"main":    true,
	"master":  true,
	"develop": true,
	"release": true,
}

// CreateWorktree creates a new git worktree at worktreePath on a new branch
// branchName, based on baseBranch. It is equivalent to:
//
//	git -C repoPath worktree add -b branchName worktreePath baseBranch
func CreateWorktree(ctx context.Context, repoPath, worktreePath, branchName, baseBranch string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath,
		"worktree", "add", "-b", branchName, worktreePath, baseBranch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add: %s: %w", bytes.TrimSpace(out), err)
	}
	return nil
}

// RemoveWorktree removes the worktree at worktreePath. It first attempts a
// normal removal and falls back to --force if the initial attempt fails.
func RemoveWorktree(ctx context.Context, repoPath, worktreePath string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath,
		"worktree", "remove", worktreePath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	// Fallback: force removal.
	cmd = exec.CommandContext(ctx, "git", "-C", repoPath,
		"worktree", "remove", "--force", worktreePath)
	out, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove --force: %s: %w", bytes.TrimSpace(out), err)
	}
	return nil
}

// IsValidWorktree returns true if dirPath looks like a git worktree
// (contains a .git file, as opposed to a .git directory in a main repo).
func IsValidWorktree(dirPath string) bool {
	info, err := os.Lstat(filepath.Join(dirPath, ".git"))
	if err != nil {
		return false
	}
	// A worktree has a .git *file* (not directory) pointing to the main repo.
	return !info.IsDir()
}

// PruneWorktrees runs git worktree prune to clean up stale worktree references.
func PruneWorktrees(ctx context.Context, repoPath string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "prune")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree prune: %s: %w", bytes.TrimSpace(out), err)
	}
	return nil
}

// GenerateBranchName produces a branch name following the convention
// agent/{agentName}/{taskID}. If that branch already exists in repoPath,
// a numeric suffix is appended (e.g. agent/Pip/task-1-2).
func GenerateBranchName(ctx context.Context, repoPath, agentName, taskID string) (string, error) {
	base := fmt.Sprintf("agent/%s/%s", agentName, taskID)
	if !branchExists(ctx, repoPath, base) {
		return base, nil
	}
	for i := 2; i < 100; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !branchExists(ctx, repoPath, candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not generate unique branch for %s (all suffixes 2-99 taken)", base)
}

// branchExists checks whether a branch name exists as a ref in the repo.
func branchExists(ctx context.Context, repoPath, branch string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath,
		"rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

// MergeBranch merges source into target using --no-ff in the given repo.
// It checks out target, performs the merge, then returns. The caller is
// responsible for handling any merge conflicts (the error will contain git output).
func MergeBranch(ctx context.Context, repoPath, source, target string) error {
	// Checkout target branch.
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "checkout", target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git checkout %s: %s: %w", target, bytes.TrimSpace(out), err)
	}
	// Merge with --no-ff.
	cmd = exec.CommandContext(ctx, "git", "-C", repoPath,
		"merge", "--no-ff", source)
	out, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git merge --no-ff %s: %s: %w", source, bytes.TrimSpace(out), err)
	}
	return nil
}

// PushBranch pushes branchName from the worktree at worktreePath to origin.
func PushBranch(ctx context.Context, worktreePath, branchName string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath,
		"push", "origin", branchName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push origin %s: %s: %w", branchName, bytes.TrimSpace(out), err)
	}
	return nil
}

// HasUncommittedChanges returns true if the worktree at worktreePath has
// unstaged or staged changes (tracked files only).
func HasUncommittedChanges(ctx context.Context, worktreePath string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath,
		"status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

// CommitAll stages all changes and commits with the given message.
// Equivalent to: git add -A && git commit -m message.
func CommitAll(ctx context.Context, worktreePath, message string) error {
	// Stage all.
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "add", "-A")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git add -A: %s: %w", bytes.TrimSpace(out), err)
	}
	// Commit.
	cmd = exec.CommandContext(ctx, "git", "-C", worktreePath, "commit", "-m", message)
	out, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git commit: %s: %w", bytes.TrimSpace(out), err)
	}
	return nil
}

// IsProtectedBranch returns true if branch is a protected branch name.
// The check is case-insensitive.
func IsProtectedBranch(branch string) bool {
	return protectedBranches[strings.ToLower(branch)]
}

// ListOrphanedWorktrees scans baseDir for directories that look like worktrees
// (contain a .git file) but whose .git file points to a repo that no longer
// lists them. baseDir is typically ~/.procyon-park/worktrees/{repo}/.
//
// It returns the list of orphaned worktree paths.
func ListOrphanedWorktrees(ctx context.Context, baseDir string) ([]string, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read dir %s: %w", baseDir, err)
	}

	var orphaned []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		wtPath := filepath.Join(baseDir, e.Name())
		if !IsValidWorktree(wtPath) {
			continue
		}
		// Read the .git file to find the main repo's gitdir.
		repoPath, err := resolveMainRepo(wtPath)
		if err != nil {
			// Can't resolve — treat as orphaned.
			orphaned = append(orphaned, wtPath)
			continue
		}
		if !isWorktreeRegistered(ctx, repoPath, wtPath) {
			orphaned = append(orphaned, wtPath)
		}
	}
	return orphaned, nil
}

// resolveMainRepo reads the .git file in a worktree directory and returns the
// path to the main repository's working directory.
func resolveMainRepo(wtPath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(wtPath, ".git"))
	if err != nil {
		return "", err
	}
	// .git file contains: gitdir: /path/to/repo/.git/worktrees/<name>
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir: ") {
		return "", fmt.Errorf("unexpected .git file content: %s", line)
	}
	gitdir := strings.TrimPrefix(line, "gitdir: ")
	// Walk up from .git/worktrees/<name> to the repo root.
	// gitdir is like /repo/.git/worktrees/<name>
	repoGitDir := filepath.Dir(filepath.Dir(gitdir)) // .git dir
	repoPath := filepath.Dir(repoGitDir)              // repo root
	return repoPath, nil
}

// isWorktreeRegistered checks if the main repo lists wtPath as an active worktree.
func isWorktreeRegistered(ctx context.Context, repoPath, wtPath string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath,
		"worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	absWt, err := filepath.EvalSymlinks(wtPath)
	if err != nil {
		absWt, err = filepath.Abs(wtPath)
		if err != nil {
			return false
		}
	}
	// Parse porcelain output: "worktree /abs/path\n..."
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			registered := strings.TrimPrefix(line, "worktree ")
			resolved, err := filepath.EvalSymlinks(registered)
			if err != nil {
				resolved = registered
			}
			if resolved == absWt {
				return true
			}
		}
	}
	return false
}
