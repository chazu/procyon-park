# Scout findings — `files_changed=0` metrics undercount on completed story workflows

**Mission:** scout-mission-1782430371-16423
**Date:** 2026-06-25
**Scope:** `procyon-park` — merge/integrate path + case/metrics builder
**Mode:** research only (no code changes)

## TL;DR

`files_changed` is **structurally always 0** for every real workflow. The bug is
**not** a wrong-ref / wrong-worktree / post-branch-deletion diff miscount — it is
that **no code anywhere emits `files_changed` into an observation payload.** The
reader (`CaseBuilder`) scans observations for one carrying `files_changed`, but
the only producers of observations in the merge/integrate path
(`MergeWorktreeAction`) write `detail`/`repoPath`/`featureBranch`/`implBranch`/
`merged_to_main` and **never a file count**. The metric defaults to 0 on every
run except unit tests, which inject synthetic observations.

This corroborates doctrine `dctr-445083c20aef8d3e4fc57673dbdcaa09`
(*zeroed-metrics-not-noop*): the telemetry undercounts merged work. The present
finding pins the exact mechanism: the producer side was never wired.

---

## (1) Where `files_changed` is computed / stored / read

### Read side (consumer) — the only place the key is touched in `src/`
- **`src/dispatcher/CaseBuilder.mag:78-89`** — builds the metric by scanning
  `observations` for the FIRST whose payload carries `files_changed`; else `0`:
  ```
  filesChanged := 0.
  foundFiles := false.
  observations do: [:ob |
    (foundFiles not and:
      [((self payloadOf: ob) at: 'files_changed' ifAbsent: [nil]) notNil])
      ifTrue: [ filesChanged := (self payloadOf: ob) at: 'files_changed'. foundFiles := true ]
  ].
  ```
- **`src/dispatcher/CaseBuilder.mag:122`** — stores it into the case tuple:
  `case at: 'files_changed' put: filesChanged.`
- **`src/dispatcher/CaseEnricher.mag:8`** — treats `files_changed` as a frozen
  skeleton field that enrichment must preserve untouched (read-only passthrough).
- **`src/dispatcher/WorkflowRecordGatherer.mag:57-61`** — supplies the
  `observations` array to CaseBuilder via `bbs scanAll: 'observation'` filtered to
  the instance. It is deliberately dumb: collects, never computes a diff.

### Producer side — **MISSING**
- A repo-wide grep for `at: 'files_changed' put:` in `src/**/*.mag` returns
  **only `CaseBuilder.mag:122`** (the consumer writing into the case). There is
  **no observation producer** that sets `files_changed`.
- **`src/dispatcher/actions/MergeWorktreeAction.mag`** emits the `merge-complete`
  observation at three sites — `:74-79` (pipeline merge), `:152-158` (standalone),
  `:175-181` (wave-child). **None** of these payloads include `files_changed`.
- **`src/dispatcher/GitOps.mag`** has no diff/numstat/name-only helper at all
  (grep for `diff|numstat|name-only|--stat` in GitOps = no hits). `mergeBranch:
  into:in:` (`:143-189`) holds everything needed — the merged SHA
  (`mergedSha`, `:179`) plus both branch refs — but never counts changed files.
- Agents never supply it either: `src/roles/Implementer.mag:30` only runs
  `pp observe implementation-complete "<brief summary>"` — free-text `detail`,
  no structured `files_changed`.

### The one diff that IS computed — but discarded
- **`src/harness/ClaudeHarness.mag:221-229`** `gitDiffSummaryFor:` runs
  `git diff --stat main...HEAD` and `git diff --stat HEAD`, but the result is
  injected **only into the reviewer prompt text** (`:112-122`) and thrown away.
  It is never parsed into a count or emitted as an observation. The capability to
  compute the number exists; it is simply not wired to the metric.

---

## (2) WHY it reads 0

The diagnosis the mission proposed (wrong ref / wrong worktree / after branch
deletion / never wired) resolves to the **last option, in its strongest form:**

> **The count is never produced.** CaseBuilder's scan over observations never
> matches because no observation in the system carries the `files_changed` key.
> `filesChanged` therefore keeps its initial value `0` (`CaseBuilder.mag:80`) on
> every real workflow.

It is not a ref/worktree miscount because there is no `git diff` feeding the
metric to get wrong in the first place. The unit tests pass
(`test/dispatcher/test_case_builder.mag:263-279`, CB5) **only because they inject
a synthetic observation** (`CBHelper observationWithFiles: 7`,
`:85-91`) carrying the key — masking the missing producer in production.

### Ref/timing pitfalls the fix MUST avoid (why a naive patch would still read 0)
1. **Branch deletion ordering.** `MergeWorktreeAction` force-deletes the impl
   branch at **`:107`** (`GitOps deleteBranchForce: implBranch`) and the feature
   branch at `:144`/`:68`. Any count against `implBranch`/`featureBranch` must be
   taken **before** these deletes, or git will have no ref to diff.
2. **Post-merge `HEAD` is the wrong base.** Counting `git diff HEAD~1 HEAD` on
   main after the merge measures only the merge commit's delta — for a
   fast-forward (`fastForwardOrMerge:`, `:205-218`) or an octopus/merge commit it
   can be 0 or wrong. The correct base is the divergence point: three-dot
   `main...implBranch` (the same semantics the reviewer already sees at
   `ClaudeHarness.mag:228`).
3. **`Shell capture:` soft-fails to `''`.** Per repo convention
   (`GitOps.mag:175-178,191-202` and project memory `project_pp_treesafe_merge`),
   `Shell capture:timeout:` is unreliable and `capture:` can return `''`. The
   counter must guard empty/nil output → treat as `0`, never crash the merge.

---

## (3) Minimal correct fix (recommended — NOT applied)

Wire the producer; the consumer (`CaseBuilder`) already reads correctly and needs
no change.

**R1 — add a counter to `GitOps`** (new classMethod, alongside `commitsAheadOf:`
at `GitOps.mag:106`):
```
classMethod: changedFileCount: branch since: base in: repoPath [
  "Count files branch changed since it diverged from base (three-dot).
   Matches the reviewer's `git diff --stat main...HEAD` view. Guards the
   soft-fail-to-empty behaviour of Shell capture: -> 0."
  | out |
  out := Shell capture: 'git -C "', repoPath, '" diff --name-only "',
           base, '...', branch, '" 2>/dev/null | wc -l'.
  out isNil ifTrue: [^0].
  ^out trimSeparators asInteger ifNil: [0]
]
```

**R2 — emit it from `MergeWorktreeAction`**, computing **before** the branch
deletes and adding it to the `merge-complete` payload at each emit site:
- Wave-child & standalone: compute `filesChanged := GitOps changedFileCount:
  implBranch since: 'main' in: repoPath` **before `:106-107`**, then
  `obsPayload at: 'files_changed' put: filesChanged` at `:152-158` and `:175-181`.
- Pipeline merge (implBranch empty): compute `GitOps changedFileCount:
  featureBranch since: 'main' in: repoPath` **before the delete at `:68`**, then
  add to the payload at `:74-79`.

Because `CaseBuilder.mag:82-88` takes the FIRST observation carrying the key, the
`merge-complete` observation becomes the canonical source and flows through
`WorkflowRecordGatherer` → `CaseBuilder` → `CaseEnricher` unchanged.

**Alternative (single-site) fix:** count inside `GitOps mergeBranch:into:in:`
(`:143-189`) while the ephemeral worktree exists — `git diff --name-only
<targetBranch-tip-before-merge>..<mergedSha>` — and return it so the action can
attach it. Slightly more invasive (changes a method signature / return shape) but
keeps all git knowledge in one place and avoids a second `main...` diff.

### Verification once fixed
- CB5 (`test_case_builder.mag:263-279`) still passes (synthetic observation).
- Add a producer-side test asserting `merge-complete` carries `files_changed > 0`
  for a branch that touched files — the gap CB5 cannot catch today.
- Spot-check story-1782428010-16337 (touched `src/roles/Role.mag`,
  `test/test_doctrine_routing.mag`) → expect `files_changed=2`, not 0.

---

## File:line index
| Concern | Location |
|---|---|
| Metric read (scan observations) | `src/dispatcher/CaseBuilder.mag:78-89` |
| Metric stored into case | `src/dispatcher/CaseBuilder.mag:122` |
| Enricher preserves field | `src/dispatcher/CaseEnricher.mag:8` |
| Observations gathered (no diff) | `src/dispatcher/WorkflowRecordGatherer.mag:57-61` |
| `merge-complete` emit — pipeline | `src/dispatcher/actions/MergeWorktreeAction.mag:74-79` |
| `merge-complete` emit — standalone | `src/dispatcher/actions/MergeWorktreeAction.mag:152-158` |
| `merge-complete` emit — wave-child | `src/dispatcher/actions/MergeWorktreeAction.mag:175-181` |
| Impl branch force-delete (timing) | `src/dispatcher/actions/MergeWorktreeAction.mag:107` |
| Feature branch delete (timing) | `src/dispatcher/actions/MergeWorktreeAction.mag:68,144` |
| Merge holds mergedSha + refs | `src/dispatcher/GitOps.mag:143-189` |
| Diff computed but discarded | `src/harness/ClaudeHarness.mag:221-229` |
| Synthetic obs masks gap (test) | `test/dispatcher/test_case_builder.mag:85-91,263-279` |
