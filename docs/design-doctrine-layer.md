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

## Contextuality

Doctrine is context-dependent (desert doctrine ≠ arctic doctrine). Each entry carries:

- **scope** — `global | repo:X | path:X`
- **tags** — e.g. `lang:maggie`, `phase:build`, `concern:memory`, `role:planner`, `risk:high`

Plan-time composition is a retrieval op: applicable set ≈
`scope ∈ {global, this-repo} AND (tags ∩ mission-context, plus untagged universals)`.
Contradictory doctrine may coexist, selected by situation.

## Data model — `doctrine` tuple category (its own category, NOT folded into `convention`)

```
category: doctrine        (durable; isDurableCategory)
identity: <stable slug>
payload:
  principle:   "<the doctrine statement>"
  rationale:   "<why>"
  scope:       global | <repo> | <repo>:<path>
  tags:        [ ... ]
  maturity:    proposed | active | retired | rejected
  provenance:  [ <case identity>, ... ]   # which AARs supported this
  confidence:  0..1
  created_at, updated_at, promoted_at
```

## Pipeline skeleton

```
cases (AAR tuples)
  → [Strategist role: cluster + dedup + score lessons across cases]   (CALL function, runs in PP)
  → doctrine tuples (scope + tags + maturity + provenance→cases)
  → consumed by planner/scout at plan time, composed by context
  → crystallizes into: convention | template change | work item
  → (later, optional) export promoted doctrine → pudl facts
```

## MVP (first slice)

1. `doctrine` BBS category (durable).
2. `pp doctrine` command — `list` / `show` / `synthesize` / `promote`.
3. **Strategist** role + **manual** synthesis: read N recent cases, cluster/dedup
   lessons, emit `proposed` doctrine with provenance back to cases.
4. Human gate: review `proposed`, `promote` to `active`.

Plan-time consumption and the dashboard surface come in a later wave.

## Maturity ladder

`proposed → active → retired` (+ `rejected`). No bitemporality needed for v1 — the
BBS history/audit log is a poor-man's transaction-time if ever wanted.

## Hard part (be deliberate)

**Reconciliation:** when synthesis re-runs, new lessons reinforce / contradict /
supersede existing doctrine. v1 punts honestly — *propose-new, human merges
against existing*. Smarter reconciliation (synthesizer reads current doctrine and
proposes diffs) is a later, deliberate step.

## Later / open

- Plan-time consumption wiring in planner/scout (the payoff). Note: `Planner.mag`
  reads plan-time context via `softCategories` + `contextBudget(60)`; adding
  `doctrine` to `softCategories` yields raw category read only — the scope/tag
  intersection + composition needs custom context assembly beyond that.
- Dashboard inspection surface for cases + doctrine (ties to the AAR-UI thread).
- Optional `pudl` export of promoted doctrine.
- Synthesis trigger beyond manual (threshold / scheduled).
- Applicability beyond tags (predicate/Datalog matching) if needed.
