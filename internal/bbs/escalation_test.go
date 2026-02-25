package bbs

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// detectSystemicObstacles tests
// ---------------------------------------------------------------------------

func TestDetectSystemicObstacles_TwoInSameScope(t *testing.T) {
	store := newTestStore(t)

	agent1, agent2 := "Fizz", "Widget"
	store.Insert("obstacle", "my-repo", "blocked-on-X", "local",
		`{"detail":"cannot proceed"}`, "session", nil, &agent1, nil)
	store.Insert("obstacle", "my-repo", "blocked-on-Y", "local",
		`{"detail":"also stuck"}`, "session", nil, &agent2, nil)

	n := detectSystemicObstacles(store, 2)
	if n != 1 {
		t.Errorf("expected 1 systemic obstacle, got %d", n)
	}

	// Verify event was written.
	cat := "event"
	ident := "systemic_obstacle"
	scope := "my-repo"
	evt, err := store.FindOne(&cat, &scope, &ident, nil, nil)
	if err != nil {
		t.Fatalf("find event: %v", err)
	}
	if evt == nil {
		t.Fatal("expected systemic_obstacle event to exist")
	}

	// Verify king notification was written.
	notifCat := "notification"
	kingScope := "king"
	notif, err := store.FindOne(&notifCat, &kingScope, nil, nil, nil)
	if err != nil {
		t.Fatalf("find notification: %v", err)
	}
	if notif == nil {
		t.Fatal("expected king notification to exist")
	}
}

func TestDetectSystemicObstacles_BelowThreshold(t *testing.T) {
	store := newTestStore(t)

	agent1 := "Fizz"
	store.Insert("obstacle", "my-repo", "one-obstacle", "local",
		`{"detail":"just one"}`, "session", nil, &agent1, nil)

	n := detectSystemicObstacles(store, 2)
	if n != 0 {
		t.Errorf("expected 0 below threshold, got %d", n)
	}
}

func TestDetectSystemicObstacles_IdempotentOnRepeat(t *testing.T) {
	store := newTestStore(t)

	agent1, agent2 := "Fizz", "Widget"
	store.Insert("obstacle", "repo", "a", "local", `{}`, "session", nil, &agent1, nil)
	store.Insert("obstacle", "repo", "b", "local", `{}`, "session", nil, &agent2, nil)

	// First run detects.
	n1 := detectSystemicObstacles(store, 2)
	if n1 != 1 {
		t.Errorf("first run: expected 1, got %d", n1)
	}

	// Second run should not re-detect (idempotent).
	n2 := detectSystemicObstacles(store, 2)
	if n2 != 0 {
		t.Errorf("second run: expected 0 (idempotent), got %d", n2)
	}
}

// ---------------------------------------------------------------------------
// detectUnclaimedNeeds tests
// ---------------------------------------------------------------------------

func TestDetectUnclaimedNeeds_EscalatesOldNeeds(t *testing.T) {
	store := newTestStore(t)

	store.Insert("need", "my-repo", "need-more-info", "local",
		`{"detail":"need design doc"}`, "session", nil, nil, nil)

	// Use 0 seconds so the need is immediately "unclaimed".
	n := detectUnclaimedNeeds(store, 0)
	if n != 1 {
		t.Errorf("expected 1 escalated need, got %d", n)
	}

	// Verify king notification.
	cat := "notification"
	scope := "king"
	notifs, _ := store.FindAll(&cat, &scope, nil, nil, nil)
	if len(notifs) != 1 {
		t.Fatalf("expected 1 king notification, got %d", len(notifs))
	}

	payload, _ := notifs[0]["payload"].(string)
	if !containsStr(payload, "unclaimed_need") {
		t.Errorf("expected unclaimed_need in payload, got %s", payload)
	}
}

func TestDetectUnclaimedNeeds_IdempotentOnRepeat(t *testing.T) {
	store := newTestStore(t)

	store.Insert("need", "repo", "need-x", "local", `{}`, "session", nil, nil, nil)

	n1 := detectUnclaimedNeeds(store, 0)
	if n1 != 1 {
		t.Errorf("first: expected 1, got %d", n1)
	}

	n2 := detectUnclaimedNeeds(store, 0)
	if n2 != 0 {
		t.Errorf("second: expected 0 (idempotent), got %d", n2)
	}
}

// ---------------------------------------------------------------------------
// detectRepeatedFailures tests
// ---------------------------------------------------------------------------

func TestDetectRepeatedFailures_ThreeFailures(t *testing.T) {
	store := newTestStore(t)

	for i := 0; i < 3; i++ {
		p, _ := json.Marshal(map[string]interface{}{"task": "task-" + string(rune('a'+i))})
		store.Insert("event", "my-repo", "task_failed", "local", string(p), "session", nil, nil, nil)
	}

	n := detectRepeatedFailures(store, 3)
	if n != 1 {
		t.Errorf("expected 1 warning, got %d", n)
	}

	// Verify furniture fact.
	cat := "fact"
	scope := "my-repo"
	facts, _ := store.FindAll(&cat, &scope, nil, nil, nil)
	found := false
	for _, f := range facts {
		ident, _ := f["identity"].(string)
		if containsStr(ident, "repo_health_warning") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected repo_health_warning fact to exist")
	}
}

func TestDetectRepeatedFailures_BelowThreshold(t *testing.T) {
	store := newTestStore(t)

	p, _ := json.Marshal(map[string]interface{}{"task": "task-a"})
	store.Insert("event", "repo", "task_failed", "local", string(p), "session", nil, nil, nil)

	n := detectRepeatedFailures(store, 3)
	if n != 0 {
		t.Errorf("expected 0 below threshold, got %d", n)
	}
}

func TestDetectRepeatedFailures_Idempotent(t *testing.T) {
	store := newTestStore(t)

	for i := 0; i < 3; i++ {
		p, _ := json.Marshal(map[string]interface{}{"task": "t"})
		store.Insert("event", "repo", "task_failed", "local", string(p), "session", nil, nil, nil)
	}

	n1 := detectRepeatedFailures(store, 3)
	n2 := detectRepeatedFailures(store, 3)
	if n1 != 1 {
		t.Errorf("first: expected 1, got %d", n1)
	}
	if n2 != 0 {
		t.Errorf("second: expected 0, got %d", n2)
	}
}

// ---------------------------------------------------------------------------
// detectWorkflowWarnings tests
// ---------------------------------------------------------------------------

func TestDetectWorkflowWarnings_HighMatchRatio(t *testing.T) {
	store := newTestStore(t)

	// Add failed workflow signature.
	fp, _ := json.Marshal(map[string]interface{}{"task": "t1", "workflow": "deploy-v2"})
	store.Insert("event", "my-repo", "task_failed", "local", string(fp), "session", nil, nil, nil)

	// Add 2 active claims with the same workflow.
	for _, name := range []string{"claim-a", "claim-b"} {
		cp, _ := json.Marshal(map[string]interface{}{"status": "in_progress", "workflow": "deploy-v2"})
		store.Insert("claim", "my-repo", name, "local", string(cp), "session", nil, nil, nil)
	}

	n := detectWorkflowWarnings(store, 0.6)
	if n != 1 {
		t.Errorf("expected 1 warning, got %d", n)
	}

	// Verify event.
	cat := "event"
	scope := "my-repo"
	ident := "early_warning:my-repo"
	evt, _ := store.FindOne(&cat, &scope, &ident, nil, nil)
	if evt == nil {
		t.Fatal("expected early_warning event")
	}
}

func TestDetectWorkflowWarnings_LowMatchRatio(t *testing.T) {
	store := newTestStore(t)

	// Add failed workflow signature.
	fp, _ := json.Marshal(map[string]interface{}{"task": "t1", "workflow": "deploy-v2"})
	store.Insert("event", "my-repo", "task_failed", "local", string(fp), "session", nil, nil, nil)

	// 1 matching, 2 non-matching (33% < 60% threshold).
	cp1, _ := json.Marshal(map[string]interface{}{"status": "in_progress", "workflow": "deploy-v2"})
	store.Insert("claim", "my-repo", "a", "local", string(cp1), "session", nil, nil, nil)
	cp2, _ := json.Marshal(map[string]interface{}{"status": "in_progress", "workflow": "build-v1"})
	store.Insert("claim", "my-repo", "b", "local", string(cp2), "session", nil, nil, nil)
	cp3, _ := json.Marshal(map[string]interface{}{"status": "in_progress", "workflow": "test-v1"})
	store.Insert("claim", "my-repo", "c", "local", string(cp3), "session", nil, nil, nil)

	n := detectWorkflowWarnings(store, 0.6)
	if n != 0 {
		t.Errorf("expected 0 (below threshold), got %d", n)
	}
}

func TestDetectWorkflowWarnings_NoFailures(t *testing.T) {
	store := newTestStore(t)

	cp, _ := json.Marshal(map[string]interface{}{"status": "in_progress"})
	store.Insert("claim", "repo", "a", "local", string(cp), "session", nil, nil, nil)

	n := detectWorkflowWarnings(store, 0.6)
	if n != 0 {
		t.Errorf("expected 0 (no failures), got %d", n)
	}
}

// ---------------------------------------------------------------------------
// RunEscalationDetection integration test
// ---------------------------------------------------------------------------

func TestRunEscalationDetection_NoOp(t *testing.T) {
	store := newTestStore(t)
	cfg := EscalationConfig{}

	result := RunEscalationDetection(store, cfg)
	if result.SystemicObstacles != 0 || result.UnclaimedNeeds != 0 ||
		result.RepoHealthWarnings != 0 || result.WorkflowWarnings != 0 {
		t.Errorf("expected all zeros for empty store, got %+v", result)
	}
}

func TestRunEscalationDetection_Combined(t *testing.T) {
	store := newTestStore(t)

	// Set up systemic obstacles.
	a1, a2 := "A", "B"
	store.Insert("obstacle", "repo", "ob-1", "local", `{}`, "session", nil, &a1, nil)
	store.Insert("obstacle", "repo", "ob-2", "local", `{}`, "session", nil, &a2, nil)

	// Set up unclaimed need.
	store.Insert("need", "repo", "need-1", "local", `{}`, "session", nil, nil, nil)

	// Set up repeated failures.
	for i := 0; i < 3; i++ {
		fp, _ := json.Marshal(map[string]interface{}{"task": "t"})
		store.Insert("event", "repo", "task_failed", "local", string(fp), "session", nil, nil, nil)
	}

	cfg := EscalationConfig{
		ObstacleThreshold: 2,
		UnclaimedNeedAge:  -1, // -1 means "check immediately" (0 seconds)
		FailureThreshold:  3,
	}

	result := RunEscalationDetection(store, cfg)
	if result.SystemicObstacles != 1 {
		t.Errorf("expected 1 systemic obstacle, got %d", result.SystemicObstacles)
	}
	if result.UnclaimedNeeds != 1 {
		t.Errorf("expected 1 unclaimed need, got %d", result.UnclaimedNeeds)
	}
	if result.RepoHealthWarnings != 1 {
		t.Errorf("expected 1 health warning, got %d", result.RepoHealthWarnings)
	}
}
