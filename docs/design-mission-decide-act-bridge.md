# Design: Mission Decide→Act bridge (Phase B)

**Date:** 2026-07-01
**Status:** design — not yet implemented (agreed in discussion; Phase A is live)
**Framing:** PP as a self-contained C2 swarm orchestrator. This closes the
Auftragstaktik loop: **commander's intent → human-gated plan → autonomous
execution → outcomes roll back up to the mission.**

## Summary

Today a mission dead-ends at `approved`: `mission-brief` researches the brief,
writes a markdown plan, flips to `awaiting-approval`, a human approves — and
**nothing happens**. Approval is a terminal status with no link to any work.

Phase B makes approval *spawn and own* the work: a dispatcher reconciliation
materializes an epic from the approved breakdown, work is dispatched behind a
light second trigger, and status rolls back up so the mission becomes the outcome
anchor. Crucially, the **mission owns a queryable, nukeable blast radius**: every
entity created because of a mission is stamped so it can be torn down wholesale
when (not if) we need to nuke it.

## The gap (Phase A, live)

```
pp mission start → mission-brief workflow → payload.plan (markdown)
                → awaiting-approval → [human approve] → approved → ∅
```

- The plan is human prose, not structured work.
- There is no edge from mission → work-items / workflows.
- `approved` is terminal; there are two approve paths (CLI signed-PUT via
  `MissionCLI>>setStatusFor:`, and the dashboard endpoint
  `Server>>handleMissionDecisionScope:`), so any "on approve" hook bolted onto
  one path misses the other.

## Target flow

```
                        ┌─ reject → (nothing to clean; breakdown was inert data)
awaiting-approval ─────►┤
        ▲               └─ approve ─► [dispatcher reconciliation]
        │                              │  approved AND no epic_id?
   [human gate]                        ▼
                              materialize epic (stamp mission_id)
                              materialize child stories (status=ready)
                                        │
                              [second trigger: dashboard Execute / pp mission run]
                                        ▼
                              dispatch (waves) → review/test/merge   (reuse full-pipeline)
                                        │
                              rollup: approved → in-progress → done → archived
```

## Agreed decisions

### D1 — Spawn via dispatcher reconciliation, not an approve-path hook
The dispatcher's housekeeping loop watches for `mission.status = approved AND
mission.epic_id = nil`, materializes the epic exactly once, and stamps
`mission.epic_id`. Idempotent (re-approve / restart safe), path-agnostic (works
for both CLI and dashboard approval), and native to the tuplespace/Petri-net
style. No new server route; no coupling to the approve call site.

### D2 — Breakdown-as-data-until-approve
`mission-brief` produces a **structured** work-item breakdown and stores it *in
the mission payload*, not as real tuples:

```
payload.breakdown : [ { title, description, wave, depends_on:[...], template } ]
```

This is inert data the human reviews at the gate. **Real work-item tuples are
materialized only on approve** (by D1's reconciliation). Therefore:

- **Reject** → flip status; there is *nothing to clean up* (no entities existed).
- **Approve** → materialize from `payload.breakdown`, each stamped `mission_id`.

This keeps the "human approves the actual breakdown" property while eliminating
the pre-approval cleanup problem entirely.

### D3 — Provenance backbone: a stamped, indexed `mission_id`
Every entity spawned *because of* a mission carries `mission_id` — stamped at
creation, never inferred. In ganso this is a generated + indexed column (mirrors
`wf_inst` / `parent` in `GansoStore>>ensureSchema`), so "everything this mission
created" is a single indexed query:

```sql
SELECT ... FROM tuples WHERE mission_id = ?
```

Carriers: work-items (epic + stories), workflows launched for them, and — via the
workflow — tasks/tokens/signals/events, plus the worktrees/branches those
workflows create. The mission also holds `epic_id` as the entry point. This
mirrors the doctrine `provenance` / `crystallized_into` lineage language already
in the codebase.

Threading: work-item creation and workflow launch must accept and persist
`mission_id`. Add it to the tuple JSON so the generated column populates; add the
column + `ix_mission` index to `GansoStore>>ensureSchema`.

### D4 — Nuke: one cascade mechanism, dry-run by default
`pp mission nuke <id> [--dry-run]` (default: `--dry-run`) cascades:

1. Cancel running workflows for the mission (reuse `pp workflow cancel`).
2. Delete work-items where `mission_id = X` (epic + stories).
3. Remove worktrees + delete **unmerged** feature branches (reuse
   `pp worktree clean` / `pp clean-branches`).
4. Delete associated tuples (tasks/tokens/signals/events with `mission_id = X`).
5. Archive or delete the mission tuple itself.

**Boundary:** nuke reclaims *unmerged* artifacts only. Merged commits are
hands-off — reverting merged code is a deliberate `git revert`, never an
auto-cascade. The same teardown serves both post-approval cleanup and the
inevitable manual "nuke everything this mission touched." Destructive → dry-run
prints the full blast radius first; a real run requires an explicit flag.

### D5 — Second trigger, not full autonomy (yet)
Approve materializes the epic + stories as `ready` but does **not** auto-dispatch.
A dashboard **Execute** button / `pp mission run <id>` is the light second
trigger that starts execution; thereafter it runs autonomously (failures escalate
via existing notifications). Tighten to auto-dispatch-on-approve once the loop is
trusted. Rationale: a rubber-stamp approval must not kick off N parallel agents
unattended.

### D6 — Rollup states + outcome anchoring
Add mission statuses `in-progress` and `done`. The dispatcher cascade advances
the mission as its `mission_id` work-items progress:
`approved → in-progress` (first dispatch) `→ done` (all children done)
`→ archived` (auto or human). The mission becomes the outcome anchor, which plugs
directly into the doctrine outcome-correlation backstop (mission-level
outcome_score).

Lifecycle (full): `researching → awaiting-approval → approved | rejected →
in-progress → done → archived` (with `rejected` and any decided state also
archivable, per the shipped archival feature).

### D7 — UI rides the snapshot cache; no new SSE stream
A richer mission representation (mission → epic → stories, with rollup status and
the provenance tree) renders **from the already-cached tick snapshot** (see
`DashboardSSE` snapshot cache, `project_pp_memory_runaway`). It recomputes only
when `BBS>>changeCount` advances — so no new per-tick scan and **no new SSE
channel**. Hard rule: everything mission-UI goes through the existing cached tick.

## Data model changes

- **mission payload:** `+ breakdown[]` (D2), `+ epic_id` (D1), new `status`
  values `in-progress` / `done` (D6). Update `MissionCLI` lifecycle docs +
  `pp mission show`.
- **mission-brief workflow:** emit `payload.breakdown` (structured) alongside the
  markdown plan. Decompose can be a planner pass or emitted directly — see Open.
- **`mission_id` column:** `GansoStore>>ensureSchema` gains
  `mission_id TEXT GENERATED ALWAYS AS (json_extract(data,'$.mission_id')) STORED`
  + `CREATE INDEX ix_mission ON tuples(mission_id)`. Work-item / workflow / task
  writers thread `mission_id` into the tuple JSON.
- **new CLI:** `pp mission nuke <id> [--dry-run]`, `pp mission run <id>`. Register
  as *subcommands only* (the `mission` top-level is already in `ppCommands` — do
  NOT re-register; known double-registration footgun).

## Execution reuse

Decomposition + dispatch reuse existing machinery: the **planner role** /
`workitem-plan` for turning the breakdown into a ready work-item tree (if not
emitted directly at brief time), and **`full-pipeline`** (plan → dispatch-waves →
review+test → evaluate → merge) to run the epic. The bridge is wiring +
provenance + lifecycle, not a new execution engine.

## Phase B v1 — tracer bullet (build this first)

Prove the novel/risky part (the ownership + teardown model) end-to-end before
layering decompose/dispatch/rollup (which reuse existing pieces):

1. **`mission_id` stamp** — add the column + index (D3); thread `mission_id`
   through work-item creation.
2. **Reconciliation spawn** — dispatcher: `approved AND no epic_id` →
   materialize a single epic linked to the mission; stamp `mission.epic_id`.
3. **`pp mission nuke <id> --dry-run`** — cascade teardown of `mission_id`
   entities (unmerged only).

This validates approve → spawn → link → nuke with real tuples, minimal blast
radius, and no execution yet. Ship, soak, then proceed.

## Later slices

- Emit structured `breakdown` from `mission-brief`; materialize child stories on
  approve.
- `pp mission run` / dashboard Execute → dispatch via `full-pipeline`.
- Rollup cascade (`in-progress` / `done`) + mission outcome_score.
- Richer mission UI (snapshot-cache-based, D7).
- Later: auto-dispatch-on-approve (drop the second trigger) once trusted.

## Risks

- **Runaway spawn** — reconciliation must be strictly idempotent (guard on
  `epic_id`); a bug spawns epics every tick. Mitigation: stamp-then-check under
  the dispatcher's single-pass housekeeping; test re-approve + restart.
- **Orphan blast radius** — if `mission_id` isn't stamped on some spawned entity,
  nuke misses it. Mitigation: stamp at the lowest common creation choke points;
  `nuke --dry-run` output is the audit that surfaces gaps.
- **Destructive nuke** — dry-run default; explicit flag for real; never touches
  merged commits.
- **Two approve paths** — do NOT hook approve; reconcile on status (D1).
- **UI regression** — mission UI must not add per-tick cost; ride the snapshot
  cache (D7), or we reintroduce the RSS problem just fixed.
- **VM/toolchain skew** — ganso schema change needs the ganso→mag→pp rebuild
  chain; work-item threading is pp-only.

## Open questions

1. Decompose = a planner pass on approve, or `mission-brief` emits structured
   `breakdown` directly at brief time? (Leaning: emit at brief time so the human
   reviews the real breakdown at the gate — but a planner pass is more flexible.)
2. Exact reconciliation placement in the dispatcher housekeeping loop.
3. Does `done` auto-archive, or wait for a human?
4. Nuke of `signal`/`token` categories — delete, or preserve for audit? (Likely
   delete task/token/event; preserve `case`/AAR since those are learning
   artifacts, even post-nuke.)
