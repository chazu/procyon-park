package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCmd_Text(t *testing.T) {
	old := Version
	Version = "1.2.3-test"
	oldOutput := flagOutput
	flagOutput = "text"
	defer func() {
		Version = old
		flagOutput = oldOutput
	}()

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "1.2.3-test") {
		t.Fatalf("expected version in output, got %q", out)
	}
	if !strings.Contains(out, "pp version") {
		t.Fatalf("expected 'pp version' prefix, got %q", out)
	}
}

func TestVersionCmd_JSON(t *testing.T) {
	old := Version
	Version = "1.2.3-test"
	oldOutput := flagOutput
	flagOutput = "json"
	defer func() {
		Version = old
		flagOutput = oldOutput
	}()

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, `"version"`) {
		t.Fatalf("expected JSON output, got %q", out)
	}
	if !strings.Contains(out, "1.2.3-test") {
		t.Fatalf("expected version in JSON, got %q", out)
	}
}
