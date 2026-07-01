# Scout Findings: Mission-panel epic/story tree

Mission: `msn-9f731047bc1a69c655d83b9e86aec898`
Brief: Render each non-archived mission's epic + child stories as an indented tree
under the mission card, off the already-cached DashboardSSE snapshot. ONE story,
edits `src/api/DashboardSSE.mag` + `test/api/test_mission_panel.mag` only.

---

## 1. Where the work lands (files + methods)

| File | Symbol | Line | Role |
|------|--------|------|------|
| `src/api/DashboardSSE.mag` | `computeSnapshot` | 176–204 | Builds the per-tick snapshot. `snap at:'missions'` (186) and `snap at:'workitems'` (181) are ALREADY populated. `missionsHtml` is pre-rendered here (203–204). **No change needed here** — data is already present. |
| `src/api/DashboardSSE.mag` | `renderMissionsHtml: snap` | 544–616 | Partitions missions into awaiting / others / archived, sorts each, renders cards. Receives the whole `snap`, so it already has `workitems`. **Primary edit site.** |
| `src/api/DashboardSSE.mag` | `renderMissionCard: m awaiting: isAwaiting` | 633–683 | Renders one card. Currently takes ONLY the mission tuple. **Must be extended (or supplemented) to render the tree.** |
| `src/api/DashboardSSE.mag` | `sortMissions: arr` | 618–631 | Deterministic sort by identity. Template for a `sortStories:` helper. |
| `src/api/DashboardSSE.mag` | `htmlEscape: aString` | 1080–1093 | Escapes `& < > " '`. **Reuse for every story field.** |
| `test/api/test_mission_panel.mag` | `TestMission*` classes + `MsnPanelHelper` | 1–513 | Add a new render test class + register it in the runner. |

---

## 2. The data model — mission → epic → story (VERIFIED, with citations)

Confirmed in `src/dispatcher/Dispatcher.mag` (`reconcileMission:`, `materializeStory:`):

- **Mission tuple** (`snap at: 'missions'`): `identity` = `mid`;
  `payload.epic_id` = `'epic-' + mid` — stamped by reconciliation
  (`Dispatcher.mag:693, 730`). Awaiting-approval missions have NO `epic_id` yet.
- **Epic workitem** (`snap at: 'workitems'`): `identity` = `epic_id`;
  `payload.type` = `'epic'`; `payload.parent` = `nil`; `payload.title`,
  `payload.status` (`'ready'`), `payload.wave` (`1`), `payload.mission_id` = mid
  (`Dispatcher.mag:694–713`).
- **Story workitem** (`snap at: 'workitems'`): `identity` = `'story-'+mid+'-'+idx`;
  `payload.type` = `'story'`; `payload.parent` = `epic_id`; `payload.title`,
  `payload.status`, `payload.wave` (`Dispatcher.mag:761–769`).

**Association is a pure in-memory filter, no query needed (satisfies the RSS-runaway constraint):**
```
epicId  := mission.payload.epic_id
epic    := workitems detect: [:w | w identity = epicId]           "the epic row"
stories := workitems select: [:w | (w payload at:'parent') = epicId]  "child stories"
```
This is exactly the `parent = mission's epic_id` filter the brief prescribes.
`snap at: 'workitems'` is the whole cached workitem array (`DashboardSSE.mag:181`,
one `bbs scanAll: 'workitem'` already run for the Kanban board) — reading it again
adds ZERO new scans.

---

## 3. Constraints that are subtler than they look

### 3.1 Determinism / change-detection (HIGHEST RISK)
`sendElements:to:key:` (`DashboardSSE.mag:236–253`, key `'missions'`) compares the
whole `missionsHtml` string **byte-for-byte** against what was last sent to each
subscriber; a wholesale `outerHTML` replace on any diff **resets scroll and
collapses every open `<details>`** (a mission plan the operator is reading).

`snap at: 'workitems'` comes from `bbs scanAll: 'workitem'`, whose bucket order is
NOT guaranteed stable tick-to-tick. **Story rows MUST be sorted into a deterministic
total order** (brief says wave-then-identity) before rendering, or the panel churns
every tick even when nothing changed. Mirror `sortMissions:` (618–631): compare
integers for wave, fall back to identity string compare, return `-1/0/1`.

### 3.2 `renderMissionCard:` can't see the tree data OR distinguish archived
Two coupled problems (recorded as observation `585cc318142c`):
- The method takes only `m` (633) — it has no `workitems`. Must thread them in
  (e.g. `renderMissionCard: m awaiting: a workitems: wis showTree: bool`) **or**
  render a separate `renderMissionTree: m workitems: wis` and append it in the
  caller loops.
- **Archived must NOT get the tree**, but BOTH the `others` loop (595–603) and the
  `archived` loop (606–613) call `renderMissionCard: m awaiting: false`. The
  `awaiting` flag alone cannot gate the tree. Gate it explicitly: render the tree
  in the awaiting loop (586–594) and others loop (595–603) only, never the archived
  loop. Recommended: keep the card render pure, append
  `self renderMissionTree: m workitems: wis` right after the card in just those two
  loops (each already wrapped in an `on: Exception` guard — extend or add one).

### 3.3 Non-string fields are already safe via `htmlEscape:`
`htmlEscape:` (1080) does `aString asString` before escaping and `nil -> ''`
(observation `21e320bfd00c`). So `wave` (an integer, `Dispatcher.mag:769`) and any
missing field render safely with NO `printString` and NO nil guard — just pass every
field through `htmlEscape:`. A story titled `<script>` renders `&lt;script&gt;`.

### 3.4 Maggie `and:` gotcha (per repo memory + existing code comments)
Never chain `a and: [b] and: [c]` — it parses as the `and:and:` selector →
doesNotUnderstand. The existing empty-state guard (579–585) nests correctly; any
new compound predicate (e.g. "epic_id is a non-empty String") must nest the same way
(see the pattern already at 661–662).

### 3.5 Per-card / per-mission exception guards must be preserved
Each mission is partitioned inside `on: Exception` (557–569) and each card inside
`on: Exception` (588–592, 597–601, 608–612). A malformed epic/story tuple in the
tree must be skipped without breaking the panel — wrap the tree build/each-row in a
guard too. MP2 in the test suite (177–203) already asserts the panel survives a
malformed tuple; the tree must not regress this.

---

## 4. Test guidance (`test/api/test_mission_panel.mag`)

- Structure: each test is a `Test* subclass: Object` with `classMethod: run: t`,
  driven by a runner. Helpers on `MsnPanelHelper` (72–135):
  `makeTempDir`, `seedMission:...`, `contains:sub:`, `count:in:`, `lf`.
- **Add a `seedWorkitem:` / `seedStory:` helper** — none exists yet. Seed via
  `bbs out: 'workitem' scope: 'default' identity: id payload: p` with
  `p at:'type'`, `'parent'`, `'title'`, `'status'`, `'wave'` (mirror
  `seedMissionWithEpic:` at 104–114 which already sets `epic_id`).
- New test (call it MP9) should: seed a mission with `epic_id`, seed the epic
  workitem + ≥2 child stories (distinct waves), run `sse computeSnapshot`, read
  `snap at:'missionsHtml'`, then assert:
  1. the tree HTML appears under the mission card (a new marker class, e.g.
     `mission-tree` / `mission-story`),
  2. each story row contains title, status, AND wave,
  3. rows appear in wave-then-identity order (assert substring index ordering),
  4. a story titled `<script>alert(1)</script>` renders `&lt;script&gt;` and no raw
     `<script>` survives (mirror MP3's assertions at 221–224),
  5. an ARCHIVED mission with an epic_id does NOT emit the tree markup.
- **Register the new class in the runner** (find where existing `Test*` classes are
  invoked at the bottom of the file — new tests are silently skipped otherwise;
  cf. repo memory `feedback_pp_add_command`: Maggie silently skips unregistered/parse-error code).

---

## 5. Success criteria that are harder than they look

- **"byte-identical tick-to-tick"** (§3.1) — the real engineering content of this
  mission. Getting the sort wrong passes the render test but ships a panel that
  flickers and fights the operator's scroll. Add an explicit ordering assertion.
- **"archived do NOT gain the tree"** (§3.2) — not free; the shared
  `renderMissionCard: … awaiting:false` path makes it a deliberate gate, not a
  fallthrough.
- **"every user-controlled field escaped"** — easy IF every field goes through
  `htmlEscape:`; the trap is interpolating `wave`/`status` raw because they "look
  safe." They are attacker-influenceable via story payloads.

## 6. Open questions / unknowns
- **Epic row rendering**: the brief says "listing the epic and its child stories."
  The epic's own title comes from the epic workitem (`identity = epic_id`), which may
  or may not be in the filtered set — render it as the tree root above the stories.
- **Missing-epic missions**: awaiting-approval missions (no `epic_id` yet) and
  pre-materialization missions render as today (no tree) — brief confirms this is
  intended (Out-of-Scope: no backfill). Guard: only build the tree when `epic_id` is
  a non-empty String.
- **CHANGELOG**: brief requires a `[Unreleased]` entry in the SAME commit
  (per `CLAUDE.md` Git Workflow Standards) — note this is a repo-root file edit; it
  is explicitly sanctioned by the mission ("update CHANGELOG.md") despite the
  single-file scope, but confirm the reconciler/lint doesn't flag it as an
  out-of-scope touch.

---

## 7. Recommended implementation shape (NOT applied — scout is read-only)

1. In `renderMissionsHtml:`, hoist `workitems := snap at:'workitems' ifAbsent:[#()]`.
2. Add `renderMissionTree: m workitems: wis` → returns `''` when no valid epic_id;
   else filters stories by `parent = epic_id`, sorts via a new `sortStories:`
   (wave then identity), emits `<ul class="mission-tree">` with epic root + one
   `<li class="mission-story">` per story showing escaped title/status/wave, each row
   guarded.
3. Append the tree after the card in the awaiting loop (586–594) and others loop
   (595–603); do NOT touch the archived loop (606–613).
4. Add `sortStories:` mirroring `sortMissions:` (618–631).
5. Add MP9 + a `seedStory:` helper to the test, and register MP9 in the runner.
6. Add a `CHANGELOG.md [Unreleased]` entry; conventional-commit message
   (`feat(dashboard): render mission epic/story tree in Missions panel`).
