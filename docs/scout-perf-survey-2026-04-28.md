# Scout: performance & panic-risk survey

Date: 2026-04-28
Scope: `src/api/Server.mag`, `src/api/DashboardSSE.mag`, `src/dispatcher/*.mag`,
`src/dispatcher/actions/*.mag`, `src/bbs/BBS.mag`.
Symptoms reported: general slowness; intermittent dispatcher panics.

Severity legend
- **PANIC** — can take the dispatcher / server down or corrupt state
- **SLOW-CRIT** — hot path scaling problem visible at modest tuple counts
- **SLOW** — wasteful but not catastrophic
- **LATENT** — only bites under unusual data shapes

---

## 1. Architectural root causes

### 1.1 Linear `index` array under one global mutex (BBS)
`src/bbs/BBS.mag:11,32,93,112,131,289,311,333,360,402` — `index` is a flat
`Array` rebuilt on every write (`index copyWith: d` allocates a fresh array)
and every consume (`index reject: …` reallocates again). Every reader holds
the same `mutex` for the entire scan/select.

Implications:
- Every `out:` is **O(n)** allocation under the writer lock.
- Every `inp:`/`removeFromIndex:` is **O(n)** scan + alloc under the lock.
- Every `scan:`/`scanAll:` is **O(n)** under the lock — and the codebase
  calls these *constantly* (see §2 / §3).

Recommendation: replace with a category→scope→identity nested map (or at
minimum a `Dictionary` keyed by category) and a separate id→tuple table for
removal; move the audit/persist work outside the lock.

**Severity: SLOW-CRIT.** Lock contention scales as O(scans/sec × tuples)
and is the single biggest contributor to "general slowness".

### 1.2 `appendHistory:` forks `stat` per BBS write
`src/bbs/BBS.mag:609-660` — every `out:` / `outPinned:` / `outAffine:` /
`inp:` / `update:` calls `appendHistory:` which calls `rotateHistoryIfNeeded`
which `Shell capture: 'stat …'`. That is one **fork+exec per tuple write**.
Under load (waves of subtask creation, token churn) this is 10–100s of forks
per second.

Recommendation: track the file size in memory, only `stat` on startup or on
every Nth append.

**Severity: SLOW-CRIT** (latency floor on every write).

### 1.3 Synchronous JSON encode + disk rename inside dispatcher tick
`src/bbs/BBS.mag:516-537` (`saveToDisk`) is invoked from
`Dispatcher>>onTick` via `bbs flushIfDirty` (`Dispatcher.mag:69`). On a busy
server with thousands of durable tuples this is a multi-MB JSON encode +
synchronous rename on the dispatcher thread, *every 10 s*. CLI sync paths
(`outSync:`/`inpSync:`, `Server.mag:523,556`) also call it on the request
thread.

Recommendation: move to incremental log append (history.jsonl already exists)
or async write with a fence; remove `flushIfDirty` from the tick path.

**Severity: SLOW-CRIT** (causes tick stalls visible as "intermittent
slowness").

---

## 2. Hot-path full scans on the request path (Server.mag)

Each item below issues at least one `scanAll:` *per HTTP request*, holding
the BBS mutex across the entire index. Listed by handler with line refs.

| Endpoint | File:line | Scans |
|---|---|---|
| `POST /api/tasks/claim` | `Server.mag:944` | `scanAll: 'task'` to find by id |
| `POST /api/tasks/available` | `Server.mag:1181` | `scanAll: 'task'` per worker poll |
| `POST /api/tasks/complete` | `Server.mag:1248` | `(scanAll: 'task') detect:` |
| `POST /api/workflow/start` (when queueMaxPending set) | `Server.mag:1444` | `scanAll: 'task'` to count pending |
| `GET /api/health` | `Server.mag:1669,1693` | `scanAll: 'task'` + `scanAll: 'event'` every probe |
| `GET /api/notifications` | `Server.mag:1754,1786` | `scanAll: 'notification'` + `scanAll: 'watch'` |
| `GET /api/notifications/stream` (long-poll) | `Server.mag:1850,1864,1786` | up to 20× (`scanAll: 'notification'` + `scanAll: 'watch'`) per blocked client |
| `POST /api/workflow/status` (cache miss) | `Server.mag:1908` | fallback `scanAll: 'workflow'` |
| `POST /api/workflow/cancel` | `Server.mag:1993,1994` | `scan: 'identity' /` + `scan: 'task' scope:` and per-task `rdp: 'worker'` |
| `GET /api/dashboard` | `Server.mag:2462-2478` | five separate `scanAll:` (workflow/task/token/notification/worker) |

The worst offender is **`handleNotificationStream:`**: a single `pp watch`
client that has nothing waiting will run **20 iterations × 2 full-table
scans** against the BBS in a 10-second window, all under the BBS mutex.
Many concurrent watchers DOS the rest of the server.

**Severity: SLOW-CRIT.** Direct cause of "request slowness" once the
tuplespace grows past a few thousand entries.

### 2.1 `bbs scanAll: 'task'` with per-element `detect:`
`Server.mag:945,1248,1908`, `WorkflowEngine.mag:729,736,743,891`,
`DispatchWavesAction.mag:275`. Every "find by id" uses
`(bbs scanAll: X) detect: [:t | (t at: 'identity') = id]`. Should be a hash
lookup. Easy O(1) win once §1.1 is addressed.

**Severity: SLOW.**

### 2.2 `affinityMatchesTask:` runs `println` per advisory commit drift
`Server.mag:1112` — inside `handleTasksAvailable`'s inner loop (capped at 20
matched, but tested over *all* tasks) the affinity check prints to stdout
when `repo` carries an `@commit`. Stdout I/O on hot path.

**Severity: SLOW.**

---

## 3. Hot-path full scans on the dispatcher tick

The dispatcher loop runs `onTick` every 10 s
(`Dispatcher.mag:51-79`). One pass currently issues:

| Source | File:line | Scan |
|---|---|---|
| `Scheduler>>checkCompleted` | `Scheduler.mag:251` | `scanAll: 'event'` — every tick |
| `WorkflowEngine>>advance` | `WorkflowEngine.mag:176` | `scanAll: 'workflow'` |
| For each running workflow: `promoteWaitingTokens:` | `WorkflowEngine.mag:292` | `scanAll: 'token'` |
| For each running workflow: `advanceInstance` | `WorkflowEngine.mag:214` | `scanAll: 'token'` (again) |
| For each running workflow with `max_review_cycles`: | `WorkflowEngine.mag:247` | `scanAll: 'task'` |
| For each waiting token: child-spawn / child-task scan | `WorkflowEngine.mag:307,327` | `scan: 'signal' scope: instanceId` (twice per waiting token) |
| For each waiting token, each child signal | `WorkflowEngine.mag:336` | `rdp: 'event'` per child |
| `RuleEngine>>evaluate` | `RuleEngine.mag:20` | `scanAll: 'rule'` then `scanAll: <category>` per `consumes`/`requires` pattern of every active rule |
| `Scheduler>>dispatch` | `Scheduler.mag:36` | `scanAll: 'task'` |
| `Dispatcher>>reapExpiredClaims` | `Dispatcher.mag:108` | `scanAll: 'task'` |
| `cleanCompletedWorkflows` (every 30 ticks) | `Dispatcher.mag:209` | `scanAll: 'workflow'` |

With **W** running workflows and **N** total tuples, a single tick is at
least O(N × W) work, all serialised through the BBS mutex.

**Severity: SLOW-CRIT.** Tick latency grows with both running workflows
and tuple population, matching the observed "general slowness" profile.

### 3.1 `DispatchWavesAction>>checkWaveCompletion`
`actions/DispatchWavesAction.mag:275` — for each child id, calls
`(bbs scanAll: 'workflow') detect:`. With **K** waves and **C** children
per wave that's **K × C** full workflow scans per tick per pipeline. On a
full-pipeline run this is the dominant cost.

**Severity: SLOW-CRIT.**

### 3.2 `WorkflowEngine` cluster of helpers
`scopeForInstance:`, `launchedByForInstance:`, `workflowStatus:`
(`WorkflowEngine.mag:726,733,740`) all `scanAll: 'workflow'`. Called from
`failWorkflow:`, action exception handlers, and `tryFireTransition:` —
which means a single workflow failure can trigger several full scans where
the engine *already has* `wf` in scope.

**Severity: SLOW.** Refactor to pass `wf` through.

### 3.3 `BBS>>childrenOfParent:in:` insertion sort
`src/bbs/BBS.mag:224-261`. Builds the sorted children list with
`sorted copyWith: t` and a tail-rebuilding loop — **O(n²) array
allocations** for every parent expansion. Called by
`scanChildrenOf:scope:` (`BBS.mag:275`), which is invoked by:
- `WorkflowEngine>>maybePromoteParent:` on every `markWorkitemDone:`
- `DispatchWavesAction>>execute:` on every wave dispatch.

For a 50-child epic that's ~2,500 array allocations under the BBS mutex.

**Severity: SLOW.** Replace with a single sort call.

---

## 4. Dashboard SSE — biggest single throughput sink

`src/api/DashboardSSE.mag` runs `tick` every 2 s
(`Server.mag:2524`). Each tick, **for each connected subscriber**,
`broadcastTo:` calls six rendering passes, each with its own scans:

- `sendWorkflows:` — `scanAll: 'workflow'` + `scanAll: 'token'` + `scanAll: 'task'` (`DashboardSSE.mag:389-391`).
- `sendCompletions:` — `scanAll: 'workflow'` + `scanAll: 'task'` (`:604-605`); plus per-completion `tokenTotalsFor:` reads `$HOME/.pp/sessions/<taskId>.jsonl` from disk synchronously (`:460-507`).
- `sendWorkitems:` — `scanAll: 'workitem'` (`:339`).
- `sendScopeViolations:` — `scanAll: 'event'` + `scanAll: 'task'` (`:134-135`).
- `sendPresence:` — `scanAll: 'worker'` (`:237`).
- `sendNotifs:` — `scanAll: 'notification'` + (per non-anon subscriber) `scanAll: 'watch'` (`:693,748`).

That is **~12 full-index scans + N session-file reads per subscriber per
2 s tick**. With three browsers open, the dashboard alone consumes most of
the BBS read budget.

Additionally:
- `sendWorkflows:` filters in-process for `status='running'` after a
  `scanAll: 'workflow'`. Completed/cancelled workflows are scanned and
  discarded. After housekeeping kicks in this is bounded, but inside the
  hour-long retention window every dead workflow keeps being re-scanned.
- `tokenTotalsFor:in:` (`DashboardSSE.mag:448`) performs blocking file
  I/O inside the SSE broadcaster goroutine — a slow filesystem stalls
  every subscriber's update.

**Severity: SLOW-CRIT.** This is the highest-leverage thing to fix; one
shared snapshot per tick (computed once, broadcast to all subscribers) plus
async session-token aggregation would slash CPU + lock pressure.

---

## 5. Panic / safety risks

### 5.1 Untrapped `at:` without `ifAbsent:` on dispatcher path
- `WorkflowEngine.mag:194` — `wfPayload at: 'status'` (no default). If a
  workflow tuple is ever written without `status`, `advance` panics on it
  and the *entire tick* aborts, because `Dispatcher>>onTick` catches at
  the loop level only (`Dispatcher.mag:53-58`), not per-step.
- `WorkflowEngine.mag:207-208` — `template at: 'payload'` and
  `templatePayload at: 'transitions'`. A malformed/partially-loaded
  template throws inside `advanceInstance` and kills the tick.
- `WorkflowEngine.mag:223` — `(t at: 'payload') at: 'place'`. A token
  missing `place` panics the tick.
- `RuleEngine.mag:45` — `payload at: 'consumes'`. Active rule with no
  `consumes` array → `consumes do:` on `nil` → panic, rule loop aborts.
- `RuleEngine.mag:69` — `consumeMatches collect: [:matches | matches first]`
  assumes every match list is non-empty (it returns early when any list is
  empty, so OK), but is fragile.
- `WorkflowEngine.mag:152` — `templatePayload at: 'start_places'` (no
  default). Bad template panics on instantiation.

**Severity: PANIC.** These match the reported "intermittent dispatcher
panics" symptom — a single misshaped tuple poisons the tick.

**Recommendation:** wrap each step inside `Dispatcher>>onTick` in its own
`on: Exception do:` so a poison tuple cannot stall every other workflow,
and add `ifAbsent:` defaults at the points above.

### 5.2 `Scheduler>>checkCompleted` slicing bug
`src/dispatcher/Scheduler.mag:256` —
```
taskId := evtIdentity copyFrom: 'task-complete:' size.
```
`copyFrom:` is being called with **one** argument (the prefix length), not
the conventional `copyFrom:to:` pair. Either:
1. Maggie's String exposes a unary `copyFrom:` whose semantics are
   "from N to end" — fine, but the user's recorded gotcha
   ("`copyFrom:to:` upper bound is exclusive (Go-style)") suggests
   selectors are positional and this may resolve to a different selector,
   or
2. it's a latent doesNotUnderstand that has so far been masked because
   `task-complete:` events are also processed by `Scheduler.dispatchTask`
   block which removes the slot before this fires.

Either way, callers in the rest of the codebase consistently use
`copyFrom:to:` (e.g. `DashboardSSE.mag:162`, `Server.mag:1796`). This is
inconsistent and worth a focused look.

**Severity: PANIC** (latent) / **LATENT BUG** depending on Maggie's
String implementation.

### 5.3 Worker `outAffine:` known-buggy duplicate path
`Server.mag:778-781` notes in-line that "BBS `outAffine:` currently
appends rather than overwriting". Worker register/heartbeat compensate by
`inp:`-then-`outAffine:`. Index-based scans pick up duplicates when a
crash happens between `inp:` and `outAffine:`. `findInIndex:` returns
*one* match arbitrarily; `scanAll:` returns all. Read-side handlers
(`handleTasksAvailable`, presence panel) treat duplicates as separate
workers.

**Severity: PANIC** for state correctness; **SLOW** because duplicate
index entries multiply scan cost.

### 5.4 Unbounded fork in `Scheduler>>dispatchTask`
`Scheduler.mag:144` — every dispatch forks an unsupervised goroutine that
may run `harness run`, then `validateScope:` (which spawns three
short-lived `/bin/sh` processes per task — `Scheduler.mag:154,167,180`),
then `ensureTaskComplete:` which writes more BBS tuples. The slot count
caps concurrent harnesses but **not** the post-validation fan-out — a
storm of completing tasks each launches three `git`/`sh` processes
synchronously. The retained `/tmp/pp-scope-check-<taskId>` files (line
175,177) are also a pid-namespacing hazard.

**Severity: SLOW** (under load); **LATENT** correctness risk if two
tasks share an id.

### 5.5 Dispatcher tick is single-thread: a hung Shell stalls everything
- `DispatchWavesAction.mag:104` — `Shell capture: 'git -C … rev-parse …'`
  on the tick path with no timeout.
- `CreateWorktreeAction.mag` — `GitOps createFeatureBranch:` and
  `GitOps createWorktree:` inline (no timeout).
- `WorkflowEngine.mag:1037` — `Shell run: 'rm -rf …'` inline.
- `BBS.mag:513,536,654,658` — `Shell run:` / `Shell capture:` for mkdir,
  mv, stat, mv (rotation).

A network-mounted repo or stuck git lock pauses **every** workflow.

**Severity: SLOW-CRIT** for any operator hitting NFS / a stale git lock.

### 5.6 Non-atomic check-then-act in `WorkflowEngine>>cancelWorkflow`
`WorkflowEngine.mag:924-945` — `bbs rdp:` then `bbs update:`; child status
checks (line 975-986) re-read each child via `rdp:` then call
`cancelWorkflow:` recursively. Concurrent cancels race; recursive call
holds the engine on the request thread.

**Severity: LATENT** correctness; **SLOW** under cascades.

### 5.7 `WorkflowEngine.mag:622` — `ExternalProcess run: 'pp' args: …` for unknown actions
Unknown action dispatch shells out to the `pp` CLI from the dispatcher
tick. A misnamed action in a template triggers a fork+exec per tick per
firing transition until someone fixes the template.

**Severity: SLOW.**

---

## 6. Memoisation opportunities

- **Templates** (`WorkflowEngine>>advanceInstance`) re-run
  `templateLoader reloadTemplate:` on every tick for every running
  workflow (`WorkflowEngine.mag:99`). Templates rarely change. Add a
  mtime cache.
- **`watchWorkflowsFor:`** (`Server.mag:1781`, `DashboardSSE.mag:742`) is
  called per request and per SSE tick per subscriber. Cache per identity
  with a short TTL.
- **`Repo new repoForName:`** (used in `Scheduler.mag:87`,
  `WorkflowEngine.mag:123`, `CreateWorktreeAction.mag:18`,
  `DispatchWavesAction.mag:90`) — file-backed config lookup repeated
  per task / per transition.
- **`signatureVerifier verify:`** runs full identity-tuple lookup per
  signed request. A keyed cache of (pubkey, sig) → actor would halve
  worker-claim CPU.

---

## 7. Suggested triage order

1. **Fix panic resilience**: per-step exception handling in
   `Dispatcher>>onTick`; `ifAbsent:` defaults at the §5.1 sites.
2. **Fix `Scheduler.mag:256` slicing** — definitive `copyFrom:to:`.
3. **Add timeouts** to all `Shell capture:` / `Shell run:` / `GitOps`
   calls reachable from the dispatcher tick (§5.5). Fail-soft instead of
   stall-forever.
4. **Index restructure** (BBS): category→identity hash for `findInIndex:`
   and consume; keep the flat array only for full scans.
5. **Stop forking `stat` per write** (§1.2).
6. **DashboardSSE**: compute one snapshot per tick, fan out to
   subscribers; move session-file aggregation to a background loop.
7. **Long-poll** `/api/notifications/stream` should subscribe to a
   notification fan-out queue, not poll `scanAll:` every 500 ms.
8. **Memoise** templates and `watchWorkflowsFor:`.
9. **Replace** `childrenOfParent:in:` insertion sort with a `sort:` call.
10. Investigate the §5.3 `outAffine:` append-vs-overwrite bug; the
    `inp:`-then-`out:` workaround is racy.

---

## Appendix A — file:line index of every `scanAll:` site reviewed

```
src/api/Server.mag           77, 944, 1181, 1248, 1444, 1669, 1693, 1754,
                             1786, 1850, 1864, 1908, 2462-2465, 2478
src/api/DashboardSSE.mag     134, 135, 237, 339, 389-391, 604, 605, 693, 748
src/dispatcher/Dispatcher.mag       108, 209
src/dispatcher/WorkflowEngine.mag   176, 214, 247, 292, 702, 729, 736, 743
src/dispatcher/RuleEngine.mag       20, 87
src/dispatcher/Scheduler.mag        36, 251
src/dispatcher/actions/DispatchWavesAction.mag   275
```
