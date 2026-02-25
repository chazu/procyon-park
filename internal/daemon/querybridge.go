// querybridge.go registers JSON-RPC handlers for the unified telemetry query
// engine. These handlers bridge CLI commands to QueryEngine operations.
package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chazu/procyon-park/internal/telemetry"
)

// TelemetryComponents holds references to all telemetry subsystems for the
// daemon bridge. Fields may be nil if the component is not running.
type TelemetryComponents struct {
	Engine   *telemetry.QueryEngine
	Receiver *telemetry.Receiver
	Pipeline *telemetry.Pipeline
	Flusher  *telemetry.Flusher
	WarmDir  string
}

// RegisterQueryHandlers wires the telemetry.query JSON-RPC method.
// Must be called before the IPCServer is started.
func RegisterQueryHandlers(srv *IPCServer, engine *telemetry.QueryEngine) {
	srv.Handle("telemetry.query", handleTelemetryQuery(engine))
}

// RegisterTelemetryHandlers wires all telemetry.* JSON-RPC methods including
// query, status, and export. Must be called before the IPCServer is started.
func RegisterTelemetryHandlers(srv *IPCServer, tc TelemetryComponents) {
	if tc.Engine != nil {
		srv.Handle("telemetry.query", handleTelemetryQuery(tc.Engine))
	}
	srv.Handle("telemetry.status", handleTelemetryStatus(tc))
	srv.Handle("telemetry.export", handleTelemetryExport(tc))
}

func handleTelemetryQuery(engine *telemetry.QueryEngine) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var cmd telemetry.QueryCommand
		if err := json.Unmarshal(params, &cmd); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}
		if cmd.Operation == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "operation is required"}
		}
		result, err := engine.HandleQuery(cmd)
		if err != nil {
			return nil, fmt.Errorf("telemetry.query: %w", err)
		}
		return result, nil
	}
}

// telemetryStatusResult is the JSON response for telemetry.status.
type telemetryStatusResult struct {
	ReceiverRunning bool   `json:"receiver_running"`
	GRPCAddr        string `json:"grpc_addr"`
	HTTPAddr        string `json:"http_addr"`
	SpanCount       int64  `json:"span_count"`
	MetricCount     int64  `json:"metric_count"`
	LogCount        int64  `json:"log_count"`
	HasWarmData     bool   `json:"has_warm_data"`
	WarmDir         string `json:"warm_dir"`
	LastFlushTime   string `json:"last_flush_time"`
}

func handleTelemetryStatus(tc TelemetryComponents) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		result := telemetryStatusResult{
			WarmDir: tc.WarmDir,
		}

		if tc.Receiver != nil {
			result.ReceiverRunning = true
			result.GRPCAddr = tc.Receiver.GRPCAddr()
			result.HTTPAddr = tc.Receiver.HTTPAddr()
		}

		if tc.Pipeline != nil {
			db := tc.Pipeline.DB()
			var count int64
			if err := db.QueryRow("SELECT COUNT(*) FROM otel_spans").Scan(&count); err == nil {
				result.SpanCount = count
			}
			if err := db.QueryRow("SELECT COUNT(*) FROM otel_metrics").Scan(&count); err == nil {
				result.MetricCount = count
			}
			if err := db.QueryRow("SELECT COUNT(*) FROM otel_logs").Scan(&count); err == nil {
				result.LogCount = count
			}
		}

		if tc.Engine != nil {
			result.HasWarmData = tc.Engine.HasWarmData()
		}

		if tc.Flusher != nil {
			if t := tc.Flusher.LastFlushTime(); !t.IsZero() {
				result.LastFlushTime = t.UTC().Format(time.RFC3339)
			}
		}

		return result, nil
	}
}

// telemetryExportParams is the JSON request for telemetry.export.
type telemetryExportParams struct {
	Format string `json:"format"` // "json" or "parquet"
	Signal string `json:"signal"` // "spans", "metrics", "logs"
	Output string `json:"output"` // output file path (optional)
}

// telemetryExportResult is the JSON response for telemetry.export.
type telemetryExportResult struct {
	Path    string `json:"path"`
	Format  string `json:"format"`
	Signal  string `json:"signal"`
	Records int64  `json:"records"`
}

func handleTelemetryExport(tc TelemetryComponents) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p telemetryExportParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}

		if tc.Engine == nil {
			return nil, fmt.Errorf("telemetry.export: query engine not available")
		}

		switch p.Format {
		case "json", "parquet":
		default:
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "format must be json or parquet"}
		}

		// Map signal name to query operation.
		var operation string
		switch p.Signal {
		case "spans":
			operation = "search_spans"
		case "logs":
			operation = "search_logs"
		case "metrics":
			operation = "aggregate_metrics"
		default:
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "signal must be spans, metrics, or logs"}
		}

		// For parquet export, use DuckDB COPY TO directly.
		if p.Format == "parquet" {
			return exportParquet(tc, p)
		}

		// For JSON export, query all data and write as JSON.
		return exportJSON(tc, p, operation)
	}
}

func exportParquet(tc TelemetryComponents, p telemetryExportParams) (*telemetryExportResult, error) {
	if tc.Pipeline == nil {
		return nil, fmt.Errorf("telemetry.export: pipeline not available for parquet export")
	}

	tableMap := map[string]string{
		"spans":   "otel_spans",
		"metrics": "otel_metrics",
		"logs":    "otel_logs",
	}
	table := tableMap[p.Signal]

	outPath := p.Output
	if outPath == "" {
		outPath = filepath.Join(os.TempDir(), fmt.Sprintf("pp-export-%s-%d.parquet", p.Signal, time.Now().Unix()))
	}

	db := tc.Pipeline.DB()

	// Count records first.
	var count int64
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		return nil, fmt.Errorf("telemetry.export: count %s: %w", table, err)
	}

	if count == 0 {
		return &telemetryExportResult{
			Format:  "parquet",
			Signal:  p.Signal,
			Records: 0,
		}, nil
	}

	copySQL := fmt.Sprintf("COPY %s TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)", table, outPath)
	if _, err := db.Exec(copySQL); err != nil {
		return nil, fmt.Errorf("telemetry.export: copy %s: %w", table, err)
	}

	return &telemetryExportResult{
		Path:    outPath,
		Format:  "parquet",
		Signal:  p.Signal,
		Records: count,
	}, nil
}

func exportJSON(tc TelemetryComponents, p telemetryExportParams, operation string) (*telemetryExportResult, error) {
	// Query with large limit to get all data.
	var cmd telemetry.QueryCommand
	switch operation {
	case "search_spans":
		cmd = telemetry.QueryCommand{
			Operation: "search_spans",
			Spans:     &telemetry.SpanSearchParams{Limit: 10000},
		}
	case "search_logs":
		cmd = telemetry.QueryCommand{
			Operation: "search_logs",
			Logs:      &telemetry.LogSearchParams{Limit: 10000},
		}
	case "aggregate_metrics":
		cmd = telemetry.QueryCommand{
			Operation: "aggregate_metrics",
			Metrics: &telemetry.MetricAggregateParams{
				Function: "SUM",
				GroupBy:  "name",
			},
		}
	}

	result, err := tc.Engine.HandleQuery(cmd)
	if err != nil {
		return nil, fmt.Errorf("telemetry.export: query: %w", err)
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("telemetry.export: marshal: %w", err)
	}

	var recordCount int64
	if result.Spans != nil {
		recordCount = int64(len(result.Spans))
	} else if result.Logs != nil {
		recordCount = int64(len(result.Logs))
	} else if result.Metrics != nil {
		recordCount = int64(len(result.Metrics))
	}

	outPath := p.Output
	if outPath != "" {
		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			return nil, fmt.Errorf("telemetry.export: write %s: %w", outPath, err)
		}
	}

	return &telemetryExportResult{
		Path:    outPath,
		Format:  "json",
		Signal:  p.Signal,
		Records: recordCount,
	}, nil
}
