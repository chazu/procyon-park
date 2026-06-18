# Scout: performance under unbounded growth of pinned/durable tuples

Date: 2026-06-18
Scope: `src/bbs/BBS.mag`, `src/dispatcher/WorkflowEngine.mag`,
`src/dispatcher/WorkflowRecordGatherer.mag`, `src/dispatcher/CaseEnricher.mag`,
`src/dispatcher/CaseBuilder.mag`, `src/cli/SystemCommands.mag` (gc),
`src/api/DashboardSSE.mag`, `src/dispatcher/Dispatcher.mag`.
Builds on `docs/scout-perf-survey-2026-04-28.md` (do not re-read its findings here —
the index-restructure and flush-fencing it recommended have LANDED; this doc
covers the residual *unbounded-growth* problem those fixes do not address).

## What changed since the 2026-04-28 survey

The April survey's §1.1 (flat-array index under one mutex) and §1.3 (synchronous
flush on tick) have been partially addressed:

- BBS now keeps three hash indices (`byId`, `byKey`, `byCatIdent`,
  `BBS.mag:149-221`). `findInIndex:`/`findByCategory:` are now **O(1)**
  (`BBS.mag:472-495`). The flat `index` is now an `ArrayList` so `add:` is
  O(1) amortised (`BBS.mag:155`).
- Flush is fenced off the tick thread: `flushAsyncIfDirty` forks `saveToDisk`
  in a background process (`BBS.mag:861-885`).

**But the unbounded-growth axis is untouched.** `scanAll:`/`scan:` still walk
the WHOLE flat index per call, `saveToDisk` still re-encodes the ENTIRE durable
set per flush, and the durable set now includes `case` — a category that is
written once per terminal workflow and **never garbage-collected**.

---

## Situation

Durable categories (`BBS.mag:816-822` `isDurableCategory:`):

```
fact convention decision rule signal artifact link ingestion
workflow task token workitem identity case
```

Of these, `pp gc` (`SystemCommands.mag:357-616`) only sweeps **workflow, task,
token, event, signal** (`gcWorkflowTuples:` line 449, `gcOrphanTuples:` lines
611-614). It NEVER removes: **case, decision, fact, convention, workitem,
identity, artifact, link, rule, ingestion.** These are the permanently-growing
set. `case` is the worst because it is the only one written *automatically on
every workflow termination* with a ~1 KB body.

Note: `observation` and `event` are NOT durable (absent from `isDurableCategory:`).
They bloat memory until restart but never reach disk; a restart clears them.
`case`, by contrast, is reloaded forever.

### The AAR/case growth law

Every completed/failed/cancelled workflow writes exactly one durable `case`:
- `WorkflowEngine.mag:1099` (completed), `:1016` (failed), `:1214` (cancelled)
  → `writeCaseSkeleton:` (`:709-738`) → `bbs out: 'case' …` (`:734`).
- It is idempotent (one per instance, `:723-724`) and best-effort, but there is
  **no upper bound and no eviction**. N workflows → N permanent `case` tuples,
  monotonically, for the life of the data dir.
- Enrichment (`CaseEnricher.mag:40`) later does `update: 'case'` (consume+rewrite),
  adding `aar` (free-text narrative) + `lessons` (array) + `tags`, growing each
  case toward ~1–2 KB.

At a sustained 200 workflows/day that is ~73 k cases/year, ~73–146 MB of `case`
payload alone, none of it ever consumed.

---

## Answers to the five questions (file:line + cost)

### Q1 — Which read/scan hot paths are O(total-tuples)?

`scanAll:` (`BBS.mag:408-419`) and `scan:scope:` (`BBS.mag:392-406`) iterate the
**entire** flat `index` and filter by category, under `mutex`. Cost is
**O(total durable tuples)** regardless of how many tuples match. This is the
crux: **a `scanAll: 'task'` pays for every `case`, `decision`, `fact`, etc. it
filters out and discards.** Cases nobody ever scans still tax every scan of
every other category.

Frequency (unchanged from April survey §3/§4, still O(N·W) per tick):
- Dispatcher tick (~every 10 s, `Dispatcher.mag`): ≥10 `scanAll:`/`scan:` per
  pass across `event`, `workflow`, `token`, `task`, `rule`, plus per-workflow
  and per-waiting-token scans.
- Dashboard SSE (~every 2 s, per subscriber): ~12 full scans
  (`DashboardSSE.mag:134-135,237,339,389-391,604-605,693,748`).
- Per CLI read / HTTP request: `/api/scan`, `/api/dashboard`, notification
  long-poll, etc. (April survey §2).

Scaling: today's index is dominated by short-lived `task`/`token`/`signal`
that gc reclaims. As `case`/`decision`/etc. accumulate, total N drifts upward
and never comes back down. Every scan's constant grows with it. At N = 10 k the
flat walk is ~10 k dictionary `at:` + compares under the mutex per scan; at
N = 100 k it is ~100 k. With ~10 scans/tick + ~12/SSE-tick/subscriber that is
millions of compares/second of pure overhead at 100 k, all serialised on one
mutex — exactly the "general slowness" profile, now with a floor that gc
cannot lower.

### Q2 — Persistence cost: full re-serialize per durable write?

**Yes.** `saveToDisk` (`BBS.mag:726-770`) snapshots the entire durable subset
(`index do:` filtering `isDurableCategory:`, lines 749-758), `Json encode:`s the
whole thing (`:762`), writes a temp file and renames. There is no incremental /
append / per-category path — one durable write dirties the whole space and the
next flush rewrites all of it.

- A single durable `out:` calls `persistAfterChange` (`BBS.mag:243,263,521,…`)
  → sets `dirty`. The dispatcher tick calls a flush each tick, which forks
  `saveToDisk` (`flushAsyncIfDirty`, `:861-885`).
- The JSON encode + file write run **off** the tick thread (forked) — good.
- BUT the snapshot loop holds `mutex` (`:746-760`) while shallow-copying every
  durable tuple **and** its payload (2 dict copies/tuple). At 100 k durable
  tuples that mutex hold is ~200 k dict copies — tens of ms during which every
  reader (the tick's own `scanAll:` calls, SSE, CLI) blocks. So it is "fenced"
  from the tick *thread* but not from the tick's *reads*: the shared mutex
  re-couples them.
- Encode/write cost grows linearly with durable size: ~10 MB at 10 k, ~100 MB at
  100 k, every ~10 s under steady load. Disk write + fsync-less rename of a
  100 MB temp file every tick is wasteful churn even when only one tuple changed.
- CLI sync paths (`outSync:`/`inpSync:`, `BBS.mag:330-347`) call the synchronous
  `flushIfDirty` on the request thread → a CLI write blocks on a full 100 MB
  encode.

### Q3 — Memory footprint

Each tuple is a Maggie `Dictionary` (category/scope/identity/id/modality/
timestamps + a `payload` sub-Dictionary). It is referenced from four places —
the `TupleSpace`, the flat `index`, `byKey`, `byCatIdent` — but these hold the
**same object**, so it is one allocation referenced 4×, plus small per-key array
overhead in the two multimaps.

Rough per-case footprint: ~1–2 KB JSON → ~2–5 KB live (Maggie dict + boxed
strings/arrays for `aar`, `lessons`, `actions`). So:
- 10 k cases ≈ 20–50 MB resident, just for cases.
- 100 k cases ≈ 200–500 MB resident.

Add `decision`/`fact`/`convention`/`workitem`/`identity` (smaller, but also
never evicted) and the durable working set is the dominant heap consumer on a
long-lived `pp serve`. The April survey notes `pp serve` had already drifted to
30 GB+ committed (mostly orphan signals, since fixed by the orphan sweep) — the
case path reintroduces an unbounded grower the sweep does not touch.

### Q4 — Restart cost

`loadFromDisk` (`BBS.mag:772-814`) reads the whole `bbs.json`, `Json decode:`s
it in one shot (`:785`), then for every tuple calls `addToIndices:` (`:797`) and
`space outPersistent:`/`out:` (`:800-804`). `addToIndices:` does a `copyWith:` on
the `byKey`/`byCatIdent` arrays per insert (`BBS.mag:161,166`) — for unique
keys these arrays stay length-1 so it is ~O(N) overall, but the JSON decode of a
100 MB file plus 100 k dict materialisations + 100 k TupleSpace inserts is
seconds-to-tens-of-seconds of single-threaded startup, blocking `pp serve`
readiness. Cases are the bulk of that file and contribute nothing to a running
dispatcher.

### Q5 — Are cases ever needed in the hot tuplespace?

**No, except transiently.** Cases are read in exactly two ways, both **O(1) by
identity**, never by scan:
- `CaseEnricher` existence + enrich: `rdp: 'case' … identity: instanceId`
  (`CaseEnricher.mag:33`, `WorkflowEngine.mag:723,930`).
- The Archivist agent reads the one case it was dispatched for (by identity,
  `roles/Archivist.mag:14`).

There is **no `scanAll: 'case'` / `scan: 'case'` anywhere** in the codebase
(verified by grep across `src/`). The case is needed in the hot space only for
the brief window between skeleton-write and archivist enrichment (minutes). After
enrichment it is cold institutional memory — pure retrieval-by-id, occasional,
and a perfect candidate to live outside the hot tuplespace.

**Steady-state bloat:** unbounded. Cases are the one durable category that grows
with *throughput* (one per workflow) rather than with *configured state*
(identities, conventions). They dominate long-run growth and gc never reclaims
them.

---

## Ranked recommendations (impact × effort)

Effort: S ≤ ~½ day, M ≈ 1–2 days, L ≈ multi-day.

### R1 — Extend `pp gc` to sweep cold cases (retention window). IMPACT: HIGH, EFFORT: S
The infrastructure already exists. `gcWorkflows:`/`gcOrphansIn:`
(`SystemCommands.mag:408,618`) is the exact pattern: scan a category, decide
keep/drop, `silentInp:` to remove. Add `gcCases: dryRun` that keeps the N most
recent (or younger-than-H-hours, using `payload.created_at`) and `inp:`s the
rest. Cases are O(1)-retrievable by id while live and gone from scans once swept.
- Pros: tiny, mirrors existing code, immediately caps the worst grower, opt-in
  flags fit the existing `--older-than`/`--keep-backups` style.
- Cons: hard deletion loses institutional memory unless paired with R3 (archive
  first). Mitigate by writing swept cases to the cold log before `inp:`.
- Primitive present: YES (`pp gc` framework, `silentInp:`, `safeScan:`).

### R2 — Move cases out of the hot tuplespace to a cold append-only store. IMPACT: HIGH, EFFORT: M
Keep only a compact in-memory index (instanceId → offset/summary) and write the
full case body to an append-only log, exactly like the existing
`history.jsonl` audit log (`BBS.mag:889-906` already appends JSONL with rotation
at 10 MB). A `case.jsonl` (or reuse history) plus a small `byCatIdent`-style id
map gives O(1) retrieval without the case ever entering the flat `index`,
`scanAll:` walks, or `saveToDisk` re-encode.
- Pros: removes cases from every hot path at once (scans, flush, memory, restart);
  append is O(1); aligns with the "git-backed doctrine repo / pudl append-only
  log" idea in the task brief.
- Cons: new read path for the two O(1) lookups (enricher + archivist); enrichment
  becomes append-new-record instead of `update:`; need a compaction story for the
  log. Medium because it touches the case write/read/enrich trio.
- Primitive present: PARTIAL — `appendHistory:`/`rotateHistoryIfNeeded`
  (`BBS.mag:889-963`) is a working append-only+rotation log to model on;
  `historyTail:`/`history:` (`:965-1027`) show the read side.

### R3 — Summarize-then-prune: distill cases → doctrine, then GC raw cases. IMPACT: HIGH, EFFORT: L
This is the *intended* lifecycle and the Archivist already half-implements it:
`Archivist.mag` produces `aar`/`lessons`/`confidence`; promote recurring lessons
into durable `convention`/`fact` tuples (small, bounded, the real institutional
memory), then let R1 GC the raw cases. The case→doctrine loop closes and the hot
space keeps only the compact distilled form.
- Pros: keeps the *value* of AARs while bounding storage; conventions/facts are
  tiny and queried rarely; this is the architecturally "right" answer.
- Cons: needs a promotion policy (dedup, confidence threshold) and an agent/role
  to run it; correctness of distillation is fuzzy. Build on top of R1+R2.
- Primitive present: PARTIAL — Archivist role + enrichment payload
  (`CaseEnricher.mag`, `Archivist.mag`); no promotion-to-doctrine step yet.

### R4 — Make `scanAll:` not O(total): per-category index buckets. IMPACT: MED-HIGH, EFFORT: M
Add a `byCategory` Dictionary (category → ArrayList of tuples) maintained in
`addToIndices:`/`removeFromIndicesById:` alongside the existing three indices.
`scanAll: cat` returns/copies that bucket — O(matches), not O(total). This
decouples every scan's cost from unrelated durable growth, so even without
retention a 100 k-case space stops taxing `scanAll: 'task'`.
- Pros: fixes the cross-category tax structurally; small, local change in BBS;
  complements (does not replace) R1/R2.
- Cons: still O(matches) — does not help a genuinely large *active* category;
  another index to keep consistent. Does nothing for flush/memory/restart.
- Primitive present: YES — `addToIndices:`/`removeFromIndicesById:` already
  maintain parallel indices; this is one more of the same shape.

### R5 — Sharded / per-category persistence (stop full re-encode per write). IMPACT: MED, EFFORT: L
Split `bbs.json` into per-category files (or dirty-track which categories changed)
so a `task` write does not rewrite the `case`/`fact`/`convention` bytes. Best
combined with R2 (cases leave `bbs.json` entirely). Or move to incremental log
replay: `history.jsonl` already records every `out`/`consume`/`upsert` op
(`BBS.mag:889-906`) — a log-structured load could replace the monolithic snapshot.
- Pros: flush cost becomes proportional to churn, not to total size; smaller
  mutex holds in `saveToDisk` snapshot.
- Cons: larger change to persistence + load; needs careful crash-consistency
  (the current temp-file+rename is simple and correct). Sequence after R1/R2.
- Primitive present: PARTIAL — `history.jsonl` op-log exists but is audit-only
  today, not used for recovery.

### R6 — Confirm which categories must be durable at all. IMPACT: LOW-MED, EFFORT: S
`isDurableCategory:` (`BBS.mag:816-822`) treats `workflow`/`task`/`token` as
durable, yet `pp gc` treats them as disposable and the orphan sweep shows ~73 %
were junk (`SystemCommands.mag:587-589`). Persisting categories that are
routinely GC'd inflates `bbs.json` and restart between gc runs. Consider: are
`token`/`task` worth persisting, or should they be affine/ephemeral and rebuilt?
`outAffine:` with TTL already exists (`BBS.mag:268-295`) for genuinely ephemeral
tuples.
- Pros: shrinks the durable set cheaply; clarifies intent.
- Cons: changing durability of `task`/`workflow` affects crash-recovery
  semantics — needs a deliberate decision, not a blind flip.
- Primitive present: YES — `outAffine:`/TTL.

---

## Suggested order

1. **R1** (cap cases now — half a day, immediate relief).
2. **R4** (decouple scan cost from unrelated growth — structural, local).
3. **R2** (move cases cold — the durable fix for the worst grower).
4. **R3** (close the case→doctrine loop — keep the value, drop the bulk).
5. **R5 / R6** (persistence sharding + durability audit — larger, sequence last).

R1+R4 together remove most of the pain for ~1 day of work and do not require the
larger architectural changes in R2/R3/R5.

---

## Appendix — evidence index

```
scanAll: walks whole index        src/bbs/BBS.mag:408-419
scan: walks whole index           src/bbs/BBS.mag:392-406
saveToDisk full re-encode         src/bbs/BBS.mag:726-770
  snapshot under mutex            src/bbs/BBS.mag:746-760
flushAsyncIfDirty (forked)        src/bbs/BBS.mag:861-885
flushIfDirty (sync, CLI)          src/bbs/BBS.mag:832-859
loadFromDisk full decode          src/bbs/BBS.mag:772-814
isDurableCategory (incl case)     src/bbs/BBS.mag:816-822
append-only log to model R2 on    src/bbs/BBS.mag:889-963
case write (completed/fail/cancel)src/dispatcher/WorkflowEngine.mag:1099,1016,1214
writeCaseSkeleton + 4 scanAll     src/dispatcher/WorkflowEngine.mag:709-738
  gather: 4x scanAll              src/dispatcher/WorkflowRecordGatherer.mag:33-67
case read = O(1) by id (no scan)  src/dispatcher/CaseEnricher.mag:33; WorkflowEngine.mag:723,930
pp gc categories swept            src/cli/SystemCommands.mag:449,611-614
  (case NEVER swept)
gc framework to extend (R1)       src/cli/SystemCommands.mag:408-465,618-648
outAffine/TTL (R6)                src/bbs/BBS.mag:268-295
```
