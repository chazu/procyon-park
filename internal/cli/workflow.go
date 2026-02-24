// workflow.go implements the 'pp workflow' CLI commands.
// Commands dispatch to daemon workflow.* RPC methods.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/chazu/procyon-park/internal/ipc"
	"github.com/chazu/procyon-park/internal/output"
	"github.com/spf13/cobra"
)

func init() {
	workflowCmd := &cobra.Command{
		Use:   "workflow",
		Short: "Manage workflows (run, list, show, cancel, approve, reject, defs)",
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
	workflowCmd.AddCommand(workflowDefsCmd())

	AddCommand(workflowCmd)
}

// ---------- workflow run ----------

func workflowRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <name>",
		Short: "Run a workflow",
		Args:  cobra.ExactArgs(1),
		RunE:  runWorkflowRunCobra,
	}
	f := cmd.Flags()
	f.String("repo-name", "", "repository name (required)")
	f.StringArray("param", nil, "workflow parameter as KEY=VALUE (repeatable)")
	cmd.MarkFlagRequired("repo-name")
	return cmd
}

func runWorkflowRunCobra(cmd *cobra.Command, args []string) error {
	paramFlags, _ := cmd.Flags().GetStringArray("param")
	wfParams := make(map[string]string)
	for _, p := range paramFlags {
		parts := strings.SplitN(p, "=", 2)
		if len(parts) != 2 {
			return NewExitErr(ExitUsage, fmt.Errorf("invalid --param format %q, expected KEY=VALUE", p))
		}
		wfParams[parts[0]] = parts[1]
	}

	params := map[string]interface{}{
		"name":      args[0],
		"repo_name": mustGetString(cmd, "repo-name"),
		"params":    wfParams,
	}

	result, err := ipc.Call(SocketPath(), "workflow.run", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("workflow run: %w", err))
	}

	return formatWorkflowInstance(result)
}

// ---------- workflow list ----------

func workflowListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List workflow instances",
		Args:  cobra.NoArgs,
		RunE:  runWorkflowListCobra,
	}
	f := cmd.Flags()
	f.String("repo-name", "", "filter by repository name")
	f.String("status", "", "filter by status (pending, running, completed, failed, cancelled)")
	return cmd
}

func runWorkflowListCobra(cmd *cobra.Command, args []string) error {
	params := map[string]interface{}{}

	if v, _ := cmd.Flags().GetString("repo-name"); v != "" {
		params["repo_name"] = v
	}
	if v, _ := cmd.Flags().GetString("status"); v != "" {
		params["status"] = v
	}

	result, err := ipc.Call(SocketPath(), "workflow.list", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("workflow list: %w", err))
	}

	return formatWorkflowList(result)
}

// ---------- workflow show ----------

func workflowShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <instance-id>",
		Short: "Show workflow instance details",
		Args:  cobra.ExactArgs(1),
		RunE:  runWorkflowShowCobra,
	}
	f := cmd.Flags()
	f.String("repo-name", "", "repository name")
	return cmd
}

func runWorkflowShowCobra(cmd *cobra.Command, args []string) error {
	params := map[string]interface{}{
		"instance_id": args[0],
	}

	if v, _ := cmd.Flags().GetString("repo-name"); v != "" {
		params["repo_name"] = v
	}

	result, err := ipc.Call(SocketPath(), "workflow.show", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("workflow show: %w", err))
	}

	return formatWorkflowShow(result)
}

// ---------- workflow cancel ----------

func workflowCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <instance-id>",
		Short: "Cancel a running workflow",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]string{
				"instance_id": args[0],
			}

			_, err := ipc.Call(SocketPath(), "workflow.cancel", params)
			if err != nil {
				return NewExitErr(ExitError, fmt.Errorf("workflow cancel: %w", err))
			}

			if !Quiet() {
				fmt.Fprintf(os.Stdout, "Workflow %s cancelled\n", args[0])
			}
			return nil
		},
	}
}

// ---------- workflow approve ----------

func workflowApproveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve <instance-id>",
		Short: "Approve a workflow gate",
		Args:  cobra.ExactArgs(1),
		RunE:  runWorkflowApproveCobra,
	}
	f := cmd.Flags()
	f.Int("step", 0, "gate step index")
	f.String("repo-name", "", "repository name")
	f.String("approver", "", "approver identity")
	return cmd
}

func runWorkflowApproveCobra(cmd *cobra.Command, args []string) error {
	stepIndex, _ := cmd.Flags().GetInt("step")
	params := map[string]interface{}{
		"instance_id": args[0],
		"step_index":  stepIndex,
	}

	if v, _ := cmd.Flags().GetString("repo-name"); v != "" {
		params["repo_name"] = v
	}
	if v, _ := cmd.Flags().GetString("approver"); v != "" {
		params["approver"] = v
	}

	_, err := ipc.Call(SocketPath(), "workflow.approve", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("workflow approve: %w", err))
	}

	if !Quiet() {
		fmt.Fprintf(os.Stdout, "Gate approved for %s step %d\n", args[0], stepIndex)
	}
	return nil
}

// ---------- workflow reject ----------

func workflowRejectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reject <instance-id>",
		Short: "Reject a workflow gate",
		Args:  cobra.ExactArgs(1),
		RunE:  runWorkflowRejectCobra,
	}
	f := cmd.Flags()
	f.Int("step", 0, "gate step index")
	f.String("repo-name", "", "repository name")
	f.String("reason", "", "rejection reason")
	f.String("approver", "", "approver identity")
	return cmd
}

func runWorkflowRejectCobra(cmd *cobra.Command, args []string) error {
	stepIndex, _ := cmd.Flags().GetInt("step")
	params := map[string]interface{}{
		"instance_id": args[0],
		"step_index":  stepIndex,
	}

	if v, _ := cmd.Flags().GetString("repo-name"); v != "" {
		params["repo_name"] = v
	}
	if v, _ := cmd.Flags().GetString("reason"); v != "" {
		params["reason"] = v
	}
	if v, _ := cmd.Flags().GetString("approver"); v != "" {
		params["approver"] = v
	}

	_, err := ipc.Call(SocketPath(), "workflow.reject", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("workflow reject: %w", err))
	}

	if !Quiet() {
		fmt.Fprintf(os.Stdout, "Gate rejected for %s step %d\n", args[0], stepIndex)
	}
	return nil
}

// ---------- workflow defs ----------

func workflowDefsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "defs",
		Short: "List available workflow definitions",
		Args:  cobra.NoArgs,
		RunE:  runWorkflowDefsCobra,
	}
	f := cmd.Flags()
	f.String("repo-root", "", "repository root path")
	return cmd
}

func runWorkflowDefsCobra(cmd *cobra.Command, args []string) error {
	params := map[string]interface{}{}

	if v, _ := cmd.Flags().GetString("repo-root"); v != "" {
		params["repo_root"] = v
	}

	result, err := ipc.Call(SocketPath(), "workflow.defs", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("workflow defs: %w", err))
	}

	return formatWorkflowDefs(result)
}

// ---------- output formatters ----------

func formatWorkflowInstance(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	var inst struct {
		InstanceID   string `json:"instance_id"`
		WorkflowName string `json:"workflow_name"`
		RepoName     string `json:"repo_name"`
		Status       string `json:"status"`
		CurrentStep  int    `json:"current_step"`
		StartedAt    string `json:"started_at"`
	}
	if err := json.Unmarshal(result, &inst); err != nil {
		fmt.Println(string(result))
		return nil
	}

	rec := output.NewRecord()
	rec.Set("ID", inst.InstanceID)
	rec.Set("Workflow", inst.WorkflowName)
	rec.Set("Repo", inst.RepoName)
	rec.Set("Status", inst.Status)
	rec.Set("Started", inst.StartedAt)

	fmtr := output.NewFormatter(f)
	return fmtr.Format(os.Stdout, []*output.Record{rec})
}

func formatWorkflowList(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	var instances []struct {
		InstanceID   string `json:"instance_id"`
		WorkflowName string `json:"workflow_name"`
		RepoName     string `json:"repo_name"`
		Status       string `json:"status"`
		CurrentStep  int    `json:"current_step"`
		StartedAt    string `json:"started_at"`
		CompletedAt  string `json:"completed_at,omitempty"`
		Error        string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(result, &instances); err != nil {
		fmt.Println(string(result))
		return nil
	}

	if len(instances) == 0 && !Quiet() {
		fmt.Println("No workflow instances found")
		return nil
	}

	records := make([]*output.Record, 0, len(instances))
	for _, inst := range instances {
		rec := output.NewRecord()
		rec.Set("ID", inst.InstanceID)
		rec.Set("Workflow", inst.WorkflowName)
		rec.Set("Repo", inst.RepoName)
		rec.Set("Status", inst.Status)
		rec.Set("Step", inst.CurrentStep)
		rec.Set("Started", inst.StartedAt)
		records = append(records, rec)
	}

	fmtr := output.NewFormatter(f)
	return fmtr.Format(os.Stdout, records)
}

func formatWorkflowShow(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	// For JSON output, pass through directly.
	if f == output.FormatJSON || f == output.FormatJSONPretty {
		fmtr := output.NewFormatter(f)
		// Wrap in a single record with the raw data.
		rec := output.NewRecord()
		var data map[string]interface{}
		json.Unmarshal(result, &data)
		for k, v := range data {
			rec.Set(k, v)
		}
		return fmtr.Format(os.Stdout, []*output.Record{rec})
	}

	// For text/table output, format as key-value pairs.
	var inst struct {
		InstanceID   string                 `json:"instance_id"`
		WorkflowName string                 `json:"workflow_name"`
		RepoName     string                 `json:"repo_name"`
		Status       string                 `json:"status"`
		CurrentStep  int                    `json:"current_step"`
		StartedAt    string                 `json:"started_at"`
		CompletedAt  string                 `json:"completed_at,omitempty"`
		Error        string                 `json:"error,omitempty"`
		IsRunning    bool                   `json:"is_running"`
		Params       map[string]string      `json:"params,omitempty"`
		Context      map[string]interface{} `json:"context,omitempty"`
		StepResults  []struct {
			StepIndex int    `json:"stepIndex"`
			StepType  string `json:"stepType"`
			Status    string `json:"status"`
			Error     string `json:"error,omitempty"`
		} `json:"step_results,omitempty"`
	}
	if err := json.Unmarshal(result, &inst); err != nil {
		fmt.Println(string(result))
		return nil
	}

	// Instance summary record.
	rec := output.NewRecord()
	rec.Set("ID", inst.InstanceID)
	rec.Set("Workflow", inst.WorkflowName)
	rec.Set("Repo", inst.RepoName)
	rec.Set("Status", inst.Status)
	rec.Set("Step", inst.CurrentStep)
	rec.Set("Running", inst.IsRunning)
	rec.Set("Started", inst.StartedAt)
	if inst.CompletedAt != "" {
		rec.Set("Completed", inst.CompletedAt)
	}
	if inst.Error != "" {
		rec.Set("Error", inst.Error)
	}

	fmtr := output.NewFormatter(f)
	if err := fmtr.Format(os.Stdout, []*output.Record{rec}); err != nil {
		return err
	}

	// Print step results if any.
	if len(inst.StepResults) > 0 {
		fmt.Fprintf(os.Stdout, "\nStep Results:\n")
		stepRecords := make([]*output.Record, 0, len(inst.StepResults))
		for _, sr := range inst.StepResults {
			srec := output.NewRecord()
			srec.Set("Step", sr.StepIndex)
			srec.Set("Type", sr.StepType)
			srec.Set("Status", sr.Status)
			if sr.Error != "" {
				srec.Set("Error", sr.Error)
			}
			stepRecords = append(stepRecords, srec)
		}
		if err := fmtr.Format(os.Stdout, stepRecords); err != nil {
			return err
		}
	}

	return nil
}

func formatWorkflowDefs(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	var defs []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Source      string `json:"source"`
		StepCount   int    `json:"stepCount"`
	}
	if err := json.Unmarshal(result, &defs); err != nil {
		fmt.Println(string(result))
		return nil
	}

	if len(defs) == 0 && !Quiet() {
		fmt.Println("No workflow definitions found")
		return nil
	}

	records := make([]*output.Record, 0, len(defs))
	for _, d := range defs {
		rec := output.NewRecord()
		rec.Set("Name", d.Name)
		rec.Set("Description", d.Description)
		rec.Set("Source", d.Source)
		rec.Set("Steps", d.StepCount)
		records = append(records, rec)
	}

	fmtr := output.NewFormatter(f)
	return fmtr.Format(os.Stdout, records)
}
