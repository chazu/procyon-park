package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// completionCmd generates shell completion scripts.
var completionCmd = &cobra.Command{
	Use:       "completion [bash|zsh|fish|powershell]",
	Short:     "Generate shell completion script",
	Long:      `Generate a shell completion script for pp. Load the output in your shell to enable tab completion.`,
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
	RunE: func(cmd *cobra.Command, args []string) error {
		w := cmd.OutOrStdout()
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletionV2(w, true)
		case "zsh":
			return rootCmd.GenZshCompletion(w)
		case "fish":
			return rootCmd.GenFishCompletion(w, true)
		case "powershell":
			return rootCmd.GenPowerShellCompletionWithDesc(w)
		default:
			return NewExitErr(ExitUsage, fmt.Errorf("unsupported shell %q (valid: bash, zsh, fish, powershell)", args[0]))
		}
	},
}

func init() {
	rootCmd.AddCommand(completionCmd)
}
