// Package telemetry query.go implements a unified query engine over the hot tier
// (in-memory DuckDB) and warm tier (Parquet files). It builds CTE-based UNION ALL
// queries so callers see a single logical view of both tiers.
package telemetry

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/marcboeker/go-duckdb"
)

// QueryEngine provides a unified query interface over in-memory DuckDB tables
// and warm-tier Parquet files.
type QueryEngine struct {
	pipeline *Pipeline // nil for standalone mode
	db       *sql.DB   // standalone DuckDB connection (nil when using pipeline)
	warmDir  string
	once     sync.Once
}

// NewQueryEngine creates a QueryEngine backed by a live Pipeline (hot tier)
// and a warm directory of Parquet files.
func NewQueryEngine(pipeline *Pipeline, warmDir string) *QueryEngine {
	return &QueryEngine{
		pipeline: pipeline,
		warmDir:  warmDir,
	}
}

// NewStandaloneQueryEngine creates a read-only QueryEngine over Parquet files
// without a running pipeline. Useful for CLI queries against the warm tier.
func NewStandaloneQueryEngine(warmDir string) (*QueryEngine, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("query: open duckdb: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("query: ping duckdb: %w", err)
	}
	return &QueryEngine{
		db:      db,
		warmDir: warmDir,
	}, nil
}

// Close releases any standalone DuckDB connection. No-op for pipeline-backed engines.
func (q *QueryEngine) Close() error {
	var err error
	q.once.Do(func() {
		if q.db != nil {
			err = q.db.Close()
		}
	})
	return err
}

// conn returns the DuckDB *sql.DB to use for queries.
func (q *QueryEngine) conn() *sql.DB {
	if q.pipeline != nil {
		return q.pipeline.DB()
	}
	return q.db
}

// ---------------------------------------------------------------------------
// Union CTE builder
// ---------------------------------------------------------------------------

// parquetGlob returns the glob pattern for a Parquet stem across all partitions.
// Returns empty string if no matching files exist.
func (q *QueryEngine) parquetGlob(stem string) string {
	pattern := filepath.Join(q.warmDir, "*", stem+".parquet")
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		return ""
	}
	return pattern
}

// unionCTE builds a CTE that combines hot-tier table with warm-tier Parquet.
// If no Parquet files exist, the CTE reads only from the hot table.
// If pipeline is nil (standalone mode), it reads only from Parquet.
func (q *QueryEngine) unionCTE(table, stem, columns string) string {
	parquet := q.parquetGlob(stem)
	hasHot := q.pipeline != nil
	hasWarm := parquet != ""

	if hasHot && hasWarm {
		return fmt.Sprintf(
			"WITH unified AS (SELECT %s FROM %s UNION ALL SELECT %s FROM read_parquet('%s'))",
			columns, table, columns, parquet,
		)
	}
	if hasHot {
		return fmt.Sprintf("WITH unified AS (SELECT %s FROM %s)", columns, table)
	}
	if hasWarm {
		return fmt.Sprintf("WITH unified AS (SELECT %s FROM read_parquet('%s'))", columns, parquet)
	}
	// No data source — return CTE over empty hot table if it exists, else Parquet placeholder.
	// This will produce zero rows.
	if q.pipeline != nil {
		return fmt.Sprintf("WITH unified AS (SELECT %s FROM %s WHERE false)", columns, table)
	}
	return ""
}

// ---------------------------------------------------------------------------
// Span types
// ---------------------------------------------------------------------------

// Span represents a single trace span from the unified view.
type Span struct {
	TraceID       string  `json:"trace_id"`
	SpanID        string  `json:"span_id"`
	ParentSpanID  string  `json:"parent_span_id"`
	Name          string  `json:"name"`
	Kind          int     `json:"kind"`
	StartTimeNs   int64   `json:"start_time_ns"`
	EndTimeNs     int64   `json:"end_time_ns"`
	DurationNs    int64   `json:"duration_ns"`
	StatusCode    int     `json:"status_code"`
	StatusMessage string  `json:"status_message"`
	AgentName     string  `json:"agent_name"`
	TaskID        string  `json:"task_id"`
	RepoName      string  `json:"repo_name"`
	ModelName     string  `json:"model_name"`
	TokensIn      int64   `json:"tokens_in"`
	TokensOut     int64   `json:"tokens_out"`
	Cost          float64 `json:"cost"`
}

const spanColumns = "trace_id, span_id, parent_span_id, name, kind, start_time_ns, end_time_ns, duration_ns, status_code, status_message, agent_name, task_id, repo_name, model_name, tokens_in, tokens_out, cost"

func scanSpan(rows *sql.Rows) (Span, error) {
	var s Span
	err := rows.Scan(
		&s.TraceID, &s.SpanID, &s.ParentSpanID, &s.Name, &s.Kind,
		&s.StartTimeNs, &s.EndTimeNs, &s.DurationNs,
		&s.StatusCode, &s.StatusMessage,
		&s.AgentName, &s.TaskID, &s.RepoName,
		&s.ModelName, &s.TokensIn, &s.TokensOut, &s.Cost,
	)
	return s, err
}

// ---------------------------------------------------------------------------
// LookupTrace
// ---------------------------------------------------------------------------

// LookupTrace returns all spans belonging to a trace ID, ordered by start time.
func (q *QueryEngine) LookupTrace(traceID string) ([]Span, error) {
	cte := q.unionCTE("otel_spans", "spans", spanColumns)
	if cte == "" {
		return nil, nil
	}
	query := cte + " SELECT " + spanColumns + " FROM unified WHERE trace_id = ? ORDER BY start_time_ns"
	rows, err := q.conn().Query(query, traceID)
	if err != nil {
		return nil, fmt.Errorf("query: lookup trace: %w", err)
	}
	defer rows.Close()

	var spans []Span
	for rows.Next() {
		s, err := scanSpan(rows)
		if err != nil {
			return nil, fmt.Errorf("query: scan span: %w", err)
		}
		spans = append(spans, s)
	}
	return spans, rows.Err()
}

// ---------------------------------------------------------------------------
// SearchSpans
// ---------------------------------------------------------------------------

// SpanSearchParams defines filters for SearchSpans.
type SpanSearchParams struct {
	Name        string `json:"name,omitempty"`         // substring match on span name
	AgentName   string `json:"agent_name,omitempty"`   // exact match
	TaskID      string `json:"task_id,omitempty"`      // exact match
	MinDuration int64  `json:"min_duration,omitempty"` // nanoseconds
	MaxDuration int64  `json:"max_duration,omitempty"` // nanoseconds
	StartAfter  int64  `json:"start_after,omitempty"`  // nanosecond unix epoch
	StartBefore int64  `json:"start_before,omitempty"` // nanosecond unix epoch
	Limit       int    `json:"limit,omitempty"`        // max results (default 100)
}

// SearchSpans returns spans matching the given filters.
func (q *QueryEngine) SearchSpans(params SpanSearchParams) ([]Span, error) {
	cte := q.unionCTE("otel_spans", "spans", spanColumns)
	if cte == "" {
		return nil, nil
	}

	var where []string
	var args []interface{}

	if params.Name != "" {
		where = append(where, "name LIKE ?")
		args = append(args, "%"+params.Name+"%")
	}
	if params.AgentName != "" {
		where = append(where, "agent_name = ?")
		args = append(args, params.AgentName)
	}
	if params.TaskID != "" {
		where = append(where, "task_id = ?")
		args = append(args, params.TaskID)
	}
	if params.MinDuration > 0 {
		where = append(where, "duration_ns >= ?")
		args = append(args, params.MinDuration)
	}
	if params.MaxDuration > 0 {
		where = append(where, "duration_ns <= ?")
		args = append(args, params.MaxDuration)
	}
	if params.StartAfter > 0 {
		where = append(where, "start_time_ns >= ?")
		args = append(args, params.StartAfter)
	}
	if params.StartBefore > 0 {
		where = append(where, "start_time_ns <= ?")
		args = append(args, params.StartBefore)
	}

	query := cte + " SELECT " + spanColumns + " FROM unified"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY start_time_ns DESC"

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	query += fmt.Sprintf(" LIMIT %d", limit)

	rows, err := q.conn().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: search spans: %w", err)
	}
	defer rows.Close()

	var spans []Span
	for rows.Next() {
		s, err := scanSpan(rows)
		if err != nil {
			return nil, fmt.Errorf("query: scan span: %w", err)
		}
		spans = append(spans, s)
	}
	return spans, rows.Err()
}

// ---------------------------------------------------------------------------
// AggregateMetrics
// ---------------------------------------------------------------------------

// MetricAggregateParams defines parameters for AggregateMetrics.
type MetricAggregateParams struct {
	MetricName string `json:"metric_name,omitempty"` // filter by metric name (exact)
	AgentName  string `json:"agent_name,omitempty"`  // filter by agent
	TaskID     string `json:"task_id,omitempty"`     // filter by task
	Function   string `json:"function"`              // SUM, AVG, MAX, MIN, COUNT
	ValueField string `json:"value_field,omitempty"` // "value_int" or "value_double" (default: value_double)
	GroupBy    string `json:"group_by,omitempty"`    // column to group by (e.g., "agent_name", "name")
}

// MetricAggregateResult holds one row of aggregation output.
type MetricAggregateResult struct {
	GroupKey string  `json:"group_key,omitempty"`
	Value    float64 `json:"value"`
}

const metricColumns = "name, description, unit, metric_type, time_ns, start_time_ns, agent_name, task_id, repo_name, value_int, value_double, is_monotonic, aggregation_temp, histogram_count, histogram_sum, histogram_min, histogram_max, bucket_counts, explicit_bounds"

// AggregateMetrics performs an aggregation over the unified metrics view.
func (q *QueryEngine) AggregateMetrics(params MetricAggregateParams) ([]MetricAggregateResult, error) {
	cte := q.unionCTE("otel_metrics", "metrics", metricColumns)
	if cte == "" {
		return nil, nil
	}

	fn := strings.ToUpper(params.Function)
	switch fn {
	case "SUM", "AVG", "MAX", "MIN", "COUNT":
	default:
		return nil, fmt.Errorf("query: unsupported aggregate function %q", params.Function)
	}

	valField := params.ValueField
	if valField == "" {
		valField = "value_double"
	}
	switch valField {
	case "value_int", "value_double":
	default:
		return nil, fmt.Errorf("query: unsupported value field %q", valField)
	}

	var where []string
	var args []interface{}

	if params.MetricName != "" {
		where = append(where, "name = ?")
		args = append(args, params.MetricName)
	}
	if params.AgentName != "" {
		where = append(where, "agent_name = ?")
		args = append(args, params.AgentName)
	}
	if params.TaskID != "" {
		where = append(where, "task_id = ?")
		args = append(args, params.TaskID)
	}

	var selectExpr string
	if params.GroupBy != "" {
		selectExpr = fmt.Sprintf("%s AS group_key, %s(CAST(%s AS DOUBLE)) AS agg_value", params.GroupBy, fn, valField)
	} else {
		selectExpr = fmt.Sprintf("'' AS group_key, %s(CAST(%s AS DOUBLE)) AS agg_value", fn, valField)
	}

	query := cte + " SELECT " + selectExpr + " FROM unified"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if params.GroupBy != "" {
		query += " GROUP BY " + params.GroupBy
	}

	rows, err := q.conn().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: aggregate metrics: %w", err)
	}
	defer rows.Close()

	var results []MetricAggregateResult
	for rows.Next() {
		var r MetricAggregateResult
		var val *float64
		if err := rows.Scan(&r.GroupKey, &val); err != nil {
			return nil, fmt.Errorf("query: scan aggregate: %w", err)
		}
		if val != nil {
			r.Value = *val
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ---------------------------------------------------------------------------
// SearchLogs
// ---------------------------------------------------------------------------

// LogRecord represents a single log record from the unified view.
type LogRecord struct {
	TimeNs         int64  `json:"time_ns"`
	ObservedTimeNs int64  `json:"observed_time_ns"`
	SeverityNumber int    `json:"severity_number"`
	SeverityText   string `json:"severity_text"`
	Body           string `json:"body"`
	TraceID        string `json:"trace_id"`
	SpanID         string `json:"span_id"`
	AgentName      string `json:"agent_name"`
	TaskID         string `json:"task_id"`
	RepoName       string `json:"repo_name"`
}

const logColumns = "time_ns, observed_time_ns, severity_number, severity_text, body, trace_id, span_id, agent_name, task_id, repo_name"

func scanLog(rows *sql.Rows) (LogRecord, error) {
	var l LogRecord
	err := rows.Scan(
		&l.TimeNs, &l.ObservedTimeNs,
		&l.SeverityNumber, &l.SeverityText,
		&l.Body, &l.TraceID, &l.SpanID,
		&l.AgentName, &l.TaskID, &l.RepoName,
	)
	return l, err
}

// LogSearchParams defines filters for SearchLogs.
type LogSearchParams struct {
	Body           string `json:"body,omitempty"`            // substring match on body
	SeverityMin    int    `json:"severity_min,omitempty"`    // minimum severity number
	TraceID        string `json:"trace_id,omitempty"`        // exact match
	AgentName      string `json:"agent_name,omitempty"`      // exact match
	TaskID         string `json:"task_id,omitempty"`         // exact match
	StartAfter     int64  `json:"start_after,omitempty"`     // nanosecond unix epoch
	StartBefore    int64  `json:"start_before,omitempty"`    // nanosecond unix epoch
	Limit          int    `json:"limit,omitempty"`           // max results (default 100)
}

// SearchLogs returns log records matching the given filters.
func (q *QueryEngine) SearchLogs(params LogSearchParams) ([]LogRecord, error) {
	cte := q.unionCTE("otel_logs", "logs", logColumns)
	if cte == "" {
		return nil, nil
	}

	var where []string
	var args []interface{}

	if params.Body != "" {
		where = append(where, "body LIKE ?")
		args = append(args, "%"+params.Body+"%")
	}
	if params.SeverityMin > 0 {
		where = append(where, "severity_number >= ?")
		args = append(args, params.SeverityMin)
	}
	if params.TraceID != "" {
		where = append(where, "trace_id = ?")
		args = append(args, params.TraceID)
	}
	if params.AgentName != "" {
		where = append(where, "agent_name = ?")
		args = append(args, params.AgentName)
	}
	if params.TaskID != "" {
		where = append(where, "task_id = ?")
		args = append(args, params.TaskID)
	}
	if params.StartAfter > 0 {
		where = append(where, "time_ns >= ?")
		args = append(args, params.StartAfter)
	}
	if params.StartBefore > 0 {
		where = append(where, "time_ns <= ?")
		args = append(args, params.StartBefore)
	}

	query := cte + " SELECT " + logColumns + " FROM unified"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY time_ns DESC"

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	query += fmt.Sprintf(" LIMIT %d", limit)

	rows, err := q.conn().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: search logs: %w", err)
	}
	defer rows.Close()

	var logs []LogRecord
	for rows.Next() {
		l, err := scanLog(rows)
		if err != nil {
			return nil, fmt.Errorf("query: scan log: %w", err)
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// ---------------------------------------------------------------------------
// IPC dispatch
// ---------------------------------------------------------------------------

// QueryCommand represents a JSON command for HandleQuery dispatch.
type QueryCommand struct {
	Operation string               `json:"operation"` // "lookup_trace", "search_spans", "aggregate_metrics", "search_logs"
	TraceID   string               `json:"trace_id,omitempty"`
	Spans     *SpanSearchParams    `json:"spans,omitempty"`
	Metrics   *MetricAggregateParams `json:"metrics,omitempty"`
	Logs      *LogSearchParams     `json:"logs,omitempty"`
}

// QueryResult wraps the result of a HandleQuery dispatch.
type QueryResult struct {
	Spans   []Span                `json:"spans,omitempty"`
	Logs    []LogRecord           `json:"logs,omitempty"`
	Metrics []MetricAggregateResult `json:"metrics,omitempty"`
}

// HandleQuery dispatches a QueryCommand to the appropriate operation.
func (q *QueryEngine) HandleQuery(cmd QueryCommand) (*QueryResult, error) {
	switch cmd.Operation {
	case "lookup_trace":
		if cmd.TraceID == "" {
			return nil, fmt.Errorf("query: lookup_trace requires trace_id")
		}
		spans, err := q.LookupTrace(cmd.TraceID)
		if err != nil {
			return nil, err
		}
		return &QueryResult{Spans: spans}, nil

	case "search_spans":
		params := SpanSearchParams{}
		if cmd.Spans != nil {
			params = *cmd.Spans
		}
		spans, err := q.SearchSpans(params)
		if err != nil {
			return nil, err
		}
		return &QueryResult{Spans: spans}, nil

	case "aggregate_metrics":
		if cmd.Metrics == nil {
			return nil, fmt.Errorf("query: aggregate_metrics requires metrics params")
		}
		results, err := q.AggregateMetrics(*cmd.Metrics)
		if err != nil {
			return nil, err
		}
		return &QueryResult{Metrics: results}, nil

	case "search_logs":
		params := LogSearchParams{}
		if cmd.Logs != nil {
			params = *cmd.Logs
		}
		logs, err := q.SearchLogs(params)
		if err != nil {
			return nil, err
		}
		return &QueryResult{Logs: logs}, nil

	default:
		return nil, fmt.Errorf("query: unknown operation %q", cmd.Operation)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// WarmDir returns the configured warm directory path.
func (q *QueryEngine) WarmDir() string { return q.warmDir }

// HasWarmData returns true if any Parquet files exist in the warm directory.
func (q *QueryEngine) HasWarmData() bool {
	if q.warmDir == "" {
		return false
	}
	entries, err := os.ReadDir(q.warmDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		partDir := filepath.Join(q.warmDir, e.Name())
		files, _ := filepath.Glob(filepath.Join(partDir, "*.parquet"))
		if len(files) > 0 {
			return true
		}
	}
	return false
}

// HasHotData returns true if the pipeline has any in-memory data.
func (q *QueryEngine) HasHotData() bool {
	if q.pipeline == nil {
		return false
	}
	for _, table := range []string{"otel_spans", "otel_metrics", "otel_logs"} {
		var count int
		if err := q.conn().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err == nil && count > 0 {
			return true
		}
	}
	return false
}

// NowNs returns the current time in nanoseconds since epoch.
func NowNs() int64 {
	return time.Now().UnixNano()
}
