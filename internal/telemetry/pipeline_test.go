package telemetry

import (
	"context"
	"database/sql"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// ---------------------------------------------------------------------------
// Schema creation
// ---------------------------------------------------------------------------

func TestPipelineSchemaCreation(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	tables := []string{"otel_spans", "otel_metrics", "otel_logs"}
	for _, tbl := range tables {
		var count int
		err := p.DB().QueryRow("SELECT COUNT(*) FROM " + tbl).Scan(&count)
		if err != nil {
			t.Errorf("table %s not queryable: %v", tbl, err)
		}
	}
}

func TestPipelineSchemaColumns(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	// Verify otel_spans has expected columns.
	rows, err := p.DB().Query("SELECT column_name FROM information_schema.columns WHERE table_name = 'otel_spans' ORDER BY ordinal_position")
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		cols = append(cols, col)
	}

	want := []string{
		"trace_id", "span_id", "parent_span_id", "name", "kind",
		"start_time_ns", "end_time_ns", "duration_ns",
		"status_code", "status_message",
		"agent_name", "task_id", "repo_name",
		"model_name", "tokens_in", "tokens_out", "cost",
	}
	if len(cols) != len(want) {
		t.Fatalf("otel_spans column count = %d, want %d; got %v", len(cols), len(want), cols)
	}
	for i, w := range want {
		if cols[i] != w {
			t.Errorf("otel_spans column %d = %q, want %q", i, cols[i], w)
		}
	}
}

// ---------------------------------------------------------------------------
// Protobuf conversion helpers
// ---------------------------------------------------------------------------

func makeTraceRequest(agentName, taskID, repoName, model string, tokensIn, tokensOut int64) *coltracepb.ExportTraceServiceRequest {
	resAttrs := []*commonpb.KeyValue{
		{Key: "cub.agent.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: agentName}}},
		{Key: "cub.repository.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: repoName}}},
	}
	spanAttrs := []*commonpb.KeyValue{
		{Key: "cub.task.id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: taskID}}},
		{Key: "gen_ai.request.model", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: model}}},
		{Key: "gen_ai.usage.input_tokens", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: tokensIn}}},
		{Key: "gen_ai.usage.output_tokens", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: tokensOut}}},
	}
	return &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{Attributes: resAttrs},
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{{
					TraceId:            []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
					SpanId:             []byte{1, 2, 3, 4, 5, 6, 7, 8},
					ParentSpanId:       []byte{8, 7, 6, 5, 4, 3, 2, 1},
					Name:               "llm.call",
					Kind:               tracepb.Span_SPAN_KIND_CLIENT,
					StartTimeUnixNano:  1000000000,
					EndTimeUnixNano:    2000000000,
					Status:             &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK, Message: "ok"},
					Attributes:         spanAttrs,
				}},
			}},
		}},
	}
}

func TestProtobufToSpan(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	req := makeTraceRequest("Lark", "task-42", "procyon-park", "claude-opus-4", 1500, 800)
	p.handleSignal(Signal{Type: SignalTraces, Payload: req})

	var (
		traceID, spanID, parentID, name string
		kind                            int32
		startNs, endNs, durationNs      int64
		statusCode                      int32
		statusMsg                        string
		agentName, taskID, repoName     string
		modelName                       string
		tokensIn, tokensOut             int64
		cost                            float64
	)
	err = p.DB().QueryRow("SELECT * FROM otel_spans").Scan(
		&traceID, &spanID, &parentID, &name, &kind,
		&startNs, &endNs, &durationNs,
		&statusCode, &statusMsg,
		&agentName, &taskID, &repoName,
		&modelName, &tokensIn, &tokensOut, &cost,
	)
	if err != nil {
		t.Fatalf("query span: %v", err)
	}

	if traceID != "0102030405060708090a0b0c0d0e0f10" {
		t.Errorf("trace_id = %q", traceID)
	}
	if spanID != "0102030405060708" {
		t.Errorf("span_id = %q", spanID)
	}
	if parentID != "0807060504030201" {
		t.Errorf("parent_span_id = %q", parentID)
	}
	if name != "llm.call" {
		t.Errorf("name = %q", name)
	}
	if kind != 3 { // CLIENT
		t.Errorf("kind = %d, want 3", kind)
	}
	if durationNs != 1000000000 {
		t.Errorf("duration_ns = %d, want 1000000000", durationNs)
	}
	if statusCode != 1 { // OK
		t.Errorf("status_code = %d, want 1", statusCode)
	}
	if agentName != "Lark" {
		t.Errorf("agent_name = %q", agentName)
	}
	if taskID != "task-42" {
		t.Errorf("task_id = %q", taskID)
	}
	if repoName != "procyon-park" {
		t.Errorf("repo_name = %q", repoName)
	}
	if modelName != "claude-opus-4" {
		t.Errorf("model_name = %q", modelName)
	}
	if tokensIn != 1500 {
		t.Errorf("tokens_in = %d", tokensIn)
	}
	if tokensOut != 800 {
		t.Errorf("tokens_out = %d", tokensOut)
	}
}

func TestProtobufToMetric(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	resAttrs := []*commonpb.KeyValue{
		{Key: "cub.agent.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "Cog"}}},
	}

	// Gauge metric
	gaugeReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource: &resourcepb.Resource{Attributes: resAttrs},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{{
					Name:        "llm.token_count",
					Description: "tokens used",
					Unit:        "tokens",
					Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
						DataPoints: []*metricspb.NumberDataPoint{{
							TimeUnixNano: 5000000000,
							Value:        &metricspb.NumberDataPoint_AsInt{AsInt: 42},
						}},
					}},
				}},
			}},
		}},
	}
	p.handleSignal(Signal{Type: SignalMetrics, Payload: gaugeReq})

	var (
		mName, mDesc, mUnit, mType string
		timeNs, startTimeNs        int64
		agentName, taskID, repoName string
		valInt                      sql.NullInt64
		valDouble                   sql.NullFloat64
		isMonotonic                 sql.NullBool
		aggTemp                     sql.NullInt32
		histCount                   sql.NullInt64
		histSum, histMin, histMax   sql.NullFloat64
		bucketCounts, explBounds    sql.NullString
	)
	err = p.DB().QueryRow("SELECT * FROM otel_metrics").Scan(
		&mName, &mDesc, &mUnit, &mType,
		&timeNs, &startTimeNs,
		&agentName, &taskID, &repoName,
		&valInt, &valDouble,
		&isMonotonic, &aggTemp,
		&histCount, &histSum, &histMin, &histMax,
		&bucketCounts, &explBounds,
	)
	if err != nil {
		t.Fatalf("query metric: %v", err)
	}
	if mName != "llm.token_count" {
		t.Errorf("name = %q", mName)
	}
	if mType != "gauge" {
		t.Errorf("metric_type = %q", mType)
	}
	if !valInt.Valid || valInt.Int64 != 42 {
		t.Errorf("value_int = %v", valInt)
	}
	if agentName != "Cog" {
		t.Errorf("agent_name = %q", agentName)
	}
}

func TestProtobufToLog(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	resAttrs := []*commonpb.KeyValue{
		{Key: "cub.agent.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "Fizz"}}},
		{Key: "cub.task.id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "task-99"}}},
		{Key: "cub.repository.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "my-repo"}}},
	}
	logReq := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource: &resourcepb.Resource{Attributes: resAttrs},
			ScopeLogs: []*logspb.ScopeLogs{{
				LogRecords: []*logspb.LogRecord{{
					TimeUnixNano:         3000000000,
					ObservedTimeUnixNano: 3000000001,
					SeverityNumber:       logspb.SeverityNumber_SEVERITY_NUMBER_ERROR,
					SeverityText:         "ERROR",
					Body:                 &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "something broke"}},
					TraceId:              []byte{0xAA, 0xBB, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
					SpanId:               []byte{0xCC, 0xDD, 0, 0, 0, 0, 0, 1},
				}},
			}},
		}},
	}
	p.handleSignal(Signal{Type: SignalLogs, Payload: logReq})

	var (
		timeNs, observedTimeNs int64
		sevNum                 int32
		sevText, body          string
		traceID, spanID        string
		agentName, taskID, repoName string
	)
	err = p.DB().QueryRow("SELECT * FROM otel_logs").Scan(
		&timeNs, &observedTimeNs, &sevNum, &sevText, &body,
		&traceID, &spanID,
		&agentName, &taskID, &repoName,
	)
	if err != nil {
		t.Fatalf("query log: %v", err)
	}
	if sevNum != 17 { // ERROR
		t.Errorf("severity_number = %d, want 17", sevNum)
	}
	if sevText != "ERROR" {
		t.Errorf("severity_text = %q", sevText)
	}
	if body != "something broke" {
		t.Errorf("body = %q", body)
	}
	if traceID != "aabb0000000000000000000000000001" {
		t.Errorf("trace_id = %q, want aabb0000000000000000000000000001", traceID)
	}
	if agentName != "Fizz" {
		t.Errorf("agent_name = %q", agentName)
	}
	if taskID != "task-99" {
		t.Errorf("task_id = %q", taskID)
	}
	if repoName != "my-repo" {
		t.Errorf("repo_name = %q", repoName)
	}
}

// ---------------------------------------------------------------------------
// Consume loop
// ---------------------------------------------------------------------------

func TestConsumeLoop(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	r := NewReceiver(ReceiverConfig{GRPCAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.ConsumeLoop(ctx, r)
		close(done)
	}()

	// Send a trace signal.
	req := makeTraceRequest("Loop", "task-loop", "repo-loop", "haiku", 100, 50)
	r.Traces <- Signal{Type: SignalTraces, Payload: req}

	// Send a log signal.
	logReq := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource: &resourcepb.Resource{},
			ScopeLogs: []*logspb.ScopeLogs{{
				LogRecords: []*logspb.LogRecord{{
					TimeUnixNano:         1,
					ObservedTimeUnixNano: 2,
					SeverityText:         "INFO",
					Body:                 &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "hello"}},
				}},
			}},
		}},
	}
	r.Logs <- Signal{Type: SignalLogs, Payload: logReq}

	// Give the loop time to process both signals.
	time.Sleep(100 * time.Millisecond)

	cancel()
	<-done

	// Verify spans were inserted.
	var spanCount int
	if err := p.DB().QueryRow("SELECT COUNT(*) FROM otel_spans").Scan(&spanCount); err != nil {
		t.Fatalf("count spans: %v", err)
	}
	if spanCount != 1 {
		t.Errorf("span count = %d, want 1", spanCount)
	}

	// Verify logs were inserted.
	var logCount int
	if err := p.DB().QueryRow("SELECT COUNT(*) FROM otel_logs").Scan(&logCount); err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if logCount != 1 {
		t.Errorf("log count = %d, want 1", logCount)
	}
}

// ---------------------------------------------------------------------------
// DuckDB queries on inserted data
// ---------------------------------------------------------------------------

func TestDuckDBQueryAggregation(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	// Insert multiple spans with different agents.
	agents := []struct {
		name  string
		model string
		cost  float64
	}{
		{"Lark", "opus", 0.05},
		{"Lark", "opus", 0.03},
		{"Cog", "haiku", 0.01},
	}
	for i, a := range agents {
		resAttrs := []*commonpb.KeyValue{
			{Key: "cub.agent.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: a.name}}},
			{Key: "gen_ai.request.model", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: a.model}}},
			{Key: "gen_ai.usage.cost", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: a.cost}}},
		}
		req := &coltracepb.ExportTraceServiceRequest{
			ResourceSpans: []*tracepb.ResourceSpans{{
				Resource: &resourcepb.Resource{Attributes: resAttrs},
				ScopeSpans: []*tracepb.ScopeSpans{{
					Spans: []*tracepb.Span{{
						TraceId:           []byte{byte(i + 1), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						SpanId:            []byte{byte(i + 1), 0, 0, 0, 0, 0, 0, 0},
						Name:              "work",
						StartTimeUnixNano: uint64(i * 1000000000),
						EndTimeUnixNano:   uint64((i + 1) * 1000000000),
					}},
				}},
			}},
		}
		p.handleSignal(Signal{Type: SignalTraces, Payload: req})
	}

	// Aggregate by agent.
	rows, err := p.DB().Query(`
		SELECT agent_name, COUNT(*) AS span_count, SUM(cost) AS total_cost
		FROM otel_spans
		GROUP BY agent_name
		ORDER BY agent_name
	`)
	if err != nil {
		t.Fatalf("aggregate query: %v", err)
	}
	defer rows.Close()

	type result struct {
		agent string
		count int
		cost  float64
	}
	var results []result
	for rows.Next() {
		var r result
		if err := rows.Scan(&r.agent, &r.count, &r.cost); err != nil {
			t.Fatalf("scan: %v", err)
		}
		results = append(results, r)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(results))
	}
	if results[0].agent != "Cog" || results[0].count != 1 {
		t.Errorf("Cog: %+v", results[0])
	}
	if results[1].agent != "Lark" || results[1].count != 2 {
		t.Errorf("Lark: %+v", results[1])
	}
	// Approximate float comparison.
	if results[1].cost < 0.079 || results[1].cost > 0.081 {
		t.Errorf("Lark total cost = %f, want ~0.08", results[1].cost)
	}
}

func TestHistogramMetric(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	sum := 150.5
	min := 10.0
	max := 50.0
	histReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{{
					Name: "llm.latency",
					Unit: "ms",
					Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
						AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
						DataPoints: []*metricspb.HistogramDataPoint{{
							TimeUnixNano:   9000000000,
							Count:          5,
							Sum:            &sum,
							Min:            &min,
							Max:            &max,
							BucketCounts:   []uint64{1, 2, 1, 1},
							ExplicitBounds: []float64{10, 25, 50},
						}},
					}},
				}},
			}},
		}},
	}
	p.handleSignal(Signal{Type: SignalMetrics, Payload: histReq})

	var (
		mType        string
		histCount    sql.NullInt64
		histSum      sql.NullFloat64
		bucketCounts sql.NullString
	)
	err = p.DB().QueryRow("SELECT metric_type, histogram_count, histogram_sum, bucket_counts FROM otel_metrics").Scan(
		&mType, &histCount, &histSum, &bucketCounts,
	)
	if err != nil {
		t.Fatalf("query histogram: %v", err)
	}
	if mType != "histogram" {
		t.Errorf("metric_type = %q", mType)
	}
	if !histCount.Valid || histCount.Int64 != 5 {
		t.Errorf("histogram_count = %v", histCount)
	}
	if !histSum.Valid || histSum.Float64 != 150.5 {
		t.Errorf("histogram_sum = %v", histSum)
	}
	if !bucketCounts.Valid || bucketCounts.String != "[1,2,1,1]" {
		t.Errorf("bucket_counts = %q", bucketCounts.String)
	}
}

// ---------------------------------------------------------------------------
// Attribute merging (resource + span)
// ---------------------------------------------------------------------------

func TestAttrMerge(t *testing.T) {
	a := attrSet{agentName: "Lark", repoName: "repo"}
	b := attrSet{agentName: "Override", taskID: "t1"}
	merged := a.merge(b)

	if merged.agentName != "Override" {
		t.Errorf("agentName = %q, want Override", merged.agentName)
	}
	if merged.taskID != "t1" {
		t.Errorf("taskID = %q, want t1", merged.taskID)
	}
	if merged.repoName != "repo" {
		t.Errorf("repoName = %q, want repo", merged.repoName)
	}
}
