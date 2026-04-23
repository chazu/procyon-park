# Authoring Workflow Templates

How to write CUE workflow templates for Procyon Park.

## File Location

- **System templates**: `workflows/*.cue` in the repo (copied to `~/.pp/workflows/` on first run)
- **Repo-scoped templates**: `<repo-path>/.pp/workflows/*.cue`
- Template identity = filename without `.cue`
- Templates are reloaded from CUE on every server start (not persisted in BBS)

## Template Structure

```cue
description: "What this workflow does"
start_places: ["request"]
terminal_places: ["done"]
max_review_cycles: 3              // optional, caps review→fix loops

transitions: [
  {
    id:  "transition-name"
    in:  ["input-place"]
    out: ["output-place"]
    // ... transition fields
  },
]
```

### Required fields

- `description` — human-readable summary
- `start_places` — array of place names where initial tokens are created
- `terminal_places` — array of place names that signal workflow completion
- `transitions` — array of transition definitions

### Optional fields

- `max_review_cycles` — integer, caps how many times a fix→review loop can repeat before forcing `exhausted` verdict

## Transitions

Every transition has `id`, `in`, and `out`. What makes them behave differently is the combination of optional fields.

### Automatic transitions

No `role` or `action`. Fire as soon as all input places have positive tokens and preconditions are met.

```cue
{
  id:  "fork"
  in:  ["integrated"]
  out: ["reviewing", "testing"]
}
```

### Role transitions (spawn an agent)

Have a `role` field. Output tokens are `waiting` until the agent dismisses.

```cue
{
  id:          "implement"
  in:          ["ready"]
  out:         ["implemented"]
  role:        "implementer"
  description: "{{description}}"
}
```

- `role` — agent role name (implementer, reviewer, tester, fixer, scout, planner, foreman)
- `description` — task description passed to the agent. Supports `{{param}}` interpolation.

### Action transitions (engine-driven, no agent)

Have an `action` field. Fire inline. Output tokens are `positive` (except `spawn-workflow` and `dispatch-waves` which produce `waiting` tokens).

```cue
{
  id:     "setup"
  in:     ["request"]
  out:    ["ready"]
  action: "create-worktree"
}
```

Built-in actions:
- `create-worktree` — create git feature branch + worktree
- `merge-worktree` — merge impl branch into feature branch; for standalone
  workflows (no `parent_branch`) also fast-forward the feature branch into
  `main` and push `origin/main` (best-effort). Wave-child workflows stop at
  the shared feature branch — the parent pipeline's `merge-worktree` handles
  the feature→main merge.
- `notify-head` — send completion notification
- `spawn-workflow` — instantiate a child workflow (see below)
- `dispatch-waves` — read a plan and dispatch stories by wave (see below)

### Preconditions

Any transition can gate on tuple existence and CUE constraints:

```cue
{
  id:  "review"
  in:  ["implemented"]
  out: ["reviewed"]
  role: "reviewer"
  preconditions: [
    {
      category: "event"
      identity: "task-complete:{{instance}}:task:implement"
    },
  ]
}
```

Precondition fields:
- `category` — tuple category to check
- `identity` — tuple identity (supports `{{param}}` interpolation)
- `scope` — optional, defaults to workflow scope
- `constraint` — optional CUE expression validated against the tuple's payload

The transition only fires when ALL preconditions are satisfied.

## Conventions

### Validate inputs on the first transition

Workflows that operate on a specific tuple (e.g., a work item) should
validate it exists and is in the right state using a precondition on
the first non-setup transition:

```cue
{
  id:  "validate-and-start"
  in:  ["request"]
  out: ["validated"]
  preconditions: [
    {
      category:   "workitem"
      identity:   "{{workitem}}"
      constraint: "{type: \"epic\", status: \"ready\"}"
    },
  ]
}
```

This prevents the workflow from advancing until the required tuple
exists and matches the constraint. If the tuple never appears, the
workflow sits at the start place. This is preferred over failing
at instantiation — it allows workflows to be started before their
inputs are ready (e.g., "start this when the epic becomes ready").

### Review transitions must instruct agents to write verdict signals

The workflow engine uses verdict signals to choose between competing
transitions (pass vs fix). The reviewer's task description MUST
tell the agent to write the signal:

```cue
{
  id:          "review"
  in:          ["implemented"]
  out:         ["reviewed"]
  role:        "reviewer"
  description: "Review: {{description}}. IMPORTANT: Write verdict: pp signal verdict:{{instance}} decision pass (or fix)."
}
```

Without this, the workflow stalls at the reviewed place.

### Agents must commit before dismissing

All role transitions that modify code depend on git commits for
merge-worktree to work. The role priming (in `src/roles/*.mag`)
enforces this, but template descriptions should reinforce it for
non-standard roles.

## Composition Patterns

### Sequential

```
in: ["A"], out: ["B"]  →  in: ["B"], out: ["C"]
```

### Fork (parallel)

One transition, multiple output places:
```
in: ["integrated"], out: ["reviewing", "testing"]
```

### Join (synchronization)

One transition, multiple input places — fires when ALL have tokens:
```
in: ["review_done", "test_done"], out: ["evaluating"]
```

### Conditional branching

Multiple transitions from the same place, distinguished by preconditions:

```cue
{id: "pass",       in: ["evaluating"], out: ["merging"],  preconditions: [{constraint: "{decision: \"pass\"}"}]}
{id: "fix_needed", in: ["evaluating"], out: ["fixing"],   preconditions: [{constraint: "{decision: \"fix\"}"}]}
{id: "exhausted",  in: ["evaluating"], out: ["merging"],  preconditions: [{constraint: "{decision: \"exhausted\"}"}]}
```

### Loops

Output places can reference earlier places:
```cue
{id: "fix", in: ["fixing"], out: ["implemented"], role: "fixer"}
```
This sends tokens back to `implemented`, restarting the review cycle.

### Spawn child workflow

```cue
{
  id:     "scout-alto"
  in:     ["ready"]
  out:    ["scouted"]
  action: "spawn-workflow"
  spawn: {
    template: "scout-mission"
    scope:    "alto"
    params: {
      repo:        "alto"
      description: "Scout: {{description}}"
    }
  }
}
```

Output tokens are `waiting`. They promote when the child workflow completes.

### Dispatch waves

```cue
{
  id:     "dispatch"
  in:     ["plan_ready"]
  out:    ["integrated"]
  action: "dispatch-waves"
}
```

Reads a plan decision from the BBS, groups subtasks by `wave` field,
dispatches each wave as parallel story workflows. Tokens promote when
all waves complete. Respects `template` (story vs story-lite) and
`batch` fields on subtasks.

## Parameter Interpolation

All `description`, `identity`, and `scope` fields in transitions support
`{{param}}` interpolation from the workflow's params dictionary.

Built-in params:
- `{{instance}}` — the workflow instance ID (always available)
- `{{description}}` — from the workflow's params (if provided)

Custom params are set via `--param key=value` at workflow start:
```bash
pp workflow my-template --param description="Build widgets" --param repo=alto
```

## Existing Templates

| Template | Use | Transitions |
|----------|-----|-------------|
| `full-pipeline` | Plan → dispatch-waves → review+test → evaluate → merge | 13 |
| `story` | Implement → review → fix cycle → merge | 8 |
| `story-lite` | Implement → merge (no review) | 4 |
| `scout-mission` | Research task → findings doc | 3 |
| `feature-design` | Idea → epic → stories → review → finalize | 10 |
| `multi-scout` | Spawn parallel scout missions | 2 |
| `workitem-plan` | Research → decompose work item into child stories | 4 |
| `workitem-review` | Review and refine work item tree | 3 |
