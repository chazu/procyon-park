package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCmd_CreatesDataDir(t *testing.T) {
	tmpHome := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", oldHome)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"init"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Created") {
		t.Fatalf("expected 'Created' in output, got %q", out)
	}
	if !strings.Contains(out, "Initialization complete") {
		t.Fatalf("expected completion message, got %q", out)
	}

	dataDir := filepath.Join(tmpHome, ".procyon-park")
	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("data dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected data dir to be a directory")
	}

	configPath := filepath.Join(dataDir, "config.toml")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file not created: %v", err)
	}
}

func TestInitCmd_CreatesIdentity(t *testing.T) {
	tmpHome := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", oldHome)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"init"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Generated node identity") {
		t.Fatalf("expected identity generation message, got %q", out)
	}

	identityDir := filepath.Join(tmpHome, ".procyon-park", "identity")
	if _, err := os.Stat(filepath.Join(identityDir, "node.json")); err != nil {
		t.Fatalf("node.json not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(identityDir, "node.key")); err != nil {
		t.Fatalf("node.key not created: %v", err)
	}
}

func TestInitCmd_CreatesBBS(t *testing.T) {
	tmpHome := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", oldHome)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"init"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Created BBS tuplespace") {
		t.Fatalf("expected BBS creation message, got %q", out)
	}

	bbsPath := filepath.Join(tmpHome, ".procyon-park", "bbs.db")
	if _, err := os.Stat(bbsPath); err != nil {
		t.Fatalf("bbs.db not created: %v", err)
	}
}

func TestInitCmd_PrintsNextSteps(t *testing.T) {
	tmpHome := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", oldHome)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"init"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Next steps:") {
		t.Fatalf("expected next steps in output, got %q", out)
	}
	if !strings.Contains(out, "pp repo add") {
		t.Fatalf("expected 'pp repo add' in next steps, got %q", out)
	}
}

func TestInitCmd_Idempotent(t *testing.T) {
	tmpHome := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", oldHome)

	// Run init twice.
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"init"})
	rootCmd.Execute()

	buf.Reset()
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"init"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("second init should succeed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Already exists") {
		t.Fatalf("expected 'Already exists' on second run, got %q", out)
	}
	if !strings.Contains(out, "Node identity exists") {
		t.Fatalf("expected 'Node identity exists' on second run, got %q", out)
	}
}
