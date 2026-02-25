package telemetry

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

const (
	// DefaultGRPCAddr is the default gRPC listen address for OTLP.
	DefaultGRPCAddr = "127.0.0.1:4317"
	// DefaultHTTPAddr is the default HTTP listen address for OTLP.
	DefaultHTTPAddr = "127.0.0.1:4318"
	// ChannelCapacity is the buffer size for signal output channels.
	ChannelCapacity = 256
	// MaxBodySize is the maximum HTTP request body size (4 MiB).
	MaxBodySize = 4 << 20
)

// SignalType identifies which OTLP signal a payload belongs to.
type SignalType int

const (
	SignalTraces SignalType = iota
	SignalMetrics
	SignalLogs
)

// Signal wraps a decoded OTLP protobuf message with its type.
type Signal struct {
	Type    SignalType
	Payload proto.Message
}

// ReceiverConfig holds configuration for the OTLP receiver.
type ReceiverConfig struct {
	GRPCAddr string
	HTTPAddr string
}

// DefaultReceiverConfig returns the default OTLP receiver configuration.
func DefaultReceiverConfig() ReceiverConfig {
	return ReceiverConfig{
		GRPCAddr: DefaultGRPCAddr,
		HTTPAddr: DefaultHTTPAddr,
	}
}

// Receiver is an embedded OTLP receiver accepting traces, metrics, and logs
// over gRPC and HTTP transports. Decoded signals are sent to buffered output
// channels. Writes are non-blocking; signals are dropped when channels are full.
type Receiver struct {
	cfg ReceiverConfig

	Traces  chan Signal
	Metrics chan Signal
	Logs    chan Signal

	grpcServer *grpc.Server
	httpServer *http.Server

	grpcLis net.Listener
	httpLis net.Listener

	wg   sync.WaitGroup
	once sync.Once
}

// NewReceiver creates a new OTLP receiver with the given config.
func NewReceiver(cfg ReceiverConfig) *Receiver {
	return &Receiver{
		cfg:     cfg,
		Traces:  make(chan Signal, ChannelCapacity),
		Metrics: make(chan Signal, ChannelCapacity),
		Logs:    make(chan Signal, ChannelCapacity),
	}
}

// Start binds gRPC and HTTP listeners and begins serving. It returns after
// both servers are accepting connections.
func (r *Receiver) Start() error {
	var err error

	// gRPC setup
	r.grpcLis, err = net.Listen("tcp", r.cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("otlp: listen grpc %s: %w", r.cfg.GRPCAddr, err)
	}
	r.grpcServer = grpc.NewServer()
	coltracepb.RegisterTraceServiceServer(r.grpcServer, &traceService{r: r})
	colmetricspb.RegisterMetricsServiceServer(r.grpcServer, &metricsService{r: r})
	collogspb.RegisterLogsServiceServer(r.grpcServer, &logsService{r: r})

	// HTTP setup
	r.httpLis, err = net.Listen("tcp", r.cfg.HTTPAddr)
	if err != nil {
		r.grpcLis.Close()
		return fmt.Errorf("otlp: listen http %s: %w", r.cfg.HTTPAddr, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", r.handleTraces)
	mux.HandleFunc("/v1/metrics", r.handleMetrics)
	mux.HandleFunc("/v1/logs", r.handleLogs)
	r.httpServer = &http.Server{Handler: mux}

	r.wg.Add(2)
	go func() {
		defer r.wg.Done()
		r.grpcServer.Serve(r.grpcLis)
	}()
	go func() {
		defer r.wg.Done()
		if err := r.httpServer.Serve(r.httpLis); err != http.ErrServerClosed {
			// logged but not propagated — Stop() is the clean path
		}
	}()

	return nil
}

// GRPCAddr returns the actual gRPC listener address (useful when using :0).
func (r *Receiver) GRPCAddr() string {
	if r.grpcLis == nil {
		return ""
	}
	return r.grpcLis.Addr().String()
}

// HTTPAddr returns the actual HTTP listener address (useful when using :0).
func (r *Receiver) HTTPAddr() string {
	if r.httpLis == nil {
		return ""
	}
	return r.httpLis.Addr().String()
}

// Stop gracefully shuts down both servers and waits for goroutines to finish.
func (r *Receiver) Stop() {
	r.once.Do(func() {
		if r.grpcServer != nil {
			r.grpcServer.GracefulStop()
		}
		if r.httpServer != nil {
			r.httpServer.Shutdown(context.Background())
		}
		r.wg.Wait()
	})
}

// emit sends a signal to the appropriate channel without blocking.
// If the channel is full, the signal is dropped (backpressure via drop).
func (r *Receiver) emit(sig Signal) {
	switch sig.Type {
	case SignalTraces:
		select {
		case r.Traces <- sig:
		default:
		}
	case SignalMetrics:
		select {
		case r.Metrics <- sig:
		default:
		}
	case SignalLogs:
		select {
		case r.Logs <- sig:
		default:
		}
	}
}

// ---------------------------------------------------------------------------
// gRPC service implementations
// ---------------------------------------------------------------------------

type traceService struct {
	coltracepb.UnimplementedTraceServiceServer
	r *Receiver
}

func (s *traceService) Export(_ context.Context, req *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	s.r.emit(Signal{Type: SignalTraces, Payload: req})
	return &coltracepb.ExportTraceServiceResponse{}, nil
}

type metricsService struct {
	colmetricspb.UnimplementedMetricsServiceServer
	r *Receiver
}

func (s *metricsService) Export(_ context.Context, req *colmetricspb.ExportMetricsServiceRequest) (*colmetricspb.ExportMetricsServiceResponse, error) {
	s.r.emit(Signal{Type: SignalMetrics, Payload: req})
	return &colmetricspb.ExportMetricsServiceResponse{}, nil
}

type logsService struct {
	collogspb.UnimplementedLogsServiceServer
	r *Receiver
}

func (s *logsService) Export(_ context.Context, req *collogspb.ExportLogsServiceRequest) (*collogspb.ExportLogsServiceResponse, error) {
	s.r.emit(Signal{Type: SignalLogs, Payload: req})
	return &collogspb.ExportLogsServiceResponse{}, nil
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

func (r *Receiver) handleTraces(w http.ResponseWriter, req *http.Request) {
	r.handleHTTP(w, req, SignalTraces, func() proto.Message { return &coltracepb.ExportTraceServiceRequest{} })
}

func (r *Receiver) handleMetrics(w http.ResponseWriter, req *http.Request) {
	r.handleHTTP(w, req, SignalMetrics, func() proto.Message { return &colmetricspb.ExportMetricsServiceRequest{} })
}

func (r *Receiver) handleLogs(w http.ResponseWriter, req *http.Request) {
	r.handleHTTP(w, req, SignalLogs, func() proto.Message { return &collogspb.ExportLogsServiceRequest{} })
}

func (r *Receiver) handleHTTP(w http.ResponseWriter, req *http.Request, sigType SignalType, newMsg func() proto.Message) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ct := req.Header.Get("Content-Type")
	if ct != "application/x-protobuf" {
		http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
		return
	}

	body, err := io.ReadAll(io.LimitReader(req.Body, MaxBodySize+1))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	if len(body) > MaxBodySize {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}

	msg := newMsg()
	if err := proto.Unmarshal(body, msg); err != nil {
		http.Error(w, "invalid protobuf", http.StatusBadRequest)
		return
	}

	r.emit(Signal{Type: sigType, Payload: msg})
	w.WriteHeader(http.StatusOK)
}
