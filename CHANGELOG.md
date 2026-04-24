# Changelog

All notable changes to this project are documented in this file.

The format is based on Keep a Changelog, and this project adheres to
Semantic Versioning.

## [Unreleased]

### Added
- `POST /api/bbs/put` and `POST /api/bbs/rm` — unsigned HTTP routes for local
  CLI inspection/ops. `put` performs an UPSERT (consumes any existing tuple
  with the same `(category, scope, identity)` triple before writing the new
  one) and reports `created:true|false`; `rm` consumes by composite key and
  reports `removed:true|false` (idempotent — repeated `rm` is not an error).
  Both flush BBS state synchronously before responding so a SIGKILL after
  the ack does not lose the mutation. Tuple ids are server-generated; any
  client-supplied id is ignored. Match the unsigned posture of `/api/rdp`
  and `/api/scan` — auth hardening tracked separately.
- `BBS>>outSync:scope:identity:payload:` and `BBS>>inpSync:scope:identity:` —
  synchronous-flush variants of `out:` / `inp:` for CLI-facing mutations
  that need durability before returning to the caller. Wrap the existing
  async-dirty-flag path with a trailing `flushIfDirty`; the default path
  is unchanged so the engine is not serialized on disk I/O. Chose new
  selectors (option b) over a keyword `sync:` arg because `out:` already
  has a dense stack of arities (actor:, launchedBy:, executedBy:) and
  adding a boolean to every one would have doubled the surface area.
- `pp workitem show <id>` now lists related workflows with their status
  (running / completed / failed). Failed entries include the failure
  reason and a `retry: pp workitem run <id>` hint so operators notice
  mid-run crashes and know how to re-dispatch.
- `pp serve` now writes durable crash logs to `~/.pp/logs/crash-<epoch>.log`
  (plus a rolling append to `~/.pp/logs/pp-serve.log`) when the server
  exits abnormally. Previously a silent crash left no breadcrumb — operators
  had to infer the failure from `pp workflow status` returning
  "no response from server".
- `scripts/pp-supervisor.sh`: auto-restart wrapper for `pp serve` with a
  burst-window circuit breaker (`PP_SUPERVISOR_MAX_RESTARTS` per
  `PP_SUPERVISOR_BURST_WINDOW` seconds) and per-run + rolling log capture.

### Changed
- `Scheduler>>dispatchTask:` now wraps the entire post-dispatch sequence
  (harness run + scope validation + task-complete safety net + BBS status
  update + slot release) in a coarse exception handler. An uncaught fault
  in the forked goroutine previously could — and did — tear down the whole
  server process; it now degrades to an error log and a best-effort slot
  release.

### Fixed
- Strict input validation at every pp boundary (pp-input-validation-strict,
  umbrella for four child bugs):
  - `pp workitem run/show/update/comment` now require `--repo <scope>` (or
    `PP_SCOPE`); when omitted, the CLI errors pre-flight and lists the
    scopes where the identity actually lives. No more silent dispatch to
    the `default` scope.
  - `pp workitem update <id>` with no field flags is rejected as a no-op
    error instead of silently stamping `updated_at` and passing an empty
    payload through. The server-side handler is already PATCH-merge, so
    the client now refuses to send vacuous updates.
  - Workflow templates may declare `required: [...]`. `WorkflowEngine`
    validates required params are present and non-empty at instantiation
    and raises with the missing names — no more empty-prompt dispatch.
    `workflows/story.cue` and `workflows/story-lite.cue` now declare
    `description` as required.
  - `ClaudeHarness` refuses to spawn Claude when the task's rendered
    description is empty; sets `failureReason` so `WorkerAgent` marks
    the task failed with an actionable diagnostic ("run pp workitem
    update <id> --description ...") instead of letting the CLI exit 1
    and leaving the state machine to guess.
- Failed workflows that never produced commits now clean up after
  themselves: `WorkflowEngine>>failWorkflow:reason:` removes the worktree
  and deletes `feature/<instance>` + `impl/<instance>` when both branches
  are zero commits ahead of `main`. Previously a crash between
  `create-worktree` and the first implementer commit left an empty
  `feature/<instance>` behind, and re-dispatching the workitem hit
  "branch already exists". Worktrees with unique commits are still
  preserved for recovery.
- `merge-worktree` on standalone story/story-lite/hotfix/spike workflows now
  fast-forwards the feature branch into `main` and pushes `origin/main`
  (best-effort) instead of leaving the commits stranded on `feature/<id>`.
  Previously the action emitted `merge-complete` without touching `main`,
  so work-items closed as "done" while nothing landed. The
  `merge-complete` observation now carries `merged_to_main: 'true' | 'false'`
  so downstream consumers can disambiguate.

### Changed
- `CreateWorktreeAction` records a `standalone` flag (and `parent_branch`
  when applicable) on the `worktree` signal so `MergeWorktreeAction` can
  distinguish wave-children (parent pipeline owns the main merge) from
  standalone workflows (self-managed main merge).
- Added `GitOps pushBranch:in:` helper.
