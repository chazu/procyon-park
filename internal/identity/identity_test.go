package identity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerate_CreatesFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identity")

	info, created, err := Generate(dir)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !created {
		t.Fatal("expected created=true for fresh directory")
	}
	if info.NodeID == "" {
		t.Fatal("expected non-empty node ID")
	}
	if len(info.PublicKey) != 32 {
		t.Fatalf("expected 32-byte public key, got %d", len(info.PublicKey))
	}

	// Verify node.json exists and parses.
	data, err := os.ReadFile(filepath.Join(dir, "node.json"))
	if err != nil {
		t.Fatalf("read node.json: %v", err)
	}
	var loaded NodeInfo
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("parse node.json: %v", err)
	}
	if loaded.NodeID != info.NodeID {
		t.Fatalf("node ID mismatch: %q vs %q", loaded.NodeID, info.NodeID)
	}

	// Verify node.key exists with restrictive permissions.
	keyInfo, err := os.Stat(filepath.Join(dir, "node.key"))
	if err != nil {
		t.Fatalf("stat node.key: %v", err)
	}
	if perm := keyInfo.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected 0600 permissions on node.key, got %o", perm)
	}
}

func TestGenerate_Idempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identity")

	info1, created1, err := Generate(dir)
	if err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	if !created1 {
		t.Fatal("first call should create")
	}

	info2, created2, err := Generate(dir)
	if err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	if created2 {
		t.Fatal("second call should not create")
	}
	if info2.NodeID != info1.NodeID {
		t.Fatalf("node ID changed on second call: %q vs %q", info2.NodeID, info1.NodeID)
	}
}

func TestGenerate_DeterministicNodeID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identity")

	info, _, err := Generate(dir)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Load and verify same ID.
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.NodeID != info.NodeID {
		t.Fatalf("loaded node ID differs: %q vs %q", loaded.NodeID, info.NodeID)
	}
}

func TestLoad_MissingDir(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent"))
	if err == nil {
		t.Fatal("expected error for missing directory")
	}
}
