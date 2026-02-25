package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/chazu/procyon-park/internal/ipc"
	"github.com/chazu/procyon-park/internal/output"
	"github.com/chazu/procyon-park/internal/telemetry"
	"github.com/spf13/cobra"
)

// ---------- telemetry ----------

var telemetryCmd = &cobra.Command{
	Use:   "telemetry",
	Short: "Telemetry interaction (status, search, trace, metrics, export)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.Help()
		return NewExitErr(ExitError, fmt.Errorf("missing telemetry subcommand"))
	},
}

// ---------- telemetry status ----------

var telemetryStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show receiver status, pipeline stats, last flush time",
	RunE:  runTelemetryStatus,
}

func runTelemetryStatus(cmd *cobra.Command, args []string) error {
	result, err := ipc.Call(SocketPath(), "telemetry.status", nil)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("telemetry status: %w", err))
	}
	return formatTelemetryStatus(result)
}

func formatTelemetryStatus(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		return writeRawJSON(result)
	}

	var r struct {
		ReceiverRunning bool   `json:"receiver_running"`
		GRPCAddr        string `json:"grpc_addr"`
		HTTPAddr        string `json:"http_addr"`
		SpanCount       int64  `json:"span_count"`
		MetricCount     int64  `json:"metric_count"`
		LogCount        int64  `json:"log_count"`
		HasWarmData     bool   `json:"has_warm_data"`
		WarmDir         string `json:"warm_dir"`
		LastFlushTime   string `json:"last_flush_time"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		fmt.Println(string(result))
		return nil
	}

	rec := output.NewRecord()
	rec.Set("Receiver", boolStatus(r.ReceiverRunning))
	rec.Set("gRPC", r.GRPCAddr)
	rec.Set("HTTP", r.HTTPAddr)
	rec.Set("Spans (hot)", r.SpanCount)
	rec.Set("Metrics (hot)", r.MetricCount)
	rec.Set("Logs (hot)", r.LogCount)
	rec.Set("Warm Data", boolStatus(r.HasWarmData))
	if r.LastFlushTime != "" {
		rec.Set("Last Flush", r.LastFlushTime)
	}

	return output.NewFormatter(f).Format(os.Stdout, []*output.Record{rec})
}

func boolStatus(b bool) string {
	if b {
		return "running"
	}
	return "stopped"
}

// ---------- telemetry search ----------

var telemetrySearchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search spans or logs",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.Help()
		return NewExitErr(ExitError, fmt.Errorf("missing search subcommand (spans or logs)"))
	},
}

// ---------- telemetry search spans ----------

var telemetrySearchSpansCmd = &cobra.Command{
	Use:   "spans",
	Short: "Search spans by name, agent, task, duration",
	RunE:  runTelemetrySearchSpans,
}

func init() {
	f := telemetrySearchSpansCmd.Flags()
	f.String("agent", "", "filter by agent name (exact)")
	f.String("task", "", "filter by task ID (exact)")
	f.String("name", "", "filter by span name (substring)")
	f.String("min-duration", "", "minimum duration (e.g. 100ms, 1s, 500us)")
	f.Int("limit", 100, "maximum results")
}

func runTelemetrySearchSpans(cmd *cobra.Command, args []string) error {
	params := telemetry.SpanSearchParams{}

	if v, _ := cmd.Flags().GetString("agent"); v != "" {
		params.AgentName = v
	}
	if v, _ := cmd.Flags().GetString("task"); v != "" {
		params.TaskID = v
	}
	if v, _ := cmd.Flags().GetString("name"); v != "" {
		params.Name = v
	}
	if v, _ := cmd.Flags().GetString("min-duration"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return NewExitErr(ExitUsage, fmt.Errorf("invalid duration %q: %w", v, err))
		}
		params.MinDuration = d.Nanoseconds()
	}
	if v, _ := cmd.Flags().GetInt("limit"); v > 0 {
		params.Limit = v
	}

	qc := telemetry.QueryCommand{
		Operation: "search_spans",
		Spans:     &params,
	}

	result, err := ipc.Call(SocketPath(), "telemetry.query", qc)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("telemetry search spans: %w", err))
	}

	return formatSpanResults(result)
}

func formatSpanResults(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		return writeRawJSON(result)
	}

	var qr telemetry.QueryResult
	if err := json.Unmarshal(result, &qr); err != nil {
		fmt.Println(string(result))
		return nil
	}

	if len(qr.Spans) == 0 {
		fmt.Println("No spans found.")
		return nil
	}

	var records []*output.Record
	for _, s := range qr.Spans {
		rec := output.NewRecord()
		rec.Set("Trace", truncate(s.TraceID, 12))
		rec.Set("Span", truncate(s.SpanID, 12))
		rec.Set("Name", truncate(s.Name, 40))
		rec.Set("Duration", formatDuration(s.DurationNs))
		rec.Set("Agent", s.AgentName)
		rec.Set("Task", s.TaskID)
		if s.StatusCode != 0 {
			rec.Set("Status", strconv.Itoa(s.StatusCode))
		}
		records = append(records, rec)
	}
	return output.NewFormatter(f).Format(os.Stdout, records)
}

// ---------- telemetry search logs ----------

var telemetrySearchLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Search logs by severity, agent, body content",
	RunE:  runTelemetrySearchLogs,
}

func init() {
	f := telemetrySearchLogsCmd.Flags()
	f.String("agent", "", "filter by agent name (exact)")
	f.String("task", "", "filter by task ID (exact)")
	f.String("body", "", "filter by body content (substring)")
	f.Int("severity", 0, "minimum severity number (1-24)")
	f.Int("limit", 100, "maximum results")
}

func runTelemetrySearchLogs(cmd *cobra.Command, args []string) error {
	params := telemetry.LogSearchParams{}

	if v, _ := cmd.Flags().GetString("agent"); v != "" {
		params.AgentName = v
	}
	if v, _ := cmd.Flags().GetString("task"); v != "" {
		params.TaskID = v
	}
	if v, _ := cmd.Flags().GetString("body"); v != "" {
		params.Body = v
	}
	if v, _ := cmd.Flags().GetInt("severity"); v > 0 {
		params.SeverityMin = v
	}
	if v, _ := cmd.Flags().GetInt("limit"); v > 0 {
		params.Limit = v
	}

	qc := telemetry.QueryCommand{
		Operation: "search_logs",
		Logs:      &params,
	}

	result, err := ipc.Call(SocketPath(), "telemetry.query", qc)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("telemetry search logs: %w", err))
	}

	return formatLogResults(result)
}

func formatLogResults(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		return writeRawJSON(result)
	}

	var qr telemetry.QueryResult
	if err := json.Unmarshal(result, &qr); err != nil {
		fmt.Println(string(result))
		return nil
	}

	if len(qr.Logs) == 0 {
		fmt.Println("No logs found.")
		return nil
	}

	var records []*output.Record
	for _, l := range qr.Logs {
		rec := output.NewRecord()
		rec.Set("Time", formatNsTime(l.TimeNs))
		rec.Set("Severity", l.SeverityText)
		rec.Set("Agent", l.AgentName)
		rec.Set("Task", l.TaskID)
		rec.Set("Body", truncate(l.Body, 80))
		records = append(records, rec)
	}
	return output.NewFormatter(f).Format(os.Stdout, records)
}

// ---------- telemetry trace ----------

var telemetryTraceCmd = &cobra.Command{
	Use:   "trace <trace-id>",
	Short: "Lookup full trace by ID",
	Args:  cobra.ExactArgs(1),
	RunE:  runTelemetryTrace,
}

func runTelemetryTrace(cmd *cobra.Command, args []string) error {
	qc := telemetry.QueryCommand{
		Operation: "lookup_trace",
		TraceID:   args[0],
	}

	result, err := ipc.Call(SocketPath(), "telemetry.query", qc)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("telemetry trace: %w", err))
	}

	return formatTraceResults(result)
}

func formatTraceResults(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		return writeRawJSON(result)
	}

	var qr telemetry.QueryResult
	if err := json.Unmarshal(result, &qr); err != nil {
		fmt.Println(string(result))
		return nil
	}

	if len(qr.Spans) == 0 {
		fmt.Println("No spans found for trace.")
		return nil
	}

	var records []*output.Record
	for _, s := range qr.Spans {
		rec := output.NewRecord()
		rec.Set("Span", truncate(s.SpanID, 12))
		rec.Set("Parent", truncate(s.ParentSpanID, 12))
		rec.Set("Name", truncate(s.Name, 40))
		rec.Set("Duration", formatDuration(s.DurationNs))
		rec.Set("Status", strconv.Itoa(s.StatusCode))
		rec.Set("Agent", s.AgentName)
		if s.ModelName != "" {
			rec.Set("Model", s.ModelName)
		}
		if s.TokensIn > 0 || s.TokensOut > 0 {
			rec.Set("Tokens", fmt.Sprintf("%d/%d", s.TokensIn, s.TokensOut))
		}
		records = append(records, rec)
	}
	return output.NewFormatter(f).Format(os.Stdout, records)
}

// ---------- telemetry metrics ----------

var telemetryMetricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Aggregate metrics by name with SUM/AVG/MAX/MIN/COUNT",
	RunE:  runTelemetryMetrics,
}

func init() {
	f := telemetryMetricsCmd.Flags()
	f.String("name", "", "metric name filter (exact)")
	f.String("agg", "SUM", "aggregation function (SUM, AVG, MAX, MIN, COUNT)")
	f.String("agent", "", "filter by agent name")
	f.String("task", "", "filter by task ID")
	f.String("group-by", "", "group results by column (e.g. agent_name, name)")
	f.String("value-field", "value_double", "value field (value_int or value_double)")
}

func runTelemetryMetrics(cmd *cobra.Command, args []string) error {
	params := telemetry.MetricAggregateParams{}

	if v, _ := cmd.Flags().GetString("name"); v != "" {
		params.MetricName = v
	}
	if v, _ := cmd.Flags().GetString("agg"); v != "" {
		params.Function = v
	}
	if v, _ := cmd.Flags().GetString("agent"); v != "" {
		params.AgentName = v
	}
	if v, _ := cmd.Flags().GetString("task"); v != "" {
		params.TaskID = v
	}
	if v, _ := cmd.Flags().GetString("group-by"); v != "" {
		params.GroupBy = v
	}
	if v, _ := cmd.Flags().GetString("value-field"); v != "" {
		params.ValueField = v
	}

	qc := telemetry.QueryCommand{
		Operation: "aggregate_metrics",
		Metrics:   &params,
	}

	result, err := ipc.Call(SocketPath(), "telemetry.query", qc)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("telemetry metrics: %w", err))
	}

	return formatMetricResults(result)
}

func formatMetricResults(result json.RawMessage) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		return writeRawJSON(result)
	}

	var qr telemetry.QueryResult
	if err := json.Unmarshal(result, &qr); err != nil {
		fmt.Println(string(result))
		return nil
	}

	if len(qr.Metrics) == 0 {
		fmt.Println("No metrics found.")
		return nil
	}

	var records []*output.Record
	for _, m := range qr.Metrics {
		rec := output.NewRecord()
		if m.GroupKey != "" {
			rec.Set("Group", m.GroupKey)
		}
		rec.Set("Value", fmt.Sprintf("%.4f", m.Value))
		records = append(records, rec)
	}
	return output.NewFormatter(f).Format(os.Stdout, records)
}

// ---------- telemetry export ----------

var telemetryExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export telemetry data to Parquet or JSON",
	RunE:  runTelemetryExport,
}

func init() {
	f := telemetryExportCmd.Flags()
	f.String("format", "json", "export format (parquet or json)")
	f.StringP("output", "O", "", "output file path (default: stdout for JSON)")
	f.String("signal", "spans", "signal type to export (spans, metrics, logs)")
}

func runTelemetryExport(cmd *cobra.Command, args []string) error {
	format, _ := cmd.Flags().GetString("format")
	outPath, _ := cmd.Flags().GetString("output")
	signal, _ := cmd.Flags().GetString("signal")

	switch format {
	case "json", "parquet":
	default:
		return NewExitErr(ExitUsage, fmt.Errorf("unsupported export format %q (use json or parquet)", format))
	}

	switch signal {
	case "spans", "metrics", "logs":
	default:
		return NewExitErr(ExitUsage, fmt.Errorf("unsupported signal type %q (use spans, metrics, or logs)", signal))
	}

	params := map[string]interface{}{
		"format": format,
		"signal": signal,
	}
	if outPath != "" {
		params["output"] = outPath
	}

	result, err := ipc.Call(SocketPath(), "telemetry.export", params)
	if err != nil {
		return NewExitErr(ExitError, fmt.Errorf("telemetry export: %w", err))
	}

	return formatExportResult(result, format)
}

func formatExportResult(result json.RawMessage, exportFormat string) error {
	f, err := OutputFormat()
	if err != nil {
		return NewExitErr(ExitUsage, err)
	}

	if f == output.FormatJSON || f == output.FormatJSONPretty {
		return writeRawJSON(result)
	}

	var r struct {
		Path    string `json:"path"`
		Format  string `json:"format"`
		Signal  string `json:"signal"`
		Records int64  `json:"records"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		fmt.Println(string(result))
		return nil
	}

	rec := output.NewRecord()
	rec.Set("Signal", r.Signal)
	rec.Set("Format", r.Format)
	rec.Set("Records", r.Records)
	if r.Path != "" {
		rec.Set("Path", r.Path)
	}

	return output.NewFormatter(f).Format(os.Stdout, []*output.Record{rec})
}

// ---------- command registration ----------

func init() {
	// search subcommands
	telemetrySearchCmd.AddCommand(telemetrySearchSpansCmd)
	telemetrySearchCmd.AddCommand(telemetrySearchLogsCmd)

	// telemetry subcommands
	telemetryCmd.AddCommand(telemetryStatusCmd)
	telemetryCmd.AddCommand(telemetrySearchCmd)
	telemetryCmd.AddCommand(telemetryTraceCmd)
	telemetryCmd.AddCommand(telemetryMetricsCmd)
	telemetryCmd.AddCommand(telemetryExportCmd)

	rootCmd.AddCommand(telemetryCmd)
}

// ---------- helpers ----------

// formatDuration formats nanoseconds as a human-readable duration.
func formatDuration(ns int64) string {
	d := time.Duration(ns)
	if d < time.Microsecond {
		return fmt.Sprintf("%dns", ns)
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%.1fus", float64(ns)/1e3)
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(ns)/1e6)
	}
	return fmt.Sprintf("%.2fs", float64(ns)/1e9)
}

// formatNsTime formats nanosecond epoch as RFC3339.
func formatNsTime(ns int64) string {
	if ns == 0 {
		return ""
	}
	return time.Unix(0, ns).UTC().Format(time.RFC3339)
}
