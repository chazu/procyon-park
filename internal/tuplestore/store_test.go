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

// ---------------------------------------------------------------------------
// GC Method Tests
// ---------------------------------------------------------------------------

func TestDeleteExpiredEphemeral(t *testing.T) {
	s := mustNewMemoryStore(t)

	// Insert an ephemeral tuple with 0-second TTL (already expired)
	ttl0 := 0
	s.Insert("notification", "agent1", "msg-1", "local",
		`{"type":"info"}`, "ephemeral", nil, nil, &ttl0)

	// Insert a session tuple (should not be affected)
	s.Insert("claim", "repo", "task-1", "local", `{}`, "session", nil, nil, nil)

	// Insert an ephemeral tuple with a very large TTL (should not be expired)
	ttl := 999999
	s.Insert("notification", "agent2", "msg-2", "local",
		`{"type":"info"}`, "ephemeral", nil, nil, &ttl)

	count, err := s.DeleteExpiredEphemeral()
	if err != nil {
		t.Fatalf("DeleteExpiredEphemeral: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 deleted, got %d", count)
	}

	// Verify: 2 tuples remain (session + non-expired ephemeral)
	all, _ := s.FindAll(nil, nil, nil, nil, nil)
	if len(all) != 2 {
		t.Fatalf("expected 2 remaining, got %d", len(all))
	}
}

func TestFindExpiredEphemeral(t *testing.T) {
	s := mustNewMemoryStore(t)

	ttl0 := 0
	s.Insert("notification", "agent1", "msg-1", "local",
		`{"type":"info"}`, "ephemeral", nil, nil, &ttl0)

	rows, err := s.FindExpiredEphemeral()
	if err != nil {
		t.Fatalf("FindExpiredEphemeral: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 expired, got %d", len(rows))
	}
	if rows[0]["identity"] != "msg-1" {
		t.Errorf("identity = %v, want msg-1", rows[0]["identity"])
	}
}

func TestFindStaleClaims(t *testing.T) {
	s := mustNewMemoryStore(t)

	// Insert a claim (just created, so 0 seconds old)
	s.Insert("claim", "repo", "task-1", "local",
		`{"agent":"Rustle","status":"in_progress"}`, "session", nil, nil, nil)

	// With 0 second max age, the claim should be "stale"
	rows, err := s.FindStaleClaims(0)
	if err != nil {
		t.Fatalf("FindStaleClaims: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 stale claim, got %d", len(rows))
	}

	// With 999999 second max age, nothing is stale
	rows2, err := s.FindStaleClaims(999999)
	if err != nil {
		t.Fatalf("FindStaleClaims large age: %v", err)
	}
	if len(rows2) != 0 {
		t.Fatalf("expected 0 stale claims, got %d", len(rows2))
	}
}

func TestHasEventForTask(t *testing.T) {
	s := mustNewMemoryStore(t)

	// No events yet
	has, err := s.HasEventForTask("task-123")
	if err != nil {
		t.Fatalf("HasEventForTask: %v", err)
	}
	if has {
		t.Fatal("expected false, no events inserted")
	}

	// Insert a task_done event with task in payload
	s.Insert("event", "repo", "task_done", "local",
		`{"task":"task-123","agent":"Rustle"}`, "session", nil, nil, nil)

	has2, err := s.HasEventForTask("task-123")
	if err != nil {
		t.Fatalf("HasEventForTask: %v", err)
	}
	if !has2 {
		t.Fatal("expected true after inserting task_done event")
	}
}

func TestGroupByScope(t *testing.T) {
	s := mustNewMemoryStore(t)

	s.Insert("obstacle", "repo-a", "build-fail", "local", `{}`, "session", nil, nil, nil)
	s.Insert("obstacle", "repo-a", "test-fail", "local", `{}`, "session", nil, nil, nil)
	s.Insert("obstacle", "repo-b", "lint-fail", "local", `{}`, "session", nil, nil, nil)

	groups, err := s.GroupByScope("obstacle")
	if err != nil {
		t.Fatalf("GroupByScope: %v", err)
	}
	if groups["repo-a"] != 2 {
		t.Errorf("repo-a count = %d, want 2", groups["repo-a"])
	}
	if groups["repo-b"] != 1 {
		t.Errorf("repo-b count = %d, want 1", groups["repo-b"])
	}
}

func TestFindUnclaimedNeeds(t *testing.T) {
	s := mustNewMemoryStore(t)

	s.Insert("need", "repo", "help-with-tests", "local",
		`{"detail":"need help"}`, "session", nil, nil, nil)

	// With 0 second threshold, the need is unclaimed
	rows, err := s.FindUnclaimedNeeds(0)
	if err != nil {
		t.Fatalf("FindUnclaimedNeeds: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 unclaimed need, got %d", len(rows))
	}

	// With very large threshold, nothing is old enough
	rows2, err := s.FindUnclaimedNeeds(999999)
	if err != nil {
		t.Fatalf("FindUnclaimedNeeds large age: %v", err)
	}
	if len(rows2) != 0 {
		t.Fatalf("expected 0 unclaimed needs, got %d", len(rows2))
	}
}

func TestFindDuplicateConventionProposals(t *testing.T) {
	s := mustNewMemoryStore(t)

	agent1 := "Rustle"
	agent2 := "Bramble"

	// Two proposals for same convention from different agents
	s.Insert("conventionProposal", "repo", "use-snake-case", "local",
		`{"detail":"snake case for variables"}`, "session", nil, &agent1, nil)
	s.Insert("conventionProposal", "repo", "use-snake-case", "local",
		`{"detail":"snake_case is better"}`, "session", nil, &agent2, nil)

	// One proposal from a single agent (should not appear)
	s.Insert("conventionProposal", "repo", "use-tabs", "local",
		`{"detail":"tabs are better"}`, "session", nil, &agent1, nil)

	results, err := s.FindDuplicateConventionProposals()
	if err != nil {
		t.Fatalf("FindDuplicateConventionProposals: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 duplicate, got %d", len(results))
	}
	if results[0]["identity"] != "use-snake-case" {
		t.Errorf("identity = %v, want use-snake-case", results[0]["identity"])
	}
	if results[0]["agent_count"] != int64(2) {
		t.Errorf("agent_count = %v, want 2", results[0]["agent_count"])
	}
}

func TestMigrationIdempotent(t *testing.T) {
	s := mustNewMemoryStore(t)

	// Running migrate again should be a no-op
	if err := s.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}
