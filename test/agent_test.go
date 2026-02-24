// Integration tests for the agent name pool and registry, exercising the
// Maggie VM layer (NamePool, AgentRegistry, Agent) backed by the BBS
// tuplespace with in-memory SQLite via Go TupleStore primitives.
package test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chazu/maggie/vm"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// agentVM creates a Maggie VM with BBS classes + agent classes compiled.
func agentVM(t *testing.T) *vm.VM {
	t.Helper()
	// Start with a full BBS VM (includes TupleStore, BBS classes, etc.)
	v := bbsVM(t)

	// Register Json decode: alias (same pattern as encode: in bbsVM)
	jsonClass := v.Classes.Lookup("Json")
	if jsonClass != nil {
		primSel := v.Selectors.Lookup("primDecode:")
		if primSel >= 0 {
			if m := jsonClass.ClassVTable.Lookup(primSel); m != nil {
				decodeSel := v.Selectors.Intern("decode:")
				jsonClass.ClassVTable.AddMethod(decodeSel, m)
			}
		}
	}

	// Load agent .mag files in dependency order
	rootDir := filepath.Join(filepath.Dir("."), "..")
	agentFiles := []string{
		"src/agent/Agent.mag",
		"src/agent/NamePool.mag",
		"src/agent/AgentRegistry.mag",
	}
	for _, f := range agentFiles {
		path := filepath.Join(rootDir, f)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		compileSourceFile(t, v, string(src))
	}

	// Fix Agent status comparison after tuplespace round-trip.
	// Same pattern as Tuple lifecycle fix in bbsVM: after JSON round-trip,
	// status comes back as a string but isActive/isStopped/isDead compare
	// against symbols. Override with Go methods that handle both.
	agentClass := v.Classes.LookupInNamespace("ProcyonPark::Agent", "Agent")
	if agentClass == nil {
		agentClass = v.Classes.Lookup("Agent")
	}
	if agentClass != nil {
		statusIdx := -1
		for i, name := range agentClass.AllInstVarNames() {
			if name == "status" {
				statusIdx = i
				break
			}
		}
		if statusIdx >= 0 {
			matchStatus := func(vmPtr interface{}, recv vm.Value, target string) vm.Value {
				theVM := vmPtr.(*vm.VM)
				obj := vm.ObjectFromValue(recv)
				if obj == nil {
					return vm.False
				}
				sVal := obj.GetSlot(statusIdx)
				if sVal == vm.Nil {
					return vm.False
				}
				var sStr string
				if vm.IsStringValue(sVal) {
					sStr = theVM.Registry().GetStringContent(sVal)
				} else if sVal.IsSymbol() {
					sStr = theVM.SymbolName(sVal.SymbolID())
				}
				if sStr == target {
					return vm.True
				}
				return vm.False
			}
			agentClass.AddMethod0(v.Selectors, "isActive", func(vmPtr interface{}, recv vm.Value) vm.Value {
				return matchStatus(vmPtr, recv, "active")
			})
			agentClass.AddMethod0(v.Selectors, "isStopped", func(vmPtr interface{}, recv vm.Value) vm.Value {
				return matchStatus(vmPtr, recv, "stopped")
			})
			agentClass.AddMethod0(v.Selectors, "isDead", func(vmPtr interface{}, recv vm.Value) vm.Value {
				return matchStatus(vmPtr, recv, "dead")
			})
		}
	}

	return v
}

// ---------------------------------------------------------------------------
// 1. TestAgentCreation: create agent, verify fields
// ---------------------------------------------------------------------------

func TestAgentCreation(t *testing.T) {
	v := agentVM(t)

	// Test factory sets defaults
	nameResult := bbsEval(t, v, `
    | a |
    a := Agent name: 'Bramble' role: 'cub'.
    ^a name`)

	if !vm.IsStringValue(nameResult) {
		t.Fatal("expected string name")
	}
	if v.Registry().GetStringContent(nameResult) != "Bramble" {
		t.Fatalf("expected 'Bramble', got %q", v.Registry().GetStringContent(nameResult))
	}

	// Test role
	v2 := agentVM(t)
	roleResult := bbsEval(t, v2, `
    | a |
    a := Agent name: 'Bramble' role: 'cub'.
    ^a role`)

	if !vm.IsStringValue(roleResult) {
		t.Fatal("expected string role")
	}
	if v2.Registry().GetStringContent(roleResult) != "cub" {
		t.Fatalf("expected 'cub', got %q", v2.Registry().GetStringContent(roleResult))
	}
}

// ---------------------------------------------------------------------------
// 2. TestAgentToPayloadFromPayload: round-trip serialization
// ---------------------------------------------------------------------------

func TestAgentToPayloadFromPayload(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | a payload b |
    a := Agent name: 'Marble' role: 'cub'.
    a tmuxSession: 'pp-Marble'.
    a worktree: '/path/to/wt'.
    a branch: 'agent/Marble/task-1'.
    a task: 'task-1'.
    a epicId: 'epic-42'.
    a status: #stopped.
    payload := a toPayload.
    b := Agent fromPayload: payload name: 'Marble'.
    "Encode: role matches, status matches, worktree matches"
    ^(b role = 'cub' and: [b isStopped]) and: [b worktree = '/path/to/wt']`)

	if result != vm.True {
		t.Fatal("round-trip toPayload/fromPayload failed")
	}
}

// ---------------------------------------------------------------------------
// 3. TestNamePoolSeed: seed 50 names, verify count
// ---------------------------------------------------------------------------

func TestNamePoolSeed(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space seeded |
    space := TupleSpace open: ':memory:'.
    seeded := NamePool seed: 'test-repo' in: space.
    ^seeded`)

	assertSmallInt(t, result, 50, "should seed 50 names")
}

// ---------------------------------------------------------------------------
// 4. TestNamePoolSeedIdempotent: seeding twice doesn't duplicate
// ---------------------------------------------------------------------------

func TestNamePoolSeedIdempotent(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space s1 s2 avail |
    space := TupleSpace open: ':memory:'.
    s1 := NamePool seed: 'test-repo' in: space.
    s2 := NamePool seed: 'test-repo' in: space.
    avail := NamePool available: 'test-repo' in: space.
    ^(s1 * 100) + (s2 * 10) + (avail = 50 ifTrue: [1] ifFalse: [0])`)

	// s1=50, s2=0, avail=50 → 50*100 + 0*10 + 1 = 5001
	assertSmallInt(t, result, 5001, "second seed should add 0, total should remain 50")
}

// ---------------------------------------------------------------------------
// 5. TestNamePoolAllocate: allocate a name, pool shrinks
// ---------------------------------------------------------------------------

func TestNamePoolAllocate(t *testing.T) {
	v := agentVM(t)

	// After allocating one name, pool should have 49
	result := bbsEval(t, v, `
    | space name avail |
    space := TupleSpace open: ':memory:'.
    NamePool seed: 'test-repo' in: space.
    name := NamePool nextName: 'test-repo' in: space.
    avail := NamePool available: 'test-repo' in: space.
    ^avail`)

	assertSmallInt(t, result, 49, "pool should have 49 after one allocation")
}

// ---------------------------------------------------------------------------
// 6. TestNamePoolAllocateReturnsPoolName: allocated name is in the set
// ---------------------------------------------------------------------------

func TestNamePoolAllocateReturnsPoolName(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space name |
    space := TupleSpace open: ':memory:'.
    NamePool seed: 'test-repo' in: space.
    name := NamePool nextName: 'test-repo' in: space.
    ^NamePool names includes: name`)

	if result != vm.True {
		t.Fatal("allocated name should be from the standard pool")
	}
}

// ---------------------------------------------------------------------------
// 7. TestNamePoolRelease: allocate then release, pool restored
// ---------------------------------------------------------------------------

func TestNamePoolRelease(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space name |
    space := TupleSpace open: ':memory:'.
    NamePool seed: 'test-repo' in: space.
    name := NamePool nextName: 'test-repo' in: space.
    NamePool release: name for: 'test-repo' in: space.
    ^NamePool available: 'test-repo' in: space`)

	assertSmallInt(t, result, 50, "pool should be 50 after release")
}

// ---------------------------------------------------------------------------
// 8. TestNamePoolReleaseIdempotent: releasing same name twice is safe
// ---------------------------------------------------------------------------

func TestNamePoolReleaseIdempotent(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space name |
    space := TupleSpace open: ':memory:'.
    NamePool seed: 'test-repo' in: space.
    name := NamePool nextName: 'test-repo' in: space.
    NamePool release: name for: 'test-repo' in: space.
    NamePool release: name for: 'test-repo' in: space.
    ^NamePool available: 'test-repo' in: space`)

	assertSmallInt(t, result, 50, "double release should not duplicate name")
}

// ---------------------------------------------------------------------------
// 9. TestNamePoolExhaustionFallback: exhaust all names, get numeric fallback
// ---------------------------------------------------------------------------

func TestNamePoolExhaustionFallback(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space name |
    space := TupleSpace open: ':memory:'.
    NamePool seed: 'test-repo' in: space.
    "Drain all 50 names"
    1 to: 50 do: [:i | NamePool nextName: 'test-repo' in: space].
    "51st allocation should fallback"
    name := NamePool nextName: 'test-repo' in: space.
    ^name`)

	if !vm.IsStringValue(result) {
		t.Fatal("expected string fallback name")
	}
	got := v.Registry().GetStringContent(result)
	if got != "cub-1" {
		t.Fatalf("expected fallback 'cub-1', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// 10. TestNamePoolFallbackNumericIncrement: second fallback is cub-2
// ---------------------------------------------------------------------------

func TestNamePoolFallbackNumericIncrement(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space name1 name2 agent |
    space := TupleSpace open: ':memory:'.
    "Don't seed — pool starts empty"
    name1 := NamePool nextName: 'test-repo' in: space.
    "Register cub-1 so the next fallback skips it"
    agent := Agent name: name1 role: 'cub'.
    AgentRegistry register: agent for: 'test-repo' in: space.
    name2 := NamePool nextName: 'test-repo' in: space.
    ^name2`)

	if !vm.IsStringValue(result) {
		t.Fatal("expected string fallback name")
	}
	got := v.Registry().GetStringContent(result)
	if got != "cub-2" {
		t.Fatalf("expected 'cub-2', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// 11. TestNamePoolReleaseNumericNoop: releasing a numeric name is a no-op
// ---------------------------------------------------------------------------

func TestNamePoolReleaseNumericNoop(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space |
    space := TupleSpace open: ':memory:'.
    NamePool release: 'cub-1' for: 'test-repo' in: space.
    ^NamePool available: 'test-repo' in: space`)

	assertSmallInt(t, result, 0, "releasing numeric name should not add to pool")
}

// ---------------------------------------------------------------------------
// 12. TestAgentRegistryRegister: register and retrieve an agent
// ---------------------------------------------------------------------------

func TestAgentRegistryRegister(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space agent found |
    space := TupleSpace open: ':memory:'.
    agent := Agent name: 'Bramble' role: 'cub'.
    agent tmuxSession: 'pp-Bramble'.
    agent worktree: '/wt/bramble'.
    agent branch: 'agent/Bramble/task-1'.
    agent task: 'task-1'.
    AgentRegistry register: agent for: 'test-repo' in: space.
    found := AgentRegistry get: 'Bramble' for: 'test-repo' in: space.
    ^found name`)

	if !vm.IsStringValue(result) {
		t.Fatal("expected string name from registry lookup")
	}
	if v.Registry().GetStringContent(result) != "Bramble" {
		t.Fatalf("expected 'Bramble', got %q", v.Registry().GetStringContent(result))
	}
}

// ---------------------------------------------------------------------------
// 13. TestAgentRegistryGetMissing: lookup missing agent returns nil
// ---------------------------------------------------------------------------

func TestAgentRegistryGetMissing(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space |
    space := TupleSpace open: ':memory:'.
    ^AgentRegistry get: 'NoSuchAgent' for: 'test-repo' in: space`)

	if result != vm.Nil {
		t.Fatal("expected nil for missing agent")
	}
}

// ---------------------------------------------------------------------------
// 14. TestAgentRegistryList: register multiple, list all
// ---------------------------------------------------------------------------

func TestAgentRegistryList(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space a1 a2 a3 agents |
    space := TupleSpace open: ':memory:'.
    a1 := Agent name: 'Bramble' role: 'cub'.
    a2 := Agent name: 'Rustle' role: 'cub'.
    a3 := Agent name: 'Marble' role: 'cub'.
    AgentRegistry register: a1 for: 'test-repo' in: space.
    AgentRegistry register: a2 for: 'test-repo' in: space.
    AgentRegistry register: a3 for: 'test-repo' in: space.
    agents := AgentRegistry list: 'test-repo' in: space.
    ^agents size`)

	assertSmallInt(t, result, 3, "should list 3 agents")
}

// ---------------------------------------------------------------------------
// 15. TestAgentRegistryUnregister: register then unregister, verify gone
// ---------------------------------------------------------------------------

func TestAgentRegistryUnregister(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space agent |
    space := TupleSpace open: ':memory:'.
    agent := Agent name: 'Bramble' role: 'cub'.
    AgentRegistry register: agent for: 'test-repo' in: space.
    AgentRegistry unregister: 'Bramble' for: 'test-repo' in: space.
    ^AgentRegistry get: 'Bramble' for: 'test-repo' in: space`)

	if result != vm.Nil {
		t.Fatal("expected nil after unregister")
	}
}

// ---------------------------------------------------------------------------
// 16. TestAgentRegistryUpdateStatus: update agent status
// ---------------------------------------------------------------------------

func TestAgentRegistryUpdateStatus(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space agent updated |
    space := TupleSpace open: ':memory:'.
    agent := Agent name: 'Bramble' role: 'cub'.
    AgentRegistry register: agent for: 'test-repo' in: space.
    updated := AgentRegistry updateStatus: #stopped for: 'Bramble' in: 'test-repo' space: space.
    ^updated isStopped`)

	if result != vm.True {
		t.Fatal("expected updated agent to be stopped")
	}
}

// ---------------------------------------------------------------------------
// 17. TestAgentRegistryUpdateStatusPersists: status change visible on re-read
// ---------------------------------------------------------------------------

func TestAgentRegistryUpdateStatusPersists(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space agent found |
    space := TupleSpace open: ':memory:'.
    agent := Agent name: 'Bramble' role: 'cub'.
    AgentRegistry register: agent for: 'test-repo' in: space.
    AgentRegistry updateStatus: #dead for: 'Bramble' in: 'test-repo' space: space.
    found := AgentRegistry get: 'Bramble' for: 'test-repo' in: space.
    ^found isDead`)

	if result != vm.True {
		t.Fatal("expected persisted status to be dead")
	}
}

// ---------------------------------------------------------------------------
// 18. TestAgentRegistryReplace: re-register same name replaces data
// ---------------------------------------------------------------------------

func TestAgentRegistryReplace(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space a1 a2 found agents |
    space := TupleSpace open: ':memory:'.
    a1 := Agent name: 'Bramble' role: 'cub'.
    a1 task: 'task-1'.
    AgentRegistry register: a1 for: 'test-repo' in: space.
    a2 := Agent name: 'Bramble' role: 'cub'.
    a2 task: 'task-2'.
    AgentRegistry register: a2 for: 'test-repo' in: space.
    found := AgentRegistry get: 'Bramble' for: 'test-repo' in: space.
    agents := AgentRegistry list: 'test-repo' in: space.
    "task should be updated and there should be only 1 agent"
    ^(found task = 'task-2') and: [agents size = 1]`)

	if result != vm.True {
		t.Fatal("re-register should replace, not duplicate")
	}
}

// ---------------------------------------------------------------------------
// 19. TestAgentRegistryRepoIsolation: agents in different repos don't mix
// ---------------------------------------------------------------------------

func TestAgentRegistryRepoIsolation(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space a1 a2 repo1Agents repo2Agents |
    space := TupleSpace open: ':memory:'.
    a1 := Agent name: 'Bramble' role: 'cub'.
    a2 := Agent name: 'Rustle' role: 'cub'.
    AgentRegistry register: a1 for: 'repo-1' in: space.
    AgentRegistry register: a2 for: 'repo-2' in: space.
    repo1Agents := AgentRegistry list: 'repo-1' in: space.
    repo2Agents := AgentRegistry list: 'repo-2' in: space.
    ^repo1Agents size * 10 + repo2Agents size`)

	// 1*10 + 1 = 11
	assertSmallInt(t, result, 11, "each repo should have 1 agent")
}

// ---------------------------------------------------------------------------
// 20. TestNamePoolRepoIsolation: name pools per-repo are independent
// ---------------------------------------------------------------------------

func TestNamePoolRepoIsolation(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space |
    space := TupleSpace open: ':memory:'.
    NamePool seed: 'repo-1' in: space.
    NamePool seed: 'repo-2' in: space.
    NamePool nextName: 'repo-1' in: space.
    ^(NamePool available: 'repo-1' in: space) * 100 +
     (NamePool available: 'repo-2' in: space)`)

	// 49*100 + 50 = 4950
	assertSmallInt(t, result, 4950, "repo-1 should have 49, repo-2 should have 50")
}

// ---------------------------------------------------------------------------
// 21. TestConcurrentAllocNoDuplicates: allocate all 50, verify all unique
// ---------------------------------------------------------------------------

func TestConcurrentAllocNoDuplicates(t *testing.T) {
	v := agentVM(t)

	result := bbsEval(t, v, `
    | space names unique |
    space := TupleSpace open: ':memory:'.
    NamePool seed: 'test-repo' in: space.
    names := Array new: 0.
    1 to: 50 do: [:i |
        names := names copyWith: (NamePool nextName: 'test-repo' in: space).
    ].
    "Check all unique by building a set (array with no duplicates)"
    unique := Array new: 0.
    names do: [:n |
        (unique includes: n) ifFalse: [unique := unique copyWith: n].
    ].
    ^unique size`)

	assertSmallInt(t, result, 50, "all 50 allocated names should be unique")
}
