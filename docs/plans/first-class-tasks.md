# Spec: First-Class Work Items

> **Status: COMPLETED** — Full work item system implemented: data model, status machine, CRUD API, CLI (`pp workitem` with all subcommands), dispatch-waves reading work item children, workitem-plan and workitem-review templates.

## Overview

Work items are the primary planning artifact. An epic is a work item with
children. A story is a work item (possibly with sub-tasks). Workflows
execute work items but don't own them. Planning creates and refines work
items; execution consumes them.

Two distinct tuple categories:
- **`workitem`** — planning artifacts (epics, stories). Managed by humans
  and planning agents. Never auto-dispatched.
- **`task`** — agent execution slots. Created by workflow transitions.
  Consumed by the Scheduler (renamed from TaskDispatcher).

## 1. Work Item Data Model

Work items are tuples in the BBS with category `'workitem'`.

### Schema

```
category: 'workitem'
scope:    '<repo-name>'         ← repo scope (e.g., 'procyon-park', 'alto')
identity: '<identity>'          ← unique string (e.g., 'epic:dashboard', 'story:header-bar')
payload: {
  title:       "Short title"
  description: "Detailed description with file paths, method names, etc."
  status:      "backlog"        ← see state machine below
  type:        "epic"           ← epic | story
  parent:      nil              ← nil for top-level, parent identity for children
  children:    []               ← array of child work item identities
  repo:        "procyon-park"   ← which registered repo this work targets
  labels:      []               ← freeform tags for filtering
  template:    "story"          ← workflow template to use: "story" | "story-lite"
  batch:       nil              ← batch group tag (for combining small tasks)
  wave:        1                ← wave number for execution ordering
  depends_on:  []               ← identities of work items that must complete first
  comments:    []               ← array of {author, text, timestamp} for feedback
  created_by:  "human"          ← "human" | "planner" | "scout" | "reviewer"
  created_at:  1775000000
  updated_at:  1775000000
}
```

### Status State Machine

```
backlog ──→ ready ──→ in-progress ──→ done
  ↑          │  ↑          │
  └──────────┘  └──────────┘
  (deprioritize) (unblock/retry)

Any state ──→ blocked (waiting on dependency or external input)
blocked   ──→ ready   (dependency resolved)
Any state ──→ cancelled
```

- **backlog**: Known work, not refined enough or not prioritized.
- **ready**: Refined and ready to execute.
- **in-progress**: Currently being executed by a workflow.
- **done**: Completed.
- **blocked**: Waiting on a dependency or external input.
- **cancelled**: Won't be done.

### Type Semantics

- **epic**: Has children. Cannot be directly executed — dispatching an epic
  means executing its ready children. Status rolls up: epic is `done` when
  all children are `done`.
- **story**: A unit of work for one agent session (or one batch). Typically
  the leaf that a workflow executes.

### Identity Convention

Flat string, namespaced by convention:
- `epic:alto-dashboard`
- `story:alto-dashboard:header-bar`
- `task:evaluate-claude-md`

The `parent` field establishes the tree structure, not the identity.

## 2. Workflow Input Validation

Workflows can declare what work items they require as input. The engine
validates at instantiation time — before any transitions fire.

### Template `requires` Block

```cue
requires: {
  category:  "workitem"
  param:     "workitem"           // identity read from workflow params[workitem]
  validate: {
    status: "ready"               // CUE constraint on the work item payload
  }
}
```

At instantiation:
1. Engine reads `params['workitem']` to get the work item identity.
2. Looks up the work item in the BBS (scope from params or workflow scope).
3. Validates the payload against `requires.validate` using CUE unification.
4. If validation fails, workflow instantiation fails with a clear error.
5. If it passes, the work item's full payload is available to transitions
   via param interpolation.

### Example: execute-epic requires an epic with ready children

```cue
requires: {
  category: "workitem"
  param:    "workitem"
  validate: {
    type:   "epic"
    status: "ready"
  }
}
```

### Example: story workflow requires a story

```cue
requires: {
  category: "workitem"
  param:    "workitem"
  validate: {
    type:   "story"
    status: "ready"
  }
}
```

### Workflows that mutate work items

Planning and review workflows operate ON work items. They read, edit, and
create children. The `requires` block ensures the target work item exists
and is in an appropriate state before the workflow starts.

The workflow's agents use `pp workitem` CLI commands to mutate items.
The engine doesn't restrict mutations — agents have full CRUD access.
Validation only gates workflow instantiation, not ongoing execution.

## 3. CLI Surface

### pp workitem — Work Item Management

```bash
# Create
pp workitem create <identity> --title "Title" --type epic --repo procyon-park --status backlog
pp workitem create <identity> --title "Title" --type story --parent epic:dashboard --repo procyon-park
pp workitem create <identity> --title "Title" --type story --parent epic:dashboard --template story-lite --batch small-fixes --wave 1

# Read
pp workitem show <identity>                         ← full detail including comments
pp workitem list [--repo R] [--status S] [--type T] [--parent P] [--label L]
pp workitem children <identity>                     ← list children of an epic/story

# Update
pp workitem update <identity> --status ready
pp workitem update <identity> --title "New title" --description "New desc"
pp workitem update <identity> --wave 2 --depends-on story:foo,story:bar

# Comment
pp workitem comment <identity> "Reviewer feedback or discussion"

# Status shortcuts
pp workitem ready <identity>                        ← set status=ready (and all children if epic)
pp workitem done <identity>                         ← set status=done
pp workitem block <identity> --reason "waiting on X"

# Execution
pp workitem run <identity>                          ← start workflow execution
pp workitem plan <identity>                         ← agentic planning/decomposition
pp workitem review <identity>                       ← agentic review/refinement
```

### pp task — Agent Tasks (unchanged)

```bash
pp task <identity> --role <role> --description <desc> [--repo R] [--workdir P]
```

This remains as-is. Agent tasks are workflow internals. `pp task` is used
by workflows and (rarely) by the foreman for ad-hoc dispatch.

## 4. API Endpoints

### New

```
POST /api/workitem/create   ← full schema (type, parent, children, labels, etc.)
POST /api/workitem/update   ← {identity, scope, fields: {status, title, ...}}
POST /api/workitem/comment  ← {identity, scope, author, text}
POST /api/workitem/list     ← {scope?, status?, type?, parent?, label?}
POST /api/workitem/show     ← {identity, scope}
POST /api/workitem/run      ← {identity, scope} → starts workflow execution
```

### Unchanged

```
POST /api/task/create       ← agent tasks (workflow internals)
```

## 5. Scheduler (renamed from TaskDispatcher)

Rename `TaskDispatcher` to `Scheduler`. It does one thing: match agent
execution tasks (category `'task'`, status `'pending'`) to available
harness slots and spawn agent sessions.

The Scheduler:
- DOES scan `category: 'task'` with `status: 'pending'`
- DOES spawn harness processes for matched tasks
- DOES NOT touch `category: 'workitem'`
- DOES NOT make planning decisions

### File changes

- Rename `src/dispatcher/TaskDispatcher.mag` → `src/dispatcher/Scheduler.mag`
- Rename class `TaskDispatcher` → `Scheduler`
- Update references in `Dispatcher.mag`
- No logic changes — just the name

## 6. dispatch-waves Changes

Currently reads from a plan decision's `subtasks` array. Changes to read
from the epic work item's children.

### New flow

1. Read `params['workitem']` to get the epic identity.
2. Look up the epic: `bbs rdp: 'workitem' scope: scope identity: epicId`.
3. Read `children` from the epic's payload.
4. For each child identity, look up the child work item.
5. Group children by `wave` field, respect `batch` tags.
6. For each wave, instantiate workflows using each child's `template`,
   `description`, and `repo`.
7. On child workflow completion, update the child work item status to `done`.
8. When all waves complete, update the epic status to `done`.

### Backward compatibility

During migration, dispatch-waves tries the workitem path first. If no
`workitem` param exists, falls back to reading a plan decision (legacy).

## 7. Workflow Templates

### execute-epic.cue

Dispatches all ready children of an epic through wave-sequenced workflows,
then review + evaluate.

```
request → setup → dispatch-waves → integrated → fork → review + test → evaluate → merge → done
```

Identical to full-pipeline but without the planner — the epic already has
its stories.

```cue
requires: {
  category: "workitem"
  param:    "workitem"
  validate: {type: "epic", status: "ready"}
}
```

### workitem-plan.cue

Agentic planning: research and decompose a work item into children.

```
request → setup → design (scout) → review-design (reviewer) → finalize (planner) → done
```

Agents use `pp workitem create` to populate children. Similar to
feature-design but outputs work items instead of plan decisions.

```cue
requires: {
  category: "workitem"
  param:    "workitem"
  validate: {status: "backlog"}
}
```

### workitem-review.cue

Agentic review/refinement of a work item and its children.

```
request → setup → review (reviewer) → done
```

The reviewer reads the work item tree, checks accuracy, decomposes,
comments, and edits.

```cue
requires: {
  category: "workitem"
  param:    "workitem"
}
```

## 8. Implementation Plan

### Phase 1: Work Item Data Model + CLI + API

- New file: `src/cli/WorkItemCLI.mag`
- Modify: `src/cli/PP.mag` (route `pp workitem` subcommands)
- Modify: `src/api/Server.mag` (new endpoints)
- Modify: `src/bbs/BBS.mag` (add `'workitem'` to durable categories)
- New file: `src/bbs/Categories.mag` (if needed — add `'workitem'` to valid categories)

### Phase 2: Scheduler Rename

- Rename: `TaskDispatcher` → `Scheduler`
- Modify: `src/dispatcher/Dispatcher.mag` (update references)

### Phase 3: Workflow Input Validation

- Modify: `src/dispatcher/WorkflowEngine.mag` (validate `requires` block
  at instantiation time)
- Modify: CUE templates to add `requires` blocks

### Phase 4: dispatch-waves Reads Work Items

- Modify: `src/dispatcher/WorkflowEngine.mag` (read from workitem children
  instead of plan decision subtasks)
- New template: `workflows/execute-epic.cue`
- Implement `pp workitem run` (starts execute-epic or story workflow)
- Update work item status on workflow completion

### Phase 5: Agentic Planning Workflows

- New template: `workflows/workitem-plan.cue`
- New template: `workflows/workitem-review.cue`
- Modify: `src/roles/Planner.mag` (use `pp workitem create`)
- Modify: `src/roles/Reviewer.mag` (add workitem review mode)
- Implement `pp workitem plan` and `pp workitem review`

### Phase 6: Cleanup

- Deprecate `pp plan` command
- Remove plan-decision reading from dispatch-waves
- Update full-pipeline to be an alias for: plan → execute-epic
- Remove `--reseed-templates` references (already done)

## 9. Example Session

```bash
# Human creates an epic
pp workitem create epic:claude-md \
  --title "Evaluate and restructure CLAUDE.md" \
  --type epic --repo procyon-park --status backlog \
  --description "CLAUDE.md has grown large with GitNexus instructions,
    conventions, and workflow references. Evaluate whether to split
    into focused files."

# Have an agent plan it
pp workitem plan epic:claude-md
# → scout researches, designer writes epic doc
# → planner creates children via pp workitem create:
#   story:claude-md:audit --wave 1 --template story-lite
#   story:claude-md:extract-gitnexus --wave 2 --template story-lite
#   story:claude-md:extract-conventions --wave 2 --template story-lite --batch split
#   story:claude-md:update-refs --wave 3 --template story

# Have an agent review the plan
pp workitem review epic:claude-md
# → reviewer checks accuracy, adds comments, adjusts scope

# Human reviews
pp workitem children epic:claude-md
pp workitem show story:claude-md:audit

# Approve and execute
pp workitem ready epic:claude-md
pp workitem run epic:claude-md
# → execute-epic dispatches children by wave
# → each story runs through story/story-lite
# → completion updates statuses automatically
```
