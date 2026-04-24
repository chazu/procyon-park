# Scout: inp: PINNED durable removal smoke test

**Verdict: PASS (with one in-process caveat).** `inp:` on a pinned
durable tuple correctly removes it from the index AND from `bbs.json`
on the next flush. After a simulated restart (fresh BBS on the same
dataDir) the pinned tuple does NOT resurrect in scan nor in rdp.
`story:bbs-cli:http-routes` rm path can ship unchanged for pinned
categories — no tombstones or pinned-file rewrite needed for the
across-restart guarantee.

There is one **in-process** inconsistency worth knowing about (not a
blocker): after `inp:` on a pinned tuple, `rdp:` / `rd:` still return
the tuple until the process restarts. This is because the in-memory
persistent TupleSpace store is not drained; only the index is.
Documented below as a design note for http-routes.

## Method

The prior linear-category smoke test
(`docs/scout-inp-durable-removal-smoke.md`) could be run end-to-end
against a live `pp serve` because `pp signal ... -> upsertSignal:`
internally calls `inp:` on the linear tuple. The pinned path has no
such CLI-reachable inp caller (all CLI pinned writers — `decide`,
etc. — never call inp:), so we drove the scenario directly at the
BBS layer using the same harness pattern as
`test/bbs/test_bbs_sync_flush.mag` — a temporary test Main executed
via `mag test --entry`. The scratch test file was removed after the
run; no source changes are being committed.

Script executed (transcribed here so the test can be re-run):

```smalltalk
dir := '/tmp/pp-scout-pinned-...'.
bbs := BBS withDataDir: dir.
payload := Dictionary new. payload at: 'body' put: 'pinned fact to consume'.
id := bbs outPinned: 'fact' scope: 'scoutscope' identity: 'pinned-1' payload: payload.
bbs flushIfDirty.                                      "write bbs.json"
consumed := bbs inp: 'fact' scope: 'scoutscope' identity: 'pinned-1'.
bbs scan: 'fact' scope: 'scoutscope'.                  "-> size 0"
bbs rdp: 'fact' scope: 'scoutscope' identity: 'pinned-1'. "-> still returns tuple! (in-process)"
bbs flushIfDirty.                                      "re-snapshot bbs.json"
bbs2 := BBS withDataDir: dir.                          "simulates restart"
bbs2 scan: 'fact' scope: 'scoutscope'.                 "-> size 0"
bbs2 rdp: 'fact' scope: 'scoutscope' identity: 'pinned-1'. "-> nil"
```

Observed output (trimmed):

```
outPinned id = 1
PASS: outPinned: returned an id
inp: returned isNil? false
PASS: inp: returned a result for pinned tuple (not nil)
scan after inp: size = 0
PASS: scan: returns 0 tuples after inp: on pinned
rdp: after inp: isNil? false           <-- in-process: still present
BBS: restored 0 tuples from disk.      <-- flushed file was tuple-free
After restart, scan size = 0
PASS: After restart, pinned tuple does NOT resurrect via scan
After restart, rdp: isNil? true
PASS: After restart, pinned tuple is not in persistent TupleSpace either
=== Pinned inp: Results: passed=5 failed=0 ===
```

## Why it works — trace through the code path

1. `outPinned:` (`BBS.mag:107-116`) calls `space outPersistent: d` and
   also appends `d` to `index`. Both the TupleSpace persistent store
   and the index hold it.
2. `flushIfDirty` → `saveToDisk` (`BBS.mag:516-537`) snapshots only
   the durable slice of `index`. bbs.json contains the pinned tuple.
3. `inp:` (`BBS.mag:165-176`) calls `space tryIn: tmpl`. From
   `mag help TupleSpace`: *"Persistent: never consumed; in: returns a
   copy but keeps the original."* So `tryIn:` returns a non-nil copy
   and **the persistent store is NOT drained**. Then
   `removeFromIndex:` (`BBS.mag:283-292`) rejects by id from `index`
   and calls `persistAfterChange` (because 'fact' is durable) →
   `dirty := true`.
4. Next `flushIfDirty` → `saveToDisk` rewrites bbs.json from the
   mutated index; the pinned tuple is absent from the file even
   though it is still in the in-memory TupleSpace.
5. On restart, `loadFromDisk` (`BBS.mag:539-580`) iterates the file.
   Because the pinned tuple is not in the file it is neither added to
   the new index nor `outPersistent:`-ed into the fresh TupleSpace.
   Resurrection surface is closed.

So the invariant is the same as for the linear path: **bbs.json IS a
full snapshot of the durable index**, and the index is the single
truth `removeFromIndex:` mutates. The pinned/persistent in-memory
store is a secondary cache that happens to outlive a successful
consume but does not survive process exit.

## In-process rdp / rd inconsistency — design note for http-routes

Between `inp:` on a pinned tuple and process exit, the tuple:

| Read path     | Returns after inp:? | Reason                        |
|---------------|---------------------|-------------------------------|
| `scan:`       | No (correct)        | index was rejected            |
| `scanAll:`    | No (correct)        | index was rejected            |
| `rdp:` / `rd:`| **YES**             | TupleSpace persistent retains |
| `findInIndex:`| No (correct)        | index was rejected            |

For `pp bbs rm` this is almost certainly fine — the CLI will commonly
return right after issuing the consume, and any agent that then runs
`rdp:` is probably misusing it. But the http-routes author should be
aware: if a client does `rm` then immediately `rdp` over HTTP in the
same process lifetime, the tuple will still be readable. To close
this fully, the BBS would need a `tryInPersistent:` (or similar) on
the TupleSpace to remove from the persistent store as well, coupled
with the existing `removeFromIndex:`. This is a nice-to-have, not a
correctness blocker for durable removal.

## Pinned + affine + TTL — not a valid combination

Per `src/bbs/Tuple.mag:60-119`, the three factories are mutually
exclusive:

- `linear:...` sets `pinned=false, modality='linear'`.
- `pinned:...` sets `pinned=true, modality='persistent'` (no ttl).
- `affine:...ttl:` sets `pinned=false, modality='affine'` (+ ttl).

There is no `pinned+affine` factory, and the BBS emitters
(`outPinned:`, `outAffine:`) are parallel not composable. A tuple is
either pinned, linear, or affine — never pinned+affine with TTL. So
the "pinned tuple with affine modality + TTL" case in the brief is
not a legal combination in this schema and does not need exercising.

## Caveats and scope

1. Test was run at the BBS class layer, not end-to-end through
   `pp serve` → HTTP → `/api/inp`. The HTTP route
   (`src/api/Server.mag:166-171`) is a thin wrapper that delegates
   directly to `bbs inp:`, so the result will be the same, but an
   HTTP-layer smoke is still advisable once `pp bbs rm` exists.
2. Only the `'fact'` pinned category was exercised. Behavior is
   driven by the persistent-store semantics, not the category name,
   so `convention` / `decision` / `rule` / `ingestion` / `artifact` /
   `link` / `identity` should all behave identically. `template` is
   pinned but not listed in `isDurableCategory:` (`BBS.mag:582-588`);
   its inp path works in-process but is not flushed to disk — that
   is intentional per the Main.mag startup reseed (templates are
   always re-read from CUE files).
3. Clean-state test with 1 tuple. No concurrency or write-pressure
   stress.

## Recommendation

- **Ship `story:bbs-cli:http-routes` rm unchanged for pinned durable
  categories.** Pinned inp: + flush + restart is correct. No
  tombstone file, no pinned-file rewrite, no design change needed.
- Note the in-process rdp-still-sees-it quirk in the http-routes PR
  description so that the reviewer and any downstream clients know
  to avoid read-immediately-after-rm patterns within a single
  process. Optional follow-up: add `tryInPersistent:` to TupleSpace
  and route pinned inp: through it to close the in-process surface.

## Artifacts

- Test transcript: see "Observed output" block above.
- Temp dataDir and scratch test file removed after the run.
- Code references: `src/bbs/BBS.mag:107-116, 165-176, 283-292,
  516-580`; `src/bbs/Tuple.mag:60-119`; `src/bbs/Categories.mag:5-30`.
