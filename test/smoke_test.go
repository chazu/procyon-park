// Smoke tests verifying all 8 Maggie primitives needed by procyon-park
// are functional: ExternalProcess, UnixSocket, Json, Sqlite, Toml,
// DuckDB, CUE, and atomic image checkpointing.
package test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chazu/maggie/compiler"
	"github.com/chazu/maggie/vm"
)

// newVM creates a fresh VM with the Go compiler wired up.
func newVM(t *testing.T) *vm.VM {
	t.Helper()
	v := vm.NewVM()
	v.UseGoCompiler(compiler.Compile)
	return v
}

// evalMethod compiles and executes a full method body, returning the result.
func evalMethod(t *testing.T, v *vm.VM, body string) vm.Value {
	t.Helper()
	source := "doIt\n" + body
	method, err := compiler.Compile(source, v.Selectors, v.Symbols, v.Registry())
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	return v.Execute(method, vm.Nil, nil)
}

func TestExternalProcess(t *testing.T) {
	v := newVM(t)

	result := evalMethod(t, v, "    ^ExternalProcess run: 'echo' args: #('hello')")
	if !vm.IsStringValue(result) {
		t.Fatal("expected string result from ExternalProcess run:args:")
	}
	stdout := v.Registry().GetStringContent(result)
	if stdout != "hello\n" {
		t.Fatalf("expected 'hello\\n', got %q", stdout)
	}
}

func TestUnixSocket(t *testing.T) {
	v := newVM(t)

	sockPath := filepath.Join(t.TempDir(), "test.sock")

	src := `    | server conn response |
    server := UnixSocketServer listenAt: '` + sockPath + `'.
    [
        | connServer |
        connServer := server accept.
        connServer sendLine: 'pong'.
        connServer close
    ] fork.
    Process sleep: 50.
    conn := UnixSocketClient connectTo: '` + sockPath + `'.
    response := conn receiveLine.
    conn close.
    server close.
    ^response`

	result := evalMethod(t, v, src)
	if !vm.IsStringValue(result) {
		t.Fatal("expected string result from unix socket round-trip")
	}
	got := v.Registry().GetStringContent(result)
	if got != "pong" {
		t.Fatalf("expected 'pong', got %q", got)
	}
}

func TestJson(t *testing.T) {
	v := newVM(t)

	// Use primEncode:/primDecode: directly (encode:/decode: are .mag wrappers)
	src := `    | d encoded |
    d := Dictionary new.
    d at: 'name' put: 'Alice'.
    d at: 'age' put: 30.
    encoded := Json primEncode: d.
    ^Json primDecode: encoded`

	result := evalMethod(t, v, src)
	if !vm.IsDictionaryValue(result) {
		t.Fatalf("expected Dictionary from JSON round-trip, got isSmallInt=%v isObject=%v isNil=%v",
			result.IsSmallInt(), result.IsObject(), result == vm.Nil)
	}
}

func TestSqlite(t *testing.T) {
	v := newVM(t)

	src := `    | db row |
    db := SqliteDatabase openMemory.
    db execute: 'CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)'.
    db execute: 'INSERT INTO t (name) VALUES (?)' with: #('Alice').
    row := db queryRow: 'SELECT * FROM t WHERE name = ?' with: #('Alice').
    db close.
    ^row at: 'name'`

	result := evalMethod(t, v, src)
	if !vm.IsStringValue(result) {
		t.Fatal("expected string from sqlite query")
	}
	got := v.Registry().GetStringContent(result)
	if got != "Alice" {
		t.Fatalf("expected 'Alice', got %q", got)
	}
}

func TestToml(t *testing.T) {
	v := newVM(t)

	src := `    ^(Toml decode: '[project]
name = "test"
version = "1.0"') at: 'project'`

	result := evalMethod(t, v, src)
	if !vm.IsDictionaryValue(result) {
		t.Fatal("expected Dictionary from TOML decode")
	}
}

func TestDuckDB(t *testing.T) {
	v := newVM(t)

	src := `    | db rows |
    db := DuckDatabase new.
    db execute: 'CREATE TABLE t (id INTEGER, name VARCHAR)'.
    db execute: 'INSERT INTO t VALUES (1, ''Alice'')'.
    rows := db query: 'SELECT * FROM t'.
    db close.
    ^rows`

	result := evalMethod(t, v, src)
	if result == vm.Nil {
		t.Fatal("expected non-nil result from DuckDB query")
	}
}

func TestCue(t *testing.T) {
	v := newVM(t)

	// toMaggie also returns a Result, so we need .value at each step
	src := `    | ctx result val lookupResult inner |
    ctx := CueContext new.
    result := ctx compileString: 'x: 42, y: x + 1'.
    val := result value.
    lookupResult := val lookup: 'y'.
    inner := lookupResult value.
    ^(inner toMaggie) value`

	result := evalMethod(t, v, src)
	if !result.IsSmallInt() || result.SmallInt() != 43 {
		t.Fatalf("expected 43 from CUE evaluation, got %v (isSmallInt=%v isFloat=%v)", result, result.IsSmallInt(), result.IsFloat())
	}
}

func TestAtomicImageCheckpoint(t *testing.T) {
	v := newVM(t)

	imgPath := filepath.Join(t.TempDir(), "test.image")

	err := v.SaveImageAtomic(imgPath)
	if err != nil {
		t.Fatalf("SaveImageAtomic failed: %v", err)
	}

	// Verify file exists
	info, err := os.Stat(imgPath)
	if err != nil {
		t.Fatalf("image file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("image file is empty")
	}

	// Verify we can load it back
	v2 := vm.NewVM()
	if err := v2.LoadImage(imgPath); err != nil {
		t.Fatalf("failed to load saved image: %v", err)
	}
}
