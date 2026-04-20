# Epic: Workflow DAG Visualization

> Status: DESIGN — scout output, not yet scheduled.

## Summary

The dashboard's Overview tab currently lists active workflows as a flat
list of `.wf-card` elements with a comma-separated string of current
places (`place: reviewing, testing`). This hides the **shape** of a
workflow — operators can't see where tokens are in the net, what is
upstream, what's downstream, which transitions are gated by a
precondition signal, or why a workflow is stuck.

This epic replaces the place-string with a live **DAG visualization**
of the workflow's Petri net: places rendered as chunky risograph-ink
rounded rectangles, transitions as labeled edges (dashed for
precondition-gated, solid for action-only), and current token positions
highlighted with a bright accent fill and a glowing token glyph.
Completed places are muted, pending places outlined only. The
visualization is driven by the same SSE stream that already powers the
Overview tab and must read like a risograph print of a state diagram,
not a d3 force-directed blob.

### Why this is the right shape

The Petri-net model is already first-class in the codebase
(`WorkflowEngine.mag` — places, transitions, positive/waiting/negative
tokens, fork/join transitions with multiple `in`/`out` places). The
template format (`workflows/*.cue`) is purely declarative and already
exposes everything a layout algorithm needs. All we are missing is
**(a)** a server-side projection that emits template-plus-token-state
as structured JSON, and **(b)** a client-side Sugiyama-style renderer
that honors the existing visual vocabulary.

---

## User Stories

### Primary

1. **As an operator**, I want each active workflow on the Overview tab
   to render as a small DAG thumbnail showing the shape of the net and
   the current token positions, **so that** I can see at a glance what
   phase a run is in and where it is going next.

2. **As an operator**, I want to click a workflow thumbnail to expand
   it into a full-size DAG detail panel, **so that** I can read
   transition labels, inspect precondition-gated edges, and understand
   why a workflow is stalled.

3. **As an operator running a fork-heavy template** (e.g.
   `full-pipeline` with `fork` → `[reviewing, testing]` and join at
   `evaluate`), I want to see both concurrent tokens simultaneously,
   **so that** I can tell which branch completed first and which one
   is holding up the join.

4. **As an operator debugging a stuck workflow**, I want
   precondition-gated transitions (e.g. `pass`, `fix_needed`,
   `exhausted` in full-pipeline) to be visually distinct from plain
   action transitions, **so that** I can immediately see "oh, we're
   waiting on a `verdict:<instance>` signal" vs. "the foreman task
   hasn't finished yet."

5. **As an operator**, I want terminal / completed / cancelled
   workflows in the recent-completions panel to keep their DAG with
   all places muted and the terminal place highlighted, **so that**
   the visual idiom is consistent between running and completed runs.

### Secondary

6. **As an operator on a small laptop screen**, I want the overview
   DAG thumbnails to collapse to a compact "mini-map" mode when
   there are more than N active workflows, **so that** the panel
   remains usable and does not scroll off the viewport.

7. **As a template author**, I want to preview a template's DAG in
   the dashboard before starting a run, **so that** I can catch
   obviously-broken nets (orphan places, dead transitions).
   *(Stretch — not required for MVP.)*

8. **As an operator**, I want the DAG to support very long linear
   chains and wide forks without the thumbnail becoming unreadable,
   **so that** the visualization scales from the 5-place
   `scout-mission` template to the 14-place `full-pipeline`
   template.

---

## Acceptance Criteria

### Server-side (DAG extraction)

- [ ] `DashboardSSE#sendWorkflows:` emits, for each running workflow,
      a structured DAG payload containing:
    - `places: [{id, role: "start"|"terminal"|"intermediate"}]`
    - `transitions: [{id, in: [...], out: [...], gated: bool,
      gate: {category, identity, constraint?} | null, role?, action?}]`
    - `tokens: [{place, status: "positive"|"waiting"|"negative",
      transition_id?}]`
- [ ] The DAG payload is derived from the pinned `template` tuple
      (looked up by `template` field of the workflow tuple, scope
      preference: repo → system) and the live `token` tuples for
      that instance.
- [ ] A transition is marked `gated: true` iff it has a non-empty
      `preconditions` array in the template.
- [ ] Multi-place `in` / `out` arrays (fork and join transitions) are
      preserved verbatim — the client is responsible for rendering
      them as one transition node connected to multiple places.
- [ ] A new GET endpoint `/api/workflow/dag?workflow_id=...` returns
      the same payload as JSON for the detail panel and for ad-hoc
      debugging (no signature required — read-only, like
      `/api/dashboard`).
- [ ] A new GET endpoint `/api/template/dag?name=...&scope=...` returns
      a template-only DAG (no tokens) for the stretch preview story.
- [ ] Unit tests exist for the extraction logic covering:
    - linear template (`scout-mission` — 6 places, 5 transitions,
      1 gated by `task-complete` event)
    - fork/join template (`full-pipeline` — 14 places, 13 transitions,
      3 gated by `verdict` signal, 1 fork with 2 outs, 1 join with
      2 ins)
    - terminal state (every token in a `terminal_places` set)

### Client-side (layout + rendering)

- [ ] Layout uses a layered/hierarchical algorithm
      (Sugiyama-style: assign layers by longest-path from start,
      order within layer to minimize edge crossings, straight
      orthogonal edges with elbow joints). Implementation is
      vanilla JS — **no d3-force**, **no cytoscape**, **no dagre
      runtime dependency that exceeds ~15KB gzipped**. A small
      in-repo Sugiyama implementation (≤ ~400 lines) is acceptable;
      `dagre` via CDN is also acceptable if the output is then
      re-rendered with the project's own SVG primitives.
- [ ] Rendering is **SVG**, not canvas — strokes, fills and offset
      shadows must be inspectable and print-faithful.
- [ ] Default flow direction is **left-to-right**. Fallback to
      top-to-bottom only when layer count × layer width exceeds the
      container aspect.
- [ ] Place nodes:
    - Chunky rounded rectangle, 2px stroke in `--rule-hi`.
    - Offset block shadow via a second `<rect>` offset by (3px, 3px)
      in `--moss-deep` / `--terra-deep` / cobalt / rose (chosen by
      hashing the workflow template name so every template has a
      stable ink color).
    - Monospace label (`var(--f-mono)`, uppercase, 10.5px).
    - **Current (positive token)**: fill = template accent (terra /
      moss / cobalt), cream-hi text, a small 5px circle "token"
      glyph in the top-right corner with a soft glow
      (`filter: drop-shadow`).
    - **Waiting token**: fill = accent at 40% opacity, dashed stroke.
    - **Completed (passed through)**: muted — `--fog` stroke,
      `--surface` fill, `--cream-dim` text, no shadow.
    - **Pending (not yet reached)**: outline only — transparent
      fill, `--rule` stroke, `--cream-dim` text.
    - **Terminal**: double-rule border; if the workflow completed
      here, accent fill.
- [ ] Transition edges:
    - Solid line for ungated transitions.
    - Dashed line (4,3) for precondition-gated transitions, with a
      small lock/diamond glyph at midpoint and the gate's `identity`
      as a tooltip.
    - Arrow head in accent color, offset shadow 1px.
    - Label (`transition.id`) rendered as a small text chip on a
      cream-tinted background so it never overlaps an edge.
- [ ] Fork transitions (one `in`, many `out`) and join transitions
      (many `in`, one `out`) render as a single transition node
      (chunky square, 45°-rotated or a thin vertical bar) fanning
      to each connected place — not as N separate edges each
      labeled with the same transition id.
- [ ] Thumbnail mode (overview): max 240×120 px, no transition
      labels, only the transition-id of the *currently firing*
      transition shown inline. Clicking the thumbnail opens the
      detail panel (in-place expand, not a modal — the panel grows
      to fill the Workflows column).
- [ ] Detail mode: full labels, full tooltips on gates and actions,
      a legend strip at the bottom showing the 4 place states and
      the 2 transition kinds.
- [ ] All SVG nodes have `data-place` / `data-transition` attributes
      so existing scope-filter CSS (`.pp-scope-mine`) can cascade.

### Integration (Overview tab)

- [ ] `#dashboard-workflows` panel keeps its current header and
      layout but each `.wf-card` is replaced by a `.wf-card` whose
      body is `[header-block] [dag-svg]`, where header-block carries
      the template name, launcher, active task row, and token-line
      exactly as today.
- [ ] The SSE `sendWorkflows:` broadcast continues to send
      `<div id="dashboard-workflows">` as Datastar patch-elements —
      the SVG is part of the patched HTML, so no new event kind is
      needed for the happy path.
- [ ] Clicking a card toggles an `is-expanded` class; expanded
      cards render the full DAG + legend; others render the
      thumbnail. State is persisted in `localStorage` keyed by
      workflow id so expansion survives page reloads.
- [ ] Keyboard: `Enter` on a focused card toggles expand;
      `Escape` collapses.

### Scaling / readability

- [ ] Templates with ≤ 8 places render thumbnails at 240×120 px.
      Templates with 9–16 places compress horizontal layer width
      but keep node height; > 16 places auto-rotate to top-to-bottom
      and allow vertical scroll inside the SVG container.
- [ ] If the Overview has > 6 active workflows, thumbnails switch to
      a denser 2-column grid inside the Workflows panel.
- [ ] A "simplify" toggle in the panel header collapses linear runs
      (places whose in-degree and out-degree are both 1) into a
      single edge-chip labeled "N steps", so wide templates become
      legible at thumbnail size. Current-token places are never
      collapsed.
- [ ] The detail view is navigable by keyboard: arrow keys move
      focus between places, Space zooms to fit.
- [ ] SVG text is always ≥ 10px effective size — when layers are
      too narrow, labels truncate with ellipsis and the full label
      is available in a tooltip.

---

## Technical Context

### Relevant files

| File | Role |
|------|------|
| `workflows/*.cue` | Template definitions. `transitions[].in/out/id/preconditions/role/action` are the source of truth for the graph structure. `start_places` / `terminal_places` mark entry/exit. |
| `workflows/_affinity.cue` | Schema only; ignored by `TemplateLoader` (leading underscore). |
| `src/dispatcher/TemplateLoader.mag` | Loads CUE → BBS `template` tuples, pinned. Scope is `system` for built-ins and `<repo>` for repo-specific. Uses `bbs outPinned: 'template'` so payload is directly usable. |
| `src/dispatcher/WorkflowEngine.mag` | Writes token tuples (`category: 'token'`, payload keys `workflow_instance`, `place`, `status`, `transition_id`, `modality`). Fires transitions, consumes inputs, produces outputs. Petri-net semantics already in place. |
| `src/api/DashboardSSE.mag` | Builds Overview HTML and pushes it over SSE (`sendWorkflows:`, `placesFor:in:`, `tokenTotalsFor:in:`). Extension point: replace `placesFor:` output with a DAG SVG block, add a new helper `dagFor:in:templates:`. |
| `src/api/Server.mag` | HTTP routes. Add `self get: '/api/workflow/dag' do: ...` and `self get: '/api/template/dag' do: ...` near the existing `handleWorkflowStatus:` cluster (lines ~1757). |
| `static/dashboard.html` | Inline styles + Datastar. Add SVG-specific CSS rules under the existing `.wf-card` block (~line 345). Add client-side layout script as a new inline `<script>` block near the existing datastar bootstrap. |

### Patterns to reuse

- **Offset block shadow idiom:** the dashboard's `.panel--terra`,
  `.panel--moss`, `.panel--cobalt` classes all use
  `box-shadow: 4px 4px 0 <color-deep>`. The DAG's place shadows use
  the same trick but on SVG, via a back-ghost rect offset by
  (3px, 3px).
- **Ink palette:** `--terra`, `--moss`, `--cobalt`, `--sun`, `--rose`
  — pick by hashing `template` so `scout-mission` is always terra,
  `full-pipeline` always cobalt, etc. Keep the hash function in JS
  and document the mapping in `dashboard.html` CSS comments.
- **SSE patch-elements:** the existing pattern already sends full
  HTML subtrees — SVG inline is just more HTML. No new framework.
- **Scope filtering:** `.pp-scope-mine` cascade from `dashboard.html`
  already hides cards by `data-launched-by`. DAG SVG must expose
  the same attribute on the outer `<svg>` root.

### Petri-net details worth knowing

- Multiple positive tokens can coexist (after a fork). The DAG must
  highlight **all** of them.
- `waiting` tokens exist for transitions whose action has dispatched
  a task but the precondition hasn't fired yet — they should render
  as a distinct "in progress" state (dashed outline, accent fill at
  40%), not as "current" or "pending".
- Preconditions are `{category, identity, constraint?}` where
  `category ∈ {signal, event}` and `identity` is a templated string
  like `verdict:{{instance}}`. Render the un-templated form.
- `full-pipeline` has `max_review_cycles: 3` at the top level —
  worth surfacing as a header badge on the DAG ("cycle 1 of 3")
  because otherwise the loop back from `fix_done` → `reviewing`
  looks like an infinite loop to a reader.

### Out of scope (explicitly)

- Editing templates from the dashboard.
- Drag-to-rearrange layout (Sugiyama output is deterministic).
- Historical playback / timeline scrubbing (separate epic #27).
- DAGs for workitems (task hierarchy); this is strictly the
  workflow-template Petri net.

---

## Implementation Stories

### Story 1 — Server: DAG extraction API

- Add `DashboardSSE#dagFor: workflowTuple templates: allTemplates tokens: allTokens`
  returning a Dictionary with keys `places`, `transitions`, `tokens`.
- Resolve the template by looking up `bbs rd: 'template' scope: repoOrSystem identity: templateId`
  with system fallback (mirror existing TemplateLoader scoping rules).
- Add `/api/workflow/dag` GET route in `Server.mag` — JSON response.
- Add `/api/template/dag` GET route — JSON, no token data.
- Unit tests in `test/` covering scout-mission, full-pipeline,
  multi-scout (fan-out), and one workflow after completion.

**Definition of done:** `curl http://localhost:<port>/api/workflow/dag?workflow_id=<id>`
returns a JSON blob with the three arrays; contract documented in
`docs/api.md` (or inline in Server.mag method docstring if no
such file exists — verify at implementation time).

### Story 2 — Client: SVG Sugiyama renderer module

- New inline script section in `static/dashboard.html` exporting a
  global `PP.renderDag(svgEl, dag, opts)` function.
- Pipeline: (1) assign layers by longest path from start_places;
  (2) within layer, order by barycenter heuristic (one pass is
  enough for the template sizes we have); (3) compute node positions
  with configurable layer/node spacing; (4) route edges as orthogonal
  polylines with one elbow.
- Emit SVG with `<defs>` for arrowheads and dashed patterns, place
  rects with offset ghost rects, transition glyphs, label chips.
- Unit-test-ready: renderer is pure (takes dag → returns SVG string),
  so a Node test harness can snapshot-test outputs for the three
  canonical templates.

**Definition of done:** given the DAG JSON from story 1, the
renderer produces an SVG that visually matches the three template
snapshots committed under `test/dashboard/dag-snapshots/`.

### Story 3 — Overview tab integration

- Replace the `place: <string>` line in the existing `.wf-card` with
  an SVG produced by `PP.renderDag` in thumbnail mode.
- Wire click-to-expand with `localStorage` persistence and
  keyboard affordances.
- Add a "simplify" toggle in the `#dashboard-workflows` header.
- Style additions in the `<style>` block under the existing
  `.wf-card` rules.

**Definition of done:** the Overview tab renders one DAG thumbnail
per running workflow at 240×120 px, visually consistent with the
rest of the risograph aesthetic (checked against
`docs/reviews/<date>-dag-visual.md` review by a second agent).

### Story 4 — Many-places readability pass

- Implement the simplify toggle (collapse linear runs where neither
  endpoint holds a token).
- Implement the >6 workflows 2-column grid and the >16 places
  top-to-bottom fallback.
- Add the review-cycle badge for templates that declare
  `max_review_cycles` (currently just `full-pipeline`).
- Load testing: render 20 simultaneous full-pipeline DAGs and verify
  SSE tick latency stays under 200 ms (currently ~50 ms for the flat
  list).

**Definition of done:** manual run of
`for i in 1..20; pp workflow full-pipeline ...; end` keeps the
Overview responsive and readable; screenshot captured in epic review.

---

## Open Questions

1. **Layout library vs. hand-roll.** Sugiyama on ≤20-node graphs is
   ~200 lines of JS, which is probably cheaper than vendoring
   `dagre` (+ ELK would be overkill). Default recommendation:
   hand-roll. Revisit if someone writes a template with > 30 places.

2. **Do we draw the `pass` / `fix_needed` / `exhausted` trio of
   guarded transitions as three separate edges fanning out from
   `evaluating`, or as one node labeled "verdict?" with three
   outgoing branches?** The template models them as three
   transitions; rendering them as three separate gated edges is
   semantically faithful but visually busy. Recommendation:
   three edges, but when all three share the same gate *identity*
   (just different `constraint`), render them as a single "decision
   diamond" glyph. Flag as an open UX call.

3. **Template preview without a running workflow** — worth doing now
   or deferred? Touches the same renderer, but needs a new tab or a
   "Templates" panel. Deferred to a follow-up epic unless trivial.

4. **Color per template vs. color per place state.** If we ink each
   template differently, two workflows of the same template are
   visually indistinguishable except by id. Alternative: ink each
   workflow instance by hashing its `workflow_id` so siblings are
   distinguishable. Needs a UX opinion — probably instance-hash is
   better on Overview, template-hash better on a template
   preview.

5. **Animations on transition fire.** When a token moves from
   place A to place B, should we animate it along the edge? Lovely
   but potentially distracting, and the SSE cadence is ~1-2 Hz so
   animation windows are tiny. Recommendation: start static, add
   a 200ms flash on the new place only, no edge animation.

6. **Waiting vs. negative token visualization.** `WorkflowEngine`
   writes negative/consumed tokens for audit trails. Do we want to
   surface these (ghost trail of past places) or hide them?
   Recommendation: hide by default, expose via a "show history"
   toggle in the detail view — overlaps with the workflow-replay
   epic, so coordinate.

7. **Gated edge glyph.** Dashed stroke alone may not read at
   thumbnail size. Consider a tiny filled diamond at the edge
   midpoint in `--sun` (signal-yellow). Lock-icon feels wrong
   aesthetically; diamond feels on-brand.

8. **Accessibility.** The current dashboard has no ARIA on the
   workflow list. New SVG should at minimum carry a `<title>`
   naming the workflow and a `<desc>` listing current places.
   Screen-reader story: "full-pipeline, currently at reviewing and
   testing." Not a blocker, but cheap and worth including.
