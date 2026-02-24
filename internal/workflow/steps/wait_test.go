package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chazu/procyon-park/internal/workflow"
)

// ---------------------------------------------------------------------------
// Mock tuple finder
// ---------------------------------------------------------------------------

type mockTupleFinder struct {
	mu       sync.Mutex
	events   map[string]json.RawMessage // agentName -> event payload
	callCount int
	findErr  error
}

func newMockTupleFinder() *mockTupleFinder {
	return &mockTupleFinder{events: make(map[string]json.RawMessage)}
}

func (m *mockTupleFinder) FindTaskDoneEvent(agentName string) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	if m.findErr != nil {
		return nil, m.findErr
	}
	event, ok := m.events[agentName]
	if !ok {
		return nil, nil
	}
	return event, nil
}

func (m *mockTupleFinder) setEvent(agentName string, payload json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events[agentName] = payload
}

func (m *mockTupleFinder) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

// ---------------------------------------------------------------------------
// Tests: WaitHandler
// ---------------------------------------------------------------------------

func TestWaitHandler_ImmediateEvent(t *testing.T) {
	finder := newMockTupleFinder()
	finder.setEvent("Bramble", json.RawMessage(`{"task":"task-1","agent":"Bramble","branch":"agent/Bramble/task-1"}`))

	handler := &WaitHandler{
		Finder:       finder,
		PollInterval: 10 * time.Millisecond,
	}

	instance := &workflow.Instance{
		ID: "wf-test",
		Context: workflow.WorkflowContext{
			ActiveAgent: "Bramble",
		},
	}

	config := json.RawMessage(`{"timeout":"30s"}`)
	result, err := handler.Execute(context.Background(), instance, 1, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "completed" {
		t.Errorf("expected completed, got %s", result.Status)
	}

	// Verify PreviousOutput was set on instance context.
	if instance.Context.PreviousOutput == nil {
		t.Fatal("expected PreviousOutput to be set")
	}

	var output WaitOutput
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.AgentName != "Bramble" {
		t.Errorf("expected agent=Bramble, got %q", output.AgentName)
	}
}

func TestWaitHandler_PollsUntilEvent(t *testing.T) {
	finder := newMockTupleFinder()

	handler := &WaitHandler{
		Finder:       finder,
		PollInterval: 20 * time.Millisecond,
	}

	instance := &workflow.Instance{
		ID: "wf-test",
		Context: workflow.WorkflowContext{
			ActiveAgent: "Widget",
		},
	}

	// Set event after a short delay.
	go func() {
		time.Sleep(60 * time.Millisecond)
		finder.setEvent("Widget", json.RawMessage(`{"task":"task-2","agent":"Widget"}`))
	}()

	config := json.RawMessage(`{"timeout":"5s"}`)
	result, err := handler.Execute(context.Background(), instance, 1, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "completed" {
		t.Errorf("expected completed, got %s", result.Status)
	}

	// Should have polled more than once.
	if finder.getCallCount() < 2 {
		t.Errorf("expected at least 2 poll calls, got %d", finder.getCallCount())
	}
}

func TestWaitHandler_Timeout(t *testing.T) {
	finder := newMockTupleFinder()
	// No event ever arrives.

	handler := &WaitHandler{
		Finder:       finder,
		PollInterval: 10 * time.Millisecond,
	}

	instance := &workflow.Instance{
		ID: "wf-test",
		Context: workflow.WorkflowContext{
			ActiveAgent: "Ghost",
		},
	}

	config := json.RawMessage(`{"timeout":"50ms"}`)
	result, err := handler.Execute(context.Background(), instance, 1, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "failed" {
		t.Errorf("expected failed, got %s", result.Status)
	}
	if result.Error == "" {
		t.Error("expected error message for timeout")
	}
}

func TestWaitHandler_NoActiveAgent(t *testing.T) {
	handler := &WaitHandler{
		Finder:       newMockTupleFinder(),
		PollInterval: 10 * time.Millisecond,
	}

	instance := &workflow.Instance{
		ID:      "wf-test",
		Context: workflow.WorkflowContext{},
	}

	config := json.RawMessage(`{"timeout":"1s"}`)
	result, err := handler.Execute(context.Background(), instance, 1, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "failed" {
		t.Errorf("expected failed, got %s", result.Status)
	}
}

func TestWaitHandler_ContextCancellation(t *testing.T) {
	finder := newMockTupleFinder()

	handler := &WaitHandler{
		Finder:       finder,
		PollInterval: 10 * time.Millisecond,
	}

	instance := &workflow.Instance{
		ID: "wf-test",
		Context: workflow.WorkflowContext{
			ActiveAgent: "Phantom",
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	config := json.RawMessage(`{"timeout":"10s"}`)
	result, err := handler.Execute(ctx, instance, 1, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "failed" {
		t.Errorf("expected failed on context cancel, got %s", result.Status)
	}
}

func TestWaitHandler_DefaultTimeout(t *testing.T) {
	finder := newMockTupleFinder()
	finder.setEvent("Quick", json.RawMessage(`{"done":true}`))

	handler := &WaitHandler{
		Finder:       finder,
		PollInterval: 10 * time.Millisecond,
	}

	instance := &workflow.Instance{
		ID: "wf-test",
		Context: workflow.WorkflowContext{
			ActiveAgent: "Quick",
		},
	}

	// Empty timeout means DefaultWaitTimeout is used.
	config := json.RawMessage(`{}`)
	result, err := handler.Execute(context.Background(), instance, 1, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "completed" {
		t.Errorf("expected completed, got %s", result.Status)
	}
}

func TestWaitHandler_FindError(t *testing.T) {
	finder := newMockTupleFinder()
	finder.findErr = fmt.Errorf("tuplespace unavailable")

	handler := &WaitHandler{
		Finder:       finder,
		PollInterval: 10 * time.Millisecond,
	}

	instance := &workflow.Instance{
		ID: "wf-test",
		Context: workflow.WorkflowContext{
			ActiveAgent: "Broken",
		},
	}

	config := json.RawMessage(`{"timeout":"1s"}`)
	result, err := handler.Execute(context.Background(), instance, 1, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "failed" {
		t.Errorf("expected failed, got %s", result.Status)
	}
}
