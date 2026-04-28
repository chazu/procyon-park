# Epic: Inline Diff Preview on Board Cards

> **Status: PROPOSED** (design)
> **Author:** scout agent (feature-design-1776655218-8260)
> **Date:** 2026-04-19

## Summary

When a story- or task-level work item completes, operators currently see a
terse card land in the Board's `done` column with nothing but title, type
badge, identity, and repo. To understand what the agent actually produced,
they have to `ssh` to the host, `cd` to `~/.pp/worktrees/<instance>/impl`,
and `git diff main...HEAD` by hand — or wait for `MergeWorktreeAction` to
fast-forward the branch and then chase the merge commit by SHA.

This epic closes that loop. Completed Board cards expand in place to show
the git changes the story produced:

1. A `diff --stat` summary rendered as a riso-print bar chart, one row per
   file, with offset drop-shadow bars in moss (`+`) and rose (`-`).
2. The full patch under a disclosure, syntax-layered in the dashboard's
   risograph palette — context lines in fog, additions in moss on warm
   near-black, removals in rose, hunk headers in cobalt, file headers
   accent-ruled to match the card type (epic=terra, story=cobalt,
   task=moss).
3. Optional merge / reject buttons that drive the already-existing
   `MergeWorktreeAction` / `DiscardWorktreeAction` through a new review
   gate — letting a human approve the diff before the workflow
   auto-merges to the feature branch.

The aesthetic is non-negotiable: chunky offset block shadows, monospace
type, brutal-but-warm vibe, tactile collapse/expand that matches the
existing `.wi-card:hover` physics (translate -1,-1 + `2px 2px 0` drop
shadow). No gradients, no spinners, no github-diff clones.

---

## Why now

- Reviewers already get an auto-generated `git diff --stat` dumped into
  their prompt context via `ClaudeHarness>>gitDiffSummaryFor:`
  (src/harness/ClaudeHarness.mag:203) — the infrastructure to produce
  diffs already exists; we just are not surfacing it in the UI.
- The Board tab (static/dashboard.html:952) is the primary lens humans
  have on in-flight work. Terminal cards ship no payload beyond title
  and repo, so operators can't distinguish a successful commit from an
  empty no-op without leaving the dashboard.
- The scope-violation panel already demonstrates that diff-adjacent data
  can flow through SSE cheaply; we have the plumbing.

---

## User stories

### US-1 — Glance at what shipped
**As** an operator watching the Board,
**I want** a completed story card to show the file-level diff --stat at a
glance,
**so that** I can tell whether the agent actually changed code — and
which part of the tree — without leaving the dashboard.

**Acceptance criteria:**
- Every `wi-card` in the `kanban-done` column whose underlying workitem
  is a `story` or `task` shows a compact stat strip under the meta row:
  `N files · +X / -Y` with a miniature horizontal bar chart (moss for
  additions, rose for deletions) sized by the ratio of the largest file.
- If the workitem produced no diff (empty branch / design-only / spike
  discarded), the strip renders as `— no diff —` in fog, italicized, not
  as a blank row.
- Epics do not show a stat strip (they roll up children); instead they
  show `N/M stories merged` where available. *(Nice-to-have; not required
  for MVP.)*
- The stat strip is visible immediately when the card arrives in the
  done column — no click required. It is populated from SSE payload, not
  lazy-fetched.

### US-2 — Expand a card to read the patch
**As** an operator,
**I want** to click a done card and have it expand in place to show the
patch,
**so that** I can actually review the change without opening a terminal.

**Acceptance criteria:**
- Clicking anywhere on the card body (but not on action buttons) toggles
  an expanded region below the meta row.
- Expansion animation uses the same physics as `.wi-card:hover` —
  `transition: transform .1s ease, box-shadow .1s ease;` — and adds a
  persistent `translate(-1px,-1px)` + `3px 3px 0 rgba(0,0,0,.6)` shadow
  while expanded, so the card visibly lifts.
- The expanded card never overflows its kanban column horizontally; the
  patch body uses `overflow-x: auto` with a 3px inset rose scrollbar.
- Re-clicking collapses; click target is the title row, and `aria-expanded`
  toggles correctly for assistive tech.
- Keyboard: `Enter` / `Space` on a focused done card toggles expansion.
- The expansion state is preserved across SSE re-renders of the same
  card (keyed by workitem identity) — patching the elements must not
  collapse an already-open card.

### US-3 — Read a stylish, readable patch
**As** a human reviewing code in the dashboard,
**I want** the patch to look like a risograph-printed code review — not
a github clone,
**so that** the dashboard's aesthetic stays cohesive and the diff is
actually readable against the warm near-black substrate.

**Acceptance criteria:**
- Per-file header: `wi-diff-file` row shows `path/to/file.mag` left-aligned
  in `--f-mono`, `+12 / −3` right-aligned; a **2px solid** bottom rule in
  the card's type-accent color (story=cobalt, task=moss).
- Additions: line prefixed with a solid `+`, left-border 3px moss,
  background `rgba(106,140,60,.08)` (tint of `--moss`).
- Removals: line prefixed with a solid `−`, left-border 3px rose,
  background `rgba(183,60,70,.08)`.
- Context lines: prefixed with single-space, color `--cream-dim`, no
  background.
- Hunk header (`@@ -a,b +c,d @@`): cobalt foreground on `--bg-2`, with
  a 1px dashed rose rule above it.
- No syntax highlighting of language tokens (keeps it brutal and cheap).
- Font: `--f-mono`, size 11px, line-height 1.45, `tab-size: 4`.
- Consistent with existing `box-shadow: 5px 5px 0 <accent>` offset idiom
  used by `.board-plate` — the whole expanded region casts a single
  unified shadow, not per-element shadows.

### US-4 — Handle big diffs without breaking the dashboard
**As** an operator,
**I want** large diffs to degrade gracefully,
**so that** a story that touched 400 files doesn't wedge my browser.

**Acceptance criteria:**
- SSE payload carries at most **400 lines** of patch text per workitem;
  server-side truncation is marker-explicit: patch ends with a sentinel
  `@@ truncated @@ <N> more lines @@`.
- When truncated, the expanded view shows a `[ view full patch ]`
  affordance that fetches the untruncated patch from a new endpoint
  `GET /api/workitem/diff?scope=<repo>&identity=<id>&full=1` and swaps
  it in place. Response is `text/plain`.
- Files-touched list is never truncated (just line counts), so the stat
  strip stays accurate even when the patch body is clipped.
- SSE payload size per workitem is bounded at ~32 KB; anything larger
  drops only the `patch` field, keeps `stat`, and the card displays
  `[ patch too large — view full ]` in fog.

### US-5 — Approve or reject a diff from the card
**As** a lead reviewing an agent's output,
**I want** merge / reject buttons on the expanded done card,
**so that** I can gate the auto-merge without dropping into a terminal.

**Acceptance criteria:**
- When a workitem's parent workflow is at a new `review` place (see
  Technical Context below), the expanded done card shows two buttons
  styled after `.notif-filters button` (chunky, offset-shadow hover):
  - `merge`  (background `--moss`, on hover: `--terra`)
  - `reject` (background `--rose`, on hover: `--terra`)
- `merge` POSTs signed to `/api/workitem/merge` with `{scope, identity}`
  — the server fires the stalled workflow transition that runs
  `MergeWorktreeAction`.
- `reject` POSTs signed to `/api/workitem/reject` — the server fires a
  transition that runs `DiscardWorktreeAction` and marks the workitem
  `status: rejected`.
- Only signed (user: / admin:) identities see the buttons; anonymous
  SSE sessions render the expanded diff read-only.
- The buttons disable themselves on click until the next SSE tick, so
  double-clicks cannot fire the transition twice.
- If the workflow is not paused at `review` (i.e. legacy auto-merge), the
  buttons are **not** rendered. The review gate is opt-in per workflow
  template for v1.

---

## Feature breakdown (stories for the planner)

### Story 1 — Extend SSE payload to carry diff data
**Scope:** src/api/DashboardSSE.mag, src/bbs, src/dispatcher

- Capture diff artifacts at workflow completion. When a workitem's task
  transitions to `status: completed`, the workflow engine (or a new
  `CaptureDiffAction`) runs:
  ```
  git -C <worktreeDir> diff --stat <featureBranch>...HEAD
  git -C <worktreeDir> diff          <featureBranch>...HEAD
  ```
  …parses the stat into `{ files: [{path, add, del}], totalAdd, totalDel }`,
  truncates the patch to 400 lines, and writes the result into the
  workitem's payload as `payload.diff = { stat, patch, truncated,
  truncatedLineCount, base, head, branch, workdir, capturedAt }`.
- The capture must run **before** `MergeWorktreeAction removeWorktree:` —
  otherwise the branch is gone. Options:
  - (a) new action `capture-diff` inserted between completion and merge
    in each workflow template, OR
  - (b) `MergeWorktreeAction` calls `self captureDiffInto: workitem`
    as its first step.
  Recommendation: **(a)** — keeps actions single-purpose and lets
  spike / design workflows capture diffs too before
  `DiscardWorktreeAction` runs.
- `DashboardSSE>>sendWorkitems:` grows a new helper
  `renderDiffStatFor: payload` that emits the stat strip inline in the
  `.wi-card`. Render only when `status = 'done'` and `payload.diff` is
  non-nil.
- Patch body is rendered inside a sibling `.wi-diff-body` div with
  `hidden` attribute by default; toggling is pure CSS/JS on the client.
  Shipping it always-in-DOM simplifies reconnect state because the
  client doesn't need a second fetch to repopulate a previously-open
  card.
- HTML-escape every line of the patch; never trust file paths or hunk
  text.

**Acceptance:** SSE tick carrying a done workitem now includes
`<div class="wi-diff-stat">` and `<div class="wi-diff-body" hidden>` as
children of `.wi-card`. Existing tests in `test/api/` still pass. New
test: fake a workitem tuple with a `payload.diff` block and assert the
rendered HTML contains the expected stat row and escape-safe patch.

---

### Story 2 — Frontend card expansion UX + risograph CSS
**Scope:** static/dashboard.html only

- Add CSS tokens to `.wi-card` / `.wi-diff-*` block (follow existing
  pattern in lines 505-532). Bind to `--terra` / `--moss` / `--cobalt`
  / `--rose` (already defined).
- New classes:
  - `.wi-card.wi-expanded` — persistent shadow + translate.
  - `.wi-diff-stat`        — flex row, stat bar chart.
  - `.wi-diff-bar`         — 100% wide inline-block, split into moss
    left / rose right segments.
  - `.wi-diff-file`        — per-file row header, type-accent bottom rule.
  - `.wi-diff-line.add`    — moss-tinted row.
  - `.wi-diff-line.del`    — rose-tinted row.
  - `.wi-diff-line.ctx`    — fog row.
  - `.wi-diff-hunk`        — cobalt hunk header.
- JS: extend the SSE boot block (`static/dashboard.html` around line 1134
  — `SSE_DASHBOARD_CATEGORIES`) with a delegated click handler on
  `#dashboard-workitems` that toggles `.wi-expanded` on the nearest
  `.wi-card`. Use `data-diff-expanded` attribute to survive SSE patches
  (the SSE HTML ships `hidden` by default; on patch, re-apply the
  expanded state from a small in-memory `Set<workitemId>` kept in the
  dashboard script).
- Keyboard handler: listen for `keydown` on `.wi-card[tabindex="0"]`
  with `key === 'Enter' || key === ' '`.
- Acceptance: toggling a card expands/collapses with the matching
  physics; style matches the spec in US-3; SSE re-render does not
  collapse open cards.

---

### Story 3 — Truncation & "view full patch" affordance
**Scope:** static/dashboard.html, src/api/Server.mag

- Server-side truncation already lives in Story 1; this story wires the
  fetch-full path:
  - New GET endpoint `/api/workitem/diff?scope=<repo>&identity=<id>`
    registered alongside the existing workitem routes
    (src/api/Server.mag:331-340). Returns `text/plain` of the full
    untruncated patch; 404 if not captured, 410 if the worktree was
    cleaned before capture.
  - Endpoint is read-only and does **not** require signing — same
    visibility rules as the SSE dashboard (whatever can see the Board
    can see the diff).
- Frontend: when `.wi-diff-body` ships with `data-truncated="true"`, the
  disclosure button reads `[ view full patch (N more lines) ]`. On
  click, `fetch()` the endpoint, `innerText` the result into a
  `<pre class="wi-diff-fullraw">`, replace the truncated body with it.
  No re-rendering as coloured diff — full raw view is plain mono
  on `--bg-2` with a single rose left rule, by design (a different
  visual "mode" that signals "you are now looking at the unfiltered
  source").

**Acceptance:** A workitem with a 2000-line patch renders as truncated
by default, expands to show 400 lines + truncation sentinel, and the
"view full" button swaps in the complete patch from the endpoint.

---

### Story 4 — Merge / reject review gate (stretch)
**Scope:** src/dispatcher/WorkflowEngine.mag,
src/api/Server.mag, static/dashboard.html, workflows/*.json

- Add an opt-in `review` place to applicable workflow templates. When
  the workflow engine reaches this place, it waits (no auto-fire) until
  a token is explicitly produced by either `/api/workitem/merge` or
  `/api/workitem/reject`.
- Two new signed routes:
  - `POST /api/workitem/merge`  { scope, identity } — produces a token
    at `review` → `merged` transition; fires MergeWorktreeAction.
  - `POST /api/workitem/reject` { scope, identity, reason? } —
    produces a token at `review` → `rejected` transition; fires
    DiscardWorktreeAction and sets `payload.status = 'rejected'`.
- Frontend: when workitem payload includes `review_pending: true`, the
  expanded card shows the two buttons. Buttons post via `fetch` with
  the dashboard's existing signed-request wrapper (same mechanism that
  `/api/workitem/update` uses; see `signedRouteOnly:` pattern at
  Server.mag:331).
- This story is **stretch**: MVP (Stories 1-3) is shippable without the
  review gate — auto-merge continues to work and the cards just show
  diffs after-the-fact. Revisit once US-1..US-4 ship and operators
  start asking for gating.

**Acceptance:** A workflow template with `review: true` pauses at the
review place; the Board's done card exposes merge/reject buttons; either
action drives the correct downstream action and updates the card on the
next tick.

---

## Technical context

**Files of interest:**
- `src/api/DashboardSSE.mag:335` — `sendWorkitems:` renders all
  `.wi-card` HTML. Grow it to emit diff-stat + diff-body divs.
- `src/api/DashboardSSE.mag:571` — `htmlEscape:` — reuse for patch
  bodies.
- `src/api/Server.mag:331-340` — signed workitem routes; model the new
  diff / merge / reject endpoints after these.
- `src/harness/ClaudeHarness.mag:203` — `gitDiffSummaryFor:` — shows
  the exact shell incantation the reviewer prompts already use; reuse
  or factor out into a shared `DiffCapture` utility.
- `src/dispatcher/GitOps.mag` — currently only covers checkout / merge /
  worktree lifecycle; consider adding `captureDiff:base:head:in:` here.
- `src/dispatcher/actions/MergeWorktreeAction.mag:82` — `removeWorktree:`
  deletes the worktree. Diff capture **must** happen before this.
- `src/dispatcher/actions/DiscardWorktreeAction.mag` — analogous issue
  for spike workflows.
- `src/dispatcher/WorkflowEngine.mag:24` — action dispatch registry;
  register `capture-diff` here.
- `static/dashboard.html:505-533` — `.wi-card` CSS block; extend here.
- `static/dashboard.html:1134` — `SSE_DASHBOARD_CATEGORIES` — no change
  needed to categories list itself, but the `workitem` render path
  gains new click handlers.

**Data model — workitem payload shape after Story 1:**
```json
{
  "title": "…",
  "type": "story",
  "status": "done",
  "repo": "alto",
  "parent": "epic:…",
  "diff": {
    "base": "feat/foo",
    "head": "wi-1776655218-8260/impl",
    "branch": "wi-1776655218-8260/impl",
    "workdir": "~/.pp/worktrees/wi-…/impl",
    "capturedAt": 1776655999,
    "stat": {
      "files": [
        { "path": "src/x.mag", "add": 42, "del": 3 },
        { "path": "test/xTest.mag", "add": 18, "del": 0 }
      ],
      "totalAdd": 60,
      "totalDel": 3,
      "fileCount": 2
    },
    "patch": "diff --git a/... …",
    "truncated": false,
    "truncatedLineCount": 0
  }
}
```

**Payload size budget:** `bbs upsertPinned: 'workitem' …` is the only
write path for workitems (Server.mag:2002, :2102, :2117, :2155,
:2190). No hard size limit exists today; the SSE queue caps at 256
events so a very large payload still fits, but the render path
serializes to HTML once per tick per subscriber — keep patch ≤ 32 KB.

**SSE rendering cost:** `sendWorkitems:` renders the full kanban every
tick. Today each card is ~200 B of HTML; adding 400 lines of patch at
~80 B/line = ~32 KB per done card × N done cards × N subscribers. This
is the reason the patch body ships `hidden` — the browser still pays
DOM cost, but the wire cost is already bounded by the payload budget
above. If this becomes a bottleneck, revisit with lazy-fetch (server
only emits the stat strip; patch body fetched on expand).

**Aesthetic anchors (from static/dashboard.html):**
- Palette: `--terra` (epic orange), `--moss` (task green), `--cobalt`
  (story blue), `--rose` (alarm red), `--fog` (muted), `--bg`,
  `--bg-2`, `--surface`, `--cream`, `--cream-dim`, `--cream-hi`.
- Physics: `transform: translate(-1px,-1px); box-shadow: 2px 2px 0 rgba(0,0,0,.5);`
  on hover (line 514); larger `5px 5px 0 #1e4a82` on plates (line 448).
- Typography: `--f-mono` for body/diff; `--f-slab` for headings and
  chunky buttons.

**Existing reviewer diff plumbing** (for Story 1 to reuse):
```smalltalk
committed   := Shell capture: '(git -C "', aWorkDir, '" diff --stat main...HEAD 2>/dev/null)'.
uncommitted := Shell capture: '(git -C "', aWorkDir, '" diff --stat HEAD 2>/dev/null)'.
```
Same Shell capture idiom should be used for the patch body capture, but
(a) compare against `featureBranch`, not literal `main`; (b) use the
workitem's recorded `signal:worktree` tuple (see MergeWorktreeAction:13)
to find `workdir` and `feature_branch`; (c) strip carriage returns and
cap lines server-side before storing in the payload.

---

## Open questions

1. **Workitem → workflow instance mapping.** Today a workitem's identity
   is scoped to the repo, but the worktree/branch info lives on a
   `signal:worktree` tuple scoped by workflow `instanceId`. How does
   `capture-diff` know which workflow instance produced a given
   workitem? Hypothesis: the workflow instance writes the workitem's id
   into its signal tuple OR the workitem carries `payload.workflow_id`.
   Need to verify by scanning how `/api/workitem/run` (Server.mag:2231)
   starts the workflow and whether it back-references the workitem.

2. **Multi-file spike workflows.** Spike workflows call
   `DiscardWorktreeAction` and intentionally throw code away. Should
   the diff still be captured (for post-mortem value) or explicitly
   skipped? Recommendation: capture anyway, label as
   `diff.kind = 'discarded-spike'` so the UI can style it with a
   diagonal hatch overlay like `.kanban-in-progress` already does.

3. **Identity on the read path.** Should anonymous SSE sessions see
   patch contents? The Board itself has no per-identity filtering
   today (sendWorkitems: does not consult `entry.identity`), so the
   default answer is "yes, same visibility as the Board." If any
   repo is sensitive, introduce the filter symmetrically with
   `sendNotifs:` which already filters by identity.

4. **Truncation strategy.** 400 lines is a guess. Should we truncate
   by bytes (32 KB) instead of lines to stay within SSE payload budget
   regardless of line length? Recommendation: truncate by **both** —
   whichever hits first — and record both counts in
   `truncatedLineCount` / `truncatedByteCount`.

5. **Merge gate default.** Should the review gate (Story 4) be opt-in
   (set `review: true` in the workflow template) or opt-out? Proposal:
   opt-in for v1 to avoid changing existing workflow semantics;
   revisit after usage data.

6. **Epic roll-up.** The US-1 acceptance criterion mentions epics
   showing `N/M stories merged` instead of a stat strip. Is that worth
   shipping in v1, or defer until stories prove the UX? Recommendation:
   defer — MVP only renders diff on story/task cards.

7. **Syntax highlighting.** Explicitly out of scope for the risograph
   aesthetic, but operators may ask. If we relent, a minimal
   language-class hint in the file header (`.mag`, `.go`, `.html`)
   lets us apply a subtle per-extension accent without breaking the
   brutal-but-warm vibe.

---

## Out of scope

- Inline commenting on diff lines.
- Side-by-side (split) diff view.
- Per-line or per-hunk staging / partial merges.
- Conflict resolution UI (merge conflicts still fail the workflow and
  require terminal intervention — see MergeWorktreeAction:77).
- Diff between two arbitrary refs. The card only ever shows
  `feature_branch…impl_branch` for its own workitem.
