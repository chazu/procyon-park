---
title: "Procyon Park MVP: Agent Orchestration System"
type: feat
date: 2026-03-15
---

# Procyon Park MVP: Agent Orchestration System

## Overview

Implement the core Procyon Park agent orchestration system in Maggie. The MVP enables a local user to orchestrate swarms of AI coding agents across a single repository, coordinated through a linear tuplespace with Petri net workflow execution, an HTTP API, and a CLI tool (`pp`) for agent-system communication.

First harness target: Claude Code.

## Problem Statement

AI coding agents today work in isolation. Coordinating multiple agents on a shared codebase — with proper quality control, parallelism, and resource tracking — requires a coordination fabric that enforces invariants (linear logic), gates execution on world state (CUE constraints), and routes work through structured pipelines (Petri net workflows).

## Proposed Solution

A single Maggie process running:
1. An in-memory tuplespace with linear logic modalities
2. An HTTP API for agent communication
3. A dispatcher loop that advances workflows and evaluates rules
4. A `pp` CLI that agents use to communicate back
5. Six agent roles with structured priming for quality-controlled parallel work

## Technical Approach

### Architecture

```
                    ┌─────────────────────────────┐
                    │     Procyon Park Process     │
                    │                              │
                    │  ┌────────┐  ┌───────────┐  │
  pp CLI ──HTTP──▶  │  │  API   │──│    BBS    │  │
                    │  │ Server │  │(TupleSpace│  │
  User Agent ─HTTP─▶│  └────────┘  │ + CUE)    │  │
                    │       │      └───────────┘  │
                    │  ┌────▼─────┐      │        │
                    │  │Dispatcher│──────-┘        │
                    │  │  Loop    │                │
                    │  └──────────┘                │
                    └─────────────────────────────┘
                           │
                    ┌──────▼──────┐
                    │ Agent Slots │
                    │ (Claude via │
                    │ ExternalPro │
                    │    cess)    │
                    └─────────────┘
```

All components run as concurrent processes within a single `mag` instance, sharing an in-memory TupleSpace. No Redis, no K8s — pure Maggie for MVP.

### File Layout

```
src/
  Main.mag              — entry point, wires everything together
  bbs/
    Tuple.mag           — tuple data model
    BBS.mag             — tuplespace wrapper with typed operations
    Categories.mag      — category constants and validation
  api/
    Server.mag          — HTTP API server
    Routes.mag          — route handlers
  dispatcher/
    Dispatcher.mag      — tick loop, workflow advancement
    WorkflowEngine.mag  — workflow instantiation and token management
    RuleEngine.mag      — reactive rule evaluation
    TaskDispatcher.mag  — match tasks to agent slots
  cli/
    PP.mag              — pp CLI entry point
    Commands.mag        — command implementations
  roles/
    Role.mag            — base role with priming protocol
    Scout.mag           — research role
    Planner.mag         — planning role
    Implementer.mag     — implementation role
    Reviewer.mag        — code review role
    Tester.mag          — testing role
    Fixer.mag           — fixup role
  harness/
    Harness.mag         — base harness protocol
    ClaudeHarness.mag   — Claude Code harness via ExternalProcess
```

### Implementation Phases

---

#### Phase 1: Tuplespace Layer (`src/bbs/`)

The foundation. Everything else builds on this.

##### Tuple.mag — Tuple Data Model

A Tuple is a Dictionary-backed object with structured fields:

```maggie
Tuple subclass: Object
  instanceVariableNames: 'id category scope identity payload pinned modality createdAt ttl'
```

Key behaviors:
- `id` — auto-incrementing integer, assigned by BBS on `out:`
- `category` — one of the valid categories (string)
- `scope` — namespace string (e.g., repo path or "system")
- `identity` — unique identifier within category+scope
- `payload` — Dictionary of arbitrary data
- `pinned` — boolean, if true the tuple is persistent (never consumed by `in:`)
- `modality` — `#linear`, `#persistent`, or `#affine`
- `createdAt` — DateTime when written
- `ttl` — for affine tuples, seconds until expiry (0 = no expiry)
- `isExpired` — checks if affine tuple has passed its TTL
- `asDictionary` — serialize to Dictionary for JSON transport
- `fromDictionary:` — class method to deserialize

##### Categories.mag — Category Registry

```maggie
Categories subclass: Object
```

Class-side method `valid` returns the Array of valid category strings. Class-side method `isValid:` checks membership. Categories:

`fact`, `convention`, `observation`, `decision`, `signal`, `task`, `template`, `workflow`, `token`, `rule`, `ingestion`, `event`, `obstacle`, `session`, `artifact`, `link`, `notification`

##### BBS.mag — Tuplespace Wrapper

The core coordination fabric. Wraps Maggie's TupleSpace with the tuple taxonomy and linear logic semantics.

```maggie
BBS subclass: Object
  instanceVariableNames: 'space nextId mutex cueCtx'
```

**Internal storage:** Each tuple is stored in the underlying TupleSpace as a Dictionary. The BBS uses CUE template matching for retrieval — it compiles match templates from category+scope+identity and uses the TupleSpace's CUE-based matching.

**Key methods:**

| Method | Behavior |
|---|---|
| `out:scope:identity:payload:` | Write linear tuple. Validates category. Assigns ID + timestamp. |
| `outPinned:scope:identity:payload:` | Write persistent tuple (pinned=true, modality=#persistent). |
| `outAffine:scope:identity:payload:ttl:` | Write affine tuple with TTL in seconds. |
| `inp:scope:identity:` | Non-blocking consume. Returns Tuple or nil. Respects pinned (returns copy, doesn't consume). |
| `in:scope:identity:` | Blocking consume. Suspends until match found. |
| `rdp:scope:identity:` | Non-blocking read (no consume). Returns Tuple or nil. |
| `rd:scope:identity:` | Blocking read. |
| `scan:scope:` | Return all tuples matching category + scope. |
| `scanAll:` | Return all tuples in a category (any scope). |
| `upsertSignal:scope:identity:payload:` | `inp:` then `outPinned:` — atomic-enough for signals. |
| `size` | Total tuple count. |
| `expireAffine` | Scan and remove expired affine tuples. Called by dispatcher. |

**CUE template matching strategy:** For each operation, BBS constructs a CUE template string like `{category: "task", scope: "myrepo", identity: "build-123"}` and uses TupleSpace's native CUE matching. For `scan:`, it uses a partial template `{category: "task", scope: "myrepo"}` to match all tuples in that category+scope.

**ID generation:** Simple atomic counter protected by Mutex. `nextId` increments on each `out:`.

**Acceptance Criteria:**
- [ ] `Tuple.mag` — data model with serialization to/from Dictionary
- [ ] `Categories.mag` — valid category list with validation
- [ ] `BBS.mag` — all operations listed above working correctly
- [ ] Linear tuples consumed exactly once by `inp:`/`in:`
- [ ] Persistent tuples returned by `inp:` but NOT consumed
- [ ] Affine tuples expire after TTL
- [ ] CUE template matching works for exact and partial matches
- [ ] Thread-safe via Mutex on ID generation

---

#### Phase 2: HTTP API (`src/api/`)

Expose the BBS over HTTP so agents (via `pp`) and users (via their Claude session) can interact with the tuplespace.

##### Server.mag — HTTP Server

```maggie
Server subclass: Object
  instanceVariableNames: 'httpServer bbs port'
```

Wraps Maggie's HttpServer. Initialized with a BBS instance and port. Registers all routes on `start`.

##### Routes.mag — Route Handlers

All routes accept and return JSON. Request bodies are parsed with `Json decode:`, responses built with `Json encode:` and `HttpResponse new:body:`.

**Core tuplespace operations:**

| Endpoint | Method | Request Body | Response |
|---|---|---|---|
| `/api/out` | POST | `{category, scope, identity, payload, pinned?, ttl?}` | `{id, status: "ok"}` |
| `/api/inp` | POST | `{category, scope, identity}` | `{tuple: {...}}` or `{tuple: null}` |
| `/api/rdp` | POST | `{category, scope, identity}` | `{tuple: {...}}` or `{tuple: null}` |
| `/api/scan` | POST | `{category, scope?}` | `{tuples: [...]}` |
| `/api/health` | GET | — | `{status: "ok", tuples: N, uptime: S}` |

**Convenience endpoints (sugar over core ops):**

| Endpoint | Method | Body | Maps to |
|---|---|---|---|
| `/api/observe` | POST | `{scope, identity, detail, tags?, task?}` | `out("observation", ...)` |
| `/api/decide` | POST | `{scope, identity, detail, rationale?, task?}` | `out("decision", ...)` |
| `/api/event` | POST | `{scope, identity, type, summary?, payload?}` | `out("event", ...)` |
| `/api/notify` | POST | `{message, scope?, severity?}` | `out("notification", ...)` |
| `/api/dismiss` | POST | `{task_id, scope}` | `out("event", scope, "task-complete:<task_id>", ...)` |
| `/api/notifications` | GET | query: `?since=<epoch>` | `scan("notification", "system")` filtered by time |
| `/api/plan` | POST | `{scope, identity, subtasks: [...]}` | Writes plan as decision + task tuples |

**Error handling:** All endpoints return `{error: "message"}` with appropriate HTTP status on failure. Category validation, missing fields, JSON parse errors all produce 400s.

**Acceptance Criteria:**
- [ ] `Server.mag` — starts HttpServer, registers routes, delegates to BBS
- [ ] `Routes.mag` — all endpoints above implemented
- [ ] JSON round-trip: Dictionary → Json encode → HTTP → Json decode → Dictionary
- [ ] Error responses for invalid categories, missing fields, malformed JSON
- [ ] Health endpoint returns tuple count and uptime
- [ ] Notifications endpoint filters by timestamp

---

#### Phase 3: Dispatcher Loop (`src/dispatcher/`)

The engine that advances workflows and evaluates rules on a tick cycle.

##### Dispatcher.mag — Tick Loop

```maggie
Dispatcher subclass: Object
  instanceVariableNames: 'bbs workflowEngine ruleEngine taskDispatcher running tick'
```

Runs as a forked Process. On each tick:
- Every tick (10s): `advanceWorkflows`, `evaluateRules`, `dispatchTasks`
- Every 6th tick (60s): `reconcile`
- Every 30th tick (5min): `housekeep`

`start` forks the loop. `stop` sets `running := false`.

##### WorkflowEngine.mag — Workflow Advancement

```maggie
WorkflowEngine subclass: Object
  instanceVariableNames: 'bbs cueCtx'
```

**`instantiate:params:`** — Create a workflow instance from a template:
1. Read template tuple by identity
2. Resolve params into transition scopes/descriptions
3. Write a `workflow` tuple with status `running`
4. Write initial `token` tuples at start places (positive, linear)
5. Return the workflow instance ID

**`advance`** — The core Petri net step:
1. Scan all `token` tuples
2. Group by `workflow_instance`
3. For each instance, find enabled transitions:
   - All input places have positive tokens
   - All preconditions met (rdp + CUE constraint eval)
4. For each enabled transition:
   - Consume input tokens (`inp:`)
   - Produce output tokens (`out:`)
   - If transition has `role`: write a `task` tuple with status `pending`
   - If transition has `workflow`: recursively instantiate sub-workflow
5. Check for completion: all tokens in terminal places → emit `workflow-complete` event + notification

**`checkPrecondition:`** — For a single precondition:
1. `rdp:` the specified category+scope+identity
2. If not found → false
3. If found and constraint specified → compile CUE constraint, unify with tuple payload
4. Return pass/fail

##### RuleEngine.mag — Reactive Rule Evaluation

```maggie
RuleEngine subclass: Object
  instanceVariableNames: 'bbs cueCtx'
```

MVP rule engine — simple, not indexed. Iterate all rules, check matches, fire.

**`evaluate`:**
1. Scan all `rule` tuples
2. For each rule with `status: "active"`:
   - For each `consumes` pattern: scan BBS for matching tuples
   - If all consume patterns have matches AND all `requires` patterns have matches:
     - Select one tuple per pattern (oldest first, same scope)
     - Fire: consume matched tuples, produce output tuples
     - Record firing time for cooldown

##### TaskDispatcher.mag — Agent Slot Management

```maggie
TaskDispatcher subclass: Object
  instanceVariableNames: 'bbs maxSlots activeSlots harnessFactory'
```

**`dispatch`:**
1. Scan `task` tuples with `status: "pending"`
2. While active slots < max slots and pending tasks exist:
   - Claim oldest pending task (consume + rewrite with `status: "dispatched"`)
   - Look up role definition for the task's role
   - Ask harnessFactory to create a harness for this role
   - Prime the harness with context from BBS (conventions, facts, signals, workflow-specific observations)
   - Start the harness in a forked Process
   - Track in activeSlots

**Slot tracking:** When a harness process completes (or a `task-complete` event is received), free the slot.

**Acceptance Criteria:**
- [ ] `Dispatcher.mag` — tick loop runs as forked process, stops cleanly
- [ ] `WorkflowEngine.mag` — instantiate workflows from templates, advance token positions
- [ ] Transitions fire when all input places have tokens and preconditions met
- [ ] CUE precondition evaluation works (compile constraint string, unify with payload)
- [ ] Task tuples written for transitions with roles
- [ ] Workflow completion detected and notified
- [ ] `RuleEngine.mag` — scan rules, match patterns, fire rules with cooldown
- [ ] `TaskDispatcher.mag` — claim tasks, spawn harnesses, track slots

---

#### Phase 4: PP CLI (`src/cli/`)

A thin Maggie binary that agents use to communicate with Procyon Park.

##### PP.mag — Entry Point

```maggie
PP subclass: Object
  instanceVariableNames: 'baseUrl client scope taskId'
```

Reads config from environment:
- `PP_URL` — server base URL (default: `http://localhost:7777`)
- `PP_SCOPE` — current scope (repo path)
- `PP_TASK` — current task ID (set by harness when spawning agent)

##### Commands.mag — Command Implementations

Each command is a method on PP that builds a JSON body and POSTs to the API.

| Command | Usage | API Call |
|---|---|---|
| `observe` | `pp observe <identity> <detail> [--tags tag1,tag2]` | POST `/api/observe` |
| `decide` | `pp decide <identity> <detail> [--rationale ...]` | POST `/api/decide` |
| `event` | `pp event <identity> [--type T] [--summary S]` | POST `/api/event` |
| `plan` | `pp plan <file.json>` | POST `/api/plan` (reads JSON from file) |
| `read` | `pp read <category> [scope] [identity]` | POST `/api/rdp` or `/api/scan` |
| `notify` | `pp notify <message> [--severity info\|warn\|urgent]` | POST `/api/notify` |
| `dismiss` | `pp dismiss` | POST `/api/dismiss` |
| `status` | `pp status` | GET `/api/health` + scan current task context |

**Argument parsing:** Maggie's `System args` returns the command-line arguments as an Array. PP parses positional args and `--flag value` options manually (simple loop over args).

**Output:** Print results to stdout as human-readable text. Errors to stderr.

**Build as separate entry point:** The `pp` binary is a separate Maggie project (or a second entry point) that compiles to its own image. Alternatively, the main Procyon Park binary can accept `pp` as a subcommand: `mag run -- pp observe ...`. For MVP, the simplest approach is a method on Main that dispatches based on args:

```
Main >> start [
  | args |
  args := System args.
  (args size > 0 and: [(args at: 0) = 'pp'])
    ifTrue: [PP new runWith: (args copyFrom: 1)]
    ifFalse: [self startServer]
]
```

**Acceptance Criteria:**
- [ ] `PP.mag` — reads env vars, creates HttpClient
- [ ] `Commands.mag` — all commands POST correct JSON to correct endpoints
- [ ] `pp observe`, `pp decide`, `pp event`, `pp notify`, `pp dismiss` work end-to-end
- [ ] `pp read` supports both single-tuple and scan modes
- [ ] `pp status` shows current task context
- [ ] Error handling: server unreachable, bad response, missing args

---

#### Phase 5: Roles and Priming (`src/roles/`)

Define the six agent roles with their system prompts and context assembly.

##### Role.mag — Base Role

```maggie
Role subclass: Object
  instanceVariableNames: 'name description systemPrompt contextConstraints ppCommands'
```

Each role defines:
- `name` — role identifier string
- `systemPrompt` — the full system prompt template for this role
- `contextConstraints` — Dictionary specifying hard/soft context for Prime
- `ppCommands` — Array of pp commands this role should know about

**`primeFor:fromBBS:`** — Assemble the priming context:
1. Always include: system prompt with `pp` usage instructions
2. Hard constraints: scan BBS for required tuples (conventions, facts, workflow-specific observations)
3. Soft constraints: fill budget with recent relevant tuples
4. Format as a single string for the harness to inject into the agent

##### Individual Role Definitions

Each role file creates a singleton role instance with its specific prompt and constraints.

**Scout.mag:**
- Purpose: Research a topic, write a document, ask to be dismissed
- Hard context: Task description, repo-metadata facts
- Soft context: Conventions, existing facts on the topic
- System prompt emphasizes: explore thoroughly, write findings to a file, then `pp dismiss`
- Key `pp` commands: `observe`, `notify`, `dismiss`

**Planner.mag:**
- Purpose: Analyze task, decompose into parallelizable subtasks
- Hard context: Task description, repo-metadata, conventions, signals (repo state)
- Soft context: Recent decisions, facts about the codebase
- System prompt emphasizes: produce a structured plan with clear subtask boundaries, identify parallelism opportunities
- Key `pp` commands: `observe`, `decide`, `plan`, `read`

**Implementer.mag:**
- Purpose: Write code for one scoped subtask
- Hard context: Plan subtask (from planner's decision), conventions
- Soft context: Facts about the code area, signals
- System prompt emphasizes: stay scoped to your subtask, report observations about surprises, signal completion
- Key `pp` commands: `observe`, `event` (task-complete), `read`

**Reviewer.mag:**
- Purpose: Independent code review
- Hard context: Plan subtask, implementation diff (NOT implementer session trace)
- Soft context: Conventions, style facts
- System prompt emphasizes: fresh eyes review, check correctness/style/security/edge cases, report issues as observations, record pass/fail decision
- Key `pp` commands: `observe`, `decide` (pass/fail), `read`

**Tester.mag:**
- Purpose: Write and run tests based on plan spec
- Hard context: Plan subtask (spec, NOT implementation)
- Soft context: Test conventions, existing test patterns
- System prompt emphasizes: test from the spec not the implementation, report failures, signal completion
- Key `pp` commands: `observe`, `event`, `read`

**Fixer.mag:**
- Purpose: Address review and test findings
- Hard context: Observations from reviewer + tester (from_transition filter), implementation diff, plan subtask
- Soft context: Conventions, decisions from implementer
- System prompt emphasizes: synthesize all feedback, address findings systematically, signal completion
- Key `pp` commands: `observe`, `event`, `read`

##### System Prompt Template Structure

Every role's system prompt follows this structure:

```
You are a {role_name} agent working within the Procyon Park orchestration system.

## Your Task
{task_description}

## Context
{assembled_context_from_bbs}

## Communication
You communicate with the coordination system using the `pp` command-line tool.
The server URL and your task context are pre-configured in your environment.

Available commands:
{role_specific_pp_commands_with_examples}

## Rules
- Do NOT attempt to coordinate with other agents directly
- All coordination flows through the tuplespace via `pp`
- Report observations about anything unexpected you discover
- When you are done, signal completion: {role_specific_completion_instruction}
```

##### ClaudeHarness.mag — Claude Code Harness

```maggie
ClaudeHarness subclass: Object
  instanceVariableNames: 'process bbs role taskId scope workDir'
```

**`startFor:task:inScope:`**
1. Set up environment: `PP_URL`, `PP_SCOPE`, `PP_TASK`
2. Assemble system prompt from role + BBS context
3. Spawn Claude via ExternalProcess:
   ```
   claude --system-prompt "<assembled_prompt>" --allowedTools "Bash,Read,Write,Edit,Glob,Grep" -p "<task_description>"
   ```
4. Monitor process completion
5. On exit: write `task-complete` event to BBS

**Acceptance Criteria:**
- [ ] `Role.mag` — base role with context assembly
- [ ] All 6 role files with system prompts, context constraints, pp commands
- [ ] `ClaudeHarness.mag` — spawns Claude with correct env + prompt
- [ ] Priming assembles correct context per role (hard/soft constraints)
- [ ] Reviewer/Tester do NOT receive implementer session traces
- [ ] Fixer receives observations from both reviewer and tester

---

## Dependencies & Prerequisites

| Dependency | Status | Notes |
|---|---|---|
| Maggie TupleSpace | Built-in | CUE template matching included |
| Maggie ConstraintStore | Built-in | For future A-MEM integration |
| Maggie HttpServer | Built-in | Route-based request handling |
| Maggie HttpClient | Built-in | For pp CLI |
| Maggie CueContext | Built-in | For constraint evaluation |
| Maggie ExternalProcess | Built-in | For spawning Claude |
| Maggie Json | Built-in | Encoding/decoding |
| Claude CLI (`claude`) | Required | Must be installed and authenticated |

## Risk Analysis & Mitigation

| Risk | Impact | Mitigation |
|---|---|---|
| TupleSpace CUE matching performance | Could slow dispatcher | MVP has small tuple counts; optimize later if needed |
| Claude CLI interface changes | Breaks harness | Isolate in ClaudeHarness.mag; single point of change |
| Parallel agent file conflicts | Corrupted worktree | Each agent gets its own git worktree (later phase) |
| Maggie language unfamiliarity | Slower development | Lean on `mag help`; keep code simple and Smalltalk-idiomatic |

## Success Metrics

- Can instantiate a workflow template and advance it through transitions
- Can dispatch a scout agent that researches, writes a document, and dismisses itself
- Can run a full pipeline: plan → implement → (review ∥ test) → fix on a real repo
- User gets notified in their Claude session when tasks need attention
- `pp` CLI works end-to-end for all commands

## Future Considerations

- **Multi-repo:** Scope-based isolation already designed in. Extend with cross-scope signals.
- **Remote deployment:** Same Maggie code, but TupleSpace backed by SQLite/DuckDB for persistence.
- **A-MEM / Gardener:** Knowledge synthesis from observations to facts. Phase 2.
- **TUI (`pp tui`):** Stretch goal. Live dashboard of system activity.
- **Git worktrees:** Each implementer works in an isolated worktree to prevent conflicts.
- **Built-in harness:** Maggie-native agent execution without external process.
