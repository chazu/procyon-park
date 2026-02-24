// workflow.go implements stub commands for 'pp workflow' that return
// "not yet implemented" with proper exit codes. These will be filled in Phase 7.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// ExitNotImplemented is returned by stub commands that are not yet implemented.
const ExitNotImplemented = 10

func init() {
	workflowCmd := &cobra.Command{
		Use:   "workflow",
		Short: "Manage workflows (not yet implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	workflowCmd.AddCommand(workflowRunCmd())
	workflowCmd.AddCommand(workflowListCmd())
	workflowCmd.AddCommand(workflowShowCmd())
	workflowCmd.AddCommand(workflowCancelCmd())
	workflowCmd.AddCommand(workflowApproveCmd())
	workflowCmd.AddCommand(workflowRejectCmd())

	AddCommand(workflowCmd)
}

func notImplementedErr(command string) error {
	return NewExitErr(ExitNotImplemented, fmt.Errorf("pp workflow %s: not yet implemented", command))
}

func workflowRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <name>",
		Short: "Run a workflow (not yet implemented)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplementedErr("run")
		},
	}
}

func workflowListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List workflows (not yet implemented)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplementedErr("list")
		},
	}
}

func workflowShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show workflow details (not yet implemented)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplementedErr("show")
		},
	}
}

func workflowCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel a running workflow (not yet implemented)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplementedErr("cancel")
		},
	}
}

func workflowApproveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "approve <id>",
		Short: "Approve a workflow gate (not yet implemented)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplementedErr("approve")
		},
	}
}

func workflowRejectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reject <id>",
		Short: "Reject a workflow gate (not yet implemented)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplementedErr("reject")
		},
	}
}
