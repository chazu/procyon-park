package daemon

import (
	"encoding/json"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// agent.show tests
// ---------------------------------------------------------------------------

func TestAgentShowBasic(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAgentHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	// Insert an agent registration tuple.
	payload := `{"role":"cub","status":"active","tmuxSession":"pp-myrepo-Sprocket","worktree":"/tmp/wt","branch":"agent/Sprocket/task-1","task":"task-1"}`
	if _, err := store.Insert("agent", "myrepo", "Sprocket", "", payload, "session", nil, nil, nil); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"agent.show","params":{"agent_name":"Sprocket","repo_name":"myrepo"},"id":1}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}

	if result["name"] != "Sprocket" {
		t.Fatalf("expected name Sprocket, got %v", result["name"])
	}
	if result["role"] != "cub" {
		t.Fatalf("expected role cub, got %v", result["role"])
	}
	if result["branch"] != "agent/Sprocket/task-1" {
		t.Fatalf("expected branch agent/Sprocket/task-1, got %v", result["branch"])
	}
	if result["task"] != "task-1" {
		t.Fatalf("expected task task-1, got %v", result["task"])
	}
}

func TestAgentShowNotFound(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAgentHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"agent.show","params":{"agent_name":"Ghost","repo_name":"myrepo"},"id":1}`)

	if resp.Error == nil {
		t.Fatal("expected error for non-existent agent")
	}
}

func TestAgentShowMissingParams(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAgentHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"agent.show","params":{"agent_name":"","repo_name":""},"id":1}`)

	if resp.Error == nil {
		t.Fatal("expected error for missing params")
	}
}

// ---------------------------------------------------------------------------
// agent.stuck tests
// ---------------------------------------------------------------------------

func TestAgentStuckBasic(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAgentHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	// Insert an agent registration tuple with "active" status.
	payload := `{"role":"cub","status":"active","tmuxSession":"pp-myrepo-Widget","branch":"agent/Widget/task-2","task":"task-2"}`
	store.Insert("agent", "myrepo", "Widget", "", payload, "session", nil, nil, nil)

	// Mark as stuck.
	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"agent.stuck","params":{"agent_name":"Widget","repo_name":"myrepo"},"id":1}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}
	if result["status"] != "stuck" {
		t.Fatalf("expected status stuck, got %v", result["status"])
	}

	// Verify the tuple was updated in the store.
	cat := "agent"
	scope := "myrepo"
	identity := "Widget"
	tuples, err := store.FindAll(&cat, &scope, &identity, nil, nil)
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(tuples) != 1 {
		t.Fatalf("expected 1 tuple, got %d", len(tuples))
	}

	storedPayload, _ := tuples[0]["payload"].(string)
	var payloadMap map[string]interface{}
	if err := json.Unmarshal([]byte(storedPayload), &payloadMap); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payloadMap["status"] != "stuck" {
		t.Fatalf("expected stored status stuck, got %v", payloadMap["status"])
	}
	// Other fields should be preserved.
	if payloadMap["role"] != "cub" {
		t.Fatalf("expected role cub preserved, got %v", payloadMap["role"])
	}
}

func TestAgentStuckNotFound(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAgentHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"agent.stuck","params":{"agent_name":"Ghost","repo_name":"myrepo"},"id":1}`)

	if resp.Error == nil {
		t.Fatal("expected error for non-existent agent")
	}
}

func TestAgentStuckMissingParams(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAgentHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"agent.stuck","params":{"agent_name":"","repo_name":""},"id":1}`)

	if resp.Error == nil {
		t.Fatal("expected error for missing params")
	}
}
