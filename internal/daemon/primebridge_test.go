package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSystemPrime_BasicRender(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})
	store := mustNewStore(t)

	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterPrimeHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"system.prime","params":{"role":"cub","agent_name":"TestBot","repo":"myrepo","task_id":"task-1","branch":"agent/TestBot/task-1","worktree":"/tmp/wt"},"id":1}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// Result should be a JSON string containing rendered instructions.
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var instructions string
	if err := json.Unmarshal(raw, &instructions); err != nil {
		t.Fatalf("unmarshal instructions string: %v (raw: %s)", err, raw)
	}

	if instructions == "" {
		t.Fatal("expected non-empty instructions")
	}

	// Should contain the agent name from the template rendering.
	if !strings.Contains(instructions, "TestBot") {
		t.Errorf("expected instructions to contain agent name 'TestBot', got:\n%s", instructions[:min(len(instructions), 200)])
	}
}

func TestSystemPrime_RoleRequired(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})
	store := mustNewStore(t)

	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterPrimeHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"system.prime","params":{},"id":1}`)

	if resp.Error == nil {
		t.Fatal("expected error for missing role")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected code %d, got %d", ErrCodeInvalidParams, resp.Error.Code)
	}
}

func TestSystemPrime_InvalidParams(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})
	store := mustNewStore(t)

	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterPrimeHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"system.prime","params":"not-an-object","id":1}`)

	if resp.Error == nil {
		t.Fatal("expected error for invalid params")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected code %d, got %d", ErrCodeInvalidParams, resp.Error.Code)
	}
}

func TestSystemPrime_WithTuplestoreContext(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})
	store := mustNewStore(t)

	// Insert a fact tuple so context injection has something to include.
	store.Insert("fact", "myrepo", "test-fact", "", `{"content":"hello from tuplestore"}`, "furniture", nil, nil, nil)

	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterPrimeHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"system.prime","params":{"role":"cub","repo":"myrepo"},"id":1}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var instructions string
	if err := json.Unmarshal(raw, &instructions); err != nil {
		t.Fatalf("unmarshal instructions: %v", err)
	}

	// Should contain tuplespace context from the fact we inserted.
	if !strings.Contains(instructions, "test-fact") {
		t.Errorf("expected instructions to include tuplespace context with 'test-fact'")
	}
}

func TestSystemPrime_RoleOnly(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})
	store := mustNewStore(t)

	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterPrimeHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	// Minimal params — just role, no repo/agent/task.
	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"system.prime","params":{"role":"cub"},"id":1}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var instructions string
	if err := json.Unmarshal(raw, &instructions); err != nil {
		t.Fatalf("unmarshal instructions: %v", err)
	}

	if instructions == "" {
		t.Fatal("expected non-empty instructions with role-only params")
	}
}
