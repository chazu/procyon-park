# Scout Findings — Mission Archival / Retirement

Mission: `msn-475b0d93e8f9827f7ee2dd4251c60090`
Brief: Add a terminal `archived` mission state + `pp mission archive <id>` verb + dashboard filtering so decided missions leave the active board.

## TL;DR for the implementer

This is a **small, well-bounded change touching 3 production files + 2 test files**. The codebase already
has every pattern you need to copy. There are **no server routes to add** and **no status-allowlist to extend**
(missions have none). The two subtle points are (1) the `ppCommands` allowlist is at the *top-level command*
granularity — `mission` is already registered, so only the subcommand dispatcher needs a new branch; and
(2) `renderMissionsHtml:` partitions into exactly two buckets today — you add a third.

---

## Relevant files / methods (with exact anchors)

### 1. CLI verb — `src/cli/MissionCLI.mag`
- **`runWith:` dispatcher** (lines 58–73): subcommand table. Add `sub = 'archive' ifTrue: [^self cmdArchive: args].`
  alongside the existing `approve`/`reject` branches (lines 68–69).
- **`setStatusFor:to:reason:action:`** (lines 308–334): the guarded read-modify-write helper the intent
  names. **It is hard-wired to `awaiting-approval` only** (line 325: `cur = 'awaiting-approval' ifFalse: [...refuse...]`).
  It CANNOT be reused as-is for archive, because archive's guard is a *different, wider* set
  (`approved | rejected | researching`). Two viable approaches:
  - **(A, recommended)** Add a sibling guarded helper, e.g. `setStatusFor:to:reason:action:allowedFrom:`
    that takes an allowed-status collection, and have both `cmdApprove:`/`cmdReject:` (via `#('awaiting-approval')`)
    and `cmdArchive:` (via `#('approved' 'rejected' 'researching')`) call it. Keeps one write path, one refusal-message shape.
  - **(B)** Write a standalone `cmdArchive:` that inlines the read-modify-write. Simpler diff but duplicates the
    rdp→guard→put→stamp logic. (A) is cleaner and matches the "reuse the guarded helper" constraint.
- **`putMission:payload:`** (lines 113–123): the signed write path (`/api/bbs/put`, modality `linear`). Archive
  reuses this verbatim — no change.
- **`cmdApprove:` / `cmdReject:`** (lines 296–306): copy this shape for `cmdArchive:`. Note `reject` threads a
  `--reason`; archive likely wants an optional `--reason` too (abandon rationale), but that's a judgment call.
- **`printUsage`** (lines 360–372): add an `archive` line and update the lifecycle comment to
  `researching -> awaiting-approval -> approved | rejected -> archived`. The header doc comment (lines 4–6) also
  states the lifecycle and should be extended.
- **`updated_at` stamping**: `setStatusFor:...` already stamps `p at: 'updated_at' put: DateTime now epochSeconds`
  (line 331). Preserve this in the archive path.

### 2. Command registration — ALREADY DONE at the top level
- `src/Main.mag:11` — `ppCommands` allowlist **already contains `'mission'`**.
- `src/cli/PP.mag:59` — `cmd = 'mission' ifTrue: [^MissionCLI new runWith: args]` already dispatches.
- **=> The `ppCommands` requirement in success-criterion #1 is satisfied by the existing top-level entry.**
  A new *subcommand* (`archive`) only needs the `MissionCLI>>runWith:` branch. The memory note
  "register in BOTH runWith: AND ppCommands" applies to NEW top-level commands, not mission subcommands.
  Do NOT add `'archive'` to `Main ppCommands` — that list is top-level verbs, not mission subcommands.

### 3. Dashboard filtering — `src/api/DashboardSSE.mag`
- **`renderMissionsHtml:`** (lines 499–545): today it partitions missions into exactly two arrays:
  `awaiting` (status = `awaiting-approval`, line 516) and `others` (everything else, line 518). Every non-awaiting
  status — including a future `archived` — falls into `others` and renders in the "Other missions" group (lines 535–543).
  **The change:** add a third bucket. In the partition loop (lines 511–522) route `st = 'archived'` to a new
  `archived` array instead of `others`. Then EITHER omit `archived` from the default render, OR (preferred per
  intent's "keep the audit trail") emit it inside a collapsed `<details><summary>Archived (N)</summary>...</details>`
  section AFTER the "Other missions" group.
- **Per-card guard structure MUST be preserved** (intent's SSE-re-render-safety constraint): each mission is rendered
  inside `[self renderMissionCard: m awaiting: false] on: Exception do: [...]` (lines 537–541); the whole panel is
  wrapped by `safeRender:rootId:do:` (line 178). Keep both guards intact for the new archived section — mirror the
  existing `others` block exactly.
- **`renderMissionCard: m awaiting:`** (lines 547–587) needs NO change — archived cards render fine as non-awaiting
  (no approve/reject controls, plan collapsed). The card's CSS class already includes the status
  (`mission-', status`, line 573) so `.mission-archived` is stylable if desired (no CSS required for correctness).
- This is a **pure server-render change, zero schema/route changes** — consistent with doctrine
  dctr-51c2ba0e (SSE dashboards re-render wholesale; hook the render path, not client filters).

### 4. Status model — NO allowlist to update (important)
- Grepped for a mission status validator: **none exists.** `WorkItemFields validStatuses`
  (`src/bbs/WorkItemFields.mag:10`) is for WORK ITEMS only; Server.mag:2213/2232 validates work-item status, not mission.
- Mission `payload.status` is a **free-form string** written directly (`cmdStart:` line 189, `cmdSetPlan:` line 290,
  `setStatusFor:` line 329). `Categories.mag:10` lists `mission` as a valid *category* but there is no per-status set.
- **=> Success-criterion #4 is effectively a no-op:** `pp mission list --status archived` **already works today**,
  because `cmdList:` (lines 207–227) filters by exact string equality (`st = statusFilter`, line 219) with no
  allowlist gate. The plan should state this explicitly rather than hunting for a set to extend.

### 5. Terminal semantics — satisfied "for free"
- Both the CLI guard (`setStatusFor:` line 325) and the **server route**
  `handleMissionDecisionScope:identity:to:reason:` (`src/api/Server.mag:2518`, guard at line 2531) refuse any
  approve/reject unless `cur = 'awaiting-approval'`. An `archived` mission is not `awaiting-approval`, so it can
  **never** be approved/rejected/re-decided through either path. No regression, no extra guard needed for
  criterion #3. (The dashboard approve/reject controls only attach to awaiting cards, line 564, so archived cards
  carry no controls either.)

## Tests to add

- **`test/cli/test_mission_cli.mag`** — the `TestMissionCLI` in-memory harness (lines 71–116) already stubs the
  BBS seams (`scanMissions`/`rdpMission:`/`putMission:payload:`) so `cmdArchive:` runs end-to-end with no server.
  `MsnHelper seedMission:id:status:brief:` (lines 145–156) seeds arbitrary statuses. Register new cases in
  `TestMissionCli class>>run:` (lines 161–175). Suggested cases:
  - `archive` succeeds from `approved`, from `rejected`, from `researching` (status → `archived`).
  - `archive` REFUSED from `awaiting-approval` (status unchanged, mirrors MS7 pattern lines 348–367).
  - optional: `archive` refused from already-`archived` (terminal sink) — depends on whether `archived` is in the
    allowed-from set (it is NOT, so this is refused automatically).
- **`test/api/test_mission_panel.mag`** — extend `TestMissionPanelRender` (lines 126–163). Seed an `archived`
  mission via `MsnPanelHelper seedMission:id:status:brief:plan:` (lines 92–102), render via
  `sse computeSnapshot` → `missionsHtml`, and assert the archived mission id does **NOT** appear inside the
  "Other missions" group (use the `count:in:` helper, lines 112–123, and/or assert it's inside an `Archived`
  `<details>` block if you render one). Existing MP1–MP8 must stay green.
- Test dirs must be listed explicitly for `mag test` (memory: feedback_maggie_test_dirs) — both dirs are already
  covered by the existing suite wiring.

## Constraints / risks / gotchas

- **No new server routes** (intent constraint): archive is CLI-only via signed `/api/bbs/put`. Do NOT add an
  `/api/mission/archive` route. (Unlike approve/reject which ALSO have a loopback-gated dashboard route at
  Server.mag:2499 for the web buttons — archive intentionally has no web control this pass.)
- **Maggie gotchas** (from memory): declare temp vars at top of method; use `Character lf` not `newline`;
  `and:`/`ifTrue:` chaining must nest, not `and: [..] and: [..]`; `HttpServer queryParam:` returns `''` not nil
  (not relevant here — no new route). The build caches — `rm` the binary before rebuild to surface compile errors.
- **Parser is strict** (memory: project_maggie_parser_strict): a parse error in an edited `.mag` now ERRORS rather
  than silently skipping — good, but means a typo in `MissionCLI.mag` fails the whole CLI load.
- **`CHANGELOG.md` exists** at repo root; per CLAUDE.md the `[Unreleased]` section must be updated in the SAME
  commit as the change (a `feat(mission): add archive verb + archived status` entry).
- **Lifecycle vocabulary**: use `archived` consistently (intent explicitly forbids a project-wide rename to
  `retired`; the doctrine parallel to `maturity:retired` is naming inspiration only, not a code touch).

## Open questions for the planner

1. **Does `archive` take `--reason`?** `reject` does; archiving a *researching* mission to "abandon a stuck
   mission" arguably wants an abandon rationale. Low cost to include; `setStatusFor:` already handles an optional
   reason (lines 330). Recommend: yes, optional `--reason`.
2. **Render archived collapsed vs fully omit?** Intent criterion #6 allows either but *prefers* a collapsed
   `Archived` `<details>` to keep history visible in the dashboard (recoverable regardless via
   `pp mission list/show`). Recommend the collapsed section for auditability; it's a few lines mirroring the
   `others` block.
3. **Guarded-helper refactor (A) vs standalone `cmdArchive:` (B)?** (A) generalizes `setStatusFor:...` with an
   allowed-from set and keeps one write path (matches the "reuse the guarded helper" constraint most literally);
   (B) is a smaller diff but duplicates logic. Recommend (A).

## Success criteria that look harder than they are (and one that looks easy but has a wrinkle)

- **#4 "Status model updated"** — looks like real work; is a **no-op** (no mission status allowlist exists;
  `--status archived` already works). Just document it.
- **#3 "Terminal semantics"** — already enforced by the existing awaiting-only guards on both CLI and server. No work.
- **The one wrinkle: reusing `setStatusFor:to:reason:action:`** — the intent says "reuse" it, but its guard is
  literally `= 'awaiting-approval'` (line 325). You must generalize it (approach A) or fork it (B); you cannot call
  it unchanged for archive. This is the single non-mechanical decision in the mission.
