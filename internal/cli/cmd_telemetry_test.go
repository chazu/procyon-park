package cli

import (
	"encoding/json"
	"testing"

	"github.com/chazu/procyon-park/internal/ipc"
	"github.com/chazu/procyon-park/internal/telemetry"
)

// ---------- telemetry command tests ----------

func TestTelemetryCmd_NoSubcommand(t *testing.T) {
	resetFlags(t)
	code := ExecuteArgs([]string{"telemetry"})
	if code != ExitError {
		t.Fatalf("expected exit code %d, got %d", ExitError, code)
	}
}

func TestTelemetryCmd_UnknownSubcommand(t *testing.T) {
	resetFlags(t)
	code := ExecuteArgs([]string{"telemetry", "foo"})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code for unknown subcommand")
	}
}

// ---------- telemetry status ----------

func TestTelemetryStatusCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"telemetry", "status",
		"--socket", socketPath,
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestTelemetryStatusCmd_WithMock(t *testing.T) {
	resetFlags(t)
	sockPath := startMockDaemon(t, map[string]methodHandler{
		"telemetry.status": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`{
				"receiver_running": true,
				"grpc_addr": "127.0.0.1:4317",
				"http_addr": "127.0.0.1:4318",
				"span_count": 42,
				"metric_count": 10,
				"log_count": 5,
				"has_warm_data": false,
				"warm_dir": "/tmp/warm"
			}`), nil
		},
	})
	code := ExecuteArgs([]string{"telemetry", "status",
		"--socket", sockPath,
	})
	if code != ExitSuccess {
		t.Fatalf("expected exit code %d, got %d", ExitSuccess, code)
	}
}

func TestTelemetryStatusCmd_JSONOutput(t *testing.T) {
	resetFlags(t)
	sockPath := startMockDaemon(t, map[string]methodHandler{
		"telemetry.status": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`{"receiver_running": true}`), nil
		},
	})
	code := ExecuteArgs([]string{"telemetry", "status",
		"--socket", sockPath,
		"--output", "json",
	})
	if code != ExitSuccess {
		t.Fatalf("expected exit code %d, got %d", ExitSuccess, code)
	}
}

// ---------- telemetry search ----------

func TestTelemetrySearchCmd_NoSubcommand(t *testing.T) {
	resetFlags(t)
	code := ExecuteArgs([]string{"telemetry", "search"})
	if code != ExitError {
		t.Fatalf("expected exit code %d, got %d", ExitError, code)
	}
}

// ---------- telemetry search spans ----------

func TestTelemetrySearchSpansCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"telemetry", "search", "spans",
		"--socket", socketPath,
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestTelemetrySearchSpansCmd_WithFlags(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"telemetry", "search", "spans",
		"--socket", socketPath,
		"--agent", "Lark",
		"--task", "test-1",
		"--name", "http",
		"--min-duration", "100ms",
		"--limit", "50",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestTelemetrySearchSpansCmd_WithMock(t *testing.T) {
	resetFlags(t)
	sockPath := startMockDaemon(t, map[string]methodHandler{
		"telemetry.query": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			var cmd telemetry.QueryCommand
			json.Unmarshal(params, &cmd)
			if cmd.Operation != "search_spans" {
				return nil, &ipc.Error{Code: -32602, Message: "unexpected operation"}
			}
			return json.RawMessage(`{"spans":[{"trace_id":"abc123","span_id":"def456","name":"test-span","duration_ns":1000000,"agent_name":"Lark","task_id":"t1"}]}`), nil
		},
	})
	code := ExecuteArgs([]string{"telemetry", "search", "spans",
		"--socket", sockPath,
		"--agent", "Lark",
	})
	if code != ExitSuccess {
		t.Fatalf("expected exit code %d, got %d", ExitSuccess, code)
	}
}

func TestTelemetrySearchSpansCmd_InvalidDuration(t *testing.T) {
	resetFlags(t)
	sockPath := startMockDaemon(t, map[string]methodHandler{})
	code := ExecuteArgs([]string{"telemetry", "search", "spans",
		"--socket", sockPath,
		"--min-duration", "notaduration",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code for invalid duration")
	}
}

// ---------- telemetry search logs ----------

func TestTelemetrySearchLogsCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"telemetry", "search", "logs",
		"--socket", socketPath,
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestTelemetrySearchLogsCmd_WithFlags(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"telemetry", "search", "logs",
		"--socket", socketPath,
		"--agent", "Cog",
		"--body", "error",
		"--severity", "9",
		"--limit", "25",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestTelemetrySearchLogsCmd_WithMock(t *testing.T) {
	resetFlags(t)
	sockPath := startMockDaemon(t, map[string]methodHandler{
		"telemetry.query": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			var cmd telemetry.QueryCommand
			json.Unmarshal(params, &cmd)
			if cmd.Operation != "search_logs" {
				return nil, &ipc.Error{Code: -32602, Message: "unexpected operation"}
			}
			return json.RawMessage(`{"logs":[{"time_ns":1700000000000000000,"severity_text":"ERROR","agent_name":"Cog","body":"something failed"}]}`), nil
		},
	})
	code := ExecuteArgs([]string{"telemetry", "search", "logs",
		"--socket", sockPath,
		"--severity", "9",
	})
	if code != ExitSuccess {
		t.Fatalf("expected exit code %d, got %d", ExitSuccess, code)
	}
}

// ---------- telemetry trace ----------

func TestTelemetryTraceCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"telemetry", "trace",
		"--socket", socketPath,
		"abc123",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestTelemetryTraceCmd_MissingArg(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"telemetry", "trace",
		"--socket", socketPath,
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code for missing trace-id")
	}
}

func TestTelemetryTraceCmd_WithMock(t *testing.T) {
	resetFlags(t)
	sockPath := startMockDaemon(t, map[string]methodHandler{
		"telemetry.query": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			var cmd telemetry.QueryCommand
			json.Unmarshal(params, &cmd)
			if cmd.Operation != "lookup_trace" || cmd.TraceID != "abc123" {
				return nil, &ipc.Error{Code: -32602, Message: "unexpected params"}
			}
			return json.RawMessage(`{"spans":[{"trace_id":"abc123","span_id":"s1","name":"root","duration_ns":5000000,"status_code":1}]}`), nil
		},
	})
	code := ExecuteArgs([]string{"telemetry", "trace",
		"--socket", sockPath,
		"abc123",
	})
	if code != ExitSuccess {
		t.Fatalf("expected exit code %d, got %d", ExitSuccess, code)
	}
}

// ---------- telemetry metrics ----------

func TestTelemetryMetricsCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"telemetry", "metrics",
		"--socket", socketPath,
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestTelemetryMetricsCmd_WithFlags(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"telemetry", "metrics",
		"--socket", socketPath,
		"--name", "http.requests",
		"--agg", "AVG",
		"--agent", "Lark",
		"--group-by", "agent_name",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestTelemetryMetricsCmd_WithMock(t *testing.T) {
	resetFlags(t)
	sockPath := startMockDaemon(t, map[string]methodHandler{
		"telemetry.query": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			var cmd telemetry.QueryCommand
			json.Unmarshal(params, &cmd)
			if cmd.Operation != "aggregate_metrics" {
				return nil, &ipc.Error{Code: -32602, Message: "unexpected operation"}
			}
			return json.RawMessage(`{"metrics":[{"group_key":"Lark","value":42.5}]}`), nil
		},
	})
	code := ExecuteArgs([]string{"telemetry", "metrics",
		"--socket", sockPath,
		"--agg", "SUM",
		"--group-by", "agent_name",
	})
	if code != ExitSuccess {
		t.Fatalf("expected exit code %d, got %d", ExitSuccess, code)
	}
}

// ---------- telemetry export ----------

func TestTelemetryExportCmd_NoDaemon(t *testing.T) {
	resetFlags(t)
	socketPath := t.TempDir() + "/no.sock"
	code := ExecuteArgs([]string{"telemetry", "export",
		"--socket", socketPath,
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code when no daemon running")
	}
}

func TestTelemetryExportCmd_InvalidFormat(t *testing.T) {
	resetFlags(t)
	sockPath := startMockDaemon(t, map[string]methodHandler{})
	code := ExecuteArgs([]string{"telemetry", "export",
		"--socket", sockPath,
		"--format", "csv",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code for invalid format")
	}
}

func TestTelemetryExportCmd_InvalidSignal(t *testing.T) {
	resetFlags(t)
	sockPath := startMockDaemon(t, map[string]methodHandler{})
	code := ExecuteArgs([]string{"telemetry", "export",
		"--socket", sockPath,
		"--signal", "traces",
	})
	if code == ExitSuccess {
		t.Fatal("expected non-zero exit code for invalid signal")
	}
}

func TestTelemetryExportCmd_WithMock(t *testing.T) {
	resetFlags(t)
	sockPath := startMockDaemon(t, map[string]methodHandler{
		"telemetry.export": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`{"path":"/tmp/export.json","format":"json","signal":"spans","records":10}`), nil
		},
	})
	code := ExecuteArgs([]string{"telemetry", "export",
		"--socket", sockPath,
		"--format", "json",
		"--signal", "spans",
	})
	if code != ExitSuccess {
		t.Fatalf("expected exit code %d, got %d", ExitSuccess, code)
	}
}

func TestTelemetryExportCmd_ParquetFormat(t *testing.T) {
	resetFlags(t)
	sockPath := startMockDaemon(t, map[string]methodHandler{
		"telemetry.export": func(params json.RawMessage) (json.RawMessage, *ipc.Error) {
			return json.RawMessage(`{"path":"/tmp/export.parquet","format":"parquet","signal":"spans","records":5}`), nil
		},
	})
	code := ExecuteArgs([]string{"telemetry", "export",
		"--socket", sockPath,
		"--format", "parquet",
		"--signal", "spans",
	})
	if code != ExitSuccess {
		t.Fatalf("expected exit code %d, got %d", ExitSuccess, code)
	}
}

// ---------- helper function tests ----------

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		ns   int64
		want string
	}{
		{500, "500ns"},
		{1500, "1.5us"},
		{1_500_000, "1.5ms"},
		{1_500_000_000, "1.50s"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.ns)
		if got != tt.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tt.ns, got, tt.want)
		}
	}
}

func TestFormatNsTime(t *testing.T) {
	got := formatNsTime(0)
	if got != "" {
		t.Errorf("formatNsTime(0) = %q, want empty", got)
	}

	got = formatNsTime(1700000000000000000)
	if got == "" {
		t.Error("formatNsTime(1700000000000000000) should not be empty")
	}
}
