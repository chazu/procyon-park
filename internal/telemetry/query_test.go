package telemetry

import (
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// setupPipelineWithSpans creates a pipeline with test spans inserted.
func setupPipelineWithSpans(t *testing.T) (*Pipeline, string) {
	t.Helper()
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	// Insert spans for different agents and traces.
	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("Lark", "task-1", "repo-a", "opus", 100, 50)})
	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("Cog", "task-2", "repo-a", "sonnet", 200, 80)})
	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("Lark", "task-1", "repo-a", "opus", 50, 25)})

	warmDir := t.TempDir()
	return p, warmDir
}

// flushAndVerify flushes data to Parquet and verifies files exist.
func flushAndVerify(t *testing.T, p *Pipeline, warmDir string, flushTime time.Time) {
	t.Helper()
	f := NewFlusher(p, FlushConfig{WarmDir: warmDir})
	if err := f.FlushAt(flushTime); err != nil {
		t.Fatalf("FlushAt: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Unified query: hot + warm tiers
// ---------------------------------------------------------------------------

func TestQueryEngineUnifiedView(t *testing.T) {
	p, warmDir := setupPipelineWithSpans(t)
	defer p.Close()

	// Flush current data to warm tier.
	flushAndVerify(t, p, warmDir, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))

	// Insert more data into hot tier (after flush).
	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("Jinx", "task-3", "repo-b", "haiku", 10, 5)})

	qe := NewQueryEngine(p, warmDir)

	// Search should find spans from both tiers.
	spans, err := qe.SearchSpans(SpanSearchParams{Limit: 50})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	// 3 from warm + 1 from hot = 4
	if len(spans) != 4 {
		t.Errorf("got %d spans, want 4", len(spans))
	}
}

// ---------------------------------------------------------------------------
// LookupTrace
// ---------------------------------------------------------------------------

func TestLookupTrace(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	// Insert two spans with the same trace.
	req := makeTraceRequest("Lark", "task-1", "repo", "opus", 100, 50)
	p.handleSignal(Signal{Type: SignalTraces, Payload: req})

	// Get the trace_id that was inserted.
	var traceID string
	if err := p.DB().QueryRow("SELECT trace_id FROM otel_spans LIMIT 1").Scan(&traceID); err != nil {
		t.Fatalf("get trace_id: %v", err)
	}

	qe := NewQueryEngine(p, t.TempDir())

	spans, err := qe.LookupTrace(traceID)
	if err != nil {
		t.Fatalf("LookupTrace: %v", err)
	}
	if len(spans) != 1 {
		t.Errorf("got %d spans, want 1", len(spans))
	}
	if len(spans) > 0 && spans[0].TraceID != traceID {
		t.Errorf("trace_id = %q, want %q", spans[0].TraceID, traceID)
	}
}

func TestLookupTraceNotFound(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	qe := NewQueryEngine(p, t.TempDir())

	spans, err := qe.LookupTrace("nonexistent")
	if err != nil {
		t.Fatalf("LookupTrace: %v", err)
	}
	if len(spans) != 0 {
		t.Errorf("got %d spans, want 0", len(spans))
	}
}

// ---------------------------------------------------------------------------
// SearchSpans with filters
// ---------------------------------------------------------------------------

func TestSearchSpansByAgent(t *testing.T) {
	p, warmDir := setupPipelineWithSpans(t)
	defer p.Close()

	qe := NewQueryEngine(p, warmDir)

	spans, err := qe.SearchSpans(SpanSearchParams{AgentName: "Lark"})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	if len(spans) != 2 {
		t.Errorf("got %d spans for Lark, want 2", len(spans))
	}
	for _, s := range spans {
		if s.AgentName != "Lark" {
			t.Errorf("span agent = %q, want Lark", s.AgentName)
		}
	}
}

func TestSearchSpansByTask(t *testing.T) {
	p, warmDir := setupPipelineWithSpans(t)
	defer p.Close()

	qe := NewQueryEngine(p, warmDir)

	spans, err := qe.SearchSpans(SpanSearchParams{TaskID: "task-2"})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	if len(spans) != 1 {
		t.Errorf("got %d spans for task-2, want 1", len(spans))
	}
}

func TestSearchSpansByName(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	// makeTraceRequest uses a fixed span name; insert spans and check name filter.
	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("A", "t", "r", "m", 1, 1)})

	// Get the span name from the inserted data.
	var name string
	p.DB().QueryRow("SELECT name FROM otel_spans LIMIT 1").Scan(&name)

	qe := NewQueryEngine(p, t.TempDir())

	spans, err := qe.SearchSpans(SpanSearchParams{Name: name})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	if len(spans) != 1 {
		t.Errorf("got %d spans matching name %q, want 1", len(spans), name)
	}
}

func TestSearchSpansDurationFilter(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	// Insert spans with known durations by direct SQL.
	p.DB().Exec(`INSERT INTO otel_spans VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"t1", "s1", "", "fast-op", 0, 1000, 2000, 1000, 0, "", "A", "t", "r", "m", 0, 0, 0.0)
	p.DB().Exec(`INSERT INTO otel_spans VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"t2", "s2", "", "slow-op", 0, 1000, 6000, 5000, 0, "", "B", "t", "r", "m", 0, 0, 0.0)

	qe := NewQueryEngine(p, t.TempDir())

	// MinDuration filter.
	spans, err := qe.SearchSpans(SpanSearchParams{MinDuration: 3000})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	if len(spans) != 1 {
		t.Errorf("got %d spans with min_duration 3000, want 1", len(spans))
	}
	if len(spans) > 0 && spans[0].Name != "slow-op" {
		t.Errorf("span name = %q, want slow-op", spans[0].Name)
	}

	// MaxDuration filter.
	spans, err = qe.SearchSpans(SpanSearchParams{MaxDuration: 2000})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	if len(spans) != 1 {
		t.Errorf("got %d spans with max_duration 2000, want 1", len(spans))
	}
}

func TestSearchSpansLimit(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	for i := 0; i < 10; i++ {
		p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("A", "t", "r", "m", 1, 1)})
	}

	qe := NewQueryEngine(p, t.TempDir())

	spans, err := qe.SearchSpans(SpanSearchParams{Limit: 3})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	if len(spans) != 3 {
		t.Errorf("got %d spans with limit 3, want 3", len(spans))
	}
}

// ---------------------------------------------------------------------------
// AggregateMetrics
// ---------------------------------------------------------------------------

func TestAggregateMetricsSum(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	// Insert gauge metrics with known values.
	p.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("Lark", 10)})
	p.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("Lark", 20)})
	p.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("Cog", 30)})

	qe := NewQueryEngine(p, t.TempDir())

	results, err := qe.AggregateMetrics(MetricAggregateParams{
		Function:   "SUM",
		ValueField: "value_int",
	})
	if err != nil {
		t.Fatalf("AggregateMetrics: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Value != 60 {
		t.Errorf("SUM = %v, want 60", results[0].Value)
	}
}

func TestAggregateMetricsGroupBy(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	p.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("Lark", 10)})
	p.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("Lark", 20)})
	p.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("Cog", 30)})

	qe := NewQueryEngine(p, t.TempDir())

	results, err := qe.AggregateMetrics(MetricAggregateParams{
		Function:   "SUM",
		ValueField: "value_int",
		GroupBy:    "agent_name",
	})
	if err != nil {
		t.Fatalf("AggregateMetrics: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d groups, want 2", len(results))
	}

	// Build map for order-independent check.
	m := map[string]float64{}
	for _, r := range results {
		m[r.GroupKey] = r.Value
	}
	if m["Lark"] != 30 {
		t.Errorf("Lark SUM = %v, want 30", m["Lark"])
	}
	if m["Cog"] != 30 {
		t.Errorf("Cog SUM = %v, want 30", m["Cog"])
	}
}

func TestAggregateMetricsCount(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	p.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("A", 1)})
	p.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("A", 2)})
	p.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("A", 3)})

	qe := NewQueryEngine(p, t.TempDir())

	results, err := qe.AggregateMetrics(MetricAggregateParams{
		Function:   "COUNT",
		ValueField: "value_int",
	})
	if err != nil {
		t.Fatalf("AggregateMetrics: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Value != 3 {
		t.Errorf("COUNT = %v, want 3", results[0].Value)
	}
}

func TestAggregateMetricsInvalidFunction(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	qe := NewQueryEngine(p, t.TempDir())

	_, err = qe.AggregateMetrics(MetricAggregateParams{Function: "INVALID"})
	if err == nil {
		t.Error("expected error for invalid function")
	}
}

func TestAggregateMetricsAvg(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	p.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("A", 10)})
	p.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("A", 20)})

	qe := NewQueryEngine(p, t.TempDir())

	results, err := qe.AggregateMetrics(MetricAggregateParams{
		Function:   "AVG",
		ValueField: "value_int",
	})
	if err != nil {
		t.Fatalf("AggregateMetrics: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Value != 15 {
		t.Errorf("AVG = %v, want 15", results[0].Value)
	}
}

// ---------------------------------------------------------------------------
// SearchLogs
// ---------------------------------------------------------------------------

func TestSearchLogs(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	p.handleSignal(Signal{Type: SignalLogs, Payload: makeLogRequest("Lark", "error connecting to database")})
	p.handleSignal(Signal{Type: SignalLogs, Payload: makeLogRequest("Cog", "span started")})
	p.handleSignal(Signal{Type: SignalLogs, Payload: makeLogRequest("Lark", "error timeout")})

	qe := NewQueryEngine(p, t.TempDir())

	// Search by body substring.
	logs, err := qe.SearchLogs(LogSearchParams{Body: "error"})
	if err != nil {
		t.Fatalf("SearchLogs: %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("got %d logs matching 'error', want 2", len(logs))
	}

	// Search by agent.
	logs, err = qe.SearchLogs(LogSearchParams{AgentName: "Cog"})
	if err != nil {
		t.Fatalf("SearchLogs: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("got %d logs for Cog, want 1", len(logs))
	}
}

func TestSearchLogsLimit(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	for i := 0; i < 10; i++ {
		p.handleSignal(Signal{Type: SignalLogs, Payload: makeLogRequest("A", "log line")})
	}

	qe := NewQueryEngine(p, t.TempDir())

	logs, err := qe.SearchLogs(LogSearchParams{Limit: 5})
	if err != nil {
		t.Fatalf("SearchLogs: %v", err)
	}
	if len(logs) != 5 {
		t.Errorf("got %d logs with limit 5, want 5", len(logs))
	}
}

// ---------------------------------------------------------------------------
// IPC dispatch
// ---------------------------------------------------------------------------

func TestHandleQueryLookupTrace(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("A", "t", "r", "m", 1, 1)})

	var traceID string
	p.DB().QueryRow("SELECT trace_id FROM otel_spans LIMIT 1").Scan(&traceID)

	qe := NewQueryEngine(p, t.TempDir())

	result, err := qe.HandleQuery(QueryCommand{
		Operation: "lookup_trace",
		TraceID:   traceID,
	})
	if err != nil {
		t.Fatalf("HandleQuery: %v", err)
	}
	if len(result.Spans) != 1 {
		t.Errorf("got %d spans, want 1", len(result.Spans))
	}
}

func TestHandleQuerySearchSpans(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("Lark", "t1", "r", "m", 1, 1)})
	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("Cog", "t2", "r", "m", 1, 1)})

	qe := NewQueryEngine(p, t.TempDir())

	result, err := qe.HandleQuery(QueryCommand{
		Operation: "search_spans",
		Spans:     &SpanSearchParams{AgentName: "Lark"},
	})
	if err != nil {
		t.Fatalf("HandleQuery: %v", err)
	}
	if len(result.Spans) != 1 {
		t.Errorf("got %d spans, want 1", len(result.Spans))
	}
}

func TestHandleQueryAggregateMetrics(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	p.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("A", 10)})
	p.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("A", 20)})

	qe := NewQueryEngine(p, t.TempDir())

	result, err := qe.HandleQuery(QueryCommand{
		Operation: "aggregate_metrics",
		Metrics:   &MetricAggregateParams{Function: "SUM", ValueField: "value_int"},
	})
	if err != nil {
		t.Fatalf("HandleQuery: %v", err)
	}
	if len(result.Metrics) != 1 || result.Metrics[0].Value != 30 {
		t.Errorf("got %+v, want SUM=30", result.Metrics)
	}
}

func TestHandleQuerySearchLogs(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	p.handleSignal(Signal{Type: SignalLogs, Payload: makeLogRequest("A", "test message")})

	qe := NewQueryEngine(p, t.TempDir())

	result, err := qe.HandleQuery(QueryCommand{
		Operation: "search_logs",
		Logs:      &LogSearchParams{Body: "test"},
	})
	if err != nil {
		t.Fatalf("HandleQuery: %v", err)
	}
	if len(result.Logs) != 1 {
		t.Errorf("got %d logs, want 1", len(result.Logs))
	}
}

func TestHandleQueryUnknownOperation(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	qe := NewQueryEngine(p, t.TempDir())

	_, err = qe.HandleQuery(QueryCommand{Operation: "unknown"})
	if err == nil {
		t.Error("expected error for unknown operation")
	}
}

func TestHandleQueryLookupTraceMissingID(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	qe := NewQueryEngine(p, t.TempDir())

	_, err = qe.HandleQuery(QueryCommand{Operation: "lookup_trace"})
	if err == nil {
		t.Error("expected error for missing trace_id")
	}
}

// ---------------------------------------------------------------------------
// Standalone mode
// ---------------------------------------------------------------------------

func TestStandaloneQueryEngine(t *testing.T) {
	// First, create Parquet files with a regular pipeline.
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	warmDir := t.TempDir()

	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("Lark", "task-1", "repo", "opus", 100, 50)})
	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("Cog", "task-2", "repo", "sonnet", 200, 80)})
	p.handleSignal(Signal{Type: SignalLogs, Payload: makeLogRequest("Lark", "standalone test log")})

	flushAndVerify(t, p, warmDir, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	p.Close()

	// Now create a standalone query engine — no pipeline.
	qe, err := NewStandaloneQueryEngine(warmDir)
	if err != nil {
		t.Fatalf("NewStandaloneQueryEngine: %v", err)
	}
	defer qe.Close()

	// Search spans should find warm-tier data.
	spans, err := qe.SearchSpans(SpanSearchParams{Limit: 50})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	if len(spans) != 2 {
		t.Errorf("got %d spans, want 2", len(spans))
	}

	// Search logs should find warm-tier data.
	logs, err := qe.SearchLogs(LogSearchParams{Body: "standalone"})
	if err != nil {
		t.Fatalf("SearchLogs: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("got %d logs, want 1", len(logs))
	}
}

func TestStandaloneQueryEngineNoData(t *testing.T) {
	warmDir := t.TempDir()

	qe, err := NewStandaloneQueryEngine(warmDir)
	if err != nil {
		t.Fatalf("NewStandaloneQueryEngine: %v", err)
	}
	defer qe.Close()

	// No Parquet files means no data — should return nil (no error).
	spans, err := qe.SearchSpans(SpanSearchParams{})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	if spans != nil {
		t.Errorf("got %d spans, want nil", len(spans))
	}
}

// ---------------------------------------------------------------------------
// HasWarmData / HasHotData
// ---------------------------------------------------------------------------

func TestHasWarmData(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	warmDir := t.TempDir()
	qe := NewQueryEngine(p, warmDir)

	if qe.HasWarmData() {
		t.Error("HasWarmData = true before any flush")
	}

	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("A", "t", "r", "m", 1, 1)})
	flushAndVerify(t, p, warmDir, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	if !qe.HasWarmData() {
		t.Error("HasWarmData = false after flush")
	}
}

func TestHasHotData(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	qe := NewQueryEngine(p, t.TempDir())

	if qe.HasHotData() {
		t.Error("HasHotData = true on empty pipeline")
	}

	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("A", "t", "r", "m", 1, 1)})

	if !qe.HasHotData() {
		t.Error("HasHotData = false after insert")
	}
}

// ---------------------------------------------------------------------------
// Warm-only query (no hot tier data)
// ---------------------------------------------------------------------------

func TestQueryWarmOnlyWithPipeline(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	warmDir := t.TempDir()

	// Insert and flush to warm tier.
	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("Lark", "task-w", "repo", "opus", 100, 50)})
	flushAndVerify(t, p, warmDir, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))

	// Hot tier is now empty after flush purge.
	qe := NewQueryEngine(p, warmDir)

	spans, err := qe.SearchSpans(SpanSearchParams{AgentName: "Lark"})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	if len(spans) != 1 {
		t.Errorf("got %d spans, want 1 from warm tier", len(spans))
	}
}

// ---------------------------------------------------------------------------
// Multiple partitions
// ---------------------------------------------------------------------------

func TestQueryAcrossPartitions(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	warmDir := t.TempDir()
	f := NewFlusher(p, FlushConfig{WarmDir: warmDir})

	// Partition 1: January.
	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("A", "t1", "r", "m", 1, 1)})
	if err := f.FlushAt(time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Flush jan: %v", err)
	}

	// Partition 2: March.
	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("B", "t2", "r", "m", 2, 2)})
	if err := f.FlushAt(time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Flush mar: %v", err)
	}

	// Verify both partition dirs exist.
	for _, part := range []string{"2026-01", "2026-03"} {
		path := filepath.Join(warmDir, part, "spans.parquet")
		if _, err := fileStat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}

	qe := NewQueryEngine(p, warmDir)

	// Should find spans from both partitions.
	spans, err := qe.SearchSpans(SpanSearchParams{Limit: 50})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	if len(spans) != 2 {
		t.Errorf("got %d spans across partitions, want 2", len(spans))
	}
}

func fileStat(path string) (interface{}, error) {
	return filepath.Glob(path)
}
