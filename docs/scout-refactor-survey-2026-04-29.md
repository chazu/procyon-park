# Scout: refactoring & test-coverage survey

Date: 2026-04-29
Scope: `src/` broadly, with focus on `src/bbs`, `src/dispatcher`,
`src/api`, `src/cli`, and `test/`.
Cross-reference: `docs/scout-perf-survey-2026-04-28.md` (perf items
already filed there are NOT re-reported here; only structural,
duplication, sprawl, and coverage concerns).

Severity legend
- **CRIT** — actively impedes change, very likely to bite again, or
  hides a correctness landmine
- **HIGH** — concrete refactor lever with high payoff
- **MED**  — worth doing in the next sweep through the file
- **LOW**  — nit; add to the running cleanup list

---

## 1. Code duplication

### 1.1 `actorCtx verified` 4-line auth preamble repeated 6× — HIGH
`src/api/Server.mag:601-606, 672-677, 795-800, 920-925, 1156-1161,
1237-1242` — the same 6-line preamble:
```
actorCtx verified ifFalse: [
  enforceSignatures
    ifTrue: [^self errorResponse: 401 message: 'signature verification failed']
    ifFalse: [^self errorResponse: 503
                     message: 'enforce_signatures must be true for <verb>']
].
```
Six handlers (`handleWorkerDrain`, `handleWorkerRegister`,
`handleWorkerHeartbeat`, `handleTaskClaim`, `handleTasksAvailable`,
`handleTaskComplete`) each copy-paste this.

**Fix:** add `requireVerified: actorCtx for: 'task claim'` returning an
`HttpResponse` or nil, and let each caller do
```
err := self requireVerified: actorCtx for: 'task claim'.
err notNil ifTrue: [^err].
```
Knocks 36 lines, prevents drift (already drifted: §1.2 below).

### 1.2 Worker-only "actor must equal worker:<name>" guard repeated 3× — MED
`Server.mag:617-620, 811-815, 945-948`. Same compute (`Identity worker:
name`) and same error shape, but each phrased slightly differently:
- `:619` — `'drain requires worker:', name, ' signer (got ', actorHeader printString, ')'`
- `:813` — `'actor ', actorStr printString, ' does not match worker ', identity`
- `:947` — `'claim requires ', expectedActor, ' signer (got ', actorHeader printString, ')'`

**Fix:** `requireWorkerActor: actorCtx name: name verb: 'drain'` →
`HttpResponse | nil`. Will normalise all three error strings into one
shape (helpful for client parsing).

### 1.3 Payload clone-and-merge pattern repeated 9× — HIGH
`src/api/Server.mag:628-630, 824-825, 2263-2265, 2284-2285, 2322-2323,
2356-2358, 2457-2459, 2607-2609, 2657-2658`; also
`src/dispatcher/WorkflowEngine.mag:896-898, 924-925`.
Every call site does:
```
newPayload := Dictionary new.
payload keysAndValuesDo: [:k :v | newPayload at: k put: v].
newPayload at: 'X' put: ...
```
This is a missing helper — should live on Dictionary or a small
util-class, e.g. `Dictionary>>copyWith: k put: v` or
`PayloadOps>>cloneOf: payload setting: aBlock`.

**Fix:** introduce `BBS>>updatePayloadOf: tuple with: aBlock` (clones,
yields the dict, returns the new dict) so handlers shrink to:
```
newPayload := bbs updatePayloadOf: existing with: [:p |
  p at: 'drain' put: true.
  p at: 'drain_requested_at' put: now
].
```

### 1.4 `validKeys := Array new: 0. validKeys := validKeys copyWith: '...'` — LOW
`WorkflowEngine.mag:593-598` — five `copyWith:` lines used to build a
literal array. Maggie supports `#('a' 'b' 'c')` literals; the existing
file already uses them in many places. Replace with a single literal.

### 1.5 `findById detect:` patterns superseded by `findByCategory:` — MED
The perf survey §2.1 noted O(n) `(scanAll:) detect:` lookups. After the
`byCatIdent` index landed (`BBS.mag:407`), several call sites still use
the old pattern:
- `src/dispatcher/WorkflowEngine.mag:248-252` — `(scanAll: 'task')
  select:` to count review tasks (still O(n), probably fine for scope-N
  but should at least scope by `bbs scan: 'task' scope: scope`).
- `src/api/Server.mag:2044-2051` — `identities detect:` over the full
  identities scan inside the per-task loop in `handleWorkflowCancel`.
  Build the lookup map once before the task loop.

**Fix:** add `BBS>>findIdentityByPubkey: pub` (uses byCatIdent or its
own pubkey index) and pre-build a pubkey→identity dict once per
`handleWorkflowCancelBody:` invocation.

### 1.6 Two `maybePromoteParent:` implementations — HIGH
`src/api/Server.mag:2300-2329` (`maybePromoteParentOf:scope:`) and
`src/dispatcher/WorkflowEngine.mag:904-930` (`maybePromoteParent:scope:`)
are essentially identical: same recursion, same allDone scan, same
upsertPinned. The only differences are 1) Server.mag updates an
`updated_at` and 2) WorkflowEngine.mag does the same. Both call into
`bbs scanChildrenOf:scope:`.

**Fix:** move the canonical implementation onto BBS or a new
`WorkitemOps` class and have both Server and WorkflowEngine call it.
This removes ~60 lines of duplicated logic, and centralises the
"derived children" semantic (Server already has a comment claiming
single source of truth — there are currently *two* sources).

### 1.7 SFRunner / RPRunner / similar test runners — MED
`test/bbs/test_bbs_sync_flush.mag:17-62` (`SFRunner`),
`test/dispatcher/test_reaper.mag:25-66` (`RPRunner`), and many
sibling test files, all define their own `assert:`, `assertEqual:`,
`report` boilerplate. These are byte-near-identical.

**Fix:** extract a `test/util/TestRunner.mag` (Object subclass with
`new`/`assert:message:`/`assertEqual:to:message:`/`report`), have each
test class subclass or instantiate it. Saves ~40 lines per test file
× ~25 test files.

### 1.8 `tokenPayload` boilerplate at 6 sites in `WorkflowEngine` — MED
`WorkflowEngine.mag:155-163, 414-422, 445-454, 461-470, 472-483` — the
same 7-line "build a token payload + bbs out: 'token' ... actor:nil
launchedBy:launchedBy executedBy:nil" sequence repeats. Differs only
in `status` ('positive' vs 'waiting') and `transition_id`.

**Fix:** `WorkflowEngine>>writeToken: status at: place transition: tid
instance: instanceId scope: scope launchedBy: launchedBy`. ~50 lines
removed and the "what makes a token tuple" contract appears in one
place.

### 1.9 Slice-by-prefix pattern: 3 different implementations — LOW
- `Scheduler.mag:248-257` — `taskIdFromCompleteEvent:` (class method,
  uses `copyFrom: prefix size to: size`).
- `Server.mag:1813` — `wid copyFrom: prefix size + 1 to: wid size` (off
  by one from the Scheduler version).
- `DashboardSSE.mag:218-220` — same pattern, indexes from `prefix size + 1`.

The perf survey §5.2 already flagged the Scheduler version; the
inconsistency between `prefix size` and `prefix size + 1` is a real
landmine — at least one of the three is wrong on either Maggie's
0-indexed or 1-indexed convention. Pick one helper
(`String>>stripPrefix: 'task-complete:'` returning the suffix or nil)
and route every call site through it.

---

## 2. Single-responsibility violations

### 2.1 `ApiServer` is the kitchen-sink class — CRIT
`src/api/Server.mag` — 2668 lines, ~120 methods, instanceVars:
`httpServer bbs port dispatcher dashboardSSE adminList skewSeconds
enforceSignatures signatureVerifier queueMaxPending inviteStore
notificationHub watchCache watchCacheExpiry`. Concerns currently
mashed together:

1. Route registration (split across 7 `register*Routes` methods only
   to evade a Maggie method-size limit — this is *layout*, not
   *design*).
2. Request-shape validation (parse JSON, type-check fields).
3. Business logic for tasks/workflows/workitems (handle*).
4. BBS-write helpers (handleBBSPut, handleBBSRm).
5. Identity bootstrap + admin TOML loading
   (`loadSecurityConfig`, `bootstrapIfNeeded`).
6. Authority-set computation for D6.15 cancellation
   (`handleWorkflowCancelBody:`, ~100 lines).
7. Affinity matching and `accept_from` enforcement
   (`affinityMatchesTask:`, `acceptFromAllows:`).
8. Notification visibility filtering (`notification:visibleTo:`).
9. Watch-cache management (`watchWorkflowsFor:`).
10. Health metrics (`computeMetrics`).
11. SSE handshake glue.
12. Workitem business rules (`validateWorkitemPayload:`,
    `maybePromoteParentOf:`).

**Fix (incremental):**
- Extract a `RouteRegistry` so registration is data-driven (table of
  `{path, method, signed?, fields, handler}`) instead of a
  hand-chained 7-link recursion. Removes the
  `registerDashboardRoutes` / `registerDashboardRoutes2` farce.
- Move `affinityMatchesTask:` / `acceptFromAllows:` /
  `repoNameMatches:inRepos:` into a new `AffinityMatcher` class. This
  also enables direct unit tests, currently only reachable via API
  e2e.
- Move `validateWorkitemPayload:` / `maybePromoteParentOf:` into a
  `WorkitemOps` class shared with `WorkflowEngine` (kills §1.6).
- Keep ApiServer as a thin route+marshalling layer.

### 2.2 `BBS` mixes index, persistence, history, and notification fan-out — HIGH
`src/bbs/BBS.mag:11` — instanceVars has 16 fields covering 4 concerns:
- tuple storage / index (`space, index, byId, byKey, byCatIdent, mutex`)
- persistence (`dataDir, dirty, flushMutex, flushInProgress`)
- audit history (`historyPath, historySize`)
- pub/sub fan-out (`notificationListener`)

**Fix:** introduce three collaborators owned by BBS — `BBSIndex`
(holds the four maps + mutex + add/remove primitives, currently lines
104-175), `BBSPersistence` (lines 612-758), `BBSHistory` (lines
761-846). The BBS facade then orchestrates and stays small. Each
collaborator becomes independently testable; the sync-flush test
already shows the persistence-only test path is straightforward
(`test/bbs/test_bbs_sync_flush.mag`).

### 2.3 `Scheduler>>dispatchTask:` does seven things — HIGH
`src/dispatcher/Scheduler.mag:58-147` — single 90-line method that
covers: (a) atomic claim, (b) logging+notify, (c) harness factory
call, (d) repo lookup + workdir resolution, (e) forked supervision
goroutine, (f) post-hoc scope validation, (g) "ensure task-complete
exists" auto-emission, (h) slot bookkeeping, (i) terminal state
update.

**Fix:** split into `claimTask:`, `prepareHarness:for:`, `runHarness:`,
`postRunValidation:`, and a small dispatcher that wires them. Also
makes the §2.4 nested-handler ladder readable.

### 2.4 `Scheduler>>validateScope:taskId:scope:` — HIGH
`Scheduler.mag:149-227` — hand-rolls **four separate** `ExternalProcess`
invocations (run `git diff`, then a second one with redirection to a
temp file in `/tmp/pp-scope-check-<taskId>`, then a `cat` via file
read, then a `rm -f`). It is also the file that the perf survey §5.4
flagged. From an SRP angle: this method opens shells, parses output,
decides what is in-scope, reverts changes, emits BBS events, *and*
sends notifications.

**Fix:** isolate the "what changed" detection into a helper that
returns a list of paths (using a single `Shell capture:` rather than
the temp-file dance), then apply policy on the result. The temp file
in `/tmp/pp-scope-check-<taskId>` is also a shared-namespace hazard
(§5.4 of perf survey, already noted).

### 2.5 `WorkflowEngine>>tryFireTransition:…overrideAffinity:` is 170 lines — HIGH
`WorkflowEngine.mag:372-541` — single method handles:
- input-place readiness check
- preconditions
- consume input tokens
- async-action branch (produces waiting tokens, runs action, fail path)
- sync-action branch (runs action, produces positive tokens, fail path)
- role-bearing branch (writes waiting tokens + a task tuple)
- role-less / action-less branch (positive tokens immediately)
- task tuple construction with affinity, timeout, fixer-context
  threading, repo, workitem, workdir, worktree-info override

**Fix:** split out `fireAsyncAction:`, `fireSyncAction:`,
`fireRoleTransition:`, `fireSilentTransition:`. The big shared
prologue (consume tokens, check preconditions) can be a setup helper
returning either `consumed` or a "skip" sentinel.

### 2.6 `BBS` has `out:` written 6× via overload chain — MED
`BBS.mag:179-200` — `out:scope:identity:payload:` →
`out:scope:identity:payload:actor:` →
`out:scope:identity:payload:actor:launchedBy:executedBy:`. Same
shape repeats for `outPinned:` (lines 202-220) and `outAffine:` (lines
222-244). 9 wrapper methods total.

This isn't strictly an SRP violation, but it's a Maggie selector idiom
that bloats the class. Two options:
- introduce a single `WriteOptions` parameter object,
- or keep the chain but auto-generate it from a macro/helper if the
  language supports it.

Probably keep as-is; flagging because the same pattern shows up in any
new write modality.

---

## 3. Sprawl / dead code / over-engineered indirection

### 3.1 `registerDashboardRoutes` / `registerDashboardRoutes2` etc. — HIGH
`src/api/Server.mag:271, 320, 339, 347, 356, 371, 372, 427, 439` — the
routes are split into a chain of 8 methods, each ending with `self
registerNextThing`, *purely* to dodge the Maggie compiler bytecode
limit. The numbered-suffix pattern (`registerDashboardRoutes2`) is a
documented anti-pattern: a numeric index in a method name almost
always means "I split this because it got big, not because the parts
are different".

**Fix:** drive registration from a literal Array of Dictionary entries
(see §2.1). One small `do:` loop fits well under any size limit.

### 3.2 `maybePromoteParentOf:` exists but the work-item endpoint also has
`updateWorkitemStatus:scope:status:` — MED
`Server.mag:2451-2462`. Used only twice
(`handleWorkitemRun:` to flip to in-progress, and indirectly inside
`handleWorkitemUpdate:`). Could be folded into the existing
`upsertPinned` flow with a small helper.

### 3.3 `Dispatcher>>expireAffineTuples` — MED (dead code)
`src/dispatcher/Dispatcher.mag:222-226`:
```
method: expireAffineTuples [
  "Scan for expired affine tuples..."
  "MVP: TupleSpace's outAffine:ttl: handles this natively."
]
```
The method body is a comment that says the method is unnecessary.
Either delete or implement. Currently called from `housekeep` (line
218) so removal needs a one-line edit.

### 3.4 `Dispatcher>>reconcile` — MED (dead code)
`Dispatcher.mag:209-212`. Same shape — empty placeholder method called
from the tick path every 6 ticks. Either implement or remove the call.

### 3.5 `instantiateWorkflow:` / `instantiate:scope:params:` overload chain — MED
`Dispatcher.mag:252-268` declares **three** entry points
(`instantiateWorkflow:scope:params:`,
`…launchedBy:`, `…launchedBy:overrideAffinity:`); the actual workflow
engine has the **same** three-method ladder
(`WorkflowEngine.mag:77-87`).
Six wrapper methods to expose three optional parameters. Drop the
back-compat ones (the only callers in `src/` are tests and other
internal sites, easy to migrate) and keep the widest signature.

### 3.6 `WorkflowEngine>>tryFireTransition:` back-compat ladder — MED
`WorkflowEngine.mag:357-371` — three back-compat entry points
(`launchedBy: nil wfAffinity: nil`, `wfAffinity: nil`, `overrideAffinity:
nil`). Grep shows zero in-tree callers of the shorter forms; they are
purely for hypothetical external callers that don't exist.

### 3.7 Three `scopeForInstance:` / `launchedByForInstance:` /
`workflowStatus:` helpers + their `*For: wf` siblings — LOW
`WorkflowEngine.mag:734-765`. The perf survey §3.2 noted that the
`*ForInstance:` versions do an extra `findByCategory:` even when the
caller has the `wf` already. The fix landed (the `*For: wf` variants
exist) but the old methods still sit there as a tempting trap. Either
delete the legacy methods or have them log a `logEngine: 'warn'` so a
profile run can surface remaining callers.

### 3.8 `handleWorkitemAddChild:` — HIGH (compat shim, marked deprecated)
`Server.mag:2366-2399` — explicitly described in the docstring as
"DEPRECATED compat shim". Endpoint exists, validates, and returns ok
without mutating anything. Plan-of-record is to remove. Add a 410
response or wire its single remaining caller to the new flow, then
drop the method + route.

### 3.9 `BBS>>workitemTuple:precedesTuple:` is unused — LOW
`BBS.mag:366-376` — provides an ordering helper but no caller exists
since `childrenOfParent:in:` was rewritten to use `sort:` directly
(line 349-362). Delete.

### 3.10 `BBS>>removeFromIndexUnsafe:` exists but only one caller — LOW
`BBS.mag:420-426`. Used in exactly one place
(`update:scope:identity:do:` at line 439). Could be inlined; the
"unsafe" affordance is a foot-gun unless the engine grows another
multi-step internal path.

### 3.11 `unused 'duplicate sweep' in outAffine:` — MED (subtle)
`BBS.mag:238`:
```
[(self inp: category scope: scope identity: identity) notNil] whileTrue: [].
```
Drains arbitrarily many tuples in a tight loop under the BBS mutex
(via inp:'s removeFromIndex:). The comment explains the intent
("collect any duplicates left behind by the legacy append path"), but
the legacy path is gone — the loop now reliably terminates after 0 or
1 iteration. Replace with a single `self inp:` to avoid surprise
quadratic worst case if a future bug ever re-introduces duplicates.

### 3.12 `validKeys` / per-key copy in `buildAffinity:` — LOW
`WorkflowEngine.mag:593-604` — the entire function reduces to "copy
known keys from `source` into `result`". A single
`['team' 'launcher_only' 'workers' 'model' 'repo'] do: [:k | …]`
literal saves the array gymnastics (overlaps with §1.4).

### 3.13 `cueCtx` instance var in BBS but never used — LOW
`BBS.mag:11, 30`. `cueCtx := CueContext new` runs in `initWithDataDir:`
but nothing else in BBS.mag reads `cueCtx`. The `templateFor:` method
constructs a fresh local `CueContext` each call (line 100). Remove
the unused field.

### 3.14 `unauthedPostAndPrint:` etc. variants in CliPP — LOW
`src/cli/CliPP.mag:1241, 1285, 1296, 1303, 1309` — five HTTP helpers
(`postAndPrint:`, `postWorkflowStart:`, `unauthedPostAndPrint:`,
`signedPost:`, `unauthedPost:`, `unauthedGet:`). These overlap;
consolidating into one with an option dict reduces a 100-line block
of near-duplicate request boilerplate.

### 3.15 `handleNotificationStream` "wait>0" path runs even when wait=0 — LOW
`Server.mag:1891-1906`. Sentinel handling fine, but the long-poll
`if (filtered isEmpty and: [wait > 0])` path is exercised only by the
client passing `wait=1`. If `wait=0` was made the explicit "single-poll"
mode the dead branch in client code goes away. Minor, but the
NotificationHub recently changed and the test surface here is thin.

---

## 4. Testing gaps

### 4.1 No tests for the BBS index hash maps — CRIT
`src/bbs/BBS.mag:121-175` (`addToIndices:`, `removeFromIndicesById:`,
`removeFromIndicesByKey:`, `findInIndex:`, `findByCategory:`).
This is a recently-changed hot path (per perf survey §1.1 it is the
core of the new perf strategy). The only tests that touch the BBS
index do so end-to-end via `test/bbs/test_bbs_sync_flush.mag` which
uses `scan:` not `findInIndex:` or `findByCategory:`. No direct
coverage for:
- byKey map staleness after `removeFromIndicesById:`
- byCatIdent fallback when scope is unknown (the whole point of the
  index)
- multiple tuples at the same key (post-`upsertPinned` consume drain)
- empty-array branch in `findByCategory:` after the last tuple is
  removed

**Fix:** new `test/bbs/test_bbs_index.mag` exercising add/remove/
upsert/scan invariants. Each invariant maps to one BBS method —
should be ~150 lines.

### 4.2 `flushAsyncIfDirty` has zero tests — CRIT
`BBS.mag:734-758`. The fence-bit + dirty-flag race protocol (sync
flushIfDirty races against async flushAsyncIfDirty races against
another flushIfDirty) is exactly the kind of code that humans get
wrong. Tests cover `outSync:`/`inpSync:` (sync path) but the *async*
fence is not exercised at all.

**Fix:** add a test that:
- writes a durable tuple (sets dirty)
- calls `flushAsyncIfDirty` twice in a tight loop and verifies only
  one fork actually runs
- asserts that a `flushIfDirty` issued mid-flight waits for the async
  flush to complete (no .tmp/.json clash)
- simulates `saveToDisk` failure → asserts dirty is re-set so the
  next tick retries.

Hard to do cleanly without a `saveToDisk:hook` injection point —
might need a small refactor to inject the writer block.

### 4.3 `Dispatcher>>reapTask:` partial coverage — HIGH
`test/dispatcher/test_reaper.mag` covers RP1-RP7 (single-task reap
flow). Missing scenarios:
- Two expired tasks in the same `reapExpiredClaims` pass: today the
  loop is `tasks do: [:t | self reapTask: t now: now]` — if `reapTask:`
  throws on tuple #1 the rest are skipped (no inner `on: Exception`).
  Add a test where one task has a malformed payload and verify the
  others still get reaped.
- Concurrency: a worker `renew`s the lease *between* `scanAll: 'task'`
  and the `update:` block's status check. The atomic `update:` block
  re-checks (`Dispatcher.mag:163`) but no test asserts that the renew
  wins.
- Cap-bypass: `missed_renewals=2, retry_count=max-1` should land a
  re-pend (re-pend, then next reap will fail it). No coverage of the
  off-by-one boundary.

### 4.4 NotificationHub fan-out has no tests — CRIT
`src/api/NotificationHub.mag` is brand-new (from the perf survey §2
fix) and replaces the long-poll scan loop. Zero direct tests:
- subscribe/publish/drain ordering
- filter exception swallowed by `[matches := f value: tuple] on:
  Exception …` (line 76)
- unsubscribe under contention (publish concurrent with unsubscribe)
- subscribers Array growth/shrink (currently `copyWith:` /
  `reject:` — O(n) on each mutation; add a test asserting the count
  is correct after 1000 subscribe/unsubscribe pairs).

**Fix:** `test/api/test_notification_hub.mag` with a 4-subscriber
fixture. ~80 lines.

### 4.5 `BBS>>history:` filter combinations untested — MED
`BBS.mag:848-900` — supports `op`, `category`, `scope`, `identity`,
`since`, `limit` filters that AND together. `test/test_history.mag`
exists but covers the smoke path. Missing:
- empty filter combinations (only `since`, only `limit`, etc.)
- the fast-path `historyTail:` branch (`size = 1 and limit notNil`)
  vs the full-load branch — both should yield identical results for
  large logs.
- the `since` boundary (>= vs >).
- post-rotation behaviour (`.1` file is not consulted; that's the
  documented behaviour, but no test pins it).

### 4.6 Server `handleNotificationStream:` end-to-end — HIGH
`Server.mag:1859-1925` is the long-poll endpoint that the
NotificationHub backs. No integration test simulates:
- two clients subscribed with different identities and disjoint
  watch sets — both must see only their own notifications.
- the timeout path (`i < 100` budget exhausted) returning empty.
- mid-poll new-watch tuple — currently designed *not* to surface
  until the next request (line 1890), but no test pins that
  semantics.

### 4.7 `handleWorkflowCancelBody:` D6.15 authority — MED
`Server.mag:1980-2082`. `test/api/test_workflow_cancel.mag` exists,
14kB; spot-check shows it covers happy paths and reason validation.
Missing edge cases:
- operator authority via active claim (path 2 in the docstring at
  line 2026).
- cascading cancel sees a child workflow already in `failed` (line
  1003 should skip).
- admin override path when actor is `admin:<hex>` and not in the
  computed `authoritySet`.

### 4.8 Workitem auto-promotion in two places: only one tested — MED
`src/api/Server.mag:2300-2329` and
`src/dispatcher/WorkflowEngine.mag:904-930` (see §1.6 above) both
recurse upward via `maybePromoteParent[Of:]`. Tests in
`test/test_workitem_parent_child.mag` exercise the workitem-update
path, but the WorkflowEngine path (when a workflow completes and the
linked workitem is the last child of an epic) isn't directly tested.

### 4.9 `NotificationHub` filter exception handling — MED
`NotificationHub.mag:76` swallows filter exceptions — no test asserts
that a buggy filter doesn't kill *other* subscribers' delivery on the
same publish. Important: this is the explicit guarantee in the
docstring.

### 4.10 Affinity matching unit tests are absent — MED
`Server.mag:1072-1123` (`affinityMatchesTask:manifest:workerId:`).
Tested only via `test/api/test_task_affinity.mag` — and that pins the
HTTP-shape behaviour, not the matrix of affinity shapes (`team`,
`launcher_only`, `workers[]`, `model`, `repo` with/without `@commit`,
combinations). After moving to `AffinityMatcher` (§2.1) make this a
matrix test.

### 4.11 `RuleEngine>>tryFire:` cooldown / scope-match — MED
`src/dispatcher/RuleEngine.mag` has zero direct test files in
`test/dispatcher/`. Hot-coded behaviour:
- `scope_match: 'same'` rejects firing when consumed tuples span
  scopes (line 73-76).
- cooldown gates re-fire (line 38-42).
- partial-consumes failure short-circuits cleanly.
None covered.

### 4.12 `SignatureVerifier>>verifySSE:` path — LOW
`Server.mag:1781, 2552`. Only covered indirectly. Verifier tests
(`test/test_signature_verifier.mag`) exist but it's worth pinning the
SSE-specific header arrangement.

---

## 5. Recommended triage order

Ordered by (a) risk if neglected, (b) leverage of the change.

1. **Add tests for `flushAsyncIfDirty` and the BBS index** (§4.1, §4.2).
   These are recently-changed hot paths and a regression here is silent
   data loss or O(n²) lookup. Get a safety net before further refactor.
2. **Add a NotificationHub test file** (§4.4). Same logic — newly
   landed, zero coverage, replaces a known-fragile long-poll.
3. **Extract `requireVerified:for:` and `requireWorkerActor:name:verb:`
   helpers in Server.mag** (§1.1, §1.2). 6 mechanical sites; trivially
   reviewable; fixes drift between the three error strings.
4. **Delete `expireAffineTuples`, `reconcile`, `workitemTuple:precedes...`,
   the back-compat overload ladders, the deprecated
   `handleWorkitemAddChild:`** (§3.3, §3.4, §3.5, §3.6, §3.8, §3.9).
   Pure removal, gets the surface area down before doing the larger
   refactors.
5. **Pull `maybePromoteParent[Of]` into a single shared helper** (§1.6,
   §4.8). Two implementations of one rule = bug factory.
6. **Add the missing reaper edge-case tests** (§4.3) — guards the
   `reapExpiredClaims` loop against poison tuples.
7. **Refactor `dispatchTask:` and `validateScope:`** (§2.3, §2.4). Big
   readability win in a method that supervises every harness fork —
   this is also where the perf survey's untimed-shell hazards live.
8. **Refactor `tryFireTransition:`** (§2.5). Largest method in the
   workflow engine; splitting opens the door to separate sync vs.
   async-action tests.
9. **Introduce `BBSIndex` / `BBSPersistence` / `BBSHistory`
   collaborators** (§2.2). Touch a lot of files but each change is
   mechanical once the seam is in place; large gain in testability.
10. **Drive route registration from a table** (§3.1, §2.1). Removes the
    `registerDashboardRoutes2` chain and unblocks adding
    middleware-style cross-cutting concerns later (auth, rate-limit,
    metrics).
11. **Extract `AffinityMatcher`, `WorkitemOps`** (§2.1, §4.10). Sets
    up matrix unit tests for affinity and unifies workitem semantics.
12. **Test runner consolidation** (§1.7). Cosmetic; do alongside any
    test file you touch. Don't do as a single sweep — rebase pain.

---

## Appendix — file:line index of key findings

```
Code duplication
  Auth preamble (§1.1):            Server.mag:601, 672, 795, 920, 1156, 1237
  Worker actor guard (§1.2):       Server.mag:617, 811, 945
  Payload clone+merge (§1.3):      Server.mag:628, 824, 2263, 2284, 2322, 2356, 2457, 2607, 2657
                                   WorkflowEngine.mag:896, 924
  Two maybePromoteParent (§1.6):   Server.mag:2300  WorkflowEngine.mag:904
  Token-write boilerplate (§1.8):  WorkflowEngine.mag:155, 414, 445, 461, 472
  Slice-by-prefix (§1.9):          Scheduler.mag:248  Server.mag:1813  DashboardSSE.mag:218

SRP violations
  ApiServer kitchen-sink (§2.1):   Server.mag (entire 2668-line file)
  BBS multi-concern (§2.2):        BBS.mag:11 (instanceVars line)
  dispatchTask: (§2.3):            Scheduler.mag:58-147
  validateScope: (§2.4):           Scheduler.mag:149-227
  tryFireTransition: (§2.5):       WorkflowEngine.mag:372-541

Sprawl / dead code
  registerDashboardRoutes2 (§3.1): Server.mag:347-356
  reconcile / expireAffine (§3.3-4): Dispatcher.mag:209, 222
  Overload ladders (§3.5-6):       Dispatcher.mag:252  WorkflowEngine.mag:357
  Deprecated add-child (§3.8):     Server.mag:2366
  Unused workitem helper (§3.9):   BBS.mag:366
  Cue-Ctx unused field (§3.13):    BBS.mag:11, 30

Testing gaps
  BBS index (§4.1):                BBS.mag:121-175  (no test/bbs/test_bbs_index.mag)
  flushAsyncIfDirty (§4.2):        BBS.mag:734-758
  reapTask poison-tuple (§4.3):    Dispatcher.mag:131-141
  NotificationHub (§4.4):          src/api/NotificationHub.mag (no tests)
  history filter combos (§4.5):    BBS.mag:848-900
  notification stream e2e (§4.6):  Server.mag:1859-1925
  workflow-cancel authority (§4.7): Server.mag:1980-2082
  RuleEngine (§4.11):              src/dispatcher/RuleEngine.mag (no tests)
```
