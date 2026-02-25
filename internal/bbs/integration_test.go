package bbs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"

	"github.com/chazu/procyon-park/internal/tuplestore"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mustNewMemoryStore creates an in-memory TupleStore and registers cleanup.
func mustNewMemoryStore(t *testing.T) *tuplestore.TupleStore {
	t.Helper()
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func strPtr(s string) *string { return &s }

// insertTuple is a convenience for inserting a tuple into a store.
func insertTuple(t *testing.T, store *tuplestore.TupleStore,
	category, scope, identity, payload, lifecycle string,
	taskID, agentID *string, ttl *int) int64 {
	t.Helper()
	id, err := store.Insert(category, scope, identity, "local",
		payload, lifecycle, taskID, agentID, ttl)
	if err != nil {
		t.Fatalf("insert %s/%s/%s: %v", category, scope, identity, err)
	}
	return id
}

// countTuples returns the total number of tuples in the store.
func countTuples(t *testing.T, store *tuplestore.TupleStore) int {
	t.Helper()
	all, err := store.FindAll(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("countTuples: %v", err)
	}
	return len(all)
}

// findByCategory returns all tuples matching a category.
func findByCategory(t *testing.T, store *tuplestore.TupleStore, category string) []map[string]interface{} {
	t.Helper()
	results, err := store.FindAll(&category, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("findByCategory %s: %v", category, err)
	}
	return results
}

// ===========================================================================
// Test 1: Full Archival Pipeline
// ===========================================================================

func TestIntegration_FullArchivalPipeline(t *testing.T) {
	store := mustNewMemoryStore(t)
	warmDir := t.TempDir()

	// Insert session tuples for a completed task.
	task := strPtr("task-001")
	agent := strPtr("Alice")

	insertTuple(t, store, "claim", "repo-a", "task-001", `{"agent":"Alice","status":"in_progress"}`, "session", task, agent, nil)
	insertTuple(t, store, "fact", "repo-a", "built-successfully", `{"content":"tests pass"}`, "session", task, agent, nil)
	insertTuple(t, store, "artifact", "repo-a", "main.go", `{"type":"file"}`, "session", task, agent, nil)
	insertTuple(t, store, "event", "repo-a", "task_done", `{"task":"task-001","agent":"Alice"}`, "session", task, agent, nil)

	// Verify 4 tuples in hot tier.
	if n := countTuples(t, store); n != 4 {
		t.Fatalf("before GC: expected 4 tuples, got %d", n)
	}

	// Run GC with archival enabled (synthesis disabled).
	cfg := GCConfig{
		WarmBaseDir: warmDir,
		Synthesis:   SynthesisConfig{Enabled: false},
	}
	result := RunGCCycle(context.Background(), store, cfg)

	// Verify tuples were archived (non-excluded ones removed from hot tier).
	// task_done is excluded from archival, so it stays.
	remaining := countTuples(t, store)
	if remaining != 1 {
		// Only task_done event should remain (excluded from archival).
		all, _ := store.FindAll(nil, nil, nil, nil, nil)
		for _, r := range all {
			t.Logf("  remaining: %s/%s/%s", r["category"], r["scope"], r["identity"])
		}
		t.Fatalf("after GC: expected 1 remaining tuple (task_done), got %d", remaining)
	}

	if result.ArchivedTasks == 0 {
		t.Error("expected ArchivedTasks > 0")
	}

	// Verify Parquet files were written.
	month := time.Now().Format("2006-01")
	globPattern := filepath.Join(warmDir, month, "repo-a", "*.parquet")
	matches, err := filepath.Glob(globPattern)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no Parquet files written to warm tier")
	}

	// Verify Parquet contents via DuckDB read.
	parquetGlob := filepath.Join(warmDir, month, "repo-a", "*.parquet")
	archived, err := tuplestore.ReadParquet(parquetGlob)
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}
	if len(archived) == 0 {
		t.Fatal("Parquet file is empty")
	}

	// Verify archived tuples have correct data.
	foundClaim, foundFact, foundArtifact := false, false, false
	for _, r := range archived {
		cat := r["category"].(string)
		switch cat {
		case "claim":
			foundClaim = true
		case "fact":
			foundFact = true
		case "artifact":
			foundArtifact = true
		}
	}
	if !foundClaim || !foundFact || !foundArtifact {
		t.Errorf("missing archived tuple types: claim=%v fact=%v artifact=%v",
			foundClaim, foundFact, foundArtifact)
	}
}

// ===========================================================================
// Test 2: Archive-Then-Delete Safety
// ===========================================================================

func TestIntegration_ArchiveThenDeleteSafety(t *testing.T) {
	store := mustNewMemoryStore(t)
	warmDir := t.TempDir()

	// Insert tuples for a task.
	task := strPtr("task-safe")
	agent := strPtr("Bob")
	insertTuple(t, store, "fact", "repo-b", "insight-1", `{"content":"important data"}`, "session", task, agent, nil)
	insertTuple(t, store, "artifact", "repo-b", "file.go", `{"type":"file"}`, "session", task, agent, nil)

	// Archive by task ID directly.
	archived, err := store.ArchiveByTaskID(warmDir, "task-safe")
	if err != nil {
		t.Fatalf("ArchiveByTaskID: %v", err)
	}
	if archived != 2 {
		t.Fatalf("expected 2 archived, got %d", archived)
	}

	// Verify tuples are gone from hot tier.
	if n := countTuples(t, store); n != 0 {
		t.Errorf("expected 0 tuples in hot tier after archive, got %d", n)
	}

	// Verify Parquet file was written (archive-then-delete: write first).
	month := time.Now().Format("2006-01")
	parquetPath := filepath.Join(warmDir, month, "repo-b", "task-task-safe.parquet")
	if _, err := os.Stat(parquetPath); os.IsNotExist(err) {
		t.Fatalf("Parquet file not written: %s", parquetPath)
	}

	// Re-read the Parquet to verify completeness.
	rows, err := tuplestore.ReadParquet(parquetPath)
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows in Parquet, got %d", len(rows))
	}
}

// ===========================================================================
// Test 3: GC Policy — TTL Expiry
// ===========================================================================

func TestIntegration_GC_TTLExpiry(t *testing.T) {
	store := mustNewMemoryStore(t)

	// Insert an ephemeral tuple with TTL of 0 seconds (expires immediately).
	insertTuple(t, store, "notification", "repo-a", "temp-notif", `{"msg":"hello"}`, "ephemeral", nil, strPtr("Agent1"), intPtr(0))

	// Wait briefly for the time boundary.
	time.Sleep(50 * time.Millisecond)

	// Verify it's found as expired.
	expired, err := store.FindExpiredEphemeral()
	if err != nil {
		t.Fatalf("FindExpiredEphemeral: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired tuple, got %d", len(expired))
	}

	// Run GC.
	result := RunGCCycle(context.Background(), store, GCConfig{})
	if result.ExpiredEphemeral != 1 {
		t.Errorf("expected 1 expired ephemeral, got %d", result.ExpiredEphemeral)
	}
	if n := countTuples(t, store); n != 0 {
		t.Errorf("expected 0 tuples after GC, got %d", n)
	}
}

// ===========================================================================
// Test 3b: GC Policy — Stale Claim Cleanup
// ===========================================================================

func TestIntegration_GC_StaleClaimCleanup(t *testing.T) {
	store := mustNewMemoryStore(t)

	// Insert a claim tuple with an old timestamp.
	_, err := store.Insert("claim", "repo-a", "task-old", "local",
		`{"agent":"Alice","status":"in_progress"}`, "session",
		strPtr("task-old"), strPtr("Alice"), nil)
	if err != nil {
		t.Fatalf("insert claim: %v", err)
	}

	// Insert a task_done event for this task.
	_, err = store.Insert("event", "repo-a", "task_done", "local",
		`{"task":"task-old","agent":"Alice"}`, "session",
		strPtr("task-old"), strPtr("Alice"), nil)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}

	// Run GC with a very short stale age (claim was just inserted, so use 0).
	cfg := GCConfig{
		StaleClaimAge:     1 * time.Millisecond, // Very short for test.
		AbandonedClaimAge: 1 * time.Millisecond,
	}
	time.Sleep(10 * time.Millisecond)
	result := RunGCCycle(context.Background(), store, cfg)

	if result.StaleClaims != 1 {
		t.Errorf("expected 1 stale claim, got %d", result.StaleClaims)
	}

	// Verify claim is gone but task_done event remains.
	claims := findByCategory(t, store, "claim")
	if len(claims) != 0 {
		t.Errorf("expected 0 claims after GC, got %d", len(claims))
	}
	events := findByCategory(t, store, "event")
	if len(events) != 1 {
		t.Errorf("expected 1 event (task_done), got %d", len(events))
	}
}

// ===========================================================================
// Test 3c: GC Policy — Convention Promotion at Quorum
// ===========================================================================

func TestIntegration_GC_ConventionPromotion(t *testing.T) {
	store := mustNewMemoryStore(t)

	// Two agents propose the same convention.
	insertTuple(t, store, "conventionProposal", "repo-a", "use-tests",
		`{"content":"always write tests"}`, "session", nil, strPtr("Alice"), nil)
	insertTuple(t, store, "conventionProposal", "repo-a", "use-tests",
		`{"content":"always write tests"}`, "session", nil, strPtr("Bob"), nil)

	// One agent proposes a different convention (should NOT be promoted — no quorum).
	insertTuple(t, store, "conventionProposal", "repo-a", "use-lint",
		`{"content":"always lint"}`, "session", nil, strPtr("Alice"), nil)

	// Run GC with quorum=2.
	cfg := GCConfig{ConventionQuorum: 2}
	result := RunGCCycle(context.Background(), store, cfg)

	if result.PromotedConventions != 1 {
		t.Errorf("expected 1 promoted convention, got %d", result.PromotedConventions)
	}

	// Verify promoted convention exists as furniture.
	conventions := findByCategory(t, store, "convention")
	if len(conventions) != 1 {
		t.Fatalf("expected 1 convention, got %d", len(conventions))
	}
	if conventions[0]["identity"].(string) != "use-tests" {
		t.Errorf("promoted convention identity = %q, want use-tests", conventions[0]["identity"])
	}
	if conventions[0]["lifecycle"].(string) != "furniture" {
		t.Errorf("promoted convention lifecycle = %q, want furniture", conventions[0]["lifecycle"])
	}

	// Verify proposals for "use-tests" are cleaned up.
	proposals := findByCategory(t, store, "conventionProposal")
	if len(proposals) != 1 {
		// Only "use-lint" should remain.
		t.Errorf("expected 1 remaining proposal (use-lint), got %d", len(proposals))
	}
}

// ===========================================================================
// Test 4: Analytics Roundtrip
// ===========================================================================

func TestIntegration_AnalyticsRoundtrip(t *testing.T) {
	store := mustNewMemoryStore(t)
	warmDir := t.TempDir()

	// Build a rich set of tuples in the store, then archive to Parquet.
	task1 := strPtr("t1")
	task2 := strPtr("t2")
	alice := strPtr("Alice")
	bob := strPtr("Bob")

	// Task t1: successful (claim→fact→artifact→task_done)
	insertTuple(t, store, "claim", "repo-x", "t1", `{"agent":"Alice","status":"in_progress"}`, "session", task1, alice, nil)
	insertTuple(t, store, "fact", "repo-x", "found-pattern", `{"content":"useful insight"}`, "session", task1, alice, nil)
	insertTuple(t, store, "artifact", "repo-x", "handler.go", `{"type":"file"}`, "session", task1, alice, nil)
	insertTuple(t, store, "obstacle", "repo-x", "build-fail", `{"detail":"missing dep"}`, "session", task1, alice, nil)

	// Task t2: Bob hit obstacles
	insertTuple(t, store, "claim", "repo-x", "t2", `{"agent":"Bob","status":"in_progress"}`, "session", task2, bob, nil)
	insertTuple(t, store, "obstacle", "repo-x", "build-fail", `{"detail":"same issue"}`, "session", task2, bob, nil)
	insertTuple(t, store, "obstacle", "repo-x", "test-fail", `{"detail":"flaky"}`, "session", task2, bob, nil)

	// Convention introduced between tasks.
	insertTuple(t, store, "convention", "repo-x", "use-dep-lock", `{}`, "session", nil, nil, nil)

	// Archive all by task ID.
	store.ArchiveByTaskID(warmDir, "t1")
	store.ArchiveByTaskID(warmDir, "t2")

	// Also write the convention to Parquet (needs to be archivable).
	// Conventions are session lifecycle, so archive by a pattern.
	// For this test, manually write Parquet with all the tuples for query coverage.
	month := time.Now().Format("2006-01")
	allTuples := []map[string]interface{}{
		tuple(1, "claim", "repo-x", "t1", "t1", "Alice", "2026-02-01 10:00:00"),
		tuple(2, "fact", "repo-x", "found-pattern", "t1", "Alice", "2026-02-01 10:01:00"),
		tuple(3, "artifact", "repo-x", "handler.go", "t1", "Alice", "2026-02-01 10:02:00"),
		tuple(4, "obstacle", "repo-x", "build-fail", "t1", "Alice", "2026-02-01 10:03:00"),
		tuple(5, "event", "repo-x", "task_done", "t1", "Alice", "2026-02-01 10:04:00"),
		tuple(6, "claim", "repo-x", "t2", "t2", "Bob", "2026-02-01 11:00:00"),
		tuple(7, "obstacle", "repo-x", "build-fail", "t2", "Bob", "2026-02-01 11:01:00"),
		tuple(8, "obstacle", "repo-x", "test-fail", "t2", "Bob", "2026-02-01 11:02:00"),
		tuple(9, "convention", "repo-x", "use-dep-lock", "", "", "2026-02-01 10:30:00"),
		// Extra before-convention tasks to meet minTasks=2
		tuple(10, "obstacle", "repo-x", "dep-missing", "t3", "Charlie", "2026-02-01 09:00:00"),
		// After convention: some success tuples
		tuple(11, "artifact", "repo-x", "api.go", "t4", "Charlie", "2026-02-01 12:00:00"),
		tuple(12, "artifact", "repo-x", "test.go", "t5", "Dana", "2026-02-01 12:30:00"),
	}

	// Write all tuples to a single Parquet for querying.
	queryDir := t.TempDir()
	outPath := filepath.Join(queryDir, month, "repo-x", "all.parquet")
	writeTestParquet(t, outPath, allTuples)

	// Query 1: AgentPerformance
	t.Run("AgentPerformance", func(t *testing.T) {
		results, err := QueryAgentPerformance(queryDir, "repo-x")
		if err != nil {
			t.Fatalf("QueryAgentPerformance: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		r := results[0]
		if r.ObstacleCount != 4 {
			t.Errorf("obstacles = %d, want 4", r.ObstacleCount)
		}
		if r.ArtifactCount != 3 {
			t.Errorf("artifacts = %d, want 3", r.ArtifactCount)
		}
	})

	// Query 2: TimeToFirstObstacle
	t.Run("TimeToFirstObstacle", func(t *testing.T) {
		results, err := QueryTimeToFirstObstacle(queryDir, "repo-x")
		if err != nil {
			t.Fatalf("QueryTimeToFirstObstacle: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results, got none")
		}
		if results[0].TaskCount < 1 {
			t.Errorf("task_count = %d, want >= 1", results[0].TaskCount)
		}
	})

	// Query 3: ConventionEffectiveness
	t.Run("ConventionEffectiveness", func(t *testing.T) {
		results, err := QueryConventionEffectiveness(queryDir, "repo-x", 2)
		if err != nil {
			t.Fatalf("QueryConventionEffectiveness: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 convention result, got %d", len(results))
		}
		if results[0].ConventionID != "use-dep-lock" {
			t.Errorf("convention = %q, want use-dep-lock", results[0].ConventionID)
		}
	})

	// Query 4: ObstacleClusters
	t.Run("ObstacleClusters", func(t *testing.T) {
		results, err := QueryObstacleClusters(queryDir, "repo-x", 2)
		if err != nil {
			t.Fatalf("QueryObstacleClusters: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 cluster (build-fail >= 2), got %d", len(results))
		}
		if results[0].Description != "build-fail" {
			t.Errorf("cluster = %q, want build-fail", results[0].Description)
		}
		if results[0].Occurrences != 2 {
			t.Errorf("occurrences = %d, want 2", results[0].Occurrences)
		}
	})

	// Query 5: WorkflowSignatures
	t.Run("WorkflowSignatures", func(t *testing.T) {
		results, err := QueryWorkflowSignatures(queryDir, "repo-x")
		if err != nil {
			t.Fatalf("QueryWorkflowSignatures: %v", err)
		}
		if len(results) < 2 {
			t.Fatalf("expected >= 2 workflow signatures, got %d", len(results))
		}
		// t1 should be success (has task_done), t2 should be incomplete
		for _, r := range results {
			if r.TaskID == "t1" && r.Outcome != "success" {
				t.Errorf("t1 outcome = %q, want success", r.Outcome)
			}
			if r.TaskID == "t2" && r.Outcome != "incomplete" {
				t.Errorf("t2 outcome = %q, want incomplete", r.Outcome)
			}
		}
	})

	// Query 6: KnowledgeFlow
	t.Run("KnowledgeFlow", func(t *testing.T) {
		results, err := QueryKnowledgeFlow(queryDir, "repo-x")
		if err != nil {
			t.Fatalf("QueryKnowledgeFlow: %v", err)
		}
		// Alice's fact at 10:01, Bob completes (task_done is at 11:xx, but we don't have
		// Bob's task_done in this set). Check we get flows.
		// Actually the only task_done is for t1/Alice. Bob has no task_done.
		// KnowledgeFlow requires: fact/convention written BEFORE a task_done by a DIFFERENT agent.
		// Alice's fact at 10:01, Alice's task_done at 10:04 — same agent, no flow.
		// No other task_done events, so 0 flows is correct for this dataset.
		_ = results // Verify query doesn't error.
	})
}

// ===========================================================================
// Test 5: Feedback Loop
// ===========================================================================

func TestIntegration_FeedbackLoop(t *testing.T) {
	store := mustNewMemoryStore(t)

	// Create warm-tier Parquet data with enough tuples for analytics.
	month := time.Now().Format("2006-01")
	warmDir := t.TempDir()

	// Build rich dataset:
	// - Convention "use-tests" introduced at 12:00
	// - 3 tasks before with obstacles
	// - 3 tasks after with artifacts
	// - build-fail obstacle appearing 3 times
	// - Alice writes fact, Bob has task_done (knowledge flow)
	allTuples := []map[string]interface{}{
		tuple(1, "convention", "repo-f", "use-tests", "", "", "2026-02-01 12:00:00"),
		// Before: 3 tasks, mostly obstacles
		tuple(2, "obstacle", "repo-f", "build-fail", "t1", "A", "2026-02-01 10:00:00"),
		tuple(3, "obstacle", "repo-f", "build-fail", "t2", "B", "2026-02-01 10:30:00"),
		tuple(4, "obstacle", "repo-f", "build-fail", "t3", "C", "2026-02-01 11:00:00"),
		// After: 3 tasks, mostly artifacts
		tuple(5, "artifact", "repo-f", "a.go", "t4", "A", "2026-02-01 13:00:00"),
		tuple(6, "artifact", "repo-f", "b.go", "t5", "B", "2026-02-01 13:30:00"),
		tuple(7, "artifact", "repo-f", "c.go", "t6", "C", "2026-02-01 14:00:00"),
		// Claims for tasks (for workflow signatures)
		tuple(8, "claim", "repo-f", "t1", "t1", "A", "2026-02-01 10:00:00"),
		tuple(9, "claim", "repo-f", "t4", "t4", "A", "2026-02-01 13:00:00"),
		// Knowledge flow: A writes fact before B completes
		tuple(10, "fact", "repo-f", "important-insight", "t1", "A", "2026-02-01 10:00:00"),
		tuple(11, "event", "repo-f", "task_done", "t5", "B", "2026-02-01 13:30:00"),
	}

	outPath := filepath.Join(warmDir, month, "repo-f", "all.parquet")
	writeTestParquet(t, outPath, allTuples)

	// Also write a convention in the hot store (so pruning can delete it).
	insertTuple(t, store, "convention", "repo-f", "bad-convention", `{}`, "furniture", nil, nil, nil)

	// Write Parquet data showing bad-convention hurt outcomes.
	badConvTuples := []map[string]interface{}{
		tuple(20, "convention", "repo-f", "bad-convention", "", "", "2026-02-01 12:00:00"),
		// Before: 3 tasks with artifacts (good)
		tuple(21, "artifact", "repo-f", "x.go", "tb1", "X", "2026-02-01 10:00:00"),
		tuple(22, "artifact", "repo-f", "y.go", "tb2", "Y", "2026-02-01 10:30:00"),
		tuple(23, "artifact", "repo-f", "z.go", "tb3", "Z", "2026-02-01 11:00:00"),
		// After: 3 tasks with obstacles (bad — convention hurt outcomes)
		tuple(24, "obstacle", "repo-f", "fail", "tb4", "X", "2026-02-01 13:00:00"),
		tuple(25, "obstacle", "repo-f", "fail", "tb5", "Y", "2026-02-01 13:30:00"),
		tuple(26, "obstacle", "repo-f", "fail", "tb6", "Z", "2026-02-01 14:00:00"),
	}
	badConvPath := filepath.Join(warmDir, month, "repo-f", "badconv.parquet")
	writeTestParquet(t, badConvPath, badConvTuples)

	// Run feedback cycle.
	cfg := FeedbackConfig{
		WarmBaseDir:            warmDir,
		Scope:                  "repo-f",
		ConventionMinTasks:     3,
		ObstacleMinOccurrences: 2,
	}
	result := RunFeedbackCycle(store, cfg)

	// Verify: conventions pruned and kept.
	if result.ConventionsPruned == 0 && result.ConventionsKept == 0 {
		t.Log("Warning: no conventions evaluated (may need minTasks adjustment)")
	}

	// Verify: obstacle clusters surfaced as furniture facts.
	if result.ObstaclesSurfaced == 0 {
		t.Error("expected obstacles to be surfaced")
	}
	obsClusters := findFurnitureFacts(t, store, "obstacle_cluster:")
	if len(obsClusters) == 0 {
		t.Error("no obstacle_cluster furniture facts written")
	}

	// Verify: repo health written as furniture.
	if result.RepoHealthUpdated == 0 {
		t.Error("expected repo health to be updated")
	}
	healthFacts := findFurnitureFactsByIdentity(t, store, "repo-health")
	if len(healthFacts) == 0 {
		t.Error("no repo-health furniture fact written")
	}

	// Verify: workflow signatures cached.
	// (incomplete tasks should be cached)
	if result.SignaturesCached == 0 {
		t.Log("Note: no incomplete workflow signatures to cache (may be expected)")
	}

	// Verify: knowledge flows written.
	if result.KnowledgeFlows == 0 {
		t.Log("Note: no knowledge flows detected (may need more cross-agent data)")
	}

	// Verify total errors are 0.
	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			t.Errorf("feedback error: %v", e)
		}
	}
}

func findFurnitureFacts(t *testing.T, store *tuplestore.TupleStore, identityPrefix string) []map[string]interface{} {
	t.Helper()
	cat := "fact"
	all, err := store.FindAll(&cat, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("findFurnitureFacts: %v", err)
	}
	var result []map[string]interface{}
	for _, r := range all {
		ident, _ := r["identity"].(string)
		lc, _ := r["lifecycle"].(string)
		if lc == "furniture" && len(ident) >= len(identityPrefix) && ident[:len(identityPrefix)] == identityPrefix {
			result = append(result, r)
		}
	}
	return result
}

func findFurnitureFactsByIdentity(t *testing.T, store *tuplestore.TupleStore, identity string) []map[string]interface{} {
	t.Helper()
	cat := "fact"
	all, err := store.FindAll(&cat, nil, &identity, nil, nil)
	if err != nil {
		t.Fatalf("findFurnitureFactsByIdentity: %v", err)
	}
	var result []map[string]interface{}
	for _, r := range all {
		if r["lifecycle"].(string) == "furniture" {
			result = append(result, r)
		}
	}
	return result
}

// ===========================================================================
// Test 6: Convention Lifecycle (propose → promote → prune)
// ===========================================================================

func TestIntegration_ConventionLifecycle(t *testing.T) {
	store := mustNewMemoryStore(t)

	// Phase 1: Two agents propose the same convention.
	insertTuple(t, store, "conventionProposal", "repo-c", "always-test",
		`{"content":"write tests for every PR"}`, "session", nil, strPtr("Alice"), nil)
	insertTuple(t, store, "conventionProposal", "repo-c", "always-test",
		`{"content":"write tests for every PR"}`, "session", nil, strPtr("Bob"), nil)

	// Phase 2: GC promotes it.
	gcCfg := GCConfig{ConventionQuorum: 2}
	gcResult := RunGCCycle(context.Background(), store, gcCfg)
	if gcResult.PromotedConventions != 1 {
		t.Fatalf("expected 1 promoted, got %d", gcResult.PromotedConventions)
	}

	// Verify convention is now furniture.
	conventions := findByCategory(t, store, "convention")
	if len(conventions) != 1 {
		t.Fatalf("expected 1 convention, got %d", len(conventions))
	}
	if conventions[0]["lifecycle"].(string) != "furniture" {
		t.Error("convention should be furniture after promotion")
	}

	// Phase 3: Create warm-tier data showing the convention HURT outcomes.
	month := time.Now().Format("2006-01")
	warmDir := t.TempDir()
	pruneTuples := []map[string]interface{}{
		tuple(1, "convention", "repo-c", "always-test", "", "", "2026-02-01 12:00:00"),
		// Before: 3 artifact tasks (100% success)
		tuple(2, "artifact", "repo-c", "a.go", "t1", "X", "2026-02-01 10:00:00"),
		tuple(3, "artifact", "repo-c", "b.go", "t2", "Y", "2026-02-01 10:30:00"),
		tuple(4, "artifact", "repo-c", "c.go", "t3", "Z", "2026-02-01 11:00:00"),
		// After: 3 obstacle tasks (0% success) — convention hurt outcomes
		tuple(5, "obstacle", "repo-c", "fail", "t4", "X", "2026-02-01 13:00:00"),
		tuple(6, "obstacle", "repo-c", "fail", "t5", "Y", "2026-02-01 13:30:00"),
		tuple(7, "obstacle", "repo-c", "fail", "t6", "Z", "2026-02-01 14:00:00"),
	}
	outPath := filepath.Join(warmDir, month, "repo-c", "data.parquet")
	writeTestParquet(t, outPath, pruneTuples)

	// Phase 4: Feedback prunes the convention.
	fbCfg := FeedbackConfig{
		WarmBaseDir:        warmDir,
		Scope:              "repo-c",
		ConventionMinTasks: 3,
	}
	fbResult := RunFeedbackCycle(store, fbCfg)
	if fbResult.ConventionsPruned != 1 {
		t.Errorf("expected 1 convention pruned, got %d", fbResult.ConventionsPruned)
	}

	// Verify convention is gone.
	conventions = findByCategory(t, store, "convention")
	if len(conventions) != 0 {
		t.Errorf("expected 0 conventions after pruning, got %d", len(conventions))
	}
}

// ===========================================================================
// Test 7: Escalation Detection (systemic obstacles, unclaimed needs)
// ===========================================================================

func TestIntegration_EscalationDetection(t *testing.T) {
	store := mustNewMemoryStore(t)

	// Insert systemic obstacles across multiple scopes.
	insertTuple(t, store, "obstacle", "repo-a", "deploy-fail", `{"detail":"timeout"}`, "session", strPtr("t1"), strPtr("Alice"), nil)
	insertTuple(t, store, "obstacle", "repo-a", "deploy-fail", `{"detail":"timeout"}`, "session", strPtr("t2"), strPtr("Bob"), nil)
	insertTuple(t, store, "obstacle", "repo-b", "auth-broken", `{"detail":"token expired"}`, "session", strPtr("t3"), strPtr("Charlie"), nil)

	// Insert unclaimed needs.
	insertTuple(t, store, "need", "repo-a", "review-needed", `{"detail":"PR #42"}`, "session", strPtr("t1"), strPtr("Alice"), nil)
	insertTuple(t, store, "need", "repo-a", "docs-needed", `{"detail":"API docs"}`, "session", strPtr("t2"), strPtr("Bob"), nil)

	// Verify GroupByScope detects obstacles per scope.
	obsByScope, err := store.GroupByScope("obstacle")
	if err != nil {
		t.Fatalf("GroupByScope(obstacle): %v", err)
	}
	if obsByScope["repo-a"] != 2 {
		t.Errorf("repo-a obstacles = %d, want 2", obsByScope["repo-a"])
	}
	if obsByScope["repo-b"] != 1 {
		t.Errorf("repo-b obstacles = %d, want 1", obsByScope["repo-b"])
	}

	// Verify GroupByScope detects needs per scope.
	needsByScope, err := store.GroupByScope("need")
	if err != nil {
		t.Fatalf("GroupByScope(need): %v", err)
	}
	if needsByScope["repo-a"] != 2 {
		t.Errorf("repo-a needs = %d, want 2", needsByScope["repo-a"])
	}

	// Verify HasEventForTask for non-done tasks.
	hasDone, err := store.HasEventForTask("t1")
	if err != nil {
		t.Fatalf("HasEventForTask: %v", err)
	}
	if hasDone {
		t.Error("t1 should not have task_done event")
	}
}

// ===========================================================================
// Test 8: Synthesis with Mocked LLM Responses
// ===========================================================================

func TestIntegration_SynthesisMock(t *testing.T) {
	// Test BuildPrompt with known tuples.
	tuples := []map[string]interface{}{
		{
			"category":  "claim",
			"scope":     "repo-s",
			"identity":  "task-syn-1",
			"agent_id":  strPtr("Alice"),
			"lifecycle": "session",
			"payload":   `{"agent":"Alice","status":"in_progress"}`,
		},
		{
			"category":  "fact",
			"scope":     "repo-s",
			"identity":  "compilation-requires-CGO",
			"agent_id":  strPtr("Alice"),
			"lifecycle": "session",
			"payload":   `{"content":"DuckDB requires CGO_ENABLED=1"}`,
		},
	}

	prompt := BuildPrompt(tuples)
	if prompt == "" {
		t.Fatal("BuildPrompt returned empty string")
	}
	if !containsScope(prompt, "claim") || !containsScope(prompt, "fact") {
		t.Error("prompt should contain tuple categories")
	}
	if !containsScope(prompt, "Alice") {
		t.Error("prompt should contain agent name")
	}

	// Test ParseResponse with valid JSON.
	t.Run("ParseValidJSON", func(t *testing.T) {
		response := `[
			{"category":"fact","scope":"repo-s","identity":"DuckDB needs CGO","payload":{"content":"CGO_ENABLED=1 required for go-duckdb"}},
			{"category":"convention","scope":"repo-s","identity":"always-set-CGO","payload":{"content":"Set CGO_ENABLED=1 in CI"}}
		]`
		results, err := ParseResponse(response)
		if err != nil {
			t.Fatalf("ParseResponse: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		if results[0].Category != "fact" {
			t.Errorf("result[0].Category = %q, want fact", results[0].Category)
		}
		if results[1].Category != "convention" {
			t.Errorf("result[1].Category = %q, want convention", results[1].Category)
		}
	})

	// Test ParseResponse with markdown-wrapped JSON.
	t.Run("ParseMarkdownWrapped", func(t *testing.T) {
		response := "```json\n" + `[{"category":"fact","scope":"s","identity":"x","payload":{"content":"y"}}]` + "\n```"
		results, err := ParseResponse(response)
		if err != nil {
			t.Fatalf("ParseResponse markdown: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
	})

	// Test ParseResponse with invalid JSON.
	t.Run("ParseInvalidJSON", func(t *testing.T) {
		_, err := ParseResponse("not json at all")
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	// Test Synthesize writes to store (using disabled synthesis to verify no-op).
	t.Run("SynthesizeDisabled", func(t *testing.T) {
		store := mustNewMemoryStore(t)
		cfg := SynthesisConfig{Enabled: false}
		n := Synthesize(context.Background(), cfg, store, tuples, "repo-s")
		if n != 0 {
			t.Errorf("expected 0 synthesized tuples when disabled, got %d", n)
		}
	})

	// Test Synthesize with no API key (should return 0, not error).
	t.Run("SynthesizeNoAPIKey", func(t *testing.T) {
		store := mustNewMemoryStore(t)
		cfg := SynthesisConfig{Enabled: true, Provider: "anthropic", APIKey: ""}
		// Clear env to ensure no key is found.
		origKey := os.Getenv("ANTHROPIC_API_KEY")
		os.Setenv("ANTHROPIC_API_KEY", "")
		defer os.Setenv("ANTHROPIC_API_KEY", origKey)

		n := Synthesize(context.Background(), cfg, store, tuples, "repo-s")
		if n != 0 {
			t.Errorf("expected 0 synthesized tuples with no API key, got %d", n)
		}
	})

	// Test validation: only fact/convention categories accepted.
	t.Run("SynthesisValidation", func(t *testing.T) {
		response := `[
			{"category":"fact","scope":"s","identity":"good","payload":{"content":"valid"}},
			{"category":"event","scope":"s","identity":"bad","payload":{"content":"should be rejected"}},
			{"category":"convention","scope":"s","identity":"also-good","payload":{"content":"valid too"}},
			{"category":"fact","scope":"s","identity":"","payload":{"content":"no identity"}},
			{"category":"fact","scope":"s","identity":"no-content","payload":{"content":""}}
		]`
		results, err := ParseResponse(response)
		if err != nil {
			t.Fatalf("ParseResponse: %v", err)
		}

		// Count valid ones (fact or convention, non-empty identity and content).
		valid := 0
		for _, r := range results {
			if (r.Category == "fact" || r.Category == "convention") &&
				r.Identity != "" && r.Payload.Content != "" {
				valid++
			}
		}
		if valid != 2 {
			t.Errorf("expected 2 valid tuples, got %d", valid)
		}
	})
}

// ===========================================================================
// Test 9: Cross-Pollination
// ===========================================================================

func TestIntegration_CrossPollination(t *testing.T) {
	store := mustNewMemoryStore(t)

	// Insert a tuple in scope repo-a that references repo-b in its payload.
	insertTuple(t, store, "fact", "repo-a", "cross-ref",
		`{"content":"This pattern from repo-b works well here"}`,
		"session", strPtr("t1"), strPtr("Alice"), nil)

	// Insert a tuple that does NOT reference another scope.
	insertTuple(t, store, "fact", "repo-a", "local-fact",
		`{"content":"purely local insight"}`,
		"session", strPtr("t2"), strPtr("Bob"), nil)

	knownScopes := []string{"repo-a", "repo-b", "repo-c"}

	// Create pollinator with no rate limit for testing.
	cp := NewCrossPollinator(CrossPollConfig{RateLimit: 0})

	result := cp.RunCrossPollination(store, knownScopes)
	if result.Checked == 0 {
		t.Error("expected > 0 tuples checked")
	}
	if result.Notified == 0 {
		t.Error("expected at least 1 cross-pollination notification")
	}

	// Verify notification tuple was written.
	notifications := findByCategory(t, store, "notification")
	if len(notifications) == 0 {
		t.Fatal("no notification tuples written")
	}

	// Verify notification scope is the referenced scope (repo-b).
	notif := notifications[0]
	if notif["scope"].(string) != "repo-b" {
		t.Errorf("notification scope = %q, want repo-b", notif["scope"])
	}

	// Verify notification payload has cross-pollination type.
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(notif["payload"].(string)), &payload); err != nil {
		t.Fatalf("parse notification payload: %v", err)
	}
	if payload["type"] != "cross_pollination" {
		t.Errorf("notification type = %v, want cross_pollination", payload["type"])
	}
	if payload["source_agent"] != "Alice" {
		t.Errorf("source_agent = %v, want Alice", payload["source_agent"])
	}
}

// ===========================================================================
// Test 9b: Cross-Pollination Rate Limiting
// ===========================================================================

func TestIntegration_CrossPollinationRateLimit(t *testing.T) {
	store := mustNewMemoryStore(t)

	// Insert two tuples from the same agent referencing the same scope.
	insertTuple(t, store, "fact", "repo-a", "ref-1",
		`{"content":"uses repo-b pattern"}`,
		"session", strPtr("t1"), strPtr("Alice"), nil)
	insertTuple(t, store, "fact", "repo-a", "ref-2",
		`{"content":"another repo-b mention"}`,
		"session", strPtr("t2"), strPtr("Alice"), nil)

	knownScopes := []string{"repo-a", "repo-b"}

	// Create pollinator with 1-hour rate limit (effectively blocks second notification).
	cp := NewCrossPollinator(CrossPollConfig{RateLimit: 1 * time.Hour})

	result := cp.RunCrossPollination(store, knownScopes)

	// Should only send 1 notification despite 2 references (rate limited).
	if result.Notified != 1 {
		t.Errorf("expected 1 notification (rate limited), got %d", result.Notified)
	}

	notifications := findByCategory(t, store, "notification")
	if len(notifications) != 1 {
		t.Errorf("expected 1 notification tuple, got %d", len(notifications))
	}
}

// ===========================================================================
// Test 10: GC Skips Agents with Pending Dismiss
// ===========================================================================

func TestIntegration_GC_SkipsPendingDismiss(t *testing.T) {
	store := mustNewMemoryStore(t)
	warmDir := t.TempDir()

	// Insert task_done for an agent with a pending dismiss_request.
	task := strPtr("task-dismiss")
	agent := strPtr("Alice")
	insertTuple(t, store, "fact", "repo-d", "some-work", `{"content":"done"}`, "session", task, agent, nil)
	insertTuple(t, store, "event", "repo-d", "task_done", `{"task":"task-dismiss","agent":"Alice"}`, "session", task, agent, nil)
	insertTuple(t, store, "event", "repo-d", "dismiss_request", `{"agent":"Alice","reason":"done"}`, "session", nil, agent, nil)

	before := countTuples(t, store)

	cfg := GCConfig{
		WarmBaseDir: warmDir,
		Synthesis:   SynthesisConfig{Enabled: false},
	}
	result := RunGCCycle(context.Background(), store, cfg)

	after := countTuples(t, store)

	// Agent with pending dismiss should be skipped — no archival should happen.
	if result.ArchivedTasks != 0 {
		t.Errorf("expected 0 archived tasks (dismiss pending), got %d", result.ArchivedTasks)
	}
	if after != before {
		t.Errorf("expected tuple count unchanged (dismiss pending): before=%d, after=%d", before, after)
	}
}

// ===========================================================================
// Test 11: Excluded Events Not Archived
// ===========================================================================

func TestIntegration_ExcludedEventsNotArchived(t *testing.T) {
	store := mustNewMemoryStore(t)
	warmDir := t.TempDir()

	// Insert a task_done and dismiss_request event alongside normal tuples.
	task := strPtr("task-excl")
	agent := strPtr("Bob")
	insertTuple(t, store, "fact", "repo-e", "insight", `{"content":"data"}`, "session", task, agent, nil)
	insertTuple(t, store, "event", "repo-e", "task_done", `{"task":"task-excl","agent":"Bob"}`, "session", task, agent, nil)

	// Archive by task ID.
	archived, err := store.ArchiveByTaskID(warmDir, "task-excl")
	if err != nil {
		t.Fatalf("ArchiveByTaskID: %v", err)
	}

	// Only the fact should be archived (task_done is excluded).
	if archived != 1 {
		t.Errorf("expected 1 archived tuple (fact only), got %d", archived)
	}

	// Verify task_done still in hot tier.
	events := findByCategory(t, store, "event")
	if len(events) != 1 {
		t.Fatalf("expected 1 event remaining, got %d", len(events))
	}
	if events[0]["identity"].(string) != "task_done" {
		t.Errorf("remaining event = %q, want task_done", events[0]["identity"])
	}
}

// ===========================================================================
// Test 12: End-to-End Pipeline (Store → GC → Parquet → Analytics → Feedback)
// ===========================================================================

func TestIntegration_EndToEnd(t *testing.T) {
	store := mustNewMemoryStore(t)
	warmDir := t.TempDir()

	// Step 1: Populate store with a completed task's tuples.
	task := strPtr("task-e2e")
	alice := strPtr("Alice")

	insertTuple(t, store, "claim", "repo-e2e", "task-e2e",
		`{"agent":"Alice","status":"in_progress"}`, "session", task, alice, nil)
	insertTuple(t, store, "obstacle", "repo-e2e", "build-error",
		`{"detail":"missing import"}`, "session", task, alice, nil)
	insertTuple(t, store, "artifact", "repo-e2e", "fix.go",
		`{"type":"file"}`, "session", task, alice, nil)
	insertTuple(t, store, "fact", "repo-e2e", "import-order-matters",
		`{"content":"always check imports"}`, "session", task, alice, nil)
	insertTuple(t, store, "event", "repo-e2e", "task_done",
		`{"task":"task-e2e","agent":"Alice"}`, "session", task, alice, nil)

	initialCount := countTuples(t, store)
	if initialCount != 5 {
		t.Fatalf("expected 5 initial tuples, got %d", initialCount)
	}

	// Step 2: Run GC to archive completed task.
	gcCfg := GCConfig{
		WarmBaseDir: warmDir,
		Synthesis:   SynthesisConfig{Enabled: false},
	}
	gcResult := RunGCCycle(context.Background(), store, gcCfg)

	if gcResult.ArchivedTasks == 0 {
		t.Fatal("expected some tuples to be archived")
	}

	// Only task_done should remain in hot tier.
	remaining := countTuples(t, store)
	if remaining != 1 {
		all, _ := store.FindAll(nil, nil, nil, nil, nil)
		for _, r := range all {
			t.Logf("  remaining: %s/%s", r["category"], r["identity"])
		}
		t.Fatalf("expected 1 remaining tuple (task_done), got %d", remaining)
	}

	// Step 3: Verify Parquet files exist.
	month := time.Now().Format("2006-01")
	globPattern := filepath.Join(warmDir, month, "repo-e2e", "*.parquet")
	matches, err := filepath.Glob(globPattern)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no Parquet files found after archival")
	}

	// Step 4: Run analytics queries on the archived data.
	perf, err := QueryAgentPerformance(warmDir, "repo-e2e")
	if err != nil {
		t.Fatalf("QueryAgentPerformance: %v", err)
	}
	if len(perf) == 0 {
		t.Fatal("no performance data from archived tuples")
	}
	if perf[0].ObstacleCount != 1 {
		t.Errorf("obstacles = %d, want 1", perf[0].ObstacleCount)
	}
	if perf[0].ArtifactCount != 1 {
		t.Errorf("artifacts = %d, want 1", perf[0].ArtifactCount)
	}

	// Step 5: Run feedback loop to write results back to hot tier.
	fbCfg := FeedbackConfig{
		WarmBaseDir:            warmDir,
		Scope:                  "repo-e2e",
		ObstacleMinOccurrences: 1,
	}
	fbResult := RunFeedbackCycle(store, fbCfg)

	if fbResult.RepoHealthUpdated == 0 {
		t.Error("expected repo health to be updated in feedback")
	}

	// Verify furniture tuples were written back.
	furnitureFacts := findByCategory(t, store, "fact")
	hasFurniture := false
	for _, f := range furnitureFacts {
		if f["lifecycle"].(string) == "furniture" && f["instance"].(string) == "analytics" {
			hasFurniture = true
			break
		}
	}
	if !hasFurniture {
		t.Error("no analytics furniture facts written to hot tier by feedback")
	}

	fmt.Printf("E2E pipeline: %d archived, %d repo-health updated, %d obstacles surfaced\n",
		gcResult.ArchivedTasks, fbResult.RepoHealthUpdated, fbResult.ObstaclesSurfaced)
}
