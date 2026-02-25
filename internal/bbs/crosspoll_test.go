package bbs

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// CrossPollinator construction
// ---------------------------------------------------------------------------

func TestNewCrossPollinator_Defaults(t *testing.T) {
	cp := NewCrossPollinator(CrossPollConfig{})
	if cp.cfg.RateLimit != 5*time.Minute {
		t.Errorf("expected 5m rate limit, got %v", cp.cfg.RateLimit)
	}
}

func TestNewCrossPollinator_CustomRateLimit(t *testing.T) {
	cp := NewCrossPollinator(CrossPollConfig{RateLimit: 1 * time.Minute})
	if cp.cfg.RateLimit != 1*time.Minute {
		t.Errorf("expected 1m rate limit, got %v", cp.cfg.RateLimit)
	}
}

// ---------------------------------------------------------------------------
// containsScope tests
// ---------------------------------------------------------------------------

func TestContainsScope_Found(t *testing.T) {
	if !containsScope(`{"scope":"my-repo","task":"do-stuff"}`, "my-repo") {
		t.Error("expected to find my-repo in payload")
	}
}

func TestContainsScope_NotFound(t *testing.T) {
	if containsScope(`{"scope":"other-repo"}`, "my-repo") {
		t.Error("should not find my-repo in payload")
	}
}

func TestContainsScope_EmptyPayload(t *testing.T) {
	if containsScope("", "my-repo") {
		t.Error("should not find scope in empty payload")
	}
}

func TestContainsScope_EmptyScope(t *testing.T) {
	// containsScope("some text", "") would find empty string anywhere.
	// With our loop, len("") == 0 so the loop runs i <= len(payload) which
	// always matches. This is fine since we guard against empty scopes
	// in CheckTuple.
	if !containsScope("abc", "") {
		t.Error("empty scope should match any payload")
	}
}

// ---------------------------------------------------------------------------
// extractAgent tests
// ---------------------------------------------------------------------------

func TestExtractAgent_FromAgentID(t *testing.T) {
	agent := "Fizz"
	tuple := map[string]interface{}{
		"agent_id": &agent,
		"payload":  `{}`,
	}
	got := extractAgent(tuple)
	if got != "Fizz" {
		t.Errorf("expected Fizz, got %q", got)
	}
}

func TestExtractAgent_FromPayload(t *testing.T) {
	tuple := map[string]interface{}{
		"agent_id": (*string)(nil),
		"payload":  `{"agent":"Widget"}`,
	}
	got := extractAgent(tuple)
	if got != "Widget" {
		t.Errorf("expected Widget, got %q", got)
	}
}

func TestExtractAgent_NoAgent(t *testing.T) {
	tuple := map[string]interface{}{
		"agent_id": (*string)(nil),
		"payload":  `{"task":"task-1"}`,
	}
	got := extractAgent(tuple)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Rate limiting tests
// ---------------------------------------------------------------------------

func TestAllowNotification_FirstTime(t *testing.T) {
	cp := NewCrossPollinator(CrossPollConfig{RateLimit: 5 * time.Minute})
	if !cp.allowNotification("Fizz", "other-repo") {
		t.Error("first notification should be allowed")
	}
}

func TestAllowNotification_RateLimited(t *testing.T) {
	cp := NewCrossPollinator(CrossPollConfig{RateLimit: 5 * time.Minute})
	cp.recordNotification("Fizz", "other-repo")

	if cp.allowNotification("Fizz", "other-repo") {
		t.Error("should be rate limited")
	}
}

func TestAllowNotification_DifferentPairs(t *testing.T) {
	cp := NewCrossPollinator(CrossPollConfig{RateLimit: 5 * time.Minute})
	cp.recordNotification("Fizz", "repo-a")

	if !cp.allowNotification("Fizz", "repo-b") {
		t.Error("different target scope should not be rate limited")
	}
	if !cp.allowNotification("Widget", "repo-a") {
		t.Error("different source agent should not be rate limited")
	}
}

func TestAllowNotification_ExpiredRateLimit(t *testing.T) {
	cp := NewCrossPollinator(CrossPollConfig{RateLimit: 1 * time.Millisecond})
	cp.recordNotification("Fizz", "other-repo")

	time.Sleep(5 * time.Millisecond)
	if !cp.allowNotification("Fizz", "other-repo") {
		t.Error("should be allowed after rate limit expires")
	}
}

// ---------------------------------------------------------------------------
// CheckTuple tests
// ---------------------------------------------------------------------------

func TestCheckTuple_WritesNotification(t *testing.T) {
	store := newTestStore(t)
	cp := NewCrossPollinator(CrossPollConfig{RateLimit: 1 * time.Millisecond})

	agent := "Fizz"
	tuple := map[string]interface{}{
		"agent_id": &agent,
		"payload":  `{"message":"see other-repo for details"}`,
		"scope":    "my-repo",
		"identity": "some-fact",
		"category": "fact",
	}

	sent := cp.CheckTuple(store, tuple, []string{"my-repo", "other-repo"})
	if !sent {
		t.Error("expected notification to be sent")
	}

	// Verify notification was written.
	cat := "notification"
	scope := "other-repo"
	notifs, _ := store.FindAll(&cat, &scope, nil, nil, nil)
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}
}

func TestCheckTuple_SkipsSameScope(t *testing.T) {
	store := newTestStore(t)
	cp := NewCrossPollinator(CrossPollConfig{RateLimit: 1 * time.Millisecond})

	agent := "Fizz"
	tuple := map[string]interface{}{
		"agent_id": &agent,
		"payload":  `{"message":"my-repo is great"}`,
		"scope":    "my-repo",
		"identity": "some-fact",
		"category": "fact",
	}

	// Only scope is "my-repo" — should not notify itself.
	sent := cp.CheckTuple(store, tuple, []string{"my-repo"})
	if sent {
		t.Error("should not notify same scope")
	}
}

func TestCheckTuple_NoAgent(t *testing.T) {
	store := newTestStore(t)
	cp := NewCrossPollinator(CrossPollConfig{RateLimit: 1 * time.Millisecond})

	tuple := map[string]interface{}{
		"agent_id": (*string)(nil),
		"payload":  `{"task":"other-repo stuff"}`,
		"scope":    "my-repo",
		"identity": "test",
		"category": "fact",
	}

	sent := cp.CheckTuple(store, tuple, []string{"my-repo", "other-repo"})
	if sent {
		t.Error("should not notify without source agent")
	}
}

func TestCheckTuple_RateLimited(t *testing.T) {
	store := newTestStore(t)
	cp := NewCrossPollinator(CrossPollConfig{RateLimit: 5 * time.Minute})

	agent := "Fizz"
	tuple := map[string]interface{}{
		"agent_id": &agent,
		"payload":  `{"message":"other-repo reference"}`,
		"scope":    "my-repo",
		"identity": "test",
		"category": "fact",
	}

	// First call succeeds.
	sent := cp.CheckTuple(store, tuple, []string{"my-repo", "other-repo"})
	if !sent {
		t.Error("first notification should succeed")
	}

	// Second call is rate limited.
	sent = cp.CheckTuple(store, tuple, []string{"my-repo", "other-repo"})
	if sent {
		t.Error("second notification should be rate limited")
	}
}

// ---------------------------------------------------------------------------
// RunCrossPollination tests
// ---------------------------------------------------------------------------

func TestRunCrossPollination_WithMatches(t *testing.T) {
	store := newTestStore(t)
	cp := NewCrossPollinator(CrossPollConfig{RateLimit: 1 * time.Millisecond})

	// Insert a session tuple that references another scope.
	agent := "Fizz"
	store.Insert("fact", "repo-a", "test-fact", "local",
		`{"message":"see repo-b for details"}`, "session", nil, &agent, nil)

	result := cp.RunCrossPollination(store, []string{"repo-a", "repo-b"})
	if result.Checked < 1 {
		t.Errorf("expected at least 1 checked, got %d", result.Checked)
	}
	if result.Notified != 1 {
		t.Errorf("expected 1 notified, got %d", result.Notified)
	}
}

func TestRunCrossPollination_NoMatches(t *testing.T) {
	store := newTestStore(t)
	cp := NewCrossPollinator(CrossPollConfig{RateLimit: 1 * time.Millisecond})

	// Insert a session tuple that doesn't reference any other scope.
	agent := "Fizz"
	store.Insert("fact", "repo-a", "test-fact", "local",
		`{"message":"nothing interesting"}`, "session", nil, &agent, nil)

	result := cp.RunCrossPollination(store, []string{"repo-a", "repo-b"})
	if result.Notified != 0 {
		t.Errorf("expected 0 notified, got %d", result.Notified)
	}
}

func TestRunCrossPollination_SkipsFurnitureTuples(t *testing.T) {
	store := newTestStore(t)
	cp := NewCrossPollinator(CrossPollConfig{RateLimit: 1 * time.Millisecond})

	// Insert a furniture tuple — should be skipped.
	agent := "Fizz"
	store.Insert("fact", "repo-a", "test-fact", "local",
		`{"message":"see repo-b for details"}`, "furniture", nil, &agent, nil)

	result := cp.RunCrossPollination(store, []string{"repo-a", "repo-b"})
	if result.Checked != 0 {
		t.Errorf("expected 0 checked (furniture skipped), got %d", result.Checked)
	}
}
