package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/chazu/procyon-park/internal/ipc"
	"github.com/chazu/procyon-park/internal/output"
	"github.com/spf13/cobra"
)

// ---------- analytics ----------

var analyticsCmd = &cobra.Command{
	Use:   "analytics",
	Short: "Query analytics (performance, obstacles, conventions, knowledge, signatures)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.Help()
		return NewExitErr(ExitError, fmt.Errorf("missing analytics subcommand"))
	},
}

// ---------- analytics performance ----------

var analyticsPerformanceCmd = &cobra.Command{
	Use:   "performance",
	Short: "Agent performance stats per scope",
	RunE:  runAnalyticsPerformance,
}

func init() {
	f := analyticsPerformanceCmd.Flags()
	f.String("repo", "", "filter by repository name")
	f.String("scope", "", "filter by scope")
}

func runAnalyticsPerformance(cmd *cobra.Command, args []string) error {
	params := map[string]interface{}{}
	if v, _ := cmd.Flags().GetString("repo"); v != "" {
		params["repo"] = v
	}
	if v, _ := cmd.Flags().GetString("scope"); v != "" {
		params["scope"] = v
	}

	result, err := ipc.Call(SocketPath(), "analytics.performance", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("analytics performance: %w", err))
	}

	return formatAnalyticsPerformance(result)
}

func formatAnalyticsPerformance(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		return writeRawJSON(result)
	}

	var rows []struct {
		Scope                string  `json:"scope"`
		ObstacleCount        int64   `json:"obstacle_count"`
		ArtifactCount        int64   `json:"artifact_count"`
		DistinctAgents       int64   `json:"distinct_agents"`
		ArtifactObstacleRate float64 `json:"artifact_obstacle_rate"`
	}
	if err := json.Unmarshal(result, &rows); err != nil {
		fmt.Println(string(result))
		return nil
	}

	if len(rows) == 0 {
		fmt.Println("No performance data available.")
		return nil
	}

	var records []*output.Record
	for _, r := range rows {
		rec := output.NewRecord()
		rec.Set("Scope", r.Scope)
		rec.Set("Obstacles", r.ObstacleCount)
		rec.Set("Artifacts", r.ArtifactCount)
		rec.Set("Agents", r.DistinctAgents)
		rec.Set("Artifact/Obstacle", fmt.Sprintf("%.2f", r.ArtifactObstacleRate))
		records = append(records, rec)
	}
	return output.NewFormatter(f).Format(os.Stdout, records)
}

// ---------- analytics obstacles ----------

var analyticsObstaclesCmd = &cobra.Command{
	Use:   "obstacles",
	Short: "Obstacle clusters by frequency",
	RunE:  runAnalyticsObstacles,
}

func init() {
	f := analyticsObstaclesCmd.Flags()
	f.String("repo", "", "filter by repository name")
	f.String("scope", "", "filter by scope")
	f.Int("min-count", 2, "minimum occurrences to include")
}

func runAnalyticsObstacles(cmd *cobra.Command, args []string) error {
	params := map[string]interface{}{}
	if v, _ := cmd.Flags().GetString("repo"); v != "" {
		params["repo"] = v
	}
	if v, _ := cmd.Flags().GetString("scope"); v != "" {
		params["scope"] = v
	}
	if v, _ := cmd.Flags().GetInt("min-count"); v > 0 {
		params["min_count"] = v
	}

	result, err := ipc.Call(SocketPath(), "analytics.obstacles", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("analytics obstacles: %w", err))
	}

	return formatAnalyticsObstacles(result)
}

func formatAnalyticsObstacles(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		return writeRawJSON(result)
	}

	var rows []struct {
		Description    string `json:"description"`
		Occurrences    int64  `json:"occurrences"`
		DistinctAgents int64  `json:"distinct_agents"`
		FirstSeen      string `json:"first_seen"`
		LastSeen       string `json:"last_seen"`
	}
	if err := json.Unmarshal(result, &rows); err != nil {
		fmt.Println(string(result))
		return nil
	}

	if len(rows) == 0 {
		fmt.Println("No obstacle clusters found.")
		return nil
	}

	var records []*output.Record
	for _, r := range rows {
		rec := output.NewRecord()
		rec.Set("Description", truncate(r.Description, 60))
		rec.Set("Count", r.Occurrences)
		rec.Set("Agents", r.DistinctAgents)
		rec.Set("First Seen", r.FirstSeen)
		rec.Set("Last Seen", r.LastSeen)
		records = append(records, rec)
	}
	return output.NewFormatter(f).Format(os.Stdout, records)
}

// ---------- analytics conventions ----------

var analyticsConventionsCmd = &cobra.Command{
	Use:   "conventions",
	Short: "Convention effectiveness (before/after success rates)",
	RunE:  runAnalyticsConventions,
}

func init() {
	f := analyticsConventionsCmd.Flags()
	f.String("repo", "", "filter by repository name")
	f.String("scope", "", "filter by scope")
}

func runAnalyticsConventions(cmd *cobra.Command, args []string) error {
	params := map[string]interface{}{}
	if v, _ := cmd.Flags().GetString("repo"); v != "" {
		params["repo"] = v
	}
	if v, _ := cmd.Flags().GetString("scope"); v != "" {
		params["scope"] = v
	}

	result, err := ipc.Call(SocketPath(), "analytics.conventions", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("analytics conventions: %w", err))
	}

	return formatAnalyticsConventions(result)
}

func formatAnalyticsConventions(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		return writeRawJSON(result)
	}

	var rows []struct {
		ConventionID    string  `json:"convention_id"`
		BeforeRate      float64 `json:"before_rate"`
		AfterRate       float64 `json:"after_rate"`
		BeforeTaskCount int64   `json:"before_task_count"`
		AfterTaskCount  int64   `json:"after_task_count"`
	}
	if err := json.Unmarshal(result, &rows); err != nil {
		fmt.Println(string(result))
		return nil
	}

	if len(rows) == 0 {
		fmt.Println("No convention effectiveness data available.")
		return nil
	}

	var records []*output.Record
	for _, r := range rows {
		rec := output.NewRecord()
		rec.Set("Convention", truncate(r.ConventionID, 40))
		rec.Set("Before", fmt.Sprintf("%.2f (%d tasks)", r.BeforeRate, r.BeforeTaskCount))
		rec.Set("After", fmt.Sprintf("%.2f (%d tasks)", r.AfterRate, r.AfterTaskCount))
		delta := r.AfterRate - r.BeforeRate
		sign := "+"
		if delta < 0 {
			sign = ""
		}
		rec.Set("Delta", fmt.Sprintf("%s%.2f", sign, delta))
		records = append(records, rec)
	}
	return output.NewFormatter(f).Format(os.Stdout, records)
}

// ---------- analytics knowledge ----------

var analyticsKnowledgeCmd = &cobra.Command{
	Use:   "knowledge",
	Short: "Cross-agent knowledge flow",
	RunE:  runAnalyticsKnowledge,
}

func init() {
	f := analyticsKnowledgeCmd.Flags()
	f.String("repo", "", "filter by repository name")
	f.String("scope", "", "filter by scope")
}

func runAnalyticsKnowledge(cmd *cobra.Command, args []string) error {
	params := map[string]interface{}{}
	if v, _ := cmd.Flags().GetString("repo"); v != "" {
		params["repo"] = v
	}
	if v, _ := cmd.Flags().GetString("scope"); v != "" {
		params["scope"] = v
	}

	result, err := ipc.Call(SocketPath(), "analytics.knowledge", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("analytics knowledge: %w", err))
	}

	return formatAnalyticsKnowledge(result)
}

func formatAnalyticsKnowledge(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		return writeRawJSON(result)
	}

	var rows []struct {
		SourceAgent string `json:"source_agent"`
		TargetAgent string `json:"target_agent"`
		Category    string `json:"category"`
		Identity    string `json:"identity"`
		SourceTask  string `json:"source_task"`
		TargetTask  string `json:"target_task"`
	}
	if err := json.Unmarshal(result, &rows); err != nil {
		fmt.Println(string(result))
		return nil
	}

	if len(rows) == 0 {
		fmt.Println("No knowledge flow data available.")
		return nil
	}

	var records []*output.Record
	for _, r := range rows {
		rec := output.NewRecord()
		rec.Set("Source", r.SourceAgent)
		rec.Set("Target", r.TargetAgent)
		rec.Set("Type", r.Category)
		rec.Set("Identity", truncate(r.Identity, 50))
		records = append(records, rec)
	}
	return output.NewFormatter(f).Format(os.Stdout, records)
}

// ---------- analytics signatures ----------

var analyticsSignaturesCmd = &cobra.Command{
	Use:   "signatures",
	Short: "Workflow signatures (tuple emission patterns)",
	RunE:  runAnalyticsSignatures,
}

func init() {
	f := analyticsSignaturesCmd.Flags()
	f.String("repo", "", "filter by repository name")
	f.String("scope", "", "filter by scope")
}

func runAnalyticsSignatures(cmd *cobra.Command, args []string) error {
	params := map[string]interface{}{}
	if v, _ := cmd.Flags().GetString("repo"); v != "" {
		params["repo"] = v
	}
	if v, _ := cmd.Flags().GetString("scope"); v != "" {
		params["scope"] = v
	}

	result, err := ipc.Call(SocketPath(), "analytics.signatures", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("analytics signatures: %w", err))
	}

	return formatAnalyticsSignatures(result)
}

func formatAnalyticsSignatures(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		return writeRawJSON(result)
	}

	var rows []struct {
		TaskID  string `json:"task_id"`
		Pattern string `json:"pattern"`
		Outcome string `json:"outcome"`
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(result, &rows); err != nil {
		fmt.Println(string(result))
		return nil
	}

	if len(rows) == 0 {
		fmt.Println("No workflow signature data available.")
		return nil
	}

	var records []*output.Record
	for _, r := range rows {
		rec := output.NewRecord()
		rec.Set("Task", r.TaskID)
		rec.Set("Agent", r.AgentID)
		rec.Set("Outcome", r.Outcome)
		rec.Set("Pattern", truncate(r.Pattern, 60))
		records = append(records, rec)
	}
	return output.NewFormatter(f).Format(os.Stdout, records)
}

// ---------- gc ----------

var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Garbage collection (run, status)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.Help()
		return NewExitErr(ExitError, fmt.Errorf("missing gc subcommand"))
	},
}

// ---------- gc run ----------

var gcRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Trigger a manual GC cycle",
	RunE:  runGCRun,
}

func init() {
	f := gcRunCmd.Flags()
	f.String("repo", "", "repository name (unused, reserved)")
}

func runGCRun(cmd *cobra.Command, args []string) error {
	result, err := ipc.Call(SocketPath(), "gc.run", nil)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("gc run: %w", err))
	}

	return formatGCResult(result)
}

func formatGCResult(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		return writeRawJSON(result)
	}

	var r struct {
		ExpiredEphemeral    int `json:"ExpiredEphemeral"`
		StaleClaims         int `json:"StaleClaims"`
		AbandonedClaims     int `json:"AbandonedClaims"`
		PromotedConventions int `json:"PromotedConventions"`
		ArchivedTasks       int `json:"ArchivedTasks"`
		SynthesizedTuples   int `json:"SynthesizedTuples"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		fmt.Println(string(result))
		return nil
	}

	rec := output.NewRecord()
	rec.Set("Expired Ephemeral", r.ExpiredEphemeral)
	rec.Set("Stale Claims", r.StaleClaims)
	rec.Set("Abandoned Claims", r.AbandonedClaims)
	rec.Set("Promoted Conventions", r.PromotedConventions)
	rec.Set("Archived Tasks", r.ArchivedTasks)
	rec.Set("Synthesized Tuples", r.SynthesizedTuples)

	return output.NewFormatter(f).Format(os.Stdout, []*output.Record{rec})
}

// ---------- gc status ----------

var gcStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show tuplespace stats (what GC would operate on)",
	RunE:  runGCStatus,
}

func runGCStatus(cmd *cobra.Command, args []string) error {
	result, err := ipc.Call(SocketPath(), "gc.status", nil)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("gc status: %w", err))
	}

	return formatGCStatus(result)
}

func formatGCStatus(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		return writeRawJSON(result)
	}

	var r struct {
		TotalTuples int            `json:"total_tuples"`
		Ephemeral   int            `json:"ephemeral"`
		Session     int            `json:"session"`
		Furniture   int            `json:"furniture"`
		Categories  map[string]int `json:"categories"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		fmt.Println(string(result))
		return nil
	}

	rec := output.NewRecord()
	rec.Set("Total", r.TotalTuples)
	rec.Set("Ephemeral", r.Ephemeral)
	rec.Set("Session", r.Session)
	rec.Set("Furniture", r.Furniture)

	records := []*output.Record{rec}

	// Add category breakdown.
	if len(r.Categories) > 0 {
		fmt.Println() // blank line before categories
		var catRecords []*output.Record
		for cat, count := range r.Categories {
			cr := output.NewRecord()
			cr.Set("Category", cat)
			cr.Set("Count", count)
			catRecords = append(catRecords, cr)
		}
		if err := output.NewFormatter(f).Format(os.Stdout, catRecords); err != nil {
			return err
		}
		return nil
	}

	return output.NewFormatter(f).Format(os.Stdout, records)
}

// ---------- feedback ----------

var feedbackCmd = &cobra.Command{
	Use:   "feedback",
	Short: "Feedback loop (run)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.Help()
		return NewExitErr(ExitError, fmt.Errorf("missing feedback subcommand"))
	},
}

// ---------- feedback run ----------

var feedbackRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Trigger a manual feedback cycle",
	RunE:  runFeedbackRun,
}

func init() {
	f := feedbackRunCmd.Flags()
	f.String("repo", "", "filter by repository name")
	f.String("scope", "", "filter by scope")
}

func runFeedbackRun(cmd *cobra.Command, args []string) error {
	params := map[string]interface{}{}
	if v, _ := cmd.Flags().GetString("repo"); v != "" {
		params["repo"] = v
	}
	if v, _ := cmd.Flags().GetString("scope"); v != "" {
		params["scope"] = v
	}

	result, err := ipc.Call(SocketPath(), "feedback.run", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("feedback run: %w", err))
	}

	return formatFeedbackResult(result)
}

func formatFeedbackResult(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		return writeRawJSON(result)
	}

	var r struct {
		ConventionsPruned  int      `json:"conventions_pruned"`
		ConventionsKept    int      `json:"conventions_kept"`
		ObstaclesSurfaced  int      `json:"obstacles_surfaced"`
		RepoHealthUpdated  int      `json:"repo_health_updated"`
		SignaturesCached   int      `json:"signatures_cached"`
		KnowledgeFlows     int      `json:"knowledge_flows"`
		Errors             []string `json:"errors"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		fmt.Println(string(result))
		return nil
	}

	rec := output.NewRecord()
	rec.Set("Conventions Pruned", r.ConventionsPruned)
	rec.Set("Conventions Kept", r.ConventionsKept)
	rec.Set("Obstacles Surfaced", r.ObstaclesSurfaced)
	rec.Set("Repo Health Updated", r.RepoHealthUpdated)
	rec.Set("Signatures Cached", r.SignaturesCached)
	rec.Set("Knowledge Flows", r.KnowledgeFlows)
	if len(r.Errors) > 0 {
		rec.Set("Errors", len(r.Errors))
	}

	return output.NewFormatter(f).Format(os.Stdout, []*output.Record{rec})
}

// ---------- synthesis ----------

var synthesisCmd = &cobra.Command{
	Use:   "synthesis",
	Short: "Manual synthesis (run)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.Help()
		return NewExitErr(ExitError, fmt.Errorf("missing synthesis subcommand"))
	},
}

// ---------- synthesis run ----------

var synthesisRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run synthesis for a specific task",
	RunE:  runSynthesisRun,
}

func init() {
	f := synthesisRunCmd.Flags()
	f.String("task", "", "task ID to synthesize (required)")
	f.String("scope", "", "scope override")
}

func runSynthesisRun(cmd *cobra.Command, args []string) error {
	taskID, _ := cmd.Flags().GetString("task")
	if taskID == "" {
		return NewExitErr(ExitUsage, fmt.Errorf("--task is required"))
	}

	params := map[string]interface{}{
		"task_id": taskID,
	}
	if v, _ := cmd.Flags().GetString("scope"); v != "" {
		params["scope"] = v
	}

	result, err := ipc.Call(SocketPath(), "synthesis.run", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("synthesis run: %w", err))
	}

	return formatSynthesisResult(result)
}

func formatSynthesisResult(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		return writeRawJSON(result)
	}

	var r struct {
		TaskID      string `json:"task_id"`
		Tuples      int    `json:"tuples"`
		Synthesized int    `json:"synthesized"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		fmt.Println(string(result))
		return nil
	}

	rec := output.NewRecord()
	rec.Set("Task", r.TaskID)
	rec.Set("Tuples Found", r.Tuples)
	rec.Set("Synthesized", r.Synthesized)

	return output.NewFormatter(f).Format(os.Stdout, []*output.Record{rec})
}

// ---------- command registration ----------

func init() {
	// analytics subcommands
	analyticsCmd.AddCommand(analyticsPerformanceCmd)
	analyticsCmd.AddCommand(analyticsObstaclesCmd)
	analyticsCmd.AddCommand(analyticsConventionsCmd)
	analyticsCmd.AddCommand(analyticsKnowledgeCmd)
	analyticsCmd.AddCommand(analyticsSignaturesCmd)
	rootCmd.AddCommand(analyticsCmd)

	// gc subcommands
	gcCmd.AddCommand(gcRunCmd)
	gcCmd.AddCommand(gcStatusCmd)
	rootCmd.AddCommand(gcCmd)

	// feedback subcommands
	feedbackCmd.AddCommand(feedbackRunCmd)
	rootCmd.AddCommand(feedbackCmd)

	// synthesis subcommands
	synthesisCmd.AddCommand(synthesisRunCmd)
	rootCmd.AddCommand(synthesisCmd)
}

// ---------- helpers ----------

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// writeRawJSON writes the raw JSON result directly to stdout.
// Used for --output=json mode where we pass through the daemon response.
func writeRawJSON(data json.RawMessage) error {
	_, err := fmt.Fprintln(os.Stdout, string(data))
	return err
}
