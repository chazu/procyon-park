package daemon

import (
	"encoding/json"
	"testing"
)

func TestConfigSetAndGet(t *testing.T) {
	store := newTestStore(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterConfigHandlers(srv, store)

	// Set a value.
	setParams, _ := json.Marshal(map[string]string{"key": "editor", "value": "vim"})
	_, err := srv.handlers["config.set"](setParams)
	if err != nil {
		t.Fatalf("config.set: %v", err)
	}

	// Get the value.
	getParams, _ := json.Marshal(map[string]string{"key": "editor"})
	result, err := srv.handlers["config.get"](getParams)
	if err != nil {
		t.Fatalf("config.get: %v", err)
	}
	if result != "vim" {
		t.Errorf("expected 'vim', got %v", result)
	}
}

func TestConfigSetUpsert(t *testing.T) {
	store := newTestStore(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterConfigHandlers(srv, store)

	// Set initial value.
	params, _ := json.Marshal(map[string]string{"key": "theme", "value": "dark"})
	srv.handlers["config.set"](params)

	// Overwrite.
	params, _ = json.Marshal(map[string]string{"key": "theme", "value": "light"})
	srv.handlers["config.set"](params)

	// Get should return updated value.
	getParams, _ := json.Marshal(map[string]string{"key": "theme"})
	result, err := srv.handlers["config.get"](getParams)
	if err != nil {
		t.Fatalf("config.get: %v", err)
	}
	if result != "light" {
		t.Errorf("expected 'light', got %v", result)
	}
}

func TestConfigGetNotFound(t *testing.T) {
	store := newTestStore(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterConfigHandlers(srv, store)

	params, _ := json.Marshal(map[string]string{"key": "nonexistent"})
	_, err := srv.handlers["config.get"](params)
	if err == nil {
		t.Fatal("expected error for nonexistent key")
	}
}

func TestConfigList(t *testing.T) {
	store := newTestStore(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterConfigHandlers(srv, store)

	// Set two values.
	p1, _ := json.Marshal(map[string]string{"key": "a", "value": "1"})
	p2, _ := json.Marshal(map[string]string{"key": "b", "value": "2"})
	srv.handlers["config.set"](p1)
	srv.handlers["config.set"](p2)

	// List.
	result, err := srv.handlers["config.list"](nil)
	if err != nil {
		t.Fatalf("config.list: %v", err)
	}

	type entry struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	data, _ := json.Marshal(result)
	var entries []entry
	json.Unmarshal(data, &entries)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestConfigPath(t *testing.T) {
	store := newTestStore(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterConfigHandlers(srv, store)

	result, err := srv.handlers["config.path"](nil)
	if err != nil {
		t.Fatalf("config.path: %v", err)
	}
	path, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if path == "" {
		t.Fatal("expected non-empty config path")
	}
}

func TestConfigSetMissingKey(t *testing.T) {
	store := newTestStore(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterConfigHandlers(srv, store)

	params, _ := json.Marshal(map[string]string{"value": "val"})
	_, err := srv.handlers["config.set"](params)
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestConfigGetMissingKey(t *testing.T) {
	store := newTestStore(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterConfigHandlers(srv, store)

	params, _ := json.Marshal(map[string]string{})
	_, err := srv.handlers["config.get"](params)
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}
