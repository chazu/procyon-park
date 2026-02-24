package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/chazu/procyon-park/internal/ipc"
	"github.com/spf13/cobra"
)

// primeCmd outputs role-specific instructions for the calling agent.
// It reads PP_AGENT_ROLE from the environment to determine which
// instruction set to output.
var primeCmd = &cobra.Command{
	Use:   "prime",
	Short: "Output role-specific agent instructions",
	Long: `Outputs priming instructions for the current agent based on the
PP_AGENT_ROLE environment variable. Agents call this at startup to
receive their role-specific instructions and orientation context.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		role := os.Getenv("PP_AGENT_ROLE")
		if role == "" {
			role = "cub" // default role
		}

		if err := EnsureDaemon(); err != nil {
			return NewExitErr(ExitConnection, fmt.Errorf("daemon not reachable: %w", err))
		}

		params := map[string]string{"role": role}
		result, err := ipc.Call(SocketPath(), "system.prime", params)
		if err != nil {
			return fmt.Errorf("prime failed: %w", err)
		}

		w := cmd.OutOrStdout()
		if OutputJSON() {
			fmt.Fprintln(w, string(result))
		} else {
			// Expect the result to be a JSON string containing the instructions.
			var instructions string
			if err := json.Unmarshal(result, &instructions); err != nil {
				// If it's not a string, print raw.
				fmt.Fprintln(w, string(result))
				return nil
			}
			fmt.Fprintln(w, instructions)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(primeCmd)
}
