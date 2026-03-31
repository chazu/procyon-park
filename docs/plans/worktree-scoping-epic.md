# Epic: Worktree Scoping Fix + Spawn-Workflow

## Context

Worktrees are always created in the server's cwd, even when the workflow targets a different repo. This broke a full-pipeline run against the alto repo. Additionally, repo-scoped templates are loaded but can never be matched by the engine.

This epic fixes both issues and adds `spawn-workflow` — a new action type that lets a parent workflow instantiate child workflows, enabling multi-repo composition via the Erlang/OTP supervisor pattern.

All changes are in `src/dispatcher/WorkflowEngine.mag` unless otherwise noted.

---

## Task 1: Thread params into action dispatch and fix worktree scoping

**Dependencies:** None
**Scope:** WorkflowEngine.mag only — 3 methods modified

### Changes

**`tryFireTransition:instance:scope:params:tokenPlaces:consumed:`**
- Line ~220: Change `self executeAction: action transition: transition instance: instanceId` to pass `params:`

**`executeAction:transition:instance:` → rename to `executeAction:transition:instance:params:`**
- Add `params` parameter
- Thread `params` to `actionCreateWorktree:instance:params:`
- Add `action = 'spawn-workflow'` dispatch (can be a no-op stub for now: `('spawn-workflow not yet implemented') println`)

**`actionCreateWorktree:instance:` → rename to `actionCreateWorktree:instance:params:`**
- Add `repoName repoConfig` to temp vars
- New repo resolution chain at the top of the method, before the existing signal/env/cwd fallback:

```smalltalk
repoName := params at: 'repo' ifAbsent: [nil].
repoName notNil ifTrue: [
  repoConfig := Repo new repoForName: repoName.
  repoConfig notNil ifTrue: [
    repoPath := repoConfig at: 'path' ifAbsent: [nil]
  ]
].
```

Priority order: **params['repo'] → signal → PP_REPO_PATH env → cwd**

### Verification

- `mag build` compiles
- `mag test` passes (109 tests)
- Register a repo: `pp repo add ../alto --name alto`
- Start workflow: `pp workflow scout-mission --repo alto --param description="test"`
- Verify worktree created branching from the alto repo path, not procyon-park

---

## Task 2: Fix template scoping fallback and repo auto-inference

**Dependencies:** None (can run parallel with Task 1)
**Scope:** WorkflowEngine.mag — 2 methods modified

### Changes

**`instantiate:scope:params:`**

After the system-scope template lookup (line ~28), add fallback:
```smalltalk
template := bbs rdp: 'template' scope: 'system' identity: templateIdentity.
template isNil ifTrue: [
  template := bbs rdp: 'template' scope: scope identity: templateIdentity
].
template isNil ifTrue: [Error signal: 'Template not found: ', templateIdentity].
```

After template resolution, auto-infer repo from scope:
```smalltalk
(params at: 'repo' ifAbsent: [nil]) isNil ifTrue: [
  repoConfig := Repo new repoForName: scope.
  repoConfig notNil ifTrue: [
    params at: 'repo' put: scope
  ]
].
```

Add `repoConfig` to the method's temp var declaration.

**`advanceInstance:`**

Same template fallback at line ~92:
```smalltalk
template := bbs rdp: 'template' scope: 'system' identity: templateId.
template isNil ifTrue: [
  template := bbs rdp: 'template' scope: scope identity: templateId
].
```

### Verification

- `mag build` compiles
- `mag test` passes
- Place a custom template at `<repoPath>/.pp/workflows/custom.cue`
- Start workflow: `pp workflow custom --scope alto`
- Verify template found (was previously "Template not found")
- Verify `params['repo']` auto-inferred as `'alto'`

---

## Task 3: Extract `scopeForInstance:` helper

**Dependencies:** None
**Scope:** WorkflowEngine.mag — new method + refactor existing code

### Problem

Lines ~296-299, ~345-346, and ~456-457 all repeat the same pattern:
```smalltalk
wf := (bbs scanAll: 'workflow') detect: [:w | (w at: 'identity') = instanceId] ifNone: [nil].
scope := wf notNil ifTrue: [wf at: 'scope'] ifFalse: ['default'].
```

### Changes

Add method:
```smalltalk
method: scopeForInstance: instanceId [
  "Look up the scope for a workflow instance. Returns 'default' if not found."
  | wf |
  wf := (bbs scanAll: 'workflow') detect: [:w | (w at: 'identity') = instanceId] ifNone: [nil].
  ^wf notNil ifTrue: [wf at: 'scope'] ifFalse: ['default']
]
```

Replace all 3 inline occurrences with `scope := self scopeForInstance: instanceId`.

### Verification

- `mag build` compiles
- `mag test` passes (behavior unchanged)

---

## Task 4: Add unique instance ID generation

**Dependencies:** None
**Scope:** WorkflowEngine.mag — 1 method modified

### Problem

Instance IDs are `templateId-epochSeconds`. If two workflows instantiate in the same second (e.g., parallel spawns), their IDs collide, corrupting the tuplespace.

### Changes

In `instantiate:scope:params:`, change:
```smalltalk
instanceId := templateIdentity, '-', DateTime now epochSeconds printString.
```
to:
```smalltalk
instanceId := templateIdentity, '-', DateTime now epochSeconds printString, '-', bbs assignId printString.
```

`bbs assignId` is already a mutex-protected incrementing counter. This guarantees uniqueness.

### Verification

- `mag build` compiles
- `mag test` passes (update any tests that assert exact instance ID format)
- Instantiate two workflows in rapid succession — verify distinct IDs

---

## Task 5: Implement spawn-workflow action

**Dependencies:** Tasks 1, 2, 3, 4
**Scope:** WorkflowEngine.mag — 2 methods modified, 1 new method

### Changes

**`tryFireTransition:instance:scope:params:tokenPlaces:consumed:`**

After the existing token status logic (line ~204-205), add spawn-workflow detection:
```smalltalk
tokenStatus := role notNil ifTrue: ['waiting'] ifFalse: ['positive'].
action notNil ifTrue: [
  action = 'spawn-workflow' ifTrue: [tokenStatus := 'waiting']
].
```

**New method: `actionSpawnWorkflow:transition:instance:params:`**

```smalltalk
method: actionSpawnWorkflow: transition instance: instanceId params: params [
  "Spawn a child workflow. Output tokens are already 'waiting'.
   They promote when the child workflow completes."

  | spawnConfig childTemplate childScope childParams childInstanceId signalPayload mergedParams |

  spawnConfig := transition at: 'spawn' ifAbsent: [nil].
  spawnConfig isNil ifTrue: [
    ('spawn-workflow: missing spawn config on transition ', (transition at: 'id')) println.
    ^self
  ].

  childTemplate := spawnConfig at: 'template'.

  "Child scope: from spawn config, else parent scope"
  childScope := spawnConfig at: 'scope' ifAbsent: [nil].
  childScope isNil ifTrue: [
    childScope := self scopeForInstance: instanceId
  ].

  "Merge params: copy parent, overlay spawn-level overrides with interpolation"
  mergedParams := Dictionary new.
  params keysAndValuesDo: [:k :v | mergedParams at: k put: v].
  childParams := spawnConfig at: 'params' ifAbsent: [Dictionary new].
  childParams keysAndValuesDo: [:k :v |
    (v isKindOf: String)
      ifTrue: [mergedParams at: k put: (self resolveParams: v with: params)]
      ifFalse: [mergedParams at: k put: v]
  ].

  "Instantiate the child"
  childInstanceId := self instantiate: childTemplate scope: childScope params: mergedParams.

  "Write signal keyed to transition AND child ID (safe for re-firing in loops)"
  signalPayload := Dictionary new.
  signalPayload at: 'child_instance' put: childInstanceId.
  signalPayload at: 'child_template' put: childTemplate.
  signalPayload at: 'child_scope' put: childScope.
  signalPayload at: 'parent_transition' put: (transition at: 'id').
  bbs upsertSignal: instanceId identity: 'child-spawn:', (transition at: 'id'), ':', childInstanceId payload: signalPayload.

  ('  Spawned child workflow: ', childInstanceId, ' from ', (transition at: 'id')) println.
  bbs notify: ('Spawned child workflow: ', childInstanceId) scope: childScope severity: 'info'
]
```

**Also write child_instance onto the waiting token payloads** — in `tryFireTransition`, after producing output tokens for a spawn-workflow action, update the token payloads. Actually, simpler: store the child instance ID on the token payload at creation time.

After the `outPlaces do:` block that writes tokens (line ~206-215), add:

```smalltalk
"For spawn-workflow, record child instance on waiting tokens for promotion lookup"
(action = 'spawn-workflow') ifTrue: [
  "child instance ID will be set after action runs — handled via child-spawn signal instead"
].
```

Actually, the child instance ID isn't known until the action runs (after tokens are written). So the signal approach is correct. The `promoteWaitingTokens` method will scan signals matching `child-spawn:<transId>:*` for the token's `transition_id`.

### Verification

- `mag build` compiles
- `mag test` passes
- See Task 7 for end-to-end spawn-workflow test

---

## Task 6: Extend promoteWaitingTokens for child workflow completion

**Dependencies:** Task 5
**Scope:** WorkflowEngine.mag — 1 method modified

### Changes

In `promoteWaitingTokens:scope:`, after checking for `task-complete` events, add child workflow completion check:

```smalltalk
"If no task-complete event, check for child workflow completion"
evt isNil ifTrue: [
  "Scan signals for child-spawn matching this transition"
  (bbs scan: 'signal' scope: instanceId) do: [:sig |
    | sigIdentity sigPayload childId childScope childEvt |
    sigIdentity := sig at: 'identity'.
    (sigIdentity startsWith: 'child-spawn:', transId, ':') ifTrue: [
      sigPayload := sig at: 'payload'.
      childId := sigPayload at: 'child_instance'.
      childScope := sigPayload at: 'child_scope'.
      childEvt := bbs rdp: 'event' scope: childScope identity: 'workflow-complete:', childId.
      childEvt notNil ifTrue: [evt := childEvt]
    ]
  ]
].
```

Add to temp vars: `sigIdentity sigPayload childId childScope childEvt` — wait, these are inside the `waitingTokens do:` block. In Maggie, block-scoped temps may not be safe. Move them to the method-level temp var declaration:

Current temp vars:
```
| allTokens waitingTokens |
```

New:
```
| allTokens waitingTokens sigIdentity sigPayload childId childEvtScope childEvt |
```

And in the `waitingTokens do:` block, declare only what's needed. Actually, the existing block already declares `| transId taskCompleteId evt tokIdentity newPayload |`. Add to that block's temp vars:

```
| transId taskCompleteId evt tokIdentity newPayload childSpawnSignals |
```

Or simplify — use method-level temps for everything since Maggie block-local temp behavior is uncertain.

### Verification

- `mag build` compiles
- `mag test` passes
- See Task 7 for end-to-end test

---

## Task 7: Create spawn-workflow test and example template

**Dependencies:** Tasks 5, 6
**Scope:** New CUE template + test additions

### New template: `workflows/multi-scout.cue`

```cue
// Multi-repo scout: spawn parallel scout missions
description: "Spawn parallel scout missions across repos"
start_places: ["request"]
terminal_places: ["done"]

transitions: [
  {
    id:     "spawn-scout"
    in:     ["request"]
    out:    ["scouted"]
    action: "spawn-workflow"
    spawn: {
      template: "scout-mission"
      params: {
        description: "{{description}}"
      }
    }
  },
  {
    id:  "complete"
    in:  ["scouted"]
    out: ["done"]
  },
]
```

### Test additions to `test/test_workflow_refactor.mag`

Add a `TestSpawnWorkflow` class covering:

1. **Spawn creates child workflow** — instantiate multi-scout, advance, verify child workflow tuple exists
2. **Spawn output tokens are waiting** — verify tokens at 'scouted' have `status: 'waiting'`
3. **Child-spawn signal written** — verify signal exists with `child_instance` key
4. **Token promotion on child completion** — complete child workflow, advance parent, verify token promoted to `positive`
5. **Param interpolation** — verify child params contain interpolated parent description
6. **Unique instance IDs** — instantiate two workflows same second, verify distinct IDs

### Verification

- `mag build` compiles
- `mag test` — all existing tests pass + new spawn tests pass
- End-to-end: start multi-scout workflow via CLI, verify child instantiated and parent completes after child

---

## Task 8: Add spawn recursion depth limit

**Dependencies:** Task 5
**Scope:** WorkflowEngine.mag — `actionSpawnWorkflow` and `instantiate` modified

### Changes

Thread a `_depth` counter through params. In `actionSpawnWorkflow`, increment it. In `instantiate`, check it.

In `instantiate:scope:params:`, add after template resolution:
```smalltalk
"Check spawn depth limit"
((params at: '_depth' ifAbsent: [0]) > 10) ifTrue: [
  Error signal: 'Spawn depth limit exceeded (max 10)'
].
```

In `actionSpawnWorkflow:transition:instance:params:`, before instantiating child:
```smalltalk
mergedParams at: '_depth' put: (params at: '_depth' ifAbsent: [0]) + 1.
```

### Verification

- `mag build` compiles
- `mag test` passes
- Verify a template that spawns itself errors at depth 11

---

## Dependency Graph

```
Task 1 (params threading) ──┐
Task 2 (template scoping) ──┤
Task 3 (scopeForInstance) ───┼──→ Task 5 (spawn-workflow) ──→ Task 6 (promotion) ──→ Task 7 (tests)
Task 4 (unique IDs) ────────┘                               Task 8 (depth limit) ──┘
```

**Parallelizable:** Tasks 1, 2, 3, 4 (all independent)
**Sequential:** Task 5 depends on 1-4. Task 6 depends on 5. Tasks 7 and 8 depend on 5-6.
