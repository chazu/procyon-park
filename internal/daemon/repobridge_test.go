package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chazu/procyon-park/internal/registry"
)

func newTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	reg, err := registry.New(path)
	if err != nil {
		t.Fatalf("create test registry: %v", err)
	}
	return reg
}

func TestRepoRegisterAndList(t *testing.T) {
	reg := newTestRegistry(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterRepoHandlers(srv, reg)

	// Create a temp dir as a fake repo with .git.
	tmpDir := t.TempDir()
	os.Mkdir(filepath.Join(tmpDir, ".git"), 0755)

	regParams, _ := json.Marshal(map[string]string{"name": "myrepo", "path": tmpDir})
	result, err := srv.handlers["repo.register"](regParams)
	if err != nil {
		t.Fatalf("repo.register: %v", err)
	}
	data, _ := json.Marshal(result)
	var registered registry.Repo
	json.Unmarshal(data, &registered)
	if registered.Name != "myrepo" {
		t.Errorf("expected name 'myrepo', got %v", registered.Name)
	}

	// List repos.
	result, err = srv.handlers["repo.list"](nil)
	if err != nil {
		t.Fatalf("repo.list: %v", err)
	}
	data, _ = json.Marshal(result)
	var repos []registry.Repo
	json.Unmarshal(data, &repos)
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].Name != "myrepo" {
		t.Errorf("expected name 'myrepo', got %q", repos[0].Name)
	}
}

func TestRepoRegisterDuplicate(t *testing.T) {
	reg := newTestRegistry(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterRepoHandlers(srv, reg)

	tmpDir := t.TempDir()
	os.Mkdir(filepath.Join(tmpDir, ".git"), 0755)

	params, _ := json.Marshal(map[string]string{"name": "dup", "path": tmpDir})
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
	reg := newTestRegistry(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterRepoHandlers(srv, reg)

	tmpDir := t.TempDir()
	os.Mkdir(filepath.Join(tmpDir, ".git"), 0755)

	regParams, _ := json.Marshal(map[string]string{"name": "tounreg", "path": tmpDir})
	srv.handlers["repo.register"](regParams)

	unregParams, _ := json.Marshal(map[string]string{"name": "tounreg"})
	_, err := srv.handlers["repo.unregister"](unregParams)
	if err != nil {
		t.Fatalf("repo.unregister: %v", err)
	}

	// List should be empty.
	result, _ := srv.handlers["repo.list"](nil)
	data, _ := json.Marshal(result)
	var repos []registry.Repo
	json.Unmarshal(data, &repos)
	if len(repos) != 0 {
		t.Errorf("expected 0 repos after unregister, got %d", len(repos))
	}
}

func TestRepoUnregisterNotFound(t *testing.T) {
	reg := newTestRegistry(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterRepoHandlers(srv, reg)

	params, _ := json.Marshal(map[string]string{"name": "nonexistent"})
	_, err := srv.handlers["repo.unregister"](params)
	if err == nil {
		t.Fatal("expected error for unregistering nonexistent repo")
	}
}

func TestRepoStatus(t *testing.T) {
	reg := newTestRegistry(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterRepoHandlers(srv, reg)

	tmpDir := t.TempDir()
	gitDir := filepath.Join(tmpDir, ".git")
	os.Mkdir(gitDir, 0755)

	regParams, _ := json.Marshal(map[string]string{"name": "testrepo", "path": tmpDir})
	srv.handlers["repo.register"](regParams)

	result, err := srv.handlers["repo.status"](nil)
	if err != nil {
		t.Fatalf("repo.status: %v", err)
	}

	data, _ := json.Marshal(result)
	var statuses []repoStatusEntry
	json.Unmarshal(data, &statuses)
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Status != "ok" {
		t.Errorf("expected status 'ok', got %q", statuses[0].Status)
	}
}

func TestRepoStatusMissing(t *testing.T) {
	reg := newTestRegistry(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterRepoHandlers(srv, reg)

	regParams, _ := json.Marshal(map[string]string{"name": "gone", "path": "/nonexistent/path/that/doesnt/exist"})
	srv.handlers["repo.register"](regParams)

	result, err := srv.handlers["repo.status"](nil)
	if err != nil {
		t.Fatalf("repo.status: %v", err)
	}

	data, _ := json.Marshal(result)
	var statuses []repoStatusEntry
	json.Unmarshal(data, &statuses)
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Status != "stale" {
		t.Errorf("expected status 'stale', got %q", statuses[0].Status)
	}
	if statuses[0].Warning == "" {
		t.Error("expected a warning for missing path")
	}
}

func TestRepoRegisterMissingParams(t *testing.T) {
	reg := newTestRegistry(t)
	srv := NewIPCServer("/dev/null", make(chan struct{}))
	RegisterRepoHandlers(srv, reg)

	// Missing path.
	params, _ := json.Marshal(map[string]string{"name": "foo"})
	_, err := srv.handlers["repo.register"](params)
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}
