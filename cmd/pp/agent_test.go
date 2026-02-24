package main

import (
	"testing"
)

// ---------------------------------------------------------------------------
// parseAgentArgs Tests
// ---------------------------------------------------------------------------

func TestParseAgentArgs_ValidSubcommands(t *testing.T) {
	for _, subcmd := range []string{"spawn", "dismiss", "status", "list", "prune"} {
		t.Run(subcmd, func(t *testing.T) {
			got, _, _, err := parseAgentArgs([]string{subcmd})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != subcmd {
				t.Fatalf("expected subcmd %q, got %q", subcmd, got)
			}
		})
	}
}

func TestParseAgentArgs_DefaultDataDir(t *testing.T) {
	_, _, dataDir, err := parseAgentArgs([]string{"list"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := defaultDataDir()
	if dataDir != expected {
		t.Fatalf("expected default data dir %q, got %q", expected, dataDir)
	}
}

func TestParseAgentArgs_CustomDataDir(t *testing.T) {
	_, remaining, dataDir, err := parseAgentArgs([]string{"list", "--data-dir", "/tmp/test-pp", "--repo-name", "myrepo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dataDir != "/tmp/test-pp" {
		t.Fatalf("expected /tmp/test-pp, got %q", dataDir)
	}
	// --data-dir should be stripped from remaining args.
	for _, r := range remaining {
		if r == "--data-dir" || r == "/tmp/test-pp" {
			t.Fatalf("--data-dir should be stripped from remaining args, got %v", remaining)
		}
	}
	// --repo-name should remain.
	found := false
	for _, r := range remaining {
		if r == "--repo-name" {
			found = true
		}
	}
	if !found {
		t.Fatalf("--repo-name should remain in args, got %v", remaining)
	}
}

func TestParseAgentArgs_MissingSubcommand(t *testing.T) {
	_, _, _, err := parseAgentArgs([]string{})
	if err == nil {
		t.Fatal("expected error for missing subcommand")
	}
}

func TestParseAgentArgs_UnknownSubcommand(t *testing.T) {
	_, _, _, err := parseAgentArgs([]string{"restart"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}

func TestParseAgentArgs_DataDirMissingValue(t *testing.T) {
	_, _, _, err := parseAgentArgs([]string{"list", "--data-dir"})
	if err == nil {
		t.Fatal("expected error for --data-dir without value")
	}
}

// ---------------------------------------------------------------------------
// parseFlags Tests
// ---------------------------------------------------------------------------

func TestParseFlags_Basic(t *testing.T) {
	flags, err := parseFlags([]string{"--role", "cub", "--task-id", "abc-123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags["role"] != "cub" {
		t.Fatalf("expected role=cub, got %q", flags["role"])
	}
	if flags["task-id"] != "abc-123" {
		t.Fatalf("expected task-id=abc-123, got %q", flags["task-id"])
	}
}

func TestParseFlags_Empty(t *testing.T) {
	flags, err := parseFlags([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(flags) != 0 {
		t.Fatalf("expected empty flags, got %v", flags)
	}
}

func TestParseFlags_UnexpectedPositional(t *testing.T) {
	_, err := parseFlags([]string{"oops"})
	if err == nil {
		t.Fatal("expected error for positional argument")
	}
}

// ---------------------------------------------------------------------------
// requireFlag Tests
// ---------------------------------------------------------------------------

func TestRequireFlag_Present(t *testing.T) {
	flags := map[string]string{"name": "Sprocket"}
	v, err := requireFlag(flags, "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "Sprocket" {
		t.Fatalf("expected Sprocket, got %q", v)
	}
}

func TestRequireFlag_Missing(t *testing.T) {
	flags := map[string]string{}
	_, err := requireFlag(flags, "name")
	if err == nil {
		t.Fatal("expected error for missing flag")
	}
}

func TestRequireFlag_Empty(t *testing.T) {
	flags := map[string]string{"name": ""}
	_, err := requireFlag(flags, "name")
	if err == nil {
		t.Fatal("expected error for empty flag value")
	}
}

// ---------------------------------------------------------------------------
// handleAgent Dispatch Tests (no daemon → connection error)
// ---------------------------------------------------------------------------

func TestHandleAgentSpawn_NoDaemon(t *testing.T) {
	code := handleAgent([]string{"spawn",
		"--data-dir", t.TempDir(),
		"--role", "cub",
		"--task-id", "test-123",
		"--base-branch", "main",
		"--repo-name", "test-repo",
		"--repo-root", "/tmp/test",
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 (no daemon), got %d", code)
	}
}

func TestHandleAgentDismiss_NoDaemon(t *testing.T) {
	code := handleAgent([]string{"dismiss",
		"--data-dir", t.TempDir(),
		"--agent-name", "Sprocket",
		"--repo-name", "test-repo",
		"--repo-root", "/tmp/test",
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 (no daemon), got %d", code)
	}
}

func TestHandleAgentStatus_NoDaemon(t *testing.T) {
	code := handleAgent([]string{"status",
		"--data-dir", t.TempDir(),
		"--agent-name", "Sprocket",
		"--repo-name", "test-repo",
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 (no daemon), got %d", code)
	}
}

func TestHandleAgentList_NoDaemon(t *testing.T) {
	code := handleAgent([]string{"list",
		"--data-dir", t.TempDir(),
		"--repo-name", "test-repo",
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 (no daemon), got %d", code)
	}
}

func TestHandleAgentPrune_NoDaemon(t *testing.T) {
	code := handleAgent([]string{"prune",
		"--data-dir", t.TempDir(),
		"--repo-name", "test-repo",
		"--repo-root", "/tmp/test",
		"--worktree-base", "/tmp/worktrees",
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 (no daemon), got %d", code)
	}
}

// ---------------------------------------------------------------------------
// Missing required flags
// ---------------------------------------------------------------------------

func TestHandleAgentSpawn_MissingRole(t *testing.T) {
	code := handleAgent([]string{"spawn",
		"--data-dir", t.TempDir(),
		"--task-id", "test-123",
		"--base-branch", "main",
		"--repo-name", "test-repo",
		"--repo-root", "/tmp/test",
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 for missing --role, got %d", code)
	}
}

func TestHandleAgentDismiss_MissingAgentName(t *testing.T) {
	code := handleAgent([]string{"dismiss",
		"--data-dir", t.TempDir(),
		"--repo-name", "test-repo",
		"--repo-root", "/tmp/test",
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 for missing --agent-name, got %d", code)
	}
}

func TestHandleAgentStatus_MissingAgentName(t *testing.T) {
	code := handleAgent([]string{"status",
		"--data-dir", t.TempDir(),
		"--repo-name", "test-repo",
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 for missing --agent-name, got %d", code)
	}
}

func TestHandleAgentList_MissingRepoName(t *testing.T) {
	code := handleAgent([]string{"list",
		"--data-dir", t.TempDir(),
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 for missing --repo-name, got %d", code)
	}
}

func TestHandleAgentPrune_MissingWorktreeBase(t *testing.T) {
	code := handleAgent([]string{"prune",
		"--data-dir", t.TempDir(),
		"--repo-name", "test-repo",
		"--repo-root", "/tmp/test",
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 for missing --worktree-base, got %d", code)
	}
}

// ---------------------------------------------------------------------------
// run() Dispatch Tests for agent
// ---------------------------------------------------------------------------

func TestRunAgentDispatch(t *testing.T) {
	// 'pp agent' with no subcommand should return error exit code
	code := run([]string{"agent"})
	if code != 1 {
		t.Fatalf("expected exit code 1 for 'pp agent' with no subcommand, got %d", code)
	}
}

func TestRunAgentListDispatch(t *testing.T) {
	// 'pp agent list' with no daemon should return 1
	code := run([]string{"agent", "list", "--data-dir", t.TempDir(), "--repo-name", "test"})
	if code != 1 {
		t.Fatalf("expected exit code 1 (no daemon running), got %d", code)
	}
}

// ---------------------------------------------------------------------------
// agentUsage Tests
// ---------------------------------------------------------------------------

func TestAgentUsage(t *testing.T) {
	usage := agentUsage()
	if len(usage) == 0 {
		t.Fatal("agentUsage should return non-empty string")
	}
	// Verify all subcommands are mentioned.
	for _, subcmd := range []string{"spawn", "dismiss", "status", "list", "prune"} {
		if !contains(usage, subcmd) {
			t.Fatalf("agentUsage should mention %q", subcmd)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
