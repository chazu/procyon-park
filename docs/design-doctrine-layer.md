# Design: Doctrine layer (AAR → plan-time guidance)

**Date:** 2026-06-24
**Status:** design — not yet implemented
**Framing:** PP as a self-contained C2 (command & control) swarm orchestrator.

## Summary

Close the learning loop: distill the lessons accumulated in `case` (AAR) tuples
into **doctrine** — a body of advisory principles consumed by the *planning*
roles at plan time. Doctrine is the swarm's codified **Orient** (OODA): the
institutional memory that biases every Decide.

## Placement (the C2 / OODA mapping)

| OODA | PP |
|------|----|
| Observe | agents act → tasks/observations → `case` (AAR) tuples |
| **Orient** | **doctrine** (synthesized from cases) ← *this layer* |
| Decide | planner/scout consult doctrine + intent → workitems (plans) |
| Act | dispatcher runs workflows → agents execute |

Doctrine is distinct from things PP already has:

- **conventions / rules** — execution-time *binding* constraints (ROE), obeyed inline by every agent.
- **workflow templates (CUE)** — TTPs / battle drills (procedural).
- **doctrine** — *plan-time, advisory*, "authoritative but requires judgment in application" (JP-1).

Doctrine can *crystallize down* into a convention (when a principle becomes a
binding rule), a template change (when it's procedural), or a work item (when it
exposes a gap). Lessons flow *up* (case → curation → doctrine); doctrine flows
*into* planning.

## Coupling decision: LOOSE (for a start)

PP **owns** its doctrine store, synthesis, and maturity ladder locally. pudl is a
*downstream export sink* (one consumer of promoted doctrine), **not** an upstream
read dependency.

Rationale: a self-contained C2 system must keep planning under comms-denial
(DDIL) to a higher echelon — if Orient is a live dependency on pudl, PP is
decapitated when pudl is unreachable (headless/cron/network). loose→tight is
additive later (project a read cache from pudl); tight→loose is not.

This matches the current reality: there is **zero** pp→pudl/nous code today, so
there is no hidden coupling to undo and no read path to accidentally depend on.
Export is greenfield and out of MVP; its target protocol is undefined and must be
confirmed before any export story is built.

## Contextuality

Doctrine is context-dependent (desert doctrine ≠ arctic doctrine). Each entry carries:

- **scope** — `global | repo:X | path:X`
- **tags** — e.g. `lang:maggie`, `phase:build`, `concern:memory`, `role:planner`, `risk:high`

Plan-time composition is a retrieval op: applicable set ≈
`scope ∈ {global, this-repo} AND (tags ∩ mission-context, plus untagged universals)`.
Contradictory doctrine may coexist, selected by situation.

## Data model — `doctrine` tuple category (its own category, NOT folded into `convention`)

**Storage discipline (per code review):** doctrine is **linear + durable**, mirroring
`case` — add to `Categories>>valid` and `BBS>>isDurableCategory:`, but do **NOT**
add it to `Categories>>pinned`. Write/mutate via `out:` / `update:` **directly**,
never the generic pinned put path (which writes persistent/pinned tuples that are
invisible to `rdp:`-based existence checks — same trap `writeCaseSkeleton` documents).

**Identity must be unique, not a reusable slug.** `out:` is append-only with no
unique constraint on `(category,scope,identity)`: reusing a stable slug produces
*two tuples at the same key* — `scan:` surfaces both and `rdp:`/`update:` see only
the latest, silently shadowing the prior entry. So every entry gets a fresh unique
id; the human-readable slug lives in the payload.

```
category: doctrine        (linear + durable; NOT pinned)
identity: dctr-<unique-id>            # fresh per entry — never reuse
payload:
  key:         "<human-readable slug>"   # the stable name (payload, not identity)
  principle:   "<the doctrine statement>"
  rationale:   "<why>"
  scope:       global | <repo> | <repo>:<path>
  tags:        [ ... ]
  maturity:    proposed | active | retired | rejected
  provenance:  [ {scope, identity}, ... ]  # source cases; scope-qualified, may
                                           # dangle after case GC → resolution must be null-tolerant
  confidence:  0..1
  created_at, updated_at, promoted_at
```

## Pipeline skeleton

Two-actor synthesis (mirrors the Archivist→CaseEnricher precedent: the LLM agent
*emits*, a pure engine-side consumer *writes*):

```
cases (AAR tuples)
  → [Strategist role (LLM agent): cluster + dedup + score lessons → EMITS a proposal observation]
  → [DoctrineWriter (engine-side, pure): validates proposals → WRITES `proposed` doctrine w/ fresh ids]
  → doctrine tuples (scope + tags + maturity + provenance→cases)
  → consumed by planner at plan time (scout TBD), composed by context
  → crystallizes into: convention | template change | work item
  → (later, optional) export promoted doctrine → pudl facts
```

The clustering/dedup/scoring is the *agent's* job; only validate-and-write is the
pure PP function. The agent never writes the canonical tuple directly — same reason
enrichment goes through `CaseEnricher` (validation + additive guarantees + off-critical-path).

## Waves (re-scoped per code review)

**Wave 1 — foundation**
1. `doctrine` category — add to `Categories>>valid` + `isDurableCategory:`; **linear +
   durable, NOT pinned**; round-trip test through both stores (json + ganso); lock
   "doctrine is exempt from `pp gc`" with a test (gc targets only `case`).
2. `pp doctrine` CLI — `list`/`show` (read) + `propose` (write via `out:`, fresh
   unique id) + `promote`/`retire`/`reject` (maturity via `update:`).

**Wave 2 — synthesis loop** (the single Strategist story splits into four)
3. `Strategist` role + register in `HarnessFactory`.
4. Strategist emit schema + role prompt (cluster/dedup/score; provenance → cases).
5. `DoctrineWriter` consumer + Dispatcher-tick sweep: validate emitted proposals →
   write `proposed` doctrine with fresh ids (mirrors `CaseEnricher` /
   `applyCompletedArchivistEnrichments`). Owns provenance scope-qualification + fresh-id guard.
6. `pp doctrine synthesize` — enqueue a `strategist` task (mirror `dispatchArchivist:`).

**Wave 3 — the payoff (hardest; NOT a `softCategories` toggle)**
7. Doctrine retrieval function: `active`-only + scope-union(`{global, this-repo}`) +
   tag-intersection; unit-tested in isolation. (`assembleContext:` does none of these.)
8. Wire (7) into `Planner` context assembly, **bypassing** the generic `softCategories`
   path (which would leak `proposed`/`rejected` doctrine and miss global scope).

**Wave 4 — peripheral**
9. Dashboard surface for cases + doctrine (the AAR-UI thread).
10. `pudl` export — **stub only**; no pp→pudl integration exists today, target protocol undefined.

## Maturity ladder

`proposed → active → retired` (+ `rejected`), flipped via `BBS>>update:` on the
tuple's unique identity (replace-by-key; survives restart on the durable category).
No bitemporality needed for v1 — the BBS history/audit log is a poor-man's
transaction-time if ever wanted.

## Hard part (be deliberate)

**Reconciliation.** `out:` appends — it does not replace by key. So "propose-new,
human merges" is only safe if **every proposal gets a fresh unique id** and the
human merge is **promote-one + retire/reject the duplicates** (never reuse an
`active` slug, or you shadow it). v1 punts the *automatic* reconciliation;
the manual merge discipline above is mandatory, not optional. Smarter
reconciliation (synthesizer reads current doctrine and proposes diffs) is later.

## Later / open

- **Plan-time consumption is the hardest story, not a toggle** (Wave 3).
  `assembleContext:` (`Role.mag`) only `scanAll`s `convention`; everything else is
  scanned at the *workflow's* scope with `mostRecent: N`. Doctrine needs three things
  none of that does: **(a) global-scope union, (b) `maturity = active` gate, (c)
  relevance (not recency) selection** — and must **bypass** `softCategories` (adding
  doctrine there would leak unreviewed `proposed`/`rejected` into planning, defeating
  the human gate). Tag-intersection + scope-union are **net-new retrieval code**
  (scan + in-memory `select:`, no tag index), modeled on `filterByWorkflow:` /
  `childrenOfParent:` — not reuse of existing query machinery.
- Scout as a second consumer is deferred — `Scout` is a research role (`hardCategories
  #('fact')`), the genuine plan-time consumer is `Planner`.
- Dashboard inspection surface for cases + doctrine.
- `pudl` export stays greenfield/stub — confirm the actual target tool/protocol before building.
- Synthesis trigger beyond manual (threshold / scheduled).
- Applicability beyond tags (predicate/Datalog matching) if needed.
