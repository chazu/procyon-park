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
}
