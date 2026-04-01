# Epic: Workflow Execution Improvements

## Overview

Replace the foreman-driven dispatch model with engine-driven wave execution.
Add story-lite template, small-story batching, and wave-level merge.

## Improvement 1: Engine-Driven Wave Dispatch (implement directly)

### Problem

The foreman agent runs for the entire dispatch phase (30-50 min), consuming
tokens while mostly idle (waiting on `pp workflow wait`). It frequently hits
context limits and hangs. Wave sequencing is unreliable because it depends
on the agent correctly parsing deps and executing them in order.

### Design

Replace the foreman's dispatch role with a new action: `dispatch-waves`.
The engine reads the plan decision from the BBS, groups subtasks by wave,
and executes waves automatically using spawn-workflow mechanics.

**New action: `dispatch-waves`**

When fired, this action:
1. Reads the most recent plan decision from BBS (category: 'decision', looks
   for one with 'subtasks' in payload).
2. Groups subtasks by `wave` field (default wave 1 if absent).
3. For each wave in order:
   a. For each subtask in the wave, instantiate a story (or story-lite)
      workflow with the subtask's description, role, and repo.
   b. Write child-spawn signals for each.
   c. Write a `wave-status` signal tracking which workflows are in this wave.
4. Output tokens are set to `waiting` — they promote when ALL child
   workflows in the LAST wave complete.

**How it fits in the template:**

Replace the `dispatch_integrate` transition (foreman role) with:
```cue
{
  id:     "dispatch"
  in:     ["plan_ready"]
  out:    ["dispatched"]
  action: "dispatch-waves"
}
```

The `dispatched` tokens are `waiting` and promote when all waves complete.
No foreman agent involved in dispatch at all.

**Wave completion tracking:**

For each wave, the engine writes a signal:
`wave:<instanceId>:N` with payload `{total: M, completed: 0, workflows: [...]}`

On each `advance` tick, `promoteWaitingTokens` checks:
- For each wave signal, count how many child workflows have completed
- When all workflows in wave N complete, dispatch wave N+1
- When all workflows in the final wave complete, promote the waiting token

**Key change to WorkflowEngine.mag:**

New method: `actionDispatchWaves:transition:instance:params:`
- Reads plan from BBS
- Groups into waves
- Dispatches wave 1 immediately
- Writes wave tracking signals

Modified: `promoteWaitingTokens:scope:`
- Adds wave-tracking promotion logic
- When wave N completes, instantiates wave N+1's workflows
- When final wave completes, promotes the output token

**Template change:**

`full-pipeline.cue` replaces the `dispatch_integrate` foreman transition with
a `dispatch-waves` action transition. The foreman role is ONLY used for the
`evaluate` transition (where judgment is needed).

### Files to modify

- `src/dispatcher/WorkflowEngine.mag` — new action + wave tracking in promotion
- `workflows/full-pipeline.cue` — replace dispatch_integrate transition
- `workflows/story.cue` — no change (used as child template)
- `src/roles/Foreman.mag` — remove dispatch mode, keep evaluate only

---

## Improvement 2: Story-Lite Template

### Problem

Every story goes through implement → review → verdict → merge. For mechanical
changes (rename, config update, add a test file), the review adds 5-10 min
of wall time with no value.

### Design

New template `story-lite.cue`:
```
setup (create-worktree) → implement → merge → notify → done
```

No review, no verdict, no fix cycle. Implement and merge.

The planner tags subtasks with `template: "story-lite"` or `template: "story"`.
The dispatch-waves action reads this field and uses it when instantiating.
Default is `"story"` if not specified.

### Files

- `workflows/story-lite.cue` — new template
- `src/dispatcher/WorkflowEngine.mag` — `actionDispatchWaves` reads template field
- `src/roles/Planner.mag` — priming tells planner to tag mechanical tasks as story-lite

---

## Improvement 3: Small Story Batching

### Problem

Wave 1 might have 5 trivial tasks. Spawning 5 separate agent sessions
(each with full context build, worktree creation, review cycle) wastes time.
A single agent could do all 5 in one session.

### Design

The planner can tag a group of small subtasks with `batch: "batch-name"`.
The dispatch-waves action groups same-batch subtasks into a single story
workflow whose description is a numbered list of all the subtask descriptions.
The implementer does them all in one session, one commit.

Batched subtasks MUST:
- Be in the same wave
- Target the same repo
- Use the same template (story or story-lite)

### Files

- `src/dispatcher/WorkflowEngine.mag` — batch grouping in `actionDispatchWaves`
- `src/roles/Planner.mag` — priming tells planner about batching

---

## Improvement 4: Wave-Level Merge

### Problem

Each story creates its own feature branch and merges its impl branch into it.
The foreman was supposed to aggregate these, but there's no mechanism for
combining multiple story branches into a single result. Stories' changes
exist on separate feature branches that nobody merges together.

### Design

The dispatch-waves action creates ONE feature branch for the entire pipeline
instance. All story workflows in all waves get worktrees branching from this
shared feature branch. Each story's merge-worktree merges its impl branch
back into the shared feature branch.

**Concrete change:**

In `actionDispatchWaves`, before dispatching wave 1:
1. Create feature branch: `feature/<pipeline-instance-id>`
2. Write worktree signal for the pipeline instance with this feature branch
3. When instantiating child story workflows, pass the feature branch name
   in params so `create-worktree` branches from it (not from main)

In `actionCreateWorktree`, check for a `parent_branch` param:
- If present, branch from it instead of creating a new feature branch
- The impl branch still goes into a worktree
- `merge-worktree` merges impl into the parent branch

After all waves complete and the pipeline reaches its own `integrate` step,
the pipeline's merge-worktree merges the feature branch into main.

### Files

- `src/dispatcher/WorkflowEngine.mag` — `actionDispatchWaves` creates shared branch,
  `actionCreateWorktree` supports `parent_branch` param
- `workflows/full-pipeline.cue` — integrate step merges the shared feature branch

---

## Improvement 5: Agent Session Timeout

### Problem

Agents can hang silently (context exhaustion, network issues). The foreman
hung for 15 min before we killed it manually.

### Design

Add `--max-turns` to the claude harness invocation. Default 200 turns.
If the agent exceeds it, the harness exits with a non-zero code, and
the workflow engine handles it as a task failure.

Also add a wall-clock timeout via the `timeout` command wrapping the
claude process. Default 30 minutes per agent session.

### Files

- `src/harness/ClaudeHarness.mag` — add timeout wrapping

---

## Dependency Graph

```
Improvement 1 (wave dispatch) — implement directly, prerequisite for all others
    ↓
Improvement 4 (wave-level merge) — depends on 1 (needs shared branch)
    ↓
Improvement 2 (story-lite) — independent, but dispatch-waves reads template field
Improvement 3 (batching) — independent, but dispatch-waves handles grouping
Improvement 5 (timeout) — fully independent
```

## Implementation Order

1. **Wave dispatch** — implement now (this session)
2. **Dispatch as epic:** improvements 2, 3, 4, 5 as stories via the new system
