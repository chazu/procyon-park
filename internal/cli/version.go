package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// versionCmd prints the binary version.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show the pp binary version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if OutputJSON() {
			fmt.Fprintf(cmd.OutOrStdout(), `{"version":%q}`+"\n", Version)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "pp version %s\n", Version)
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
