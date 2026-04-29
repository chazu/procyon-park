# Changelog

All notable changes to this project are documented in this file.

The format is based on Keep a Changelog, and this project adheres to
Semantic Versioning.

## [Unreleased]

### Performance
- Memoised four hot paths flagged in
  `docs/scout-perf-survey-2026-04-28.md` §6:
  - `TemplateLoader>>reloadTemplate:into:` now caches parsed payloads
    keyed by template identity + file mtime. WorkflowEngine ticks no
    longer re-parse and re-pin the same CUE template every tick.
  - `ApiServer>>watchWorkflowsFor:` and
    `DashboardSSE>>watchWorkflowsFor:in:` cache the resolved workflow-id
    Array per identity for 2 s, halving the per-request /
    per-subscriber walk over the watches set.
  - `Repo>>repoForName:` is now backed by a class-side TTL cache (5 s)
    with explicit invalidation from `pp repo add` / `pp repo remove`,
    eliminating repeated `~/.pp/repos/<name>.json` reads on the
    Scheduler / WorkflowEngine / CreateWorktreeAction /
    DispatchWavesAction hot paths.
  - `SignatureVerifier>>verify:` caches resolved `ActorContext` keyed by
    `(actor, signature)`. Skew is still checked on every hit; rotation-
    mode (`requireOldPub:`) calls deliberately bypass the cache.

### Changed
- `/api/notifications/stream` long-poll replaced with pub/sub fan-out
  via the new `NotificationHub`. Previously a blocked `pp watch` client
  ran up to 20 iterations × 2 full-table scans (`scanAll: 'notification'`
  + `scanAll: 'watch'`) per 10 s window under the BBS mutex, so a
  handful of concurrent watchers could DOS the rest of the server. Now
  the handler does one initial backlog `scanAll`, then subscribes a
  filter block to `NotificationHub` and sleeps on its own per-subscriber
  pending queue. BBS invokes the hub once per notification write
  (outside the index mutex). See
  `docs/scout-perf-survey-2026-04-28.md` §2.
- Dashboard SSE broadcaster computes ONE snapshot per 2 s tick and fans
  it out to every subscriber. Previously each subscriber re-ran ~12 BBS
  full-index scans (`workflow`/`token`/`task`/`workitem`/`event`/
  `worker`/`notification`/`watch`) and N synchronous session-file reads.
  `DashboardSSE>>tick` now runs all scans once, pre-renders every
  identity-independent panel (workflows/completions/workitems/scope-
  violations/presence/anonymous-notifs), and `broadcastTo:snapshot:`
  enqueues the cached HTML for each subscriber. Per-identity notification
  filtering still runs per signed subscriber but reuses the snapshot's
  cached `notifications` and `watch` arrays. See
  `docs/scout-perf-survey-2026-04-28.md` §4.
- `DashboardSSE>>tokenTotalsFor:in:` no longer reads
  `$HOME/.pp/sessions/<taskId>.jsonl` from disk on the broadcast hot
  path. A background `[self tokenCacheLoop] fork` (started in
  `initBBS:`) rebuilds an in-memory taskId → {input, output} cache every
  5 s; `tokenTotalsFor:in:` now sums cache entries with zero I/O. A slow
  filesystem can no longer stall every SSE subscriber's update.
- BBS index restructured for O(1) lookup. The flat `index` array is
  retained for `scan:` / `scanAll:` full scans, but write/remove paths
  now also maintain three hash indices: `byId` (id → tuple), `byKey`
  (`category|scope|identity` → tuples), and `byCatIdent`
  (`category|identity` → tuples). `findInIndex:` is now an O(1) hash
  lookup, and a new `findByCategory:identity:` resolves tuples whose
  scope is unknown without scanning. Hot callers in `Server`,
  `WorkflowEngine`, and `DispatchWavesAction` that previously did
  `(bbs scanAll: …) detect: [:t | (t at: 'identity') = id]` now use the
  hash lookup. Removes O(n) per-write/per-consume work from every BBS
  mutation. See `docs/scout-perf-survey-2026-04-28.md` §1.1.
- Dispatcher tick no longer blocks on the BBS save-to-disk path. The 10s
  tick now calls `bbs flushAsyncIfDirty`, which forks a fenced background
  write so a multi-MB JSON encode + atomic rename can never stall the
  scheduler / workflow-engine / reaper loop. CLI request paths
  (`outSync:` / `inpSync:`, `/api/bbs/out`, `/api/bbs/rm`) keep their
  synchronous `flushIfDirty` durability contract, but now wait on the
  same fence so the two writers can never race on `bbs.json.tmp`. See
  docs/scout-perf-survey-2026-04-28.md §1.3.
- BBS history rotation no longer forks `stat` on every tuple write.
  `appendHistory:` / `logEngine:` now track `history.jsonl` size in
  memory, stat'ing once lazily on the first call after process start
  and resetting the counter on rotation. Removes one fork+exec per
  `out:` / `outPinned:` / `outAffine:` / `inp:` / `update:` call. See
  `docs/scout-perf-survey-2026-04-28.md` §1.2.

### Added
- `Shell run:timeout:`, `Shell capture:timeout:`, and
  `Shell runChecked:timeout:` variants that wrap any shell command in a
  POSIX watchdog (SIGTERM after N seconds, SIGKILL 1s later). Exits with
  124 on timeout to match GNU `timeout` convention.

### Fixed
- `BBS>>outAffine:` now provides true overwrite semantics: a write at an
  existing `(category, scope, identity)` consumes the prior tuple before
  appending the fresh one. Previously each call appended a new tuple and
  callers (worker register/heartbeat, workflow watch) used a racy
  `inp:`-then-`outAffine:` workaround that could leak duplicate tuples on
  crash, double-counting workers in the dashboard presence panel. The
  workarounds in `Server.mag` (handleWorkerRegister, handleWorkerHeartbeat,
  handleWorkflowWatch) are removed. See
  docs/scout-perf-survey-2026-04-28.md §5.3.
- Dispatcher tick no longer stalls indefinitely on a stuck `git` lock or
  paused NFS mount. All inline `Shell run:` / `Shell capture:` calls on
  the tick path now have wall-clock caps and fail soft: branch-existence
  probe in `DispatchWavesAction` (10s), `GitOps` write/push operations
  (30s/60s), worktree cleanup `rm -rf` in `WorkflowEngine` (30s), and
  BBS persistence mkdir/mv/stat/rotation calls (5–10s). Reference:
  docs/scout-perf-survey-2026-04-28.md §5.5.

### Added
- `pp bbs` subcommand for tuplespace inspection and manipulation
  (`list` / `get` / `put` / `rm`). See README → `pp bbs` for the full
  surface, guarantees (durable writes, category validation, upsert,
  idempotent rm), and worked examples.
- `pp bbs put <category> <scope> <identity> <payload>` and
  `pp bbs rm <category> <scope> <identity>` CLI subcommands, implementing
  the write half of `pp bbs`. `<payload>` accepts either inline JSON or
  `@path/to/file.json`. Optional flags: `--pinned`, `--ttl SEC`,
  `--modality <persistent|linear|affine>` (defaults driven by category —
  pinned categories default to `persistent`). `put` prints
  `<id> created|updated` on success; `rm` is idempotent and prints
  `removed <cat>/<scope>/<identity>` or `no such tuple`. Invalid
  categories surface the server's 400 message (including the valid
  category list) and exit non-zero. `pp bbs` usage text updated to
  document all four subcommands + flags.
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
- `pp bbs list` and `pp bbs get` read-only subcommands for direct BBS
  tuplespace access. `list` supports `--category`, `--scope`, `--identity`,
  and `--json` filters; invalid `--category` values fail fast with the
  valid set listed. `put` and `rm` are stubbed (exit 2) pending
  story:bbs-cli:write-cmds.
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
- Dashboard "Recent Activity" list now renders newest-first instead of
  newest-last, surfacing the most recent notification at the top of the
  panel without forcing operators to scroll.
- `Scheduler>>dispatchTask:` now wraps the entire post-dispatch sequence
  (harness run + scope validation + task-complete safety net + BBS status
  update + slot release) in a coarse exception handler. An uncaught fault
  in the forked goroutine previously could — and did — tear down the whole
  server process; it now degrades to an error log and a best-effort slot
  release.

### Fixed
- Dispatcher tick loop is now resilient to malformed tuples. Each step
  (`scheduler checkCompleted`, `workflowEngine advance`, `ruleEngine
  evaluate`, `scheduler dispatch`, reapExpiredClaims, flushIfDirty,
  reconcile, housekeep) runs inside its own `on: Exception do:` so a
  single bad tuple no longer kills the entire tick — the remaining steps
  still run. Added `ifAbsent:` defaults to `WorkflowEngine` reads of
  `start_places`, workflow `payload`/`status`, template `payload`/
  `transitions`, token `place`, and to `RuleEngine`'s `consumes` lookup
  so missing keys degrade gracefully instead of raising. Refs:
  docs/scout-perf-survey-2026-04-28.md §5.1.
- `Scheduler>>checkCompleted` no longer raises `doesNotUnderstand` on every
  drain of `task-complete:<id>` events. The site at `Scheduler.mag:256`
  invoked `String>>copyFrom:` with a single argument; refactored to a
  testable class method `Scheduler taskIdFromCompleteEvent:` using the
  canonical `copyFrom:to:` form (0-indexed half-open). Adds unit coverage
  in `test/dispatcher/test_scheduler_complete_event.mag`. Reference:
  docs/scout-perf-survey-2026-04-28.md §5.2.
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
