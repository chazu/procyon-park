---
date: 2026-02-23
researcher: Crumble
task: procyon-park-vkq
repository: procyon-park
topic: "Phase 8: Telemetry & OTEL Integration"
tags: [research, telemetry, otel, opentelemetry, duckdb, parquet, maggie]
status: complete
---

# Phase 8: Telemetry & OTEL Integration

## Executive Summary

This document covers the telemetry system design for procyon-park, based on analysis of imp-castle's existing `internal/telemetry/` and `internal/telemetry/otel/` packages. The design uses a two-tier architecture: a lightweight event store (BadgerDB/SQLite) for SDLC activity events, and a columnar analytics engine (DuckDB + Parquet) for OTEL signal ingestion. This combination provides both real-time event tracking and high-performance historical analytics.

---

## 1. SDLC Event Categories

The telemetry system captures activity across seven categories, each with specific event types.

| Category   | Event Types                                      | Volume   | Description                          |
|------------|--------------------------------------------------|----------|--------------------------------------|
| `agent`    | spawned, dismissed, stuck, recovered, heartbeat  | Low      | Agent lifecycle events               |
| `task`     | claimed, started, completed, failed, blocked     | Low      | Beads issue state changes            |
| `mail`     | sent, received, read                             | Medium   | Mail metadata (not content)          |
| `git`      | commit, push, pull, merge, conflict              | Medium   | VCS operations                       |
| `session`  | started, ended, idle, active                     | Low      | Agent session lifecycle              |
| `error`    | crash, timeout, retry, panic                     | Low      | Failure events                       |
| `workflow` | started, step_completed, gate_waiting, completed | Low-Med  | Workflow engine progress             |

### Event Schema

imp-castle defines a `TelemetryEvent` struct (in `internal/telemetry/telemetry.go`):

```go
type TelemetryEvent struct {
    ID        string            `json:"id"`         // Random hex, 16 chars
    Timestamp time.Time         `json:"timestamp"`
    EventType string            `json:"event_type"` // e.g. "spawned", "claimed"
    Category  EventCategory     `json:"category"`   // e.g. "agent", "task"
    Source    string            `json:"source"`      // Who generated it (agent name, command)
    Target    string            `json:"target"`      // What it affects (task ID, file path)
    Data      json.RawMessage   `json:"data"`        // Event-specific JSON payload
    NodeID    string            `json:"node_id"`     // Installation identifier
    SyncedAt  *time.Time        `json:"synced_at"`   // When synced to hub (nil = unsynced)
}
```

Key design decisions:
- **Flat schema**: No nested structures. Category + EventType give the two-level hierarchy.
- **Flexible `Data` field**: `json.RawMessage` allows event-specific payloads without schema bloat.
- **NodeID**: Supports multi-node deployments from day one.
- **SyncedAt**: Enables offline-first with eventual hub sync.

---

## 2. Event Storage: BadgerDB vs SQLite

imp-castle uses a `Store` interface (`internal/store/store.go`) that abstracts storage behind bucket/key operations. The actual implementation is SQLite (`SQLiteStore`), despite the original design targeting BadgerDB.

### Current Implementation (SQLite)

The `TelemetryStore` wraps the `Store` interface:

```go
type TelemetryStore struct {
    store  store.Store  // SQLite-backed
    config config       // RetentionDays (default 30)
}
```

**Key structure**: `telemetry/{RFC3339Nano timestamp}/{eventId}`

This timestamp-prefixed key enables efficient time-range iteration — the store's `Iterate()` method scans keys in order, so time-range queries naturally start/stop at the right boundaries.

### Storage Tradeoffs

| Dimension        | BadgerDB                                  | SQLite                                       |
|------------------|-------------------------------------------|----------------------------------------------|
| **Write speed**  | Very fast (LSM tree, batch writes)        | Fast (WAL mode), slightly slower for burst    |
| **Read pattern** | Sequential scan, prefix iteration         | Full SQL queries, indexes                     |
| **Disk usage**   | Value log + LSM; compaction needed        | Single file, predictable size                 |
| **Concurrency**  | Single writer, many readers               | Single writer, many readers (WAL mode)        |
| **Query power**  | Key-prefix scan only                      | Full SQL with JOINs, aggregation              |
| **Embedding**    | Pure Go, no CGo                           | Requires CGo (mattn/go-sqlite3) or WASM       |
| **Operational**  | Needs periodic compaction                 | Autovacuum, simpler operations                |

### Recommendation for procyon-park

**Use SQLite** for the event store. Reasons:
1. imp-castle already migrated from BadgerDB to SQLite for the store layer — follow the proven path.
2. Event queries benefit from SQL (filter by category, time range, source — all in one query).
3. The key-iteration pattern in `TelemetryStore.listEvents()` is a hand-rolled query engine — SQL would be cleaner.
4. Single-file deployment simplifies backup and inspection (`sqlite3 telemetry.db`).

The event store handles low-to-medium volume SDLC events (hundreds/day). SQLite handles this trivially.

---

## 3. OTEL Receiver: gRPC + HTTP Ingestion

imp-castle's `internal/telemetry/otel/` package implements a full OTLP receiver that accepts all three signal types.

### Architecture

```
                    ┌─────────────────────────────────┐
                    │        OTLP Receiver             │
                    │                                  │
  Agent/Process ───►│  gRPC :4317  ──► Traces chan     │
                    │  HTTP :4318  ──► Metrics chan     │──► Pipeline ──► DuckDB ──► Parquet
  Claude Code ─────►│               ──► Logs chan       │
                    │                                  │
                    └─────────────────────────────────┘
```

### Receiver Design (`receiver.go`)

- **Dual transport**: gRPC (port 4317) and HTTP (port 4318), per OTLP specification.
- **Localhost-only binding**: `127.0.0.1` — no external exposure.
- **Buffered channels**: Output channels buffer up to 256 messages (configurable).
- **Non-blocking writes**: If channels are full, batches are dropped with a debug log (backpressure via drop, not block).
- **Protobuf native**: Uses `go.opentelemetry.io/proto/otlp/collector/{traces,metrics,logs}/v1` for gRPC service registration and HTTP protobuf deserialization.

### HTTP Endpoints

| Endpoint       | Method | Content-Type               | Description       |
|----------------|--------|----------------------------|-------------------|
| `/v1/traces`   | POST   | `application/x-protobuf`   | Span data         |
| `/v1/metrics`  | POST   | `application/x-protobuf`   | Metric data       |
| `/v1/logs`     | POST   | `application/x-protobuf`   | Log records        |

Body size limit: 4 MiB (`maxHTTPBodySize`).

### Key Design Decisions

1. **Embedded, not standalone**: The receiver runs inside the pp daemon, not as a separate OTEL Collector. This avoids deploying/configuring a separate binary.
2. **Channel-based pipeline**: Receiver → channels → Pipeline consumer. Clean separation of ingestion from storage.
3. **GracefulStop**: gRPC uses `GracefulStop()`, HTTP uses `Shutdown()` — in-flight requests complete before channels close.

---

## 4. Storage Pipeline: DuckDB + Parquet

### Pipeline Architecture (`pipeline.go`)

```
  Receiver Channels ──► consumeLoop() ──► DuckDB (in-memory)
                                               │
                                          flushLoop() (5 min)
                                               │
                                               ▼
                                    ~/.imp-castle/telemetry/warm/
                                        └── 2026-02/
                                            ├── spans.parquet
                                            ├── metrics.parquet
                                            └── logs.parquet
```

- **In-memory DuckDB**: Receives converted protobuf data. Provides immediate query access.
- **Periodic flush**: Every 5 minutes (`DefaultFlushInterval`), in-memory data is exported to Parquet with ZSTD compression and purged from DuckDB.
- **Time-partitioned**: Files are organized by `YYYY-MM` directories.
- **Final flush on stop**: `Stop()` performs one last flush before closing DuckDB.

### Schema Design (`schema.go`)

Three DuckDB tables optimized for columnar analytics:

#### `otel_spans`
- **Trace identity**: trace_id, span_id, parent_span_id, trace_state
- **Span metadata**: name, kind, start/end time (unix nanos), duration_ns, status
- **imp-castle correlation**: agent_name, task_id, repo_name (extracted from OTEL resource/span attributes)
- **LLM-specific (Gen AI semantic conventions)**: model_name, prompt_tokens, completion_tokens, total_tokens, estimated_cost_usd
- **Flexible storage**: attributes, resource_attributes, events, links as JSON strings

#### `otel_metrics`
- **Single table for all metric types**: gauge, sum, histogram
- **Type-specific nullable columns**: value_double/value_int for gauge/sum, histogram_count/sum/min/max/buckets for histograms
- **Aggregation metadata**: aggregation_temporality, is_monotonic

#### `otel_logs`
- **Severity**: severity_number (OTEL 1-24 scale), severity_text
- **Trace correlation**: trace_id, span_id (logs linked to spans)
- **Body**: Log message as string

### Protobuf → Schema Conversion (`convert.go`)

The conversion layer extracts imp-castle correlation attributes from OTEL resource and span/metric/log attributes:

| OTEL Attribute Key         | Schema Column   | Description            |
|----------------------------|-----------------|------------------------|
| `cub.agent.name`           | agent_name      | Agent that emitted     |
| `cub.task.id`              | task_id         | Associated beads task  |
| `cub.repository.name`      | repo_name       | Repository context     |
| `gen_ai.request.model`     | model_name      | LLM model used (spans) |

Attributes are checked at resource level first, then at signal level (span/datapoint/log record) as fallback. This allows processes to set correlation once at the resource level.

---

## 5. Query Engine for Historical Analysis

### Unified Query Layer (`query.go`)

The `QueryEngine` presents a unified view over both in-memory DuckDB and warm-tier Parquet files:

```go
type QueryEngine struct {
    db      *sql.DB   // In-memory DuckDB (unflushed data)
    warmDir string    // Parquet file directory
}
```

The `unionQuery()` method builds a CTE that unions both sources:

```sql
WITH combined AS (
    SELECT * FROM otel_spans              -- in-memory
    UNION ALL
    SELECT * FROM read_parquet('.../*.parquet')  -- warm tier
)
SELECT ... FROM combined WHERE ...
```

This is transparent to callers — queries always return the full dataset.

### Query Operations

| Operation            | Method                | Description                                |
|----------------------|-----------------------|--------------------------------------------|
| Trace lookup         | `LookupTrace(id)`     | Get all spans for a trace ID               |
| Span search          | `SearchSpans(params)` | Filter by name, agent, task, duration, time |
| Metric aggregation   | `AggregateMetrics(p)` | SUM/AVG/MAX/MIN/COUNT with GROUP BY        |
| Log search           | `SearchLogs(params)`  | Filter by body, severity, trace, agent      |

### IPC Integration

The `HandleQuery()` method dispatches JSON command requests, enabling CLI tools to query telemetry through the daemon's IPC socket:

```json
{"command": "span_search", "params": {"agent_name": "Sprocket", "limit": 10}}
```

### Standalone Mode

`NewStandaloneQueryEngine()` opens a fresh DuckDB connection for read-only Parquet queries. This lets CLI tools query warm-tier data without a running pipeline (e.g., `cub stats` commands).

---

## 6. Parquet Export

### Export Strategy

- **Format**: Apache Parquet with ZSTD compression via DuckDB's `COPY ... TO ... (FORMAT PARQUET, COMPRESSION ZSTD)`.
- **Partitioning**: `{warmDir}/{YYYY-MM}/{signal}.parquet` — monthly partitions per signal type.
- **Export-then-purge**: Data is exported before deletion from DuckDB. A crash between export and purge means duplicate data (safe) rather than lost data.
- **Read-back**: DuckDB's `read_parquet()` with glob patterns enables seamless querying across all partitions.

### Why Parquet + DuckDB

| Benefit                    | Detail                                                        |
|----------------------------|---------------------------------------------------------------|
| **Columnar efficiency**    | Telemetry queries typically access few columns across many rows |
| **Compression**            | ZSTD on Parquet achieves excellent compression for string-heavy OTEL data |
| **No external service**    | DuckDB is embedded — no PostgreSQL/ClickHouse to manage       |
| **Standard format**        | Parquet files are readable by Pandas, Spark, DuckDB CLI, etc.  |
| **Append-friendly**        | New partitions are created; old files are never modified        |

---

## 7. How Maggie Processes Can Emit Telemetry Natively

Maggie (the Smalltalk-inspired language) currently has no OTEL integration. Here's how Maggie processes could emit telemetry natively.

### Approach: OTEL SDK via Go FFI

Since Maggie's VM is implemented in Go, the most natural approach is to expose OTEL primitives as built-in Maggie methods via the existing FFI/primitive mechanism.

#### Option A: Direct OTEL SDK Integration (Recommended)

Add OTEL trace/metric/log primitives to Maggie's VM:

```smalltalk
"Maggie process emitting a span"
Telemetry span: 'task.process' do: [
    | result |
    result := self processWork.
    Telemetry setAttribute: 'cub.task.id' value: taskId.
    Telemetry setAttribute: 'result.status' value: result status.
    result
].

"Maggie process emitting a metric"
Telemetry counter: 'maggie.tasks.processed' increment: 1
    attributes: { 'task.type' -> 'build'. 'agent' -> agentName }.

"Maggie process emitting a log"
Telemetry log: 'Processing started' severity: #info
    attributes: { 'task.id' -> taskId }.
```

Implementation in Go:

```go
// In vm/primitives_telemetry.go
func (v *VM) primTelemetrySpan(name string, block func() any) any {
    ctx, span := v.tracer.Start(v.ctx, name)
    defer span.End()
    // Set resource attributes for cub correlation
    span.SetAttributes(
        attribute.String("cub.agent.name", v.agentName),
        attribute.String("cub.repository.name", v.repoName),
    )
    return v.evalBlock(ctx, block)
}
```

The OTEL SDK's exporter sends spans/metrics/logs to the local OTLP receiver (localhost:4317), completing the loop.

#### Option B: Structured Event Emission (Simpler)

For simpler cases, Maggie processes could emit structured events via the SDLC telemetry store without full OTEL:

```smalltalk
"Emit a telemetry event"
System emit: #workflow event: 'step_completed'
    data: { 'step' -> 'compile'. 'duration_ms' -> elapsed }.
```

This maps directly to `TelemetryEvent` and bypasses the OTEL pipeline entirely. Simpler but loses trace correlation, distributed context propagation, and columnar analytics.

#### Recommendation

**Use Option A for workflow/agent telemetry** (traced operations with durations and parent-child relationships). **Use Option B for simple events** (lifecycle, status changes). The OTEL SDK handles context propagation, batching, and export automatically.

### Required VM Changes

1. **Add a `tracer` field to the VM** (from `go.opentelemetry.io/otel/trace`).
2. **Configure the OTEL exporter** to send to `localhost:4317` (the embedded receiver).
3. **Register telemetry primitives** in the VM's primitive table.
4. **Propagate context** through Maggie's call stack (thread `context.Context` through block evaluation).

### Resource Attributes for Maggie

Every Maggie process should set these resource attributes on its OTEL TracerProvider:

```go
resource.NewWithAttributes(
    semconv.SchemaURL,
    attribute.String("service.name", "maggie"),
    attribute.String("cub.agent.name", agentName),
    attribute.String("cub.repository.name", repoName),
    attribute.String("cub.task.id", taskID),
    attribute.String("maggie.process.name", processName),
)
```

This ensures all signals from a Maggie process are automatically correlated in the telemetry pipeline.

---

## 8. Design Recommendations for procyon-park

### Architecture Summary

```
┌──────────────────────────────────────────────────────────────────┐
│  procyon-park Telemetry Architecture                              │
│                                                                   │
│  ┌─────────────┐    ┌─────────────────┐    ┌──────────────────┐  │
│  │ SDLC Events │    │  OTLP Receiver  │    │  Maggie Process  │  │
│  │ (agent,task, │    │  gRPC :4317     │    │  (OTEL SDK)      │  │
│  │  git,mail)  │    │  HTTP :4318     │    │                  │  │
│  └──────┬──────┘    └───────┬─────────┘    └────────┬─────────┘  │
│         │                   │                       │             │
│         ▼                   ▼                       │             │
│  ┌─────────────┐    ┌─────────────────┐            │             │
│  │  SQLite     │    │  DuckDB         │◄───────────┘             │
│  │  (events)   │    │  (in-memory)    │  via OTLP exporter       │
│  └──────┬──────┘    └───────┬─────────┘                          │
│         │                   │ flush every 5 min                   │
│         │                   ▼                                     │
│         │           ┌─────────────────┐                          │
│         │           │  Parquet Files  │                          │
│         │           │  (warm tier)    │                          │
│         │           └───────┬─────────┘                          │
│         │                   │                                     │
│         ▼                   ▼                                     │
│  ┌─────────────────────────────────────┐                         │
│  │         Query Engine                │                         │
│  │  - SQLite queries (events)          │                         │
│  │  - DuckDB + Parquet (OTEL signals)  │                         │
│  │  - IPC dispatch for CLI             │                         │
│  └─────────────────────────────────────┘                         │
└──────────────────────────────────────────────────────────────────┘
```

### Key Decisions

| Decision                    | Choice                        | Rationale                                           |
|-----------------------------|-------------------------------|-----------------------------------------------------|
| SDLC event store            | SQLite                        | SQL queries, single file, proven in imp-castle       |
| OTEL signal store           | DuckDB + Parquet              | Columnar analytics, embedded, standard format        |
| OTLP transport              | gRPC + HTTP (embedded)        | OTEL-standard, no external collector needed          |
| Backpressure                | Drop on full channel          | Prefer availability over completeness for telemetry  |
| Parquet partitioning        | Monthly (`YYYY-MM`)           | Good balance of file count vs query granularity      |
| Parquet compression         | ZSTD                          | Best ratio for mixed string/numeric telemetry data   |
| cub correlation attributes  | Resource-level with fallback  | Set once per process, override per-signal if needed  |
| Maggie telemetry            | OTEL SDK via Go FFI           | Full trace correlation, automatic export             |
| Retention                   | 30 days (events), indefinite (Parquet) | Events are transient; Parquet is cheap to keep |

### Implementation Order

1. **SQLite event store** — Port `TelemetryStore` from imp-castle. Simplest, immediate value.
2. **OTLP receiver** — Port `Receiver` from imp-castle. Enables external tools to send data.
3. **DuckDB pipeline** — Port `Pipeline` + schema. Connects receiver to storage.
4. **Query engine** — Port `QueryEngine`. Enables CLI analytics.
5. **Maggie OTEL primitives** — Add tracer to VM, register primitives.
6. **Parquet export** — Automatic via pipeline flush loop.

### Dependencies

- `github.com/marcboeker/go-duckdb` — DuckDB Go driver (CGo)
- `go.opentelemetry.io/otel` + SDK — OTEL API and SDK
- `go.opentelemetry.io/proto/otlp/...` — OTLP protobuf definitions
- `google.golang.org/grpc` — gRPC server for OTLP receiver
- SQLite driver (already in use via store package)

---

## 9. Open Questions

1. **Cold tier**: Should Parquet files older than N months be moved to compressed archives or object storage?
2. **Sampling**: For high-volume agents, should the OTEL receiver support tail-based sampling?
3. **Dashboard**: What visualization layer for telemetry? DuckDB CLI, custom TUI, or web UI?
4. **Privacy**: Should telemetry strip file paths and task titles before Parquet export (following the hub sync privacy model)?
5. **Hub sync for OTEL**: Should Parquet files be synced to a hub, or only SDLC events? OTEL data is much larger.
