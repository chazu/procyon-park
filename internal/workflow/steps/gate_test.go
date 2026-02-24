package steps

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chazu/procyon-park/internal/tuplestore"
	"github.com/chazu/procyon-park/internal/workflow"
)

func mustGateTestDeps(t *testing.T) (*workflow.Store, *tuplestore.TupleStore) {
	t.Helper()
	ws, err := workflow.NewMemoryStore()
	if err != nil {
		t.Fatalf("NewMemoryStore (workflow): %v", err)
	}
	t.Cleanup(func() { ws.Close() })

	ts, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatalf("NewMemoryStore (tuplestore): %v", err)
	}
	t.Cleanup(func() { ts.Close() })

	return ws, ts
}

func makeGateInstance() *workflow.Instance {
	return &workflow.Instance{
		ID:           "wf-gate-test",
		WorkflowName: "test-wf",
		RepoName:     "test-repo",
		Status:       workflow.StatusRunning,
		CurrentStep:  0,
		Params:       map[string]string{},
		Context:      workflow.WorkflowContext{},
		StepResults:  []workflow.StepResult{},
		StartedAt:    time.Now().UTC(),
	}
}

func TestHumanGate_Approve(t *testing.T) {
	ws, ts := mustGateTestDeps(t)
	h := &GateHandler{Store: ws, Tuples: ts, PollInterval: 10 * time.Millisecond}

	instance := makeGateInstance()
	config := json.RawMessage(`{"gateType":"human","approvers":["alice"],"prompt":"Deploy to prod?","timeout":"5s"}`)

	// Run the gate in a goroutine since it polls.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan *workflow.StepResult, 1)
	go func() {
		result, err := h.Execute(ctx, instance, 0, config)
		if err != nil {
			t.Errorf("Execute: %v", err)
			return
		}
		done <- result
	}()

	// Wait a bit for the gate to write requests and start polling.
	time.Sleep(50 * time.Millisecond)

	// Verify gate_request tuple was written.
	cat := "gate_request"
	scope := "test-repo"
	requests, err := ts.FindAll(&cat, &scope, nil, nil, nil)
	if err != nil {
		t.Fatalf("FindAll gate_request: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("gate_request count = %d, want 1", len(requests))
	}

	// Write an approval response.
	respPayload, _ := json.Marshal(map[string]string{
		"decision": "approved",
		"approver": "alice",
	})
	identity := "wf-gate-test-0"
	_, err = ts.Insert("gate_response", "test-repo", identity, "local", string(respPayload), "session", nil, nil, nil)
	if err != nil {
		t.Fatalf("Insert gate_response: %v", err)
	}

	select {
	case result := <-done:
		if result.Status != "completed" {
			t.Errorf("status = %s, want completed; error = %s", result.Status, result.Error)
		}
	case <-ctx.Done():
		t.Fatal("gate did not complete in time")
	}
}

func TestHumanGate_Reject(t *testing.T) {
	ws, ts := mustGateTestDeps(t)
	h := &GateHandler{Store: ws, Tuples: ts, PollInterval: 10 * time.Millisecond}

	instance := makeGateInstance()
	config := json.RawMessage(`{"gateType":"human","approvers":["bob"],"timeout":"5s"}`)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan *workflow.StepResult, 1)
	go func() {
		result, err := h.Execute(ctx, instance, 0, config)
		if err != nil {
			t.Errorf("Execute: %v", err)
			return
		}
		done <- result
	}()

	time.Sleep(50 * time.Millisecond)

	// Write a rejection response.
	respPayload, _ := json.Marshal(map[string]string{
		"decision": "rejected",
		"approver": "bob",
		"reason":   "not ready",
	})
	identity := "wf-gate-test-0"
	ts.Insert("gate_response", "test-repo", identity, "local", string(respPayload), "session", nil, nil, nil)

	select {
	case result := <-done:
		if result.Status != "failed" {
			t.Errorf("status = %s, want failed", result.Status)
		}
		if !strings.Contains(result.Error, "rejected") {
			t.Errorf("error = %q, want 'rejected'", result.Error)
		}
	case <-ctx.Done():
		t.Fatal("gate did not complete in time")
	}
}

func TestHumanGate_Timeout(t *testing.T) {
	ws, ts := mustGateTestDeps(t)
	h := &GateHandler{Store: ws, Tuples: ts, PollInterval: 10 * time.Millisecond}

	instance := makeGateInstance()
	// Very short timeout to trigger quickly.
	config := json.RawMessage(`{"gateType":"human","approvers":["alice"],"timeout":"50ms"}`)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := h.Execute(ctx, instance, 0, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("status = %s, want failed", result.Status)
	}
	if !strings.Contains(result.Error, "timed out") {
		t.Errorf("error = %q, want 'timed out'", result.Error)
	}

	// Verify gate state was persisted as timed_out.
	gs, err := ws.GetGateState("wf-gate-test", 0)
	if err != nil {
		t.Fatalf("GetGateState: %v", err)
	}
	if gs == nil {
		t.Fatal("gate state not persisted")
	}
	if gs.State != "timed_out" {
		t.Errorf("gate state = %s, want timed_out", gs.State)
	}
}

func TestTimerGate_Duration(t *testing.T) {
	ws, ts := mustGateTestDeps(t)
	h := &GateHandler{Store: ws, Tuples: ts}

	instance := makeGateInstance()
	config := json.RawMessage(`{"gateType":"timer","duration":"50ms"}`)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	result, err := h.Execute(ctx, instance, 0, config)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("status = %s, want completed; error = %s", result.Status, result.Error)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("elapsed = %v, expected at least ~50ms", elapsed)
	}
}

func TestTimerGate_CrashRecovery(t *testing.T) {
	ws, ts := mustGateTestDeps(t)
	h := &GateHandler{Store: ws, Tuples: ts}

	instance := makeGateInstance()

	// Pre-persist gate state as if we started 200ms ago with a 100ms duration.
	gs := &workflow.GateState{
		InstanceID: instance.ID,
		StepIndex:  0,
		StartedAt:  time.Now().UTC().Add(-200 * time.Millisecond),
		State:      "waiting",
	}
	if err := ws.SaveGateState(gs); err != nil {
		t.Fatalf("SaveGateState: %v", err)
	}

	config := json.RawMessage(`{"gateType":"timer","duration":"100ms"}`)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	start := time.Now()
	result, err := h.Execute(ctx, instance, 0, config)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("status = %s, want completed; error = %s", result.Status, result.Error)
	}
	// Should complete almost immediately since the duration already elapsed.
	if elapsed > 50*time.Millisecond {
		t.Errorf("elapsed = %v, expected near-instant completion (crash recovery)", elapsed)
	}
}

func TestHumanGate_CrashRecovery(t *testing.T) {
	ws, ts := mustGateTestDeps(t)
	h := &GateHandler{Store: ws, Tuples: ts, PollInterval: 10 * time.Millisecond}

	instance := makeGateInstance()

	// Pre-persist gate state as already approved (simulating recovery after approval).
	gs := &workflow.GateState{
		InstanceID: instance.ID,
		StepIndex:  0,
		StartedAt:  time.Now().UTC().Add(-time.Minute),
		State:      "approved",
		PromptSent: true,
	}
	if err := ws.SaveGateState(gs); err != nil {
		t.Fatalf("SaveGateState: %v", err)
	}

	config := json.RawMessage(`{"gateType":"human","approvers":["alice"],"timeout":"5s"}`)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	result, err := h.Execute(ctx, instance, 0, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("status = %s, want completed; error = %s", result.Status, result.Error)
	}
}

func TestTimerGate_InvalidDuration(t *testing.T) {
	ws, ts := mustGateTestDeps(t)
	h := &GateHandler{Store: ws, Tuples: ts}

	instance := makeGateInstance()
	config := json.RawMessage(`{"gateType":"timer","duration":"invalid"}`)

	result, err := h.Execute(context.Background(), instance, 0, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("status = %s, want failed", result.Status)
	}
	if !strings.Contains(result.Error, "invalid duration") {
		t.Errorf("error = %q, want 'invalid duration'", result.Error)
	}
}

func TestGate_UnknownType(t *testing.T) {
	ws, ts := mustGateTestDeps(t)
	h := &GateHandler{Store: ws, Tuples: ts}

	instance := makeGateInstance()
	config := json.RawMessage(`{"gateType":"unknown"}`)

	result, err := h.Execute(context.Background(), instance, 0, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("status = %s, want failed", result.Status)
	}
	if !strings.Contains(result.Error, "unknown gate type") {
		t.Errorf("error = %q, want 'unknown gate type'", result.Error)
	}
}
