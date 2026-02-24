package prime

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chazu/procyon-park/internal/tuplestore"
)

func newTestStore(t *testing.T) *tuplestore.TupleStore {
	t.Helper()
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func insertTuple(t *testing.T, store *tuplestore.TupleStore, category, scope, identity, payload, lifecycle string) {
	t.Helper()
	_, err := store.Insert(category, scope, identity, "local", payload, lifecycle, nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to insert tuple: %v", err)
	}
}

func TestBuildAgentContext_EmptyStore(t *testing.T) {
	store := newTestStore(t)

	result, err := BuildAgentContext(store, "myrepo", "task-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "BBS TUPLESPACE CONTEXT") {
		t.Error("should contain context header")
	}
	// With empty store, no category sections should appear.
	if strings.Contains(result, "CONVENTIONS:") {
		t.Error("should not contain CONVENTIONS section with empty store")
	}
	if strings.Contains(result, "FACTS:") {
		t.Error("should not contain FACTS section with empty store")
	}
}

func TestBuildAgentContext_Conventions(t *testing.T) {
	store := newTestStore(t)

	insertTuple(t, store, "convention", "myrepo", "use-snake-case", `{"detail":"all identifiers"}`, "furniture")
	insertTuple(t, store, "convention", "myrepo", "test-before-commit", `{"detail":"always run tests"}`, "furniture")

	result, err := BuildAgentContext(store, "myrepo", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "CONVENTIONS:") {
		t.Error("should contain CONVENTIONS section")
	}
	if !strings.Contains(result, "use-snake-case") {
		t.Error("should contain convention identity")
	}
	if !strings.Contains(result, "detail=all identifiers") {
		t.Error("should contain payload summary")
	}
}

func TestBuildAgentContext_Facts(t *testing.T) {
	store := newTestStore(t)

	insertTuple(t, store, "fact", "myrepo", "go-version-1.21", `{"source":"go.mod"}`, "furniture")

	result, err := BuildAgentContext(store, "myrepo", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "FACTS:") {
		t.Error("should contain FACTS section")
	}
	if !strings.Contains(result, "go-version-1.21") {
		t.Error("should contain fact identity")
	}
}

func TestBuildAgentContext_ActiveClaims(t *testing.T) {
	store := newTestStore(t)

	insertTuple(t, store, "claim", "myrepo", "task-42", `{"agent":"Sprocket","status":"in_progress"}`, "session")

	result, err := BuildAgentContext(store, "myrepo", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "ACTIVE AGENT ACTIVITY:") {
		t.Error("should contain ACTIVE AGENT ACTIVITY section")
	}
	if !strings.Contains(result, "task-42") {
		t.Error("should contain claim identity")
	}
	if !strings.Contains(result, "agent=Sprocket") {
		t.Error("should contain agent name in payload summary")
	}
}

func TestBuildAgentContext_Obstacles(t *testing.T) {
	store := newTestStore(t)

	insertTuple(t, store, "obstacle", "myrepo", "tests-failing", `{"task":"task-1","detail":"CI red"}`, "session")

	result, err := BuildAgentContext(store, "myrepo", "task-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "OBSTACLES:") {
		t.Error("should contain OBSTACLES section")
	}
	if !strings.Contains(result, "tests-failing") {
		t.Error("should contain obstacle identity")
	}
}

func TestBuildAgentContext_Needs(t *testing.T) {
	store := newTestStore(t)

	insertTuple(t, store, "need", "myrepo", "api-docs", `{"task":"task-1","detail":"need API documentation"}`, "session")

	result, err := BuildAgentContext(store, "myrepo", "task-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "NEEDS:") {
		t.Error("should contain NEEDS section")
	}
	if !strings.Contains(result, "api-docs") {
		t.Error("should contain need identity")
	}
}

func TestBuildAgentContext_TaskEscalations(t *testing.T) {
	store := newTestStore(t)

	// Obstacle referencing our task
	insertTuple(t, store, "obstacle", "myrepo", "blocked-on-dep", `{"task":"task-99","detail":"dependency missing"}`, "session")
	// Need referencing our task
	insertTuple(t, store, "need", "myrepo", "design-review", `{"task":"task-99","detail":"needs review"}`, "session")
	// Obstacle for a different task (should not appear in task-specific)
	insertTuple(t, store, "obstacle", "myrepo", "other-issue", `{"task":"task-50","detail":"unrelated"}`, "session")

	result, err := BuildAgentContext(store, "myrepo", "task-99")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "TASK-SPECIFIC ESCALATIONS:") {
		t.Error("should contain TASK-SPECIFIC ESCALATIONS section")
	}
	if !strings.Contains(result, "blocked-on-dep") {
		t.Error("should contain task-specific obstacle")
	}
	if !strings.Contains(result, "design-review") {
		t.Error("should contain task-specific need")
	}
}

func TestBuildAgentContext_NoTaskEscalationsWithoutTaskID(t *testing.T) {
	store := newTestStore(t)

	insertTuple(t, store, "obstacle", "myrepo", "some-issue", `{"task":"task-1"}`, "session")

	result, err := BuildAgentContext(store, "myrepo", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result, "TASK-SPECIFIC ESCALATIONS:") {
		t.Error("should not contain TASK-SPECIFIC ESCALATIONS when taskID is empty")
	}
}

func TestBuildAgentContext_CapAtLimit(t *testing.T) {
	store := newTestStore(t)

	// Insert more than maxPerCategory facts
	for i := 0; i < maxPerCategory+5; i++ {
		insertTuple(t, store, "fact", "myrepo", fmt.Sprintf("fact-%d", i), `{}`, "furniture")
	}

	result, err := BuildAgentContext(store, "myrepo", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "more fact tuples omitted") {
		t.Error("should contain omission notice when exceeding limit")
	}

	// Count fact entries — should be exactly maxPerCategory
	lines := strings.Split(result, "\n")
	factCount := 0
	for _, line := range lines {
		if strings.Contains(line, "[fact/myrepo]") {
			factCount++
		}
	}
	if factCount != maxPerCategory {
		t.Errorf("expected %d fact entries, got %d", maxPerCategory, factCount)
	}
}

func TestBuildAgentContext_ScopeFiltering(t *testing.T) {
	store := newTestStore(t)

	insertTuple(t, store, "fact", "repo-a", "fact-a", `{"x":"1"}`, "furniture")
	insertTuple(t, store, "fact", "repo-b", "fact-b", `{"x":"2"}`, "furniture")

	result, err := BuildAgentContext(store, "repo-a", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "fact-a") {
		t.Error("should contain facts from the requested scope")
	}
	if strings.Contains(result, "fact-b") {
		t.Error("should not contain facts from other scopes")
	}
}

func TestBuildAgentContext_EmptyPayload(t *testing.T) {
	store := newTestStore(t)

	insertTuple(t, store, "fact", "myrepo", "simple-fact", `{}`, "furniture")

	result, err := BuildAgentContext(store, "myrepo", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should show identity without trailing colon/payload
	if !strings.Contains(result, "simple-fact") {
		t.Error("should contain fact identity")
	}
	// The line should end with the identity, no ": " payload suffix
	for _, line := range strings.Split(result, "\n") {
		if strings.Contains(line, "simple-fact") {
			if strings.HasSuffix(strings.TrimSpace(line), ":") {
				t.Error("empty payload should not produce trailing colon")
			}
		}
	}
}

func TestPayloadSummary(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantNil bool
	}{
		{"empty object", "{}", "", true},
		{"empty string", "", "", true},
		{"single key", `{"agent":"Sprocket"}`, "agent=Sprocket", false},
		{"multiple keys sorted", `{"z":"last","a":"first"}`, "a=first, z=last", false},
		{"invalid json", "not-json", "", true},
		{"numeric value", `{"count":42}`, "count=42", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := payloadSummary(tt.input)
			if tt.wantNil && got != "" {
				t.Errorf("expected empty string, got %q", got)
			}
			if !tt.wantNil && got != tt.want {
				t.Errorf("expected %q, got %q", tt.want, got)
			}
		})
	}
}
