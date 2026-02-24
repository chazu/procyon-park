package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/chazu/procyon-park/internal/ipc"
	"github.com/chazu/procyon-park/internal/output"
	"github.com/spf13/cobra"
)

// AgentCmd is the top-level agent command.
var AgentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage agents (spawn, dismiss, status, list, prune, respawn, show, attach, logs, stuck)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.Help()
		return NewExitErr(ExitUsage, fmt.Errorf("missing agent subcommand"))
	},
}

// ---------- agent spawn ----------

var agentSpawnCmd = &cobra.Command{
	Use:   "spawn",
	Short: "Spawn a new agent in a repo",
	RunE:  runAgentSpawnCobra,
}

func init() {
	f := agentSpawnCmd.Flags()
	f.String("role", "", "agent role: cub or king (required)")
	f.String("task-id", "", "beads issue ID (required)")
	f.String("base-branch", "", "git branch to base on (required)")
	f.String("repo-name", "", "repository name (required)")
	f.String("repo-root", "", "absolute path to repo (required)")
	f.String("epic-id", "", "epic ID for feature branch context")
	f.String("agent-cmd", "", "command to launch in agent session")
	f.String("prime-text", "", "priming instruction to send")
	f.String("worktree-base", "", "worktree base directory")
	f.Int("prompt-wait-ms", 0, "wait time after launch in milliseconds")
	agentSpawnCmd.MarkFlagRequired("role")
	agentSpawnCmd.MarkFlagRequired("task-id")
	agentSpawnCmd.MarkFlagRequired("base-branch")
	agentSpawnCmd.MarkFlagRequired("repo-name")
	agentSpawnCmd.MarkFlagRequired("repo-root")
}

func runAgentSpawnCobra(cmd *cobra.Command, args []string) error {
	params := map[string]interface{}{
		"role":        mustGetString(cmd, "role"),
		"task_id":     mustGetString(cmd, "task-id"),
		"base_branch": mustGetString(cmd, "base-branch"),
		"repo_name":   mustGetString(cmd, "repo-name"),
		"repo_root":   mustGetString(cmd, "repo-root"),
	}

	if v, _ := cmd.Flags().GetString("worktree-base"); v != "" {
		params["worktree_base"] = v
	}
	if v, _ := cmd.Flags().GetString("epic-id"); v != "" {
		params["epic_id"] = v
	}
	if v, _ := cmd.Flags().GetString("agent-cmd"); v != "" {
		params["agent_cmd"] = v
	}
	if v, _ := cmd.Flags().GetString("prime-text"); v != "" {
		params["prime_text"] = v
	}
	if v, _ := cmd.Flags().GetInt("prompt-wait-ms"); v > 0 {
		params["prompt_wait_ms"] = v
	}

	result, err := ipc.Call(SocketPath(), "agent.spawn", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("spawn: %w", err))
	}

	return formatSpawnResult(result)
}

func formatSpawnResult(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	var sr struct {
		AgentName   string `json:"agent_name"`
		Branch      string `json:"branch"`
		Worktree    string `json:"worktree"`
		TmuxSession string `json:"tmux_session"`
		RepoName    string `json:"repo_name"`
		TaskID      string `json:"task_id"`
		Role        string `json:"role"`
	}
	if err := json.Unmarshal(result, &sr); err != nil {
		fmt.Println(string(result))
		return nil
	}

	rec := output.NewRecord()
	rec.Set("Name", sr.AgentName)
	rec.Set("Branch", sr.Branch)
	rec.Set("Worktree", sr.Worktree)
	rec.Set("Session", sr.TmuxSession)
	rec.Set("Task", sr.TaskID)
	rec.Set("Role", sr.Role)

	fmtr := output.NewFormatter(f)
	return fmtr.Format(os.Stdout, []*output.Record{rec})
}

// ---------- agent dismiss ----------

var agentDismissCmd = &cobra.Command{
	Use:   "dismiss",
	Short: "Dismiss (tear down) an agent",
	RunE:  runAgentDismissCobra,
}

func init() {
	f := agentDismissCmd.Flags()
	f.String("agent-name", "", "agent name (required)")
	f.String("repo-name", "", "repository name (required)")
	f.String("repo-root", "", "absolute path to repo (required)")
	agentDismissCmd.MarkFlagRequired("agent-name")
	agentDismissCmd.MarkFlagRequired("repo-name")
	agentDismissCmd.MarkFlagRequired("repo-root")
}

func runAgentDismissCobra(cmd *cobra.Command, args []string) error {
	params := map[string]string{
		"agent_name": mustGetString(cmd, "agent-name"),
		"repo_name":  mustGetString(cmd, "repo-name"),
		"repo_root":  mustGetString(cmd, "repo-root"),
	}

	_, err := ipc.Call(SocketPath(), "agent.dismiss", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("dismiss: %w", err))
	}

	if !Quiet() {
		fmt.Fprintf(os.Stdout, "Agent %q dismissed\n", params["agent_name"])
	}
	return nil
}

// ---------- agent status ----------

var agentStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check agent status with liveness detection",
	RunE:  runAgentStatusCobra,
}

func init() {
	f := agentStatusCmd.Flags()
	f.String("agent-name", "", "agent name (required)")
	f.String("repo-name", "", "repository name (required)")
	agentStatusCmd.MarkFlagRequired("agent-name")
	agentStatusCmd.MarkFlagRequired("repo-name")
}

func runAgentStatusCobra(cmd *cobra.Command, args []string) error {
	params := map[string]string{
		"agent_name": mustGetString(cmd, "agent-name"),
		"repo_name":  mustGetString(cmd, "repo-name"),
	}

	result, err := ipc.Call(SocketPath(), "agent.status", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("status: %w", err))
	}

	return formatStatusResult(result, params["agent_name"])
}

func formatStatusResult(result json.RawMessage, agentName string) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	var status struct {
		StoredStatus  string `json:"stored_status"`
		ActualStatus  string `json:"actual_status"`
		SessionExists bool   `json:"session_exists"`
	}
	if err := json.Unmarshal(result, &status); err != nil {
		fmt.Println(string(result))
		return nil
	}

	rec := output.NewRecord()
	rec.Set("Name", agentName)
	rec.Set("Stored Status", status.StoredStatus)
	rec.Set("Actual Status", status.ActualStatus)
	rec.Set("Session", strconv.FormatBool(status.SessionExists))

	fmtr := output.NewFormatter(f)
	return fmtr.Format(os.Stdout, []*output.Record{rec})
}

// ---------- agent list ----------

var agentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List agents for a repo",
	RunE:  runAgentListCobra,
}

func init() {
	f := agentListCmd.Flags()
	f.String("repo-name", "", "repository name (required)")
	agentListCmd.MarkFlagRequired("repo-name")
}

func runAgentListCobra(cmd *cobra.Command, args []string) error {
	params := map[string]string{
		"repo_name": mustGetString(cmd, "repo-name"),
	}

	result, err := ipc.Call(SocketPath(), "agent.list", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("list: %w", err))
	}

	return formatListResult(result)
}

func formatListResult(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	var agents []struct {
		Name    string `json:"name"`
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(result, &agents); err != nil {
		fmt.Println(string(result))
		return nil
	}

	if len(agents) == 0 {
		if !Quiet() {
			fmt.Fprintln(os.Stdout, "No agents registered")
		}
		return nil
	}

	records := make([]*output.Record, 0, len(agents))
	for _, a := range agents {
		rec := output.NewRecord()
		rec.Set("Name", a.Name)

		var info struct {
			Role   string `json:"role"`
			Status string `json:"status"`
			Task   string `json:"task"`
			Branch string `json:"branch"`
		}
		if err := json.Unmarshal([]byte(a.Payload), &info); err == nil {
			rec.Set("Role", info.Role)
			rec.Set("Status", info.Status)
			rec.Set("Task", info.Task)
			rec.Set("Branch", info.Branch)
		} else {
			rec.Set("Payload", a.Payload)
		}
		records = append(records, rec)
	}

	fmtr := output.NewFormatter(f)
	return fmtr.Format(os.Stdout, records)
}

// ---------- agent prune ----------

var agentPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Clean up dead agents and orphaned resources",
	RunE:  runAgentPruneCobra,
}

func init() {
	f := agentPruneCmd.Flags()
	f.String("repo-name", "", "repository name (required)")
	f.String("repo-root", "", "absolute path to repo (required)")
	f.String("worktree-base", "", "worktree base directory (required)")
	f.Duration("branch-age", 24*time.Hour, "minimum branch age to delete")
	agentPruneCmd.MarkFlagRequired("repo-name")
	agentPruneCmd.MarkFlagRequired("repo-root")
	agentPruneCmd.MarkFlagRequired("worktree-base")
}

func runAgentPruneCobra(cmd *cobra.Command, args []string) error {
	branchAge, _ := cmd.Flags().GetDuration("branch-age")

	params := map[string]interface{}{
		"repo_name":     mustGetString(cmd, "repo-name"),
		"repo_root":     mustGetString(cmd, "repo-root"),
		"worktree_base": mustGetString(cmd, "worktree-base"),
		"branch_age_ms": int(branchAge.Milliseconds()),
	}

	result, err := ipc.Call(SocketPath(), "agent.prune", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("prune: %w", err))
	}

	return formatPruneResult(result)
}

func formatPruneResult(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	var pr struct {
		DeadAgents        []string `json:"DeadAgents"`
		OrphanedWorktrees []string `json:"OrphanedWorktrees"`
		PreservedBranches []string `json:"PreservedBranches"`
		DeletedBranches   []string `json:"DeletedBranches"`
		Errors            []string `json:"Errors"`
	}
	if err := json.Unmarshal(result, &pr); err != nil {
		fmt.Println(string(result))
		return nil
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		fmtr := output.NewFormatter(f)
		rec := output.NewRecord()
		rec.Set("Dead Agents", strings.Join(pr.DeadAgents, ", "))
		rec.Set("Orphaned Worktrees", strconv.Itoa(len(pr.OrphanedWorktrees)))
		rec.Set("Deleted Branches", strings.Join(pr.DeletedBranches, ", "))
		rec.Set("Preserved Branches", strings.Join(pr.PreservedBranches, ", "))
		if len(pr.Errors) > 0 {
			rec.Set("Errors", strings.Join(pr.Errors, "; "))
		}
		return fmtr.Format(os.Stdout, []*output.Record{rec})
	}

	// Text/table output.
	if len(pr.DeadAgents) > 0 {
		fmt.Fprintf(os.Stdout, "Dead agents cleaned up: %s\n", strings.Join(pr.DeadAgents, ", "))
	}
	if len(pr.OrphanedWorktrees) > 0 {
		fmt.Fprintf(os.Stdout, "Orphaned worktrees removed: %d\n", len(pr.OrphanedWorktrees))
	}
	if len(pr.DeletedBranches) > 0 {
		fmt.Fprintf(os.Stdout, "Deleted branches: %s\n", strings.Join(pr.DeletedBranches, ", "))
	}
	if len(pr.PreservedBranches) > 0 {
		fmt.Fprintf(os.Stdout, "Preserved branches (too recent): %s\n", strings.Join(pr.PreservedBranches, ", "))
	}
	if len(pr.Errors) > 0 {
		fmt.Fprintf(os.Stdout, "Non-fatal errors: %s\n", strings.Join(pr.Errors, "; "))
	}
	if len(pr.DeadAgents) == 0 && len(pr.OrphanedWorktrees) == 0 && len(pr.DeletedBranches) == 0 {
		fmt.Fprintln(os.Stdout, "Nothing to prune")
	}
	return nil
}

// ---------- agent respawn ----------

var agentRespawnCmd = &cobra.Command{
	Use:   "respawn",
	Short: "Respawn an agent (preserve work, new session)",
	RunE:  runAgentRespawnCobra,
}

func init() {
	f := agentRespawnCmd.Flags()
	f.String("agent-name", "", "agent name (required)")
	f.String("repo-name", "", "repository name (required)")
	f.String("repo-root", "", "absolute path to repo (required)")
	f.String("worktree-base", "", "worktree base directory")
	f.String("agent-cmd", "", "command to launch in agent session")
	f.String("prime-text", "", "priming instruction to send")
	agentRespawnCmd.MarkFlagRequired("agent-name")
	agentRespawnCmd.MarkFlagRequired("repo-name")
	agentRespawnCmd.MarkFlagRequired("repo-root")
}

func runAgentRespawnCobra(cmd *cobra.Command, args []string) error {
	params := map[string]interface{}{
		"agent_name": mustGetString(cmd, "agent-name"),
		"repo_name":  mustGetString(cmd, "repo-name"),
		"repo_root":  mustGetString(cmd, "repo-root"),
	}

	if v, _ := cmd.Flags().GetString("worktree-base"); v != "" {
		params["worktree_base"] = v
	}
	if v, _ := cmd.Flags().GetString("agent-cmd"); v != "" {
		params["agent_cmd"] = v
	}
	if v, _ := cmd.Flags().GetString("prime-text"); v != "" {
		params["prime_text"] = v
	}

	result, err := ipc.Call(SocketPath(), "agent.respawn", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("respawn: %w", err))
	}

	return formatSpawnResult(result)
}

// ---------- agent show ----------

var agentShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show detailed agent information",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentShowCobra,
}

func init() {
	f := agentShowCmd.Flags()
	f.String("repo-name", "", "repository name (required)")
	agentShowCmd.MarkFlagRequired("repo-name")
}

func runAgentShowCobra(cmd *cobra.Command, args []string) error {
	agentName := args[0]
	repoName := mustGetString(cmd, "repo-name")

	params := map[string]string{
		"agent_name": agentName,
		"repo_name":  repoName,
	}

	result, err := ipc.Call(SocketPath(), "agent.show", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("show: %w", err))
	}

	return formatShowResult(result)
}

func formatShowResult(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	var info struct {
		Name          string `json:"name"`
		Role          string `json:"role"`
		Status        string `json:"status"`
		ActualStatus  string `json:"actual_status"`
		SessionExists bool   `json:"session_exists"`
		TmuxSession   string `json:"tmux_session"`
		Worktree      string `json:"worktree"`
		Branch        string `json:"branch"`
		Task          string `json:"task"`
		EpicID        string `json:"epic_id"`
	}
	if err := json.Unmarshal(result, &info); err != nil {
		fmt.Println(string(result))
		return nil
	}

	rec := output.NewRecord()
	rec.Set("Name", info.Name)
	rec.Set("Role", info.Role)
	rec.Set("Status", info.ActualStatus)
	rec.Set("Task", info.Task)
	rec.Set("Branch", info.Branch)
	rec.Set("Worktree", info.Worktree)
	rec.Set("Session", info.TmuxSession)
	rec.Set("Session Alive", strconv.FormatBool(info.SessionExists))
	if info.EpicID != "" {
		rec.Set("Epic", info.EpicID)
	}

	fmtr := output.NewFormatter(f)
	return fmtr.Format(os.Stdout, []*output.Record{rec})
}

// ---------- agent attach ----------

var agentAttachCmd = &cobra.Command{
	Use:   "attach <name>",
	Short: "Attach to an agent's tmux session",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentAttachCobra,
}

func init() {
	f := agentAttachCmd.Flags()
	f.String("repo-name", "", "repository name (required)")
	agentAttachCmd.MarkFlagRequired("repo-name")
}

func runAgentAttachCobra(cmd *cobra.Command, args []string) error {
	agentName := args[0]
	repoName := mustGetString(cmd, "repo-name")
	sessionName := fmt.Sprintf("pp-%s-%s", repoName, agentName)

	tmuxCmd := exec.Command("tmux", "attach-session", "-t", sessionName)
	tmuxCmd.Stdin = os.Stdin
	tmuxCmd.Stdout = os.Stdout
	tmuxCmd.Stderr = os.Stderr

	if err := tmuxCmd.Run(); err != nil {
		return NewExitErr(ExitError, fmt.Errorf("attach to session %s: %w", sessionName, err))
	}
	return nil
}

// ---------- agent logs ----------

var agentLogsCmd = &cobra.Command{
	Use:   "logs <name>",
	Short: "Capture agent's tmux pane output",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentLogsCobra,
}

func init() {
	f := agentLogsCmd.Flags()
	f.String("repo-name", "", "repository name (required)")
	f.Int("lines", 100, "number of lines to capture")
	agentLogsCmd.MarkFlagRequired("repo-name")
}

func runAgentLogsCobra(cmd *cobra.Command, args []string) error {
	agentName := args[0]
	repoName := mustGetString(cmd, "repo-name")
	lines, _ := cmd.Flags().GetInt("lines")
	sessionName := fmt.Sprintf("pp-%s-%s", repoName, agentName)

	tmuxCmd := exec.Command("tmux", "capture-pane", "-t", sessionName, "-p", "-S", fmt.Sprintf("-%d", lines))
	tmuxCmd.Stderr = os.Stderr
	out, err := tmuxCmd.Output()
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("capture pane for session %s: %w", sessionName, err))
	}

	os.Stdout.Write(out)
	return nil
}

// ---------- agent stuck ----------

var agentStuckCmd = &cobra.Command{
	Use:   "stuck <name>",
	Short: "Mark an agent as stuck",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentStuckCobra,
}

func init() {
	f := agentStuckCmd.Flags()
	f.String("repo-name", "", "repository name (required)")
	agentStuckCmd.MarkFlagRequired("repo-name")
}

func runAgentStuckCobra(cmd *cobra.Command, args []string) error {
	agentName := args[0]
	repoName := mustGetString(cmd, "repo-name")

	params := map[string]string{
		"agent_name": agentName,
		"repo_name":  repoName,
	}

	_, err := ipc.Call(SocketPath(), "agent.stuck", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("stuck: %w", err))
	}

	if !Quiet() {
		fmt.Fprintf(os.Stdout, "Agent %q marked as stuck\n", agentName)
	}
	return nil
}

// ---------- register all ----------

func init() {
	AgentCmd.AddCommand(
		agentSpawnCmd,
		agentDismissCmd,
		agentStatusCmd,
		agentListCmd,
		agentPruneCmd,
		agentRespawnCmd,
		agentShowCmd,
		agentAttachCmd,
		agentLogsCmd,
		agentStuckCmd,
	)
}

// mustGetString returns a flag value, panicking on error (safe because
// MarkFlagRequired ensures the flag is set before RunE executes).
func mustGetString(cmd *cobra.Command, name string) string {
	v, err := cmd.Flags().GetString(name)
	if err != nil {
		panic(fmt.Sprintf("flag %q: %v", name, err))
	}
	return v
}
