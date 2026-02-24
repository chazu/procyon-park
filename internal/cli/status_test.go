package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chazu/procyon-park/internal/ipc"
)

func TestStatusCmd_Text(t *testing.T) {
	sock := startMockDaemon(t, map[string]methodHandler{
		"system.status": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`{"status":"running","pid":1234}`), nil
		},
		"agent.list": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`[{"name":"Marble","role":"cub","status":"active","task":"test-123"}]`), nil
		},
	})

	oldSocket := flagSocket
	flagSocket = sock
	oldOutput := flagOutput
	flagOutput = "text"
	defer func() {
		flagSocket = oldSocket
		flagOutput = oldOutput
	}()

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"status"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "=== Daemon ===") {
		t.Fatalf("expected daemon section, got %q", out)
	}
	if !strings.Contains(out, "=== Agents ===") {
		t.Fatalf("expected agents section, got %q", out)
	}
}

func TestStatusCmd_JSON(t *testing.T) {
	sock := startMockDaemon(t, map[string]methodHandler{
		"system.status": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`{"status":"running"}`), nil
		},
		"agent.list": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`[]`), nil
		},
	})

	oldSocket := flagSocket
	flagSocket = sock
	oldOutput := flagOutput
	flagOutput = "json"
	defer func() {
		flagSocket = oldSocket
		flagOutput = oldOutput
	}()

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"status"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("expected valid JSON, got %q: %v", out, err)
	}
	if _, ok := resp["daemon"]; !ok {
		t.Fatal("expected 'daemon' key in JSON response")
	}
	if _, ok := resp["agents"]; !ok {
		t.Fatal("expected 'agents' key in JSON response")
	}
}

func TestStatusCmd_NoAgents(t *testing.T) {
	sock := startMockDaemon(t, map[string]methodHandler{
		"system.status": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`{"status":"running"}`), nil
		},
		"agent.list": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`[]`), nil
		},
	})

	oldSocket := flagSocket
	flagSocket = sock
	oldOutput := flagOutput
	flagOutput = "text"
	defer func() {
		flagSocket = oldSocket
		flagOutput = oldOutput
	}()

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"status"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "(no agents)") {
		t.Fatalf("expected '(no agents)' for empty agent list, got %q", out)
	}
}
