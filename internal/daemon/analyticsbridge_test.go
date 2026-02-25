package daemon

import (
	"encoding/json"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// analytics.performance tests
// ---------------------------------------------------------------------------

func TestAnalyticsPerformance_NoData(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAnalyticsHandlers(srv, store, t.TempDir())
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"analytics.performance","params":{},"id":1}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// Should return empty array.
	b, _ := json.Marshal(resp.Result)
	var results []interface{}
	if err := json.Unmarshal(b, &results); err != nil {
		t.Fatalf("expected array result, got %s", string(b))
	}
	if len(results) != 0 {
		t.Fatalf("expected empty array, got %d items", len(results))
	}
}

func TestAnalyticsPerformance_WithScope(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAnalyticsHandlers(srv, store, t.TempDir())
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"analytics.performance","params":{"scope":"test-repo"},"id":2}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestAnalyticsPerformance_InvalidParams(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAnalyticsHandlers(srv, store, t.TempDir())
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"analytics.performance","params":"bad","id":3}`)

	if resp.Error == nil {
		t.Fatal("expected error for invalid params")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected code %d, got %d", ErrCodeInvalidParams, resp.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// analytics.obstacles tests
// ---------------------------------------------------------------------------

func TestAnalyticsObstacles_NoData(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAnalyticsHandlers(srv, store, t.TempDir())
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"analytics.obstacles","params":{"min_count":3},"id":4}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

// ---------------------------------------------------------------------------
// analytics.conventions tests
// ---------------------------------------------------------------------------

func TestAnalyticsConventions_NoData(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAnalyticsHandlers(srv, store, t.TempDir())
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"analytics.conventions","params":{},"id":5}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

// ---------------------------------------------------------------------------
// analytics.knowledge tests
// ---------------------------------------------------------------------------

func TestAnalyticsKnowledge_NoData(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAnalyticsHandlers(srv, store, t.TempDir())
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"analytics.knowledge","params":{},"id":6}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

// ---------------------------------------------------------------------------
// analytics.signatures tests
// ---------------------------------------------------------------------------

func TestAnalyticsSignatures_NoData(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAnalyticsHandlers(srv, store, t.TempDir())
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"analytics.signatures","params":{},"id":7}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

// ---------------------------------------------------------------------------
// gc.run tests
// ---------------------------------------------------------------------------

func TestGCRun_EmptyStore(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAnalyticsHandlers(srv, store, t.TempDir())
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"gc.run","params":null,"id":8}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	b, _ := json.Marshal(resp.Result)
	var result map[string]interface{}
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("expected map result, got %s", string(b))
	}

	// All counters should be 0 on an empty store.
	for _, key := range []string{"ExpiredEphemeral", "StaleClaims", "AbandonedClaims", "PromotedConventions", "ArchivedTasks", "SynthesizedTuples"} {
		val, ok := result[key].(float64)
		if !ok {
			t.Fatalf("expected float64 for %s, got %T", key, result[key])
		}
		if val != 0 {
			t.Fatalf("expected 0 for %s, got %v", key, val)
		}
	}
}

// ---------------------------------------------------------------------------
// gc.status tests
// ---------------------------------------------------------------------------

func TestGCStatus_EmptyStore(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAnalyticsHandlers(srv, store, t.TempDir())
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"gc.status","params":null,"id":9}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	b, _ := json.Marshal(resp.Result)
	var result map[string]interface{}
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("expected map result, got %s", string(b))
	}
	total, ok := result["total_tuples"].(float64)
	if !ok {
		t.Fatalf("expected float64 for total_tuples, got %T", result["total_tuples"])
	}
	if total != 0 {
		t.Fatalf("expected 0 total tuples, got %v", total)
	}
}

func TestGCStatus_WithTuples(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	// Insert some tuples.
	store.Insert("fact", "test", "ident1", "local", "{}", "session", nil, nil, nil)
	store.Insert("claim", "test", "ident2", "local", "{}", "session", nil, nil, nil)
	store.Insert("convention", "test", "ident3", "local", "{}", "furniture", nil, nil, nil)

	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAnalyticsHandlers(srv, store, t.TempDir())
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"gc.status","params":null,"id":10}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	b, _ := json.Marshal(resp.Result)
	var result map[string]interface{}
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("expected map result, got %s", string(b))
	}
	total, ok := result["total_tuples"].(float64)
	if !ok {
		t.Fatalf("expected float64 for total_tuples, got %T", result["total_tuples"])
	}
	if total != 3 {
		t.Fatalf("expected 3 total tuples, got %v", total)
	}
}

// ---------------------------------------------------------------------------
// feedback.run tests
// ---------------------------------------------------------------------------

func TestFeedbackRun_NoWarmData(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAnalyticsHandlers(srv, store, t.TempDir())
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"feedback.run","params":{"repo":"test"},"id":11}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

// ---------------------------------------------------------------------------
// synthesis.run tests
// ---------------------------------------------------------------------------

func TestSynthesisRun_MissingTaskID(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAnalyticsHandlers(srv, store, t.TempDir())
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"synthesis.run","params":{},"id":12}`)

	if resp.Error == nil {
		t.Fatal("expected error for missing task_id")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected code %d, got %d", ErrCodeInvalidParams, resp.Error.Code)
	}
}

func TestSynthesisRun_NoTuples(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAnalyticsHandlers(srv, store, t.TempDir())
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"synthesis.run","params":{"task_id":"nonexistent-task"},"id":13}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	b, _ := json.Marshal(resp.Result)
	var result map[string]interface{}
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("expected map result, got %s", string(b))
	}
	if result["task_id"] != "nonexistent-task" {
		t.Fatalf("expected task_id nonexistent-task, got %v", result["task_id"])
	}
	if result["tuples"].(float64) != 0 {
		t.Fatalf("expected 0 tuples, got %v", result["tuples"])
	}
}

func TestSynthesisRun_InvalidParams(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	store := mustNewStore(t)
	srv := NewIPCServer(sockPath, shutdownCh)
	RegisterAnalyticsHandlers(srv, store, t.TempDir())
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"synthesis.run","params":"bad","id":14}`)

	if resp.Error == nil {
		t.Fatal("expected error for invalid params")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected code %d, got %d", ErrCodeInvalidParams, resp.Error.Code)
	}
}
