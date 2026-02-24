package daemon

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// tuple.write tests
// ---------------------------------------------------------------------------

func TestTupleWriteBasic(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.write","params":{"category":"fact","scope":"myrepo","identity":"test-fact","payload":"{\"key\":\"value\"}"},"id":1}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}
	id, ok := result["id"].(float64)
	if !ok || id < 1 {
		t.Fatalf("expected positive id, got %v", result["id"])
	}
}

func TestTupleWriteAllFields(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.write","params":{"category":"claim","scope":"repo","identity":"task-1","instance":"local","payload":"{\"agent\":\"Doodle\"}","lifecycle":"ephemeral","task_id":"task-1","agent_id":"Doodle","ttl":300},"id":2}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestTupleWriteMissingCategory(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.write","params":{"scope":"repo"},"id":3}`)

	if resp.Error == nil {
		t.Fatal("expected error for missing category")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected code %d, got %d", ErrCodeInvalidParams, resp.Error.Code)
	}
}

func TestTupleWriteInvalidParams(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.write","params":"not-an-object","id":4}`)

	if resp.Error == nil {
		t.Fatal("expected error for invalid params")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected code %d, got %d", ErrCodeInvalidParams, resp.Error.Code)
	}
}

func TestTupleWriteDefaultPayloadAndLifecycle(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	// Write with no payload or lifecycle — should default to "{}" and "session".
	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.write","params":{"category":"need","scope":"repo"},"id":5}`)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// Read it back to verify defaults.
	resp = rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.read","params":{"category":"need","scope":"repo"},"id":6}`)
	if resp.Error != nil {
		t.Fatalf("read error: %+v", resp.Error)
	}

	row, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", resp.Result)
	}
	if row["payload"] != "{}" {
		t.Fatalf("expected default payload '{}', got %v", row["payload"])
	}
	if row["lifecycle"] != "session" {
		t.Fatalf("expected lifecycle 'session', got %v", row["lifecycle"])
	}
}

// ---------------------------------------------------------------------------
// tuple.read tests
// ---------------------------------------------------------------------------

func TestTupleReadFound(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	store.Insert("fact", "repo", "health", "local", `{"status":"ok"}`, "session", nil, nil, nil)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.read","params":{"category":"fact","scope":"repo","identity":"health"},"id":10}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	row, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}
	if row["category"] != "fact" {
		t.Fatalf("expected category 'fact', got %v", row["category"])
	}
	if row["identity"] != "health" {
		t.Fatalf("expected identity 'health', got %v", row["identity"])
	}
}

func TestTupleReadNotFound(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.read","params":{"category":"nonexistent"},"id":11}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Result != nil {
		t.Fatalf("expected nil result, got %v", resp.Result)
	}
}

func TestTupleReadNullParams(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	store.Insert("fact", "repo", "x", "local", "{}", "session", nil, nil, nil)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.read","params":null,"id":12}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected a tuple with null params wildcard")
	}
}

func TestTupleReadEmptyParams(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	store.Insert("fact", "repo", "x", "local", "{}", "session", nil, nil, nil)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.read","id":13}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected a tuple with empty params wildcard")
	}
}

func TestTupleReadInvalidParams(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.read","params":42,"id":14}`)

	if resp.Error == nil {
		t.Fatal("expected error for non-object params")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected code %d, got %d", ErrCodeInvalidParams, resp.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// tuple.take tests
// ---------------------------------------------------------------------------

func TestTupleTakeRemovesTuple(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	store.Insert("available", "repo", "task-1", "local", "{}", "session", nil, nil, nil)

	// Take should return the tuple.
	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.take","params":{"category":"available","identity":"task-1"},"id":20}`)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected tuple result from take")
	}

	// Second take should return null (already removed).
	resp = rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.take","params":{"category":"available","identity":"task-1"},"id":21}`)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Result != nil {
		t.Fatal("expected nil result after take consumed the tuple")
	}
}

func TestTupleTakeNotFound(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.take","params":{"category":"nonexistent"},"id":22}`)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Result != nil {
		t.Fatalf("expected nil result, got %v", resp.Result)
	}
}

// ---------------------------------------------------------------------------
// tuple.scan tests
// ---------------------------------------------------------------------------

func TestTupleScanReturnsMultiple(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	for i := 0; i < 3; i++ {
		store.Insert("claim", "repo", fmt.Sprintf("task-%d", i), "local",
			fmt.Sprintf(`{"agent":"agent-%d"}`, i), "session", nil, nil, nil)
	}

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.scan","params":{"category":"claim","scope":"repo"},"id":30}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	rows, ok := resp.Result.([]interface{})
	if !ok {
		t.Fatalf("expected array result, got %T", resp.Result)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
}

func TestTupleScanEmptyResult(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.scan","params":{"category":"nonexistent"},"id":31}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	rows, ok := resp.Result.([]interface{})
	if !ok {
		t.Fatalf("expected array result, got %T", resp.Result)
	}
	if len(rows) != 0 {
		t.Fatalf("expected empty array, got %d rows", len(rows))
	}
}

func TestTupleScanDoesNotRemove(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	store.Insert("fact", "repo", "test", "local", "{}", "session", nil, nil, nil)

	// First scan.
	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.scan","params":{"category":"fact"},"id":32}`)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	rows := resp.Result.([]interface{})
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	// Second scan should still find it (scan is non-destructive).
	resp = rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.scan","params":{"category":"fact"},"id":33}`)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	rows = resp.Result.([]interface{})
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after re-scan, got %d", len(rows))
	}
}

// ---------------------------------------------------------------------------
// End-to-end: write then read/take/scan
// ---------------------------------------------------------------------------

func TestBBSBridgeRoundTrip(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	// 1. Write a tuple via JSON-RPC.
	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.write","params":{"category":"obstacle","scope":"myrepo","identity":"build-fail","payload":"{\"detail\":\"tests broken\"}"},"id":40}`)
	if resp.Error != nil {
		t.Fatalf("write error: %+v", resp.Error)
	}

	// 2. Read it back (non-destructive).
	resp = rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.read","params":{"category":"obstacle","scope":"myrepo"},"id":41}`)
	if resp.Error != nil {
		t.Fatalf("read error: %+v", resp.Error)
	}
	row := resp.Result.(map[string]interface{})
	if row["identity"] != "build-fail" {
		t.Fatalf("expected identity 'build-fail', got %v", row["identity"])
	}

	// 3. Scan to verify it shows up.
	resp = rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.scan","params":{"category":"obstacle"},"id":42}`)
	if resp.Error != nil {
		t.Fatalf("scan error: %+v", resp.Error)
	}
	rows := resp.Result.([]interface{})
	if len(rows) != 1 {
		t.Fatalf("expected 1 obstacle, got %d", len(rows))
	}

	// 4. Take (destructive read).
	resp = rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.take","params":{"category":"obstacle","scope":"myrepo"},"id":43}`)
	if resp.Error != nil {
		t.Fatalf("take error: %+v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected tuple from take")
	}

	// 5. Verify it's gone.
	resp = rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.read","params":{"category":"obstacle","scope":"myrepo"},"id":44}`)
	if resp.Error != nil {
		t.Fatalf("read-after-take error: %+v", resp.Error)
	}
	if resp.Result != nil {
		t.Fatal("expected nil after take removed the tuple")
	}
}

// ---------------------------------------------------------------------------
// Error code routing tests
// ---------------------------------------------------------------------------

func TestRPCErrorCodeRouting(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterBBSHandlers(srv, store)

	// Also register a handler that returns a plain error (should get ErrCodeInternal).
	srv.Handle("plain.error", func(params json.RawMessage) (interface{}, error) {
		return nil, fmt.Errorf("plain failure")
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	// rpcError should produce ErrCodeInvalidParams.
	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"tuple.write","params":"bad","id":50}`)
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected code %d, got %+v", ErrCodeInvalidParams, resp.Error)
	}

	// Plain error should still produce ErrCodeInternal.
	resp = rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"plain.error","id":51}`)
	if resp.Error == nil || resp.Error.Code != ErrCodeInternal {
		t.Fatalf("expected code %d, got %+v", ErrCodeInternal, resp.Error)
	}
}
