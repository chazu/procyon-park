package cli

import (
	"fmt"

	"github.com/chazu/procyon-park/internal/ipc"
	"github.com/spf13/cobra"
)

// listCmd is a top-level alias for 'pp agent list' (imp-castle compatibility).
var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List agents (alias for 'pp agent list')",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := EnsureDaemon(); err != nil {
			return NewExitErr(ExitConnection, fmt.Errorf("daemon not reachable: %w", err))
		}

		result, err := ipc.Call(SocketPath(), "agent.list", nil)
		if err != nil {
			return fmt.Errorf("agent.list failed: %w", err)
		}

		w := cmd.OutOrStdout()
		if OutputJSON() {
			fmt.Fprintln(w, string(result))
			return nil
		}

		printAgentList(w, result)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
