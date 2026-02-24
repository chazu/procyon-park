package main

import (
	"github.com/chazu/procyon-park/internal/cli"
	"github.com/spf13/cobra"
)

func init() {
	cli.AddCommand(versionCmd)
}

// versionCmd prints the version string.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version and exit",
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Println("pp (procyon-park) " + cli.Version)
	},
}

// run provides backward-compatible dispatch used by existing tests.
// It maps the old arg-based routing to Cobra command execution.
func run(args []string) int {
	return cli.ExecuteArgs(args)
}
