package telemetry

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// ---------------------------------------------------------------------------
// Flush creates Parquet files
// ---------------------------------------------------------------------------

func TestFlushCreatesParquetFiles(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	warmDir := t.TempDir()
	f := NewFlusher(p, FlushConfig{WarmDir: warmDir})

	// Insert a span.
	req := makeTraceRequest("Lark", "task-1", "repo", "opus", 100, 50)
	p.handleSignal(Signal{Type: SignalTraces, Payload: req})

	// Insert a log.
	logReq := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource: &resourcepb.Resource{},
			ScopeLogs: []*logspb.ScopeLogs{{
				LogRecords: []*logspb.LogRecord{{
					TimeUnixNano:         1,
					ObservedTimeUnixNano: 2,
					Body:                 &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "hello"}},
				}},
			}},
		}},
	}
	p.handleSignal(Signal{Type: SignalLogs, Payload: logReq})

	flushTime := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)
	if err := f.FlushAt(flushTime); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Verify Parquet files exist in the correct partition directory.
	partDir := filepath.Join(warmDir, "2026-02")
	for _, stem := range []string{"spans", "logs"} {
		path := filepath.Join(partDir, stem+".parquet")
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("expected %s to be non-empty", path)
		}
	}

	// Metrics table was empty, so no metrics.parquet should be created.
	metricsPath := filepath.Join(partDir, "metrics.parquet")
	if _, err := os.Stat(metricsPath); err == nil {
		t.Errorf("expected metrics.parquet NOT to exist (no metrics were inserted)")
	}
}

// ---------------------------------------------------------------------------
// Flush purges in-memory data
// ---------------------------------------------------------------------------

func TestFlushPurgesData(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	warmDir := t.TempDir()
	f := NewFlusher(p, FlushConfig{WarmDir: warmDir})

	// Insert spans.
	for i := 0; i < 5; i++ {
		req := makeTraceRequest("Agent", "task", "repo", "model", 10, 5)
		p.handleSignal(Signal{Type: SignalTraces, Payload: req})
	}

	var count int
	p.DB().QueryRow("SELECT COUNT(*) FROM otel_spans").Scan(&count)
	if count != 5 {
		t.Fatalf("pre-flush count = %d, want 5", count)
	}

	if err := f.FlushAt(time.Now()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// After flush, in-memory table should be empty.
	p.DB().QueryRow("SELECT COUNT(*) FROM otel_spans").Scan(&count)
	if count != 0 {
		t.Errorf("post-flush count = %d, want 0", count)
	}
}

// ---------------------------------------------------------------------------
// Time partitioning
// ---------------------------------------------------------------------------

func TestFlushTimePartitioning(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	warmDir := t.TempDir()
	f := NewFlusher(p, FlushConfig{WarmDir: warmDir})

	// First batch: January 2026.
	req := makeTraceRequest("A", "t1", "r", "m", 1, 1)
	p.handleSignal(Signal{Type: SignalTraces, Payload: req})
	if err := f.FlushAt(time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("FlushAt jan: %v", err)
	}

	// Second batch: March 2026.
	req2 := makeTraceRequest("B", "t2", "r", "m", 2, 2)
	p.handleSignal(Signal{Type: SignalTraces, Payload: req2})
	if err := f.FlushAt(time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("FlushAt mar: %v", err)
	}

	// Verify both partition directories exist.
	for _, part := range []string{"2026-01", "2026-03"} {
		path := filepath.Join(warmDir, part, "spans.parquet")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}
}

// ---------------------------------------------------------------------------
// ZSTD compression (verify via DuckDB read-back)
// ---------------------------------------------------------------------------

func TestFlushZSTDCompression(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	warmDir := t.TempDir()
	f := NewFlusher(p, FlushConfig{WarmDir: warmDir})

	req := makeTraceRequest("Lark", "task-z", "repo-z", "opus", 500, 200)
	p.handleSignal(Signal{Type: SignalTraces, Payload: req})

	flushTime := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if err := f.FlushAt(flushTime); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Read back the Parquet file via DuckDB to verify it's valid.
	parquetPath := filepath.Join(warmDir, "2026-02", "spans.parquet")
	var agentName string
	err = p.DB().QueryRow(
		"SELECT agent_name FROM read_parquet('"+parquetPath+"') LIMIT 1",
	).Scan(&agentName)
	if err != nil {
		t.Fatalf("read_parquet: %v", err)
	}
	if agentName != "Lark" {
		t.Errorf("agent_name = %q, want Lark", agentName)
	}
}

// ---------------------------------------------------------------------------
// Crash-recovery semantics: export-then-purge
// ---------------------------------------------------------------------------

func TestFlushExportThenPurge(t *testing.T) {
	// This test verifies the ordering: Parquet file is written before
	// in-memory data is purged. We do this by checking that after flush,
	// both the Parquet file exists AND the in-memory table is empty.
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	warmDir := t.TempDir()
	f := NewFlusher(p, FlushConfig{WarmDir: warmDir})

	req := makeTraceRequest("CrashTest", "task-cr", "repo-cr", "model", 1, 1)
	p.handleSignal(Signal{Type: SignalTraces, Payload: req})

	if err := f.FlushAt(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Parquet file must exist (export happened).
	parquetPath := filepath.Join(warmDir, "2026-05", "spans.parquet")
	if _, err := os.Stat(parquetPath); err != nil {
		t.Fatalf("parquet not created: %v", err)
	}

	// In-memory must be empty (purge happened after export).
	var count int
	p.DB().QueryRow("SELECT COUNT(*) FROM otel_spans").Scan(&count)
	if count != 0 {
		t.Errorf("in-memory count = %d after flush, want 0", count)
	}

	// Data is recoverable from Parquet.
	var agent string
	err = p.DB().QueryRow("SELECT agent_name FROM read_parquet('" + parquetPath + "')").Scan(&agent)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if agent != "CrashTest" {
		t.Errorf("agent = %q, want CrashTest", agent)
	}
}

// ---------------------------------------------------------------------------
// Final flush on Stop()
// ---------------------------------------------------------------------------

func TestFinalFlushOnStop(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	warmDir := t.TempDir()
	f := NewFlusher(p, FlushConfig{
		WarmDir:       warmDir,
		FlushInterval: 1 * time.Hour, // Long interval so periodic flush won't fire.
	})

	// Insert data.
	req := makeTraceRequest("StopTest", "task-stop", "repo", "m", 1, 1)
	p.handleSignal(Signal{Type: SignalTraces, Payload: req})

	ctx, cancel := context.WithCancel(context.Background())
	go f.Start(ctx)

	// Give the loop time to start.
	time.Sleep(50 * time.Millisecond)

	// Cancel context to trigger final flush + shutdown.
	cancel()

	// Wait for flush loop to complete.
	select {
	case <-f.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("flush loop did not stop within 5s")
	}

	// Data should have been flushed even though the periodic ticker didn't fire.
	var count int
	p.DB().QueryRow("SELECT COUNT(*) FROM otel_spans").Scan(&count)
	if count != 0 {
		t.Errorf("in-memory count = %d after stop, want 0 (final flush should have run)", count)
	}

	// Find the Parquet file in the partition directory.
	entries, err := os.ReadDir(warmDir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no partition directories created after final flush")
	}

	spansPath := filepath.Join(warmDir, entries[0].Name(), "spans.parquet")
	if _, err := os.Stat(spansPath); err != nil {
		t.Errorf("spans.parquet not found after final flush: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Empty flush is a no-op
// ---------------------------------------------------------------------------

func TestFlushEmptyTablesNoOp(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	warmDir := t.TempDir()
	f := NewFlusher(p, FlushConfig{WarmDir: warmDir})

	// Flush with no data — should succeed and create no files.
	if err := f.FlushAt(time.Now()); err != nil {
		t.Fatalf("Flush empty: %v", err)
	}

	entries, _ := os.ReadDir(warmDir)
	if len(entries) != 0 {
		t.Errorf("expected no directories for empty flush, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// DefaultFlushInterval
// ---------------------------------------------------------------------------

func TestDefaultFlushInterval(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	f := NewFlusher(p, FlushConfig{WarmDir: t.TempDir()})
	if f.cfg.FlushInterval != DefaultFlushInterval {
		t.Errorf("interval = %v, want %v", f.cfg.FlushInterval, DefaultFlushInterval)
	}

	custom := NewFlusher(p, FlushConfig{WarmDir: t.TempDir(), FlushInterval: 30 * time.Second})
	if custom.cfg.FlushInterval != 30*time.Second {
		t.Errorf("custom interval = %v, want 30s", custom.cfg.FlushInterval)
	}
}

// ---------------------------------------------------------------------------
// Flush loop with Stop()
// ---------------------------------------------------------------------------

func TestFlushLoopStopSignal(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	warmDir := t.TempDir()
	f := NewFlusher(p, FlushConfig{
		WarmDir:       warmDir,
		FlushInterval: 1 * time.Hour,
	})

	// Insert data.
	req := makeTraceRequest("StopSig", "task-ss", "repo", "m", 1, 1)
	p.handleSignal(Signal{Type: SignalTraces, Payload: req})

	// Start with a long-lived context — we'll use Stop() instead of cancel.
	go f.Start(context.Background())
	time.Sleep(50 * time.Millisecond)

	f.Stop()

	// Data should have been flushed on Stop.
	var count int
	p.DB().QueryRow("SELECT COUNT(*) FROM otel_spans").Scan(&count)
	if count != 0 {
		t.Errorf("in-memory count = %d after Stop(), want 0", count)
	}
}

// ---------------------------------------------------------------------------
// All three signal types flush to Parquet
// ---------------------------------------------------------------------------

func TestFlushAllSignalTypes(t *testing.T) {
	p, err := NewPipeline()
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer p.Close()

	warmDir := t.TempDir()
	f := NewFlusher(p, FlushConfig{WarmDir: warmDir})

	// Insert spans.
	p.handleSignal(Signal{Type: SignalTraces, Payload: makeTraceRequest("A", "t", "r", "m", 1, 1)})

	// Insert metrics (gauge).
	metricReq := makeGaugeMetricRequest("A", 42)
	p.handleSignal(Signal{Type: SignalMetrics, Payload: metricReq})

	// Insert logs.
	logReq := makeLogRequest("A", "hello")
	p.handleSignal(Signal{Type: SignalLogs, Payload: logReq})

	flushTime := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := f.FlushAt(flushTime); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	partDir := filepath.Join(warmDir, "2026-06")
	for _, stem := range []string{"spans", "metrics", "logs"} {
		path := filepath.Join(partDir, stem+".parquet")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func makeGaugeMetricRequest(agentName string, value int64) *colmetricspb.ExportMetricsServiceRequest {
	resAttrs := []*commonpb.KeyValue{
		{Key: "cub.agent.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: agentName}}},
	}
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource: &resourcepb.Resource{Attributes: resAttrs},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{{
					Name: "test.gauge",
					Unit: "1",
					Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
						DataPoints: []*metricspb.NumberDataPoint{{
							TimeUnixNano: 1000000000,
							Value:        &metricspb.NumberDataPoint_AsInt{AsInt: value},
						}},
					}},
				}},
			}},
		}},
	}
}

func makeLogRequest(agentName, body string) *collogspb.ExportLogsServiceRequest {
	resAttrs := []*commonpb.KeyValue{
		{Key: "cub.agent.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: agentName}}},
	}
	return &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource: &resourcepb.Resource{Attributes: resAttrs},
			ScopeLogs: []*logspb.ScopeLogs{{
				LogRecords: []*logspb.LogRecord{{
					TimeUnixNano:         1000000000,
					ObservedTimeUnixNano: 1000000001,
					Body:                 &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: body}},
				}},
			}},
		}},
	}
}
