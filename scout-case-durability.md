# Scout: Case Category Durability Verification

## Task
Durability verify: note one fact about the `case` tuple category.

## Headline Fact (verified)

**`case` durability comes from `BBS>>isDurableCategory`, NOT from `Categories>>pinned`.**
These are two *separate, independently-maintained* lists. A category being pinned
does not make it survive a server restart; only membership in `isDurableCategory`
gates the disk flush (`persistAfterChange`) and reload (`loadFromDisk`).

## Evidence

### Two distinct lists

| List | File:Line | Members | Includes `case`? |
|------|-----------|---------|------------------|
| `Categories pinned` | `src/bbs/Categories.mag:23-26` | 10 cats (`fact convention template rule ingestion artifact link decision identity case`) | yes |
| `BBS isDurableCategory:` | `src/bbs/BBS.mag:816-822` | 14 cats (`fact convention decision rule signal artifact link ingestion workflow task token workitem identity case`) | yes |

Lists differ in membership (e.g. `template` is pinned but NOT durable; `signal`/`task`/`token`/`workflow`/`workitem` are durable but NOT pinned). They are not interchangeable.

### Durability is wired through `isDurableCategory`, not pinning
`persistAfterChange` (the dirty-flag → disk flush) is gated by `isDurableCategory:`
at three write/update sites: `BBS.mag:243`, `:469`, `:521`.

```
(self isDurableCategory: category) ifTrue: [self persistAfterChange].
```

### The bug this guards against (HEAD commit 04ae911)
`fix(aar): make case tuples durable across server restart` — the prior wire story
added `case` to `Categories pinned` but **not** to `isDurableCategory`, so cases
were written as linear, non-durable tuples, never flushed, and vanished on the
next `pp serve` boot — defeating their purpose as AAR institutional memory.

### Design note: `case` is LINEAR + durable (not pinned-durable)
Cases are intentionally kept linear (reached via `rdp:`/`update:`), not pinned.
Case existence checks (`WorkflowEngine>>writeCaseSkeleton` idempotency,
`CaseEnricher>>applyEnrichment`) go through the linear-tuplespace `rdp:` path;
a pinned tuple is reachable only via `findInIndex:`/`updatePinned:` and would be
invisible to those checks. Durability therefore must come from the
durable-category flush+reload, not from pinning.

### Test coverage
- `test/bbs/test_case_category.mag` — CC1–CC4: `case` is valid + pinned.
- `test/dispatcher/test_case_skeleton_wire.mag:233+` — **CSW5**: writes a case,
  flushes, loads a fresh BBS from the same data dir (simulated restart), and
  proves the case reloads. This is the regression guard for the durability bug.

## Verdict
Durability of `case` is **confirmed correct on HEAD**: `case` is present in
`isDurableCategory:` (`BBS.mag:821`), so writes flush to disk and reload on boot.
The one fact worth carrying forward: *pinned ≠ durable in this codebase* — two
lists, two purposes; durability is the `isDurableCategory` list alone.

## Recommendation (no code change made)
The pinned/durable split is a known footgun (this exact bug recurred for `case`).
Consider a single source of truth or a startup assertion that flags categories
which are pinned-but-not-durable (or vice versa) where that combination is
unintended. Left as a recommendation only — scout does not edit source.
