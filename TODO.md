# Procyon Park — TODO

## Open

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
- Worktree scoping fix (params['repo'] → signal → env → cwd)
- Template scoping fallback (system → repo scope)
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
- Scheduler rename (TaskDispatcher → Scheduler)
- dispatch-waves reads work item children
- workitem-plan and workitem-review workflow templates
- BBS upsertPinned + findInIndex (fixed persistent tuple duplication)
- Workflow authoring guide
- `make install` to PATH, maggie.toml [[target]] fix
