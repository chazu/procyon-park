package cli

import (
	"fmt"
	"os"
	"path/filepath"

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

		fmt.Fprintln(w, "Initialization complete.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}

const defaultConfig = `# Procyon Park configuration
# See: pp help config

# [daemon]
# socket = "~/.procyon-park/daemon.sock"

# [agent]
# default_role = "cub"
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
