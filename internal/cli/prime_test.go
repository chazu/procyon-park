package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/chazu/procyon-park/internal/ipc"
)

func TestPrimeCmd_Text(t *testing.T) {
	var receivedRole string
	sock := startMockDaemon(t, map[string]methodHandler{
		"system.prime": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			var p map[string]string
			json.Unmarshal(params, &p)
			receivedRole = p["role"]
			return json.RawMessage(`"You are an pp agent. Do your work."`), nil
		},
	})

	os.Setenv("PP_AGENT_ROLE", "cub")
	defer os.Unsetenv("PP_AGENT_ROLE")

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"prime", "--socket", sock, "--output", "text"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "You are an pp agent") {
		t.Fatalf("expected instructions in output, got %q", out)
	}
	if receivedRole != "cub" {
		t.Fatalf("expected role=cub sent to daemon, got %q", receivedRole)
	}
}

func TestPrimeCmd_DefaultRole(t *testing.T) {
	var receivedRole string
	sock := startMockDaemon(t, map[string]methodHandler{
		"system.prime": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			var p map[string]string
			json.Unmarshal(params, &p)
			receivedRole = p["role"]
			return json.RawMessage(`"instructions"`), nil
		},
	})

	os.Unsetenv("PP_AGENT_ROLE")

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"prime", "--socket", sock, "--output", "text"})
	rootCmd.Execute()

	if receivedRole != "cub" {
		t.Fatalf("expected default role 'cub', got %q", receivedRole)
	}
}

func TestPrimeCmd_SendsAllEnvVars(t *testing.T) {
	var received map[string]string
	sock := startMockDaemon(t, map[string]methodHandler{
		"system.prime": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			received = make(map[string]string)
			json.Unmarshal(params, &received)
			return json.RawMessage(`"ok"`), nil
		},
	})

	os.Setenv("PP_AGENT_ROLE", "king")
	os.Setenv("PP_AGENT_NAME", "Marble")
	os.Setenv("PP_REPO", "myrepo")
	os.Setenv("PP_TASK", "task-42")
	os.Setenv("PP_BRANCH", "agent/Marble/task-42")
	os.Setenv("PP_WORKTREE", "/tmp/wt/Marble")
	defer func() {
		os.Unsetenv("PP_AGENT_ROLE")
		os.Unsetenv("PP_AGENT_NAME")
		os.Unsetenv("PP_REPO")
		os.Unsetenv("PP_TASK")
		os.Unsetenv("PP_BRANCH")
		os.Unsetenv("PP_WORKTREE")
	}()

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"prime", "--socket", sock, "--output", "text"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := map[string]string{
		"role":       "king",
		"agent_name": "Marble",
		"repo":       "myrepo",
		"task_id":    "task-42",
		"branch":     "agent/Marble/task-42",
		"worktree":   "/tmp/wt/Marble",
	}
	for key, want := range checks {
		if got := received[key]; got != want {
			t.Errorf("param %q: got %q, want %q", key, got, want)
		}
	}
}

func TestPrimeCmd_JSON(t *testing.T) {
	sock := startMockDaemon(t, map[string]methodHandler{
		"system.prime": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`{"role":"cub","instructions":"do stuff"}`), nil
		},
	})

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"prime", "--socket", sock, "--output", "json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "instructions") {
		t.Fatalf("expected JSON output with instructions, got %q", out)
	}
}
