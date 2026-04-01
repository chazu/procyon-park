# Procyon Park — Assessment & TODO

## What's Working Well

- **Top-level decomposition is sound** — BBS, Dispatcher, WorkflowEngine, RuleEngine, Scheduler each own a clear slice. You can reason about workflow advancement independently from task dispatch independently from rule evaluation.
- **Role hierarchy is textbook template method** — `completionInstructions` as hook, `primeFor:context:` as template. Clean.
- **Agent-system boundary is the best design decision** — All coordination through `pp` (HTTP/CLI) is genuine asynchronous message passing via tuplespace. This is where the architecture shines.
- **Dual-storage strategy (TupleSpace + index) is pragmatic and correct** for avoiding the drain-and-restore deadlock.

## Critical Issues

### 1. WorkflowEngine is a God Object (~900 lines)
Five built-in actions (create-worktree, merge-worktree, spawn-workflow, dispatch-waves, notify-head) at 50-80 lines each have nothing to do with Petri net semantics. **Extract into `WorkflowAction` subclasses** with polymorphic dispatch — cuts the engine roughly in half and makes actions independently testable.

### 2. Pervasive O(n^2) Array Building
`copyWith:` in loops is everywhere. Each call allocates a new array. If Maggie has OrderedCollection, use it. If not, this is the single highest-value primitive to add to the runtime.

### 3. No Atomicity in Tuple State Transitions
The `inp:` then `out:` pattern for updates (Scheduler, WorkflowEngine, ApiServer) has no transactional boundary. If the process dies between consume and re-write, the tuple vanishes. Consider a `swap:with:` or `update:do:` primitive on BBS.

### 4. `persistAfterChange` Writes Full State on Every Durable Mutation
Could become a bottleneck. You already have JSONL history — lean on a write-ahead log with periodic compaction instead of full JSON serialization per change.

## Design Friction

- **Code is largely procedural in Smalltalk clothing.** Objects hold state and expose it through accessors, but most behavior is long methods imperatively reading dictionaries. The Dispatcher tick loop is synchronous method calls, not message passing between components.
- **String-based identity conventions** (`'task-complete:' , instanceId, ':task:', transId`) are fragile — a typo silently produces unmatched tuples. An `Identity` class or named constructors would help.
- **Dictionary-as-struct everywhere** — field names are string-typed and unchecked. A `Tuple` wrapper class would catch misspellings at construction time.
- **ApiServer is ~800 lines of mechanical parse-validate-call-respond boilerplate** — cries out for a Command/Endpoint abstraction.
- **Role configuration is procedural** — each subclass's `classMethod: new` builds dictionaries manually. CUE or declarative class-side config would be more natural (you already have CUE infrastructure).

## Concurrency Concerns

- **Race in Scheduler.dispatch** — between scanning pending tasks and consuming them, another tick could find the same task. 10-second interval makes it unlikely but architecturally unsound.
- **Shell.capture: temp file uses epoch seconds** — two calls in the same second clobber each other.
- **Mutex discipline is otherwise consistent and sound.**

## Architectural Growth Risks

- **BBS as in-memory linear scan** — every scan iterates the full index. Needs category-based partitioning or embedded DB (SQLite/DuckDB are available) as workflows multiply.
- **10-second tick polling** limits responsiveness. Event-driven tuple-write notifications would be needed for sub-second coordination.
- **No template versioning** — modifying a CUE template while workflows run silently changes the transition graph mid-execution.
- **Limited error recovery** — no general retry-transition or rollback-to-place mechanism beyond the Foreman's exhausted path.

## Recommended Priorities (all completed)

1. ~~**Extract WorkflowEngine actions** into `WorkflowAction` subclasses~~ — Done: 5 action classes in `src/dispatcher/actions/`, registry-based dispatch
2. ~~**Reduce Dictionary boilerplate** — `Tuple` builder class~~ — Done: `src/bbs/Tuple.mag` with `linear:`, `pinned:`, `affine:` factories
3. ~~**Extract ApiServer handler pattern**~~ — Done: `post:fields:do:` and `get:do:` helpers, 12 handlers inlined
4. ~~**Declarative Role configuration**~~ — Done: class-side protocol overrides (`roleName`, `hardCategories`, etc.) in Role base class
5. ~~**Replace `copyWith:` loops** with growable collections~~ — Done: `GrowableArray` in `src/collections/`, converted Scheduler + DispatchWavesAction
6. ~~**Add `BBS >> update:do:` primitive**~~ — Done: `update:scope:identity:do:` and `updatePinned:scope:identity:do:`, converted 6 sites

## Prior Open Items

### Wave-level merge
Stories in a wave create separate feature branches. A shared feature branch
per wave would simplify integration — all stories merge into one branch,
then the pipeline merges that branch to main. Specced in
`docs/plans/workflow-execution-improvements.md` (Improvement 4) but not
yet implemented.

### Agent session capture
The `--output-file` flag doesn't exist in the Claude CLI. Need a different
approach (e.g., piping stdout to tee, or capturing via the harness).

### History log pagination
The `pp history` command loads the entire JSONL file into memory. For
long-running servers, this could be slow. Add server-side pagination
or streaming.

## Completed

- Template system: CUE files, two-tier discovery, always reload on startup
- Shell/GitOps extraction, dead code removal
- Worktree scoping fix (params['repo'] -> signal -> env -> cwd)
- Template scoping fallback (system -> repo scope)
- spawn-workflow action with child completion tracking
- Unique instance IDs, spawn depth limit
- Durable workflow/task/token/workitem state
- Merge failure handling (failWorkflow)
- Review cycle cap (configurable per template)
- `pp workflow wait` command
- `pp log --no-follow` fix (curl for single-fetch)
- Stale worktree cleanup on completion
- Engine-driven wave dispatch (replaced foreman for dispatch)
- Story-lite template (no review cycle)
- Agent session timeout (30min + max-turns 200)
- Small story batching in dispatch-waves
- Wave params fix (repo preserved across waves)
- Templates never persist (reload from CUE every startup)
- Commit enforcement in all agent role priming
- Verdict signal instructions in all reviewer/foreman priming
- First-class work items (category: workitem, full CRUD + status machine)
- `pp workitem` CLI (create/show/list/children/update/comment/ready/done/block/run/plan/review)
- Scheduler rename (TaskDispatcher -> Scheduler)
- dispatch-waves reads work item children
- workitem-plan and workitem-review workflow templates
- BBS upsertPinned + findInIndex (fixed persistent tuple duplication)
- Workflow authoring guide
- `make install` to PATH, maggie.toml [[target]] fix
