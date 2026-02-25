// primitives.go registers OTEL trace/metric/log primitives on the Maggie VM.
//
// Maggie API:
//
//	Telemetry new: aDictionary                    "class method — creates provider with resource attrs"
//	telem shutdown                                "graceful shutdown of all providers"
//
//	telem span:do: 'name' aBlock                  "create traced span, execute block, end span"
//	telem setAttribute:value: 'key' 'val'         "set attribute on current span"
//	telem counter:increment:attributes: 'name' n d "increment a counter metric"
//	telem log:severity:attributes: 'msg' 'info' d  "emit a log record"
package telemetry

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/chazu/maggie/vm"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otellog "go.opentelemetry.io/otel/log"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Provider bundles OTEL tracer, meter, and logger with their SDK providers
// for lifecycle management. It maintains a context stack for span propagation
// within the single-threaded Maggie VM.
type Provider struct {
	tracer   trace.Tracer
	meter    *sdkmetric.MeterProvider
	logger   *sdklog.LoggerProvider
	tp       *sdktrace.TracerProvider
	otelLog  otellog.Logger
	ctxStack []context.Context
	counters map[string]*counterEntry
	mu       sync.Mutex // protects counters map (accessed from Go side only)
}

type counterEntry struct {
	counter otelmetric.Float64Counter
}

// ProviderConfig holds configuration for creating a Provider.
type ProviderConfig struct {
	Endpoint       string // OTLP endpoint (default: localhost:4317)
	ServiceName    string // service.name resource attribute
	AgentName      string // cub.agent.name resource attribute
	RepositoryName string // cub.repository.name resource attribute
	TaskID         string // cub.task.id resource attribute
	ProcessName    string // maggie.process.name resource attribute
}

// NewProvider creates a fully-configured OTEL Provider with trace, metric, and
// log exporters sending to the given OTLP endpoint.
func NewProvider(ctx context.Context, cfg ProviderConfig) (*Provider, error) {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "localhost:4317"
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "maggie"
	}

	// Build resource with all required attributes.
	attrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
	}
	if cfg.AgentName != "" {
		attrs = append(attrs, attribute.String("cub.agent.name", cfg.AgentName))
	}
	if cfg.RepositoryName != "" {
		attrs = append(attrs, attribute.String("cub.repository.name", cfg.RepositoryName))
	}
	if cfg.TaskID != "" {
		attrs = append(attrs, attribute.String("cub.task.id", cfg.TaskID))
	}
	if cfg.ProcessName != "" {
		attrs = append(attrs, attribute.String("maggie.process.name", cfg.ProcessName))
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(attrs...),
	)
	if err != nil {
		return nil, err
	}

	// Trace exporter + provider.
	traceExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)

	// Metric exporter + provider.
	metricExp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		tp.Shutdown(ctx)
		return nil, err
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
		sdkmetric.WithResource(res),
	)

	// Log exporter + provider.
	logExp, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpoint(cfg.Endpoint),
		otlploggrpc.WithInsecure(),
	)
	if err != nil {
		tp.Shutdown(ctx)
		mp.Shutdown(ctx)
		return nil, err
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
		sdklog.WithResource(res),
	)

	return &Provider{
		tracer:   tp.Tracer("maggie"),
		meter:    mp,
		logger:   lp,
		tp:       tp,
		otelLog:  lp.Logger("maggie"),
		ctxStack: []context.Context{context.Background()},
		counters: make(map[string]*counterEntry),
	}, nil
}

// NewTestProvider creates a Provider backed by the given SDK providers. This is
// used in tests to inject in-memory exporters.
func NewTestProvider(tp *sdktrace.TracerProvider, mp *sdkmetric.MeterProvider, lp *sdklog.LoggerProvider) *Provider {
	return &Provider{
		tracer:   tp.Tracer("maggie"),
		meter:    mp,
		logger:   lp,
		tp:       tp,
		otelLog:  lp.Logger("maggie"),
		ctxStack: []context.Context{context.Background()},
		counters: make(map[string]*counterEntry),
	}
}

// Shutdown gracefully flushes and shuts down all providers.
func (p *Provider) Shutdown(ctx context.Context) error {
	var firstErr error
	if p.tp != nil {
		if err := p.tp.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if p.meter != nil {
		if err := p.meter.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if p.logger != nil {
		if err := p.logger.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// currentCtx returns the current context from the stack.
func (p *Provider) currentCtx() context.Context {
	if len(p.ctxStack) == 0 {
		return context.Background()
	}
	return p.ctxStack[len(p.ctxStack)-1]
}

// pushCtx pushes a context onto the stack.
func (p *Provider) pushCtx(ctx context.Context) {
	p.ctxStack = append(p.ctxStack, ctx)
}

// popCtx pops the top context from the stack.
func (p *Provider) popCtx() {
	if len(p.ctxStack) > 1 {
		p.ctxStack = p.ctxStack[:len(p.ctxStack)-1]
	}
}

// Register registers the Telemetry class and its primitives on the given VM.
func Register(vmInst *vm.VM) {
	provType := reflect.TypeOf((*Provider)(nil))
	telemClass := vmInst.RegisterGoType("Telemetry", provType)

	registerTelemClassMethods(vmInst, telemClass)
	registerTelemInstanceMethods(vmInst, telemClass)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func getProvider(vmInst *vm.VM, v vm.Value) *Provider {
	goVal, ok := vmInst.GetGoObject(v)
	if !ok {
		return nil
	}
	p, ok := goVal.(*Provider)
	if !ok {
		return nil
	}
	return p
}

func telemToString(vmInst *vm.VM, v vm.Value) string {
	if vm.IsStringValue(v) {
		return vmInst.Registry().GetStringContent(v)
	}
	if v.IsSymbol() {
		return vmInst.SymbolName(v.SymbolID())
	}
	return ""
}

func telemFailure(vmInst *vm.VM, reason string) vm.Value {
	reasonVal := vmInst.Registry().NewStringValue(reason)
	failureClassVal := vmInst.ClassValue(vmInst.FailureClass)
	return vmInst.Send(failureClassVal, "with:", []vm.Value{reasonVal})
}

// dictToAttributes converts a Maggie Dictionary to OTEL key-value attributes.
func dictToAttributes(vmInst *vm.VM, dict vm.Value) []attribute.KeyValue {
	if dict == vm.Nil {
		return nil
	}
	d := vmInst.Registry().GetDictionaryObject(dict)
	if d == nil {
		return nil
	}
	var attrs []attribute.KeyValue
	for h, key := range d.Keys {
		keyStr := telemToString(vmInst, key)
		val := d.Data[h]
		switch {
		case val.IsSmallInt():
			attrs = append(attrs, attribute.Int64(keyStr, val.SmallInt()))
		case vm.IsStringValue(val):
			attrs = append(attrs, attribute.String(keyStr, vmInst.Registry().GetStringContent(val)))
		case val.IsSymbol():
			attrs = append(attrs, attribute.String(keyStr, vmInst.SymbolName(val.SymbolID())))
		case val == vm.True:
			attrs = append(attrs, attribute.Bool(keyStr, true))
		case val == vm.False:
			attrs = append(attrs, attribute.Bool(keyStr, false))
		default:
			attrs = append(attrs, attribute.String(keyStr, "<unknown>"))
		}
	}
	return attrs
}

// severityFromString maps string severity names to OTEL log severity numbers.
func severityFromString(s string) otellog.Severity {
	switch strings.ToLower(s) {
	case "trace":
		return otellog.SeverityTrace
	case "debug":
		return otellog.SeverityDebug
	case "info":
		return otellog.SeverityInfo
	case "warn", "warning":
		return otellog.SeverityWarn
	case "error":
		return otellog.SeverityError
	case "fatal":
		return otellog.SeverityFatal
	default:
		return otellog.SeverityInfo
	}
}

// ---------------------------------------------------------------------------
// Class Methods
// ---------------------------------------------------------------------------

func registerTelemClassMethods(vmInst *vm.VM, telemClass *vm.Class) {
	// new: aDictionary — Create a new Telemetry provider.
	// Dictionary keys: endpoint, serviceName, agentName, repositoryName,
	//                  taskId, processName.
	telemClass.AddClassMethod1(vmInst.Selectors, "new:", func(vmPtr interface{}, recv vm.Value, dictVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)

		get := func(key string) string {
			k := v.Registry().NewStringValue(key)
			val := v.DictionaryAt(dictVal, k)
			if val == vm.Nil {
				return ""
			}
			return telemToString(v, val)
		}

		cfg := ProviderConfig{
			Endpoint:       get("endpoint"),
			ServiceName:    get("serviceName"),
			AgentName:      get("agentName"),
			RepositoryName: get("repositoryName"),
			TaskID:         get("taskId"),
			ProcessName:    get("processName"),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		provider, err := NewProvider(ctx, cfg)
		if err != nil {
			return telemFailure(v, "Telemetry new: "+err.Error())
		}

		val, regErr := v.RegisterGoObject(provider)
		if regErr != nil {
			provider.Shutdown(context.Background())
			return telemFailure(v, "Telemetry new: cannot register: "+regErr.Error())
		}
		return val
	})
}

// ---------------------------------------------------------------------------
// Instance Methods
// ---------------------------------------------------------------------------

func registerTelemInstanceMethods(vmInst *vm.VM, telemClass *vm.Class) {
	// shutdown — Graceful shutdown of all providers.
	telemClass.AddMethod0(vmInst.Selectors, "shutdown", func(vmPtr interface{}, recv vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		p := getProvider(v, recv)
		if p == nil {
			return telemFailure(v, "Not a Telemetry provider")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.Shutdown(ctx); err != nil {
			return telemFailure(v, "Telemetry shutdown: "+err.Error())
		}
		return vm.True
	})

	// span:do: spanName aBlock — Create a traced span, execute block, end span.
	// The block receives the span name as argument. Returns the block's result.
	telemClass.AddMethod2(vmInst.Selectors, "span:do:", func(vmPtr interface{}, recv vm.Value, nameVal vm.Value, blockVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		p := getProvider(v, recv)
		if p == nil {
			return telemFailure(v, "Not a Telemetry provider")
		}

		spanName := telemToString(v, nameVal)
		if spanName == "" {
			return telemFailure(v, "Telemetry span:do: requires a span name")
		}

		// Start span with the current context from the stack.
		ctx, span := p.tracer.Start(p.currentCtx(), spanName)
		p.pushCtx(ctx)
		defer func() {
			p.popCtx()
			span.End()
		}()

		// Execute the block. Maggie blocks respond to "value" (0-arg).
		result := v.Send(blockVal, "value", nil)

		// If the block returned a Failure, mark the span as error.
		failureClass := v.FailureClass
		if failureClass != nil {
			resultClass := v.ClassFor(result)
			if resultClass == failureClass {
				span.SetStatus(codes.Error, "block returned Failure")
			}
		}

		return result
	})

	// setAttribute:value: key val — Set an attribute on the current span.
	telemClass.AddMethod2(vmInst.Selectors, "setAttribute:value:", func(vmPtr interface{}, recv vm.Value, keyVal vm.Value, valVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		p := getProvider(v, recv)
		if p == nil {
			return telemFailure(v, "Not a Telemetry provider")
		}

		key := telemToString(v, keyVal)
		if key == "" {
			return telemFailure(v, "Telemetry setAttribute:value: requires a key")
		}

		span := trace.SpanFromContext(p.currentCtx())
		switch {
		case valVal.IsSmallInt():
			span.SetAttributes(attribute.Int64(key, valVal.SmallInt()))
		case vm.IsStringValue(valVal):
			span.SetAttributes(attribute.String(key, v.Registry().GetStringContent(valVal)))
		case valVal.IsSymbol():
			span.SetAttributes(attribute.String(key, v.SymbolName(valVal.SymbolID())))
		case valVal == vm.True:
			span.SetAttributes(attribute.Bool(key, true))
		case valVal == vm.False:
			span.SetAttributes(attribute.Bool(key, false))
		default:
			span.SetAttributes(attribute.String(key, "<unknown>"))
		}

		return vm.True
	})

	// counter:increment:attributes: name amount attrDict — Increment a counter metric.
	telemClass.AddMethod3(vmInst.Selectors, "counter:increment:attributes:", func(vmPtr interface{}, recv vm.Value, nameVal vm.Value, amountVal vm.Value, attrsVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		p := getProvider(v, recv)
		if p == nil {
			return telemFailure(v, "Not a Telemetry provider")
		}

		name := telemToString(v, nameVal)
		if name == "" {
			return telemFailure(v, "Telemetry counter:increment:attributes: requires a counter name")
		}

		var amount float64
		if amountVal.IsSmallInt() {
			amount = float64(amountVal.SmallInt())
		} else {
			amount = 1.0
		}

		// Get or create the counter.
		p.mu.Lock()
		entry, ok := p.counters[name]
		if !ok {
			m := p.meter.Meter("maggie")
			c, err := m.Float64Counter(name)
			if err != nil {
				p.mu.Unlock()
				return telemFailure(v, "Telemetry counter: "+err.Error())
			}
			entry = &counterEntry{counter: c}
			p.counters[name] = entry
		}
		p.mu.Unlock()

		attrs := dictToAttributes(v, attrsVal)
		entry.counter.Add(p.currentCtx(), amount, otelmetric.WithAttributes(attrs...))
		return vm.True
	})

	// log:severity:attributes: message severity attrDict — Emit a log record.
	telemClass.AddMethod3(vmInst.Selectors, "log:severity:attributes:", func(vmPtr interface{}, recv vm.Value, msgVal vm.Value, sevVal vm.Value, attrsVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		p := getProvider(v, recv)
		if p == nil {
			return telemFailure(v, "Not a Telemetry provider")
		}

		msg := telemToString(v, msgVal)
		sev := telemToString(v, sevVal)
		severity := severityFromString(sev)

		var rec otellog.Record
		rec.SetTimestamp(time.Now())
		rec.SetSeverity(severity)
		rec.SetBody(otellog.StringValue(msg))

		// Add attributes from dictionary.
		attrs := dictToAttributes(v, attrsVal)
		if len(attrs) > 0 {
			logAttrs := make([]otellog.KeyValue, len(attrs))
			for i, a := range attrs {
				logAttrs[i] = otellog.String(string(a.Key), a.Value.AsString())
			}
			rec.AddAttributes(logAttrs...)
		}

		p.otelLog.Emit(p.currentCtx(), rec)
		return vm.True
	})
}
