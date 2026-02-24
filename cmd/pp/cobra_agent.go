package main

import (
	"fmt"

	"github.com/chazu/procyon-park/internal/cli"
	"github.com/spf13/cobra"
)

func init() {
	cli.AddCommand(agentCmd)
}

// agentCmd wraps the legacy handleAgent dispatcher as a Cobra command.
// procyon-park-rdi will convert individual subcommands to native Cobra.
var agentCmd = &cobra.Command{
	Use:                "agent",
	Short:              "Manage agents (spawn, dismiss, status, list, prune)",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		code := handleAgent(args)
		if code != 0 {
			return cli.NewExitErr(code, fmt.Errorf("agent command failed"))
		}
		return nil
	},
}
