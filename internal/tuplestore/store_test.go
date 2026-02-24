package tuplestore

import (
	"testing"
)

func mustNewMemoryStore(t *testing.T) *TupleStore {
	t.Helper()
	s, err := NewMemoryStore()
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func strPtr(s string) *string { return &s }
func intPtr(n int) *int       { return &n }

func TestInsertAndFindOne(t *testing.T) {
	s := mustNewMemoryStore(t)

	id, err := s.Insert("fact", "myrepo", "build-works", "local", `{"detail":"yes"}`, "session", nil, nil, nil)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	row, err := s.FindOne(strPtr("fact"), strPtr("myrepo"), nil, nil, nil)
	if err != nil {
		t.Fatalf("FindOne: %v", err)
	}
	if row == nil {
		t.Fatal("FindOne returned nil, expected a tuple")
	}
	if row["category"] != "fact" {
		t.Errorf("category = %v, want fact", row["category"])
	}
	if row["scope"] != "myrepo" {
		t.Errorf("scope = %v, want myrepo", row["scope"])
	}
	if row["identity"] != "build-works" {
		t.Errorf("identity = %v, want build-works", row["identity"])
	}
}

func TestFindOneNoMatch(t *testing.T) {
	s := mustNewMemoryStore(t)

	row, err := s.FindOne(strPtr("nonexistent"), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("FindOne: %v", err)
	}
	if row != nil {
		t.Fatalf("expected nil, got %v", row)
	}
}

func TestFindAndDelete(t *testing.T) {
	s := mustNewMemoryStore(t)

	s.Insert("claim", "repo", "task-1", "local", `{"agent":"Rustle"}`, "session", nil, nil, nil)

	row, err := s.FindAndDelete(strPtr("claim"), strPtr("repo"), nil, nil, nil)
	if err != nil {
		t.Fatalf("FindAndDelete: %v", err)
	}
	if row == nil {
		t.Fatal("FindAndDelete returned nil")
	}
	if row["identity"] != "task-1" {
		t.Errorf("identity = %v, want task-1", row["identity"])
	}

	// Should be gone now
	row2, err := s.FindOne(strPtr("claim"), strPtr("repo"), nil, nil, nil)
	if err != nil {
		t.Fatalf("FindOne after delete: %v", err)
	}
	if row2 != nil {
		t.Fatal("tuple should have been deleted")
	}
}

func TestFindAll(t *testing.T) {
	s := mustNewMemoryStore(t)

	s.Insert("fact", "repo", "a", "local", `{}`, "furniture", nil, nil, nil)
	s.Insert("fact", "repo", "b", "local", `{}`, "furniture", nil, nil, nil)
	s.Insert("claim", "repo", "c", "local", `{}`, "session", nil, nil, nil)

	rows, err := s.FindAll(strPtr("fact"), strPtr("repo"), nil, nil, nil)
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 facts, got %d", len(rows))
	}
}

func TestDelete(t *testing.T) {
	s := mustNewMemoryStore(t)

	id, _ := s.Insert("event", "repo", "task_done", "local", `{}`, "session", nil, nil, nil)

	ok, err := s.Delete(id)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !ok {
		t.Fatal("Delete returned false, expected true")
	}

	ok2, err := s.Delete(id)
	if err != nil {
		t.Fatalf("Delete again: %v", err)
	}
	if ok2 {
		t.Fatal("Delete returned true for already-deleted tuple")
	}
}

func TestDeleteByPattern(t *testing.T) {
	s := mustNewMemoryStore(t)

	s.Insert("claim", "repo", "t1", "local", `{}`, "session", nil, nil, nil)
	s.Insert("claim", "repo", "t2", "local", `{}`, "session", nil, nil, nil)
	s.Insert("fact", "repo", "f1", "local", `{}`, "furniture", nil, nil, nil)

	count, err := s.DeleteByPattern(strPtr("claim"), strPtr("repo"), nil, nil)
	if err != nil {
		t.Fatalf("DeleteByPattern: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 deleted, got %d", count)
	}

	// fact should still exist
	rows, _ := s.FindAll(strPtr("fact"), nil, nil, nil, nil)
	if len(rows) != 1 {
		t.Fatalf("expected 1 fact remaining, got %d", len(rows))
	}
}

func TestCount(t *testing.T) {
	s := mustNewMemoryStore(t)

	s.Insert("fact", "repo", "a", "local", `{}`, "furniture", nil, nil, nil)
	s.Insert("fact", "repo", "b", "local", `{}`, "furniture", nil, nil, nil)
	s.Insert("claim", "repo", "c", "local", `{}`, "session", nil, nil, nil)

	count, err := s.Count(strPtr("fact"), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2, got %d", count)
	}

	all, err := s.Count(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Count all: %v", err)
	}
	if all != 3 {
		t.Fatalf("expected 3, got %d", all)
	}
}

func TestFTS5PayloadSearch(t *testing.T) {
	s := mustNewMemoryStore(t)

	s.Insert("obstacle", "repo", "build-fail", "local",
		`{"detail":"missing dependency libfoo","task":"task-123"}`, "session", nil, nil, nil)
	s.Insert("obstacle", "repo", "test-fail", "local",
		`{"detail":"timeout in integration tests","task":"task-456"}`, "session", nil, nil, nil)
	s.Insert("fact", "repo", "note", "local",
		`{"content":"libfoo is available on homebrew"}`, "furniture", nil, nil, nil)

	// Search for "libfoo" across all tuples
	rows, err := s.FindAll(nil, nil, nil, nil, strPtr("libfoo"))
	if err != nil {
		t.Fatalf("FTS5 findAll: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 matches for 'libfoo', got %d", len(rows))
	}

	// Search scoped to obstacles
	rows2, err := s.FindAll(strPtr("obstacle"), nil, nil, nil, strPtr("timeout"))
	if err != nil {
		t.Fatalf("FTS5 scoped findAll: %v", err)
	}
	if len(rows2) != 1 {
		t.Fatalf("expected 1 match for 'timeout' in obstacles, got %d", len(rows2))
	}

	// FTS5 with count
	count, err := s.Count(nil, nil, nil, nil, strPtr("libfoo"))
	if err != nil {
		t.Fatalf("FTS5 count: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}
}

func TestOptionalFields(t *testing.T) {
	s := mustNewMemoryStore(t)

	taskID := "task-abc"
	agentID := "Rustle"
	ttl := 300

	id, err := s.Insert("notification", "Rustle", "msg-1", "local",
		`{"type":"info"}`, "ephemeral", &taskID, &agentID, &ttl)
	if err != nil {
		t.Fatalf("Insert with optional fields: %v", err)
	}

	row, err := s.FindOne(strPtr("notification"), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("FindOne: %v", err)
	}
	if row == nil {
		t.Fatal("expected row")
	}
	if row["id"] != id {
		t.Errorf("id = %v, want %d", row["id"], id)
	}

	gotTask, ok := row["task_id"].(*string)
	if !ok || gotTask == nil || *gotTask != taskID {
		t.Errorf("task_id = %v, want %s", row["task_id"], taskID)
	}

	gotAgent, ok := row["agent_id"].(*string)
	if !ok || gotAgent == nil || *gotAgent != agentID {
		t.Errorf("agent_id = %v, want %s", row["agent_id"], agentID)
	}
}

func TestWildcardPattern(t *testing.T) {
	s := mustNewMemoryStore(t)

	s.Insert("fact", "repo1", "a", "local", `{}`, "furniture", nil, nil, nil)
	s.Insert("fact", "repo2", "b", "local", `{}`, "furniture", nil, nil, nil)
	s.Insert("claim", "repo1", "c", "local", `{}`, "session", nil, nil, nil)

	// All tuples (full wildcard)
	all, err := s.FindAll(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("FindAll wildcard: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3, got %d", len(all))
	}

	// All in repo1
	repo1, err := s.FindAll(nil, strPtr("repo1"), nil, nil, nil)
	if err != nil {
		t.Fatalf("FindAll repo1: %v", err)
	}
	if len(repo1) != 2 {
		t.Fatalf("expected 2 in repo1, got %d", len(repo1))
	}
}

func TestMigrationIdempotent(t *testing.T) {
	s := mustNewMemoryStore(t)

	// Running migrate again should be a no-op
	if err := s.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}
