# Scout: Clickable story modal + dismiss on the dashboard board

**Date:** 2026-06-24
**Scout mission:** clickable issue/story modal with a dismiss-to-hide action on the Kanban board
**Status:** research only — no code written

---

## TL;DR

The clickable detail modal **already exists and is ~80% of the requested feature.**
A board card click already opens a server-data-backed modal showing title,
description, status, type, parent, wave, labels, and child relationships. What is
**missing** relative to this mission:

1. **Comments** are not rendered in the modal (the data is already available).
2. **Linked workflow** is not rendered (data is *not* directly available — needs a derivation).
3. **A non-destructive `dismiss` action** (hide-from-my-board, persistent across reloads).
   Today the only modal action is **`Cancel item`**, which is a *global, destructive*
   status change (`status -> cancelled`), not a per-viewer hide.

The bulk of the work is therefore (a) two small client-side render additions, and
(b) a new `dismiss` mechanism. The central design decision is **where dismiss state
lives**, and that decision is dominated by one architectural fact: **the board HTML
is rendered once and broadcast to every SSE subscriber — it is not per-user.**

**Recommendation:** implement dismiss as a **client-side, `localStorage`-backed hide**
that reuses the existing `.wi-hidden` / `refreshBoardFilters` path. It is persistent
across reloads, requires zero tuplespace schema change, and is the only option that
fits the single-render-broadcast SSE model without a server rearchitecture. Tradeoffs
and a server-side alternative are documented below.

---

## 1. How the board renders cards today

### Server side — `src/api/DashboardSSE.mag`

- `renderWorkitemsHtml: snap` (**line 436**) builds the Kanban board.
  - Reads `snap at: 'workitems'` (populated by `bbs scanAll: 'workitem'`, see
    `DashboardSSE.mag:155`).
  - Buckets items by `payload.status` into the fixed column list
    `#('backlog' 'ready' 'in-progress' 'done' 'blocked')` (**line 441**).
  - **Important exclusion behavior:** the render loop only walks those 5 statuses.
    Any item with another status (e.g. `cancelled`) gets a bucket created but is
    **never rendered** — this is exactly how `Cancel item` makes a card disappear.
  - Each card (**lines 476–486**) already emits the attributes the modal needs:
    ```
    <div class="wi-card wi-<type>"
         data-wi-id="<identity>"
         data-wi-type="<type>"
         data-scope="<scope>"
         data-repo="<repo>"> … </div>
    ```
  - The in-code comment at **line 474** explicitly says these attributes exist to
    "let the Board detail modal resolve the item's identity + scope to fetch
    `/api/workitem/detail`."

### SSE delivery (Datastar v1 conventions)

- `renderWorkitemsHtml:` output is wrapped in `<div id="dashboard-workitems">…</div>`
  and pushed as a `patch-elements` event (`sendElements:to:`, `DashboardSSE.mag:200`).
- Client handler `handlePatchElements` (**dashboard.html:1248**) strips the
  `elements ` line prefix, parses the fragment, and replaces the matching element by
  `id` via `target.outerHTML = root.outerHTML` (**line 1261**).
- After replacing the board it calls `refreshBoardFilters()` (**line 1263**) — this is
  the re-application hook that any client-side hide must piggyback on, because the
  board DOM is wholesale-replaced on every tick.

### Client side — `static/dashboard.html`

- Card CSS / hover / cursor:pointer at **lines 505–533**.
- `.wi-card.wi-hidden { display: none; }` (**line 709**) — already used by the board
  search/repo filter (`applyBoardFilter`, **line 1380**, via `classList.toggle('wi-hidden', …)`).
- The full modal markup is present: scrim `#wi-modal-scrim`, dialog `#wi-modal`,
  close button, body `#wi-modal-body` (**lines 1117–1122**), with complete riso-styled
  CSS for `.wi-modal*` (**lines 533–625**).

---

## 2. Where detail data comes from

### `/api/workitem/detail` already exists (no new endpoint needed for read)

- Route registered **unsigned GET** at `Server.mag:366`
  (`self get: '/api/workitem/detail' do: [:req | self handleWorkitemDetail: req]`).
- `handleWorkitemDetail:` (**Server.mag:2415**) → `handleWorkitemDetailScope:identity:`
  (**2429**):
  - `bbs findInIndex: 'workitem' scope: scope identity: identity` → 404 if missing.
  - `bbs scanChildrenOf: identity scope: scope` → child summaries (derived from
    `child.parent`, never stored).
  - Returns `{ identity, scope, payload, children:[{identity,title,type,status}] }`.
- Because the **entire `payload` is returned**, every field the mission lists is
  already on the wire *if present in the payload*:
  - title, description, status, type, parent, wave, labels — **yes** (rendered).
  - **comments** — `payload.comments` (array of `{author,text,timestamp}`, written by
    `/api/workitem/comment`, `Server.mag:2327`). **On the wire, but not rendered.**
  - **linked workflow** — **not present** in the workitem payload. Needs derivation
    (see §5). Tuplespace reads alone do not currently expose a workitem→workflow link.

**Conclusion:** existing tuplespace reads suffice for everything except the linked
workflow. No new read endpoint is required for comments.

---

## 3. Click → open-modal wiring (already implemented, client-side)

`initWorkitemModal()` IIFE at **dashboard.html:1528**:

- **Delegated** click listener on `#dashboard-workitems` (**line 1697**) — survives the
  SSE wholesale-replace because it is bound to the stable parent, not to cards.
- Ignores clicks on interactive children (`a, button, input, select, textarea, label`).
- Reads `data-wi-id` + `data-scope` from the clicked `.wi-card`, calls
  `openModal(scope, identity)`.
- `openModal` (**1559**) `fetch()`es `/api/workitem/detail?scope=…&identity=…`
  client-side and renders into `#wi-modal-body`.
- `renderDetail(data)` (**1579**) builds the HTML; accessibility is handled (focus
  trap, Esc-to-close, scrim click, `aria-modal`).

### Architecture note: client-fetch, NOT server-rendered SSE

The modal is deliberately **client-side fetch**, *not* a Datastar `patch-elements`
push. This is the correct choice and should be kept:

- The modal is a **per-viewer, on-demand, request/response** interaction. The SSE
  channel is a **broadcast** of shared board state; pushing one viewer's modal down
  the shared channel would render it for everyone.
- It keeps the SSE snapshot small and the open-action latency independent of the
  broadcast tick.

**Recommendation:** keep modal open + dismiss interactions client-side / request-
response; do not route them through SSE patch-elements.

---

## 4. The `dismiss` semantics — the core decision

The mission's `dismiss` = "hide *this* story from the board so it no longer appears,"
offered as a modal action, persistent across reloads. Critically this is described as
**per-user/per-board** and **non-destructive** — it must NOT change the story's real
status or affect other viewers' boards.

### Why the existing `Cancel item` is *not* dismiss

`/api/workitem/cancel` (`Server.mag:2457`, `handleWorkitemCancelRoute:`):
- POST, **loopback-gated** (Host header check, `isLoopbackHost:` 2488).
- Sets `status='cancelled'` + `cancelled_at` via `applyWorkitemFields:` (2475), reusing
  the update merge path (validation, parent-promotion, child-cascade).
- This is a **global, semantic, destructive** mutation: the card disappears for
  *everyone* because `cancelled` is not a rendered column (§1). It changes the work
  item's meaning ("this work is cancelled"), which is wrong for "I don't want to see
  this on my board right now."

So `dismiss` must be a *separate* mechanism.

### The dominating constraint

`renderWorkitemsHtml:` runs **once per SSE tick** and the result is broadcast to **all**
subscribers (`broadcast`/`sendElements:to:`). There is no per-subscriber rendering and
no server-side notion of "the requesting user" at render time. Therefore **any
server-side per-user filtering would require re-architecting the board render into a
per-subscriber render** — a large, risky change to the hot SSE path.

### Options considered

| Option | Persistence | Per-user? | Server change | SSE-model fit | Schema change |
|---|---|---|---|---|---|
| **A. Client-side `localStorage` hide** (recommended) | reloads (same browser) | yes (per browser) | none | excellent | none |
| B. New `board-dismissal` tuple keyed (user,scope,wid), filtered client-side | cross-device | yes | small read endpoint | good | new category |
| C. New `board-dismissal` tuple, filtered **server-side** in render | cross-device | yes | **large** (per-subscriber render) | poor | new category |
| D. `dismissed`/`dismissed_by` flag on the workitem payload | cross-device | only if list-valued | medium | poor→mediocre | payload field |

#### Option A — client-side `localStorage` (RECOMMENDED)

- On `Dismiss` click: add `scope|identity` to a `Set` persisted under a
  `localStorage` key (e.g. `pp.dismissedCards`), then hide the card and close the modal.
- Hiding reuses the **existing** `.wi-hidden` class and the **existing**
  `refreshBoardFilters()` → `applyBoardFilter()` re-application path that already runs
  after every SSE patch (**dashboard.html:1263, 1380, 1395**). Add a dismissed-set check
  inside `applyBoardFilter` so dismissed cards are hidden on every re-render.
- A small "N hidden — show all / undo" affordance near the existing board filter
  (`#board-filter-*`, ~line 1376) lets the operator un-dismiss.

**Pros:** zero tuplespace/schema change; survives reloads (localStorage); naturally
per-viewer; rides infrastructure that already exists; cannot corrupt shared state; no
new attack surface on a browser-initiated mutation.
**Cons:** not synced across browsers/devices; cleared if the user clears site data;
"per-board" only insofar as the dismissed key can be namespaced by scope (it should be,
e.g. store `scope|identity` so the same story can be dismissed independently per scope).

This option also matches the strong signal that `.wi-hidden` was **pre-staged** for
exactly this kind of hide.

#### Option B — `board-dismissal` tuple, client-side filter

- New tuple category `board-dismissal`, identity e.g. `<user>:<scope>:<wid>`,
  `modality persistent`, payload `{user, scope, wid, dismissed_at}`.
- New unsigned GET `/api/board/dismissals?user=…` (mirrors the existing unsigned
  read-only dashboard GETs). Client fetches the set on load and after reconnect, then
  filters client-side exactly as Option A.
- Dismiss action POSTs a new `board-dismissal` (loopback-gated, like cancel).
- **Pros:** cross-device persistence; auditable; true per-user/per-board.
- **Cons:** needs schema + 2 endpoints + a current-user identity on the dashboard
  (today the dashboard is effectively single-operator/loopback and "user" is fuzzy —
  see the `multiplayer:identity` follow-up referenced at `Server.mag:2497`). More moving
  parts; the per-user identity story is not yet solid.

#### Option C — server-side filtered render

Rejected: forces per-subscriber rendering of the board, defeating the single-render
broadcast design and touching the performance-sensitive SSE path. High risk, low payoff.

#### Option D — flag on the workitem payload

Rejected for per-user dismiss: the workitem tuple is **shared/global**. A scalar
`dismissed=true` hides it for everyone (that's just a worse `cancel`). A list-valued
`dismissed_by:[…]` invites lost-update races on a hot shared tuple and still needs the
per-user render in §C to be excluded server-side. Only viable if "dismiss" were ever
redefined as global.

### Recommendation & sequencing

**Phase 1 (this mission): Option A.** Smallest, safest, fully satisfies "hide + persist
across reloads" for the current single-operator dashboard.
**Phase 2 (later, with multiplayer identity): migrate to Option B** for cross-device
sync, reusing the same client-side filter so the UI does not change.

---

## 5. Linked workflow — the one genuine data gap

Workitem payloads do not carry a `workflow_id`. Options:
- **Derive on the server** in `handleWorkitemDetailScope:identity:` by scanning
  `workflow` tuples for one whose `params` reference this work item (e.g. a
  `params.workitem_id` or matching identity), and attach `d at: 'workflow'` to the
  detail response. Cleanest for the client.
- **Or** display the workflow only when the linkage already exists (e.g. if a future
  `payload.workflow_id` is stamped at run time by `/api/workitem/run`,
  `handleWorkitemRun:`). Worth confirming whether `run` stamps a back-reference; today
  it does not appear to.

**Recommendation:** treat linked-workflow as a small, optional server-side enrichment of
the *existing* detail endpoint (add a `workflow` summary field), rendered by the client
only when present. Keep it out of the critical path of the dismiss work.

---

## 6. Exact files / functions to touch

### `static/dashboard.html` (most of the work)
1. `renderDetail(data)` (**~1579**): render `payload.comments` (array of
   `{author,text,timestamp}`) as a `.wi-modal-rel`-style block; render
   `data.workflow` (once §5 supplies it) as a row/link.
2. `renderDetail(data)` actions block (**~1644**): add a **`Dismiss`** button alongside
   `Cancel item`. Wire its click to add `scope|identity` to the dismissed set, persist
   to `localStorage`, hide the card, close modal.
3. `applyBoardFilter()` (**~1380**): also hide cards whose `data-scope|data-wi-id` is in
   the dismissed set (so dismissal re-applies after every SSE patch via the existing
   `refreshBoardFilters` call at **1263**).
4. Add a small "N hidden / show-all" control near `#board-filter-*` (**~1376**) for undo.
5. (Optional CSS) a distinct style if dismissed cards should be greyed in a "show all"
   mode rather than fully hidden.

### `src/api/Server.mag` (only if doing §5 or Option B)
- §5 linked workflow: enrich `handleWorkitemDetailScope:identity:` (**2429**) to attach a
  `workflow` summary. *Run `gitnexus_impact` on this symbol first — it is the shared
  detail core.*
- Option B only: add `board-dismissal` write (loopback-gated POST, mirror
  `handleWorkitemCancelRoute:` 2457) and an unsigned GET list endpoint; register in
  `registerDashboardRoutes2` (**361**).

### `src/api/DashboardSSE.mag`
- **No change needed for Option A.** (`renderWorkitemsHtml:` already emits all required
  `data-*` attributes.) Touch only if Option B's server-side filtering were chosen
  (not recommended).

### Tuplespace schema change
- **Option A: none.**
- Option B: new `board-dismissal` persistent category (and a current-user identity).
- Option D: new `payload.dismissed`/`dismissed_by` field (not recommended).

---

## 7. Risks & notes

- **Mission overlap / possible duplicate work.** The detail modal is already built. The
  net-new work is narrower than the brief implies. Confirm the brief's author knows the
  modal exists, to avoid re-implementing it.
- **Dismiss vs Cancel confusion (UX).** Two adjacent destructive-looking actions
  (`Cancel item` = global/permanent; `Dismiss` = personal/reversible) must be visually
  and textually distinct, or operators will conflict the two. Recommend clear labels
  ("Cancel item (everyone)" vs "Hide from my board") and an obvious undo for dismiss.
- **localStorage unbounded growth (Option A).** The dismissed set grows monotonically and
  can accumulate ids for items that were later cancelled/deleted. Mitigate by pruning
  ids not present in the current board on each `refreshBoardFilters` (cheap, and keeps
  the set bounded to live items) — but note this also means a dismissed item that
  *reappears* (status flip) would surface again; document the chosen behavior.
- **Per-board semantics.** Namespace the dismissed key by `scope` (store `scope|identity`)
  so dismiss is per-board, matching the existing scope/`pp-scope-mine` model
  (**dashboard.html:916, 1155**). A bare identity would hide the item across all scopes.
- **SSE re-render timing.** Because the board DOM is replaced wholesale each tick, the
  hide MUST be re-applied from `applyBoardFilter` (driven by `refreshBoardFilters` at
  **1263**) — hiding the DOM node directly at click time is not enough; it returns on the
  next tick.
- **Multiplayer / identity.** True per-user server-side dismiss (Option B/C) is blocked
  on the unresolved dashboard identity story already flagged at `Server.mag:2497`
  (`multiplayer:identity`). Option A sidesteps this entirely for now.
- **`gitnexus_impact` before any server edit.** Per repo CLAUDE.md, run impact analysis
  on `handleWorkitemDetailScope:identity:` before enriching it for §5.

---

## Appendix — key references

| What | Location |
|---|---|
| Board render (Kanban) | `src/api/DashboardSSE.mag:436` `renderWorkitemsHtml:` |
| Card `data-*` attributes | `src/api/DashboardSSE.mag:474–486` |
| Status columns (exclusion behavior) | `src/api/DashboardSSE.mag:441` |
| Detail endpoint route (unsigned GET) | `src/api/Server.mag:366` |
| Detail handler | `src/api/Server.mag:2415` / core `:2429` |
| Comment write (payload.comments) | `src/api/Server.mag:2327` |
| Cancel route (global, loopback-gated) | `src/api/Server.mag:2457` |
| Loopback gate | `src/api/Server.mag:2488` `isLoopbackHost:` |
| Modal markup | `static/dashboard.html:1117–1122` |
| Modal CSS | `static/dashboard.html:533–625` |
| `.wi-hidden` | `static/dashboard.html:709` |
| Modal JS (open/render/cancel) | `static/dashboard.html:1528–1725` |
| SSE patch handler + board re-filter hook | `static/dashboard.html:1248–1266` |
| Board filter (re-applied per tick) | `static/dashboard.html:1380–1403` |
| Scope/`pp-scope-mine` model | `static/dashboard.html:916, 1127–1216` |
