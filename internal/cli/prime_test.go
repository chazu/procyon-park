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

	oldSocket := flagSocket
	flagSocket = sock
	oldOutput := flagOutput
	flagOutput = "text"
	defer func() {
		flagSocket = oldSocket
		flagOutput = oldOutput
	}()

	os.Setenv("PP_AGENT_ROLE", "cub")
	defer os.Unsetenv("PP_AGENT_ROLE")

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"prime"})
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

	oldSocket := flagSocket
	flagSocket = sock
	oldOutput := flagOutput
	flagOutput = "text"
	defer func() {
		flagSocket = oldSocket
		flagOutput = oldOutput
	}()

	os.Unsetenv("PP_AGENT_ROLE")

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"prime"})
	rootCmd.Execute()

	if receivedRole != "cub" {
		t.Fatalf("expected default role 'cub', got %q", receivedRole)
	}
}

func TestPrimeCmd_JSON(t *testing.T) {
	sock := startMockDaemon(t, map[string]methodHandler{
		"system.prime": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`{"role":"cub","instructions":"do stuff"}`), nil
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
	rootCmd.SetArgs([]string{"prime"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "instructions") {
		t.Fatalf("expected JSON output with instructions, got %q", out)
	}
}
