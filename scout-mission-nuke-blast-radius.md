# Scout findings — mission msn-a5d47af9c5ebe5898d5805f4dfd2e532
## Harden `pp mission nuke`: complete blast-radius reclamation + task provenance

Scout: mission-brief-1782947928-19351 · 2026-07-01

---

## 1. Verified anchor points (intent's file:line claims checked against HEAD)

| Intent claim | Verified location | Status |
|---|---|---|
| workflow payload mission_id stamp | `src/dispatcher/WorkflowEngine.mag:145-147` (`workflowPayload at: 'mission_id' put: (params at: 'mission')`) | ✅ exact |
| role-bearing taskPayload build site | `src/dispatcher/WorkflowEngine.mag:480-537` (`taskPayload` built at 482, `bbs out: 'task'` at 536) | ✅ exact |
| archivist task site | `src/dispatcher/WorkflowEngine.mag:755-807` (`dispatchArchivist:`, taskPayload 792-801, out at 803) | ✅ exact |
| `MissionCLI>>cmdNuke:` | `src/cli/MissionCLI.mag:398-476` | ✅ exact |
| `Repo>>cmdWorktreeClean:` | `src/cli/Repo.mag:273-325` | ✅ |
| `Repo>>cleanWorktreeTask:dryRun:ppUrl:tmpFile:` | `src/cli/Repo.mag:336-399` | ✅ |
| `Repo>>cmdCleanBranches:` | `src/cli/Repo.mag:403-502` | ✅ |
| `test/cli/test_worktree_clean.mag` | exists; invariants WC1–WC4 (per-task rescue, failure tally) | ✅ |

There is a **third** task-creation site: `WorkflowEngine>>dispatchStrategist:` (~line 811) — it is
scope-level, has no workflow/params context, and legitimately stays unstamped. Confirm in plan
that it's excluded.

## 2. Naming/provenance map (how mission → worktrees/branches resolves)

- Workflow instance ids: `<template>-<epoch>-<assignId>` (`WorkflowEngine.mag:131`).
- Worktree dir: `~/.pp/worktrees/<instanceId>/impl` (`actions/CreateWorktreeAction.mag:42`);
  resolver adds `/<instanceId>/resolve` (`actions/SyncWorktreeAction.mag:41`).
- Branches: `impl/<instanceId>` (`CreateWorktreeAction.mag:41`) and `feature/<instanceId>`
  (`CreateWorktreeAction.mag:69`, `actions/DispatchWavesAction.mag:102`). Wave children reuse the
  parent's `feature/<parentInstanceId>` via `parent_branch` (`CreateWorktreeAction.mag:51-57`).
- Per-instance **worktree signal**: `bbs upsertSignal: instanceId identity: 'worktree'` with payload
  `{workdir, branch, feature_branch, repo_path, standalone, parent_branch?}`
  (`CreateWorktreeAction.mag:94-109`). `signal` is a DURABLE category (`src/bbs/BBS.mag:1043`), so
  this is the authoritative, fail-safe source of exactly-what-to-delete per instance.
- Mission → workflows: every workflow (top-level via `Server.mag:2666` `wfParams at:'mission'`,
  children via `DispatchWavesAction.mag:191-193,259-261`) carries `params.mission`, and
  `WorkflowEngine.mag:145` stamps `payload.mission_id`. Enumerating `workflow` tuples by
  `payload.mission_id` therefore covers the whole pipeline tree.
- Ganso index: `ix_mission` is a **category-agnostic** virtual column on
  `$.payload.mission_id` (`src/bbs/GansoStore.mag:52-68`) — stamping tasks makes them
  index-queryable with ZERO schema work.

## 3. Critical finding — the worktree-clean "status probe" is DEAD CODE (fail-dangerous)

`Repo>>cleanWorktreeTask:` (Repo.mag:347) probes `GET /api/v1/workflow/status/<id>`.
**That route does not exist in `src/api/Server.mag`** — only signed `POST /api/workflow/status`
(Server.mag:356, handler at 1999). Verified live:

```
curl -s -o /dev/null -w "%{http_code}" http://localhost:7777/api/v1/workflow/status/mission-brief-1782947928-19351
→ 302, empty body     (this is a LIVE, running workflow)
```

Empty body → `statusResponse isEmpty ifTrue: [shouldRemove := true]` (Repo.mag:351-352).
So with a live server, `pp worktree clean` (non-dry-run) **removes EVERY worktree under
`~/.pp/worktrees/`, live ones included**. `src/cli/CliPP.mag:3605` has the same dead probe.

Consequences for this mission:
- Calling `cmdWorktreeClean:` wholesale from `cmdNuke:` would delete OTHER missions' live
  worktrees → directly violates success criterion 2 ("worktrees of other missions untouched").
- The mission-scoped path must **positively select** task dirs whose name ∈ the mission's workflow
  instance ids, then reuse only the *removal half* of `cleanWorktreeTask:` (Repo.mag:380-398:
  `git worktree remove || rm -rf` per subdir, then `rm -rf` task dir). Do NOT rely on the probe.
- Recommend a new entry point, e.g. `Repo>>cleanWorktreesForInstances: ids dryRun: d` that skips the
  probe entirely (membership in the mission id-set IS the removal predicate), leaving generic
  `cmdWorktreeClean:` untouched — `test_worktree_clean.mag` WC1–WC4 depend on current sweep
  semantics and would break if the probe were made fail-safe in place.
- The dead probe itself deserves its own fix ticket (out of this mission's scope, but flagged).

## 4. clean-branches reuse is also not mission-shaped as-is

`Repo>>cmdCleanBranches:` (Repo.mag:403-502):
- default mode deletes only branches **merged** to main (`branch -d`, Repo.mag:474) — the mission's
  UNMERGED branches would be *skipped*;
- `--all` force-deletes **every** unmerged `impl/*`/`feature/*` in the repo (Repo.mag:484) — would
  destroy other missions' branches.

Neither matches criterion 3. The implementer needs a mission-scoped variant (e.g.
`Repo>>cleanBranchesForInstances: ids in: repoPath dryRun: d`) that:
- builds candidate names `impl/<id>` + `feature/<id>` from the mission's instance ids (or better:
  reads each instance's worktree signal `branch`/`feature_branch`);
- deletes via existing `GitOps deleteBranchForce:in:` (`src/dispatcher/GitOps.mag:85-93`, warns not
  signals) — `branch -D` is required since these are unmerged by definition;
- leaves merged branches alone (criterion 3 says merged branches are *untouched*, not merely safe);
  merged-ness check primitives exist: `git branch --merged main` (Repo.mag:444) and
  `GitOps commitsAheadOf:on:in:` (GitOps.mag:106).
- `GitOps branchExists:in:` (GitOps.mag:97) for the dry-run listing.

Repo path resolution: mission scope → `Repo new repoForName: scope → at:'path'` (same as
`CreateWorktreeAction.mag:16-21`); per-instance `repo_path` from the worktree signal is more precise
and handles multi-repo edge cases.

## 5. Ordering constraint between (b) and (c) — beyond "same method"

`impl/<instanceId>` is **checked out inside the worktree**; `git branch -D` refuses to delete a
branch checked out in any worktree. So worktree removal (b) MUST run before branch deletion (c)
inside `cmdNuke:`. This makes the intended wave order (b) → (c) not just a merge-conflict
serialization but a functional dependency. `DiscardWorktreeAction.mag` (whole file, 74 lines) is a
proven per-instance teardown template with exactly this order: checkout main → removeWorktree →
deleteBranch impl → deleteBranch feature-only-if-`feature/<instanceId>` (line 57's guard against
deleting shared parent branches).

Also: `git worktree remove` can leave a registered-but-gone worktree; `GitOps
pruneEphemeralWorktree:` (GitOps.mag:151-161) shows the `worktree prune` fallback pattern.

## 6. Enumeration gap in cmdNuke today

`MissionCLI>>runningWorkflowsForMission:scope:` (MissionCLI.mag:478-507) filters
`status = 'running'`. Worktree/branch reclamation needs **ALL** mission workflows regardless of
status — completed/failed/cancelled pipelines are precisely the ones that leave orphaned worktrees
and unmerged branches. Needs an `allWorkflowsForMission:` variant (drop the status filter, keep the
`payload.mission_id` OR `params.mission` ownership test at lines 499-500). The dry-run listing
(criterion 4) must then enumerate: workflows, work-items, **tasks** (new — scan category `task`,
filter `payload.mission_id`, mirroring `workitemsForMission:` at 530-554), worktree dirs
(File-exists per instance id), and branches (branchExists per candidate).

## 7. Task stamping (a) — mechanics per site

- **Role site** (WorkflowEngine.mag:482-536): `params` is in scope and carries `mission`; mirror
  lines 145-147: `(params at: 'mission' ifAbsent: [nil]) notNil ifTrue: [taskPayload at:
  'mission_id' put: (params at: 'mission')]`.
- **Archivist site** (WorkflowEngine.mag:792-803): the workflow tuple is already re-read at 783-787;
  cleanest to read `wfPayload at: 'mission_id' ifAbsent: [nil]` directly (already-stamped value)
  rather than `params at: 'mission'` — both available.
- Backward compat is naturally satisfied: all read paths in the repo use `at:'mission_id'
  ifAbsent:[nil]` (e.g. Dispatcher.mag:884,915; MissionCLI.mag:499,548).
- Dispatcher rollup already reads `task`-adjacent provenance by mission (`Dispatcher.mag:905-921`
  for workitems) — stamped tasks slot into the same pattern.

## 8. Test landscape

- `test/cli/test_mission_cli.mag` covers MS1–MS8 (category shape, start/show/approve/reject/list) —
  **no nuke tests exist at all**. New tests must build mission+workflow+task tuples and fake
  worktree dirs/branches; the WCTest harness in `test/cli/test_worktree_clean.mag` shows the
  convention (real temp dirs, per-task rescue assertions).
- `test_worktree_clean.mag` invariants implicitly depend on probe-failure ⇒ remove; don't change
  `cleanWorktreeTask:` semantics in place (see §3).
- Remember `mag test` dirs must be listed explicitly (non-recursive), and `rm` the binary before
  rebuild to surface compile errors.

## 9. Risks / open questions

1. **Dead status probe** (§3) — biggest trap; "reuse worktree-clean" taken literally deletes other
   missions' live worktrees.
2. **GC of workflow tuples**: `pp gc` / `reapStaleWorkflows` mutate workflow status; if any sweep
   ever hard-deletes workflow tuples, the mission→instance-id mapping is lost and worktrees/branches
   must be KEPT (fail-safe). The worktree signal (scope=instanceId) is a second, durable source —
   but its scope key is the instance id, so you still need the id list first.
3. **Wave-shared feature branches**: a child's `feature_branch` = parent's `feature/<parentId>`;
   deleting it while another *same-mission* child is mid-flight is fine post-cancel, but the delete
   set should be deduped and only executed AFTER all cancels complete (cmdNuke already cancels
   first, MissionCLI.mag:456-460).
4. **`scope` ivar/block-shadow trap** is live in this exact file — MissionCLI.mag:410-413 and
   536-539 document silently-nil closures; all new methods must use `msnScope`/`sc` param names.
5. **Repo.mag `Shell capture:` reliability**: memory notes Shell capture:timeout:/run:timeout: are
   unreliable; Repo.mag uses the shellRun-to-tmpfile pattern instead — keep that pattern.
6. **Criterion 5 (merged history inviolate)** is easy to satisfy (nothing here touches main) but the
   test for it ("git log main unchanged") requires a fixture repo with a partially-merged mission —
   the most expensive test in the set.
7. `parent_branch` may be `'main'` for hotfix templates (`CreateWorktreeAction.mag:50`) — the
   feature-branch delete guard MUST be name-based (`feature/<id>` ∈ mission id set), never
   "delete whatever `feature_branch` says" (DiscardWorktreeAction.mag:57 already models this).

## 10. Suggested decomposition (consistent with intent's serialization note)

- **Wave 1 (parallel):**
  - (a) stamp `mission_id` in the two `WorkflowEngine.mag` task sites + unit test.
  - (b) `cmdNuke:` worktree reclamation: `allWorkflowsForMission:` + `Repo>>cleanWorktreesForInstances:`
    + dry-run listing extension (tasks + worktrees) — edits MissionCLI.mag + Repo.mag.
- **Wave 2 (after b):**
  - (c) branch reclamation: `Repo>>cleanBranchesForInstances:` + `cmdNuke:` wiring after the
    worktree step + dry-run listing of branches — edits the SAME `cmdNuke:` + Repo.mag.
- Each change updates `CHANGELOG.md [Unreleased]` in the same commit (repo convention).
