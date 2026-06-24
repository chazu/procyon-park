# Changelog

All notable changes to this project are documented in this file.

The format is based on Keep a Changelog, and this project adheres to
Semantic Versioning.

## [Unreleased]

### Changed
- The dashboard SSE route and per-tick broadcast are always registered again.
  The `PP_NO_SSE` kill-switch (an operational mitigation for the per-tick
  memory leak) has been removed now that the leak is fixed in the Maggie VM
  (tracing string/dictionary GC plus a frame-bound block sweep). Verified
  stable under a 12-subscriber SSE soak: RSS sawtooths under load and the
  collector reclaims mid-load instead of growing unbounded toward an OOM kill.

### Added
- Board work-item detail modal. Clicking a card on the dashboard Board
  (Kanban) opens a dialog with the item's full detail — title, id, type,
  status, description, wave, labels, parent/children relationships,
  `depends_on`, repo/scope, and human-readable timestamps — fetched from a
  new unsigned `GET /api/workitem/detail?scope=&identity=` read endpoint
  (404 when not found). The modal is focus-trapped and closes on Esc or
  scrim click. A "Cancel item" button logically cancels the item (status →
  `cancelled`) via a new `POST /api/workitem/cancel`; after a confirm step
  the card drops off the active board on the next SSE tick. The cancel route
  is the first browser-initiated mutation: it is unsigned but loopback-gated
  (localhost/127.x/[::1] only; non-loopback callers get 403) for the
  single-operator local dashboard. Cancel is a logical delete only — the
  tuple, history, and relationships are preserved (no hard delete/cascade).
- Workflow staleness reaping. A workflow stuck in `running`/`dispatched`
  with no live task and past a TTL (1h) is now transitioned to `failed`
  (`last_failure_reason: workflow-stale`) by the Dispatcher housekeep pass,
  so it can be cascade-cleaned and moves from Active Workflows to Recent
  Completions. Previously such a workflow was immortal — shown active
  forever, never reaped or gc'd, even with all agents dead.
- `pp gc` now also sweeps stale-running workflows (non-terminal status, no
  live task, past TTL), giving operators a manual escape hatch for zombie
  workflows in addition to the automatic reaper.
- `pp gc` now sweeps cold `case` tuples (per-category retention window). Cases
  are written once per terminal workflow and were previously never garbage-
  collected — an unbounded, monotonic grower. `pp gc` keeps the N most-recent
  cases (`--keep-cases <N>`, default 2000) and sweeps the rest; `--no-cases`
  skips the sweep and `--cases-older-than <hours>` adds an age guard so a burst
  of recent workflows is never pruned. Cases stay retrievable by id while live
  (enricher/archivist read O(1), never by scan).

### Changed
- BBS in-memory `scan:`/`scanAll:` are now O(matches) instead of O(total durable
  tuples): a per-category bucket index (`byCategory`) is maintained alongside the
  existing hash indices. Unbounded growth in one category (e.g. `case`) no longer
  taxes every scan of every other category. (JSON-store path; the SQLite/ganso
  backend already indexes by category.)

### Fixed
- Dashboard "Active Workflows" panel now requires evidence of liveness (a
  live task, or a recent `started_at`) before showing a `running` workflow,
  instead of trusting the stored status absolutely. Abandoned/zombie
  workflows no longer linger in the active list.
- Guarded unguarded nil/non-Integer field reads in the workflows SSE render
  (`dispatched_at`, plus non-string `launched_by`/`executed_by` coercion)
  that intermittently produced `SSE render failed for workflows: Message
  not understood: ifTrue:` and a per-tick panel flicker.
- Dispatcher housekeep no longer crashes every ~5 minutes. Its workflow-id
  heuristic compared characters with `>=`/`<=`, which the Maggie VM did not
  implement for `Character` (the message returned nil), so `(c >= '0') and:
  [...]` raised `Message not understood: and:` whenever any signal tuple was
  present — aborting the orphan signal/token sweep and terminal-task reaping
  and letting the tuplespace accumulate unbounded. Now compares by integer
  code point (the project convention). The underlying VM gap (`Character`
  `<=`/`>=`) was also fixed upstream in Maggie.

## [0.2.0] - 2026-06-18

### Added
- **Ganso/SQLite backing store (now the default).** The tuplespace is backed by
  a single SQLite database (`~/.pp/data/tuples.db`, JSON `data` + indexed
  generated columns) via the embedded `ganso` coordination toolkit, replacing
  the in-memory index + whole-file `bbs.json` re-encode. Writes are durable
  per-statement (WAL); restart is O(1) (≈60× faster); memory moves to disk.
  `BBS` delegates all store ops behind the backend; set `PP_STORE=json` to use
  the legacy store (rollback). Existing `bbs.json` data is imported on first
  boot. See `ganso.md`.
- After-Action Review (AAR): every terminal workflow writes a durable `case`
  (deterministic skeleton), an Archivist agent enriches it (narrative + lessons
  + confidence), strictly off the critical path.
- `pp read <category>` now accepts `--all` (alias `-A`) to scan every scope
  at once (a non-consuming cross-scope read), instead of only the current
  scope. The default stays scoped — scope isolation is preserved. When a
  scoped read finds nothing but matches exist in other scopes, `pp read`
  prints a discoverability hint pointing at `--all` (the hint count comes
  from a second non-consuming scan and never removes tuples).
- After-Action Review enrichment loop is now closed. When an Archivist task
  completes, the dispatcher reads its structured enrichment (an `observation`
  tuple, identity `case-enrichment`, linked to the workflow instance and
  carrying `aar`/`lessons`/`confidence`/`tags`) and merges it into the case
  via `CaseEnricher`. The handler is strictly off-critical-path: malformed,
  empty, or absent archivist output is a no-op (never a crash), and a
  workflow that completed successfully stays `completed` regardless of the
  archivist outcome.

### Changed
- Migrated the entire codebase to Maggie's 1-based array/string indexing
  (Smalltalk-80 convention), which the language adopted upstream. All
  element access, `copyFrom:to:` slices (now closed intervals), manual
  index loops, and `indexOf:` not-found checks (now `0` instead of `-1`)
  were converted across `src/` and the test suite. Without this, building
  against current Maggie produced silent off-by-one corruption and
  `index 0 out of bounds` crashes throughout the server and CLI.

### Fixed
- AAR `case` tuples now survive a server restart (they are institutional
  memory). The wire story added `case` to `Categories.pinned` but NOT to
  `BBS>>isDurableCategory` — the *separate* list that actually gates the
  disk flush + reload — so cases were written linear, never flushed, and
  vanished on the next `pp serve` boot. Added `case` to `isDurableCategory`
  (kept linear, since case reads/updates use the `rdp:`/`update:` path; a
  pinned tuple would be invisible to those existence checks). New CSW5 test
  writes a case, flushes, and loads a fresh BBS from the same dir to prove
  it reloads.
- AAR enrichment now actually populates the case. The live Archivist agent
  emits its enrichment as a JSON object in the observation's `detail` field,
  but the handler expected flat `aar`/`lessons`/`confidence` keys on the
  payload — so every live enrichment silently no-op'd to empty
  (`aar=''`/`lessons=[]`/`confidence=0`) even though the loop was wired and
  crash-free. The handler now decodes `detail` JSON (falling back to the
  payload for direct-dict callers), and the Archivist prompt was tightened to
  emit exactly `{aar, lessons, confidence, tags}`. Verified live: a completed
  workflow's case is enriched with a real AAR.
- `pp workitem <subcommand>` is no longer misrouted to "Unknown workitem
  command". The dispatcher read the subcommand at `args at: 3`, but the
  handler frame is `[workitem, sub, arg]`, so the subcommand is at
  `args at: 2` (an off-by-one missed in the 1-based migration). Every
  `pp workitem` subcommand (create, create-from, run, ready, …) was broken.
- All `pp` CLI signed writes (`notify`, `observe`, `signal`, `workitem
  create`, …) no longer crash. The request-signing canonical string built
  its line separators with `String with: Character lf`, which now panics
  (`primConcat: argument must be a string`) on current Maggie. Switched to
  `String lf` (the `primLf` primitive), which yields identical bytes so
  existing server-side signature verification stays compatible.
- Restored the **server** build against current Maggie. The remaining 17
  `String with: Character lf` sites (`Main.mag`, `api/Server.mag`,
  `api/SignatureVerifier.mag`) panicked the same way at runtime, so a
  server rebuilt on current Maggie could not verify signed writes (its
  `SignatureVerifier` canonical) — the prior running server worked only
  because it predated the VM change. All 17 migrated to `String lf`
  (byte-identical `\n`), so a freshly built `pp serve` verifies signed
  writes again. This unblocks live deployment of the AAR case/enrichment
  feature.
- `pp worktree clean` no longer crashes the whole command when a single
  worktree fails to process. The sweep now handles each task inside its own
  rescue (logging and continuing on error) and skips malformed, non-string
  directory-listing entries instead of crashing on string concatenation.
  Reports a count of any skipped worktrees.
- Restored buildability against current Maggie. The bundled `alto` Go
  interop shims (`wrap/tcell`, `wrap/terminal`) used the old
  `PrimitiveFunc` signature (`interface{}` receiver) and no longer
  compiled after the VM switched to a typed `*VM` receiver. Since `alto`
  was unused (no `src/` code referenced its symbols), the dependency was
  dropped entirely rather than patched.

### Added
- `pp gc` is now the single command for cleaning up everything stale.
  In addition to the prior behaviour (terminal workflows + their
  associated tuples, merged `feature/*` branches), the default sweep
  now also covers:
    - **Orphan tuples** — tasks/tokens/events whose
      `payload.workflow_instance` references a workflow that no longer
      exists, plus signals whose scope matches the workflow-id
      convention (`<template>-<epoch>-<id>`) but isn't in the live set.
      Survey snapshot found 793 of 1,290 tuples (~62%) were unreachable
      junk because the legacy walk could only reach tuples through an
      existing workflow.
    - **Worktrees** — folds in `pp worktree clean`, the largest
      consumer of `~/.pp` disk (~88 MB per task dir).
    - **Session files** — orphan `~/.pp/sessions/<task>.jsonl` older
      than `--older-than` hours (default 24). Was opt-in via
      `--sessions`, now default-on (use `--no-sessions` to skip).
    - **bbs.json backup rotation** — keeps the newest
      `--keep-backups N` (default 2) and removes the rest.
  Use `--dry-run` to preview, `--no-sessions` / `--no-worktrees` as
  escape hatches.

### Removed
- The unused `alto` dependency and its generated Go interop wrappers
  (`wrap/tcell`, `wrap/terminal`).

### Fixed
- Dashboard "Recent Activity" panel was silently dropping the newest
  notification and producing a `Message not understood: at:ifAbsent:`
  on every SSE tick, which (combined with the unrescued tick loop)
  blacked out the entire dashboard. `renderNotificationsHtml:` iterated
  `recent size to: 1 by: -1`, treating the slice as 1-indexed; Maggie
  arrays are 0-indexed half-open (same convention as `copyFrom:to:`,
  see commit 7c182d6). Loop now walks `(recent size - 1) to: 0 by: -1`,
  reads `created_at` from the tuple top-level (the prior code looked
  inside `payload`, where it never lives, so timestamps were always
  empty), and uses the two-arg `copyFrom:to:` form for the >30
  trim. Each row is wrapped in its own rescue so one malformed
  notification can no longer void the whole panel.
- Dashboard SSE broadcast goroutine no longer dies permanently on a
  single bad tuple. `Server>>startSSETick` now wraps each `tick`
  invocation in `on: Exception do:`, `DashboardSSE>>tick` rescues
  `computeSnapshot` independently, and `computeSnapshot` runs each
  panel's render in its own `safeRender:rootId:do:` so a single failing
  panel falls back to a render-error fragment instead of blanking the
  whole dashboard. Prior behaviour: a single throw permanently killed
  the broadcast fork, leaving every panel stuck on "Connecting…" with
  the HTTP server still running and `/api/dashboard` still returning
  fresh data — fault localised to the broadcast loop.
- `CLIBase>>silentInp:scope:identity:` was posting to the signed
  `/api/inp` route, which silently failed for `task`/`token`/`signal`
  removals (root cause not yet diagnosed; `/api/inp` works fine
  unsigned via curl, and `pp bbs rm` works fine via the unsigned
  `/api/bbs/rm` route). Effect: `pp gc` printed `[orphan task] …`
  and `[orphan signal] …` lines but the live tuple count never
  moved — the workflow-children sweep and the new orphan sweep
  were no-ops in practice. Switched the helper to the unsigned
  `/api/bbs/rm` endpoint that `pp bbs rm` already uses; that
  endpoint also sync-flushes, so a server restart between `pp gc`
  and the next async flush no longer resurrects the tuples.
  Verified end-to-end: a single `pp gc` dropped a stale tuplespace
  from 1,294 to 504 tuples and `~/.pp/worktrees` from 1.5 GB to 0.

- DashboardSSE `tokenCacheLoop` ran every 5 s reading every
  `~/.pp/sessions/<task>.jsonl` (~127 MB / 413 files on the survey
  host) regardless of whether any dashboard client was connected,
  burning ~25 MB/s of allocations the Go heap couldn't return to the
  OS fast enough. Over a few hours pp serve drifted to 32 G of
  committed pages with macOS compressing 12 G of them — system-wide
  memory pressure from a closed dashboard. Fixed three ways:
    - The refresh now early-returns when `sseSubscribers isEmpty`.
    - Per-file mtime cache (`tokenCacheMtimes`) so unchanged session
      files aren't re-parsed.
    - The dedicated `tokenCacheLoop` fork is gone; the work runs
      inline in `tick`, which is already gated on subscribers and
      already runs every 5 s. Permanent goroutine count drops from 3
      to 2.

  Combined with the upstream maggie `petermattis/goid` arm64 fast-path
  upgrade, idle CPU went from ~200% to ~0% and `top` memory from 32 G
  to 1.5 G.

- BBS pinned-upsert paths (`updatePinned:do:`, `upsertPinned:`,
  `upsertSignal:`) had read-modify-write races: two concurrent updaters
  on the same key could lose updates or leave duplicate signal tuples.
  Serialised through a new `upsertMutex` distinct from the index `mutex`
  so the find/remove/write sequence runs as one critical section without
  reentering the non-reentrant index lock.
- BBS `outAffine:` drained legacy duplicates with a tight
  `[(self inp: ...) notNil] whileTrue: []` spin loop. A pathological
  writer could pin a CPU core indefinitely. Replaced with a bounded
  drain (cap 1024) that yields between iterations.

### Changed
- BBS flat `index` switched from `Array` (with O(N) `copyWith:` per write)
  to `ArrayList` (amortized O(1) `add:`). Public `scan:`/`scanAll:`
  still return a fresh `Array` so JSON encoding and existing callers
  are unaffected.
- BBS `saveToDisk` snapshot is now self-contained: each durable tuple
  and its payload are shallow-copied inside the index critical block
  before the JSON encode runs unlocked. Removes the unstated invariant
  that no caller may mutate a payload Dictionary in place.
- Dispatcher housekeeping is now single-pass: one `scanAll:` per
  category (`workflow`/`token`/`task`/`signal`) with tuples bucketed
  by `workflow_instance` (or scope, for signals). Prior implementation
  issued 3 `scanAll:` per terminal workflow inside `cleanWorkflowCascade:`
  — O(N*M) in (terminal workflows × tuples). Now O(M).
- Dispatcher `onTick` takes ONE `bbs scanAll: 'task'` snapshot per tick
  and threads it through `Scheduler>>dispatchTasks:`,
  `reapExpiredClaimsIn:`, and `reapStuckDispatchedIn:`. Prior code took
  three independent task scans every tick. The original `dispatch`,
  `reapExpiredClaims`, and `reapStuckDispatched` selectors remain as
  no-arg trampolines for callers that don't have a snapshot.
- Replaced `victims := victims copyWith: t` accumulators in the
  housekeeping cascade and both reaper filter loops with
  `GrowableArray` (amortized O(1) append) — was O(K²) on K matches.

- Unified the duplicated `maybePromoteParentOf:` (Server.mag) and
  `maybePromoteParent:` (WorkflowEngine.mag) cascade-promotion methods
  into a single `WorkflowEngine>>maybePromoteParent:scope:` impl.
  `Server>>handleWorkitemUpdate:` now delegates via
  `dispatcher workflowEngine maybePromoteParent:scope:`, and the
  unified impl uses the new `BBS>>updatePinned:do:` helper. Net -27
  source lines (deleted Server's 28-line duplicate). Scout survey §1.6.
- Migrated 6 manual payload-clone + `upsertPinned:` sites to use the
  existing `BBS>>updatePinned:scope:identity:do:` block helper (which
  already does the safe payload-copy + atomic replace). Added an
  `actor:do:` overload so the 4 sites that needed actor attribution
  (`handleWorkitemUpdate:`, `handleWorkitemComment:`, `handleUserRevoke:`,
  `handleUserRotate:`) can also use it. Sites in `Server.mag`
  (`updateWorkitemStatus:`, `handleWorkitemUpdate:` plus its child
  cascade, `handleWorkitemComment:`, `handleUserRevoke:`,
  `handleUserRotate:`) and `WorkflowEngine.mag` (`markWorkitemDone:`).
  Net diff: -36 source lines. Scout survey §1.3.
- Replaced `Array new: 0` + sequential `copyWith:` build-up patterns
  with array literal syntax `#('a' 'b' 'c')` where the leading elements
  are static strings. Touches `WorkflowEngine>>buildAffinity:` (5-element
  valid-keys list), `Shell` class methods (`run:`, `capture:`,
  `runChecked:`), `Scheduler>>validateScope:` (4 shell-args sites), and
  the `Main`/`PP`/`Repo`/`CliPP`/`WorkItemCLI` CLI dispatch helpers.
  Pure refactor — no behavior change. Scout survey §1.4/§3.12.

### Fixed
- Slice-by-prefix off-by-one in `ApiServer>>watchWorkflowsFor:` (Server.mag)
  and `DashboardSSE>>renderTodayWindow` (scope-violation aggregation): both
  used `prefix size + 1` against Maggie's exclusive `copyFrom:to:` upper
  bound, dropping the first character of the suffix. Centralised the slice
  in a new `StringUtil>>stripPrefix:from:` helper and replaced all three
  sites (the third was the already-correct
  `Scheduler>>taskIdFromCompleteEvent:`). See scout survey §1.9.
- `Dispatcher>>reapExpiredClaims` no longer crashes the tick when it
  observes a stale `task` tuple snapshot. `BBS>>scanAll:` returns shared
  index references, so a payload mutated by a racing
  `Server>>handleTaskComplete` or `Scheduler>>checkCompleted`
  (status='completed', claim_expires_at=nil) could surface to the reaper
  mid-iteration and a `now > nil` comparison or a missing `payload`
  would panic with "invalid memory address or nil pointer dereference",
  killing the entire sweep. The per-tuple body is now wrapped in a
  defensive try/catch that logs and skips, and the comparison guard
  re-checks `payload notNil` and `expiresAt notNil` before reading.
  Mirrored inside `reapTask:`'s atomic `update:` block. Adds RP8/RP9
  unit tests covering the post-completion stale-tuple and corrupt
  (non-Number) `claim_expires_at` cases.

### Removed
- Dead code per scout §3 cleanup:
  - `Dispatcher>>reconcile` (empty placeholder) and its onTick caller.
  - `Dispatcher>>expireAffineTuples` (empty placeholder) and its
    housekeep caller — TupleSpace already handles affine TTLs natively.
  - `BBS>>workitemTuple:precedesTuple:` (unused after `childrenOfParent:`
    was switched to `sort:`).
  - `BBS` `cueCtx` instance variable — assigned but never read.
  - `BBS>>removeFromIndexUnsafe:` — inlined into its sole caller
    (`update:scope:identity:do:`).
  - Deprecated `Server>>handleWorkitemAddChild:` and the
    `/api/workitem/add-child` route — no in-tree callers remained.
  - Back-compat overload ladders for
    `Dispatcher>>instantiateWorkflow:scope:params:...` and
    `WorkflowEngine>>tryFireTransition:...` — only the widest signature
    is kept; in-tree callers were migrated.

### Performance
- `WorkflowEngine` failure path no longer triggers redundant
  `scanAll: 'workflow'` calls. Added wf-accepting variants
  (`scopeFromWf:`, `launchedByFromWf:`, `workflowStatusFromWf:`) and a
  `failWorkflow:scope:reason:` overload; `tryFireTransition:` action
  exception handlers and the post-action status check now use scope-aware
  rdp lookups instead of full-table scans. See
  `docs/scout-perf-survey-2026-04-28.md` §3.2.
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
