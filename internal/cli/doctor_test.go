package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chazu/procyon-park/internal/config"
	"github.com/chazu/procyon-park/internal/identity"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// setupDoctorEnv creates a minimal valid procyon-park data directory.
func setupDoctorEnv(t *testing.T) (string, func()) {
	t.Helper()

	tmpHome := t.TempDir()
	dataDir := filepath.Join(tmpHome, ".procyon-park")

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)

	// Create data dir.
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write default config.
	configPath := filepath.Join(dataDir, "config.toml")
	if err := os.WriteFile(configPath, []byte("# test config\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Generate identity.
	identityDir := filepath.Join(dataDir, "identity")
	if _, _, err := identity.Generate(identityDir); err != nil {
		t.Fatal(err)
	}

	// Create BBS.
	bbsPath := filepath.Join(dataDir, "bbs.db")
	store, err := tuplestore.NewStore(bbsPath)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	cleanup := func() {
		os.Setenv("HOME", oldHome)
		config.Reset()
	}

	return dataDir, cleanup
}

func TestCheckDataDir_Exists(t *testing.T) {
	dataDir, cleanup := setupDoctorEnv(t)
	defer cleanup()

	r := checkDataDir(dataDir)
	if !r.OK {
		t.Fatalf("expected OK, got: %s", r.Message)
	}
	if !strings.Contains(r.Message, dataDir) {
		t.Fatalf("expected message to contain data dir path, got: %s", r.Message)
	}
}

func TestCheckDataDir_Missing(t *testing.T) {
	r := checkDataDir("/nonexistent/path/procyon-park")
	if r.OK {
		t.Fatal("expected failure for missing data dir")
	}
	if r.Fix == "" {
		t.Fatal("expected actionable fix message")
	}
}

func TestCheckConfigParseable_Valid(t *testing.T) {
	_, cleanup := setupDoctorEnv(t)
	defer cleanup()

	r := checkConfigParseable(DataDir())
	if !r.OK {
		t.Fatalf("expected OK, got: %s", r.Message)
	}
}

func TestCheckConfigParseable_Missing(t *testing.T) {
	tmpDir := t.TempDir()
	r := checkConfigParseable(tmpDir)
	if r.OK {
		t.Fatal("expected failure for missing config")
	}
}

func TestCheckConfigParseable_Invalid(t *testing.T) {
	tmpHome := t.TempDir()
	dataDir := filepath.Join(tmpHome, ".procyon-park")
	os.MkdirAll(dataDir, 0755)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", oldHome)
	defer config.Reset()

	// Write invalid TOML with unknown key.
	configPath := filepath.Join(dataDir, "config.toml")
	os.WriteFile(configPath, []byte("bogus_key = true\n"), 0644)

	r := checkConfigParseable(dataDir)
	if r.OK {
		t.Fatal("expected failure for invalid config")
	}
	if !strings.Contains(r.Message, "parse error") && !strings.Contains(r.Message, "unknown") {
		t.Fatalf("expected parse/validation error, got: %s", r.Message)
	}
}

func TestCheckIdentity_Valid(t *testing.T) {
	dataDir, cleanup := setupDoctorEnv(t)
	defer cleanup()

	r := checkIdentity(dataDir)
	if !r.OK {
		t.Fatalf("expected OK, got: %s", r.Message)
	}
	if !strings.Contains(r.Message, "Node identity:") {
		t.Fatalf("expected node ID in message, got: %s", r.Message)
	}
}

func TestCheckIdentity_Missing(t *testing.T) {
	r := checkIdentity(t.TempDir())
	if r.OK {
		t.Fatal("expected failure for missing identity")
	}
}

func TestCheckBBS_Valid(t *testing.T) {
	dataDir, cleanup := setupDoctorEnv(t)
	defer cleanup()

	r := checkBBS(dataDir)
	if !r.OK {
		t.Fatalf("expected OK, got: %s", r.Message)
	}
}

func TestCheckBBS_Missing(t *testing.T) {
	r := checkBBS(t.TempDir())
	if r.OK {
		t.Fatal("expected failure for missing BBS")
	}
}

func TestCheckGit_Available(t *testing.T) {
	r := checkGit(context.Background())
	// git should be available in test environments.
	if !r.OK {
		t.Skipf("git not available: %s", r.Message)
	}
	if !strings.Contains(r.Message, "git version") {
		t.Fatalf("expected git version string, got: %s", r.Message)
	}
}

func TestCheckRepos_NoneRegistered(t *testing.T) {
	r := checkRepos(context.Background(), t.TempDir())
	if !r.OK {
		t.Fatalf("expected OK with no repos, got: %s", r.Message)
	}
}

func TestCheckRepos_BrokenRepo(t *testing.T) {
	tmpDir := t.TempDir()
	regPath := filepath.Join(tmpDir, "repos.json")

	// Write a repo pointing to a nonexistent path.
	repoData := []map[string]interface{}{
		{"name": "ghost", "path": "/nonexistent/repo", "main_branch": "main"},
	}
	data, _ := json.Marshal(repoData)
	os.WriteFile(regPath, data, 0644)

	r := checkRepos(context.Background(), tmpDir)
	if r.OK {
		t.Fatal("expected failure for broken repo")
	}
	if !strings.Contains(r.Message, "ghost") {
		t.Fatalf("expected repo name in error, got: %s", r.Message)
	}
}

func TestCheckDaemonSocket_NotRunning(t *testing.T) {
	r := checkDaemonSocket(t.TempDir())
	if r.OK {
		t.Fatal("expected failure with no daemon running")
	}
	if r.Fix == "" {
		t.Fatal("expected actionable fix")
	}
}

func TestRunDoctorChecks_AllPass(t *testing.T) {
	dataDir, cleanup := setupDoctorEnv(t)
	defer cleanup()

	results := runDoctorChecks(context.Background(), dataDir)

	// We expect most checks to pass except daemon (not running in tests).
	for _, r := range results {
		if r.Name == "daemon" || r.Name == "tmux" {
			continue // These may fail in test environments.
		}
		if !r.OK {
			t.Errorf("check %q failed: %s", r.Name, r.Message)
		}
	}
}

func TestDoctorCmd_TextOutput(t *testing.T) {
	_, cleanup := setupDoctorEnv(t)
	defer cleanup()

	// Force --no-color for consistent output.
	flagNoColor = true
	defer func() { flagNoColor = false }()

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"doctor"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "✓") && !strings.Contains(out, "✗") {
		t.Fatalf("expected check marks in output, got: %q", out)
	}
}

func TestDoctorCmd_JSONOutput(t *testing.T) {
	_, cleanup := setupDoctorEnv(t)
	defer cleanup()

	flagOutput = "json"
	defer func() { flagOutput = "text" }()

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"doctor", "--output", "json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var results []CheckResult
	if err := json.Unmarshal(buf.Bytes(), &results); err != nil {
		t.Fatalf("expected valid JSON, got parse error: %v\nraw: %s", err, buf.String())
	}
	if len(results) == 0 {
		t.Fatal("expected at least one check result")
	}

	// Verify structure.
	for _, r := range results {
		if r.Name == "" {
			t.Error("check result missing name")
		}
		if r.Message == "" {
			t.Error("check result missing message")
		}
	}
}

func TestDoctorCmd_FailureCases(t *testing.T) {
	// Use empty temp dir — everything should fail except git/tmux.
	tmpHome := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", oldHome)
	defer config.Reset()

	flagNoColor = true
	defer func() { flagNoColor = false }()

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"doctor"})
	rootCmd.Execute()

	out := buf.String()
	if !strings.Contains(out, "✗") {
		t.Fatalf("expected failures in output, got: %q", out)
	}
	if !strings.Contains(out, "failed") {
		t.Fatalf("expected failure count, got: %q", out)
	}
}
