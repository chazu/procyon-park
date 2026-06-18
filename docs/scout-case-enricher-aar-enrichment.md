# Scout Findings: Role of CaseEnricher in Live AAR Enrichment

**Task:** Live AAR enrichment verify — note the role of `CaseEnricher`.
**Date:** 2026-06-18
**Scope:** `src/dispatcher/CaseEnricher.mag`, `src/dispatcher/WorkflowEngine.mag`, `src/dispatcher/Dispatcher.mag`, `src/roles/Archivist.mag`

---

## TL;DR

`CaseEnricher` is the **pure, additive merge primitive** that closes the AAR
(after-action review) learning loop. It takes the structured enrichment an
Archivist agent produced and folds it into the already-written `case` tuple
**without ever touching the deterministic skeleton** that `CaseBuilder` wrote.
It is the *last step* of the loop, called only from `WorkflowEngine`, never
directly from the tick or a role.

---

## The AAR enrichment loop (end to end)

```
 workflow reaches terminal state
        │
        ▼
 WorkflowEngine writes the deterministic `case` skeleton (CaseBuilder fields)
        │
        ▼
 WorkflowEngine>>dispatchArchivist:scope:        (WorkflowEngine.mag:737, called :1015/:1098)
   └─ enqueues EXACTLY ONE idempotent, non-fatal 'archivist' task for the instance
        │
        ▼
 Archivist agent (src/roles/Archivist.mag) — read-only / write-only-to-KB
   └─ synthesizes AAR, emits ONE JSON object via:
        pp observe case-enrichment '{"aar":...,"lessons":[...],"confidence":0.8,"tags":[...]}'
        │
        ▼
 Dispatcher tick (Dispatcher.mag:88)
   └─ WorkflowEngine>>applyCompletedArchivistEnrichments   (WorkflowEngine.mag:902)
        ├─ scans COMPLETED archivist tasks whose case has no `enriched_at` yet
        ├─ requires a 'task-complete:<taskId>' event (real completion proof)
        └─ WorkflowEngine>>applyArchivistEnrichment:scope:  (WorkflowEngine.mag:832)
             ├─ findArchivistEnrichment: → locates the 'case-enrichment' observation
             ├─ enrichmentDictFrom: → JSON-decodes observation `detail` to flat dict
             ├─ enrichmentHasContent: → empty/malformed ⇒ NO-OP
             └─►  CaseEnricher applyEnrichment:toCaseWithInstance:scope:bbs:   ◄── THE ROLE
```

## Role of `CaseEnricher` specifically

`CaseEnricher` (subclass of `Object`) has exactly **two class methods** and holds
no state. It is the integration endpoint — `WorkflowEngine` does all the locating,
decoding, and guarding; `CaseEnricher` does the actual merge.

### 1. `applyEnrichment:toCaseWithInstance:scope:bbs:`
- Guarded, additive merge into the `case` tuple for `instanceId`.
- Returns `false` (no-op) if no such case exists; else mutates in place, returns `true`.
- Sets **only four new keys** and unions tags:
  - `aar`        (String, default `''`)
  - `lessons`    (Array of String, default empty)
  - `confidence` (Number, default `0`)
  - `enriched_at`(`DateTime now epochSeconds` — the only clock/BBS touch)
  - `tags`       ← `mergeTags:` UNION of existing + supplied tags
- **Never reads, rewrites, or removes** any deterministic skeleton field:
  `workflow_instance, terminal_state, duration_minutes, retries, review_cycles,
  human_interventions, files_changed, actions, repo, mission_type, created_at`.

### 2. `mergeTags:with:` (PURE helper)
- Order-stable union with de-duplication. Existing tags keep order/precedence;
  new tags appended only if not already present. `nil` treated as empty.
- No BBS, no clock — unit-testable in isolation.

## Key invariants (verified in source + doc comments)

| Invariant | Where enforced |
|---|---|
| Additive only — skeleton survives untouched | `CaseEnricher.mag:1-24, 36-44` |
| Idempotent — case with `enriched_at` is skipped | `WorkflowEngine.mag:924-928` |
| Off-critical-path — wrapped in `on:Exception`, never gates/flips status | `WorkflowEngine.mag:838-841, 865-868`; `Dispatcher.mag:88` |
| No-op on absent/empty/malformed enrichment | `WorkflowEngine.mag:842-845, 860-863, 891-900` |
| Channel = OBSERVATION `case-enrichment`, not a decision | `WorkflowEngine.mag:793-812` |
| Archivist is read/write-KB-only, emits flat 4-key JSON | `Archivist.mag:22-35` |

## Channel contract (decided + documented in WorkflowEngine)
The Archivist writes enrichment as an **observation** tuple (identity
`case-enrichment`), not a decision — because the Archivist role's `hardCategories`
include `observation` but not `decision`. The observation must link to the workflow
via payload `workflow_instance = <instanceId>` (or `task = <instanceId>:task:archivist`).
Extra payload keys (detail, agent_identity, scope, task…) are ignored — only the four
keys + tags union reach `CaseEnricher`.

## Verdict
Live AAR enrichment wiring is **coherent and complete**. `CaseEnricher` is correctly
positioned as the narrow, pure, additive write primitive at the tail of the loop;
all locating/decoding/guarding lives upstream in `WorkflowEngine`, and the whole
consumption path is driven off the Dispatcher tick behind exception guards so an
archivist failure can never affect workflow status. Tests exist:
`test/dispatcher/test_case_enricher.mag`, `test/dispatcher/test_aar_learning_integration.mag`.

## Recommendations (no code changed — scout is read-only)
1. **None blocking.** Design is sound and well-documented inline.
2. Minor: `applyCompletedArchivistEnrichments` re-scans `task` + an `event` rdp every
   tick; if archivist task volume grows this is O(tasks) per tick. Consider a
   processed-marker or scoped scan if tick latency becomes a concern. (efficiency, low.)
3. Minor: the `pudl observe` agent-protocol hook references a command absent from the
   installed `pudl` binary (`pudl: unknown command "observe"`); observation logging for
   this repo currently flows through `pp observe` instead. Worth reconciling the two
   protocols so the startup nag is satisfiable.
