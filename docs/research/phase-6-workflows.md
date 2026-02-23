# Phase 6 Research: Workflow Engine

## Overview

The imp-castle workflow engine provides automated, sequential execution of multi-step agent orchestration tasks. Workflows are defined in CUE, loaded and validated at parse time, resolved with runtime parameters, and executed step-by-step by a daemon-hosted executor. The engine handles agent spawning, output collection, output validation, agent dismissal, and human/timer gates — all with timeout handling, crash recovery, and telemetry integration.

**Source**: `internal/workflow/` (types, loader, executor, state) and `internal/workflow/steps/` (spawn, wait, evaluate, dismiss, gate).

---

## 1. CUE Workflow Definition Loading and Validation

### Schema Location

The CUE schema is embedded into the binary via `//go:embed schema.cue` in `loader.go`. The schema defines `#Workflow`, `#Step` (union type), `#Aspect`, `#AspectMatch`, `#Param`, and all step-specific definitions (`#SpawnStep`, `#WaitStep`, `#EvaluateStep`, `#DismissStep`, `#GateStep`).

### Definition Resolution (Two Directories)

Workflow `.cue` files are resolved from two locations, repo-specific taking precedence:

1. `<repo>/.imp-castle/workflows/<name>.cue` (repo-specific)
2. `~/.imp-castle/workflows/<name>.cue` (global)

Resolution happens in `resolveWorkflowPath()` — it checks repo first, then global.

### Two-Phase Loading

Loading follows a **parse-then-resolve** pattern:

**Phase 1 — Parse** (`parseWorkflowFromFile`):
- Reads the `.cue` file
- Prepends `_input: [string]: _` and `_ctx: [string]: _` stubs so that `_input.paramName` and `_ctx.fieldName` references compile without concrete values
- Uses `cue/load` for module-aware loading (walks up to find `cue.mod/`)
- Validates structure against `#Workflow` schema (relaxed: name must be concrete, steps must exist, but param values can be non-concrete)
- Extracts param declarations, name, description, step count
- Returns a `ParsedWorkflow` holding the raw CUE bytes for later resolution

**Phase 2 — Resolve** (`ResolveWorkflow`):
- Takes the `ParsedWorkflow` and actual `map[string]string` parameter values
- Builds concrete CUE `_input: { paramName: "value" }` source
- Re-compiles the workflow file with concrete input values
- Validates with `cue.Concrete(true)` — all values must now be fully resolved
- Unifies against `#Workflow` schema
- Converts CUE value to Go `Workflow` struct via JSON marshaling
- Applies aspect expansion as a final transformation

### CUE Module Infrastructure

`EnsureModuleInfrastructure()` creates the CUE module scaffolding in a workflows directory:
- `cue.mod/module.cue` (module declaration)
- `tasks/tasks.cue` (shared `#CommonTask` and `#StandardSteps` definitions)
- `aspects/aspects.cue` (shared `#Timeout` and `#Retry` definitions)

### Context Variables

Two hidden CUE fields are available in workflow definitions:

- **`_input`**: Runtime parameters passed via `--param key=value`. Declared in `params` section, referenced as `_input.paramName` in step configs.
- **`_ctx`**: Implicit workflow context populated by step execution. Updated as steps complete. Contains:
  - `_ctx.taskId` — primary task ID (set by first spawn step or from params)
  - `_ctx.activeAgent` — most recently spawned agent name
  - `_ctx.activeBranch` — most recently created branch
  - `_ctx.activeRepo` — repository the workflow runs in
  - `_ctx.previousOutput` — JSON output from the last wait step

`_ctx` references in step configs are resolved at execution time via `ResolveStepConfig()`, which re-parses the CUE config with current context values injected.

---

## 2. State Machine Executor (Maggie Context)

### Executor Architecture

The `Executor` struct (`executor.go`) is the central runtime component:

```go
type Executor struct {
    store              store.Store           // Persistent KV store (Badger)
    handlers           map[string]StepHandler // Step type -> handler registry
    telemetry          *telemetry.TelemetryStore
    nodeID             string
    notifyFn           NotifyFunc            // Failure notification callback
    killFn             CancelKillFunc        // Agent dismissal function
    livenessCheck      AgentLivenessChecker  // Tmux session liveness check
    completionNotifier CompletionNotifier    // Workflow complete/fail notification
    mu                 sync.Mutex
    running            map[string]context.CancelFunc  // Active instance cancellation
}
```

The executor is configured via functional options (`WithTelemetry`, `WithNotifyFunc`, `WithCancelKillFn`, etc.) and created with `NewExecutor(store, handlers, ...opts)`.

### Execution Loop

`Run()` orchestrates workflow execution:

1. **Parse** the workflow (Phase 1 — validates CUE, extracts params)
2. **Validate params** — reject unknown params, apply defaults, check required
3. **Resolve** the workflow (Phase 2 — CUE interpolation with actual values)
4. **Create instance** — generate `wf-<random>` ID, persist to store
5. **Launch goroutine** — `executeLoop()` runs steps sequentially

`executeLoopFrom()` is the core loop:
- Iterates steps from `startFrom` to end
- Checks for cancellation before each step
- Records `StepResult` (status: running) and persists
- Looks up handler by step type from `handlers` map
- Resolves `_ctx` references in step config via `ResolveStepConfig()`
- Applies per-step timeout if specified (wraps context with deadline)
- Calls `handler.Execute(ctx, instance, stepIndex, resolvedConfig)`
- On success: marks step completed, persists
- On failure: calls `failInstance()` — marks step + instance failed, emits telemetry, sends notification
- On context cancellation: returns silently (Cancel() handles status)
- After all steps: marks instance completed, emits telemetry, sends completion notification

### Instance State Machine

```
pending → running → completed
                  → failed
                  → cancelled
```

Instance status is tracked in the `Instance` struct and persisted with a status index for efficient filtering. The `state.go` module manages persistence via a KV store with two buckets:
- `workflow` — instance data keyed by `repoName/instanceID`
- `workflow-idx` — status index keyed by `status/repoName/status/instanceID`

### StepHandler Interface

```go
type StepHandler interface {
    Execute(ctx context.Context, instance *Instance, stepIndex int, config json.RawMessage) (*StepResult, error)
}
```

Handlers receive the full instance (for context access and mutation), the step index, and the resolved config as raw JSON. They return a `StepResult` with output or an error.

---

## 3. Step Types

### 3.1 Spawn (`steps/spawn.go`)

**Purpose**: Creates a new agent (cub, king, reviewer, etc.) with a dedicated worktree and branch.

**Config** (`SpawnConfig`):
- `role` — agent role (default: "cub")
- `task` — `TaskDef` with `title`, optional `description`, `taskType` (task/feature/bug)
- `repo` — optional repo override
- `branch` — optional base branch for worktree

**Execution**:
1. Creates a beads task via `worktracker.CreateTask()`
2. Calls `SpawnFn(ctx, role, taskID, repoName, repoRoot, baseBranch, workflowID)`
3. Generates branch name: `agent/{agentName}/{sanitizedTaskID}`
4. Updates `instance.Context`: sets `ActiveAgent`, `ActiveBranch`, `ActiveRepo`, and `TaskID` (on first spawn)
5. Returns output: `{agentName, taskID, branchName}`

**Error handling**: On spawn failure, cleans up the orphaned task.

### 3.2 Wait (`steps/wait.go`)

**Purpose**: Polls for output messages from the most recently spawned agent.

**Config** (`WaitConfig`):
- `timeout` — max wait time (default: "10m")

**Execution**:
1. Resolves agent name from most recent spawn step's output (walks step results backward)
2. Polls message store every 5s for messages of type `output` addressed to `daemon` from the agent
3. On message found: sets `instance.Context.PreviousOutput` to message data, returns output
4. On timeout: returns error

### 3.3 Evaluate (`steps/evaluate.go`)

**Purpose**: Validates the previous wait step's output using CUE unification.

**Config** (`EvaluateConfig`):
- `expect` — CUE value defining expected output structure (e.g., `{exitCode: 0}`)

**Execution**:
1. Finds the most recent wait step with output (scans backward — handles aspect injection)
2. Compiles expected value and actual output as CUE values
3. Unifies expected with actual — checks concrete validity
4. Checks subsumption: actual must contain all expected fields
5. If either check fails: returns detailed error with both expected and actual values

### 3.4 Dismiss (`steps/dismiss.go`)

**Purpose**: Terminates the most recently spawned agent and optionally merges their work.

**Config** (anonymous struct):
- `noMerge` — if true, skip merge (work stays on feature branch)

**Execution**:
1. Resolves agent name from step results
2. Calls `KillFn(ctx, repoName, agentName, noMerge)`
3. Updates context: clears `ActiveAgent`; clears `ActiveBranch` only if merge happened
4. Merge conflicts are treated as non-fatal (returns completed with warning)
5. Other dismiss errors are also non-fatal (agent may already be gone)

### 3.5 Gate (`steps/gate.go`)

Two variants:

**Human Gate** (`gateType: "human"`):
- Sends approval prompt messages to specified approvers
- Polls for `workflow_approve` or `workflow_reject` command messages
- Default timeout: 30m
- Uses persistent `gateState` for crash recovery (tracks `startedAt`, whether prompt was sent)
- On approve: returns `{gate: "approved", approvedBy: "..."}`
- On reject: returns error with reason
- CLI commands: `cub workflow approve <id>`, `cub workflow reject <id> --reason "..."`

**Timer Gate** (`gateType: "timer"`):
- Pauses for specified duration (e.g., "5m", "1h")
- Uses persistent `gateState` for crash recovery (calculates remaining time from original start)
- Returns `{gate: "timer_elapsed", duration: "..."}` on completion

---

## 4. Aspects (Cross-Cutting Step Injection)

### Concept

Aspects are a compile-time transformation that injects steps before and/or after matching steps in a workflow. They enable cross-cutting concerns (logging, notifications, health checks) without modifying individual step definitions.

### Schema

```cue
#Aspect: {
    match: #AspectMatch
    before?: [...#Step]
    after?: [...#Step]
}

#AspectMatch: {
    type?: string  // match by step type
    name?: string  // glob pattern against task title (spawn) or step type
    role?: string  // match spawn steps by agent role
}
```

### Matching Semantics

- All non-empty fields use **AND** semantics (all must match)
- `type` matches the step type string ("spawn", "wait", "evaluate", "dismiss", "gate")
- `name` uses `filepath.Match` glob patterns — for spawn steps matches against `task.title`, for others matches the step type
- `role` only applies to spawn steps; non-spawn steps never match if role is set

### Expansion Algorithm (`expandAspects`)

Applied at load time (after CUE → Go conversion), as a single-pass per-aspect transformation:

```
for each aspect in declaration order:
    for each step in current list:
        if step matches aspect:
            inject: [...before, step, ...after]
        else:
            keep step as-is
```

- First aspect declared is innermost (wrapped closest to original step)
- Injected steps are NOT re-matched by subsequent aspects in the same expansion
- Aspect-injected steps don't inherit the original step's timeout

### Example

```cue
aspects: [{
    match: {type: "spawn"}
    before: [{type: "gate", gateType: "human", approvers: ["admin"]}]
    after:  [{type: "wait", timeout: "15m"}, {type: "evaluate", expect: {exitCode: 0}}]
}]
```

This wraps every spawn step with an approval gate before and a wait+evaluate after.

---

## 5. Workflow Instance Tracking

### Instance Struct

```go
type Instance struct {
    ID           string            // "wf-<random hex>"
    WorkflowName string
    RepoName     string
    RepoRoot     string            // Filesystem path for workflow resolution
    Status       InstanceStatus    // pending|running|completed|failed|cancelled
    CurrentStep  int               // Index of step being executed
    Params       map[string]string // Resolved parameter values
    Context      WorkflowContext   // Implicit context (activeAgent, etc.)
    StepResults  []StepResult      // Per-step execution results
    Error        string            // Overall error message (if failed)
    StartedAt    time.Time
    CompletedAt  *time.Time
}
```

### Persistence

Instances are stored in a Badger KV store with:
- **Primary bucket** (`workflow`): `repoName/instanceID` → JSON-serialized Instance
- **Index bucket** (`workflow-idx`): `status/repoName/status/instanceID` → empty value

Status index is maintained on every `UpdateInstance()` call — old index entry deleted, new one written in a transaction.

### CLI Management

- `cub workflow list [--repo] [--status]` — list instances
- `cub workflow status <id> [--repo]` — show instance details
- `cub workflow cancel <id>` — cancel running instance (dismisses active agent)
- `cub workflow definitions` — list available workflow definitions

---

## 6. Approval Gates

Human gates implement a message-based approval flow:

1. **Prompt**: System sends chat messages to each approver with approve/reject instructions
2. **Poll**: Gate handler polls for command messages to daemon with matching `WorkflowID`
3. **Approve**: `cub workflow approve <id>` sends a `workflow_approve` command message
4. **Reject**: `cub workflow reject <id> --reason "..."` sends a `workflow_reject` command message
5. **Timeout**: Default 30m, configurable per-gate

Gate state is persisted separately (in `gate-state` bucket) for crash recovery — if the daemon restarts mid-gate, it resumes from where it left off without re-sending prompts.

---

## 7. Timeout Handling

Timeouts operate at two levels:

### Step-Level Timeout

Each step definition can include a `timeout` field (e.g., `"30s"`, `"5m"`). The executor wraps the context with `context.WithTimeout()` before calling the handler. If the step exceeds the timeout, the handler receives a cancelled context, and the executor reports "step timed out after X".

### Step-Internal Timeout

Some steps have their own internal timeout handling:
- **Wait**: `timeout` in `WaitConfig` (default "10m") — separate from the step-level timeout. The handler polls until this deadline.
- **Human Gate**: `timeout` in `GateConfig` (default "30m") — uses gate-state's `StartedAt` for crash-recovery-aware deadline.
- **Timer Gate**: `duration` in `GateConfig` — the actual delay duration, not a timeout per se.

Step-level and step-internal timeouts are independent. The step-level timeout is an absolute cap on execution time; the internal timeout is the handler's own logic.

---

## 8. Crash Recovery

The executor implements crash recovery in `RecoverOnStartup()`:

1. Lists all instances with `StatusRunning` for the repo
2. If an `AgentLivenessChecker` is configured:
   - Extracts agent name from most recent spawn step output
   - Checks if agent is still alive (tmux session exists)
   - **Alive**: Reloads workflow definition, trims incomplete step result, resumes from `CurrentStep`
   - **Dead**: Marks instance as failed with "daemon restarted"
3. If no liveness checker: marks all running instances as failed (legacy behavior)

Gate handlers also implement their own recovery:
- `gateState` is persisted independently, tracking `startedAt`, `state`, and `promptSent`
- On restart, the gate resumes polling without re-sending prompts
- Timer gates calculate remaining time from the original start

---

## 9. Telemetry Integration

The executor emits telemetry events for workflow lifecycle transitions:
- `workflow_started` — when a new instance begins
- `workflow_completed` — when all steps finish successfully
- `workflow_failed` — when a step fails
- `workflow_cancelled` — when explicitly cancelled

Events use `telemetry.CategoryWorkflow` and include workflow name, repo name, instance ID, and status. The completion notifier sends structured `WorkflowCompleteData` to the king.

---

## 10. Future Direction: Smalltalk-Native Workflow DSL

### Why Consider an Alternative to CUE?

CUE is excellent for type-safe configuration and validation. However, Procyon Park's Smalltalk-inspired architecture suggests a potential alternative that could feel more native to the system:

### What a Smalltalk-Style DSL Might Look Like

```smalltalk
"A workflow as a message-passing sequence"
Workflow new: 'build-and-check'
    param: #taskTitle type: #string required: true;
    spawn: (Agent cub task: (Task title: _input taskTitle));
    waitFor: #output timeout: 10 minutes;
    evaluate: [:output | output exitCode = 0];
    dismiss.

"Aspects as method wrapping (à la doesNotUnderstand: / method wrappers)"
Workflow aspect: 'logging'
    matching: [:step | step isSpawn]
    before: [Workflow gate: (Gate human approvers: #('admin'))].
```

### Key Design Considerations

1. **Message-passing as execution model**: Steps could be messages sent to a workflow object, naturally fitting Smalltalk's everything-is-a-message philosophy.

2. **Blocks as predicates/validators**: The evaluate step maps naturally to Smalltalk blocks — `[:output | output exitCode = 0]` is more expressive than CUE unification for complex validation.

3. **Method wrappers for aspects**: Smalltalk's existing method-wrapping facilities (used in profiling, debugging) could serve as the aspect mechanism, making it a language-level feature rather than a load-time transformation.

4. **Live objects vs. static definitions**: Workflow instances could be live Smalltalk objects with inspectable state, rather than JSON serialized to a KV store. This aligns with Smalltalk's image-based persistence.

5. **Trade-offs**:
   - **Pro**: Tighter integration with the Maggie runtime, more expressive, live debugging
   - **Pro**: Blocks provide Turing-complete evaluation (vs. CUE's intentional constraints)
   - **Con**: Loses CUE's built-in validation guarantees and type safety
   - **Con**: Less portable — CUE definitions are language-agnostic config files
   - **Con**: Security implications of Turing-complete workflow definitions

### Practical Path Forward

A hybrid approach might work best:
- Keep CUE for workflow **definition** and **validation** (the schema layer)
- Use Smalltalk for workflow **execution** (the runtime layer)
- The CUE definition compiles to Smalltalk workflow objects at load time
- This preserves CUE's validation while gaining Smalltalk's runtime expressiveness

---

## Architecture Summary

```
┌─────────────────────────────────────────────────────┐
│                    CLI Layer                          │
│  cub workflow run/list/status/cancel/approve/reject   │
└────────────────────────┬────────────────────────────┘
                         │ (via daemon RPC)
┌────────────────────────▼────────────────────────────┐
│                   Executor                           │
│  - Run(): parse → validate → resolve → launch loop   │
│  - executeLoop(): sequential step dispatch            │
│  - Cancel(): cancel context + dismiss agent           │
│  - RecoverOnStartup(): liveness check + resume        │
└────────────────────────┬────────────────────────────┘
                         │ (handler dispatch)
┌────────────────────────▼────────────────────────────┐
│              Step Handlers                            │
│  ┌─────────┐ ┌──────┐ ┌──────────┐ ┌───────┐ ┌────┐│
│  │ Spawn   │ │ Wait │ │ Evaluate │ │Dismiss│ │Gate││
│  │ (agent) │ │(poll)│ │  (CUE)   │ │(kill) │ │(h/t)│
│  └─────────┘ └──────┘ └──────────┘ └───────┘ └────┘│
└────────────────────────┬────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────┐
│               Loader (CUE)                           │
│  - Schema embedding (schema.cue)                     │
│  - Two-phase: parse (stubs) → resolve (concrete)     │
│  - _input / _ctx variable injection                   │
│  - Aspect expansion (load-time transformation)        │
│  - Module-aware CUE loading                           │
└─────────────────────────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────┐
│            State / Persistence                       │
│  - Badger KV: workflow + workflow-idx buckets         │
│  - Gate state: separate bucket for crash recovery    │
│  - Instance lifecycle: pending→running→completed     │
│  - Status index for filtered queries                 │
└─────────────────────────────────────────────────────┘
```

## Key Reimplementation Notes for Procyon Park

1. **CUE dependency is substantial** — the loader relies heavily on `cuelang.org/go` for schema compilation, unification, and validation. Procyon Park will need to decide whether to use CUE as a Go library, shell out to the `cue` binary, or implement a subset.

2. **The StepHandler interface is clean and extensible** — new step types can be added by implementing `Execute(ctx, instance, stepIndex, config)`. This should transfer well to any architecture.

3. **Aspect expansion is a pure function** — `expandAspects(steps, aspects) → steps`. It operates at load time with no runtime state. Simple to reimplement.

4. **The executor is tightly coupled to the daemon** — it runs as a goroutine within the daemon process, using in-process function calls for spawn/dismiss. A Smalltalk version might use message passing between objects instead.

5. **Crash recovery depends on persistent state** — both instance state and gate state must survive daemon restarts. The KV store + liveness checker pattern is essential.

6. **The _ctx mechanism is elegant** — CUE re-compilation at each step allows context-dependent step configs without a separate template language. A Smalltalk reimplementation could use live object references instead.
