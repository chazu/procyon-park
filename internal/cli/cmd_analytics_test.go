package cli

import (
	"testing"
)

// ---------- analytics command tests ----------

func TestAnalyticsCmd_NoSubcommand(t *testing.T) {
	resetFlags(t)
	code := ExecuteArgs([]string{"analytics"})
	if code != ExitError {
		t.Fatalf("expected exit code %d, got %d", ExitError, code)
	}
}

func TestAnalyticsCmd_UnknownSubcommand(t *testing.T) {
	resetFlags(t)
	code := ExecuteArgs([]string{"analytics", "foo"})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code for unknown subcommand")
	}
}

func TestAnalyticsPerformanceCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"analytics", "performance",
		"--socket", socketPath,
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestAnalyticsPerformanceCmd_WithRepo(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"analytics", "performance",
		"--socket", socketPath,
		"--repo", "test-repo",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestAnalyticsObstaclesCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"analytics", "obstacles",
		"--socket", socketPath,
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestAnalyticsObstaclesCmd_WithMinCount(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"analytics", "obstacles",
		"--socket", socketPath,
		"--min-count", "5",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestAnalyticsConventionsCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"analytics", "conventions",
		"--socket", socketPath,
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestAnalyticsKnowledgeCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"analytics", "knowledge",
		"--socket", socketPath,
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestAnalyticsSignaturesCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"analytics", "signatures",
		"--socket", socketPath,
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

// ---------- gc command tests ----------

func TestGCCmd_NoSubcommand(t *testing.T) {
	resetFlags(t)
	code := ExecuteArgs([]string{"gc"})
	if code != ExitError {
		t.Fatalf("expected exit code %d, got %d", ExitError, code)
	}
}

func TestGCRunCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"gc", "run",
		"--socket", socketPath,
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestGCStatusCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"gc", "status",
		"--socket", socketPath,
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

// ---------- feedback command tests ----------

func TestFeedbackCmd_NoSubcommand(t *testing.T) {
	resetFlags(t)
	code := ExecuteArgs([]string{"feedback"})
	if code != ExitError {
		t.Fatalf("expected exit code %d, got %d", ExitError, code)
	}
}

func TestFeedbackRunCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"feedback", "run",
		"--socket", socketPath,
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestFeedbackRunCmd_WithRepo(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"feedback", "run",
		"--socket", socketPath,
		"--repo", "test-repo",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

// ---------- synthesis command tests ----------

func TestSynthesisCmd_NoSubcommand(t *testing.T) {
	resetFlags(t)
	code := ExecuteArgs([]string{"synthesis"})
	if code != ExitError {
		t.Fatalf("expected exit code %d, got %d", ExitError, code)
	}
}

func TestSynthesisRunCmd_MissingTask(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"synthesis", "run",
		"--socket", socketPath,
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when --task is missing")
	}
}

func TestSynthesisRunCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"synthesis", "run",
		"--socket", socketPath,
		"--task", "test-task-123",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

// ---------- helper function tests ----------

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "he..."},
		{"hello", 5, "hello"},
		{"hi", 3, "hi"},
		{"hello", 3, "hel"},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}
