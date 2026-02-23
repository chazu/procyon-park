# Phase 7 Research: Analytics & Storage Tiers

## Overview

This document analyzes the analytics and tiered storage system in imp-castle's BBS tuplespace, covering the full lifecycle from hot storage through warm archival to cold tier, along with GC policies, analytics queries, the feedback loop, and LLM-powered synthesis.

## 1. Storage Architecture: Three Tiers

### 1.1 Hot Tier (SQLite)

**Source**: `internal/bbs/store.go`

The hot tier is the live tuplespace — a single SQLite database using WAL mode with a single-connection pool. It stores active tuples that agents read and write in real-time.

**Schema** (v2):
- `tuples` table with columns: id, category, scope, identity, instance, payload (JSON-validated), lifecycle (`furniture`/`session`/`ephemeral`), task_id, agent_id, created_at, updated_at, ttl_seconds
- FTS5 full-text search index on payload (`tuples_fts`)
- Indexes on category, scope, identity, instance, lifecycle, task_id, created_at, and a composite (category, scope)

**Lifecycle types**:
- **furniture**: Permanent tuples (conventions, promoted facts). Persist across sessions. Cannot be consumed via `In()`.
- **session**: Per-task tuples (claims, artifacts, obstacles). Archived when the task completes.
- **ephemeral**: TTL-based tuples (notifications). Garbage collected after expiry.

**Design decisions**:
- Single connection avoids SQLite lock contention while WAL provides read concurrency
- `json_valid(payload)` CHECK constraint enforces well-formed payloads at the database level
- `busy_timeout=5000` gives 5 seconds of retry before returning SQLITE_BUSY

### 1.2 Warm Tier (Parquet via DuckDB)

**Source**: `internal/bbs/archive.go`, `internal/bbs/analytics.go`

Archived tuples are exported to Parquet files organized by time partition and scope:

```
~/.imp-castle/bbs/warm/<YYYY-MM>/<scope>/<groupKey>.parquet
```

**Export mechanism**: An in-memory DuckDB instance creates a temporary `export` table matching the tuple schema, inserts all tuples, then runs `COPY ... TO ... (FORMAT PARQUET)`. This uses DuckDB as a pure Parquet writer — it is not a long-lived analytical database.

**Reading archived data**: Analytics queries open fresh in-memory DuckDB connections and use `read_parquet('<glob>')` to query across partitions. The `parquetGlob()` helper builds scope-filtered or wildcard glob expressions.

**Key property**: Archive-then-delete pattern. Parquet files are written before hot-tier deletes commit. On crash, the worst case is duplicate data (idempotent re-archive), never data loss.

### 1.3 Cold Tier (S3 — Not Yet Implemented)

The cold tier for S3 archival is mentioned in the design but not yet implemented in the codebase. The warm tier's Parquet-based partition scheme (`YYYY-MM/scope/`) is designed to make future S3 sync straightforward — a tool like `aws s3 sync` or `rclone` can mirror the warm directory to a bucket with the same prefix structure.

**Recommended approach for cold tier**:
- Periodic job moves Parquet files older than N months from warm to S3
- S3-compatible object storage (AWS S3, MinIO, Cloudflare R2)
- DuckDB's `read_parquet('s3://bucket/path/**/*.parquet')` enables seamless querying across cold and warm tiers
- Lifecycle policies on S3 for eventual deletion or transition to Glacier

## 2. GC Policies

**Source**: `internal/bbs/gc.go`, `internal/bbs/escalation.go`

The GC runs a periodic loop every 60 seconds, executing multiple cleanup and detection passes.

### 2.1 TTL Expiry (`collectExpiredEphemeral`)

Deletes ephemeral tuples whose `created_at + ttl_seconds` has elapsed. Archives before deleting. Logs unclaimed expired tuples (those with no agent_id) as "potential stuck coordination" — a diagnostic signal that a notification was written but no agent ever claimed it.

### 2.2 Stale Claim Detection (`cleanupStaleClaims`)

Two thresholds:
- **Stale** (1 hour): Claims older than `staleClaimAge` whose corresponding task has a `task_done` event are deleted (the agent finished but the claim wasn't cleaned up).
- **Abandoned** (2 hours): Claims older than `2 * staleClaimAge` with no `task_done` event are deleted as truly abandoned (the agent likely crashed).

Pre-scans all `task_done` events once to build a `doneTasks` set, then checks each stale claim against it. Archives before deleting.

### 2.3 Convention Promotion (`promoteConventions`)

Scans `convention-proposal` identity tuples. Groups proposals by their `content` field (canonical JSON marshaling for grouping). When `DefaultConventionQuorum` (2) or more **distinct agents** propose the same convention, it is promoted:
- A new furniture `convention` tuple is written to `scope=global`
- Original proposal tuples are deleted
- The promoted payload includes `promoted_from` (list of original tuple IDs)

This is an emergent consensus mechanism — agents independently propose conventions, and the GC promotes when quorum is reached.

### 2.4 Completed Task Archival (`archiveCompletedTasks`)

Scans for `task_done` events. For each, runs the full archival pipeline:
1. **Synthesis** (LLM knowledge extraction) — see Section 5
2. **Archive by task_id** — selects session tuples matching the task_id
3. **Archive by agent_id** — catches tuples that lack task_id but have agent_id

**Critical rule**: Agents with pending `dismiss_request` events are skipped. Their archival is deferred to the dismiss path (`ArchiveAndDeleteEvents`) so the king can process the dismiss first. This prevents events from being deleted before the king acts on them.

Event tuples (`task_done`, `dismiss_request`) are explicitly excluded from archival until after dismissal. This is the "events must persist until king acts" invariant.

### 2.5 Escalation Detection

**Source**: `internal/bbs/escalation.go`

Three real-time detection mechanisms run every GC cycle:

#### Systemic Obstacles (`detectSystemicObstacles`)
When 2+ obstacle tuples share the same scope, writes a `systemic_obstacle` event and a `notification` tuple targeting the king agent. Deduplicates by checking for existing systemic_obstacle events for the scope.

#### Unclaimed Needs (`detectUnclaimedNeeds`)
Needs with no `agent_id` older than 10 minutes trigger escalation notifications to the king. Each need is escalated at most once (deduplication via `unclaimed_need_<id>` identity).

#### Repeated Failures (`detectRepeatedFailures`)
When 3+ `task_failed` events exist in the same scope, writes a `repo_health_warning` furniture fact. This persists as durable knowledge so future agents and the king are aware of the scope's instability.

### 2.6 Workflow Signature Warnings (`detectWorkflowWarnings`)

Compares active tasks' tuple emission patterns against historically-failed workflow signatures cached as furniture tuples. If 60%+ of steps match a failed signature, writes an `early_warning` notification to the king. This is a predictive mechanism — detecting likely-to-fail tasks before they actually fail.

## 3. Analytics Queries (DuckDB Warm Tier)

**Source**: `internal/bbs/analytics.go`

All queries follow the same pattern: open a fresh in-memory DuckDB, build a `read_parquet(glob)` expression, execute an analytical SQL query, and return typed Go structs.

### 3.1 Agent Performance (`QueryAgentPerformance`)

Groups by scope, counts obstacles, distinct agents blocked, artifacts produced, and computes the artifact-to-obstacle ratio. Higher ratio = healthier scope (agents producing more artifacts than hitting obstacles).

### 3.2 Time to First Obstacle (`QueryTimeToFirstObstacle`)

For each scope, computes the average seconds between a task's first tuple and its first obstacle tuple. Diagnostic insight: early obstacles suggest poor documentation, late obstacles suggest hidden complexity.

### 3.3 Convention Effectiveness (`QueryConventionEffectiveness`)

Before/after analysis: for each convention's introduction date, computes task success rates before and after. Requires a minimum number of tasks on each side (`minTasks`, default 3) to avoid noisy results.

### 3.4 Obstacle Clustering (`QueryObstacleClusters`)

Groups obstacles by their `$.description` payload field. Returns clusters with `minOccurrences` (default 3) or more, including distinct agents affected and first/last seen timestamps. Surfaces recurring problems that individual agents might not recognize.

### 3.5 Workflow Signatures (`QueryWorkflowSignatures`)

Computes the dominant tuple emission pattern (category sequence) for successful vs. failed tasks. Uses window functions (ROW_NUMBER) to build per-step frequency tables, then selects the most common category at each step position. The result is a compact signature like `[claim, artifact, obstacle, event]` that characterizes how tasks typically unfold.

### 3.6 Knowledge Flow (`QueryKnowledgeFlow`)

Traces how knowledge propagates between agents. Identifies tuples (facts, conventions, obstacles) written by one agent that were present in the tuplespace when **other** agents subsequently completed their tasks. This measures the actual impact of knowledge sharing — a fact that helped 5 agents succeed is more valuable than one that helped 1.

## 4. The Feedback Loop

**Source**: `internal/bbs/feedback.go`

The analytics feedback loop runs on a configurable interval (default: 24 hours) and closes the learning cycle by writing warm-tier analytics results back into the hot tuplespace as furniture tuples.

### 4.1 Convention Pruning

Queries `QueryConventionEffectiveness`. If a convention's `success_rate_after < success_rate_before`, it hurt outcomes — delete it from hot storage. If it improved outcomes, write a `fact` tuple with type `convention_effectiveness` documenting the improvement.

### 4.2 Obstacle Surfacing

Queries `QueryObstacleClusters`. Writes recurring obstacles as `fact` tuples (type `obstacle_cluster`) so agents see common pitfalls without needing to query the warm tier.

### 4.3 Repo Health

Queries `QueryAgentPerformance`. Writes per-scope health summaries as `fact` tuples (type `repo_health`) with obstacle counts, artifact counts, and ratios.

### 4.4 Workflow Signature Caching

Queries `QueryWorkflowSignatures`. Caches failed-task signatures (with 2+ tasks) as furniture `fact` tuples (identity: `workflow-signature-failed`) so the GC's `detectWorkflowWarnings` can compare live tasks against them without querying the warm tier on every cycle.

### 4.5 Knowledge Flow Surfacing

Queries `QueryKnowledgeFlow`. Writes high-value cross-agent knowledge as `fact` tuples (type `knowledge_flow`) identifying which agents produced knowledge that demonstrably helped others succeed.

### 4.6 Feedback Result Tracking

`FeedbackResult` struct tracks counts: conventions pruned, obstacle facts written, repo health facts written, signatures cached, knowledge flow facts written, and any errors. Logged after each run for observability.

## 5. LLM-Powered Synthesis

**Source**: `internal/bbs/synthesis.go`

### 5.1 Purpose

Before session tuples are archived (and deleted from hot storage), an LLM extracts durable knowledge from the complete tuple history of a completed task. This converts ephemeral session data into permanent furniture tuples.

### 5.2 Configuration

`SynthesisConfig`:
- `Enabled`: Toggle on/off. Zero-value disables.
- `Provider`: "anthropic" (default) or "openai"
- `Model`: Default is `claude-sonnet-4-5-20250929`
- `APIKey`: From config or environment (`ANTHROPIC_API_KEY` / `OPENAI_API_KEY`)

### 5.3 Prompt Design

The prompt presents the full tuple history in a structured format:
```
[category/scope] identity (agent=X, lifecycle=Y) -- payload
```

Asks the LLM to output a JSON array of extracted knowledge tuples with `category` (fact or convention), `scope`, `identity`, and `payload` with a `content` field.

### 5.4 Pipeline

1. Read all session tuples for the task from hot storage (ordered by created_at)
2. Build the synthesis prompt with all tuples
3. Call the LLM API (90-second timeout)
4. Parse the JSON response (handles markdown code fences)
5. Write extracted tuples to hot space as furniture with `instance=synthesized`
6. Uses `Upsert` to avoid duplicates on re-synthesis

### 5.5 Error Handling

Synthesis errors are logged but **do not block archival**. The system degrades gracefully — if synthesis fails, tuples are still archived to the warm tier. Knowledge extraction is best-effort.

### 5.6 Provider Abstraction

`callLLMStandalone` dispatches to either `callAnthropicStandalone` or `callOpenAIStandalone`. Both are standalone functions (no GC dependency) so they can be used from CLI-driven batch synthesis as well.

## 6. Cross-Pollination

**Source**: `internal/bbs/crosspoll.go`

Not strictly analytics, but part of the knowledge flow system. The `CrossPollinator` detects when a tuple's payload references another known scope (simple substring match) and writes notification tuples for agents in the referenced scope. Rate-limited to 1 notification per (source_agent, target_scope) per 5 minutes to prevent spam.

## 7. Architecture Diagram

```
                     Hot Tier (SQLite)
                    +-------------------+
  Agents <--------> | tuples table      |  <-- Real-time reads/writes
                    | tuples_fts (FTS5) |
                    +--------+----------+
                             |
                    GC loop (60s)
                             |
              +--------------+------------------+
              |              |                  |
        TTL expiry    Stale claims     Task archival
                                            |
                                   +--------+--------+
                                   |                 |
                              Synthesis         Archive
                           (LLM extract)    (Parquet export)
                                   |                 |
                                   v                 v
                         furniture tuples     Warm Tier (Parquet)
                         written to hot      ~/.imp-castle/bbs/warm/
                                              YYYY-MM/scope/*.parquet
                                                     |
                                            DuckDB queries (analytics)
                                                     |
                                            Feedback Loop (24h)
                                                     |
                                                     v
                                            furniture facts/conventions
                                            written back to hot tier
                                                     |
                                            [Future] Cold Tier (S3)
```

## 8. Key Design Patterns

### 8.1 Archive-Then-Delete
Parquet written before SQLite delete commits. Crash-safe: worst case is duplicate data, never loss.

### 8.2 Events Persist Until King Acts
`task_done` and `dismiss_request` events are excluded from archival until after the king dismisses the agent. This ensures the coordination protocol completes before cleanup.

### 8.3 Feedback Writes Furniture
Analytics results are written as furniture tuples, not session tuples. This means they persist indefinitely and are visible to all agents — closing the learning loop from warm-tier analysis back to real-time agent behavior.

### 8.4 Synthesis is Best-Effort
LLM failures don't block archival. The system works without synthesis (just loses the knowledge extraction benefit).

### 8.5 DuckDB as Ephemeral Query Engine
DuckDB is used purely as a computation engine — opened in-memory, query runs, connection closes. No persistent DuckDB state. All durable state lives in SQLite (hot) or Parquet files (warm).

### 8.6 Self-Improving Conventions
The convention lifecycle: agents propose -> GC promotes at quorum -> feedback loop prunes if ineffective -> synthesis extracts from successful sessions. This creates a self-improving system where conventions that work survive and those that don't are automatically removed.

## 9. Gaps and Future Work

### 9.1 Cold Tier Implementation
S3 archival is designed for but not implemented. The partition scheme supports it. Needs: a periodic mover, DuckDB S3 integration for cross-tier queries, lifecycle policies.

### 9.2 Synthesis Batching
Currently synthesizes one task at a time. Batch synthesis across multiple tasks could find cross-task patterns. The standalone `callLLMStandalone` function suggests this was anticipated.

### 9.3 Analytics Query Optimization
Each query opens a fresh DuckDB connection. For dashboards or frequent queries, a connection pool or persistent DuckDB with attached Parquet views would reduce overhead.

### 9.4 Obstacle Clustering Sophistication
Current clustering uses exact payload match on `$.description`. Semantic clustering (embeddings, fuzzy matching) would catch obstacles that are phrased differently but describe the same root cause.

### 9.5 Retention Policies
No configurable retention period for warm-tier data. The partition scheme enables time-based pruning but no mechanism exists to enforce it.

### 9.6 Metrics Export
Analytics results are written as tuples but not exported to external monitoring (Prometheus, Grafana). An exporter could surface repo health metrics for dashboards.
