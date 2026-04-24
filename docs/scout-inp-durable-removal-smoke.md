# Scout: inp: durable removal smoke test

**Verdict: PASS.** `inp:` removes durable tuples from both the in-memory
index AND the durable JSON file on the next dispatcher flush. After a
server restart, consumed tuples do **not** resurrect. The `pp bbs rm`
design may proceed as-is — no tombstones or index-rewrite workaround
required for the linear durable path.

## Method

Ran an isolated `pp serve` (separate HOME and port, to avoid disturbing
the running system) and exercised a durable, linear-category consume
via the `upsertSignal:` code path, which internally does
`self inp: 'signal' ...` followed by a fresh `self out: 'signal' ...`
(see `src/bbs/BBS.mag:345–350`). This is a production call path that
specifically depends on the premise being tested.

Steps executed:

1. `HOME=/tmp/ppsmoke pp serve --port 7788` → empty state, dispatcher running.
2. `pp signal testid keyA valA` (scope=`smoketest`) → id **17**.
3. Verified via `pp read signal smoketest testid` → present (id 17).
4. Waited for dispatcher flush (~2s). `grep '"id":17' ~/.pp/data/bbs.json` → 1 match (persisted).
5. `pp signal testid keyA valB` → id **18**; `upsertSignal:` internally called `inp:` on id 17 then `out:` a new tuple.
6. `grep '"id":17' ~/.pp/data/bbs.json` → **0 matches** (already flushed out).
7. `grep '"id":18' ~/.pp/data/bbs.json` → 1 match.
8. `history.jsonl` contains: `{"op":"consume","id":17,"category":"signal",...}`.
9. `kill` the pp serve process.
10. Restart `pp serve --port 7788` on same HOME → `BBS: restored 6 tuples from disk.`
11. `pp read signal smoketest testid` → only id 18 returned. **id 17 did not resurrect.**
12. `pp read signal smoketest` (scan) → `count: 1`, only id 18.

## Why it works — code path review

- `BBS.mag:138–149` `inp:` calls `space tryIn:` (removes from in-memory
  TupleSpace), then `self removeFromIndex: result`.
- `BBS.mag:257–265` `removeFromIndex:` rejects the tuple from `index` by
  id and, **if the category is durable**, calls `persistAfterChange`
  which sets `dirty := true`.
- Dispatcher tick (`src/dispatcher/Dispatcher.mag:43,69`) calls
  `flushIfDirty` ~every second.
- `BBS.mag:568–574` `flushIfDirty` → `saveToDisk`
  (`BBS.mag:489–510`) rewrites `bbs.json` from the current filtered
  index atomically (tmp file + rename). Because the removed tuple is no
  longer in `index`, it is simply absent from the new file — there is no
  separate delete-log; the file **is** a snapshot of the live index.
- `loadFromDisk` (`BBS.mag:512–553`) rebuilds both index and TupleSpace
  solely from `bbs.json`. Since the consumed tuple was absent, it stays
  absent on restart.

So the design is: the durable store is always a full rewrite of the
durable subset of the index. inp: mutates the index; flush re-snapshots.
No resurrection surface exists for linear durable tuples.

## Caveats and scope

1. **Pinned tuples (`outPinned:`) were NOT directly tested.** `inp:` in
   BBS.mag calls `space tryIn:` which targets the linear TupleSpace
   store. Pinned tuples live in `space outPersistent:` and may not be
   matched by `tryIn:` at all. The pinned categories listed in
   `Categories.mag:23–26` (`fact`, `convention`, `template`, `rule`,
   `ingestion`, `artifact`, `link`, `decision`, `identity`) have a
   comment stating they are "never consumed by in:". **If `pp bbs rm`
   needs to remove pinned tuples, it cannot rely on `inp:` — it must
   call `removeFromIndex:` directly or add a dedicated removal method
   on the TupleSpace persistent store.** This is the first thing the
   design should address.
2. `signal` is a linear durable category, so the test covers exactly
   the assumption needed for any `pp bbs rm` path that operates on
   linear durable categories (`signal`, `workflow`, `task`, `token`,
   `workitem`). For the pinned-only categories, a separate verification
   is required.
3. This was a clean-state test (6 tuples total). Behavior under heavy
   write pressure or concurrent inp:/out: was not stressed.

## Recommendation

- Proceed with `pp bbs rm` design for **linear durable categories** as
  currently planned (inp: + flush).
- **Extend design** to handle pinned durable categories separately.
  Candidate approach: a new BBS method that (a) removes from
  `space` persistent store (needs a `tryInPersistent:` or equivalent
  that isn't currently exposed — check `TupleSpace` implementation),
  and (b) removes from the index via `removeFromIndex:`. Then
  `flushIfDirty` handles the disk side uniformly.
- Add a targeted test that proves the pinned-removal path before
  shipping `pp bbs rm`.

## Artifacts

- Isolated serve log: `/tmp/ppsmoke/serve.log`, `/tmp/ppsmoke/serve2.log`
- Snapshot of durable file pre-consume contained id 17; post-consume
  did not. History log at `/tmp/ppsmoke/.pp/data/history.jsonl` records
  a `consume` op for id 17.
