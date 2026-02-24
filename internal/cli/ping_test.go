package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chazu/procyon-park/internal/ipc"
)

func TestPingCmd_Text(t *testing.T) {
	sock := startMockDaemon(t, map[string]methodHandler{
		"system.ping": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`"pong"`), nil
		},
	})

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"ping", "--socket", sock, "--output", "text"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "pong") {
		t.Fatalf("expected 'pong' in output, got %q", out)
	}
}

func TestPingCmd_JSON(t *testing.T) {
	sock := startMockDaemon(t, map[string]methodHandler{
		"system.ping": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`"pong"`), nil
		},
	})

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"ping", "--socket", sock, "--output", "json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("expected valid JSON, got %q: %v", out, err)
	}
	if resp["status"] != "ok" {
		t.Fatalf("expected status=ok, got %v", resp["status"])
	}
}

func TestPingCmd_DaemonDown(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"ping", "--socket", "/tmp/nonexistent-ping-test.sock"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when daemon is down")
	}
}
