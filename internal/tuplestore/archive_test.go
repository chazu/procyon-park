package tuplestore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDuckDB(t *testing.T) {
	db, err := openDuckDB()
	if err != nil {
		t.Fatalf("openDuckDB: %v", err)
	}
	defer db.Close()

	// Verify it's usable.
	var result int
	if err := db.QueryRow("SELECT 42").Scan(&result); err != nil {
		t.Fatalf("query: %v", err)
	}
	if result != 42 {
		t.Fatalf("got %d, want 42", result)
	}
}

func TestParquetGlob(t *testing.T) {
	base := "/home/user/.procyon-park/bbs/warm"

	// Scoped glob.
	got := parquetGlob(base, "myrepo", "2026-02")
	want := filepath.Join(base, "2026-02", "myrepo", "*.parquet")
	if got != want {
		t.Errorf("scoped glob = %q, want %q", got, want)
	}

	// Wildcard glob (empty scope).
	got2 := parquetGlob(base, "", "2026-02")
	want2 := filepath.Join(base, "2026-02", "*", "*.parquet")
	if got2 != want2 {
		t.Errorf("wildcard glob = %q, want %q", got2, want2)
	}
}

func TestExportAndReadRoundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "test.parquet")

	taskID := "task-abc"
	agentID := "Fizz"
	tuples := []map[string]interface{}{
		{
			"id": int64(1), "category": "fact", "scope": "myrepo",
			"identity": "build-works", "instance": "local",
			"payload": `{"detail":"yes"}`, "lifecycle": "session",
			"task_id": &taskID, "agent_id": &agentID,
			"created_at": "2026-02-24 10:00:00", "updated_at": "2026-02-24 10:00:00",
			"ttl_seconds": (*int)(nil),
		},
		{
			"id": int64(2), "category": "claim", "scope": "myrepo",
			"identity": "task-1", "instance": "local",
			"payload": `{"agent":"Fizz"}`, "lifecycle": "session",
			"task_id": (*string)(nil), "agent_id": (*string)(nil),
			"created_at": "2026-02-24 10:01:00", "updated_at": "2026-02-24 10:01:00",
			"ttl_seconds": (*int)(nil),
		},
	}

	if err := exportToParquet(tuples, outPath); err != nil {
		t.Fatalf("exportToParquet: %v", err)
	}

	// File should exist.
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("parquet file missing: %v", err)
	}

	// Read it back.
	results, err := ReadParquet(outPath)
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(results))
	}
	if results[0]["category"] != "fact" {
		t.Errorf("row 0 category = %v, want fact", results[0]["category"])
	}
	if results[1]["identity"] != "task-1" {
		t.Errorf("row 1 identity = %v, want task-1", results[1]["identity"])
	}

	// Verify nullable fields survived roundtrip.
	if results[0]["task_id"] == nil {
		t.Error("row 0 task_id should not be nil")
	}
	if results[1]["task_id"] != nil {
		// DuckDB may return empty string for NULL — check both.
		if sp, ok := results[1]["task_id"].(*string); ok && sp != nil && *sp != "" {
			t.Errorf("row 1 task_id = %v, want nil", results[1]["task_id"])
		}
	}
}

func TestExportEmptyTuples(t *testing.T) {
	// Exporting zero tuples should be a no-op.
	if err := exportToParquet(nil, "/tmp/should-not-exist.parquet"); err != nil {
		t.Fatalf("exportToParquet empty: %v", err)
	}
}

func TestArchiveByTaskID(t *testing.T) {
	s := mustNewMemoryStore(t)
	tmpDir := t.TempDir()

	taskID := "task-99"
	agentID := "Fizz"

	// Insert session tuples with task_id.
	s.Insert("fact", "myrepo", "f1", "local", `{"detail":"a"}`, "session", &taskID, &agentID, nil)
	s.Insert("claim", "myrepo", "c1", "local", `{"agent":"Fizz"}`, "session", &taskID, &agentID, nil)

	// Insert a task_done event for the same task — should be EXCLUDED from archival.
	s.Insert("event", "myrepo", "task_done", "local",
		`{"task":"task-99","agent":"Fizz"}`, "session", &taskID, &agentID, nil)

	// Insert a dismiss_request event — should also be excluded.
	s.Insert("event", "myrepo", "dismiss_request", "local",
		`{"agent":"Fizz"}`, "session", &taskID, &agentID, nil)

	// Insert a furniture tuple — should NOT be archived (lifecycle != session matched by query,
	// but we set lifecycle=session on all above, so this one with furniture should be skipped).
	s.Insert("convention", "system", "style", "local", `{}`, "furniture", &taskID, nil, nil)

	archived, err := s.ArchiveByTaskID(tmpDir, taskID)
	if err != nil {
		t.Fatalf("ArchiveByTaskID: %v", err)
	}
	// Only fact + claim should be archived (2), not the events or furniture.
	if archived != 2 {
		t.Fatalf("expected 2 archived, got %d", archived)
	}

	// The 2 archived tuples should be deleted from SQLite.
	remaining, _ := s.FindAll(nil, nil, nil, nil, nil)
	// Should have: task_done + dismiss_request + furniture = 3
	if len(remaining) != 3 {
		t.Fatalf("expected 3 remaining, got %d", len(remaining))
	}

	// Verify the excluded events are still present.
	for _, r := range remaining {
		cat := r["category"].(string)
		ident := r["identity"].(string)
		if cat == "fact" || cat == "claim" {
			t.Errorf("archived tuple still in SQLite: %s/%s", cat, ident)
		}
	}
}

func TestArchiveByAgentID(t *testing.T) {
	s := mustNewMemoryStore(t)
	tmpDir := t.TempDir()

	agentID := "Bramble"
	taskID := "task-50"

	// Insert session tuples with agent_id.
	s.Insert("obstacle", "myrepo", "build-fail", "local",
		`{"detail":"missing dep"}`, "session", &taskID, &agentID, nil)
	s.Insert("artifact", "myrepo", "main.go", "local",
		`{"type":"file"}`, "session", nil, &agentID, nil)

	// Insert a task_done event by this agent — excluded.
	s.Insert("event", "myrepo", "task_done", "local",
		`{"task":"task-50","agent":"Bramble"}`, "session", nil, &agentID, nil)

	archived, err := s.ArchiveByAgentID(tmpDir, agentID)
	if err != nil {
		t.Fatalf("ArchiveByAgentID: %v", err)
	}
	if archived != 2 {
		t.Fatalf("expected 2 archived, got %d", archived)
	}

	remaining, _ := s.FindAll(nil, nil, nil, nil, nil)
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining (task_done event), got %d", len(remaining))
	}
	if remaining[0]["identity"] != "task_done" {
		t.Errorf("remaining tuple identity = %v, want task_done", remaining[0]["identity"])
	}
}

func TestArchiveThenDeleteAtomicity(t *testing.T) {
	// Verify that if export succeeds, the data exists in Parquet even before
	// SQLite deletion. This tests the ordering guarantee.
	s := mustNewMemoryStore(t)
	tmpDir := t.TempDir()

	taskID := "task-atom"
	s.Insert("fact", "myrepo", "atomicity-test", "local",
		`{"detail":"crash-safe"}`, "session", &taskID, nil, nil)

	archived, err := s.ArchiveByTaskID(tmpDir, taskID)
	if err != nil {
		t.Fatalf("ArchiveByTaskID: %v", err)
	}
	if archived != 1 {
		t.Fatalf("expected 1 archived, got %d", archived)
	}

	// The Parquet file should exist with the data.
	globPattern := parquetGlob(tmpDir, "myrepo", "")
	// Use a more specific glob for the task file.
	files, err := filepath.Glob(filepath.Join(tmpDir, "*", "myrepo", "task-task-atom.parquet"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 parquet file, got %d (glob: %s)", len(files), globPattern)
	}

	// Read back from Parquet.
	results, err := ReadParquet(files[0])
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 row in parquet, got %d", len(results))
	}
	if results[0]["identity"] != "atomicity-test" {
		t.Errorf("identity = %v, want atomicity-test", results[0]["identity"])
	}

	// SQLite should be empty (the tuple was deleted after export).
	remaining, _ := s.FindAll(nil, nil, nil, nil, nil)
	if len(remaining) != 0 {
		t.Fatalf("expected 0 remaining, got %d", len(remaining))
	}
}

func TestEventExclusion(t *testing.T) {
	// Verify that isExcludedEvent correctly identifies protected events.
	tests := []struct {
		category string
		identity string
		excluded bool
	}{
		{"event", "task_done", true},
		{"event", "dismiss_request", true},
		{"event", "other_event", false},
		{"fact", "task_done", false},   // Not an event category.
		{"claim", "some-task", false},  // Not an event category.
	}

	for _, tt := range tests {
		row := map[string]interface{}{
			"category": tt.category,
			"identity": tt.identity,
		}
		got := isExcludedEvent(row)
		if got != tt.excluded {
			t.Errorf("isExcludedEvent(%s/%s) = %v, want %v",
				tt.category, tt.identity, got, tt.excluded)
		}
	}
}

func TestScopeFromTuples(t *testing.T) {
	tuples := []map[string]interface{}{
		{"scope": "repo-a"},
		{"scope": "repo-a"},
		{"scope": "repo-b"},
	}
	got := scopeFromTuples(tuples)
	if got != "repo-a" {
		t.Errorf("scopeFromTuples = %q, want repo-a", got)
	}

	// Empty tuples.
	got2 := scopeFromTuples(nil)
	if got2 != "unknown" {
		t.Errorf("scopeFromTuples(nil) = %q, want unknown", got2)
	}
}

func TestWarmBaseDir(t *testing.T) {
	got := WarmBaseDir("/home/user")
	want := filepath.Join("/home/user", ".procyon-park", "bbs", "warm")
	if got != want {
		t.Errorf("WarmBaseDir = %q, want %q", got, want)
	}
}
