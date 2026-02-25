package telemetry

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------------------------
// 1. SDLC event roundtrip: save → query → verify
// ---------------------------------------------------------------------------

func TestIntegration_SDLCEventRoundtrip(t *testing.T) {
	store, err := NewMemoryStore()
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	defer store.Close()

	// Save events across multiple categories.
	events := []*TelemetryEvent{
		NewEvent(CategoryAgent, "agent.spawn", "king", "node-1").WithTarget("Juniper").WithData(map[string]string{"branch": "feature/x"}),
		NewEvent(CategoryTask, "task.start", "Juniper", "node-1").WithTarget("task-42"),
		NewEvent(CategoryGit, "git.commit", "Juniper", "node-1").WithData(map[string]string{"sha": "abc123"}),
		NewEvent(CategorySession, "session.begin", "daemon", "node-1"),
		NewEvent(CategoryError, "error.panic", "Juniper", "node-1").WithData(map[string]string{"msg": "nil pointer"}),
	}

	for _, ev := range events {
		if err := store.SaveEvent(ev); err != nil {
			t.Fatalf("SaveEvent(%s): %v", ev.EventType, err)
		}
	}

	// Verify count.
	count, err := store.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != len(events) {
		t.Fatalf("count = %d, want %d", count, len(events))
	}

	// Query back each event by ID and verify round-trip.
	for _, want := range events {
		got, err := store.GetEvent(want.ID)
		if err != nil {
			t.Errorf("GetEvent(%s): %v", want.ID, err)
			continue
		}
		if got.EventType != want.EventType {
			t.Errorf("EventType = %q, want %q", got.EventType, want.EventType)
		}
		if got.Category != want.Category {
			t.Errorf("Category = %q, want %q", got.Category, want.Category)
		}
		if got.Source != want.Source {
			t.Errorf("Source = %q, want %q", got.Source, want.Source)
		}
		if got.Target != want.Target {
			t.Errorf("Target = %q, want %q", got.Target, want.Target)
		}
		if got.NodeID != want.NodeID {
			t.Errorf("NodeID = %q, want %q", got.NodeID, want.NodeID)
		}
	}

	// Filter by category.
	agentEvents, err := store.ListEvents(ListOptions{Category: CategoryAgent})
	if err != nil {
		t.Fatalf("ListEvents(agent): %v", err)
	}
	if len(agentEvents) != 1 {
		t.Errorf("agent events = %d, want 1", len(agentEvents))
	}

	// Filter by source.
	juniperEvents, err := store.ListEvents(ListOptions{Source: "Juniper"})
	if err != nil {
		t.Fatalf("ListEvents(source=Juniper): %v", err)
	}
	if len(juniperEvents) != 3 {
		t.Errorf("Juniper events = %d, want 3", len(juniperEvents))
	}
}

// ---------------------------------------------------------------------------
// 2. OTLP ingestion roundtrip: gRPC → Receiver → Pipeline → DuckDB → Query
// ---------------------------------------------------------------------------

func TestIntegration_OTLPIngestionRoundtrip(t *testing.T) {
	// Start receiver on ephemeral ports.
	recv := NewReceiver(ReceiverConfig{GRPCAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0"})
	if err := recv.Start(); err != nil {
		t.Fatalf("receiver.Start: %v", err)
	}
	defer recv.Stop()

	// Start pipeline and consume loop.
	pipeline, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer pipeline.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pipeline.ConsumeLoop(ctx, recv)

	// --- Send traces via gRPC ---
	conn, err := grpc.NewClient(recv.GRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()

	traceClient := coltracepb.NewTraceServiceClient(conn)
	traceReq := makeTraceRequest("Juniper", "task-99", "procyon-park", "opus", 500, 200)
	grpcCtx, grpcCancel := context.WithTimeout(ctx, 2*time.Second)
	defer grpcCancel()
	if _, err := traceClient.Export(grpcCtx, traceReq); err != nil {
		t.Fatalf("Export traces: %v", err)
	}

	// --- Send metrics via gRPC ---
	metricClient := colmetricspb.NewMetricsServiceClient(conn)
	metricReq := makeGaugeMetricRequest("Juniper", 42)
	if _, err := metricClient.Export(grpcCtx, metricReq); err != nil {
		t.Fatalf("Export metrics: %v", err)
	}

	// --- Send logs via HTTP ---
	logReq := makeLogRequest("Juniper", "integration test log")
	logBytes, err := proto.Marshal(logReq)
	if err != nil {
		t.Fatalf("marshal log: %v", err)
	}
	httpResp, err := postProto("http://"+recv.HTTPAddr()+"/v1/logs", logBytes)
	if err != nil {
		t.Fatalf("POST /v1/logs: %v", err)
	}
	if httpResp.StatusCode != 200 {
		t.Fatalf("POST /v1/logs status = %d, want 200", httpResp.StatusCode)
	}
	httpResp.Body.Close()

	// Wait for pipeline to process.
	waitForCount(t, pipeline, "otel_spans", 1, 3*time.Second)
	waitForCount(t, pipeline, "otel_metrics", 1, 3*time.Second)
	waitForCount(t, pipeline, "otel_logs", 1, 3*time.Second)

	// Query via QueryEngine.
	warmDir := t.TempDir()
	qe := NewQueryEngine(pipeline, warmDir)

	// Search spans.
	spans, err := qe.SearchSpans(SpanSearchParams{AgentName: "Juniper"})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	if spans[0].AgentName != "Juniper" {
		t.Errorf("agent_name = %q, want Juniper", spans[0].AgentName)
	}
	if spans[0].TaskID != "task-99" {
		t.Errorf("task_id = %q, want task-99", spans[0].TaskID)
	}
	if spans[0].ModelName != "opus" {
		t.Errorf("model_name = %q, want opus", spans[0].ModelName)
	}
	if spans[0].TokensIn != 500 {
		t.Errorf("tokens_in = %d, want 500", spans[0].TokensIn)
	}

	// Search logs.
	logs, err := qe.SearchLogs(LogSearchParams{Body: "integration"})
	if err != nil {
		t.Fatalf("SearchLogs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(logs))
	}
	if logs[0].AgentName != "Juniper" {
		t.Errorf("log agent_name = %q, want Juniper", logs[0].AgentName)
	}

	// Aggregate metrics.
	results, err := qe.AggregateMetrics(MetricAggregateParams{
		MetricName: "test.gauge",
		Function:   "SUM",
		ValueField: "value_int",
	})
	if err != nil {
		t.Fatalf("AggregateMetrics: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("metric results = %d, want 1", len(results))
	}
	if results[0].Value != 42 {
		t.Errorf("metric value = %f, want 42", results[0].Value)
	}
}

// ---------------------------------------------------------------------------
// 3. Flush-and-query: ingest → flush → verify Parquet → query unified
// ---------------------------------------------------------------------------

func TestIntegration_FlushAndQuery(t *testing.T) {
	pipeline, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer pipeline.Close()

	warmDir := t.TempDir()
	flusher := NewFlusher(pipeline, FlushConfig{WarmDir: warmDir})

	// Insert data across all three signal types.
	pipeline.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("Alpha", "t1", "repo-a", "sonnet", 100, 50)})
	pipeline.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("Beta", "t2", "repo-b", "haiku", 200, 100)})
	pipeline.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("Alpha", 10)})
	pipeline.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("Beta", 20)})
	pipeline.handleSignal(Signal{Type: SignalLogs, Payload: makeLogRequest("Alpha", "alpha log")})

	// Flush to Parquet (January partition).
	flushTime := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	if err := flusher.FlushAt(flushTime); err != nil {
		t.Fatalf("FlushAt: %v", err)
	}

	// Verify Parquet files.
	partDir := filepath.Join(warmDir, "2026-01")
	for _, stem := range []string{"spans", "metrics", "logs"} {
		path := filepath.Join(partDir, stem+".parquet")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
		if info.Size() == 0 {
			t.Fatalf("expected %s to be non-empty", path)
		}
	}

	// Hot tier should be empty after flush.
	assertTableCount(t, pipeline, "otel_spans", 0)
	assertTableCount(t, pipeline, "otel_metrics", 0)
	assertTableCount(t, pipeline, "otel_logs", 0)

	// Insert new data (hot tier).
	pipeline.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("Gamma", "t3", "repo-c", "opus", 300, 150)})

	// Query via unified engine — should see warm + hot data.
	qe := NewQueryEngine(pipeline, warmDir)

	allSpans, err := qe.SearchSpans(SpanSearchParams{Limit: 100})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	if len(allSpans) != 3 {
		t.Fatalf("total spans = %d, want 3 (2 warm + 1 hot)", len(allSpans))
	}

	// Verify we can query warm-only data.
	alphaSpans, err := qe.SearchSpans(SpanSearchParams{AgentName: "Alpha"})
	if err != nil {
		t.Fatalf("SearchSpans(Alpha): %v", err)
	}
	if len(alphaSpans) != 1 {
		t.Errorf("Alpha spans = %d, want 1", len(alphaSpans))
	}

	// Verify hot-only data.
	gammaSpans, err := qe.SearchSpans(SpanSearchParams{AgentName: "Gamma"})
	if err != nil {
		t.Fatalf("SearchSpans(Gamma): %v", err)
	}
	if len(gammaSpans) != 1 {
		t.Errorf("Gamma spans = %d, want 1", len(gammaSpans))
	}

	// Aggregate metrics across warm tier.
	metricResults, err := qe.AggregateMetrics(MetricAggregateParams{
		MetricName: "test.gauge",
		Function:   "SUM",
		ValueField: "value_int",
	})
	if err != nil {
		t.Fatalf("AggregateMetrics: %v", err)
	}
	if len(metricResults) != 1 || metricResults[0].Value != 30 {
		t.Errorf("metric SUM = %v, want 30", metricResults)
	}

	// Search logs.
	allLogs, err := qe.SearchLogs(LogSearchParams{})
	if err != nil {
		t.Fatalf("SearchLogs: %v", err)
	}
	if len(allLogs) != 1 {
		t.Errorf("logs = %d, want 1", len(allLogs))
	}

	// HasWarmData and HasHotData checks.
	if !qe.HasWarmData() {
		t.Error("HasWarmData should be true after flush")
	}
	if !qe.HasHotData() {
		t.Error("HasHotData should be true (Gamma in hot tier)")
	}
}

// ---------------------------------------------------------------------------
// 4. Maggie integration: primitives → OTLP → pipeline → query
// ---------------------------------------------------------------------------

// TestIntegration_MaggiePrimitives tests that the Maggie VM OTEL primitives
// produce real OTLP data that flows through the receiver into the pipeline.
// We use NewProvider (not test provider) pointed at our ephemeral receiver.
func TestIntegration_MaggiePrimitives(t *testing.T) {
	// Start receiver on ephemeral ports.
	recv := NewReceiver(ReceiverConfig{GRPCAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0"})
	if err := recv.Start(); err != nil {
		t.Fatalf("receiver.Start: %v", err)
	}
	defer recv.Stop()

	// Start pipeline consume loop.
	pipeline, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer pipeline.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pipeline.ConsumeLoop(ctx, recv)

	// Create a real provider pointing at the receiver.
	provCtx, provCancel := context.WithTimeout(ctx, 5*time.Second)
	defer provCancel()
	provider, err := NewProvider(provCtx, ProviderConfig{
		Endpoint:       recv.GRPCAddr(),
		ServiceName:    "maggie-integration-test",
		AgentName:      "TestAgent",
		RepositoryName: "test-repo",
		TaskID:         "task-integration",
		ProcessName:    "test-process",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	// Create a span (simulating what Maggie VM would do via span:do:).
	spanCtx, span := provider.tracer.Start(provider.currentCtx(), "maggie.test.span")
	provider.pushCtx(spanCtx)
	span.End()
	provider.popCtx()

	// Shutdown to flush all SDK exporters.
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	if err := provider.Shutdown(shutCtx); err != nil {
		t.Fatalf("provider.Shutdown: %v", err)
	}

	// Wait for the span to appear in the pipeline.
	waitForCount(t, pipeline, "otel_spans", 1, 5*time.Second)

	// Query and verify the span has correct resource attributes.
	warmDir := t.TempDir()
	qe := NewQueryEngine(pipeline, warmDir)
	spans, err := qe.SearchSpans(SpanSearchParams{AgentName: "TestAgent"})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	if len(spans) == 0 {
		t.Fatal("no spans found for agent TestAgent")
	}
	s := spans[0]
	if s.Name != "maggie.test.span" {
		t.Errorf("span name = %q, want maggie.test.span", s.Name)
	}
	if s.RepoName != "test-repo" {
		t.Errorf("repo_name = %q, want test-repo", s.RepoName)
	}
	if s.TaskID != "task-integration" {
		t.Errorf("task_id = %q, want task-integration", s.TaskID)
	}
}

// ---------------------------------------------------------------------------
// 5. CLI roundtrip: HandleQuery dispatches correctly for all operations
// ---------------------------------------------------------------------------

func TestIntegration_CLIQueryRoundtrip(t *testing.T) {
	pipeline, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer pipeline.Close()

	warmDir := t.TempDir()

	// Insert test data.
	traceReq := makeTraceRequestWithIDs(
		"CLI-Agent", "cli-task", "cli-repo", "opus", 100, 50,
		[]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0x00},
		[]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88},
	)
	pipeline.handleSignal(Signal{Type: SignalTraces, Payload: traceReq})
	pipeline.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("CLI-Agent", 77)})
	pipeline.handleSignal(Signal{Type: SignalLogs, Payload: makeLogRequest("CLI-Agent", "cli log message")})

	qe := NewQueryEngine(pipeline, warmDir)

	// --- lookup_trace ---
	traceResult, err := qe.HandleQuery(QueryCommand{
		Operation: "lookup_trace",
		TraceID:   "aabbccddeeff11223344556677889900",
	})
	if err != nil {
		t.Fatalf("HandleQuery(lookup_trace): %v", err)
	}
	if len(traceResult.Spans) != 1 {
		t.Fatalf("lookup_trace spans = %d, want 1", len(traceResult.Spans))
	}
	if traceResult.Spans[0].AgentName != "CLI-Agent" {
		t.Errorf("lookup_trace agent = %q, want CLI-Agent", traceResult.Spans[0].AgentName)
	}

	// --- search_spans ---
	spanResult, err := qe.HandleQuery(QueryCommand{
		Operation: "search_spans",
		Spans:     &SpanSearchParams{AgentName: "CLI-Agent"},
	})
	if err != nil {
		t.Fatalf("HandleQuery(search_spans): %v", err)
	}
	if len(spanResult.Spans) != 1 {
		t.Fatalf("search_spans = %d, want 1", len(spanResult.Spans))
	}

	// --- aggregate_metrics ---
	metricResult, err := qe.HandleQuery(QueryCommand{
		Operation: "aggregate_metrics",
		Metrics: &MetricAggregateParams{
			MetricName: "test.gauge",
			Function:   "SUM",
			ValueField: "value_int",
		},
	})
	if err != nil {
		t.Fatalf("HandleQuery(aggregate_metrics): %v", err)
	}
	if len(metricResult.Metrics) != 1 || metricResult.Metrics[0].Value != 77 {
		t.Errorf("aggregate value = %v, want 77", metricResult.Metrics)
	}

	// --- search_logs ---
	logResult, err := qe.HandleQuery(QueryCommand{
		Operation: "search_logs",
		Logs:      &LogSearchParams{Body: "cli log"},
	})
	if err != nil {
		t.Fatalf("HandleQuery(search_logs): %v", err)
	}
	if len(logResult.Logs) != 1 {
		t.Fatalf("search_logs = %d, want 1", len(logResult.Logs))
	}
	if logResult.Logs[0].Body != "cli log message" {
		t.Errorf("log body = %q, want %q", logResult.Logs[0].Body, "cli log message")
	}

	// --- unknown operation ---
	_, err = qe.HandleQuery(QueryCommand{Operation: "invalid_op"})
	if err == nil {
		t.Error("expected error for invalid operation")
	}
}

// ---------------------------------------------------------------------------
// 6. Retention: verify event cleanup after TTL
// ---------------------------------------------------------------------------

func TestIntegration_Retention(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "telem.db"), Config{RetentionDays: 7})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	now := time.Now()

	// Insert events at various ages.
	oldEvent := NewEvent(CategoryAgent, "agent.spawn", "king", "node-1")
	oldEvent.Timestamp = now.AddDate(0, 0, -10) // 10 days ago — beyond 7-day retention
	if err := store.SaveEvent(oldEvent); err != nil {
		t.Fatalf("SaveEvent(old): %v", err)
	}

	recentEvent := NewEvent(CategoryAgent, "agent.spawn", "king", "node-1")
	recentEvent.Timestamp = now.AddDate(0, 0, -3) // 3 days ago — within retention
	if err := store.SaveEvent(recentEvent); err != nil {
		t.Fatalf("SaveEvent(recent): %v", err)
	}

	currentEvent := NewEvent(CategoryTask, "task.done", "Juniper", "node-1")
	// currentEvent.Timestamp is now — within retention
	if err := store.SaveEvent(currentEvent); err != nil {
		t.Fatalf("SaveEvent(current): %v", err)
	}

	// Verify all 3 exist before pruning.
	count, _ := store.Count()
	if count != 3 {
		t.Fatalf("pre-prune count = %d, want 3", count)
	}

	// Prune based on retention.
	pruned, err := store.PruneEvents()
	if err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned = %d, want 1 (the 10-day-old event)", pruned)
	}

	// Verify remaining events.
	count, _ = store.Count()
	if count != 2 {
		t.Errorf("post-prune count = %d, want 2", count)
	}

	// The old event should be gone.
	_, err = store.GetEvent(oldEvent.ID)
	if err != ErrEventNotFound {
		t.Errorf("expected ErrEventNotFound for pruned event, got %v", err)
	}

	// Recent and current should still exist.
	if _, err := store.GetEvent(recentEvent.ID); err != nil {
		t.Errorf("recentEvent should still exist: %v", err)
	}
	if _, err := store.GetEvent(currentEvent.ID); err != nil {
		t.Errorf("currentEvent should still exist: %v", err)
	}

	// PruneEventsBefore with a future cutoff removes everything.
	pruned2, err := store.PruneEventsBefore(now.Add(1 * time.Hour))
	if err != nil {
		t.Fatalf("PruneEventsBefore: %v", err)
	}
	if pruned2 != 2 {
		t.Errorf("pruned2 = %d, want 2", pruned2)
	}
	count, _ = store.Count()
	if count != 0 {
		t.Errorf("final count = %d, want 0", count)
	}
}

// ---------------------------------------------------------------------------
// 7. Crash recovery: flush exports before purge (invariant test)
// ---------------------------------------------------------------------------

func TestIntegration_CrashRecovery(t *testing.T) {
	// This test verifies the export-then-purge invariant: even if we
	// "crash" (simulate by not doing anything after flush), the data is
	// recoverable from Parquet. We verify the invariant by checking that
	// Parquet contains the data and the in-memory store is empty.
	pipeline, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer pipeline.Close()

	warmDir := t.TempDir()
	flusher := NewFlusher(pipeline, FlushConfig{WarmDir: warmDir})

	// Insert data across all three signal types.
	for i := 0; i < 5; i++ {
		pipeline.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("CrashAgent", "crash-task", "crash-repo", "model", int64(i*10), int64(i*5))})
	}
	pipeline.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("CrashAgent", 99)})
	pipeline.handleSignal(Signal{Type: SignalLogs, Payload: makeLogRequest("CrashAgent", "crash test log")})

	// Verify data is in memory.
	assertTableCount(t, pipeline, "otel_spans", 5)
	assertTableCount(t, pipeline, "otel_metrics", 1)
	assertTableCount(t, pipeline, "otel_logs", 1)

	// Flush to Parquet.
	flushTime := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	if err := flusher.FlushAt(flushTime); err != nil {
		t.Fatalf("FlushAt: %v", err)
	}

	// In-memory is now empty (purge happened).
	assertTableCount(t, pipeline, "otel_spans", 0)
	assertTableCount(t, pipeline, "otel_metrics", 0)
	assertTableCount(t, pipeline, "otel_logs", 0)

	// Parquet files exist.
	partDir := filepath.Join(warmDir, "2026-04")
	for _, stem := range []string{"spans", "metrics", "logs"} {
		path := filepath.Join(partDir, stem+".parquet")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}

	// Data is fully recoverable from Parquet via DuckDB read.
	var spanCount int
	err = pipeline.DB().QueryRow(
		"SELECT COUNT(*) FROM read_parquet('" + filepath.Join(partDir, "spans.parquet") + "')",
	).Scan(&spanCount)
	if err != nil {
		t.Fatalf("read_parquet spans: %v", err)
	}
	if spanCount != 5 {
		t.Errorf("recovered span count = %d, want 5", spanCount)
	}

	var metricCount int
	err = pipeline.DB().QueryRow(
		"SELECT COUNT(*) FROM read_parquet('" + filepath.Join(partDir, "metrics.parquet") + "')",
	).Scan(&metricCount)
	if err != nil {
		t.Fatalf("read_parquet metrics: %v", err)
	}
	if metricCount != 1 {
		t.Errorf("recovered metric count = %d, want 1", metricCount)
	}

	var logCount int
	err = pipeline.DB().QueryRow(
		"SELECT COUNT(*) FROM read_parquet('" + filepath.Join(partDir, "logs.parquet") + "')",
	).Scan(&logCount)
	if err != nil {
		t.Fatalf("read_parquet logs: %v", err)
	}
	if logCount != 1 {
		t.Errorf("recovered log count = %d, want 1", logCount)
	}

	// Verify data integrity: check agent names survived roundtrip.
	var agent string
	err = pipeline.DB().QueryRow(
		"SELECT agent_name FROM read_parquet('" + filepath.Join(partDir, "spans.parquet") + "') LIMIT 1",
	).Scan(&agent)
	if err != nil {
		t.Fatalf("read agent: %v", err)
	}
	if agent != "CrashAgent" {
		t.Errorf("agent = %q, want CrashAgent", agent)
	}
}

// ---------------------------------------------------------------------------
// 8. Standalone query: Parquet-only queries without running pipeline
// ---------------------------------------------------------------------------

func TestIntegration_StandaloneQuery(t *testing.T) {
	// First, create Parquet data using a temporary pipeline + flusher.
	pipeline, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	warmDir := t.TempDir()
	flusher := NewFlusher(pipeline, FlushConfig{WarmDir: warmDir})

	// Insert data across two partitions.
	pipeline.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("Agent-Jan", "task-jan", "repo", "opus", 100, 50)})
	pipeline.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("Agent-Jan", 25)})
	pipeline.handleSignal(Signal{Type: SignalLogs, Payload: makeLogRequest("Agent-Jan", "january log")})
	if err := flusher.FlushAt(time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("FlushAt(jan): %v", err)
	}

	pipeline.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("Agent-Mar", "task-mar", "repo", "haiku", 200, 100)})
	pipeline.handleSignal(Signal{Type: SignalMetrics, Payload: makeGaugeMetricRequest("Agent-Mar", 75)})
	pipeline.handleSignal(Signal{Type: SignalLogs, Payload: makeLogRequest("Agent-Mar", "march log")})
	if err := flusher.FlushAt(time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("FlushAt(mar): %v", err)
	}

	// Close the pipeline — standalone mode has no running pipeline.
	pipeline.Close()

	// Create standalone query engine (Parquet only, no pipeline).
	sqe, err := NewStandaloneQueryEngine(warmDir)
	if err != nil {
		t.Fatalf("NewStandaloneQueryEngine: %v", err)
	}
	defer sqe.Close()

	// HasWarmData should be true.
	if !sqe.HasWarmData() {
		t.Error("HasWarmData should be true")
	}
	// HasHotData should be false (no pipeline).
	if sqe.HasHotData() {
		t.Error("HasHotData should be false in standalone mode")
	}

	// Query all spans across partitions.
	allSpans, err := sqe.SearchSpans(SpanSearchParams{Limit: 100})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	if len(allSpans) != 2 {
		t.Fatalf("spans = %d, want 2 (across 2 partitions)", len(allSpans))
	}

	// Filter by specific agent.
	janSpans, err := sqe.SearchSpans(SpanSearchParams{AgentName: "Agent-Jan"})
	if err != nil {
		t.Fatalf("SearchSpans(Agent-Jan): %v", err)
	}
	if len(janSpans) != 1 {
		t.Errorf("Jan spans = %d, want 1", len(janSpans))
	}

	// Aggregate metrics across partitions.
	metricResults, err := sqe.AggregateMetrics(MetricAggregateParams{
		MetricName: "test.gauge",
		Function:   "SUM",
		ValueField: "value_int",
	})
	if err != nil {
		t.Fatalf("AggregateMetrics: %v", err)
	}
	if len(metricResults) != 1 || metricResults[0].Value != 100 {
		t.Errorf("metric SUM = %v, want 100 (25+75)", metricResults)
	}

	// Search logs.
	allLogs, err := sqe.SearchLogs(LogSearchParams{})
	if err != nil {
		t.Fatalf("SearchLogs: %v", err)
	}
	if len(allLogs) != 2 {
		t.Errorf("logs = %d, want 2", len(allLogs))
	}

	// HandleQuery dispatch works in standalone mode.
	result, err := sqe.HandleQuery(QueryCommand{
		Operation: "search_spans",
		Spans:     &SpanSearchParams{AgentName: "Agent-Mar"},
	})
	if err != nil {
		t.Fatalf("HandleQuery: %v", err)
	}
	if len(result.Spans) != 1 {
		t.Errorf("HandleQuery spans = %d, want 1", len(result.Spans))
	}
	if result.Spans[0].ModelName != "haiku" {
		t.Errorf("model = %q, want haiku", result.Spans[0].ModelName)
	}
}

// ---------------------------------------------------------------------------
// Integration test helpers
// ---------------------------------------------------------------------------

// postProto sends a POST request with protobuf content type.
func postProto(url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	return http.DefaultClient.Do(req)
}

// waitForCount polls a DuckDB table until it has the expected count or timeout.
func waitForCount(t *testing.T, p *Pipeline, table string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var count int
		if err := p.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err == nil && count >= want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	var count int
	p.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
	t.Fatalf("timeout waiting for %s count >= %d (got %d)", table, want, count)
}

// assertTableCount verifies the row count of a DuckDB table.
func assertTableCount(t *testing.T, p *Pipeline, table string, want int) {
	t.Helper()
	var count int
	if err := p.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s count = %d, want %d", table, count, want)
	}
}

// makeTraceRequestWithIDs creates a trace request with specific trace and span IDs.
func makeTraceRequestWithIDs(agentName, taskID, repoName, model string, tokensIn, tokensOut int64, traceID, spanID []byte) *coltracepb.ExportTraceServiceRequest {
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
					TraceId:           traceID,
					SpanId:            spanID,
					Name:              "llm.call",
					Kind:              tracepb.Span_SPAN_KIND_CLIENT,
					StartTimeUnixNano: 1000000000,
					EndTimeUnixNano:   2000000000,
					Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK, Message: "ok"},
					Attributes:        spanAttrs,
				}},
			}},
		}},
	}
}
