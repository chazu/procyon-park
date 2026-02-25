// Package identity generates and manages node identity for procyon-park.
// Each node has an Ed25519 keypair and a UUID v5 node ID derived from the
// public key.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// procyonParkNamespace is a fixed UUID v5 namespace for deriving node IDs.
var procyonParkNamespace = uuid.MustParse("a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d")

// NodeInfo is the public identity stored in node.json.
type NodeInfo struct {
	NodeID    string `json:"node_id"`
	PublicKey []byte `json:"public_key"`
}

// Generate creates a new Ed25519 keypair and derives a UUID v5 node ID from
// the public key. It writes node.json and node.key into dir. Existing files
// are never overwritten; Generate returns (info, false, nil) if identity
// already exists.
func Generate(dir string) (*NodeInfo, bool, error) {
	jsonPath := filepath.Join(dir, "node.json")
	keyPath := filepath.Join(dir, "node.key")

	// Check for existing identity.
	if info, err := Load(dir); err == nil {
		return info, false, nil
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, false, fmt.Errorf("create identity dir: %w", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, false, fmt.Errorf("generate ed25519 key: %w", err)
	}

	nodeID := uuid.NewSHA1(procyonParkNamespace, pub).String()

	info := &NodeInfo{
		NodeID:    nodeID,
		PublicKey: []byte(pub),
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("marshal node info: %w", err)
	}
	if err := os.WriteFile(jsonPath, append(data, '\n'), 0644); err != nil {
		return nil, false, fmt.Errorf("write node.json: %w", err)
	}

	pemBlock := &pem.Block{
		Type:  "ED25519 PRIVATE KEY",
		Bytes: priv.Seed(),
	}
	pemData := pem.EncodeToMemory(pemBlock)
	if err := os.WriteFile(keyPath, pemData, 0600); err != nil {
		// Clean up the json file on partial failure.
		os.Remove(jsonPath)
		return nil, false, fmt.Errorf("write node.key: %w", err)
	}

	return info, true, nil
}

// Load reads an existing node identity from dir/node.json.
func Load(dir string) (*NodeInfo, error) {
	data, err := os.ReadFile(filepath.Join(dir, "node.json"))
	if err != nil {
		return nil, err
	}
	var info NodeInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("parse node.json: %w", err)
	}
	return &info, nil
}
