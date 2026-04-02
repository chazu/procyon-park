# Procyon Park — Status & TODO

## What's Working Well

- **Top-level decomposition is sound** — BBS, Dispatcher, WorkflowEngine, RuleEngine, Scheduler each own a clear slice
- **Role hierarchy is textbook template method** — declarative class-side protocol overrides with `completionInstructions` as hook
- **Agent-system boundary is the best design decision** — all coordination through `pp` (HTTP/CLI) is genuine asynchronous message passing via tuplespace
- **Dual-storage strategy (TupleSpace + index)** is pragmatic and correct for avoiding the drain-and-restore deadlock
- **Action extraction** — WorkflowEngine is focused on Petri net semantics; 5 built-in actions are independently testable in `src/dispatcher/actions/`
- **Atomic tuple updates** — `BBS >> update:do:` eliminates the consume-then-rewrite gap

## Open Issues

(None — all items from the assessment have been addressed.)

## Architectural Growth Risks

- **BBS as in-memory linear scan** — every scan iterates the full index. Needs category-based partitioning or embedded DB as workflows multiply
- **10-second tick polling** limits responsiveness. Event-driven tuple-write notifications would be needed for sub-second coordination
- **No template versioning** — modifying a CUE template while workflows run silently changes the transition graph mid-execution
- **Limited error recovery** — no general retry-transition or rollback-to-place mechanism beyond the Foreman's exhausted path

## Not Yet Implemented

### Agent session capture
The `--output-file` flag doesn't exist in the Claude CLI. Need a different approach (e.g., piping stdout to tee, or capturing via the harness).

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
- Extract WorkflowEngine actions into WorkflowAction subclasses (registry-based dispatch)
- Tuple builder class (`src/bbs/Tuple.mag`) with linear/pinned/affine factories
- ApiServer handler pattern (`post:fields:do:`, `get:do:` helpers)
- Declarative Role configuration (class-side protocol overrides)
- GrowableArray (`src/collections/`) replacing O(n^2) copyWith: loops
- BBS `update:do:` atomic primitive for tuple state transitions
- Scheduler dispatch race fix (compare-and-swap guard in `dispatchTask:`)
- Wave-level merge (shared feature branch per pipeline, parent_branch threading)
- Identity class (`src/bbs/Identity.mag`) — named constructors for tuplespace identity strings
- History log pagination (`historyTail:` fast path via `tail` for limit-only queries)
- Persistence optimization (dirty-flag with periodic flush on dispatcher tick)
