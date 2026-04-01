# Workflow Composition in Procyon Park

## The Execution Model: Petri Nets over a Tuplespace

A workflow is a **colored Petri net** where:
- **Places** are named states (strings like `"request"`, `"ready"`, `"reviewing"`)
- **Tokens** are tuples in the BBS with a `place` and `status` (`positive` or `waiting`)
- **Transitions** are the edges — they consume tokens from input places and produce tokens at output places

The dispatcher ticks every 10s. Each tick: `checkCompleted` -> `advance` (workflows) -> `evaluate` (rules) -> `dispatch` (tasks).

## Transition Types

Every transition has `id`, `in` (input places), and `out` (output places). What makes them different:

### 1. Automatic transitions

No `role` or `action`. Fire as soon as input tokens arrive and preconditions are met. Tokens produced are immediately `positive`.

### 2. Role transitions

Have a `role` field. When fired:
- Output tokens are produced with `status: "waiting"` (not `positive`)
- A `task` tuple is written to the BBS
- TaskDispatcher picks it up, spawns a Claude harness for that role
- When the agent runs `pp dismiss`, a `task-complete` event is emitted
- Next tick, `promoteWaitingTokens` detects the event and flips the token to `positive`

### 3. Action transitions

Have an `action` field. Fire inline with no agent:
- `create-worktree` — creates git branch + worktree, writes a `worktree` signal
- `merge-worktree` — merges impl branch into feature branch, cleans up
- `notify-head` — sends a completion notification

## Composition Primitives

### Sequential flow

`in: ["A"], out: ["B"]` — one place feeds the next.

### Fork (parallelism)

`out: ["reviewing", "testing"]` — one transition produces tokens in multiple places simultaneously. The `fork` transition in `full-pipeline.cue` does this.

### Join (synchronization)

`in: ["review_done", "test_done"]` — transition only fires when ALL input places have tokens. The `evaluate` transition waits for both review and test to finish.

### Conditional branching via preconditions

Multiple transitions can read from the same place, distinguished by CUE constraints on signals:

```cue
// These three compete for tokens at "evaluating"
{id: "pass",       in: ["evaluating"], out: ["merging"], preconditions: [{constraint: '{decision: "pass"}'}]}
{id: "fix_needed", in: ["evaluating"], out: ["fixing"],  preconditions: [{constraint: '{decision: "fix"}'}]}
{id: "exhausted",  in: ["evaluating"], out: ["merging"], preconditions: [{constraint: '{decision: "exhausted"}'}]}
```

The foreman agent writes `pp signal <id> decision pass` and whichever precondition matches fires.

### Loops

Output places can point back to earlier places. In `story.cue`, the `fix` transition outputs to `"implemented"`, which is the input to `review` — creating a review->fix->review cycle.

### Preconditions on BBS state

Any transition can gate on the existence of a tuple:

```cue
preconditions: [{category: "event", identity: "task-complete:{{instance}}:task:implement"}]
```

This waits for a specific event to appear in the tuplespace before firing.

### Parameter interpolation

`{{description}}`, `{{instance}}` are resolved from workflow params at fire time.

## The Rule Engine (separate layer)

Rules are standing reactive patterns in the BBS. They have `consumes` (patterns to match and consume), `requires` (patterns that must exist but aren't consumed), and `produces` (tuples to write). They support glob matching (`identity_match: "foo:*"`) and CUE constraint validation. Rules can fire across workflows — they operate on the global tuplespace.

## What You Can Do Now

- **Sub-workflows**: `spawn-workflow` action instantiates a child workflow from a parent transition. Parent tokens wait until the child completes.
- **Wave dispatch**: `dispatch-waves` action reads an epic's children (or a plan decision) and dispatches them as parallel story workflows, grouped by wave number.
- **Agent timeouts**: Harness wraps claude with 30-minute wall clock timeout and --max-turns 200.
- **Review cycle cap**: Templates can set `max_review_cycles` to prevent infinite fix loops.

## Current Limitations

- **Dynamic transition creation**: The template is static once instantiated. You can't add transitions at runtime.
- **Cross-workflow token passing**: Each workflow has its own token namespace. Workflows can only communicate via signals/events in the shared BBS.
- **Wave-level merge**: Stories in a wave create separate feature branches. No shared branch across a wave (each story merges independently).
