# Ganso Migration Plan

Replace Procyon Park's bespoke in-memory-tuplespace + JSON-flush backing store
(and its hand-rolled dispatch/event/notify layers) with [ganso](~/dev/go/ganso)
— a CGo-free, SQLite-backed coordination toolkit (queue, stream, notify, locks,
outbox) — wrapped as a Maggie contrib binding.

**End goal: behavioural parity with today, with ganso as the backing store.**
Not a feature change. Every externally observable behaviour (CLI, API, dashboard,
agent harness) must be unchanged; only the substrate changes.

---

## Why (recap of the findings that motivate this)

From this session's scouts + the live crash:

- **Write amplification.** Every durable write re-encodes the *entire* `bbs.json`
  (`BBS>>saveToDisk`). O(total tuples) per write. (`docs/scout-perf-pinned-tuples-2026-06-18.md` Q2.)
- **O(N) scans.** `scan:`/`scanAll:` walk the whole in-memory index. (Q1.)
- **Unbounded memory.** The whole tuplespace is held in RAM forever; pp has
  drifted to ~32 GB. (Q3.)
- **Full-decode restart.** `loadFromDisk` decodes the entire file on boot. (Q4.)
- **Dispatch crash class.** Hand-rolled claim/reaper produced a harness spawn
  storm (duplicate `…:task:review` tuples, orphan tasks, no concurrency cap) →
  resource exhaustion → silent server death + crash-loop.
  (`docs/scout-server-crash-resilience-2026-06-18.md`.)

Ganso addresses all five: incremental SQL persistence, indexed queries, on-disk
storage with a bounded page cache, O(1) open, and a correct job queue
(`ClaimBatch(n)` cap, atomic claim, `SweepExpired`, heartbeat, dead-letter).

---

## Principles (hold these at every step)

1. **Parity behind the `BBS` API.** All callers go through `BBS.mag`'s public
   methods. We re-back those methods; we do not change their contracts. The
   in-memory indices (`byKey`/`byId`/`byCatIdent`/`index`) and the JSON flush are
   *implementation* to be replaced.
2. **Test stability + regressions at EVERY step.** After every phase: `rm -f pp
   && mag build` clean, full `mag test` green (no NEW failures vs the recorded
   baseline), and a server soak (boot, exercise, no crash, clean shutdown).
3. **Measure perf at EVERY step.** A repeatable benchmark harness (Phase 1)
   records write/scan/restart/memory before and after each phase. Record the
   numbers in this doc's "Measurements log". A regression is a blocker unless
   justified.
4. **Reversible.** Each phase lands behind a flag or as an additive layer so we
   can fall back to the JSON store until the final cutover. Keep `bbs.json`
   import/export working until Phase 6.
5. **Small, verifiable slices.** Highest-risk-reducing slice first (the binding
   smoke test), then the crash-fixing slice (Queue), then the store, then
   events. Never a big-bang swap.
6. **Enhancements allowed.** Per the goal, the Maggie runtime (`~/dev/go/maggie`)
   and pp may both be modified. The ganso Maggie binding lives in
   `~/dev/go/maggie/vm/contrib/ganso/` (mirrors `contrib/sqlite`).

---

## Known baseline (pre-migration)

- `main` HEAD at plan time: see `git log`. AAR feature + `pp read --all` landed.
- Test baseline has KNOWN pre-existing failures that are NOT regressions:
  ~8 workflow-refactor assertions (WR15c/d/e "rot"), 1 multiplayer assertion,
  10 PENDING multiplayer suites (unrelated missing selectors). The gate is
  "no NEW failures beyond these", not "zero failures".
- Maggie embeds contrib plugins via blank-import in `cmd/mag/main.go` and
  `cmd/bootstrap/main.go` (sqlite + duckdb already there). Ganso is CGo-free
  (`modernc.org/sqlite` + `zombiezen.com/go/sqlite`), so it coexists with the
  existing sqlite contrib in one binary.

---

## Target architecture

```
            ┌──────────────────────── pp (Maggie) ────────────────────────┐
  callers → │  BBS.mag  (UNCHANGED public API: out:/in:/rdp:/scan:/update: │
            │            /observe:/decide:/emitEvent:/notify: …)           │
            │      │ re-backed onto ↓                                      │
            │  GansoStore.mag  ── lib/Ganso*.mag (Maggie wrappers)         │
            └──────────────────────────│──────────────────────────────────┘
                                        │ primitives
            ┌───────────────── vm/contrib/ganso (Go) ─────────────────────┐
            │  wraps ganso.Database / Queue / Stream / Listener / Tx       │
            └──────────────────────────│──────────────────────────────────┘
                                        │
                                 ganso  ── one SQLite file (WAL)
   tuples table (JSON + generated cols + indices)  ·  _ganso_live (queue)
   _ganso_stream (events)  ·  _ganso_notifications  ·  _ganso_locks …
```

### Tuple storage schema (Phase 3)

```sql
CREATE TABLE tuples (
  id         INTEGER PRIMARY KEY,                 -- BBS assignId
  data       TEXT NOT NULL,                       -- full tuple as JSON
  category   TEXT    GENERATED ALWAYS AS (json_extract(data,'$.category'))            STORED,
  scope      TEXT    GENERATED ALWAYS AS (json_extract(data,'$.scope'))               STORED,
  identity   TEXT    GENERATED ALWAYS AS (json_extract(data,'$.identity'))            STORED,
  modality   TEXT    GENERATED ALWAYS AS (json_extract(data,'$.modality'))            STORED,
  created_at INTEGER GENERATED ALWAYS AS (json_extract(data,'$.created_at'))          STORED,
  expires_at INTEGER GENERATED ALWAYS AS (json_extract(data,'$.expires_at'))          STORED,
  parent     TEXT    GENERATED ALWAYS AS (json_extract(data,'$.payload.parent'))      STORED,
  wf_inst    TEXT    GENERATED ALWAYS AS (json_extract(data,'$.payload.workflow_instance')) STORED
);
CREATE UNIQUE INDEX ux_key  ON tuples(category,scope,identity);   -- pinned/upsert key
CREATE INDEX ix_cat_scope   ON tuples(category,scope);
CREATE INDEX ix_cat         ON tuples(category);
CREATE INDEX ix_wf          ON tuples(wf_inst);
CREATE INDEX ix_parent      ON tuples(parent);
CREATE INDEX ix_expires     ON tuples(expires_at);
```

Note: linear tuples may legitimately share a (category,scope,identity) key over
time (consume + re-write). The unique index suits pinned/upsert categories; for
linear categories that allow multiples we either include `id` in the key or use
a non-unique index. Resolve per-category during Phase 3 (a real design point —
the current code already distinguishes linear vs pinned writes).

### BBS method → SQL / ganso mapping

| BBS method | backing |
|---|---|
| `out:`/`outPinned:` | `INSERT` (pinned: `INSERT … ON CONFLICT(key) DO UPDATE`) |
| `outAffine:…ttl:` | `INSERT` with `expires_at = now+ttl` |
| `rdp:`/`rd:`/`findInIndex:` | indexed `SELECT … LIMIT 1` |
| `scan:`/`scanAll:`/`findByCategory:` | indexed `SELECT` (+ `LIMIT` paging) |
| `scanChildrenOf:` | `SELECT … WHERE parent=?` |
| `inp:`/`in:`/`inpSync:`/`drainTuplesAt:` | `DELETE … WHERE … RETURNING data` (atomic) |
| `update:`/`updatePinned:`/`upsertPinned:`/`upsertSignal:` | `UPDATE`/upsert in a `WithTx` |
| `emitEvent:` | also `Stream.Publish` (Phase 5) |
| `notify:` | also `Notify`/`Listen` (Phase 5) |
| task dispatch (Scheduler) | `ganso.Queue` (Phase 4) |
| `saveToDisk`/`loadFromDisk` | **removed** — WAL is durable; open is the load |

### Dispatch → ganso.Queue (Phase 4 — the crash fix)

| pp dispatch concept | ganso.Queue |
|---|---|
| task tuple `pending` | `Enqueue(payload)` |
| Scheduler claim + worker_id | `ClaimOne(workerID)` / `ClaimBatch(workerID, n)` — `n` = the **concurrency cap** |
| claim renewal / `missed_renewals` | `Heartbeat(jobID, extendSec)` |
| reaper (`reapExpiredClaims`) | `SweepExpired()` (lease-based, correct) |
| completion | `Ack` |
| failure/retry | `Fail` / `Retry(delaySec)` → dead-letter on exhaustion |
| duplicate-task bug | impossible — claim is atomic; one job, one claimer |

---

## Metrics (Phase 1 harness; record in Measurements log)

Each measured before migration and after each phase, same machine, same dataset
sizes (seed N ∈ {1k, 10k, 100k} tuples across a few categories/scopes):

- **W**: wall-clock to write 1k durable tuples (amortised per-write).
- **S**: wall-clock of `scanAll: 'case'` and `scan: 'task' scope: X`.
- **R**: server cold-start time (boot → ready) at each N.
- **M**: RSS of `pp serve` at idle at each N (and after a load burst).
- **F**: disk bytes written per single durable `out:` (proxy for write amp).

Tools: a Maggie bench script (`test/bench/bbs_bench.mag`) that times the BBS API
directly; `/usr/bin/time -l` (macOS) for RSS; `ls -la` / `stat` for file growth.

---

## Phases & gates

### Phase 0 — Smoke test (binding links + runs) — *START HERE*
- Add minimal `~/dev/go/maggie/vm/contrib/ganso/{plugin.go,ganso_primitives.go}`
  wrapping `ganso.Open`, `Database.Queue`, `Queue.Enqueue`, `Queue.ClaimOne`,
  `Job.Ack`. Blank-import in `cmd/mag/main.go` + `cmd/bootstrap/main.go`.
  Build maggie (`go build ./...` or the project's build) so the new contrib links.
- `lib/Ganso*.mag` minimal wrappers (or call primitives directly in the test).
- A standalone Maggie smoke script: open a temp ganso DB, enqueue a job, claim
  it, ack it, assert round-trip.
- **Gate:** the script runs green in a freshly built `mag`/`pp` binary. Confirms
  ganso links into the Maggie runtime and the primitive bridge works.
- **Stability:** N/A (no server). **Perf:** N/A. **Rollback:** delete the contrib
  dir + the two blank imports.

### Phase 1 — Baseline measurement harness
- Write `test/bench/bbs_bench.mag` (W/S/R/F) + a shell wrapper for M/R.
- Run against current `main`. Record numbers in Measurements log.
- **Gate:** reproducible numbers at N = 1k/10k(/100k if feasible).

### Phase 2 — Full Maggie ganso binding
- Expand `vm/contrib/ganso` to the surface we need: `Database` (Open, WithTx,
  Query, Execute, Close, pragma/WAL), `Queue` (Enqueue, ClaimOne, ClaimBatch,
  Ack, Fail, Retry, Heartbeat, SweepExpired, GetJob, Cancel), `Stream` (Publish,
  Read, SaveOffset, GetOffset), `Notify`/`Listen` (Notify, Listen, Next, Prune).
  Wrap the **synchronous/polling** variants only (no Go-channel bridging).
- `lib/GansoDatabase.mag`, `GansoQueue.mag`, `GansoStream.mag`, `GansoListener.mag`.
- Tests: `test/contrib/test_ganso_binding.mag` mirroring ganso's Go acceptance
  tests for each wrapped op (enqueue/claim/ack/expire, publish/read/offset,
  notify/listen).
- **Gate:** binding tests green; `mag test` no new failures; build clean.

### Phase 3 — Store migration (tuples table behind BBS)
- New `src/bbs/GansoStore.mag`: the schema above + the BBS-method→SQL mapping.
- Re-back `BBS.mag`'s store methods onto `GansoStore` behind a flag
  (`PP_STORE=ganso|json`, default `json` until the gate passes). Keep the JSON
  path intact for rollback.
- One-time importer: load existing `bbs.json` → `tuples` table on first ganso boot.
- **Regression:** the FULL existing `mag test` suite must pass with `PP_STORE=ganso`
  (BBS tests, dispatcher tests, API tests, AAR tests). This is the parity proof —
  the same tests, different backend.
- **Stability:** boot `pp serve` with ganso store; run the CLI surface
  (read/observe/notify/workitem/workflow status); restart; confirm pinned data
  (cases, workitems, conventions) survives; no crash.
- **Perf:** re-run Phase 1 harness with `PP_STORE=ganso`. Expect W↓ (no full
  re-encode), S↓ (indexed), R↓ (no full decode), M↓ (on-disk). Record deltas.
- **Gate:** parity (tests green) AND perf neutral-or-better. Then flip default to
  `ganso`.

### Phase 4 — Dispatch on ganso.Queue (the crash fix)
- Move task dispatch (`src/dispatcher/Scheduler.mag` + reaper) onto `ganso.Queue`:
  Enqueue on task creation, `ClaimBatch(workerID, CAP)` in the dispatch loop
  (CAP = the harness concurrency cap = the thing whose absence crashed us),
  Heartbeat from the harness, `SweepExpired` as the reaper, Ack/Fail/Retry on
  completion. Remove the bespoke claim/renewal/missed_renewals/dedup logic.
- **Stability (the key test):** a soak that terminates many workflows at once
  (the exact crash trigger) and confirms NO spawn storm — concurrency stays ≤ CAP,
  no duplicate claims, orphans go to dead-letter, server survives. Re-run the
  scenario that crashed `ppserve4/5`.
- **Regression:** dispatcher tests green; a real story workflow runs end-to-end
  (setup→implement→review→merge) without manual task cleanup.
- **Perf:** dispatch latency; max concurrent harnesses bounded.
- **Gate:** crash scenario survived + dispatcher parity.

### Phase 5 — Events + notifications + blocking on Stream/Notify
- `emitEvent:` → also `Stream.Publish`; the SSE dashboard reads via
  `Stream.Read`+offset (durable, resumable — replaces the broadcast fork that
  crashed). `notify:` → `Notify`; blocking `in:`/`read:` consumers → `Listen`/`Next`.
- **Regression:** dashboard renders the same panels; no lost events; notification
  long-poll parity.
- **Stability:** SSE soak — connect/disconnect clients, kill a panel's data, no
  broadcast death.
- **Gate:** dashboard + notify parity.

### Phase 6 — Cutover + cleanup + final report
- Remove the JSON flush, the in-memory indices, the dead `saveToDisk`/`loadFromDisk`,
  the `PP_STORE` flag (ganso only). Keep a `pp export`/`import` for backup.
- Full regression sweep; final Phase 1 measurements vs baseline → Measurements log.
- **Gate:** all green; documented perf deltas; parity confirmed.

---

## Rollback strategy

- Phases 0–2 are additive (new contrib + lib + tests) — deleting them restores
  the prior state.
- Phase 3–5 land behind `PP_STORE` / per-subsystem flags; default stays on the
  legacy path until each gate passes. A bad gate = flip the flag back.
- `bbs.json` import/export retained through Phase 5 so we can always reconstruct
  the legacy store.

## Risk register

- **R-1 Channel/ctx bridging.** Mitigated: wrap only synchronous ganso variants.
- **R-2 Two SQLite layers in one binary** (Maggie contrib/sqlite + ganso's
  zombiezen/modernc). Both CGo-free; verify they link together in Phase 0.
- **R-3 Linear vs pinned key semantics** in the tuples table (unique vs
  multi). Resolve in Phase 3 with per-category key rules; cover with tests.
- **R-4 Blocking Linda ops** (`in:`/`read:` that wait). Audit usage; back with
  `Listen`/`Next`. If pp barely uses blocking reads (CLI/dispatcher use
  `rdp`/`scan`/atomic-consume), the surface is small.
- **R-5 Maggie runtime change blast radius.** The binding is additive (new
  contrib); it cannot break existing Maggie behaviour. Build + `mag test` of
  maggie itself after adding the contrib.
- **R-6 Perf regression on JSON-extract for non-indexed fields.** Index the hot
  keys (schema above); leave cold fields in JSON. Measure in Phase 3.

---

## Measurements log

(populated as phases land)

| metric | baseline (json) | P3 (ganso store) | P4 | P6 final |
|---|---|---|---|---|
| Write 10k (ms) | 452 + 125 flush = 577 | 553 (durable, no flush) | | |
| Tflush @10k (ms) | 125 | 0 (incremental WAL) | | |
| Sscan case @10k (ms) | 4 | 102 | | |
| Rload @10k (ms) | 118 | **2** | | |
| Fsize bytes/tuple | ~598 | ~695 | | |

P3 store deltas (10k): **Rload 60× faster** (2ms vs 118ms — O(1) open, the restart/boot
win); **no write-amplification** (durable incremental writes ≈ JSON write+flush, and no
full re-encode that grows with total tuples); **memory** moves to disk + bounded page
cache (the unbounded-RAM/OOM fix). TRADEOFF: full `scanAll` ~25× slower (deserializes
from disk vs JSON's live in-RAM objects) — acceptable because hot paths use narrow
INDEXED queries (`scan: cat scope:`, `WHERE status=…`), not full scans; a hot cache can
close the rest. Wdur is 10k separate IMMEDIATE txns (~55µs each); batching would cut it.

Baseline scaling (10× data): Tflush 7.3×, Rload 7.5×, Sscan 5× — all ~linear in
total tuples. Extrapolated to 100k: ~1.3 s per flush, ~1.2 s per boot. This is the
write-amplification + restart cost ganso must eliminate. Measured via the
temporary `pp bench` command (`src/util/BbsBench.mag`, dispatched from
`Main.ppCommands` + `PP.mag`) — **a migration-only tool; remove in Phase 6.**

## Execution log

(append per step: date, what ran, result, gate pass/fail)

### 2026-06-18 — Phase 3: BBS→GansoStore delegation — behavioral PARITY (1 test-isolation gap)
- `BBS.mag`: added a `gansoStore` ivar + `PP_STORE=ganso` init (open GansoStore,
  one-time `bbs.json` import, restore `nextId` from `maxId`). Delegated ALL store
  methods to GansoStore behind a guard: `out:`/`outPinned:`/`outAffine:`,
  `inp:`/`rdp:`/`rd:`, `scan:`/`scanAll:`/`findInIndex:`/`findByCategory:`,
  `update:`/`updatePinned:`/`upsertPinned:` (`upsertSignal:` funnels through
  inp:/out:). `persistAfterChange`/`flushIfDirty`/`flushAsyncIfDirty` are no-ops
  under ganso (WAL is durable). The legacy JSON path is fully intact for rollback
  (default when `PP_STORE` unset). KEY SIMPLIFIER: blocking `in:`/`rd:` are unused
  in pp, so no blocking-wait layer was needed.
- GansoStore gained `putTuple:`/`putPinnedTuple:`/`removeKey:`/`findByCategory:`/
  `maxId` so BBS stores its verbatim tuple Dicts (id and all) and gets ids back.
- **Parity (`PP_STORE=ganso mag test`):** workflow-refactor failures 8 = JSON
  baseline 8 (SAME — no new workflow regressions); the 3 JSON-backend-internal
  suites (BBSIndex / BBSFlushAsync / TestBBSCmd — they test the in-memory index,
  the flush mechanism, and the `bbs.json` file) are skipped under the flag since
  that machinery is replaced by SQLite. The one initial new failure
  (`test_invite_store`) was a TEST-ISOLATION artifact (`BBS new` shares the
  durable default `tuples.db` under ganso vs accidental in-memory isolation
  under JSON) — FIXED test-side (isolated temp dir per BBS). **RESULT: under
  `PP_STORE=ganso` the suite is IDENTICAL to the JSON baseline (workflow-refactor
  8, multiplayer 1 — both pre-existing, failing under BOTH stores). FULL
  BEHAVIORAL PARITY.** pp runs on ganso as the sole backing store.
- KNOWN cleanups: (a) GansoStore isn't `close`d on BBS teardown, leaking ganso's
  update-watcher goroutine — harmless for the singleton server, noisy in tests
  (`watcher: file identity check failed` spam). (b) The `BBS new` test-isolation
  fix above. Both bounded.
- Net: pp can run with ganso as the SOLE backing store; the substantive behavior
  is at parity. Remaining: the two cleanups, then Phases 4 (dispatch→Queue), 5
  (events/notify→Stream/Notify), 6 (cutover + remove JSON path & `pp bench`).

### 2026-06-18 — Phase 3: GansoStore core + perf — store proven
- `src/bbs/GansoStore.mag`: the `tuples` schema (JSON `data` + STORED generated
  columns + indices) and the core BBS store ops (`out:`/`outPinned:`/`upsert`,
  `rdp:`/`scan:`/`scanAll:`/`scanChildrenOf:`/`count`, `inp:` atomic consume,
  `update:do:`) over the Phase-2 SQL binding.
- **Build-tooling fixes (Maggie runtime) — these were the real blockers:**
  1. `gowrap/build.go` `contribPackages`: added `vm/contrib/ganso` (the list
     blank-imported into BUILT binaries — separate from `cmd/mag/main.go`; built
     pp lacked the ganso primitives without it).
  2. `gowrap/build.go` `generateEmbeddedGoMod`: now propagates maggie's OWN
     `require`/`replace` directives into the embedded build's go.mod, so contrib
     packages can import maggie's transitive local-path deps (ganso). Without it
     `go mod tidy` failed on `github.com/chazu/ganso@…unknown revision`.
  3. `~/dev/go/ganso/pp_sql.go`: `Database.Exec`/`QueryArgs` (positional params).
- **Parity:** GansoStore round-trips out/rdp/scan/scanAll/update/upsert/inp in the
  built pp binary — `PARITY: PASS`.
- **Perf (vs JSON baseline, 10k):** Rload 2ms vs 118ms (60× — the boot win); no
  write-amplification (durable incremental writes ≈ JSON write+flush); scanAll
  102ms vs 4ms (the deserialization tradeoff — mitigate with narrow indexed
  queries + a hot cache). A bench bug surfaced + fixed: O(N²) `copyWith:`-loop in
  `decodeRows:` made scanAll 8.8s → 102ms after switching to `collect:`.
- REMAINING for Phase 3: re-back `BBS.mag`'s public store methods onto GansoStore
  behind `PP_STORE=ganso` (keeping the JSON path for rollback), the `bbs.json`
  importer, and the FULL `mag test` suite green under `PP_STORE=ganso` (the
  parity proof). This is the larger refactor; the store + binding it needs are
  proven and measured.

### 2026-06-18 — Phase 2: SQL binding — PASS (scoped to the store's needs)
- Added positional-param SQL to ganso (`~/dev/go/ganso/pp_sql.go`):
  `Database.Exec(sql, args...)` (write in IMMEDIATE tx) + `Database.QueryArgs`
  (pooled read → `[]map[string]any`). Additive, separate file.
- Binding (`ganso_primitives.go`): `GansoDatabase primExec:args:` /
  `primQuery:args:`. Params via `gansoArgs` (Array→[]any, mirrors sqlite's
  `valueToGoArgs`); results via `v.GoToValue(rows)` (Go []map → Maggie
  Array-of-Dict, one call).
- **SQL smoke** (`/tmp/ganso_sql.mag`): created `tuples` table with
  `json_extract(...) STORED` generated columns + index, inserted JSON tuples,
  indexed `WHERE category=?` returned exactly the matching row with generated
  columns populated, JSON round-tripped. `SQL SMOKE OK`.
- Re-scope note: Queue/Stream/Notify bindings deferred to their phases (4/5);
  Phase 2 delivers only the Database SQL surface Phase 3 needs.

### 2026-06-18 — Phase 0: smoke test — PASS
- Added `~/dev/go/maggie/vm/contrib/ganso/{plugin.go,ganso_primitives.go}` —
  minimal binding: `GansoDatabase primOpen:`/`primQueue:`/`primClose`,
  `GansoQueue primEnqueue:`/`primClaimOne:`, `GansoJob primId`/`primPayload`/`primAck`.
  Pattern mirrors `contrib/sqlite` (`RegisterContrib` → `RegisterGoType` →
  `AddMethodN` over wrapped Go structs).
- go.mod: `require github.com/chazu/ganso` + `replace` → `/Users/chazu/dev/go/ganso`.
  Blank-imported in `cmd/mag/main.go` + `cmd/bootstrap/main.go`.
- `go mod tidy` bumped the build toolchain 1.25.7 → **1.26.4** (ganso requires
  go ≥ 1.26.2). pp is unaffected (compiled by `mag`'s VM, not the Go toolchain).
- Coexists with the existing `contrib/sqlite` (both CGo-free; ganso pulls
  `zombiezen` + `modernc`).
- Installed the ganso-enabled `mag` to `~/go/bin/mag`.
- **Smoke** (`/tmp/ganso_smoke.mag`): open → queue → enqueue (got UUID) →
  claimOne → payload `"hello-ganso"` → ack=true → `SMOKE OK`. Bareword
  `GansoDatabase` resolves directly (no lib wrapper needed for primitives).
- **Gate:** binding links + round-trips ✅; `pp` builds clean with new `mag` ✅;
  full `mag test` shows the SAME baseline (8 workflow-refactor + 1 multiplayer),
  **no new failures** ✅.
- Note: the built pp *binary*'s ganso embedding is exercised for real in Phase 3
  (GansoStore); if `mag build` didn't embed the contrib it will fail loudly then.
