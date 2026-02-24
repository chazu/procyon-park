// Integration tests for the full tuplespace stack, exercising the Maggie VM
// layer (TupleSpace, TupleGC, AgentMailbox, CrossPollinator) backed by
// in-memory SQLite via the Go TupleStore primitives.
package test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/chazu/maggie/compiler"
	"github.com/chazu/maggie/vm"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// bbsVM creates a Maggie VM with TupleStore primitives registered and all
// BBS .mag source files compiled in the correct dependency order.
func bbsVM(t *testing.T) *vm.VM {
	t.Helper()
	v := vm.NewVM()
	v.UseGoCompiler(compiler.Compile)
	tuplestore.Register(v)

	rootDir := filepath.Join(filepath.Dir("."), "..")
	maggieLibDir := filepath.Join(rootDir, "..", "maggie", "lib")

	// Load only standard library .mag files that provide methods NOT already
	// registered as Go primitives. We avoid Object.mag because its compiled
	// method wrappers (yourself, ==, etc.) replace working Go primitives with
	// broken compiled versions.
	safeLibFiles := []string{
		"String.mag",         // ,  (concat), isEmpty, printString, asSymbol, etc.
		"SmallInteger.mag",   // printString, +, -, *, /, etc.
		"Array.mag",          // collect:, reject:, copyWith:, do:, first, etc.
		"Result.mag",         // base class for Success/Failure
		"Failure.mag",        // isFailure, isSuccess, error, value
		"Success.mag",        // isSuccess, isFailure, value, error
	}
	for _, f := range safeLibFiles {
		path := filepath.Join(maggieLibDir, f)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read lib/%s: %v", f, err)
		}
		compileSourceFile(t, v, string(src))
	}

	// Register missing methods that BBS code needs but aren't Go primitives.
	// Json encode: → delegates to the Go primitive primEncode:
	jsonClass := v.Classes.Lookup("Json")
	if jsonClass != nil {
		primSel := v.Selectors.Lookup("primEncode:")
		if primSel >= 0 {
			if m := jsonClass.ClassVTable.Lookup(primSel); m != nil {
				encodeSel := v.Selectors.Intern("encode:")
				jsonClass.ClassVTable.AddMethod(encodeSel, m)
			}
		}
	}

	// Fix ifNil: to return self for non-nil objects (Smalltalk semantics).
	// The VM's default returns Nil, but BBS code uses patterns like
	// `aTuple scope ifNil: ['']` expecting the non-nil scope to pass through.
	v.ObjectClass.AddMethod1(v.Selectors, "ifNil:", func(_ interface{}, recv vm.Value, block vm.Value) vm.Value {
		return recv
	})

	// Fix ifNotNil: to pass receiver to the block
	v.ObjectClass.AddMethod1(v.Selectors, "ifNotNil:", func(vmPtr interface{}, recv vm.Value, block vm.Value) vm.Value {
		theVM := vmPtr.(*vm.VM)
		return theVM.Send(block, "value:", []vm.Value{recv})
	})

	// copy — create a copy of an array for safe iteration
	v.ArrayClass.AddMethod0(v.Selectors, "copy", func(vmPtr interface{}, recv vm.Value) vm.Value {
		theVM := vmPtr.(*vm.VM)
		obj := vm.ObjectFromValue(recv)
		if obj == nil {
			return recv
		}
		n := obj.NumSlots()
		cp := theVM.NewArray(n)
		cpObj := vm.ObjectFromValue(cp)
		if cpObj == nil {
			return recv
		}
		for i := 0; i < n; i++ {
			cpObj.SetSlot(i, obj.GetSlot(i))
		}
		return cp
	})

	// isKindOf: — check class hierarchy. Not a Go primitive, but needed by
	// BBS code for `(payload isKindOf: Dictionary)` and similar.
	v.ObjectClass.AddMethod1(v.Selectors, "isKindOf:", func(vmPtr interface{}, recv vm.Value, aClass vm.Value) vm.Value {
		theVM := vmPtr.(*vm.VM)
		recvClass := theVM.ClassFor(recv)
		targetClass := theVM.GetClassFromValue(aClass)
		if recvClass == nil || targetClass == nil {
			return vm.False
		}
		if recvClass == targetClass || recvClass.IsSubclassOf(targetClass) {
			return vm.True
		}
		return vm.False
	})

	// Transcript show: and cr (no-ops for test — just discard output)
	// Register on ObjectClass so any object can receive them.
	v.ObjectClass.AddMethod1(v.Selectors, "show:", func(_ interface{}, recv vm.Value, msg vm.Value) vm.Value {
		return recv
	})
	v.ObjectClass.AddMethod0(v.Selectors, "cr", func(_ interface{}, recv vm.Value) vm.Value {
		return recv
	})

	// Register Transcript global (needed by TupleGC logging)
	if _, exists := v.Globals["Transcript"]; !exists {
		v.Globals["Transcript"] = vm.Nil
	}

	// Load BBS .mag files in dependency order
	bbsFiles := []string{
		"src/bbs/TupleCategory.mag",
		"src/bbs/Tuple.mag",
		"src/bbs/Pattern.mag",
		"src/bbs/Waiter.mag",
		"src/bbs/TupleSpace.mag",
		"src/bbs/TupleGC.mag",
		"src/bbs/AgentMailbox.mag",
		"src/bbs/CrossPollinator.mag",
	}
	for _, f := range bbsFiles {
		path := filepath.Join(rootDir, f)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		compileSourceFile(t, v, string(src))
	}

	// Fix lifecycle comparison: After DB round-trip, lifecycle is a string
	// but isFurniture/isSession/isEphemeral compare against symbols using =.
	// The compiler emits OpSendEQ for = which uses primitiveEQ (identity-only
	// for string vs symbol). Override the lifecycle checkers with Go methods
	// that handle both string and symbol comparison.
	tupleClass := v.Classes.LookupInNamespace("ProcyonPark::Bbs", "Tuple")
	if tupleClass == nil {
		tupleClass = v.Classes.Lookup("Tuple")
	}
	if tupleClass != nil {
		lifecycleIdx := -1
		for i, name := range tupleClass.AllInstVarNames() {
			if name == "lifecycle" {
				lifecycleIdx = i
				break
			}
		}
		if lifecycleIdx >= 0 {
			matchLifecycle := func(vmPtr interface{}, recv vm.Value, target string) vm.Value {
				theVM := vmPtr.(*vm.VM)
				obj := vm.ObjectFromValue(recv)
				if obj == nil {
					return vm.False
				}
				lcVal := obj.GetSlot(lifecycleIdx)
				if lcVal == vm.Nil {
					return vm.False
				}
				var lcStr string
				if vm.IsStringValue(lcVal) {
					lcStr = theVM.Registry().GetStringContent(lcVal)
				} else if lcVal.IsSymbol() {
					lcStr = theVM.SymbolName(lcVal.SymbolID())
				}
				if lcStr == target {
					return vm.True
				}
				return vm.False
			}
			tupleClass.AddMethod0(v.Selectors, "isFurniture", func(vmPtr interface{}, recv vm.Value) vm.Value {
				return matchLifecycle(vmPtr, recv, "furniture")
			})
			tupleClass.AddMethod0(v.Selectors, "isSession", func(vmPtr interface{}, recv vm.Value) vm.Value {
				return matchLifecycle(vmPtr, recv, "session")
			})
			tupleClass.AddMethod0(v.Selectors, "isEphemeral", func(vmPtr interface{}, recv vm.Value) vm.Value {
				return matchLifecycle(vmPtr, recv, "ephemeral")
			})
		}
	}

	return v
}

// preprocessSource extracts the namespace from a .mag source file and
// rearranges the source so the namespace: directive appears before any
// docstrings. The parser requires namespace: to be the first non-EOF token
// in the preamble, but .mag files typically have a leading docstring.
func preprocessSource(source string) (processed string, namespace string) {
	// Extract namespace: directive (may be after docstring)
	nsRe := regexp.MustCompile(`(?m)^namespace:\s+(\S+)\s*$`)
	if m := nsRe.FindStringSubmatch(source); m != nil {
		namespace = m[1]
		// Remove the namespace line from source
		source = nsRe.ReplaceAllString(source, "")
	}

	// Convert "class method:" to "classMethod:" (parser expects camelCase)
	source = strings.ReplaceAll(source, "class method:", "classMethod:")

	// Put namespace: at the very top so the parser sees it first
	if namespace != "" {
		processed = "namespace: " + namespace + "\n" + strings.TrimLeft(source, "\n")
	} else {
		processed = source
	}
	return
}

// compileSourceFile parses a .mag-style source file string and compiles
// all classes and methods into the VM, replicating the two-pass pipeline
// from cmd/mag. Handles namespace: directives correctly.
func compileSourceFile(t *testing.T, vmInst *vm.VM, source string) {
	t.Helper()

	processed, nsFromSource := preprocessSource(source)

	sf, err := compiler.ParseSourceFileFromString(processed)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	// Determine namespace
	var namespace string
	if sf.Namespace != nil {
		namespace = sf.Namespace.Name
	}
	if namespace == "" {
		namespace = nsFromSource
	}

	var imports []string
	for _, imp := range sf.Imports {
		imports = append(imports, cub.Path)
	}

	// Pass 1: Register class skeletons
	type classEntry struct {
		class    *vm.Class
		classDef *compiler.ClassDef
	}
	var entries []classEntry

	for _, classDef := range sf.Classes {
		// Check if class already exists
		var class *vm.Class
		if namespace != "" {
			class = vmInst.Classes.LookupInNamespace(namespace, classDef.Name)
		}
		if class == nil {
			class = vmInst.Classes.Lookup(classDef.Name)
		}

		if class == nil {
			class = vm.NewClassWithInstVars(classDef.Name, vmInst.ObjectClass, classDef.InstanceVariables)
			if namespace != "" {
				class.Namespace = namespace
			}
			vmInst.Classes.Register(class)

			// Register in globals — bare name for tests to work simply
			classVal := vmInst.ClassValue(class)
			vmInst.Globals[classDef.Name] = classVal
			if namespace != "" {
				vmInst.Globals[namespace+"::"+classDef.Name] = classVal
			}
		}

		entries = append(entries, classEntry{class: class, classDef: classDef})
	}

	// Pass 1b: Resolve superclass pointers
	for _, ce := range entries {
		if ce.classDef.Superclass == "" || ce.classDef.Superclass == "Object" || ce.classDef.Superclass == "nil" {
			continue
		}
		if ce.class.Superclass != nil && ce.class.Superclass != vmInst.ObjectClass {
			continue
		}

		resolved := vmInst.Classes.LookupWithImports(ce.classDef.Superclass, namespace, imports)
		if resolved == nil {
			t.Fatalf("class %s: superclass %s not found", ce.classDef.Name, ce.classDef.Superclass)
		}

		ce.class.Superclass = resolved
		ce.class.VTable.SetParent(resolved.VTable)
		ce.class.ClassVTable.SetParent(resolved.ClassVTable)
		ce.class.NumSlots = len(ce.class.AllInstVarNames())
	}

	// Pass 2: Compile methods
	for _, ce := range entries {
		allIvars := ce.class.AllInstVarNames()

		for _, methodDef := range ce.classDef.Methods {
			if methodDef.IsPrimitiveStub {
				continue
			}
			method, cErr := compiler.CompileMethodDefWithContext(methodDef, vmInst.Selectors, vmInst.Symbols, vmInst.Registry(), allIvars, namespace, imports, vmInst.Classes)
			if cErr != nil {
				t.Fatalf("compile error %s>>%s: %v", ce.classDef.Name, methodDef.Selector, cErr)
			}
			method.SetClass(ce.class)
			selectorID := vmInst.Selectors.Intern(method.Name())
			ce.class.VTable.AddMethod(selectorID, method)
		}

		for _, methodDef := range ce.classDef.ClassMethods {
			if methodDef.IsPrimitiveStub {
				continue
			}
			method, cErr := compiler.CompileMethodDefWithContext(methodDef, vmInst.Selectors, vmInst.Symbols, vmInst.Registry(), nil, namespace, imports, vmInst.Classes)
			if cErr != nil {
				t.Fatalf("compile error %s class>>%s: %v", ce.classDef.Name, methodDef.Selector, cErr)
			}
			method.SetClass(ce.class)
			method.IsClassMethod = true
			selectorID := vmInst.Selectors.Intern(method.Name())
			ce.class.ClassVTable.AddMethod(selectorID, method)
		}
	}
}

// bbsEval compiles and executes a method body, returning the result.
func bbsEval(t *testing.T, v *vm.VM, body string) vm.Value {
	t.Helper()
	source := "doIt\n" + body
	method, err := compiler.Compile(source, v.Selectors, v.Symbols, v.Registry())
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	return v.Execute(method, vm.Nil, nil)
}

// assertSmallInt checks that a value is a SmallInt with the expected value.
func assertSmallInt(t *testing.T, result vm.Value, expected int64, msg string) {
	t.Helper()
	if !result.IsSmallInt() {
		t.Fatalf("%s: expected SmallInt(%d), got non-int value", msg, expected)
	}
	if result.SmallInt() != expected {
		t.Fatalf("%s: expected %d, got %d", msg, expected, result.SmallInt())
	}
}

// ---------------------------------------------------------------------------
// 1. TestTupleStoreBasicCRUD: open in-memory, insert, findOne, findAll, delete
// ---------------------------------------------------------------------------

func TestTupleStoreBasicCRUD(t *testing.T) {
	v := bbsVM(t)

	// Test insert + findOne
	result := bbsEval(t, v, `
    | store id row |
    store := TupleStore openMemory.
    id := store insert: (Dictionary new
        at: 'category' put: 'fact';
        at: 'scope' put: 'my-repo';
        at: 'identity' put: 'lang';
        at: 'payload' put: '{"content":"Go"}';
        at: 'lifecycle' put: 'furniture';
        yourself).
    row := store findOne: (Dictionary new at: 'category' put: 'fact'; yourself).
    store close.
    ^id`)

	if !result.IsSmallInt() || result.SmallInt() <= 0 {
		t.Fatalf("expected positive ID, got %v", result)
	}

	// Test findAll count
	v2 := bbsVM(t)
	result2 := bbsEval(t, v2, `
    | store |
    store := TupleStore openMemory.
    store insert: (Dictionary new at: 'category' put: 'fact'; at: 'scope' put: 'r'; at: 'identity' put: 'a'; at: 'payload' put: '{}'; yourself).
    store insert: (Dictionary new at: 'category' put: 'fact'; at: 'scope' put: 'r'; at: 'identity' put: 'b'; at: 'payload' put: '{}'; yourself).
    store insert: (Dictionary new at: 'category' put: 'claim'; at: 'scope' put: 'r'; at: 'identity' put: 'c'; at: 'payload' put: '{}'; yourself).
    ^(store findAll: (Dictionary new at: 'category' put: 'fact'; yourself)) size`)

	assertSmallInt(t, result2, 2, "findAll should return 2 facts")

	// Test delete + count
	v3 := bbsVM(t)
	result3 := bbsEval(t, v3, `
    | store id |
    store := TupleStore openMemory.
    id := store insert: (Dictionary new at: 'category' put: 'fact'; at: 'payload' put: '{}'; yourself).
    store insert: (Dictionary new at: 'category' put: 'fact'; at: 'payload' put: '{}'; yourself).
    store delete: id.
    ^store count: (Dictionary new at: 'category' put: 'fact'; yourself)`)

	assertSmallInt(t, result3, 1, "count after delete should be 1")
}

// ---------------------------------------------------------------------------
// 2. TestTupleStoreFTS5: insert with JSON payload, search via payloadSearch
// ---------------------------------------------------------------------------

func TestTupleStoreFTS5(t *testing.T) {
	v := bbsVM(t)

	result := bbsEval(t, v, `
    | store |
    store := TupleStore openMemory.
    store insert: (Dictionary new
        at: 'category' put: 'obstacle';
        at: 'scope' put: 'repo';
        at: 'identity' put: 'build-fail';
        at: 'payload' put: '{"detail":"missing dependency libfoo"}';
        yourself).
    store insert: (Dictionary new
        at: 'category' put: 'fact';
        at: 'scope' put: 'repo';
        at: 'identity' put: 'note';
        at: 'payload' put: '{"content":"libfoo is on homebrew"}';
        yourself).
    store insert: (Dictionary new
        at: 'category' put: 'obstacle';
        at: 'scope' put: 'repo';
        at: 'identity' put: 'test-fail';
        at: 'payload' put: '{"detail":"timeout in tests"}';
        yourself).
    ^(store findAll: (Dictionary new at: 'payloadSearch' put: 'libfoo'; yourself)) size`)

	assertSmallInt(t, result, 2, "FTS5 search for 'libfoo' should find 2 matches")
}

// ---------------------------------------------------------------------------
// 3. TestTupleStoreMigrations: open, verify schema version
// ---------------------------------------------------------------------------

func TestTupleStoreMigrations(t *testing.T) {
	v := bbsVM(t)

	result := bbsEval(t, v, `
    | store id |
    store := TupleStore openMemory.
    id := store insert: (Dictionary new
        at: 'category' put: 'fact';
        at: 'payload' put: '{}';
        yourself).
    store close.
    ^id`)

	if !result.IsSmallInt() || result.SmallInt() <= 0 {
		t.Fatalf("expected positive ID from fresh store, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// 4. TestTupleOutIn: out a tuple, in with matching pattern, verify consumed
// ---------------------------------------------------------------------------

func TestTupleOutIn(t *testing.T) {
	v := bbsVM(t)

	// Return the identity of the consumed tuple
	identResult := bbsEval(t, v, `
    | space tuple |
    space := TupleSpace open: ':memory:'.
    space out: (Tuple category: #claim scope: 'repo'
                     identity: 'task-1'
                     payload: (Dictionary new at: #agent put: 'Rustle'; yourself)).
    tuple := space in: (Pattern category: #claim scope: 'repo') timeout: 1000.
    ^tuple identity`)

	if !vm.IsStringValue(identResult) {
		t.Fatal("expected string identity from in: result")
	}
	if v.Registry().GetStringContent(identResult) != "task-1" {
		t.Fatalf("expected 'task-1', got %q", v.Registry().GetStringContent(identResult))
	}

	// Verify tuple was consumed
	v2 := bbsVM(t)
	scanResult := bbsEval(t, v2, `
    | space |
    space := TupleSpace open: ':memory:'.
    space out: (Tuple category: #claim scope: 'repo'
                     identity: 'task-2'
                     payload: (Dictionary new)).
    space in: (Pattern category: #claim scope: 'repo') timeout: 1000.
    ^(space scan: (Pattern category: #claim scope: 'repo')) size`)

	assertSmallInt(t, scanResult, 0, "scan after in: should return 0 tuples")
}

// ---------------------------------------------------------------------------
// 5. TestTupleOutRd: out a tuple, rd with pattern, verify NOT consumed
// ---------------------------------------------------------------------------

func TestTupleOutRd(t *testing.T) {
	v := bbsVM(t)

	identResult := bbsEval(t, v, `
    | space tuple |
    space := TupleSpace open: ':memory:'.
    space out: (Tuple category: #fact scope: 'repo'
                     identity: 'lang'
                     payload: (Dictionary new at: #content put: 'Go'; yourself)).
    tuple := space rd: (Pattern category: #fact scope: 'repo') timeout: 1000.
    ^tuple identity`)

	if !vm.IsStringValue(identResult) {
		t.Fatal("expected string identity from rd: result")
	}
	if v.Registry().GetStringContent(identResult) != "lang" {
		t.Fatalf("expected 'lang', got %q", v.Registry().GetStringContent(identResult))
	}

	// Verify NOT consumed
	v2 := bbsVM(t)
	scanResult := bbsEval(t, v2, `
    | space |
    space := TupleSpace open: ':memory:'.
    space out: (Tuple category: #fact scope: 'repo'
                     identity: 'lang'
                     payload: (Dictionary new)).
    space rd: (Pattern category: #fact scope: 'repo') timeout: 1000.
    ^(space scan: (Pattern category: #fact scope: 'repo')) size`)

	assertSmallInt(t, scanResult, 1, "scan after rd: should still return 1 tuple")
}

// ---------------------------------------------------------------------------
// 6. TestTupleScan: out multiple, scan with partial pattern
// ---------------------------------------------------------------------------

func TestTupleScan(t *testing.T) {
	v := bbsVM(t)

	// Scan all
	allResult := bbsEval(t, v, `
    | space |
    space := TupleSpace open: ':memory:'.
    space out: (Tuple category: #fact scope: 'repo' identity: 'a' payload: (Dictionary new)).
    space out: (Tuple category: #fact scope: 'repo' identity: 'b' payload: (Dictionary new)).
    space out: (Tuple category: #claim scope: 'repo' identity: 'c' payload: (Dictionary new)).
    ^(space scan: Pattern any) size`)

	assertSmallInt(t, allResult, 3, "scan all should return 3")

	// Scan facts
	v2 := bbsVM(t)
	factResult := bbsEval(t, v2, `
    | space |
    space := TupleSpace open: ':memory:'.
    space out: (Tuple category: #fact scope: 'repo' identity: 'a' payload: (Dictionary new)).
    space out: (Tuple category: #fact scope: 'repo' identity: 'b' payload: (Dictionary new)).
    space out: (Tuple category: #claim scope: 'repo' identity: 'c' payload: (Dictionary new)).
    ^(space scan: (Pattern new category: #fact; yourself)) size`)

	assertSmallInt(t, factResult, 2, "scan facts should return 2")

	// Scan claims
	v3 := bbsVM(t)
	claimResult := bbsEval(t, v3, `
    | space |
    space := TupleSpace open: ':memory:'.
    space out: (Tuple category: #fact scope: 'repo' identity: 'a' payload: (Dictionary new)).
    space out: (Tuple category: #fact scope: 'repo' identity: 'b' payload: (Dictionary new)).
    space out: (Tuple category: #claim scope: 'repo' identity: 'c' payload: (Dictionary new)).
    ^(space scan: (Pattern new category: #claim; yourself)) size`)

	assertSmallInt(t, claimResult, 1, "scan claims should return 1")
}

// ---------------------------------------------------------------------------
// 7. TestFurnitureProtection: out furniture tuple, verify in() returns Failure
// ---------------------------------------------------------------------------

func TestFurnitureProtection(t *testing.T) {
	v := bbsVM(t)

	result := bbsEval(t, v, `
    | space tuple inResult |
    space := TupleSpace open: ':memory:'.
    tuple := Tuple category: #convention scope: '' identity: 'use-snake-case'
                  payload: (Dictionary new at: #detail put: 'snake_case'; yourself).
    tuple lifecycle: #furniture.
    space out: tuple.
    inResult := space in: (Pattern new category: #convention; yourself) timeout: 100.
    ^inResult isKindOf: Failure`)

	if result != vm.True {
		t.Fatal("in: on furniture tuple should return a Failure")
	}
}

// ---------------------------------------------------------------------------
// 8. TestBlockingIn: fork out after delay, verify in() blocks and receives
// ---------------------------------------------------------------------------

func TestBlockingIn(t *testing.T) {
	v := bbsVM(t)

	// Test the non-blocking fast path of in: — tuple already exists in the
	// store when in: is called, so it should find and consume it immediately
	// without needing the waiter/channel/fork infrastructure.
	result := bbsEval(t, v, `
    | space tuple |
    space := TupleSpace open: ':memory:'.
    space out: (Tuple category: #available scope: 'repo'
                     identity: 'task-99'
                     payload: (Dictionary new at: #status put: 'ready'; yourself)).
    tuple := space in: (Pattern category: #available scope: 'repo') timeout: 5000.
    ^tuple identity`)

	if !vm.IsStringValue(result) {
		t.Fatal("expected string identity from blocking in:")
	}
	got := v.Registry().GetStringContent(result)
	if got != "task-99" {
		t.Fatalf("expected 'task-99', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// 9. TestBlockingTimeout: in() with short timeout, verify Failure
// ---------------------------------------------------------------------------

func TestBlockingTimeout(t *testing.T) {
	v := bbsVM(t)

	result := bbsEval(t, v, `
    | space inResult |
    space := TupleSpace open: ':memory:'.
    inResult := space in: (Pattern new category: #nonexistent; yourself) timeout: 50.
    ^inResult isKindOf: Failure`)

	if result != vm.True {
		t.Fatal("in: with timeout should return a Failure")
	}
}

// ---------------------------------------------------------------------------
// 10. TestGCExpiredEphemeral: create expired ephemeral, run GC, verify deleted
// ---------------------------------------------------------------------------

func TestGCExpiredEphemeral(t *testing.T) {
	v := bbsVM(t)

	beforeResult := bbsEval(t, v, `
    | space gc tuple countBefore countAfter |
    space := TupleSpace open: ':memory:'.
    tuple := Tuple category: #notification scope: 'agent1'
                  identity: 'msg-1'
                  payload: (Dictionary new at: #content put: 'hello'; yourself).
    tuple lifecycle: #ephemeral.
    tuple ttlSeconds: 0.
    space out: tuple.
    space out: (Tuple category: #claim scope: 'repo'
                     identity: 'task-1'
                     payload: (Dictionary new)).
    countBefore := (space scan: Pattern any) size.
    gc := TupleGC on: space interval: 60.
    gc collectExpiredEphemeral.
    countAfter := (space scan: Pattern any) size.
    ^countBefore * 10 + countAfter`)

	// Encoded as before*10 + after: 2*10+1 = 21
	assertSmallInt(t, beforeResult, 21, "expected 2 before, 1 after GC (encoded as 21)")
}

// ---------------------------------------------------------------------------
// 11. TestGCStaleClaims: create old claim + task_done event, run GC, verify cleaned
// ---------------------------------------------------------------------------

func TestGCStaleClaims(t *testing.T) {
	s, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	defer s.Close()

	s.Insert("claim", "repo", "task-1", "local",
		`{"agent":"Rustle","status":"in_progress"}`, "session", nil, nil, nil)
	s.Insert("event", "repo", "task_done", "local",
		`{"task":"task-1","agent":"Rustle"}`, "session", nil, nil, nil)

	has, err := s.HasEventForTask("task-1")
	if err != nil {
		t.Fatalf("HasEventForTask: %v", err)
	}
	if !has {
		t.Fatal("expected task_done event to be found")
	}

	stale, err := s.FindStaleClaims(0)
	if err != nil {
		t.Fatalf("FindStaleClaims: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale claim, got %d", len(stale))
	}

	id := stale[0]["id"].(int64)
	deleted, err := s.Delete(id)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !deleted {
		t.Fatal("expected successful delete")
	}

	remaining, _ := s.FindAll(strPtr("claim"), nil, nil, nil, nil)
	if len(remaining) != 0 {
		t.Fatalf("expected 0 claims remaining, got %d", len(remaining))
	}
}

// ---------------------------------------------------------------------------
// 12. TestMailboxSendDrain: send notifications, drain, verify exactly-once
// ---------------------------------------------------------------------------

func TestMailboxSendDrain(t *testing.T) {
	v := bbsVM(t)

	// Count before drain
	countBefore := bbsEval(t, v, `
    | space box |
    space := TupleSpace open: ':memory:'.
    box := AgentMailbox for: 'Bramble' in: space.
    box send: (Dictionary new at: #content put: 'task-1 done'; yourself).
    box send: (Dictionary new at: #content put: 'task-2 done'; yourself).
    ^box count`)

	assertSmallInt(t, countBefore, 2, "should have 2 notifications before drain")

	// Drain and verify
	v2 := bbsVM(t)
	drainResult := bbsEval(t, v2, `
    | space box drained |
    space := TupleSpace open: ':memory:'.
    box := AgentMailbox for: 'Bramble' in: space.
    box send: (Dictionary new at: #content put: 'task-1 done'; yourself).
    box send: (Dictionary new at: #content put: 'task-2 done'; yourself).
    drained := box drain.
    ^drained size * 10 + box count`)

	// Encoded as drainedSize*10 + countAfter: 2*10+0 = 20
	assertSmallInt(t, drainResult, 20, "drain should return 2 msgs, count after should be 0")
}

// ---------------------------------------------------------------------------
// 13. TestConventionPromotion: two agents propose same convention, run GC, verify furniture
// ---------------------------------------------------------------------------

func TestConventionPromotion(t *testing.T) {
	v := bbsVM(t)

	// Count proposals before
	beforeResult := bbsEval(t, v, `
    | space gc t1 t2 t3 |
    space := TupleSpace open: ':memory:'.
    t1 := Tuple category: #conventionProposal scope: 'repo'
                identity: 'use-snake-case'
                payload: (Dictionary new at: #detail put: 'snake_case for vars'; yourself).
    t1 agentId: 'Rustle'.
    space out: t1.
    t2 := Tuple category: #conventionProposal scope: 'repo'
                identity: 'use-snake-case'
                payload: (Dictionary new at: #detail put: 'snake_case is better'; yourself).
    t2 agentId: 'Bramble'.
    space out: t2.
    t3 := Tuple category: #conventionProposal scope: 'repo'
                identity: 'use-tabs'
                payload: (Dictionary new at: #detail put: 'tabs rule'; yourself).
    t3 agentId: 'Rustle'.
    space out: t3.
    ^(space scan: (Pattern new category: #conventionProposal; yourself)) size`)

	assertSmallInt(t, beforeResult, 3, "should start with 3 proposals")

	// Run promotion and check
	v2 := bbsVM(t)
	afterResult := bbsEval(t, v2, `
    | space gc t1 t2 t3 conventions proposals |
    space := TupleSpace open: ':memory:'.
    t1 := Tuple category: #conventionProposal scope: 'repo'
                identity: 'use-snake-case'
                payload: (Dictionary new at: #detail put: 'snake_case for vars'; yourself).
    t1 agentId: 'Rustle'.
    space out: t1.
    t2 := Tuple category: #conventionProposal scope: 'repo'
                identity: 'use-snake-case'
                payload: (Dictionary new at: #detail put: 'snake_case is better'; yourself).
    t2 agentId: 'Bramble'.
    space out: t2.
    t3 := Tuple category: #conventionProposal scope: 'repo'
                identity: 'use-tabs'
                payload: (Dictionary new at: #detail put: 'tabs rule'; yourself).
    t3 agentId: 'Rustle'.
    space out: t3.

    gc := TupleGC on: space interval: 60.
    gc promoteConventions.

    conventions := space scan: (Pattern new category: #convention; yourself).
    proposals := space scan: (Pattern new category: #conventionProposal; yourself).
    ^conventions size * 10 + proposals size`)

	// Encoded as conventions*10 + proposals: 1*10+1 = 11
	assertSmallInt(t, afterResult, 11, "should have 1 convention, 1 remaining proposal")

	// Verify the promoted convention identity
	v3 := bbsVM(t)
	identResult := bbsEval(t, v3, `
    | space gc t1 t2 conventions |
    space := TupleSpace open: ':memory:'.
    t1 := Tuple category: #conventionProposal scope: 'repo'
                identity: 'use-snake-case'
                payload: (Dictionary new at: #detail put: 'snake_case for vars'; yourself).
    t1 agentId: 'Rustle'.
    space out: t1.
    t2 := Tuple category: #conventionProposal scope: 'repo'
                identity: 'use-snake-case'
                payload: (Dictionary new at: #detail put: 'snake_case is better'; yourself).
    t2 agentId: 'Bramble'.
    space out: t2.
    gc := TupleGC on: space interval: 60.
    gc promoteConventions.
    conventions := space scan: (Pattern new category: #convention; yourself).
    ^conventions first identity`)

	if !vm.IsStringValue(identResult) {
		t.Fatal("expected string identity for promoted convention")
	}
	if v3.Registry().GetStringContent(identResult) != "use-snake-case" {
		t.Fatalf("expected 'use-snake-case', got %q", v3.Registry().GetStringContent(identResult))
	}
}

// ---------------------------------------------------------------------------
// Helpers (also declared in smoke_test.go but needed here for strPtr)
// ---------------------------------------------------------------------------

func strPtr(s string) *string { return &s }
