// Package registry implements a JSON-based repository registry stored at
// ~/.procyon-park/repos.json. It provides CRUD operations, path resolution,
// name collision handling, main branch detection, and staleness checks.
//
// All writes use file locking and atomic temp+rename for safety.
package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Repo represents a tracked repository in the registry.
type Repo struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	AddedAt    time.Time `json:"added_at"`
	HasBeads   bool      `json:"has_beads"`
	MainBranch string    `json:"main_branch"`
	BBSScope   string    `json:"bbs_scope"`
	HubNodeID  string    `json:"hub_node_id,omitempty"`
}

// StalenessWarning describes an advisory staleness issue.
type StalenessWarning struct {
	Name    string `json:"name"`
	Warning string `json:"warning"`
}

// Registry manages repos.json on disk with file locking and atomic writes.
type Registry struct {
	path string // path to repos.json
}

// New creates a Registry backed by the given file path.
// The parent directory is created if needed.
func New(path string) (*Registry, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create registry dir: %w", err)
	}
	return &Registry{path: path}, nil
}

// DefaultPath returns the default registry path: ~/.procyon-park/repos.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}
	return filepath.Join(home, ".procyon-park", "repos.json"), nil
}

// Path returns the registry file path.
func (r *Registry) Path() string { return r.path }

// Add registers a new repository. It resolves the path, detects the main branch,
// checks for beads, and handles name collisions.
func (r *Registry) Add(ctx context.Context, name, rawPath string) (*Repo, error) {
	resolved, err := ResolvePath(ctx, rawPath)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	mainBranch := DetectMainBranch(ctx, resolved)
	hasBeads := detectBeads(resolved)

	var result *Repo
	err = r.withFileLock(func(repos []Repo) ([]Repo, error) {
		if name == "" {
			name = filepath.Base(resolved)
		}
		// Handle name collisions.
		name = r.uniqueName(name, resolved, repos)

		// Check for path duplicates.
		for _, existing := range repos {
			if existing.Path == resolved {
				return repos, fmt.Errorf("path %q is already registered as %q", resolved, existing.Name)
			}
		}

		repo := Repo{
			Name:       name,
			Path:       resolved,
			AddedAt:    time.Now().UTC().Truncate(time.Second),
			HasBeads:   hasBeads,
			MainBranch: mainBranch,
			BBSScope:   name, // defaults to name
		}
		result = &repo
		return append(repos, repo), nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Remove unregisters a repository by name.
func (r *Registry) Remove(name string) error {
	return r.withFileLock(func(repos []Repo) ([]Repo, error) {
		for i, repo := range repos {
			if repo.Name == name {
				return append(repos[:i], repos[i+1:]...), nil
			}
		}
		return repos, fmt.Errorf("repository %q not found", name)
	})
}

// Get returns a single repo by name.
func (r *Registry) Get(name string) (*Repo, error) {
	repos, err := r.load()
	if err != nil {
		return nil, err
	}
	for _, repo := range repos {
		if repo.Name == name {
			return &repo, nil
		}
	}
	return nil, fmt.Errorf("repository %q not found", name)
}

// List returns all registered repos.
func (r *Registry) List() ([]Repo, error) {
	return r.load()
}

// Update modifies a repo in-place using the provided function.
func (r *Registry) Update(name string, fn func(*Repo)) error {
	return r.withFileLock(func(repos []Repo) ([]Repo, error) {
		for i := range repos {
			if repos[i].Name == name {
				fn(&repos[i])
				return repos, nil
			}
		}
		return repos, fmt.Errorf("repository %q not found", name)
	})
}

// CheckStaleness returns advisory warnings for repos with issues.
func (r *Registry) CheckStaleness(ctx context.Context) ([]StalenessWarning, error) {
	repos, err := r.load()
	if err != nil {
		return nil, err
	}

	var warnings []StalenessWarning
	for _, repo := range repos {
		if w := checkRepoStaleness(ctx, repo); w != "" {
			warnings = append(warnings, StalenessWarning{Name: repo.Name, Warning: w})
		}
	}
	return warnings, nil
}

// Resolve finds a repo using the three-step resolution:
// 1. By name (if flagRepo matches a registered name)
// 2. By path (if flagRepo is a path to a registered repo)
// 3. By cwd git detection (detect repo from current working directory)
// Returns an error prompting "pp repo add" if nothing matches.
func (r *Registry) Resolve(ctx context.Context, flagRepo string) (*Repo, error) {
	repos, err := r.load()
	if err != nil {
		return nil, err
	}

	if flagRepo != "" {
		// Step 1: try by name.
		for _, repo := range repos {
			if repo.Name == flagRepo {
				return &repo, nil
			}
		}
		// Step 2: try by path.
		resolved, err := ResolvePath(ctx, flagRepo)
		if err == nil {
			for _, repo := range repos {
				if repo.Path == resolved {
					return &repo, nil
				}
			}
		}
		return nil, fmt.Errorf("repository %q not found; register it with: pp repo add %s", flagRepo, flagRepo)
	}

	// Step 3: cwd git detection.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}

	resolved, err := ResolvePath(ctx, cwd)
	if err != nil {
		return nil, fmt.Errorf("not in a git repository; specify --repo or register with: pp repo add <path>")
	}

	for _, repo := range repos {
		if repo.Path == resolved {
			return &repo, nil
		}
	}
	return nil, fmt.Errorf("repository at %s is not registered; add it with: pp repo add %s", resolved, resolved)
}

// ResolvePath resolves a raw path to the canonical repository root:
// absolute path → symlink eval → worktree-to-main-repo.
func ResolvePath(ctx context.Context, rawPath string) (string, error) {
	absPath, err := filepath.Abs(rawPath)
	if err != nil {
		return "", fmt.Errorf("absolute path: %w", err)
	}
	evaled, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// Path might not exist yet; fall back to abs.
		evaled = absPath
	}
	// Detect worktree and resolve to main repo.
	mainRepo, err := resolveWorktreeToMain(ctx, evaled)
	if err != nil {
		// Not a worktree or not a git repo — return evaled as-is.
		return evaled, nil
	}
	return mainRepo, nil
}

// DetectMainBranch detects the main branch of a repo.
// Uses git symbolic-ref refs/remotes/origin/HEAD, defaults to "main".
func DetectMainBranch(ctx context.Context, repoPath string) string {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath,
		"symbolic-ref", "refs/remotes/origin/HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "main"
	}
	ref := strings.TrimSpace(string(out))
	// ref is like "refs/remotes/origin/main"
	parts := strings.Split(ref, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "main"
}

// resolveWorktreeToMain uses git rev-parse --git-common-dir to find the main
// repo from a worktree path.
func resolveWorktreeToMain(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", path,
		"rev-parse", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repo: %w", err)
	}
	gitCommon := strings.TrimSpace(string(out))
	if gitCommon == "" {
		return "", fmt.Errorf("empty git-common-dir")
	}
	// Make absolute if relative.
	if !filepath.IsAbs(gitCommon) {
		gitCommon = filepath.Join(path, gitCommon)
	}
	gitCommon = filepath.Clean(gitCommon)

	// gitCommon is the .git dir of the main repo. Parent is the repo root.
	// But if gitCommon ends with ".git", the repo root is the parent.
	// For a bare repo or if gitCommon IS the repo, handle accordingly.
	if filepath.Base(gitCommon) == ".git" {
		repoRoot := filepath.Dir(gitCommon)
		evaled, err := filepath.EvalSymlinks(repoRoot)
		if err != nil {
			return repoRoot, nil
		}
		return evaled, nil
	}
	return filepath.Dir(gitCommon), nil
}

// detectBeads checks if a .beads directory exists in the repo.
func detectBeads(repoPath string) bool {
	_, err := os.Stat(filepath.Join(repoPath, ".beads"))
	return err == nil
}

// uniqueName handles name collisions by appending @parent-dir.
func (r *Registry) uniqueName(name, resolvedPath string, repos []Repo) string {
	taken := false
	for _, repo := range repos {
		if repo.Name == name {
			taken = true
			break
		}
	}
	if !taken {
		return name
	}
	parent := filepath.Base(filepath.Dir(resolvedPath))
	candidate := name + "@" + parent
	// If still taken, append numeric suffix.
	if !nameExists(candidate, repos) {
		return candidate
	}
	for i := 2; i < 100; i++ {
		c := fmt.Sprintf("%s@%s-%d", name, parent, i)
		if !nameExists(c, repos) {
			return c
		}
	}
	return candidate // give up on uniqueness, caller will get a path-duplicate error
}

func nameExists(name string, repos []Repo) bool {
	for _, r := range repos {
		if r.Name == name {
			return true
		}
	}
	return false
}

// checkRepoStaleness returns a warning string if the repo has issues, or "".
func checkRepoStaleness(ctx context.Context, repo Repo) string {
	info, err := os.Stat(repo.Path)
	if err != nil {
		return "path does not exist"
	}
	if !info.IsDir() {
		return "path is not a directory"
	}
	gitDir := filepath.Join(repo.Path, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return "not a git repository"
	}
	// Check if main branch changed.
	current := DetectMainBranch(ctx, repo.Path)
	if current != repo.MainBranch {
		return fmt.Sprintf("main branch changed from %q to %q", repo.MainBranch, current)
	}
	return ""
}

// load reads the repos.json file. Returns empty slice if file doesn't exist.
func (r *Registry) load() ([]Repo, error) {
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read registry: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var repos []Repo
	if err := json.Unmarshal(data, &repos); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	return repos, nil
}

// save writes the repos slice atomically via temp+rename.
func (r *Registry) save(repos []Repo) error {
	if repos == nil {
		repos = []Repo{}
	}
	data, err := json.MarshalIndent(repos, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal registry: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(r.path)
	tmp, err := os.CreateTemp(dir, ".repos-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, r.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// lockPath returns the path to the lock file.
func (r *Registry) lockPath() string {
	return r.path + ".lock"
}

// withFileLock acquires an exclusive lock, loads repos, calls fn, and saves.
func (r *Registry) withFileLock(fn func([]Repo) ([]Repo, error)) error {
	lockFile, err := os.OpenFile(r.lockPath(), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquire file lock: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) //nolint:errcheck

	repos, err := r.load()
	if err != nil {
		return err
	}
	if repos == nil {
		repos = []Repo{}
	}

	updated, err := fn(repos)
	if err != nil {
		return err
	}

	return r.save(updated)
}
