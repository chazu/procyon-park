package bbs

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/marcboeker/go-duckdb"
)

// writeTestParquet creates a Parquet file at outPath with the given tuple rows.
// Each row is a map with keys matching the Parquet schema from archive.go.
func writeTestParquet(t *testing.T, outPath string, tuples []map[string]interface{}) {
	t.Helper()

	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	duck, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer duck.Close()

	_, err = duck.Exec(`CREATE TABLE export_tuples (
		id          BIGINT,
		category    VARCHAR,
		scope       VARCHAR,
		identity    VARCHAR,
		instance    VARCHAR,
		payload     VARCHAR,
		lifecycle   VARCHAR,
		task_id     VARCHAR,
		agent_id    VARCHAR,
		created_at  VARCHAR,
		updated_at  VARCHAR,
		ttl_seconds INTEGER
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	stmt, err := duck.Prepare(`INSERT INTO export_tuples VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer stmt.Close()

	for _, r := range tuples {
		_, err := stmt.Exec(
			r["id"], r["category"], r["scope"], r["identity"],
			r["instance"], r["payload"], r["lifecycle"],
			r["task_id"], r["agent_id"], r["created_at"], r["updated_at"],
			r["ttl_seconds"],
		)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	copySQL := fmt.Sprintf("COPY export_tuples TO '%s' (FORMAT PARQUET)", outPath)
	if _, err := duck.Exec(copySQL); err != nil {
		t.Fatalf("COPY TO parquet: %v", err)
	}
}

// tuple is a convenience builder for test Parquet rows.
func tuple(id int64, category, scope, identity, taskID, agentID, createdAt string) map[string]interface{} {
	return map[string]interface{}{
		"id":          id,
		"category":    category,
		"scope":       scope,
		"identity":    identity,
		"instance":    "local",
		"payload":     "{}",
		"lifecycle":   "session",
		"task_id":     nilIfEmpty(taskID),
		"agent_id":    nilIfEmpty(agentID),
		"created_at":  createdAt,
		"updated_at":  createdAt,
		"ttl_seconds": nil,
	}
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// setupTestWarmDir creates a warm tier directory structure with Parquet data
// under <tmpDir>/<month>/<scope>/data.parquet.
func setupTestWarmDir(t *testing.T, scope, month string, tuples []map[string]interface{}) string {
	t.Helper()
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, month, scope, "data.parquet")
	writeTestParquet(t, outPath, tuples)
	return tmpDir
}

// ---------------------------------------------------------------------------
// Test: QueryAgentPerformance
// ---------------------------------------------------------------------------

func TestQueryAgentPerformance(t *testing.T) {
	tuples := []map[string]interface{}{
		tuple(1, "obstacle", "repo-a", "build-fail", "t1", "Alice", "2026-02-01 10:00:00"),
		tuple(2, "obstacle", "repo-a", "test-fail", "t1", "Alice", "2026-02-01 10:01:00"),
		tuple(3, "artifact", "repo-a", "main.go", "t1", "Alice", "2026-02-01 10:02:00"),
		tuple(4, "artifact", "repo-a", "util.go", "t2", "Bob", "2026-02-01 10:03:00"),
		tuple(5, "artifact", "repo-a", "api.go", "t2", "Bob", "2026-02-01 10:04:00"),
		tuple(6, "claim", "repo-a", "t1", "t1", "Alice", "2026-02-01 10:00:00"),
	}
	baseDir := setupTestWarmDir(t, "repo-a", "2026-02", tuples)

	results, err := QueryAgentPerformance(baseDir, "repo-a")
	if err != nil {
		t.Fatalf("QueryAgentPerformance: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Scope != "repo-a" {
		t.Errorf("scope = %q, want repo-a", r.Scope)
	}
	if r.ObstacleCount != 2 {
		t.Errorf("obstacles = %d, want 2", r.ObstacleCount)
	}
	if r.ArtifactCount != 3 {
		t.Errorf("artifacts = %d, want 3", r.ArtifactCount)
	}
	if r.DistinctAgents != 2 {
		t.Errorf("agents = %d, want 2", r.DistinctAgents)
	}
	if r.ArtifactObstacleRate != 1.5 {
		t.Errorf("rate = %f, want 1.5", r.ArtifactObstacleRate)
	}
}

func TestQueryAgentPerformanceEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	results, err := QueryAgentPerformance(tmpDir, "nonexistent")
	if err != nil {
		t.Fatalf("QueryAgentPerformance empty: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// Test: QueryTimeToFirstObstacle
// ---------------------------------------------------------------------------

func TestQueryTimeToFirstObstacle(t *testing.T) {
	tuples := []map[string]interface{}{
		// Task t1: starts at 10:00, first obstacle at 10:05 (300s)
		tuple(1, "claim", "repo-a", "t1", "t1", "Alice", "2026-02-01 10:00:00"),
		tuple(2, "obstacle", "repo-a", "fail-1", "t1", "Alice", "2026-02-01 10:05:00"),
		// Task t2: starts at 11:00, first obstacle at 11:10 (600s)
		tuple(3, "claim", "repo-a", "t2", "t2", "Bob", "2026-02-01 11:00:00"),
		tuple(4, "obstacle", "repo-a", "fail-2", "t2", "Bob", "2026-02-01 11:10:00"),
		tuple(5, "obstacle", "repo-a", "fail-3", "t2", "Bob", "2026-02-01 11:15:00"),
	}
	baseDir := setupTestWarmDir(t, "repo-a", "2026-02", tuples)

	results, err := QueryTimeToFirstObstacle(baseDir, "repo-a")
	if err != nil {
		t.Fatalf("QueryTimeToFirstObstacle: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.TaskCount != 2 {
		t.Errorf("task_count = %d, want 2", r.TaskCount)
	}
	// avg of 300 and 600 = 450
	if r.AvgSeconds < 449 || r.AvgSeconds > 451 {
		t.Errorf("avg_seconds = %f, want ~450", r.AvgSeconds)
	}
}

func TestQueryTimeToFirstObstacleNoObstacles(t *testing.T) {
	tuples := []map[string]interface{}{
		tuple(1, "claim", "repo-a", "t1", "t1", "Alice", "2026-02-01 10:00:00"),
		tuple(2, "artifact", "repo-a", "main.go", "t1", "Alice", "2026-02-01 10:05:00"),
	}
	baseDir := setupTestWarmDir(t, "repo-a", "2026-02", tuples)

	results, err := QueryTimeToFirstObstacle(baseDir, "repo-a")
	if err != nil {
		t.Fatalf("QueryTimeToFirstObstacle no obstacles: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results (no obstacles), got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// Test: QueryConventionEffectiveness
// ---------------------------------------------------------------------------

func TestQueryConventionEffectiveness(t *testing.T) {
	tuples := []map[string]interface{}{
		// Convention introduced at 12:00
		tuple(1, "convention", "repo-a", "use-tests", "", "", "2026-02-01 12:00:00"),
		// Before convention: 3 tasks, mostly obstacles
		tuple(2, "obstacle", "repo-a", "fail", "t1", "A", "2026-02-01 10:00:00"),
		tuple(3, "obstacle", "repo-a", "fail", "t2", "B", "2026-02-01 10:30:00"),
		tuple(4, "artifact", "repo-a", "f.go", "t3", "C", "2026-02-01 11:00:00"),
		// After convention: 3 tasks, mostly artifacts
		tuple(5, "artifact", "repo-a", "g.go", "t4", "A", "2026-02-01 13:00:00"),
		tuple(6, "artifact", "repo-a", "h.go", "t5", "B", "2026-02-01 13:30:00"),
		tuple(7, "obstacle", "repo-a", "minor", "t6", "C", "2026-02-01 14:00:00"),
	}
	baseDir := setupTestWarmDir(t, "repo-a", "2026-02", tuples)

	results, err := QueryConventionEffectiveness(baseDir, "repo-a", 3)
	if err != nil {
		t.Fatalf("QueryConventionEffectiveness: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.ConventionID != "use-tests" {
		t.Errorf("convention = %q, want use-tests", r.ConventionID)
	}
	// Before: 1 artifact, 2 obstacles -> 1/3 ≈ 0.333
	if r.BeforeRate < 0.30 || r.BeforeRate > 0.35 {
		t.Errorf("before_rate = %f, want ~0.333", r.BeforeRate)
	}
	// After: 2 artifacts, 1 obstacle -> 2/3 ≈ 0.667
	if r.AfterRate < 0.63 || r.AfterRate > 0.70 {
		t.Errorf("after_rate = %f, want ~0.667", r.AfterRate)
	}
}

func TestQueryConventionEffectivenessMinTasks(t *testing.T) {
	tuples := []map[string]interface{}{
		tuple(1, "convention", "repo-a", "use-lint", "", "", "2026-02-01 12:00:00"),
		// Only 1 task before (below minTasks=3)
		tuple(2, "artifact", "repo-a", "f.go", "t1", "A", "2026-02-01 10:00:00"),
		// 3 tasks after
		tuple(3, "artifact", "repo-a", "g.go", "t2", "B", "2026-02-01 13:00:00"),
		tuple(4, "artifact", "repo-a", "h.go", "t3", "C", "2026-02-01 14:00:00"),
		tuple(5, "artifact", "repo-a", "i.go", "t4", "D", "2026-02-01 15:00:00"),
	}
	baseDir := setupTestWarmDir(t, "repo-a", "2026-02", tuples)

	results, err := QueryConventionEffectiveness(baseDir, "repo-a", 3)
	if err != nil {
		t.Fatalf("QueryConventionEffectiveness minTasks: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results (minTasks not met), got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// Test: QueryObstacleClusters
// ---------------------------------------------------------------------------

func TestQueryObstacleClusters(t *testing.T) {
	tuples := []map[string]interface{}{
		tuple(1, "obstacle", "repo-a", "build-fail", "t1", "A", "2026-02-01 10:00:00"),
		tuple(2, "obstacle", "repo-a", "build-fail", "t2", "B", "2026-02-01 11:00:00"),
		tuple(3, "obstacle", "repo-a", "build-fail", "t3", "C", "2026-02-02 10:00:00"),
		tuple(4, "obstacle", "repo-a", "test-fail", "t4", "A", "2026-02-01 12:00:00"),
		// Non-obstacle should be ignored
		tuple(5, "artifact", "repo-a", "build-fail", "t5", "D", "2026-02-01 13:00:00"),
	}
	baseDir := setupTestWarmDir(t, "repo-a", "2026-02", tuples)

	results, err := QueryObstacleClusters(baseDir, "repo-a", 3)
	if err != nil {
		t.Fatalf("QueryObstacleClusters: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(results))
	}

	r := results[0]
	if r.Description != "build-fail" {
		t.Errorf("description = %q, want build-fail", r.Description)
	}
	if r.Occurrences != 3 {
		t.Errorf("occurrences = %d, want 3", r.Occurrences)
	}
	if r.DistinctAgents != 3 {
		t.Errorf("distinct_agents = %d, want 3", r.DistinctAgents)
	}
	if r.FirstSeen != "2026-02-01 10:00:00" {
		t.Errorf("first_seen = %q, want 2026-02-01 10:00:00", r.FirstSeen)
	}
	if r.LastSeen != "2026-02-02 10:00:00" {
		t.Errorf("last_seen = %q, want 2026-02-02 10:00:00", r.LastSeen)
	}
}

func TestQueryObstacleClustersEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	results, err := QueryObstacleClusters(tmpDir, "nonexistent", 3)
	if err != nil {
		t.Fatalf("QueryObstacleClusters empty: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// Test: QueryWorkflowSignatures
// ---------------------------------------------------------------------------

func TestQueryWorkflowSignatures(t *testing.T) {
	tuples := []map[string]interface{}{
		// Task t1: claim -> fact -> artifact -> event(task_done) = success
		tuple(1, "claim", "repo-a", "t1", "t1", "Alice", "2026-02-01 10:00:00"),
		tuple(2, "fact", "repo-a", "built", "t1", "Alice", "2026-02-01 10:01:00"),
		tuple(3, "artifact", "repo-a", "main.go", "t1", "Alice", "2026-02-01 10:02:00"),
		tuple(4, "event", "repo-a", "task_done", "t1", "Alice", "2026-02-01 10:03:00"),
		// Task t2: claim -> obstacle = incomplete
		tuple(5, "claim", "repo-a", "t2", "t2", "Bob", "2026-02-01 11:00:00"),
		tuple(6, "obstacle", "repo-a", "blocked", "t2", "Bob", "2026-02-01 11:01:00"),
	}
	baseDir := setupTestWarmDir(t, "repo-a", "2026-02", tuples)

	results, err := QueryWorkflowSignatures(baseDir, "repo-a")
	if err != nil {
		t.Fatalf("QueryWorkflowSignatures: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Results sorted by task_id
	if results[0].TaskID != "t1" {
		t.Errorf("result[0].TaskID = %q, want t1", results[0].TaskID)
	}
	if results[0].Outcome != "success" {
		t.Errorf("result[0].Outcome = %q, want success", results[0].Outcome)
	}
	if results[0].Pattern != "claim,fact,artifact,event" {
		t.Errorf("result[0].Pattern = %q, want claim,fact,artifact,event", results[0].Pattern)
	}

	if results[1].TaskID != "t2" {
		t.Errorf("result[1].TaskID = %q, want t2", results[1].TaskID)
	}
	if results[1].Outcome != "incomplete" {
		t.Errorf("result[1].Outcome = %q, want incomplete", results[1].Outcome)
	}
}

func TestQueryWorkflowSignaturesSingleTask(t *testing.T) {
	tuples := []map[string]interface{}{
		tuple(1, "claim", "repo-a", "t1", "t1", "Alice", "2026-02-01 10:00:00"),
	}
	baseDir := setupTestWarmDir(t, "repo-a", "2026-02", tuples)

	results, err := QueryWorkflowSignatures(baseDir, "repo-a")
	if err != nil {
		t.Fatalf("QueryWorkflowSignatures single: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Pattern != "claim" {
		t.Errorf("pattern = %q, want claim", results[0].Pattern)
	}
	if results[0].Outcome != "incomplete" {
		t.Errorf("outcome = %q, want incomplete", results[0].Outcome)
	}
}

// ---------------------------------------------------------------------------
// Test: QueryKnowledgeFlow
// ---------------------------------------------------------------------------

func TestQueryKnowledgeFlow(t *testing.T) {
	tuples := []map[string]interface{}{
		// Alice writes a fact at 10:00
		tuple(1, "fact", "repo-a", "go-tests-pass", "t1", "Alice", "2026-02-01 10:00:00"),
		// Bob completes a task at 11:00 (after Alice's fact)
		tuple(2, "event", "repo-a", "task_done", "t2", "Bob", "2026-02-01 11:00:00"),
		// Alice's own task_done should NOT appear as flow (same agent)
		tuple(3, "event", "repo-a", "task_done", "t1", "Alice", "2026-02-01 10:30:00"),
	}
	baseDir := setupTestWarmDir(t, "repo-a", "2026-02", tuples)

	results, err := QueryKnowledgeFlow(baseDir, "repo-a")
	if err != nil {
		t.Fatalf("QueryKnowledgeFlow: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.SourceAgent != "Alice" {
		t.Errorf("source = %q, want Alice", r.SourceAgent)
	}
	if r.TargetAgent != "Bob" {
		t.Errorf("target = %q, want Bob", r.TargetAgent)
	}
	if r.Identity != "go-tests-pass" {
		t.Errorf("identity = %q, want go-tests-pass", r.Identity)
	}
}

func TestQueryKnowledgeFlowEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	results, err := QueryKnowledgeFlow(tmpDir, "nonexistent")
	if err != nil {
		t.Fatalf("QueryKnowledgeFlow empty: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestQueryKnowledgeFlowNoConflict(t *testing.T) {
	// Knowledge written AFTER task completion should not count
	tuples := []map[string]interface{}{
		tuple(1, "event", "repo-a", "task_done", "t1", "Bob", "2026-02-01 10:00:00"),
		tuple(2, "fact", "repo-a", "late-fact", "t2", "Alice", "2026-02-01 11:00:00"),
	}
	baseDir := setupTestWarmDir(t, "repo-a", "2026-02", tuples)

	results, err := QueryKnowledgeFlow(baseDir, "repo-a")
	if err != nil {
		t.Fatalf("QueryKnowledgeFlow no conflict: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results (knowledge written after), got %d", len(results))
	}
}
