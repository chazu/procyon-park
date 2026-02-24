package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chazu/procyon-park/internal/ipc"
)

func TestListCmd_JSON(t *testing.T) {
	sock := startMockDaemon(t, map[string]methodHandler{
		"agent.list": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`[{"name":"Marble","role":"cub","status":"active"}]`), nil
		},
	})

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"list", "--socket", sock, "--output", "json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Marble") {
		t.Fatalf("expected agent name in output, got %q", out)
	}
}

func TestListCmd_Text(t *testing.T) {
	sock := startMockDaemon(t, map[string]methodHandler{
		"agent.list": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`[{"name":"Sprocket","role":"cub","status":"active","task":"xyz-123"}]`), nil
		},
	})

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"list", "--socket", sock, "--output", "text"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Sprocket") {
		t.Fatalf("expected agent name in table output, got %q", out)
	}
}

func TestListCmd_Empty(t *testing.T) {
	sock := startMockDaemon(t, map[string]methodHandler{
		"agent.list": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`[]`), nil
		},
	})

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"list", "--socket", sock, "--output", "text"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "(no agents)") {
		t.Fatalf("expected '(no agents)' for empty list, got %q", out)
	}
}
