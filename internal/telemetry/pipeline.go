// Package telemetry pipeline.go implements an in-memory DuckDB pipeline that
// consumes decoded OTLP signals from the Receiver's channels and inserts them
// into three tables: otel_spans, otel_metrics, otel_logs.
//
// Protobuf-to-schema conversion extracts cub correlation attributes
// (cub.agent.name, cub.task.id, cub.repository.name) and gen_ai fields
// (gen_ai.request.model, gen_ai.usage.input_tokens, gen_ai.usage.output_tokens,
// gen_ai.usage.cost) from span/resource attributes.
package telemetry

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sync"

	_ "github.com/marcboeker/go-duckdb"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
)

// Pipeline consumes OTLP signals from a Receiver and inserts them into DuckDB.
type Pipeline struct {
	db   *sql.DB
	mu   sync.Mutex
	once sync.Once
}

// NewPipeline creates a new in-memory DuckDB pipeline and initialises the schema.
func NewPipeline() (*Pipeline, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("pipeline: open duckdb: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pipeline: ping duckdb: %w", err)
	}

	p := &Pipeline{db: db}
	if err := p.createSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return p, nil
}

// DB returns the underlying *sql.DB for direct querying.
func (p *Pipeline) DB() *sql.DB { return p.db }

// Close closes the DuckDB connection.
func (p *Pipeline) Close() error {
	var err error
	p.once.Do(func() { err = p.db.Close() })
	return err
}

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

func (p *Pipeline) createSchema() error {
	ddl := []string{
		schemaSpans,
		schemaMetrics,
		schemaLogs,
	}
	for _, stmt := range ddl {
		if _, err := p.db.Exec(stmt); err != nil {
			return fmt.Errorf("pipeline: create schema: %w", err)
		}
	}
	return nil
}

const schemaSpans = `CREATE TABLE otel_spans (
	trace_id       VARCHAR NOT NULL,
	span_id        VARCHAR NOT NULL,
	parent_span_id VARCHAR NOT NULL DEFAULT '',
	name           VARCHAR NOT NULL,
	kind           INTEGER NOT NULL DEFAULT 0,
	start_time_ns  BIGINT  NOT NULL,
	end_time_ns    BIGINT  NOT NULL,
	duration_ns    BIGINT  NOT NULL,
	status_code    INTEGER NOT NULL DEFAULT 0,
	status_message VARCHAR NOT NULL DEFAULT '',
	agent_name     VARCHAR NOT NULL DEFAULT '',
	task_id        VARCHAR NOT NULL DEFAULT '',
	repo_name      VARCHAR NOT NULL DEFAULT '',
	model_name     VARCHAR NOT NULL DEFAULT '',
	tokens_in      BIGINT  NOT NULL DEFAULT 0,
	tokens_out     BIGINT  NOT NULL DEFAULT 0,
	cost           DOUBLE  NOT NULL DEFAULT 0.0
)`

const schemaMetrics = `CREATE TABLE otel_metrics (
	name              VARCHAR NOT NULL,
	description       VARCHAR NOT NULL DEFAULT '',
	unit              VARCHAR NOT NULL DEFAULT '',
	metric_type       VARCHAR NOT NULL,
	time_ns           BIGINT  NOT NULL,
	start_time_ns     BIGINT  NOT NULL DEFAULT 0,
	agent_name        VARCHAR NOT NULL DEFAULT '',
	task_id           VARCHAR NOT NULL DEFAULT '',
	repo_name         VARCHAR NOT NULL DEFAULT '',
	value_int         BIGINT,
	value_double      DOUBLE,
	is_monotonic      BOOLEAN,
	aggregation_temp  INTEGER,
	histogram_count   BIGINT,
	histogram_sum     DOUBLE,
	histogram_min     DOUBLE,
	histogram_max     DOUBLE,
	bucket_counts     VARCHAR,
	explicit_bounds   VARCHAR
)`

const schemaLogs = `CREATE TABLE otel_logs (
	time_ns          BIGINT  NOT NULL,
	observed_time_ns BIGINT  NOT NULL,
	severity_number  INTEGER NOT NULL DEFAULT 0,
	severity_text    VARCHAR NOT NULL DEFAULT '',
	body             VARCHAR NOT NULL DEFAULT '',
	trace_id         VARCHAR NOT NULL DEFAULT '',
	span_id          VARCHAR NOT NULL DEFAULT '',
	agent_name       VARCHAR NOT NULL DEFAULT '',
	task_id          VARCHAR NOT NULL DEFAULT '',
	repo_name        VARCHAR NOT NULL DEFAULT ''
)`

// ---------------------------------------------------------------------------
// Consume Loop
// ---------------------------------------------------------------------------

// ConsumeLoop reads from the Receiver's channels until ctx is cancelled.
// It blocks and should be run in a goroutine.
func (p *Pipeline) ConsumeLoop(ctx context.Context, r *Receiver) {
	for {
		select {
		case <-ctx.Done():
			return
		case sig, ok := <-r.Traces:
			if !ok {
				return
			}
			p.handleSignal(sig)
		case sig, ok := <-r.Metrics:
			if !ok {
				return
			}
			p.handleSignal(sig)
		case sig, ok := <-r.Logs:
			if !ok {
				return
			}
			p.handleSignal(sig)
		}
	}
}

func (p *Pipeline) handleSignal(sig Signal) {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch sig.Type {
	case SignalTraces:
		p.insertTraces(sig.Payload)
	case SignalMetrics:
		p.insertMetrics(sig.Payload)
	case SignalLogs:
		p.insertLogs(sig.Payload)
	}
}

// ---------------------------------------------------------------------------
// Trace Insertion
// ---------------------------------------------------------------------------

func (p *Pipeline) insertTraces(msg proto.Message) {
	req, ok := msg.(*coltracepb.ExportTraceServiceRequest)
	if !ok {
		return
	}
	for _, rs := range req.GetResourceSpans() {
		attrs := mergeAttrs(rs.GetResource())
		for _, ss := range rs.GetScopeSpans() {
			for _, span := range ss.GetSpans() {
				p.insertSpan(span, attrs)
			}
		}
	}
}

func (p *Pipeline) insertSpan(span *tracepb.Span, attrs attrSet) {
	startNs := int64(span.GetStartTimeUnixNano())
	endNs := int64(span.GetEndTimeUnixNano())
	durationNs := endNs - startNs

	// Merge span-level attributes with resource-level.
	spanAttrs := extractAttrs(span.GetAttributes())
	merged := attrs.merge(spanAttrs)

	var statusCode int32
	var statusMsg string
	if s := span.GetStatus(); s != nil {
		statusCode = int32(s.GetCode())
		statusMsg = s.GetMessage()
	}

	p.db.Exec(`INSERT INTO otel_spans VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		hexID(span.GetTraceId()),
		hexID(span.GetSpanId()),
		hexID(span.GetParentSpanId()),
		span.GetName(),
		int32(span.GetKind()),
		startNs,
		endNs,
		durationNs,
		statusCode,
		statusMsg,
		merged.agentName,
		merged.taskID,
		merged.repoName,
		merged.modelName,
		merged.tokensIn,
		merged.tokensOut,
		merged.cost,
	)
}

// ---------------------------------------------------------------------------
// Metric Insertion
// ---------------------------------------------------------------------------

func (p *Pipeline) insertMetrics(msg proto.Message) {
	req, ok := msg.(*colmetricspb.ExportMetricsServiceRequest)
	if !ok {
		return
	}
	for _, rm := range req.GetResourceMetrics() {
		attrs := mergeAttrs(rm.GetResource())
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				p.insertMetric(m, attrs)
			}
		}
	}
}

func (p *Pipeline) insertMetric(m *metricspb.Metric, attrs attrSet) {
	name := m.GetName()
	desc := m.GetDescription()
	unit := m.GetUnit()

	switch d := m.GetData().(type) {
	case *metricspb.Metric_Gauge:
		for _, dp := range d.Gauge.GetDataPoints() {
			dpAttrs := extractAttrs(dp.GetAttributes())
			merged := attrs.merge(dpAttrs)
			valInt, valDouble := numberValue(dp)
			p.db.Exec(`INSERT INTO otel_metrics VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				name, desc, unit, "gauge",
				int64(dp.GetTimeUnixNano()),
				int64(dp.GetStartTimeUnixNano()),
				merged.agentName, merged.taskID, merged.repoName,
				valInt, valDouble,
				nil, nil, // is_monotonic, aggregation_temp
				nil, nil, nil, nil, // histogram fields
				nil, nil, // bucket_counts, explicit_bounds
			)
		}
	case *metricspb.Metric_Sum:
		for _, dp := range d.Sum.GetDataPoints() {
			dpAttrs := extractAttrs(dp.GetAttributes())
			merged := attrs.merge(dpAttrs)
			valInt, valDouble := numberValue(dp)
			p.db.Exec(`INSERT INTO otel_metrics VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				name, desc, unit, "sum",
				int64(dp.GetTimeUnixNano()),
				int64(dp.GetStartTimeUnixNano()),
				merged.agentName, merged.taskID, merged.repoName,
				valInt, valDouble,
				d.Sum.GetIsMonotonic(),
				int32(d.Sum.GetAggregationTemporality()),
				nil, nil, nil, nil,
				nil, nil,
			)
		}
	case *metricspb.Metric_Histogram:
		for _, dp := range d.Histogram.GetDataPoints() {
			dpAttrs := extractAttrs(dp.GetAttributes())
			merged := attrs.merge(dpAttrs)
			p.db.Exec(`INSERT INTO otel_metrics VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				name, desc, unit, "histogram",
				int64(dp.GetTimeUnixNano()),
				int64(dp.GetStartTimeUnixNano()),
				merged.agentName, merged.taskID, merged.repoName,
				nil, nil, // value_int, value_double
				nil,
				int32(d.Histogram.GetAggregationTemporality()),
				int64(dp.GetCount()),
				ptrFloat(dp.Sum),
				ptrFloat(dp.Min),
				ptrFloat(dp.Max),
				formatUint64Slice(dp.GetBucketCounts()),
				formatFloat64Slice(dp.GetExplicitBounds()),
			)
		}
	}
}

// numberValue extracts the int/double value from a NumberDataPoint.
type numberDataPoint interface {
	GetValue() interface{}
}

func numberValue(dp *metricspb.NumberDataPoint) (*int64, *float64) {
	switch v := dp.GetValue().(type) {
	case *metricspb.NumberDataPoint_AsInt:
		i := v.AsInt
		return &i, nil
	case *metricspb.NumberDataPoint_AsDouble:
		d := v.AsDouble
		return nil, &d
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// Log Insertion
// ---------------------------------------------------------------------------

func (p *Pipeline) insertLogs(msg proto.Message) {
	req, ok := msg.(*collogspb.ExportLogsServiceRequest)
	if !ok {
		return
	}
	for _, rl := range req.GetResourceLogs() {
		attrs := mergeAttrs(rl.GetResource())
		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				p.insertLog(lr, attrs)
			}
		}
	}
}

func (p *Pipeline) insertLog(lr *logspb.LogRecord, attrs attrSet) {
	logAttrs := extractAttrs(lr.GetAttributes())
	merged := attrs.merge(logAttrs)

	body := anyValueString(lr.GetBody())

	p.db.Exec(`INSERT INTO otel_logs VALUES (?,?,?,?,?,?,?,?,?,?)`,
		int64(lr.GetTimeUnixNano()),
		int64(lr.GetObservedTimeUnixNano()),
		int32(lr.GetSeverityNumber()),
		lr.GetSeverityText(),
		body,
		hexID(lr.GetTraceId()),
		hexID(lr.GetSpanId()),
		merged.agentName,
		merged.taskID,
		merged.repoName,
	)
}

// ---------------------------------------------------------------------------
// Attribute Extraction
// ---------------------------------------------------------------------------

// attrSet holds the cub correlation and gen_ai fields extracted from attributes.
type attrSet struct {
	agentName string
	taskID    string
	repoName  string
	modelName string
	tokensIn  int64
	tokensOut int64
	cost      float64
}

// merge returns a new attrSet where non-empty values from other override this set.
func (a attrSet) merge(other attrSet) attrSet {
	if other.agentName != "" {
		a.agentName = other.agentName
	}
	if other.taskID != "" {
		a.taskID = other.taskID
	}
	if other.repoName != "" {
		a.repoName = other.repoName
	}
	if other.modelName != "" {
		a.modelName = other.modelName
	}
	if other.tokensIn != 0 {
		a.tokensIn = other.tokensIn
	}
	if other.tokensOut != 0 {
		a.tokensOut = other.tokensOut
	}
	if other.cost != 0 {
		a.cost = other.cost
	}
	return a
}

// mergeAttrs extracts cub/gen_ai attributes from a Resource.
func mergeAttrs(res *resourcepb.Resource) attrSet {
	if res == nil {
		return attrSet{}
	}
	return extractAttrs(res.GetAttributes())
}

// extractAttrs extracts cub/gen_ai fields from a KeyValue slice.
func extractAttrs(kvs []*commonpb.KeyValue) attrSet {
	var a attrSet
	for _, kv := range kvs {
		switch kv.GetKey() {
		case "cub.agent.name":
			a.agentName = kv.GetValue().GetStringValue()
		case "cub.task.id":
			a.taskID = kv.GetValue().GetStringValue()
		case "cub.repository.name":
			a.repoName = kv.GetValue().GetStringValue()
		case "gen_ai.request.model":
			a.modelName = kv.GetValue().GetStringValue()
		case "gen_ai.usage.input_tokens":
			a.tokensIn = kv.GetValue().GetIntValue()
		case "gen_ai.usage.output_tokens":
			a.tokensOut = kv.GetValue().GetIntValue()
		case "gen_ai.usage.cost":
			a.cost = kv.GetValue().GetDoubleValue()
		}
	}
	return a
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// hexID converts a byte-slice ID to a lowercase hex string.
func hexID(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return hex.EncodeToString(b)
}

// anyValueString extracts a string representation from an AnyValue.
func anyValueString(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	switch val := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return val.StringValue
	case *commonpb.AnyValue_IntValue:
		return fmt.Sprintf("%d", val.IntValue)
	case *commonpb.AnyValue_DoubleValue:
		return fmt.Sprintf("%g", val.DoubleValue)
	case *commonpb.AnyValue_BoolValue:
		return fmt.Sprintf("%t", val.BoolValue)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func ptrFloat(f *float64) *float64 { return f }

func formatUint64Slice(s []uint64) string {
	if len(s) == 0 {
		return ""
	}
	out := "["
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf("%d", v)
	}
	return out + "]"
}

func formatFloat64Slice(s []float64) string {
	if len(s) == 0 {
		return ""
	}
	out := "["
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf("%g", v)
	}
	return out + "]"
}
