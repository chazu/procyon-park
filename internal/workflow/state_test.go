package workflow

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewMemoryStore()
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func makeInstance(id, repo, name string, status InstanceStatus) *Instance {
	now := time.Now().UTC().Truncate(time.Second)
	return &Instance{
		ID:           id,
		RepoName:     repo,
		WorkflowName: name,
		Status:       status,
		CurrentStep:  0,
		Context:      json.RawMessage(`{"key":"value"}`),
		Params:       json.RawMessage(`{"p1":"v1"}`),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func TestGenerateID(t *testing.T) {
	id, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID: %v", err)
	}
	if !strings.HasPrefix(id, "wf-") {
		t.Errorf("expected prefix wf-, got %s", id)
	}
	if len(id) != 19 { // "wf-" + 16 hex chars
		t.Errorf("expected length 19, got %d: %s", len(id), id)
	}

	// Uniqueness.
	id2, _ := GenerateID()
	if id == id2 {
		t.Error("two generated IDs should differ")
	}
}

func TestCRUD(t *testing.T) {
	s := mustStore(t)

	inst := makeInstance("wf-abc123", "myrepo", "deploy", StatusPending)

	// Create.
	if err := s.CreateInstance(inst); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Get.
	got, err := s.GetInstance("myrepo", "wf-abc123")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if got == nil {
		t.Fatal("GetInstance returned nil")
	}
	if got.WorkflowName != "deploy" {
		t.Errorf("workflow_name = %s, want deploy", got.WorkflowName)
	}
	if got.Status != StatusPending {
		t.Errorf("status = %s, want pending", got.Status)
	}
	if string(got.Context) != `{"key":"value"}` {
		t.Errorf("context = %s", got.Context)
	}

	// Update.
	inst.Status = StatusRunning
	inst.CurrentStep = 2
	inst.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	if err := s.UpdateInstance(inst); err != nil {
		t.Fatalf("UpdateInstance: %v", err)
	}

	got, _ = s.GetInstance("myrepo", "wf-abc123")
	if got.Status != StatusRunning {
		t.Errorf("after update: status = %s, want running", got.Status)
	}
	if got.CurrentStep != 2 {
		t.Errorf("after update: current_step = %d, want 2", got.CurrentStep)
	}

	// Get not found.
	got, err = s.GetInstance("myrepo", "wf-nonexistent")
	if err != nil {
		t.Fatalf("GetInstance nonexistent: %v", err)
	}
	if got != nil {
		t.Error("expected nil for nonexistent instance")
	}

	// Delete.
	if err := s.DeleteInstance("myrepo", "wf-abc123"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}
	got, _ = s.GetInstance("myrepo", "wf-abc123")
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestUpdateNotFound(t *testing.T) {
	s := mustStore(t)
	inst := makeInstance("wf-missing", "myrepo", "deploy", StatusRunning)
	err := s.UpdateInstance(inst)
	if err == nil {
		t.Fatal("expected error updating nonexistent instance")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want 'not found'", err)
	}
}

func TestDeleteNotFound(t *testing.T) {
	s := mustStore(t)
	err := s.DeleteInstance("myrepo", "wf-missing")
	if err == nil {
		t.Fatal("expected error deleting nonexistent instance")
	}
}

func TestDuplicateCreate(t *testing.T) {
	s := mustStore(t)
	inst := makeInstance("wf-dup", "myrepo", "deploy", StatusPending)
	if err := s.CreateInstance(inst); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := s.CreateInstance(inst)
	if err == nil {
		t.Fatal("expected error on duplicate create")
	}
}

func TestStatusIndexQueries(t *testing.T) {
	s := mustStore(t)

	// Create instances with different statuses and repos.
	for i, tc := range []struct {
		id     string
		repo   string
		status InstanceStatus
	}{
		{"wf-001", "repo-a", StatusPending},
		{"wf-002", "repo-a", StatusRunning},
		{"wf-003", "repo-a", StatusRunning},
		{"wf-004", "repo-b", StatusRunning},
		{"wf-005", "repo-b", StatusCompleted},
	} {
		inst := makeInstance(tc.id, tc.repo, "wf", tc.status)
		inst.CreatedAt = inst.CreatedAt.Add(time.Duration(i) * time.Second)
		inst.UpdatedAt = inst.CreatedAt
		if err := s.CreateInstance(inst); err != nil {
			t.Fatalf("create %s: %v", tc.id, err)
		}
	}

	// Filter by status only.
	running, err := s.ListInstances(StatusRunning, "")
	if err != nil {
		t.Fatalf("ListInstances running: %v", err)
	}
	if len(running) != 3 {
		t.Errorf("running count = %d, want 3", len(running))
	}

	// Filter by status and repo.
	runningA, err := s.ListInstances(StatusRunning, "repo-a")
	if err != nil {
		t.Fatalf("ListInstances running/repo-a: %v", err)
	}
	if len(runningA) != 2 {
		t.Errorf("running/repo-a count = %d, want 2", len(runningA))
	}

	// Filter by repo only.
	allB, err := s.ListInstances("", "repo-b")
	if err != nil {
		t.Fatalf("ListInstances repo-b: %v", err)
	}
	if len(allB) != 2 {
		t.Errorf("repo-b count = %d, want 2", len(allB))
	}

	// No filter.
	all, err := s.ListInstances("", "")
	if err != nil {
		t.Fatalf("ListInstances all: %v", err)
	}
	if len(all) != 5 {
		t.Errorf("all count = %d, want 5", len(all))
	}

	// Status index is updated on UpdateInstance.
	inst := makeInstance("wf-002", "repo-a", "wf", StatusCompleted)
	inst.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	if err := s.UpdateInstance(inst); err != nil {
		t.Fatalf("UpdateInstance: %v", err)
	}
	running2, _ := s.ListInstances(StatusRunning, "")
	if len(running2) != 2 {
		t.Errorf("after update: running count = %d, want 2", len(running2))
	}
	completed, _ := s.ListInstances(StatusCompleted, "")
	if len(completed) != 2 {
		t.Errorf("after update: completed count = %d, want 2", len(completed))
	}
}

func TestGateStatePersistence(t *testing.T) {
	s := mustStore(t)

	gs := &GateState{
		InstanceID: "wf-gate1",
		StepIndex:  3,
		StartedAt:  time.Now().UTC().Truncate(time.Second),
		State:      "waiting",
		PromptSent: false,
	}

	// Save.
	if err := s.SaveGateState(gs); err != nil {
		t.Fatalf("SaveGateState: %v", err)
	}

	// Get.
	got, err := s.GetGateState("wf-gate1", 3)
	if err != nil {
		t.Fatalf("GetGateState: %v", err)
	}
	if got == nil {
		t.Fatal("GetGateState returned nil")
	}
	if got.State != "waiting" {
		t.Errorf("state = %s, want waiting", got.State)
	}
	if got.PromptSent {
		t.Error("prompt_sent should be false")
	}
	if !got.StartedAt.Equal(gs.StartedAt) {
		t.Errorf("started_at = %v, want %v", got.StartedAt, gs.StartedAt)
	}

	// Update via SaveGateState (INSERT OR REPLACE).
	gs.State = "approved"
	gs.PromptSent = true
	if err := s.SaveGateState(gs); err != nil {
		t.Fatalf("SaveGateState update: %v", err)
	}
	got, _ = s.GetGateState("wf-gate1", 3)
	if got.State != "approved" {
		t.Errorf("after update: state = %s, want approved", got.State)
	}
	if !got.PromptSent {
		t.Error("after update: prompt_sent should be true")
	}

	// Get not found.
	got, err = s.GetGateState("wf-nonexistent", 0)
	if err != nil {
		t.Fatalf("GetGateState nonexistent: %v", err)
	}
	if got != nil {
		t.Error("expected nil for nonexistent gate state")
	}

	// Delete.
	if err := s.DeleteGateState("wf-gate1", 3); err != nil {
		t.Fatalf("DeleteGateState: %v", err)
	}
	got, _ = s.GetGateState("wf-gate1", 3)
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestConcurrentUpdates(t *testing.T) {
	s := mustStore(t)

	inst := makeInstance("wf-conc", "myrepo", "deploy", StatusPending)
	if err := s.CreateInstance(inst); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(step int) {
			defer wg.Done()
			upd := makeInstance("wf-conc", "myrepo", "deploy", StatusRunning)
			upd.CurrentStep = step
			upd.UpdatedAt = time.Now().UTC().Truncate(time.Second)
			if err := s.UpdateInstance(upd); err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent update error: %v", err)
	}

	// Verify instance is still retrievable and consistent.
	got, err := s.GetInstance("myrepo", "wf-conc")
	if err != nil {
		t.Fatalf("GetInstance after concurrent: %v", err)
	}
	if got == nil {
		t.Fatal("instance disappeared after concurrent updates")
	}
	if got.Status != StatusRunning {
		t.Errorf("status = %s, want running", got.Status)
	}

	// Status index should have exactly one entry.
	running, _ := s.ListInstances(StatusRunning, "myrepo")
	if len(running) != 1 {
		t.Errorf("running instances = %d, want 1", len(running))
	}
}
