package cli

import (
	"testing"
)

// resetFlags resets global flag state between tests to prevent leakage
// from previous ExecuteArgs calls. Cobra doesn't reset persistent flag
// values when rootCmd is reused.
func resetFlags(t *testing.T) {
	t.Helper()
	flagSocket = ""
	flagOutput = "text"
	flagQuiet = false
	flagVerbose = false
	flagNoColor = false
	flagDataDir = ""
}

func TestAgentCmd_NoSubcommand(t *testing.T) {
	resetFlags(t)
	code := ExecuteArgs([]string{"agent"})
	if code != ExitError {
		t.Fatalf("expected exit code %d, got %d", ExitError, code)
	}
}

func TestAgentCmd_UnknownSubcommand(t *testing.T) {
	resetFlags(t)
	code := ExecuteArgs([]string{"agent", "foo"})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code for unknown subcommand")
	}
}

func TestAgentSpawnCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"agent", "spawn",
		"--socket", socketPath,
		"--role", "cub",
		"--task-id", "test-123",
		"--base-branch", "main",
		"--repo-name", "test-repo",
		"--repo-root", "/tmp/test",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestAgentDismissCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"agent", "dismiss",
		"--socket", socketPath,
		"--agent-name", "Sprocket",
		"--repo-name", "test-repo",
		"--repo-root", "/tmp/test",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestAgentStatusCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"agent", "status",
		"--socket", socketPath,
		"--agent-name", "Sprocket",
		"--repo-name", "test-repo",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestAgentListCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"agent", "list", "--socket", socketPath, "--repo-name", "test"})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestAgentPruneCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"agent", "prune",
		"--socket", socketPath,
		"--repo-name", "test",
		"--repo-root", "/tmp/test",
		"--worktree-base", "/tmp/wt",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestAgentRespawnCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"agent", "respawn",
		"--socket", socketPath,
		"--agent-name", "Sprocket",
		"--repo-name", "test-repo",
		"--repo-root", "/tmp/test",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestAgentShowCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"agent", "show", "Sprocket",
		"--socket", socketPath,
		"--repo-name", "test-repo",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestAgentShowCmd_MissingName(t *testing.T) {
	resetFlags(t)
	// 'agent show' without positional arg should fail.
	code := ExecuteArgs([]string{"agent", "show", "--repo-name", "test"})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code for missing agent name")
	}
}

func TestAgentStuckCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"agent", "stuck", "Sprocket",
		"--socket", socketPath,
		"--repo-name", "test-repo",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestAgentLogsCmd_NoTmux(t *testing.T) {
	resetFlags(t)
	// logs without a valid tmux session should fail.
	code := ExecuteArgs([]string{"agent", "logs", "Sprocket",
		"--repo-name", "test-repo",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code for invalid tmux session")
	}
}
