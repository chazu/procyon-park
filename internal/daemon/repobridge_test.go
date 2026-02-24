package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chazu/procyon-park/internal/tuplestore"
)

func newTestStore(t *testing.T) *tuplestore.TupleStore {
	t.Helper()
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatalf("create test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestRepoRegisterAndList(t *testing.T) {
	store := newTestStore(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterRepoHandlers(srv, store)

	// Register a repo.
	regParams, _ := json.Marshal(map[string]string{"name": "myrepo", "path": "/tmp/myrepo"})
	result, err := srv.handlers["repo.register"](regParams)
	if err != nil {
		t.Fatalf("repo.register: %v", err)
	}
	reg := result.(map[string]interface{})
	if reg["name"] != "myrepo" {
		t.Errorf("expected name 'myrepo', got %v", reg["name"])
	}

	// List repos.
	result, err = srv.handlers["repo.list"](nil)
	if err != nil {
		t.Fatalf("repo.list: %v", err)
	}
	type repoEntry struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	data, _ := json.Marshal(result)
	var repos []repoEntry
	json.Unmarshal(data, &repos)
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].Name != "myrepo" {
		t.Errorf("expected name 'myrepo', got %q", repos[0].Name)
	}
}

func TestRepoRegisterDuplicate(t *testing.T) {
	store := newTestStore(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterRepoHandlers(srv, store)

	params, _ := json.Marshal(map[string]string{"name": "dup", "path": "/tmp/dup"})
	_, err := srv.handlers["repo.register"](params)
	if err != nil {
		t.Fatalf("first register: %v", err)
	}

	_, err = srv.handlers["repo.register"](params)
	if err == nil {
		t.Fatal("expected error for duplicate registration")
	}
}

func TestRepoUnregister(t *testing.T) {
	store := newTestStore(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterRepoHandlers(srv, store)

	// Register then unregister.
	regParams, _ := json.Marshal(map[string]string{"name": "tounreg", "path": "/tmp/tounreg"})
	srv.handlers["repo.register"](regParams)

	unregParams, _ := json.Marshal(map[string]string{"name": "tounreg"})
	_, err := srv.handlers["repo.unregister"](unregParams)
	if err != nil {
		t.Fatalf("repo.unregister: %v", err)
	}

	// List should be empty.
	result, _ := srv.handlers["repo.list"](nil)
	data, _ := json.Marshal(result)
	var repos []struct{ Name string }
	json.Unmarshal(data, &repos)
	if len(repos) != 0 {
		t.Errorf("expected 0 repos after unregister, got %d", len(repos))
	}
}

func TestRepoUnregisterNotFound(t *testing.T) {
	store := newTestStore(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterRepoHandlers(srv, store)

	params, _ := json.Marshal(map[string]string{"name": "nonexistent"})
	_, err := srv.handlers["repo.unregister"](params)
	if err == nil {
		t.Fatal("expected error for unregistering nonexistent repo")
	}
}

func TestRepoStatus(t *testing.T) {
	store := newTestStore(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterRepoHandlers(srv, store)

	// Create a temp dir as a fake repo.
	tmpDir := t.TempDir()
	gitDir := filepath.Join(tmpDir, ".git")
	os.Mkdir(gitDir, 0755)

	regParams, _ := json.Marshal(map[string]string{"name": "testrepo", "path": tmpDir})
	srv.handlers["repo.register"](regParams)

	// Status should show "ok" for the temp dir with .git.
	result, err := srv.handlers["repo.status"](nil)
	if err != nil {
		t.Fatalf("repo.status: %v", err)
	}

	type statusEntry struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	data, _ := json.Marshal(result)
	var statuses []statusEntry
	json.Unmarshal(data, &statuses)
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Status != "ok" {
		t.Errorf("expected status 'ok', got %q", statuses[0].Status)
	}
}

func TestRepoStatusMissing(t *testing.T) {
	store := newTestStore(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterRepoHandlers(srv, store)

	regParams, _ := json.Marshal(map[string]string{"name": "gone", "path": "/nonexistent/path/that/doesnt/exist"})
	srv.handlers["repo.register"](regParams)

	result, err := srv.handlers["repo.status"](nil)
	if err != nil {
		t.Fatalf("repo.status: %v", err)
	}

	type statusEntry struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	data, _ := json.Marshal(result)
	var statuses []statusEntry
	json.Unmarshal(data, &statuses)
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Status != "missing" {
		t.Errorf("expected status 'missing', got %q", statuses[0].Status)
	}
}

func TestRepoRegisterMissingParams(t *testing.T) {
	store := newTestStore(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterRepoHandlers(srv, store)

	// Missing name.
	params, _ := json.Marshal(map[string]string{"path": "/tmp/foo"})
	_, err := srv.handlers["repo.register"](params)
	if err == nil {
		t.Fatal("expected error for missing name")
	}

	// Missing path.
	params, _ = json.Marshal(map[string]string{"name": "foo"})
	_, err = srv.handlers["repo.register"](params)
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}
