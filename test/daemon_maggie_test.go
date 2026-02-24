// Tests for Maggie-side daemon classes: Config loading, DaemonMain event loop,
// and IPC dispatch. Exercises the full Maggie layer using the same test harness
// as bbs_test.go.
package test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chazu/maggie/vm"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// daemonVM creates a Maggie VM with TupleStore primitives, BBS classes,
// Config class, and DaemonMain class all compiled and ready.
func daemonVM(t *testing.T) *vm.VM {
	t.Helper()
	// Start from the full BBS VM (includes TupleSpace, Pattern, etc.)
	v := bbsVM(t)

	rootDir := filepath.Join(filepath.Dir("."), "..")

	// Load Config and DaemonMain in dependency order
	daemonFiles := []string{
		"src/config/Config.mag",
		"src/daemon/DaemonMain.mag",
	}
	for _, f := range daemonFiles {
		path := filepath.Join(rootDir, f)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		compileSourceFile(t, v, string(src))
	}

	return v
}

// daemonEval compiles and executes a method body in a daemon-ready VM.
func daemonEval(t *testing.T, v *vm.VM, body string) vm.Value {
	t.Helper()
	return bbsEval(t, v, body)
}

// ---------------------------------------------------------------------------
// Config Tests
// ---------------------------------------------------------------------------

func TestConfigDefaults(t *testing.T) {
	v := daemonVM(t)

	// Check that defaults are populated
	result := daemonEval(t, v, `
    | cfg |
    cfg := Config defaults.
    ^cfg checkpointInterval`)

	assertSmallInt(t, result, 300000, "default checkpoint interval should be 300000ms (5 min)")
}

func TestConfigDefaultShutdownTimeout(t *testing.T) {
	v := daemonVM(t)

	result := daemonEval(t, v, `
    | cfg |
    cfg := Config defaults.
    ^cfg shutdownTimeout`)

	assertSmallInt(t, result, 30000, "default shutdown timeout should be 30000ms (30 sec)")
}

func TestConfigDefaultProjectName(t *testing.T) {
	v := daemonVM(t)

	result := daemonEval(t, v, `
    | cfg |
    cfg := Config defaults.
    ^cfg projectName`)

	if !vm.IsStringValue(result) {
		t.Fatal("expected string project name")
	}
	got := v.Registry().GetStringContent(result)
	if got != "procyon-park" {
		t.Fatalf("expected 'procyon-park', got %q", got)
	}
}

func TestConfigFromToml(t *testing.T) {
	v := daemonVM(t)

	result := daemonEval(t, v, `
    | cfg |
    cfg := Config fromToml: '[project]
name = "my-project"
version = "2.0"

[daemon]
checkpoint_interval = 60000
shutdown_timeout = 10000'.
    ^cfg checkpointInterval`)

	assertSmallInt(t, result, 60000, "TOML checkpoint_interval should be 60000")
}

func TestConfigFromTomlVersion(t *testing.T) {
	v := daemonVM(t)

	result := daemonEval(t, v, `
    | cfg |
    cfg := Config fromToml: '[project]
name = "my-project"
version = "2.0"'.
    ^cfg version`)

	if !vm.IsStringValue(result) {
		t.Fatal("expected string version")
	}
	got := v.Registry().GetStringContent(result)
	if got != "2.0" {
		t.Fatalf("expected '2.0', got %q", got)
	}
}

func TestConfigFromTomlPartial(t *testing.T) {
	v := daemonVM(t)

	// TOML with only [project], no [daemon] — daemon settings should use defaults
	result := daemonEval(t, v, `
    | cfg |
    cfg := Config fromToml: '[project]
name = "partial"'.
    ^cfg checkpointInterval`)

	assertSmallInt(t, result, 300000, "missing [daemon] section should use default checkpoint interval")
}

func TestConfigLoadMissingFile(t *testing.T) {
	v := daemonVM(t)

	// Loading a nonexistent file should return defaults, not fail
	result := daemonEval(t, v, `
    | cfg |
    cfg := Config load: '/nonexistent/maggie.toml'.
    ^cfg projectName`)

	if !vm.IsStringValue(result) {
		t.Fatal("expected string project name from defaults")
	}
	got := v.Registry().GetStringContent(result)
	if got != "procyon-park" {
		t.Fatalf("expected default 'procyon-park', got %q", got)
	}
}

func TestConfigLoadFromFile(t *testing.T) {
	// Write a temp TOML file and load it
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "maggie.toml")
	tomlContent := `[project]
name = "file-test"
version = "3.0"

[daemon]
checkpoint_interval = 120000
`
	if err := os.WriteFile(tomlPath, []byte(tomlContent), 0644); err != nil {
		t.Fatalf("write toml: %v", err)
	}

	v := daemonVM(t)

	result := daemonEval(t, v, `
    | cfg |
    cfg := Config load: '`+tomlPath+`'.
    ^cfg checkpointInterval`)

	assertSmallInt(t, result, 120000, "loaded checkpoint_interval should be 120000")
}

// ---------------------------------------------------------------------------
// DaemonMain Event Loop Tests
// ---------------------------------------------------------------------------

func TestDaemonMainTestInstance(t *testing.T) {
	v := daemonVM(t)
	tuplestore.Register(v) // ensure primitives are registered

	result := daemonEval(t, v, `
    | daemon |
    daemon := DaemonMain testInstance.
    ^daemon running`)

	// testInstance does not call run, so running should be false
	if result != vm.False {
		t.Fatal("testInstance should not be running before run is called")
	}
}

func TestDaemonMainIpcDispatchStatus(t *testing.T) {
	v := daemonVM(t)

	// Test IPC dispatch: system.status
	result := daemonEval(t, v, `
    | daemon resp |
    daemon := DaemonMain testInstance.
    daemon initWith: (Config fromToml: '[project]
name = "test-daemon"
version = "1.0"').
    daemon space: (TupleSpace open: ':memory:').
    resp := daemon dispatch: 'system.status' params: Dictionary new.
    ^resp at: 'status'`)

	if !vm.IsStringValue(result) {
		t.Fatal("expected string status")
	}
	got := v.Registry().GetStringContent(result)
	if got != "running" {
		t.Fatalf("expected 'running', got %q", got)
	}
}

func TestDaemonMainIpcDispatchUnknown(t *testing.T) {
	v := daemonVM(t)

	// Unknown method should return error
	result := daemonEval(t, v, `
    | daemon resp |
    daemon := DaemonMain testInstance.
    daemon initWith: Config defaults.
    daemon space: (TupleSpace open: ':memory:').
    resp := daemon dispatch: 'bogus.method' params: Dictionary new.
    ^resp at: 'error'`)

	if !vm.IsStringValue(result) {
		t.Fatal("expected string error message")
	}
	got := v.Registry().GetStringContent(result)
	if got == "" {
		t.Fatal("expected non-empty error message for unknown method")
	}
}

func TestDaemonMainCheckpointHandler(t *testing.T) {
	v := daemonVM(t)

	// Test that handleCheckpointTick updates lastCheckpoint
	result := daemonEval(t, v, `
    | daemon |
    daemon := DaemonMain testInstance.
    daemon initWith: Config defaults.
    daemon space: (TupleSpace open: ':memory:').
    daemon handleCheckpointTick.
    ^daemon lastCheckpoint`)

	if !vm.IsStringValue(result) {
		t.Fatal("expected string lastCheckpoint marker")
	}
	got := v.Registry().GetStringContent(result)
	if got != "checkpoint-done" {
		t.Fatalf("expected 'checkpoint-done', got %q", got)
	}
}

func TestDaemonMainEventLoopShutdown(t *testing.T) {
	v := daemonVM(t)

	// Start the daemon event loop in a forked process, then send a shutdown
	// signal. The loop should exit and running should become false.
	result := daemonEval(t, v, `
    | daemon resultCh |
    daemon := DaemonMain testInstance.
    daemon initWith: (Config fromToml: '[daemon]
checkpoint_interval = 600000').
    daemon space: (TupleSpace open: ':memory:').

    resultCh := Channel new: 1.

    "Run the daemon in a forked process"
    [daemon run. resultCh send: daemon running] fork.

    "Give it a moment to start"
    Process sleep: 50.

    "Send shutdown signal"
    daemon requestShutdown: 'test shutdown'.

    "Wait for the daemon to finish"
    ^resultCh receive`)

	// After shutdown, running should be false
	if result != vm.False {
		t.Fatal("daemon should not be running after shutdown")
	}
}

func TestDaemonMainIpcThroughEventLoop(t *testing.T) {
	v := daemonVM(t)

	// Submit an IPC request through the event loop and verify the response
	result := daemonEval(t, v, `
    | daemon resultCh resp |
    daemon := DaemonMain testInstance.
    daemon initWith: (Config fromToml: '[project]
name = "ipc-test"
version = "5.0"

[daemon]
checkpoint_interval = 600000').
    daemon space: (TupleSpace open: ':memory:').

    resultCh := Channel new: 1.

    "Run daemon in background"
    [daemon run] fork.
    Process sleep: 50.

    "Submit IPC request through the channel"
    resp := daemon submit: 'system.status' params: Dictionary new.
    resultCh send: (resp at: 'version').

    "Shut down"
    daemon requestShutdown: 'done'.

    ^resultCh receive`)

	if !vm.IsStringValue(result) {
		t.Fatal("expected string version from IPC status response")
	}
	got := v.Registry().GetStringContent(result)
	if got != "5.0" {
		t.Fatalf("expected '5.0', got %q", got)
	}
}

func TestDaemonMainShutdownViaIpc(t *testing.T) {
	v := daemonVM(t)

	// Test system.shutdown IPC method
	result := daemonEval(t, v, `
    | daemon resultCh |
    daemon := DaemonMain testInstance.
    daemon initWith: (Config fromToml: '[daemon]
checkpoint_interval = 600000').
    daemon space: (TupleSpace open: ':memory:').

    resultCh := Channel new: 1.

    "Run daemon in background"
    [daemon run. resultCh send: daemon running] fork.
    Process sleep: 50.

    "Trigger shutdown via IPC"
    daemon submit: 'system.shutdown' params: Dictionary new.

    ^resultCh receive`)

	if result != vm.False {
		t.Fatal("daemon should not be running after IPC shutdown")
	}
}
