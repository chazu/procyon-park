package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chazu/procyon-park/internal/identity"
	"github.com/chazu/procyon-park/internal/tuplestore"
	"github.com/spf13/cobra"
)

// initCmd performs first-time setup for procyon-park.
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "First-time setup (create ~/.procyon-park/ and default config)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot determine home directory: %w", err)
		}

		dataDir := filepath.Join(home, ".procyon-park")
		w := cmd.OutOrStdout()

		// Create data directory.
		created, err := ensureDir(dataDir)
		if err != nil {
			return fmt.Errorf("create data dir: %w", err)
		}
		if created {
			fmt.Fprintf(w, "Created %s\n", dataDir)
		} else {
			fmt.Fprintf(w, "Already exists: %s\n", dataDir)
		}

		// Create default config file if it doesn't exist.
		configPath := filepath.Join(dataDir, "config.toml")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			if err := os.WriteFile(configPath, []byte(defaultConfig), 0644); err != nil {
				return fmt.Errorf("write config: %w", err)
			}
			fmt.Fprintf(w, "Created %s\n", configPath)
		} else {
			fmt.Fprintf(w, "Already exists: %s\n", configPath)
		}

		// Generate node identity (Ed25519 keypair + UUID v5 node ID).
		identityDir := filepath.Join(dataDir, "identity")
		info, identityCreated, err := identity.Generate(identityDir)
		if err != nil {
			return fmt.Errorf("generate identity: %w", err)
		}
		if identityCreated {
			fmt.Fprintf(w, "Generated node identity: %s\n", info.NodeID)
		} else {
			fmt.Fprintf(w, "Node identity exists: %s\n", info.NodeID)
		}

		// Initialize BBS tuplespace storage.
		bbsPath := filepath.Join(dataDir, "bbs.db")
		if _, err := os.Stat(bbsPath); os.IsNotExist(err) {
			store, err := tuplestore.NewStore(bbsPath)
			if err != nil {
				return fmt.Errorf("init tuplespace: %w", err)
			}
			store.Close()
			fmt.Fprintf(w, "Created BBS tuplespace: %s\n", bbsPath)
		} else {
			fmt.Fprintf(w, "Already exists: %s\n", bbsPath)
		}

		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Initialization complete.")
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Next steps:")
		fmt.Fprintln(w, "  pp repo add <path>    Register a repository")
		fmt.Fprintln(w, "  pp config list        View configuration")
		fmt.Fprintln(w, "  pp doctor             Verify setup")

		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}

const defaultConfig = `# Procyon Park configuration
# See: pp help config

# [agent]
# command = "claude"        # agent command to run
# max_concurrent = 4        # max parallel agents

# [daemon]
# poll_interval = "5s"      # work-checking interval

# [telemetry]
# enabled = false            # enable OTEL telemetry

# [features]
# bbs_enabled = true         # enable BBS tuplespace
# workflows_enabled = true   # enable workflow engine
`

// ensureDir creates a directory if it doesn't exist. Returns true if created.
func ensureDir(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return false, err
	}
	return true, nil
}
