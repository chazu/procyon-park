package telemetry

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

func startTestReceiver(t *testing.T) *Receiver {
	t.Helper()
	r := NewReceiver(ReceiverConfig{
		GRPCAddr: "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
	})
	if err := r.Start(); err != nil {
		t.Fatalf("start receiver: %v", err)
	}
	t.Cleanup(r.Stop)
	return r
}

func TestReceiverStartStop(t *testing.T) {
	r := startTestReceiver(t)

	if addr := r.GRPCAddr(); addr == "" {
		t.Fatal("expected non-empty gRPC address")
	}
	if addr := r.HTTPAddr(); addr == "" {
		t.Fatal("expected non-empty HTTP address")
	}

	// Double-stop should be safe (sync.Once).
	r.Stop()
	r.Stop()
}

func TestGRPCTraceIngestion(t *testing.T) {
	r := startTestReceiver(t)

	conn, err := grpc.NewClient(r.GRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	defer conn.Close()

	client := coltracepb.NewTraceServiceClient(conn)
	req := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{{
					Key:   "service.name",
					Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "test"}},
				}},
			},
		}},
	}

	_, err = client.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("export traces: %v", err)
	}

	select {
	case sig := <-r.Traces:
		if sig.Type != SignalTraces {
			t.Fatalf("expected SignalTraces, got %d", sig.Type)
		}
		got := sig.Payload.(*coltracepb.ExportTraceServiceRequest)
		if len(got.ResourceSpans) != 1 {
			t.Fatalf("expected 1 resource span, got %d", len(got.ResourceSpans))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for trace signal")
	}
}

func TestGRPCMetricsIngestion(t *testing.T) {
	r := startTestReceiver(t)

	conn, err := grpc.NewClient(r.GRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	defer conn.Close()

	client := colmetricspb.NewMetricsServiceClient(conn)
	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource: &resourcepb.Resource{},
		}},
	}

	_, err = client.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("export metrics: %v", err)
	}

	select {
	case sig := <-r.Metrics:
		if sig.Type != SignalMetrics {
			t.Fatalf("expected SignalMetrics, got %d", sig.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for metrics signal")
	}
}

func TestGRPCLogsIngestion(t *testing.T) {
	r := startTestReceiver(t)

	conn, err := grpc.NewClient(r.GRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	defer conn.Close()

	client := collogspb.NewLogsServiceClient(conn)
	req := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource: &resourcepb.Resource{},
		}},
	}

	_, err = client.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("export logs: %v", err)
	}

	select {
	case sig := <-r.Logs:
		if sig.Type != SignalLogs {
			t.Fatalf("expected SignalLogs, got %d", sig.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for logs signal")
	}
}

func TestHTTPTraceIngestion(t *testing.T) {
	r := startTestReceiver(t)

	req := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{},
		}},
	}
	body, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp, err := http.Post(
		"http://"+r.HTTPAddr()+"/v1/traces",
		"application/x-protobuf",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("http post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	select {
	case sig := <-r.Traces:
		if sig.Type != SignalTraces {
			t.Fatalf("expected SignalTraces, got %d", sig.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for trace signal")
	}
}

func TestHTTPMetricsIngestion(t *testing.T) {
	r := startTestReceiver(t)

	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource: &resourcepb.Resource{},
		}},
	}
	body, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp, err := http.Post(
		"http://"+r.HTTPAddr()+"/v1/metrics",
		"application/x-protobuf",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("http post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	select {
	case sig := <-r.Metrics:
		if sig.Type != SignalMetrics {
			t.Fatalf("expected SignalMetrics, got %d", sig.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for metrics signal")
	}
}

func TestHTTPLogsIngestion(t *testing.T) {
	r := startTestReceiver(t)

	req := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource: &resourcepb.Resource{},
		}},
	}
	body, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp, err := http.Post(
		"http://"+r.HTTPAddr()+"/v1/logs",
		"application/x-protobuf",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("http post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	select {
	case sig := <-r.Logs:
		if sig.Type != SignalLogs {
			t.Fatalf("expected SignalLogs, got %d", sig.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for logs signal")
	}
}

func TestHTTPMethodNotAllowed(t *testing.T) {
	r := startTestReceiver(t)

	resp, err := http.Get("http://" + r.HTTPAddr() + "/v1/traces")
	if err != nil {
		t.Fatalf("http get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestHTTPUnsupportedContentType(t *testing.T) {
	r := startTestReceiver(t)

	resp, err := http.Post(
		"http://"+r.HTTPAddr()+"/v1/traces",
		"application/json",
		bytes.NewReader([]byte("{}")),
	)
	if err != nil {
		t.Fatalf("http post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", resp.StatusCode)
	}
}

func TestHTTPBodyTooLarge(t *testing.T) {
	r := startTestReceiver(t)

	bigBody := make([]byte, MaxBodySize+1)
	resp, err := http.Post(
		"http://"+r.HTTPAddr()+"/v1/traces",
		"application/x-protobuf",
		bytes.NewReader(bigBody),
	)
	if err != nil {
		t.Fatalf("http post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", resp.StatusCode)
	}
}

func TestHTTPInvalidProtobuf(t *testing.T) {
	r := startTestReceiver(t)

	resp, err := http.Post(
		"http://"+r.HTTPAddr()+"/v1/traces",
		"application/x-protobuf",
		bytes.NewReader([]byte("not protobuf")),
	)
	if err != nil {
		t.Fatalf("http post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestBackpressureDrop(t *testing.T) {
	r := NewReceiver(ReceiverConfig{
		GRPCAddr: "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
	})
	if err := r.Start(); err != nil {
		t.Fatalf("start receiver: %v", err)
	}
	defer r.Stop()

	// Fill the traces channel to capacity.
	for i := 0; i < ChannelCapacity; i++ {
		r.Traces <- Signal{Type: SignalTraces}
	}

	// Next emit should drop without blocking.
	done := make(chan struct{})
	go func() {
		r.emit(Signal{Type: SignalTraces, Payload: &coltracepb.ExportTraceServiceRequest{}})
		close(done)
	}()

	select {
	case <-done:
		// Non-blocking — correct behavior.
	case <-time.After(time.Second):
		t.Fatal("emit blocked on full channel — expected drop")
	}

	// Channel should still be at capacity (nothing extra got in).
	if len(r.Traces) != ChannelCapacity {
		t.Fatalf("expected channel len %d, got %d", ChannelCapacity, len(r.Traces))
	}
}
