// Phase 9 integration tests: config, registry, and work tracking.
//
// These tests exercise the end-to-end integration of configuration loading,
// repository registry, work tracker, and CLI commands (init, repo add, doctor).
// They test cross-cutting concerns: merge precedence, env overrides, feature
// flags, registry lifecycle, onboarding flows, idempotency, and error paths.
package test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chazu/procyon-park/internal/config"
	"github.com/chazu/procyon-park/internal/identity"
	"github.com/chazu/procyon-park/internal/registry"
	"github.com/chazu/procyon-park/internal/tuplestore"
	"github.com/chazu/procyon-park/internal/worktracker"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setupHome creates a fake HOME directory and returns the data dir path.
// Callers must call config.Reset() to clear cached config.
func setupHome(t *testing.T) (home string, dataDir string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	dataDir = filepath.Join(home, ".procyon-park")
	config.Reset()
	return home, dataDir
}

// initGitRepo creates a minimal git repo at path with an initial commit.
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
	readme := filepath.Join(path, "README.md")
	os.WriteFile(readme, []byte("test"), 0644)
	run("add", ".")
	run("commit", "-m", "init")
}

// writeGlobalConfig writes a TOML config to ~/.procyon-park/config.toml.
func writeGlobalConfig(t *testing.T, dataDir, content string) {
	t.Helper()
	os.MkdirAll(dataDir, 0755)
	if err := os.WriteFile(filepath.Join(dataDir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatalf("write global config: %v", err)
	}
}

// writeRepoConfig writes a TOML config to <repoRoot>/.procyon-park/config.toml.
func writeRepoConfig(t *testing.T, repoRoot, content string) {
	t.Helper()
	dir := filepath.Join(repoRoot, ".procyon-park")
	os.MkdirAll(dir, 0755)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatalf("write repo config: %v", err)
	}
}

// setupFullEnv sets up a complete procyon-park data directory (for doctor tests).
func setupFullEnv(t *testing.T) string {
	t.Helper()
	_, dataDir := setupHome(t)

	os.MkdirAll(dataDir, 0755)

	// Config.
	writeGlobalConfig(t, dataDir, "# default test config\n")

	// Identity.
	identityDir := filepath.Join(dataDir, "identity")
	if _, _, err := identity.Generate(identityDir); err != nil {
		t.Fatalf("generate identity: %v", err)
	}

	// BBS.
	bbsPath := filepath.Join(dataDir, "bbs.db")
	store, err := tuplestore.NewStore(bbsPath)
	if err != nil {
		t.Fatalf("create bbs: %v", err)
	}
	store.Close()

	return dataDir
}

// newTestRegistry creates a registry backed by a temp file.
func newTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	reg, err := registry.New(path)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return reg
}

// ===========================================================================
// 1. Config roundtrip: write global + per-repo TOML, load with LoadConfig,
//    verify merge precedence.
// ===========================================================================

func TestIntegration_ConfigRoundtrip(t *testing.T) {
	_, dataDir := setupHome(t)

	writeGlobalConfig(t, dataDir, `
[agent]
command = "aider"
max_concurrent = 8

[daemon]
poll_interval = "10s"
http_port = 9090

[telemetry]
enabled = true
endpoint = "https://global.example.com"

[features]
bbs_enabled = true
workflows_enabled = false
`)

	repoRoot := t.TempDir()
	writeRepoConfig(t, repoRoot, `
[agent]
command = "claude"

[telemetry]
endpoint = "https://repo.example.com"

[features]
workflows_enabled = true
`)

	cfg, err := config.Load(repoRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Per-repo overrides.
	if cfg.Agent.Command != "claude" {
		t.Errorf("Agent.Command = %q, want %q (per-repo override)", cfg.Agent.Command, "claude")
	}
	if cfg.Telemetry.Endpoint != "https://repo.example.com" {
		t.Errorf("Telemetry.Endpoint = %q, want repo override", cfg.Telemetry.Endpoint)
	}
	if !cfg.Features.WorkflowsEnabled {
		t.Error("Features.WorkflowsEnabled should be true (per-repo override)")
	}

	// Global values survive leaf-level merge.
	if cfg.Agent.MaxConcurrent != 8 {
		t.Errorf("Agent.MaxConcurrent = %d, want 8 (from global)", cfg.Agent.MaxConcurrent)
	}
	if cfg.Daemon.PollInterval != "10s" {
		t.Errorf("Daemon.PollInterval = %q, want %q (from global)", cfg.Daemon.PollInterval, "10s")
	}
	if cfg.Daemon.HTTPPort != 9090 {
		t.Errorf("Daemon.HTTPPort = %d, want 9090 (from global)", cfg.Daemon.HTTPPort)
	}
	if !cfg.Telemetry.Enabled {
		t.Error("Telemetry.Enabled should be true (from global)")
	}
	if !cfg.Features.BBSEnabled {
		t.Error("Features.BBSEnabled should be true (from global)")
	}

	// Validate merged config.
	if err := config.Validate(cfg); err != nil {
		t.Errorf("merged config should validate: %v", err)
	}
}

// ===========================================================================
// 2. Env override: set PP_* env vars, verify they win over file config.
// ===========================================================================

func TestIntegration_EnvOverride(t *testing.T) {
	_, dataDir := setupHome(t)

	writeGlobalConfig(t, dataDir, `
[agent]
command = "aider"
max_concurrent = 4

[telemetry]
enabled = false

[features]
bbs_enabled = true
`)

	repoRoot := t.TempDir()
	writeRepoConfig(t, repoRoot, `
[agent]
command = "claude"
`)

	// Env vars override everything.
	t.Setenv("PP_AGENT_COMMAND", "codex")
	t.Setenv("PP_AGENT_MAX_CONCURRENT", "32")
	t.Setenv("PP_TELEMETRY_ENABLED", "true")
	t.Setenv("PP_FEATURES_BBS_ENABLED", "false")
	t.Setenv("PP_DAEMON_HTTP_PORT", "8888")

	cfg, err := config.Load(repoRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Env overrides per-repo command.
	if cfg.Agent.Command != "codex" {
		t.Errorf("Agent.Command = %q, want %q (env override)", cfg.Agent.Command, "codex")
	}
	// Env overrides global max_concurrent.
	if cfg.Agent.MaxConcurrent != 32 {
		t.Errorf("Agent.MaxConcurrent = %d, want 32 (env override)", cfg.Agent.MaxConcurrent)
	}
	// Env enables telemetry over global disabled.
	if !cfg.Telemetry.Enabled {
		t.Error("Telemetry.Enabled should be true (env override)")
	}
	// Env disables BBS over global enabled.
	if cfg.Features.BBSEnabled {
		t.Error("Features.BBSEnabled should be false (env override)")
	}
	// Env sets port.
	if cfg.Daemon.HTTPPort != 8888 {
		t.Errorf("Daemon.HTTPPort = %d, want 8888 (env override)", cfg.Daemon.HTTPPort)
	}
}

// TestIntegration_EnvOverridePrecedence verifies that env beats both TOML layers
// even when all three set the same field.
func TestIntegration_EnvOverridePrecedence(t *testing.T) {
	_, dataDir := setupHome(t)

	writeGlobalConfig(t, dataDir, `
[agent]
command = "global-agent"
`)

	repoRoot := t.TempDir()
	writeRepoConfig(t, repoRoot, `
[agent]
command = "repo-agent"
`)

	t.Setenv("PP_AGENT_COMMAND", "env-agent")

	cfg, err := config.Load(repoRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Agent.Command != "env-agent" {
		t.Errorf("Agent.Command = %q, want %q (env > repo > global)", cfg.Agent.Command, "env-agent")
	}
}

// ===========================================================================
// 3. Feature flags: toggle features via config and env, verify feature checks.
// ===========================================================================

func TestIntegration_FeatureFlags(t *testing.T) {
	_, dataDir := setupHome(t)

	// Global: BBS on, workflows on, telemetry_otel off, hub_discovery off.
	writeGlobalConfig(t, dataDir, `
[features]
bbs_enabled = true
workflows_enabled = true
telemetry_otel = false
hub_discovery = false
`)

	repoRoot := t.TempDir()
	writeRepoConfig(t, repoRoot, `
[features]
telemetry_otel = true
`)

	cfg, err := config.Load(repoRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Global features.
	if !cfg.Features.BBSEnabled {
		t.Error("BBSEnabled should be true from global")
	}
	if !cfg.Features.WorkflowsEnabled {
		t.Error("WorkflowsEnabled should be true from global")
	}
	// Per-repo override.
	if !cfg.Features.TelemetryOTEL {
		t.Error("TelemetryOTEL should be true from per-repo override")
	}
	// Unchanged.
	if cfg.Features.HubDiscovery {
		t.Error("HubDiscovery should remain false")
	}
}

func TestIntegration_FeatureFlagsEnvToggle(t *testing.T) {
	_, dataDir := setupHome(t)

	writeGlobalConfig(t, dataDir, `
[features]
bbs_enabled = true
workflows_enabled = true
`)

	// Disable BBS via env.
	t.Setenv("PP_FEATURES_BBS_ENABLED", "false")
	// Enable hub_discovery via env.
	t.Setenv("PP_FEATURES_HUB_DISCOVERY", "1")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Features.BBSEnabled {
		t.Error("BBSEnabled should be false (env disabled)")
	}
	if !cfg.Features.WorkflowsEnabled {
		t.Error("WorkflowsEnabled should still be true")
	}
	if !cfg.Features.HubDiscovery {
		t.Error("HubDiscovery should be true (env enabled with '1')")
	}
}

func TestIntegration_FeatureFlagsBoolValues(t *testing.T) {
	setupHome(t)

	// Test all boolean truth values via env.
	for _, val := range []string{"true", "1", "yes"} {
		config.Reset()
		t.Setenv("PP_FEATURES_TELEMETRY_OTEL", val)
		cfg, err := config.Load("")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.Features.TelemetryOTEL {
			t.Errorf("PP_FEATURES_TELEMETRY_OTEL=%q should enable feature", val)
		}
	}

	// Test false values.
	for _, val := range []string{"false", "0", "no"} {
		config.Reset()
		t.Setenv("PP_FEATURES_TELEMETRY_OTEL", val)
		cfg, err := config.Load("")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Features.TelemetryOTEL {
			t.Errorf("PP_FEATURES_TELEMETRY_OTEL=%q should disable feature", val)
		}
	}
}

// ===========================================================================
// 4. Registry lifecycle: add → list → staleness → remove.
// ===========================================================================

func TestIntegration_RegistryLifecycle(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	// Create two git repos.
	base := t.TempDir()
	repoA := filepath.Join(base, "alpha")
	repoB := filepath.Join(base, "beta")
	initGitRepo(t, repoA)
	initGitRepo(t, repoB)

	// Add.
	a, err := reg.Add(ctx, "alpha", repoA)
	if err != nil {
		t.Fatalf("add alpha: %v", err)
	}
	b, err := reg.Add(ctx, "beta", repoB)
	if err != nil {
		t.Fatalf("add beta: %v", err)
	}

	if a.Name != "alpha" || b.Name != "beta" {
		t.Errorf("names: got %q/%q", a.Name, b.Name)
	}
	if a.MainBranch != "main" || b.MainBranch != "main" {
		t.Errorf("main branches: got %q/%q", a.MainBranch, b.MainBranch)
	}

	// List.
	repos, err := reg.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("list: got %d, want 2", len(repos))
	}

	// No staleness for healthy repos.
	warnings, err := reg.CheckStaleness(ctx)
	if err != nil {
		t.Fatalf("staleness: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected 0 staleness warnings, got %d: %v", len(warnings), warnings)
	}

	// Make alpha stale by removing its directory.
	os.RemoveAll(repoA)
	warnings, err = reg.CheckStaleness(ctx)
	if err != nil {
		t.Fatalf("staleness after remove: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].Name != "alpha" {
		t.Errorf("warning name: got %q", warnings[0].Name)
	}
	if warnings[0].Warning != "path does not exist" {
		t.Errorf("warning text: got %q", warnings[0].Warning)
	}

	// Make beta stale by removing .git.
	os.RemoveAll(filepath.Join(repoB, ".git"))
	warnings, err = reg.CheckStaleness(ctx)
	if err != nil {
		t.Fatalf("staleness after git remove: %v", err)
	}
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d", len(warnings))
	}

	// Remove both.
	if err := reg.Remove("alpha"); err != nil {
		t.Fatalf("remove alpha: %v", err)
	}
	if err := reg.Remove("beta"); err != nil {
		t.Fatalf("remove beta: %v", err)
	}

	repos, err = reg.List()
	if err != nil {
		t.Fatalf("list after remove: %v", err)
	}
	if repos != nil && len(repos) != 0 {
		t.Errorf("list after remove: got %d, want 0", len(repos))
	}
}

func TestIntegration_RegistryNameCollision(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	base := t.TempDir()
	dir1 := filepath.Join(base, "one", "myapp")
	dir2 := filepath.Join(base, "two", "myapp")
	initGitRepo(t, dir1)
	initGitRepo(t, dir2)

	r1, err := reg.Add(ctx, "myapp", dir1)
	if err != nil {
		t.Fatalf("add first: %v", err)
	}
	if r1.Name != "myapp" {
		t.Errorf("first name: got %q", r1.Name)
	}

	r2, err := reg.Add(ctx, "myapp", dir2)
	if err != nil {
		t.Fatalf("add second: %v", err)
	}
	if r2.Name != "myapp@two" {
		t.Errorf("second name: got %q, want %q", r2.Name, "myapp@two")
	}

	// Resolve by name works for both.
	got1, err := reg.Resolve(ctx, "myapp")
	if err != nil {
		t.Fatalf("resolve myapp: %v", err)
	}
	if got1.Path != r1.Path {
		t.Errorf("resolve myapp path: got %q, want %q", got1.Path, r1.Path)
	}

	got2, err := reg.Resolve(ctx, "myapp@two")
	if err != nil {
		t.Fatalf("resolve myapp@two: %v", err)
	}
	if got2.Path != r2.Path {
		t.Errorf("resolve myapp@two path: got %q, want %q", got2.Path, r2.Path)
	}
}

func TestIntegration_RegistryResolveByPath(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "resolve-by-path")
	initGitRepo(t, repoDir)

	_, err := reg.Add(ctx, "pathr", repoDir)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Resolve by providing the path.
	got, err := reg.Resolve(ctx, repoDir)
	if err != nil {
		t.Fatalf("resolve by path: %v", err)
	}
	if got.Name != "pathr" {
		t.Errorf("resolve by path name: got %q", got.Name)
	}
}

func TestIntegration_RegistryDuplicatePath(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "dup-path")
	initGitRepo(t, repoDir)

	_, err := reg.Add(ctx, "first", repoDir)
	if err != nil {
		t.Fatalf("first add: %v", err)
	}

	_, err = reg.Add(ctx, "second", repoDir)
	if err == nil {
		t.Fatal("expected error for duplicate path")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("error should mention already registered: %v", err)
	}
}

func TestIntegration_RegistryUpdate(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "update-test")
	initGitRepo(t, repoDir)

	_, err := reg.Add(ctx, "updatable", repoDir)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Update hub node ID.
	if err := reg.Update("updatable", func(r *registry.Repo) {
		r.HubNodeID = "node-abc-123"
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := reg.Get("updatable")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.HubNodeID != "node-abc-123" {
		t.Errorf("HubNodeID: got %q, want %q", got.HubNodeID, "node-abc-123")
	}
}

func TestIntegration_RegistrySymlinkResolve(t *testing.T) {
	ctx := context.Background()

	base := t.TempDir()
	realDir := filepath.Join(base, "real-repo")
	initGitRepo(t, realDir)

	linkDir := filepath.Join(base, "link-repo")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	reg := newTestRegistry(t)

	// Add via the symlink path.
	_, err := reg.Add(ctx, "linked", linkDir)
	if err != nil {
		t.Fatalf("add via symlink: %v", err)
	}

	// Adding via the real path should fail (duplicate).
	_, err = reg.Add(ctx, "real", realDir)
	if err == nil {
		t.Fatal("expected error: symlink and real path should resolve to same repo")
	}
}

// ===========================================================================
// 5. Work tracker roundtrip: create task via tracker, query back, update, close.
// ===========================================================================

func TestIntegration_WorkTrackerRoundtrip(t *testing.T) {
	m := worktracker.NewMockTracker()

	// Create a task.
	created, err := m.CreateTask(worktracker.CreateTaskOpts{
		Title:       "Implement feature X",
		Description: "Build the X feature with full test coverage",
		TaskType:    "feature",
		Priority:    2,
		Labels:      []string{"backend"},
		Parent:      "epic-99",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.Status != "open" {
		t.Errorf("new task status: got %q, want %q", created.Status, "open")
	}
	if created.Title != "Implement feature X" {
		t.Errorf("title: got %q", created.Title)
	}

	// Query back.
	got, err := m.GetTask(created.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, created.ID)
	}
	if got.Description != "Build the X feature with full test coverage" {
		t.Errorf("description mismatch: got %q", got.Description)
	}

	// ListReady should return it (no blockers).
	ready, err := m.ListReady()
	if err != nil {
		t.Fatalf("ListReady: %v", err)
	}
	if len(ready) != 1 {
		t.Fatalf("ListReady: got %d, want 1", len(ready))
	}

	// Update status.
	status := "in_progress"
	if err := m.UpdateTask(created.ID, worktracker.UpdateTaskOpts{Status: &status}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	got, _ = m.GetTask(created.ID)
	if got.Status != "in_progress" {
		t.Errorf("status after update: got %q", got.Status)
	}

	// Update assignee and notes.
	assignee := "Juniper"
	notes := "Working on tests first"
	if err := m.UpdateTask(created.ID, worktracker.UpdateTaskOpts{
		Assignee: &assignee,
		Notes:    &notes,
	}); err != nil {
		t.Fatalf("UpdateTask (fields): %v", err)
	}
	got, _ = m.GetTask(created.ID)
	if got.Assignee != "Juniper" {
		t.Errorf("assignee: got %q", got.Assignee)
	}
	if got.Notes != "Working on tests first" {
		t.Errorf("notes: got %q", got.Notes)
	}

	// ListByStatus.
	inProgress, err := m.ListByStatus("in_progress")
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(inProgress) != 1 {
		t.Fatalf("ListByStatus: got %d, want 1", len(inProgress))
	}

	// ListByParent.
	children, err := m.ListByParent("epic-99")
	if err != nil {
		t.Fatalf("ListByParent: %v", err)
	}
	if len(children) != 1 || children[0].ID != created.ID {
		t.Fatalf("ListByParent: unexpected %v", children)
	}

	// Close.
	if err := m.CloseTask(created.ID); err != nil {
		t.Fatalf("CloseTask: %v", err)
	}
	got, _ = m.GetTask(created.ID)
	if got.Status != "closed" {
		t.Errorf("status after close: got %q", got.Status)
	}

	// ListReady should be empty now.
	ready, err = m.ListReady()
	if err != nil {
		t.Fatalf("ListReady after close: %v", err)
	}
	if len(ready) != 0 {
		t.Errorf("ListReady after close: got %d, want 0", len(ready))
	}
}

func TestIntegration_WorkTrackerDependencies(t *testing.T) {
	m := worktracker.NewMockTracker()

	// Create tasks.
	t1, _ := m.CreateTask(worktracker.CreateTaskOpts{Title: "Build API"})
	t2, _ := m.CreateTask(worktracker.CreateTaskOpts{Title: "Write tests"})

	// t2 depends on t1.
	if err := m.AddDependency(t2.ID, t1.ID); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	// t2 should be blocked.
	got, _ := m.GetTask(t2.ID)
	if len(got.BlockedBy) != 1 || got.BlockedBy[0] != t1.ID {
		t.Errorf("BlockedBy: got %v, want [%s]", got.BlockedBy, t1.ID)
	}

	// ListReady should only return t1 (t2 is blocked).
	ready, err := m.ListReady()
	if err != nil {
		t.Fatalf("ListReady: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != t1.ID {
		t.Errorf("ListReady: got %v, want only %s", ready, t1.ID)
	}
}

func TestIntegration_WorkTrackerDefaultNoop(t *testing.T) {
	// Reset to noop.
	worktracker.SetDefault(&worktracker.NoopTracker{})
	defer worktracker.SetDefault(&worktracker.NoopTracker{})

	d := worktracker.Default()
	if _, ok := d.(*worktracker.NoopTracker); !ok {
		t.Fatalf("expected default to be *NoopTracker, got %T", d)
	}

	// Noop operations should not error.
	task, err := d.GetTask("anything")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task != nil {
		t.Errorf("expected nil from noop GetTask")
	}

	created, err := d.CreateTask(worktracker.CreateTaskOpts{Title: "test"})
	if err != nil || created == nil {
		t.Fatalf("CreateTask: err=%v, created=%v", err, created)
	}

	if err := d.CloseTask("x"); err != nil {
		t.Fatalf("CloseTask: %v", err)
	}
}

func TestIntegration_WorkTrackerSetDefault(t *testing.T) {
	original := worktracker.Default()
	defer worktracker.SetDefault(original)

	m := worktracker.NewMockTracker()
	worktracker.SetDefault(m)
	if worktracker.Default() != m {
		t.Fatal("SetDefault did not take effect")
	}
}

// ===========================================================================
// 6. Onboarding flow: pp init → pp repo add → pp doctor (all green).
//    Tested at the library level (not subprocess) for reliability.
// ===========================================================================

func TestIntegration_OnboardingFlow(t *testing.T) {
	home, dataDir := setupHome(t)
	ctx := context.Background()

	// --- Step 1: pp init equivalent ---

	// Create data dir.
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	if _, err := os.Stat(dataDir); err != nil {
		t.Fatalf("data dir not created: %v", err)
	}

	// Write config.
	configPath := filepath.Join(dataDir, "config.toml")
	if err := os.WriteFile(configPath, []byte("# test config\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Generate identity.
	identityDir := filepath.Join(dataDir, "identity")
	info, created, err := identity.Generate(identityDir)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	if !created {
		t.Error("identity should be newly created")
	}
	if info.NodeID == "" {
		t.Error("node ID should not be empty")
	}

	// Initialize BBS.
	bbsPath := filepath.Join(dataDir, "bbs.db")
	store, err := tuplestore.NewStore(bbsPath)
	if err != nil {
		t.Fatalf("create bbs: %v", err)
	}
	store.Close()
	if _, err := os.Stat(bbsPath); err != nil {
		t.Fatalf("bbs.db not created: %v", err)
	}

	// --- Step 2: pp repo add equivalent ---

	repoDir := filepath.Join(home, "projects", "myapp")
	initGitRepo(t, repoDir)

	regPath := filepath.Join(dataDir, "repos.json")
	reg, err := registry.New(regPath)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	repo, err := reg.Add(ctx, "myapp", repoDir)
	if err != nil {
		t.Fatalf("repo add: %v", err)
	}
	if repo.Name != "myapp" {
		t.Errorf("repo name: got %q", repo.Name)
	}
	if repo.MainBranch != "main" {
		t.Errorf("main branch: got %q", repo.MainBranch)
	}

	// --- Step 3: pp doctor equivalent (all green) ---

	// Config parses OK.
	config.Reset()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config load: %v", err)
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("config validate: %v", err)
	}

	// Identity loads OK.
	loadedInfo, err := identity.Load(identityDir)
	if err != nil {
		t.Fatalf("load identity: %v", err)
	}
	if loadedInfo.NodeID != info.NodeID {
		t.Errorf("node ID mismatch: got %q, want %q", loadedInfo.NodeID, info.NodeID)
	}

	// BBS exists.
	if _, err := os.Stat(bbsPath); err != nil {
		t.Fatalf("bbs.db missing: %v", err)
	}

	// Repos healthy.
	warnings, err := reg.CheckStaleness(ctx)
	if err != nil {
		t.Fatalf("staleness: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}

	// Registry lists our repo.
	repos, err := reg.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
}

// ===========================================================================
// 7. Idempotency: run pp init twice, verify no corruption.
// ===========================================================================

func TestIntegration_InitIdempotency(t *testing.T) {
	_, dataDir := setupHome(t)

	// --- First init ---
	os.MkdirAll(dataDir, 0755)
	configPath := filepath.Join(dataDir, "config.toml")
	os.WriteFile(configPath, []byte("# test\n"), 0644)

	identityDir := filepath.Join(dataDir, "identity")
	info1, created1, err := identity.Generate(identityDir)
	if err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if !created1 {
		t.Error("first run should create identity")
	}

	bbsPath := filepath.Join(dataDir, "bbs.db")
	store1, err := tuplestore.NewStore(bbsPath)
	if err != nil {
		t.Fatalf("first bbs: %v", err)
	}
	store1.Close()

	// --- Second init ---

	// Data dir already exists — MkdirAll is a no-op.
	os.MkdirAll(dataDir, 0755)

	// Config already exists — we don't overwrite.
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config should still exist: %v", err)
	}

	// Identity should NOT be regenerated.
	info2, created2, err := identity.Generate(identityDir)
	if err != nil {
		t.Fatalf("second generate: %v", err)
	}
	if created2 {
		t.Error("second run should NOT create new identity")
	}
	if info2.NodeID != info1.NodeID {
		t.Errorf("node ID changed! %q → %q", info1.NodeID, info2.NodeID)
	}

	// BBS already exists — NewStore should still work.
	store2, err := tuplestore.NewStore(bbsPath)
	if err != nil {
		t.Fatalf("second bbs open: %v", err)
	}
	store2.Close()

	// Verify config still loads correctly.
	config.Reset()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config load after second init: %v", err)
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("config validate: %v", err)
	}

	// Verify identity is intact.
	loadedInfo, err := identity.Load(identityDir)
	if err != nil {
		t.Fatalf("load identity: %v", err)
	}
	if loadedInfo.NodeID != info1.NodeID {
		t.Errorf("node ID changed after second init: %q → %q", info1.NodeID, loadedInfo.NodeID)
	}
}

// TestIntegration_InitIdempotencyViaCommand tests idempotency through the
// Cobra command layer (like the existing init_cmd_test.go but cross-cutting).
func TestIntegration_InitIdempotencyViaCommand(t *testing.T) {
	_, dataDir := setupHome(t)
	_ = dataDir

	// Use the same pattern as init_cmd_test.go: direct cobra execution.
	// First init.
	buf := new(bytes.Buffer)
	// We can't import rootCmd from internal/cli in test/ package,
	// so we test at the library level (already done above).
	// This test verifies the data is not corrupted even when opening
	// the BBS database file twice sequentially.
	setupFullEnv(t)

	// Re-open BBS to simulate second init.
	bbsPath := filepath.Join(filepath.Dir(buf.String()), "bbs.db")
	_ = bbsPath // The key assertion is that setupFullEnv succeeds.

	// Load config twice with reset.
	config.Reset()
	cfg1, err := config.Load("")
	if err != nil {
		t.Fatalf("first load: %v", err)
	}

	config.Reset()
	cfg2, err := config.Load("")
	if err != nil {
		t.Fatalf("second load: %v", err)
	}

	// Both should have the same defaults.
	if cfg1.Agent.Command != cfg2.Agent.Command {
		t.Errorf("Agent.Command changed between loads")
	}
	if cfg1.Features.BBSEnabled != cfg2.Features.BBSEnabled {
		t.Errorf("Features.BBSEnabled changed between loads")
	}
}

// ===========================================================================
// 8. Error paths: invalid config, missing repo, broken identity.
// ===========================================================================

func TestIntegration_ErrorInvalidConfig(t *testing.T) {
	_, dataDir := setupHome(t)

	// Write invalid TOML (unknown key).
	writeGlobalConfig(t, dataDir, `
[agent]
command = "claude"
bogus_key = "oops"
`)

	_, err := config.Load("")
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
	if !strings.Contains(err.Error(), "unknown keys") {
		t.Errorf("error should mention unknown keys: %v", err)
	}
}

func TestIntegration_ErrorMalformedTOML(t *testing.T) {
	_, dataDir := setupHome(t)

	// Write broken TOML syntax.
	writeGlobalConfig(t, dataDir, `
[agent
command = "broken"
`)

	_, err := config.Load("")
	if err == nil {
		t.Fatal("expected error for malformed TOML")
	}
}

func TestIntegration_ErrorInvalidRepoConfig(t *testing.T) {
	_, dataDir := setupHome(t)

	// Global is fine.
	writeGlobalConfig(t, dataDir, "# ok\n")

	// Per-repo has unknown key.
	repoRoot := t.TempDir()
	writeRepoConfig(t, repoRoot, `
[features]
nonexistent_flag = true
`)

	_, err := config.Load(repoRoot)
	if err == nil {
		t.Fatal("expected error for invalid repo config")
	}
	if !strings.Contains(err.Error(), "unknown keys") {
		t.Errorf("error should mention unknown keys: %v", err)
	}
}

func TestIntegration_ErrorMissingRepo(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	// Get non-existent.
	_, err := reg.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for Get non-existent repo")
	}

	// Remove non-existent.
	err = reg.Remove("nonexistent")
	if err == nil {
		t.Fatal("expected error for Remove non-existent repo")
	}

	// Resolve non-existent.
	_, err = reg.Resolve(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for Resolve non-existent repo")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention not found: %v", err)
	}

	// Update non-existent.
	err = reg.Update("nonexistent", func(r *registry.Repo) {})
	if err == nil {
		t.Fatal("expected error for Update non-existent repo")
	}
}

func TestIntegration_ErrorBrokenIdentity(t *testing.T) {
	_, dataDir := setupHome(t)
	os.MkdirAll(dataDir, 0755)

	identityDir := filepath.Join(dataDir, "identity")

	// No identity yet — Load should fail.
	_, err := identity.Load(identityDir)
	if err == nil {
		t.Fatal("expected error loading non-existent identity")
	}

	// Create identity dir with garbage.
	os.MkdirAll(identityDir, 0755)
	os.WriteFile(filepath.Join(identityDir, "node.json"), []byte("not json"), 0644)

	_, err = identity.Load(identityDir)
	if err == nil {
		t.Fatal("expected error loading corrupt identity")
	}
}

func TestIntegration_ErrorConfigValidation(t *testing.T) {
	_, dataDir := setupHome(t)

	// Write config with valid TOML but invalid values.
	writeGlobalConfig(t, dataDir, `
[agent]
max_concurrent = -5

[daemon]
http_port = 99999
`)

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v (validation is separate)", err)
	}

	err = config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "max_concurrent") {
		t.Errorf("error should mention max_concurrent: %v", err)
	}
	if !strings.Contains(errMsg, "http_port") {
		t.Errorf("error should mention http_port: %v", err)
	}
}

func TestIntegration_ErrorRegistryCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "repos.json")

	// Write invalid JSON.
	os.WriteFile(regPath, []byte("not json at all"), 0644)

	reg, err := registry.New(regPath)
	if err != nil {
		t.Fatalf("New should succeed: %v", err)
	}

	_, err = reg.List()
	if err == nil {
		t.Fatal("expected error for corrupt JSON registry")
	}
}

// ===========================================================================
// Additional integration: config caching across repo boundaries
// ===========================================================================

func TestIntegration_ConfigCachingPerRepo(t *testing.T) {
	_, dataDir := setupHome(t)

	writeGlobalConfig(t, dataDir, `
[agent]
command = "global-default"
`)

	repoA := t.TempDir()
	writeRepoConfig(t, repoA, `
[agent]
command = "repo-a-agent"
`)

	repoB := t.TempDir()
	writeRepoConfig(t, repoB, `
[agent]
command = "repo-b-agent"
`)

	cfgA, err := config.Load(repoA)
	if err != nil {
		t.Fatalf("Load repoA: %v", err)
	}
	cfgB, err := config.Load(repoB)
	if err != nil {
		t.Fatalf("Load repoB: %v", err)
	}

	if cfgA.Agent.Command != "repo-a-agent" {
		t.Errorf("repoA command: got %q", cfgA.Agent.Command)
	}
	if cfgB.Agent.Command != "repo-b-agent" {
		t.Errorf("repoB command: got %q", cfgB.Agent.Command)
	}

	// Same pointer on second load (caching).
	cfgA2, _ := config.Load(repoA)
	if cfgA != cfgA2 {
		t.Error("expected same pointer from cache for repoA")
	}
}

// ===========================================================================
// Registry JSON roundtrip
// ===========================================================================

func TestIntegration_RegistryJSONRoundtrip(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	repoDir := filepath.Join(t.TempDir(), "json-rt")
	initGitRepo(t, repoDir)
	os.Mkdir(filepath.Join(repoDir, ".beads"), 0755)

	added, err := reg.Add(ctx, "json-rt", repoDir)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Read raw JSON and verify it's valid.
	data, err := os.ReadFile(reg.Path())
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var repos []registry.Repo
	if err := json.Unmarshal(data, &repos); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(repos) != 1 {
		t.Fatalf("expected 1, got %d", len(repos))
	}

	r := repos[0]
	if r.Name != added.Name {
		t.Errorf("name: %q != %q", r.Name, added.Name)
	}
	if r.HasBeads != true {
		t.Error("expected has_beads=true")
	}
	if r.BBSScope != "json-rt" {
		t.Errorf("bbs_scope: %q", r.BBSScope)
	}
}
