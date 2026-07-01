# Mission Plan

Mission: `msn-a5d47af9c5ebe5898d5805f4dfd2e532` — Complete blast-radius reclamation for `pp mission nuke` + task provenance
Repo: procyon-park · Planner synthesis date: 2026-07-01

## Intent

Make `pp mission nuke <id> --confirm` reclaim the COMPLETE unmerged blast radius of a
mission. Today it cancels running workflows and deletes work-items, but leaves behind
(1) the git worktrees created for the mission's pipelines and (2) the unmerged
`impl/<id>` / `feature/<id>` branches those pipelines created; tasks are unreachable
because they are never stamped with `mission_id`. Three goals:

- **(a) Task provenance** — stamp `mission_id` onto task tuples at task-creation time
  in `WorkflowEngine`, propagated from the workflow's params (mirroring the existing
  workflow-payload stamp), so the mission's task footprint is enumerable.
- **(b) Worktree reclamation** — `nuke --confirm` also removes the mission's pipeline
  worktrees under `~/.pp/worktrees/`.
- **(c) Branch reclamation** — `nuke --confirm` also deletes the mission's UNMERGED
  feature/impl branches.

Hard boundary: unmerged artifacts only — merged commits on main are NEVER touched.
Dry-run stays the default and must enumerate the new artifact classes (tasks,
worktrees, branches). Removals must be positively scoped by `mission_id` and
fail-safe: on any ambiguity (missing stamp, unreadable state), KEEP the artifact.

## Success Criteria

1. **Task provenance (a):** Every task tuple created by `WorkflowEngine` for a
   workflow whose params carry a mission id includes `payload.mission_id` (taskPayload
   build sites `src/dispatcher/WorkflowEngine.mag` ~482–536 plus the archivist site
   ~792, mirroring the workflow stamp at ~146). Verifiable: run a mission-launched
   workflow, inspect a created task tuple — `payload.mission_id` present and equal to
   the mission id; tasks enumerable by `mission_id` for nuke's blast-radius listing.
2. **Worktree reclamation (b):** After `pp mission nuke <id> --confirm`, the
   worktrees created for the mission's pipelines are removed; worktrees belonging to
   other missions/workflows are untouched.
3. **Branch reclamation (c):** After `nuke --confirm`, the mission's UNMERGED
   branches (`feature/<instanceId>`, `impl/<instanceId>`) are deleted; branches
   already merged to main and branches of other missions are untouched.
4. **Dry-run default preserved:** `pp mission nuke <id>` without `--confirm` prints
   the full blast radius — workflows, work-items, tasks, worktrees, branches — and
   changes nothing (filesystem/git state byte-identical after).
5. **Merged history is inviolate:** No nuke path deletes or rewrites merged commits
   on main; after nuking a partially-merged mission, `git log main` is unchanged.
6. **Regression safety:** Existing suites (`mag test`, incl.
   `test/cli/test_worktree_clean.mag` WC1–WC4) still pass; new tests cover task
   stamping and the extended nuke behavior.

## Research Synthesis

The scout verified every file:line anchor in the intent and the adversarial reviewer
independently re-verified all load-bearing claims against HEAD (verdict: **SOUND**).
Combined, corrected picture:

**Anchors (exact at HEAD).** Workflow stamp `WorkflowEngine.mag:145-147`; role
taskPayload site `:482-536` (`params` in scope); archivist site `:792-803`
(`wfPayload` re-read at 783-787 makes `wfPayload at:'mission_id'` the cleanest
source). A third task site, `dispatchStrategist:` (~:811), is scope-level with no
workflow/params context and is correctly **excluded** from stamping.
`MissionCLI>>cmdNuke:` is at `MissionCLI.mag:398-476`.

**CRITICAL correction — literal "reuse" would be fail-dangerous.** The intent's
constraint "reuse worktree-clean / clean-branches" is in direct tension with verified
reality (reviewer confirmed both):

- `Repo>>cleanWorktreeTask:` (Repo.mag:347) probes `GET /api/v1/workflow/status/<id>`
  — **that route does not exist** in Server.mag (only signed POST
  /api/workflow/status:356). Live-verified: empty curl body ⇒
  `shouldRemove := true` (Repo.mag:351-352) for EVERY worktree, live ones included.
  Calling `cmdWorktreeClean:` wholesale from nuke would delete OTHER missions' live
  worktrees, violating criterion 2. (`CliPP.mag:3605` carries the same dead probe —
  its fix is a separate ticket, out of scope here.)
- `Repo>>cmdCleanBranches:` default mode (`-d`, :474) deletes only MERGED branches —
  the mission's unmerged branches would be skipped; `--all` (`-D`, :484) destroys ALL
  unmerged branches repo-wide, including other missions'.

**Resolution (per scout §3-4, reviewer-endorsed):** reinterpret "reuse" as new
**mission-scoped entry points** — `Repo>>cleanWorktreesForInstances:dryRun:` and
`Repo>>cleanBranchesForInstances:in:dryRun:` — that reuse only the *removal half* of
`cleanWorktreeTask:` (Repo.mag:380-398: `git worktree remove || rm -rf` per subdir)
and the GitOps primitives. Membership in the mission's instance-id set IS the removal
predicate; the dead probe is bypassed entirely. Do NOT change `cleanWorktreeTask:`
semantics in place — `test_worktree_clean.mag` WC1–WC4 depend on the current
probe-failure⇒remove sweep semantics.

**Branch deletion must force.** The scout's §5 template (`DiscardWorktreeAction`)
literally calls `GitOps deleteBranch:` (`-d`), which fails warn-only on unmerged
branches — the mission's branches are unmerged *by definition*. Use
`GitOps deleteBranchForce:in:` (`-D`, GitOps.mag:85) per reviewer's correction, while
keeping the template's name-based guard: delete `feature/<id>` only when `<id>` ∈ the
mission's instance-id set (never "whatever `feature_branch` says" — `parent_branch`
can be `main` for hotfix templates).

**(b)→(c) is a FUNCTIONAL dependency, not just merge serialization.** `impl/<id>` is
checked out inside its worktree; `git branch -D` refuses to delete a checked-out
branch. Worktree removal MUST precede branch deletion. Wave order b→c is mandatory.

**Enumeration gap.** `runningWorkflowsForMission:` (MissionCLI.mag:478-507) filters
`status='running'`; reclamation needs ALL statuses — terminal pipelines are exactly
the orphan-leavers. A new `allWorkflowsForMission:` (drop status filter, keep the
`payload.mission_id` / `params.mission` ownership test) is required.

**Deterministic naming + durable signals.** Worktrees live at
`~/.pp/worktrees/<instanceId>/{impl,resolve}`; branches are `impl/<instanceId>` and
`feature/<instanceId>` (wave children share the parent's `feature/<parentId>`). Each
instance's durable `worktree` signal (payload: workdir, branch, feature_branch,
repo_path) is the authoritative fail-safe source; missing signal/id ⇒ KEEP.

**Free indexing.** Ganso's `ix_mission` virtual column (GansoStore.mag:52-68) is
category-agnostic — stamped tasks become index-queryable with zero schema work.
`DispatchWavesAction` mission propagation (:191-193, :259-261) verified, so children
carry `params.mission` too.

**Test landscape.** No nuke tests exist in `test_mission_cli.mag` (MS1–MS8 only).
New tests needed for stamping and extended nuke; the criterion-5 partially-merged
fixture is the expensive one and is budgeted as its own story. Hazard: the
`scope` ivar/block-shadow trap is live in MissionCLI.mag (:410-413, :536-539) — all
new methods must use `msnScope`/`sc` parameter names.

**Shared feature branches.** Wave children share `feature/<parentId>`; the delete set
must be deduped and executed only AFTER all cancels complete (cmdNuke already cancels
first, MissionCLI.mag:456-460).

## Proposed Approach

1. **Stamp task provenance in WorkflowEngine** (independent file, wave 1): mirror the
   :145-147 workflow stamp at the role taskPayload site (from `params at:'mission'`)
   and the archivist site (from the re-read `wfPayload at:'mission_id'`). Leave
   `dispatchStrategist:` unstamped. Backward compatible by construction — all readers
   use `at:'mission_id' ifAbsent:[nil]`.
2. **Mission-scoped worktree reclamation** (wave 1, parallel with 1): add
   `MissionCLI>>allWorkflowsForMission:` (all statuses) and a new
   `Repo>>cleanWorktreesForInstances:dryRun:` that positively selects
   `~/.pp/worktrees/<id>` dirs by membership in the mission's instance-id set and
   reuses only the removal half of `cleanWorktreeTask:`. Wire into `cmdNuke:` after
   workflow cancellation; extend the dry-run listing with tasks (scan `task` by
   `payload.mission_id`) and worktrees. Generic `cmdWorktreeClean:` untouched.
3. **Mission-scoped branch reclamation** (wave 2, after 2 — same `cmdNuke:` method +
   functional ordering): add `Repo>>cleanBranchesForInstances:in:dryRun:` building
   candidates `impl/<id>` + `feature/<id>` (deduped; name-based guard), deleting via
   `GitOps deleteBranchForce:in:`, skipping branches merged to main. Wire into
   `cmdNuke:` AFTER the worktree step; extend dry-run listing with branches.
4. **End-to-end safety net** (wave 3): partially-merged-mission fixture test proving
   criterion 5 (main unchanged, merged content intact, other missions' artifacts
   untouched) plus full-suite regression run.

Each story updates `CHANGELOG.md [Unreleased]` in the same commit (repo convention);
Maggie gotchas apply (`rm` binary before rebuild, explicit test dirs, msnScope naming).

## Work Breakdown

- **Story 1 (wave 1, parallel):** Stamp `mission_id` onto task payloads at both
  WorkflowEngine sites + unit tests. Touches `src/dispatcher/WorkflowEngine.mag` only.
- **Story 2 (wave 1, parallel):** Worktree reclamation — `allWorkflowsForMission:`,
  `Repo>>cleanWorktreesForInstances:dryRun:` (probe-free, positive selection),
  `cmdNuke:` wiring, dry-run listing extended with tasks + worktrees, tests. Touches
  `src/cli/MissionCLI.mag` + `src/cli/Repo.mag`.
- **Story 3 (wave 2, depends on Story 2):** Branch reclamation —
  `Repo>>cleanBranchesForInstances:in:dryRun:` (force-delete, name-guarded, deduped,
  merged-skip), `cmdNuke:` wiring AFTER worktree removal, dry-run branch listing,
  tests. Touches the SAME `cmdNuke:` — hard serialization after Story 2.
- **Story 4 (wave 3, depends on Stories 1-3):** Partially-merged-mission fixture
  test for criterion 5 + full regression sweep (incl. WC1–WC4) + dry-run
  byte-identical assertion.

## Risks

1. **Dead status probe (fail-dangerous reuse).** Literal reuse of `cmdWorktreeClean:`
   deletes other missions' live worktrees. *Mitigation:* new probe-free
   mission-scoped entry points; membership in instance-id set is the only removal
   predicate; generic command and WC1–WC4 semantics untouched.
2. **Wrong branch-delete primitive.** `deleteBranch:` (`-d`) silently fails on
   unmerged branches. *Mitigation:* Story 3 explicitly mandates
   `deleteBranchForce:in:` (`-D`) with a name-based `feature/<id>`-in-mission-set
   guard.
3. **b→c functional ordering.** Deleting `impl/<id>` while its worktree exists fails
   (`git branch -D` refuses checked-out branches). *Mitigation:* wave-serialized
   stories; `cmdNuke:` executes worktrees-then-branches; `git worktree prune`
   fallback pattern available (GitOps.mag:151-161).
4. **Shared `feature/<parentId>` branches.** Deleting while a same-mission sibling is
   mid-flight. *Mitigation:* dedupe the delete set and run only after all cancels
   complete (cancel already precedes in cmdNuke).
5. **Lost mission→instance mapping** (if workflow tuples ever hard-deleted by GC).
   *Mitigation:* fail-safe rule — no id list ⇒ KEEP artifacts; durable per-instance
   `worktree` signal as second source.
6. **MissionCLI `scope` ivar/block-shadow trap.** Silently-nil closures. *Mitigation:*
   all new methods use `msnScope`/`sc` names; documented in each story description.
7. **Same-file merge conflicts.** Stories 2 and 3 both edit `cmdNuke:`; parallel
   same-file wave-mates cause silent method shadowing in this repo. *Mitigation:*
   strict wave serialization (2 → 3).
8. **Expensive criterion-5 test.** Fixture repo with a partially-merged mission.
   *Mitigation:* budgeted explicitly as Story 4 rather than squeezed into 2/3.

## Definition of Done

- All four stories merged to main with Conventional Commits and `CHANGELOG.md
  [Unreleased]` entries.
- A mission-launched workflow produces task tuples carrying `payload.mission_id`
  equal to the mission id (criterion 1), enumerable via the `ix_mission` index.
- `pp mission nuke <id>` (dry-run) enumerates workflows, work-items, tasks,
  worktrees, and branches, changing nothing; `--confirm` removes the mission's
  worktrees and force-deletes its unmerged `impl/*`/`feature/*` branches while other
  missions' artifacts and all merged history on main remain untouched (criteria 2–5).
- `mag test` fully green, including `test_worktree_clean.mag` WC1–WC4 unchanged,
  new stamping tests, extended nuke tests, and the partially-merged fixture test
  (criterion 6).
