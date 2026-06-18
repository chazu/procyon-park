# Scout: Dashboard "Active Workflows" shows non-active workflows

Date: 2026-06-18
Scope: `procyon-park:src/api` (render) + `procyon-park:src/dispatcher` (reaper) + `procyon-park:src/cli` (gc)
Mode: read-only research. No code changes — recommendations only.

## TL;DR

Two distinct faults, only the first explains the symptom:

- **(A) PRIMARY — confirmed.** The "Active Workflows" panel filters on
  `status = 'running'` (`src/api/DashboardSSE.mag:499`) with **no liveness
  check**. Nothing ever transitions a stuck `running` workflow to a terminal
  state: the Dispatcher reaper only reaps **task claims**, never the **parent
  workflow tuple** (`classifyWorkflow:` treats every non-terminal status as
  *live* — `Dispatcher.mag:555-556`). `pp gc` has the same blind spot
  (`SystemCommands.mag:423`). A workflow whose `status` never left `running`
  is therefore **immortal**: shown as active forever, never reaped, never
  gc'd — even with all its agents long dead.

- **(B) SECONDARY — intermittent.** The recurring log line
  `SSE render failed for workflows: Message not understood: ifTrue:`
  is a *separate*, data-dependent render fault caught by `safeRender:`
  (`DashboardSSE.mag:185`), which replaces the panel with a fallback "render
  error" fragment on that tick. It does **not** produce the "two stale cards"
  symptom (that requires the render to *succeed*). Root cause: unguarded
  field reads in `renderWorkflowsHtml:` that feed a comparison / `ifTrue:`
  with a value that is `nil` when a workflow/task tuple is malformed or read
  mid-write. Strongest single candidate: `DashboardSSE.mag:519`.

---

## STEP 1 — The two workflows + their TRUE state

Enumerated via `pp read workflow default` and `pp read workflow procyon-park`.
Both currently render in "Active Workflows". Reference `now ≈ 1781785996`.

| identity | scope | payload.status | started_at | terminal ts | true age | TRUE state |
|---|---|---|---|---|---|---|
| `story-lite-1777641262-13342` | default | `running` | 1777641262 | *(none)* | **≈ 48 days** | OLD / abandoned. No live tasks/claims; agents dead. Never transitioned. |
| `story-1781750008-13506` | procyon-park | `running` | 1781750008 | *(none)* | ≈ 10 h | CANCELLED story (`story:aar-skel:category`). Work item cancelled, but cancellation never propagated to the workflow tuple — still `running`. |

Key fact: **both tuples literally carry `status = "running"`** with **no**
`failed_at` / `cancelled_at` / `completed_at`. Neither is "failed" in the
tuplespace; both are *stuck-running*. Their TRUE state is "abandoned /
cancelled with no live workers", but the workflow tuple was never updated, so
every status-based consumer (dashboard, housekeep, gc) treats them as alive.

For contrast, the 11 other procyon-park workflows correctly carry
`status: completed` + `completed_at`, and the 3 in-flight scout/story tuples
are genuinely `running` with live tasks — those *should* show.

---

## STEP 2 — The filter predicate + the ifTrue: source

### (a) The "active" predicate

`src/api/DashboardSSE.mag` — `computeSnapshot` (line 152, `bbs scanAll:
'workflow'`) → `renderWorkflowsHtml:` (line 488). The snapshot scans **all
scopes** (no scope filter), so a stale `default`-scope tuple appears next to
`procyon-park` ones.

The predicate, `renderWorkflowsHtml:` line **498-499**:

```smalltalk
status := p at: 'status' ifAbsent: ['?'].
status = 'running' ifTrue: [        "<-- the ONLY gate for 'active'"
    ... render card ...
]
```

- The filter is **only** `status = 'running'`. No check that the workflow has
  any live/dispatched task, no age/TTL bound, no liveness probe.
- It does **not** include terminal statuses (good — those go to
  `renderCompletionsHtml:`), but it **trusts the stored status absolutely**.
  Because nothing ever flips a stale tuple off `running`, the gate passes
  forever.

**Client side:** `static/dashboard.html:906` `#dashboard-workflows` is a pure
SSE patch target — the server HTML is injected verbatim. There is **no
client-side filtering** of the workflows panel (the `*-filters` controls at
lines 928/954/970 are for the Activity, Kanban, and History panels only). So
the fix must be server-side.

### (b) The `ifTrue:` doesNotUnderstand

`safeRender: 'workflows' ...` (`DashboardSSE.mag:165`) wraps
`renderWorkflowsHtml:`; on throw it logs
`'SSE render failed for workflows: ' , e messageText` (line 186) and returns
the fallback error fragment (lines 187-190).

I could **not** find a *static* `ifTrue:` sent to a non-Boolean in
`renderWorkflowsHtml:` — every literal `... ifTrue:` receiver there is a
boolean expression (lines 499, 503, 513, 524). The DNU is therefore
**data-dependent / transient**, fired when a workflow or task tuple is
malformed or read mid-write and a field that should be comparable/boolean is
`nil`. Hotspots, in priority order:

1. **`DashboardSSE.mag:518-521` (strongest candidate).**
   ```smalltalk
   dispatchedAt := tp at: 'dispatched_at' ifAbsent: [0].
   durStr := dispatchedAt > 0
     ifTrue: [self formatDuration: (now - dispatchedAt)]
     ifFalse: ['pending'].
   ```
   If a task payload contains the key `dispatched_at` with an **explicit
   `nil`** value, `at:ifAbsent:` returns that `nil` (the absent-block does not
   run). Then `nil > 0` / the `ifTrue:` chain receives a non-Boolean. (Same
   class of failure as the `formatDuration:` `<` on line 836 if `now -
   dispatchedAt` is non-integer.)

2. **`DashboardSSE.mag:524-528`** — compound predicate reads `tStatus` /
   `executedBy` from the task payload; a non-string `executedBy` makes
   `executedBy isEmpty` (and the surrounding `and:`/`ifTrue:`) throw.

3. **`DashboardSSE.mag:507`** — `(launchedBy isNil or: [launchedBy isEmpty])
   ifTrue:` throws if `launched_by` is present but non-string.

NOTE: I could not deterministically reproduce the exact failing selector
without a captured malformed tuple; (B) is independent of the visible symptom,
which is fully explained by (A).

### Related: the housekeep `and:` log line

The lead also cites recurring `Dispatcher tick step error (housekeep):
Message not understood: and:` (wrapper `Dispatcher.mag:114-115`). I audited
every `and:`/`or:` chain reachable from `housekeep`:

- `reapStuckDispatchedIn:` `Dispatcher.mag:347-350`
- `maybeRemoveTerminalOrphanTask:` `:649`, `:653`, `:656`
- `cleanTerminalOrphanTasks` `:706-709`
- `classifyWorkflow:` `:555`
- `isOrphanWf:` `:635`, orphan sweep `:512-513`

**All are correctly *nested* `X and: [Y and: [Z]]`** — none exhibit the flat
`and: [x] and: [y]` → `and:and:` selector bug described in the memory note
`feedback_maggie_and_chaining`. A repo-wide grep for `] and: [` / `] or:`
chains returned **no matches**. Conclusion: the static chaining bug is **not
present in this worktree** — the recurring `and:` housekeep error is either
already-fixed-and-stale in the log, or a runtime non-Boolean receiver (most
plausibly line `512`'s region or a corrupt payload reaching a wrapped inner
handler). Flagged for confirmation against a fresh server log; not reproduced.

---

## STEP 3 — Root cause + ranked fixes

### Root cause per instance

- `story-lite-1777641262-13342` (OLD): workflow tuple stuck `running` ~48d.
  No workflow-level liveness reaper exists; `classifyWorkflow:`
  (`Dispatcher.mag:555-556`) only ages-out *already-terminal* workflows, so a
  `running` tuple is permanently classified live and never cascade-removed.
  Dashboard `status='running'` gate (`DashboardSSE.mag:499`) then shows it.

- `story-1781750008-13506` (cancelled): the cancel path updated the work item
  but **did not** transition the workflow tuple to `cancelled`
  (no `cancelled_at`, status still `running`). Same downstream consequence.

Shared upstream defect: **a stuck-`running` workflow is immortal** across all
three status-trusting consumers — dashboard render, Dispatcher housekeep, and
`pp gc` (`SystemCommands.mag:423`).

### Ranked fixes

**Fix 1 — Workflow staleness reaper (HIGH impact, MED effort). Addresses the actual symptom + the immortality.**
Add a Dispatcher pass (sibling to `reapStuckDispatched`) that transitions a
workflow `running` → `failed` (`last_failure_reason: 'workflow-stale'`,
stamp `failed_at`) when it has **no live/dispatched task** AND
`now - started_at > TTL`. Once terminal, existing housekeep
(`classifyWorkflow:` `:558-564`) and `pp gc` (`:423`) reap it normally, and it
moves to "Recent Completions". This is the correct, durable fix: the dashboard
filter then needs no change.
*Files:* `src/dispatcher/Dispatcher.mag` (new method + tick wiring near
`:98-102`); ensure cancel path stamps `cancelled_at` too.

**Fix 2 — Tighten the active predicate (HIGH impact, LOW effort). Immediate mitigation.**
In `renderWorkflowsHtml:` (`DashboardSSE.mag:499`), require *evidence of
liveness*, not just stored status:
require `status = 'running'` **and** `self activeTaskFor: wfId in: allTasks`
returns non-nil (a live/dispatched/claimed task), **or** bound the card by age
(`now - started_at < staleWindow`). Hides zombies without waiting for Fix 1.
Trade-off: cosmetic only — the zombie tuples still exist and still bloat
storage until Fix 1/3 reaps them.

**Fix 3 — `pp gc` should sweep stale-running too (MED impact, LOW effort).**
In `gcWorkflows:` (`SystemCommands.mag:423`), extend the predicate: also
collect workflows that are `running` but past TTL with no live tasks (mirror
Fix 1's liveness test). Gives operators a manual escape hatch
(`pp gc`) for the existing two zombies today.

**Fix 4 — Guard the workflows render against non-Boolean reads (MED impact, LOW effort). Fixes the ifTrue: DNU (B).**
In `renderWorkflowsHtml:`: coerce/guard field reads before comparison —
e.g. `dispatchedAt := tp at: 'dispatched_at' ifAbsent: [0]. (dispatchedAt
isKindOf: Integer) ifFalse: [dispatchedAt := 0]` before line 519; guard
`status`/`executedBy`/`launchedBy` as Strings. `safeRender:` already prevents
a panel-wide crash, but guarding stops the per-tick error spam and the
fallback flicker.

### Recommended sequence
Fix 2 (instant cosmetic relief) → Fix 1 (durable reaper, the real fix) →
Fix 3 (manual gc parity) → Fix 4 (render robustness / log hygiene).
Manual one-off cleanup of the two existing zombies: after Fix 3, run
`pp gc`; or transition them by hand.

---

## Evidence index (file:line)

- Active filter (sole gate): `src/api/DashboardSSE.mag:498-499`
- Snapshot scans all scopes: `src/api/DashboardSSE.mag:152`
- `safeRender` fallback + log: `src/api/DashboardSSE.mag:165,185-190`
- ifTrue: hotspots: `src/api/DashboardSSE.mag:518-521` (top), `:524-528`, `:507`
- No workflow liveness reaper (running always "live"): `src/dispatcher/Dispatcher.mag:555-556`
- Task-claim reapers (do NOT touch workflow tuple): `:147,303`
- housekeep terminal-only cascade: `:494-501`, `classifyWorkflow:` `:542-565`
- and: chains all correctly nested (no flat-chain bug): `:347-350,512-513,649-656,706-709`
- `pp gc` terminal-only predicate: `src/cli/SystemCommands.mag:423`
- Client panel is server-rendered, no client filter: `static/dashboard.html:906-909`
