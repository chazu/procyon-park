package main

import (
	"fmt"

	"github.com/chazu/procyon-park/internal/cli"
	"github.com/spf13/cobra"
)

func init() {
	cli.AddCommand(daemonCmd)
}

// daemonCmd wraps the legacy handleDaemon dispatcher as a Cobra command.
// procyon-park-rdi will convert individual subcommands to native Cobra.
var daemonCmd = &cobra.Command{
	Use:                "daemon",
	Short:              "Manage the background daemon",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		code := handleDaemon(args)
		if code != 0 {
			return cli.NewExitErr(code, fmt.Errorf("daemon command failed"))
		}
		return nil
	},
}
