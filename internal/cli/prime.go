package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/chazu/procyon-park/internal/ipc"
	"github.com/spf13/cobra"
)

// primeCmd outputs role-specific instructions for the calling agent.
// It reads PP_* environment variables and sends them to the daemon's
// system.prime handler to generate role-specific instructions.
var primeCmd = &cobra.Command{
	Use:   "prime",
	Short: "Output role-specific agent instructions",
	Long: `Outputs priming instructions for the current agent based on PP_*
environment variables. Agents call this at startup to receive their
role-specific instructions and orientation context.

Environment variables read:
  PP_AGENT_ROLE  Agent role (default: cub)
  PP_AGENT_NAME  Agent name
  PP_REPO        Repository name
  PP_TASK        Current task ID
  PP_BRANCH      Git branch name
  PP_WORKTREE    Worktree path`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		role := os.Getenv("PP_AGENT_ROLE")
		if role == "" {
			role = "cub" // default role
		}

		if err := EnsureDaemon(); err != nil {
			return NewExitErr(ExitConnection, fmt.Errorf("daemon not reachable: %w", err))
		}

		params := map[string]string{
			"role":       role,
			"agent_name": os.Getenv("PP_AGENT_NAME"),
			"repo":       os.Getenv("PP_REPO"),
			"task_id":    os.Getenv("PP_TASK"),
			"branch":     os.Getenv("PP_BRANCH"),
			"worktree":   os.Getenv("PP_WORKTREE"),
		}
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
