package bbs

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// collectExpiredEphemeral tests
// ---------------------------------------------------------------------------

func TestCollectExpiredEphemeral_NoExpired(t *testing.T) {
	store := newTestStore(t)

	// Insert a non-expired ephemeral tuple (TTL 3600s).
	store.Insert("notification", "agent1", "ping", "local", `{}`, "ephemeral", nil, nil, intPtr(3600))

	n := collectExpiredEphemeral(store)
	if n != 0 {
		t.Errorf("expected 0 expired, got %d", n)
	}
}

func TestCollectExpiredEphemeral_DeletesExpired(t *testing.T) {
	store := newTestStore(t)

	// Insert an ephemeral tuple with 0s TTL (immediately expired).
	store.Insert("notification", "agent1", "ping", "local", `{}`, "ephemeral", nil, nil, intPtr(0))

	// Wait briefly for the datetime comparison to work.
	time.Sleep(10 * time.Millisecond)

	n := collectExpiredEphemeral(store)
	if n != 1 {
		t.Errorf("expected 1 expired, got %d", n)
	}

	// Verify it's actually gone.
	cat := "notification"
	remaining, _ := store.FindAll(&cat, nil, nil, nil, nil)
	if len(remaining) != 0 {
		t.Errorf("expected 0 remaining, got %d", len(remaining))
	}
}

func TestCollectExpiredEphemeral_SkipsNonEphemeral(t *testing.T) {
	store := newTestStore(t)

	// Session and furniture tuples should not be expired even with TTL.
	store.Insert("fact", "repo", "a", "local", `{}`, "furniture", nil, nil, nil)
	store.Insert("claim", "repo", "b", "local", `{}`, "session", nil, nil, nil)

	n := collectExpiredEphemeral(store)
	if n != 0 {
		t.Errorf("expected 0 expired (non-ephemeral), got %d", n)
	}
}

// ---------------------------------------------------------------------------
// cleanupStaleClaims tests
// ---------------------------------------------------------------------------

func TestCleanupStaleClaims_RemovesStaleWithTaskDone(t *testing.T) {
	store := newTestStore(t)

	// Insert a claim with TTL 0 (immediately stale).
	store.Insert("claim", "repo", "task-1", "local",
		`{"agent":"Fizz","status":"in_progress"}`, "session", nil, nil, nil)

	// Insert a task_done event for this task.
	payload, _ := json.Marshal(map[string]interface{}{"task": "task-1", "agent": "Fizz"})
	store.Insert("event", "repo", "task_done", "local", string(payload), "session", nil, nil, nil)

	cfg := GCConfig{
		StaleClaimAge:     0,    // 0 = immediately stale
		AbandonedClaimAge: 999 * time.Hour, // Very long, won't trigger
	}
	gcDefaults(&cfg)
	cfg.StaleClaimAge = 0
	cfg.AbandonedClaimAge = 999 * time.Hour

	stale, abandoned := cleanupStaleClaims(store, cfg)
	if stale != 1 {
		t.Errorf("expected 1 stale, got %d", stale)
	}
	if abandoned != 0 {
		t.Errorf("expected 0 abandoned, got %d", abandoned)
	}
}

func TestCleanupStaleClaims_KeepsRecentClaims(t *testing.T) {
	store := newTestStore(t)

	// Insert a recent claim.
	store.Insert("claim", "repo", "task-2", "local",
		`{"agent":"Widget","status":"in_progress"}`, "session", nil, nil, nil)

	cfg := GCConfig{
		StaleClaimAge:     1 * time.Hour,
		AbandonedClaimAge: 2 * time.Hour,
	}

	stale, abandoned := cleanupStaleClaims(store, cfg)
	if stale != 0 {
		t.Errorf("expected 0 stale, got %d", stale)
	}
	if abandoned != 0 {
		t.Errorf("expected 0 abandoned, got %d", abandoned)
	}
}

// ---------------------------------------------------------------------------
// promoteConventions tests
// ---------------------------------------------------------------------------

func TestPromoteConventions_PromotesAtQuorum(t *testing.T) {
	store := newTestStore(t)

	// Two agents propose the same convention.
	agent1, agent2 := "Fizz", "Widget"
	store.Insert("conventionProposal", "repo", "use-snakecase", "local",
		`{"content":"use snake_case for variables"}`, "session", nil, &agent1, nil)
	store.Insert("conventionProposal", "repo", "use-snakecase", "local",
		`{"content":"use snake_case for variables"}`, "session", nil, &agent2, nil)

	promoted := promoteConventions(store, 2)
	if promoted != 1 {
		t.Errorf("expected 1 promoted, got %d", promoted)
	}

	// Verify the convention was written.
	cat := "convention"
	convs, _ := store.FindAll(&cat, nil, nil, nil, nil)
	if len(convs) != 1 {
		t.Fatalf("expected 1 convention, got %d", len(convs))
	}
	if convs[0]["identity"] != "use-snakecase" {
		t.Errorf("expected identity=use-snakecase, got %v", convs[0]["identity"])
	}

	// Verify proposals were cleaned up.
	propCat := "conventionProposal"
	remaining, _ := store.FindAll(&propCat, nil, nil, nil, nil)
	if len(remaining) != 0 {
		t.Errorf("expected 0 remaining proposals, got %d", len(remaining))
	}
}

func TestPromoteConventions_BelowQuorum(t *testing.T) {
	store := newTestStore(t)

	// Only one agent proposes.
	agent1 := "Fizz"
	store.Insert("conventionProposal", "repo", "one-proposal", "local",
		`{"content":"only one agent"}`, "session", nil, &agent1, nil)

	promoted := promoteConventions(store, 2)
	if promoted != 0 {
		t.Errorf("expected 0 promoted (below quorum), got %d", promoted)
	}
}

// ---------------------------------------------------------------------------
// archiveCompletedTasks tests
// ---------------------------------------------------------------------------

func TestArchiveCompletedTasks_SkipsWithoutWarmDir(t *testing.T) {
	store := newTestStore(t)
	cfg := GCConfig{WarmBaseDir: ""}

	archived, synthesized := archiveCompletedTasks(context.Background(), store, cfg)
	if archived != 0 || synthesized != 0 {
		t.Errorf("expected 0/0 without WarmBaseDir, got %d/%d", archived, synthesized)
	}
}

func TestArchiveCompletedTasks_SkipsPendingDismiss(t *testing.T) {
	store := newTestStore(t)

	// Agent has a dismiss_request pending.
	dismissPayload, _ := json.Marshal(map[string]interface{}{"agent": "Fizz"})
	store.Insert("event", "repo", "dismiss_request", "local",
		string(dismissPayload), "session", nil, nil, nil)

	// Also has a task_done.
	donePayload, _ := json.Marshal(map[string]interface{}{"task": "task-1", "agent": "Fizz"})
	store.Insert("event", "repo", "task_done", "local",
		string(donePayload), "session", nil, nil, nil)

	// Insert some session tuples for this task.
	task := "task-1"
	store.Insert("claim", "repo", "task-1", "local",
		`{"agent":"Fizz"}`, "session", &task, nil, nil)

	cfg := GCConfig{WarmBaseDir: t.TempDir()}

	archived, _ := archiveCompletedTasks(context.Background(), store, cfg)
	if archived != 0 {
		t.Errorf("expected 0 archived (pending dismiss), got %d", archived)
	}
}

// ---------------------------------------------------------------------------
// buildDoneTaskSet tests
// ---------------------------------------------------------------------------

func TestBuildDoneTaskSet(t *testing.T) {
	store := newTestStore(t)

	p1, _ := json.Marshal(map[string]interface{}{"task": "task-1"})
	p2, _ := json.Marshal(map[string]interface{}{"task": "task-2"})
	store.Insert("event", "repo", "task_done", "local", string(p1), "session", nil, nil, nil)
	store.Insert("event", "repo", "task_done", "local", string(p2), "session", nil, nil, nil)

	set := buildDoneTaskSet(store)
	if !set["task-1"] {
		t.Error("expected task-1 in done set")
	}
	if !set["task-2"] {
		t.Error("expected task-2 in done set")
	}
	if set["task-3"] {
		t.Error("task-3 should not be in done set")
	}
}

// ---------------------------------------------------------------------------
// buildPendingDismissSet tests
// ---------------------------------------------------------------------------

func TestBuildPendingDismissSet(t *testing.T) {
	store := newTestStore(t)

	p, _ := json.Marshal(map[string]interface{}{"agent": "Fizz"})
	store.Insert("event", "repo", "dismiss_request", "local", string(p), "session", nil, nil, nil)

	set := buildPendingDismissSet(store)
	if !set["Fizz"] {
		t.Error("expected Fizz in pending dismiss set")
	}
	if set["Widget"] {
		t.Error("Widget should not be in pending dismiss set")
	}
}

// ---------------------------------------------------------------------------
// RunGCCycle integration test
// ---------------------------------------------------------------------------

func TestRunGCCycle_NoOp(t *testing.T) {
	store := newTestStore(t)
	cfg := GCConfig{
		Interval:  1 * time.Second,
		WarmBaseDir: t.TempDir(),
	}

	result := RunGCCycle(context.Background(), store, cfg)
	if result.ExpiredEphemeral != 0 || result.StaleClaims != 0 ||
		result.AbandonedClaims != 0 || result.PromotedConventions != 0 ||
		result.ArchivedTasks != 0 || result.SynthesizedTuples != 0 {
		t.Errorf("expected all zeros for empty store, got %+v", result)
	}
}

func TestRunGCCycle_Combined(t *testing.T) {
	store := newTestStore(t)

	// Set up expired ephemeral.
	store.Insert("notification", "agent1", "old", "local", `{}`, "ephemeral", nil, nil, intPtr(0))
	time.Sleep(10 * time.Millisecond)

	// Set up convention proposals at quorum.
	agent1, agent2 := "Fizz", "Widget"
	store.Insert("conventionProposal", "repo", "test-conv", "local",
		`{"content":"test"}`, "session", nil, &agent1, nil)
	store.Insert("conventionProposal", "repo", "test-conv", "local",
		`{"content":"test"}`, "session", nil, &agent2, nil)

	cfg := GCConfig{
		WarmBaseDir:       t.TempDir(),
		StaleClaimAge:     1 * time.Hour,
		AbandonedClaimAge: 2 * time.Hour,
		ConventionQuorum:  2,
	}

	result := RunGCCycle(context.Background(), store, cfg)
	if result.ExpiredEphemeral != 1 {
		t.Errorf("expected 1 expired, got %d", result.ExpiredEphemeral)
	}
	if result.PromotedConventions != 1 {
		t.Errorf("expected 1 promoted, got %d", result.PromotedConventions)
	}
}

func intPtr(n int) *int { return &n }
