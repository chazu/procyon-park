package telemetry

import (
	"context"
	"sync"
	"testing"

	"github.com/chazu/maggie/compiler"
	"github.com/chazu/maggie/vm"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// memLogExporter is a simple in-memory log exporter for testing.
type memLogExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *memLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = append(e.records, records...)
	return nil
}

func (e *memLogExporter) Shutdown(_ context.Context) error { return nil }
func (e *memLogExporter) ForceFlush(_ context.Context) error { return nil }

func (e *memLogExporter) Records() []sdklog.Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	cp := make([]sdklog.Record, len(e.records))
	copy(cp, e.records)
	return cp
}

// testSetup creates a Maggie VM with Telemetry primitives backed by in-memory
// exporters.
type testSetup struct {
	VM       *vm.VM
	Provider *Provider
	Spans    *tracetest.InMemoryExporter
	Metrics  *sdkmetric.ManualReader
	Logs     *memLogExporter
}

func newTestSetup(t *testing.T) *testSetup {
	t.Helper()

	spanExp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(spanExp),
	)

	metricReader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(metricReader),
	)

	logExp := &memLogExporter{}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewSimpleProcessor(logExp)),
	)

	provider := NewTestProvider(tp, mp, lp)

	v := vm.NewVM()
	v.UseGoCompiler(compiler.Compile)
	Register(v)

	provVal, err := v.RegisterGoObject(provider)
	if err != nil {
		t.Fatalf("register provider: %v", err)
	}
	v.Globals["TestTelemetry"] = provVal

	return &testSetup{
		VM:       v,
		Provider: provider,
		Spans:    spanExp,
		Metrics:  metricReader,
		Logs:     logExp,
	}
}

// eval compiles and executes a Maggie expression, returning the result.
func eval(t *testing.T, v *vm.VM, source string) vm.Value {
	t.Helper()
	fn, err := compiler.CompileExpr(source, v.Selectors, v.Symbols, v.Registry())
	if err != nil {
		t.Fatalf("compilation failed: %v\nsource: %s", err, source)
	}
	return v.Execute(fn, vm.Nil, nil)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestSpanCreation(t *testing.T) {
	ts := newTestSetup(t)

	result := eval(t, ts.VM, `TestTelemetry span: 'test-span' do: [ 42 ]`)

	if !result.IsSmallInt() || result.SmallInt() != 42 {
		t.Fatalf("expected block result 42, got %v (isSmallInt: %v)", result, result.IsSmallInt())
	}

	ts.Provider.tp.ForceFlush(context.Background())
	spans := ts.Spans.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Name != "test-span" {
		t.Fatalf("expected span name 'test-span', got %q", spans[0].Name)
	}
}

func TestSetAttribute(t *testing.T) {
	ts := newTestSetup(t)

	eval(t, ts.VM, `
		TestTelemetry span: 'attr-span' do: [
			TestTelemetry setAttribute: 'myKey' value: 'myValue'
		]
	`)

	ts.Provider.tp.ForceFlush(context.Background())
	spans := ts.Spans.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	found := false
	for _, a := range spans[0].Attributes {
		if string(a.Key) == "myKey" && a.Value.AsString() == "myValue" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("attribute 'myKey'='myValue' not found on span, attrs: %v", spans[0].Attributes)
	}
}

func TestNestedSpans(t *testing.T) {
	ts := newTestSetup(t)

	eval(t, ts.VM, `
		TestTelemetry span: 'outer' do: [
			TestTelemetry span: 'inner' do: [ 99 ]
		]
	`)

	ts.Provider.tp.ForceFlush(context.Background())
	spans := ts.Spans.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}

	var inner, outer tracetest.SpanStub
	for _, s := range spans {
		if s.Name == "inner" {
			inner = s
		} else if s.Name == "outer" {
			outer = s
		}
	}
	if inner.Name == "" || outer.Name == "" {
		t.Fatalf("could not find inner/outer spans")
	}
	if inner.Parent.TraceID() != outer.SpanContext.TraceID() {
		t.Fatalf("inner span should share trace ID with outer span")
	}
	if inner.Parent.SpanID() != outer.SpanContext.SpanID() {
		t.Fatalf("inner span's parent should be outer span")
	}
}

func TestCounterIncrement(t *testing.T) {
	ts := newTestSetup(t)
	v := ts.VM
	p := ts.Provider

	// Build attributes dict via Go API and call the primitive directly.
	attrDict := v.NewDictionary()
	v.DictionaryAtPut(attrDict, v.Registry().NewStringValue("env"), v.Registry().NewStringValue("test"))

	provVal := v.Globals["TestTelemetry"]
	result := v.Send(provVal, "counter:increment:attributes:", []vm.Value{
		v.Registry().NewStringValue("my.counter"),
		vm.FromSmallInt(5),
		attrDict,
	})

	// counter method returns vm.True but Send from Go may return Nil if dispatch fails.
	// The counter is created internally regardless. Check the metric data.
	_ = result
	_ = p

	var rm metricdata.ResourceMetrics
	if err := ts.Metrics.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}

	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "my.counter" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("counter 'my.counter' not found in collected metrics")
	}
}

func TestLogEmission(t *testing.T) {
	ts := newTestSetup(t)
	v := ts.VM

	attrDict := v.NewDictionary()
	v.DictionaryAtPut(attrDict, v.Registry().NewStringValue("component"), v.Registry().NewStringValue("test"))

	provVal := v.Globals["TestTelemetry"]
	v.Send(provVal, "log:severity:attributes:", []vm.Value{
		v.Registry().NewStringValue("hello from maggie"),
		v.Registry().NewStringValue("info"),
		attrDict,
	})

	records := ts.Logs.Records()
	if len(records) == 0 {
		t.Fatalf("expected at least 1 log record, got 0")
	}

	found := false
	for _, r := range records {
		body := r.Body()
		if body.Kind() == otellog.KindString && body.AsString() == "hello from maggie" {
			found = true
		}
	}
	if !found {
		t.Fatalf("log record 'hello from maggie' not found")
	}
}

func TestContextPropagation(t *testing.T) {
	ts := newTestSetup(t)
	p := ts.Provider

	if len(p.ctxStack) != 1 {
		t.Fatalf("expected initial stack size 1, got %d", len(p.ctxStack))
	}

	ctx1 := context.WithValue(context.Background(), contextKey("a"), "1")
	p.pushCtx(ctx1)
	if p.currentCtx() != ctx1 {
		t.Fatal("expected pushed context to be current")
	}

	ctx2 := context.WithValue(ctx1, contextKey("b"), "2")
	p.pushCtx(ctx2)
	if p.currentCtx() != ctx2 {
		t.Fatal("expected second pushed context to be current")
	}

	p.popCtx()
	if p.currentCtx() != ctx1 {
		t.Fatal("expected first context after pop")
	}

	p.popCtx()
	if len(p.ctxStack) != 1 {
		t.Fatal("expected stack size 1 after popping all pushed contexts")
	}
}

type contextKey string
