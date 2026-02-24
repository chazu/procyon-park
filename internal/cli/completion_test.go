package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompletionCmd_Bash(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"completion", "bash"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "bash") {
		t.Fatalf("expected bash completion script, got %q", out[:min(len(out), 200)])
	}
}

func TestCompletionCmd_Zsh(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"completion", "zsh"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if len(out) == 0 {
		t.Fatal("expected non-empty zsh completion script")
	}
}

func TestCompletionCmd_Fish(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"completion", "fish"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if len(out) == 0 {
		t.Fatal("expected non-empty fish completion script")
	}
}

func TestCompletionCmd_InvalidShell(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"completion", "nushell"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid shell")
	}
}

func TestCompletionCmd_NoArgs(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"completion"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing shell argument")
	}
}
