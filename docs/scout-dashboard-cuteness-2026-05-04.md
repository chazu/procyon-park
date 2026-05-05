# Scout Report: Dashboard Cuteness Opportunities

**Date:** 2026-05-04
**Scope:** `pp dashboard` / `pp serve` web UI — research only, no code changes.
**Author:** scout

## TL;DR

The dashboard already has a *very strong* visual identity: a riso-print on
warm-near-black aesthetic with a five-ink palette (moss / terra / cobalt / sun
/ rose) and a slab+serif+mono type system. The bones are excellent. The vibe is
"riso punk operator console" — handsome, but currently a little stern. There's
no mascot presence on screen, no microinteractions beyond a couple of pulses, no
celebration when work completes, and empty states are all a flat "Connecting…"
or em-dash.

The role-badges already render inline next to active task roles
(`dashboard.html:484`) and use the gorgeous raccoon-eye PNGs in
`static/roles/role-*.png` — but this is the only mascot surface in the entire
UI. There is enormous low-effort upside in (a) leaning the raccoon harder, (b)
giving each empty state a personality, (c) celebrating workflow completion, and
(d) tightening the riso textures already present.

---

## 1. Current State (what it looks like today)

### 1.1 Page anatomy

All of the dashboard chrome lives in a single file:
`static/dashboard.html` (1,425 lines, inline `<style>` and `<script>`). Panel
*content* is server-rendered HTML strings produced by
`src/api/DashboardSSE.mag` and patched in via Datastar SSE
(`datastar-patch-elements`, see `dashboard.html:1147` and
`DashboardSSE.mag:170`).

Layout (`dashboard.html:863-1022`):

```
┌────────────────────────────────────────────────────────────────────┐
│  [logo]  PROCYON PARK            [Mine|Team] [pending: N] [● Live] │  hero (12px–10px pad, 128px logo)
│          SWARM DISPATCH                                            │  hero-sub
├────────────────────────────────────────────────────────────────────┤
│ ▌moss ▌terra ▌cobalt ▌sun ▌rose                                    │  ink-rule (3px five-color hairline)
├────────────────────────────────────────────────────────────────────┤
│  Overview │ Board │ History                                        │  tabbar (sticky, slab caps)
├────────────────────────────────────────────────────────────────────┤
│ ┌─ Active Workflows (panel--terra) ┐ ┌─ Activity (panel--cobalt) ┐ │
│ │ • wf-card (left bar = status)    │ │ filters: scope/wf/sev/txt │ │
│ │   role badge img next to role    │ │ • notif li sev-* colored  │ │
│ │ • Recent Completions             │ │                           │ │
│ │ • Scope Violations (sv-list)     │ │                           │ │
│ │ • Presence (humans | workers)    │ │                           │ │
│ └──────────────────────────────────┘ └───────────────────────────┘ │
└────────────────────────────────────────────────────────────────────┘
```

### 1.2 Palette (already lovely)

`dashboard.html:16-44` defines:

- Surfaces: `--bg #0a0806` (sampled from logo substrate), `--bg-2 #121010`,
  `--surface #17140f`, `--surface-hi #221d16`.
- Rules: `--rule #3a3024`, `--rule-hi #524432`.
- Ink: `--cream #ece3cf`, `--cream-hi #f5ecd6`, `--cream-dim #b0a58c`,
  `--fog #7e745f`.
- Spot inks: `--moss #6f9c4a`, `--terra #e05a24`, `--cobalt #3f7fcf`,
  `--sun #f0b830`, `--rose #d24e68`. (These are the same five inks shown in the
  `.ink-rule` swatch under the hero, `dashboard.html:885-891`.)

This is already a riso palette pulled off well. The `bluestripedpot.webp`
reference (red+yellow+blue+green) is a slightly more saturated children's-book
direction; `riso.webp` (teal+pink+orange+green diamonds) and `riso2.webp`
(festival flyers) are mid-century pulp/risograph aesthetic — closely related.

### 1.3 Typography

- `--f-slab` Big Shoulders Display 700/800/900 — used for headings, tab labels,
  badges, role pills.
- `--f-serif` Fraunces 600/900 italic — used *only* in `.empty-state` and
  `.sv-summary b`. **Underused.** This face has incredible cute potential
  (italic Fraunces is downright friendly).
- `--f-mono` JetBrains Mono — body, IDs, timestamps.

The hero title uses an offset-shadow trick (`text-shadow: 2px 0 0 var(--terra),
-2px 1px 0 var(--moss);`, `dashboard.html:95`) — a cheap, tasteful
mis-registration effect that mimics a 2-color riso plate misalignment. Beautiful.

### 1.4 Riso/halftone textures already in place

- `--grain-light` and `--grain-dark` SVG `feTurbulence` filters as
  `body.background-image` (`dashboard.html:46-49, 60`). Two-layer paper grain.
- Halftone-dot corner flag on every `.panel::before` (`dashboard.html:244-252`)
  using `radial-gradient` at 5px tile.
- Repeating-linear-gradient stripe in `panel h2::after`
  (`dashboard.html:273-278`) — the "ticket stub" tear pattern in titles.
- 45° caution-tape stripes on systematic scope violations
  (`dashboard.html:418-426`) and 135° hot-zone stripes on the "in-progress"
  Kanban column (`dashboard.html:495-499`).
- Hard offset shadows on panels (`box-shadow: 4px 4px 0 ...`,
  `dashboard.html:237`) — the "printed sticker on dark paper" effect.

### 1.5 Mascot/raccoon usage today

**Just one place.** `DashboardSSE.mag:484` injects an `<img class="role-badge"
src="/dashboard/roles/role-<role>.png">` next to the role name in active
workflow cards. Style is `dashboard.html:662-671` — 18px tall, inline,
silent failure on missing image (`onerror="this.style.display='none'"`).

The six role badge PNGs at `static/roles/role-{scout,planner,implementer,
reviewer,foreman,head}.png` are **gorgeous** — single-color riso raccoon-eye
strips, one per role color, with tiny role labels under each in the source
sheet. The Scout badge is cobalt-blue raccoon eyes; Planner is moss-green
goggles; Implementer is terra+cream furrowed brow; Reviewer is sun-yellow
side-eye; Foreman is cobalt+terra combo eyes; Head is fierce terra eyes.

These are dramatically underused. They appear at 18px height ONCE per active
workflow card. Nowhere else in the UI is the raccoon present.

The combined `role-badges.png` at the repo root is a 6-up sheet of all six
badges (presumably the source).

The `static/logo.png` is the green+terra "pp" infinity-mark on the dark
substrate that the page background is tuned to bleed into seamlessly. It is
rendered once at 128px in the hero (`dashboard.html:864`). No hover state, no
animation.

### 1.6 Microinteractions present today

- `.queue-badge.is-hot` pulses orange when pending > 3× slot capacity
  (`dashboard.html:128-138, 1417`).
- `.conn .dot` pulses moss-green when SSE is live (`dashboard.html:151-157`).
- `.wf-card:hover` and `.wi-card:hover` lift 1px and gain a sticker shadow
  (`dashboard.html:353, 514`).
- `@keyframes press-in` (`dashboard.html:851-858`) — a tiny "stamp landing"
  animation on every newly-rendered card / list item. **This is the cutest
  thing in the UI** and most users will never consciously notice it.

### 1.7 Empty states

`.empty-state` exists (`dashboard.html:828-839`) with italic Fraunces and a
dotted-grid background. But every actual usage just says "Connecting…",
"No recent completions", "No scope violations recorded", "No subscribers",
"No workers online", or "—". Functionally fine, emotionally flat.

### 1.8 Vibe summary

- **Strengths:** distinctive five-ink riso identity, consistent type system,
  good use of offset shadows + halftones, server-rendered HTML keeps the SSE
  hot path simple.
- **Weaknesses:** raccoons hide, no celebration ever, headers are stern, empty
  states are silent, the logo doesn't move, Fraunces is barely used.

---

## 2. Cute-ification opportunities

### 2.1 Mascot — "the raccoons are running the park"

**Concept:** the raccoons in the role-badge sheet are characters, not icons.
Lean in. Each role gets a name (the badge already labels them: scout, planner,
implementer, reviewer, foreman, head). Surface them everywhere identity is
shown.

Concrete surfaces:

1. **Presence panel workers list** (`DashboardSSE.mag:325-360`) currently
   prints `worker:foo  op=… slots=2  models=…`. Each worker tuple's payload
   includes a *role*-ish association via the tasks they pick up; even without
   that, every worker is fundamentally a raccoon. Render a 14-16px raccoon
   eye-strip (could rotate through the 6 badges, or use a generic
   "worker-raccoon" eye-strip we generate) on the left of each `.presence-worker
   li` (`dashboard.html:326-327`). The cobalt left-border becomes the raccoon's
   tail.
2. **Notification items** — when a notif's scope corresponds to a role, prepend
   the role badge. E.g. "implementer:foo dispatched task X" gets the
   implementer eye-strip glyph before the timestamp.
3. **Hero corner mascot** — a small (40-60px) raccoon peeking over the right
   edge of the hero, maybe just the top of the head + ears, that *blinks every
   ~8s* via a 2-frame CSS animation (eyes-open → eyes-closed → eyes-open). The
   blink is the cuteness kill-shot. (Implementation: 2 PNGs swapped via
   `@keyframes` with `animation-timing-function: steps(1)`.)
4. **Empty-state mascots** — see §2.3.
5. **Tab indicator** — when a tab has new activity, prepend a tiny raccoon-eye
   bullet in the tab's ink color.
6. **Scope violations role row** (`DashboardSSE.mag:252-257`) — already
   prints `<b>{role}</b>` but without the badge. Inject the role-badge `<img>`
   before `<b>{role}</b>` to keep visual identity consistent with active
   workflow cards.

### 2.2 Workflow-completion delight (Datastar SSE confetti)

**Today:** `wf-card.completion-completed` quietly turns moss-green and the
`<b>` template pill becomes a green stamp (`dashboard.html:371-376`).

**Concept:** when a workflow transitions running→completed, fire a small
confetti burst from the wf-card's position. This is server-driven via the
existing SSE channel — emit a `datastar-merge-signals` or a custom event
(`pp-celebrate`) carrying the wfId, and a tiny client-side handler spawns a 30-
particle riso-shaped confetti (diamonds + circles in the five inks for ~1.4s,
then fade). The shapes should match the riso vocabulary — solid filled
diamonds and squares like in `riso.webp`, NOT generic round confetti.

For failed: a cream-colored "aw shucks" puff (no celebration). For cancelled:
nothing.

Implementation note: server-side confetti dispatch lives naturally in
`DashboardSSE.mag>>renderCompletionsHtml:` — when emitting a card whose
`endedAt` is within the last tick window (~5s), add a `data-celebrate="just-now"`
attribute. The client `MutationObserver` watches for this attribute appearing on
any `.wf-card.completion-completed` and triggers the confetti once per id.
This avoids needing a separate event channel.

### 2.3 Friendlier empty states

Replace generic strings with characterful copy + a tiny mascot illustration.

| Panel | Current | Proposed |
|---|---|---|
| Active Workflows (`dashboard.html:908`) | "Connecting…" | scout raccoon eye-strip + "Park's quiet. Nothing running right now." |
| Recent Completions | "No recent completions" | foreman eye-strip + "Nothing's wrapped up yet today." |
| Scope Violations (`DashboardSSE.mag:237`) | "No scope violations recorded" | (a tiny green check + halftone dot pattern) "All raccoons stayed in their lanes. ✦" |
| Presence humans | "No subscribers" | "No humans watching. Hello?" (cream italic) |
| Presence workers (`DashboardSSE.mag:323`) | "No workers online" | sleeping-raccoon glyph + "All raccoons are napping." |
| Kanban column empty (`DashboardSSE.mag:416`) | "—" | a single 6×6 halftone dot in the column's color (almost invisible — quieter, more deliberate than the em-dash). |
| Activity feed | "No recent activity" | "All quiet on the activity feed. 𓂃 ✿" |

Style polish: empty-state currently has a `radial-gradient` dot grid
(`dashboard.html:837`). Bump dot opacity in cream, give it a 2px dashed cream
border instead of `var(--rule)`, and increase the Fraunces italic size to 14px.

### 2.4 Softer color/typography touches

These are feather-touches, not redirects. The current palette is too good to
overhaul.

1. **Use Fraunces more.** It's loaded but only used in 2 places. Use italic
   Fraunces for the `--hero-sub` ("swarm dispatch") and for empty-state copy.
   Drop `letter-spacing: 0.24em` on the hero-sub — it currently looks like
   surveillance-state caps; italic Fraunces in normal tracking would feel like
   a children's-book caption.
2. **Round a couple of corners.** The dashboard is rigorously square
   everywhere — that's part of the riso identity. But the `queue-badge` and
   `conn` pills (`dashboard.html:108-150`) could become true pills (12px
   border-radius) without breaking the aesthetic. Pills read "playful";
   rectangles read "industrial".
3. **Halftone fade between sections.** Add a 12px tall halftone-dot fade at
   the bottom of each panel (radial-gradient that thins out). Gentler than the
   hard `border` we currently rely on.
4. **Kanban column header texture.** The current solid-color headers
   (`dashboard.html:487-492`) could each get a 4px halftone overlay in
   `var(--cream)` at 0.08 alpha. Keeps the riso vocabulary going on the board.
5. **Workflow card "stamped" feel.** The `<b>` template pill on `.wf-card`
   currently has zero rotation. A `transform: rotate(-1deg)` on
   `.wf-card.completion-completed b` would give the *moss "DONE" stamp* an
   actual hand-stamped feel. Same trick at `+1deg` for `failed` (rose). 1°
   isn't enough to look broken; it's enough to feel human.

### 2.5 Microinteractions to add

1. **Logo wiggle on hover.** `dashboard.html:864` — the hero logo never reacts.
   Add `transition: transform .25s` and `.hero-logo:hover{ transform: rotate(-3deg)
   scale(1.04); }`. The riso "pp" infinity-mark already looks like a friendly
   creature; let it nod.
2. **Tab "click" stamp.** When a tab is activated, briefly flash a
   `box-shadow: 0 0 0 3px var(--terra)` ring then collapse it (200ms). Mimics a
   rubber-stamp impact.
3. **Filter button hover already has transform** (`dashboard.html:586`); extend
   the same pattern to all buttons (`board-filter-clear`, `hist-refresh`,
   `hist-clear`) for consistency. They currently only color-shift.
4. **Live dot heartbeat instead of pulse.** The current `conn .dot` ring pulse
   (`dashboard.html:155-157`) is uniform. A two-beat heartbeat (lub-dub) at
   1.2s feels more alive. Replace `@keyframes pulse` with two-stage ring
   expansion.
5. **Press-in animation on initial mount, not on every replace.** Currently
   `.wf-card, .wi-card, .sv-list li, #dashboard-notifications li` ALL animate
   `press-in` on every Datastar patch (`dashboard.html:856-858`). After the
   first tick this means every card re-stamps every 5s if anything in the
   parent re-renders. Animate ONLY new cards (compare via `data-id` on a
   MutationObserver, or scope the keyframe to a `.is-new` class the server
   adds for items <3s old). This is currently a *visual nausea* hazard —
   removing it will paradoxically make the UI feel calmer, which gives more
   room for added cuteness elsewhere.
6. **Workitem card type-pill micro-rotation.** The `.wi-type` pill
   (`dashboard.html:520-532`) at `-1deg`/`+1deg` randomized per card via
   `nth-child` mod 2. Free, breaks the grid rigidity, reads as
   sticker-on-cardboard.
7. **Sound (optional, opt-in).** A single 80ms wood-block "tock" on workflow
   complete, gated by a localStorage `pp.cuteness.sound` setting and a tiny
   speaker toggle in the hero. Most operators won't enable it; the ones who do
   will love it.

### 2.6 Riso/print-pack aesthetic deepening

The `Riso-Print-Pack-Cover-Affinity` and `riso2.webp` references suggest a
"matchbook / festival flyer" direction. Specific borrows:

- **Diamond/dot horizontal accent** like the diamonds-on-net in `riso.webp`.
  Replace the current `panel h2::after` repeating-line with a row of small
  riso diamonds at 6px size in alternating ink colors.
- **Hand-drawn underline** on `h2` headings. A tiny SVG squiggle under the
  text, in `var(--terra)`.
- **"Park map" easter egg.** The repo's name *Procyon Park* literally invites
  it. A `?map` URL parameter (or a hidden ‹m› hotkey) replaces the Overview
  layout with a stylized riso "park map" showing each panel as a building:
  Workflows = the Foreman's Office, Activity = the Bulletin Board, Presence =
  the Campsite, Scope Violations = the Lost & Found. High-effort, very high-
  delight.

### 2.7 Things to NOT do (preserving identity)

- Don't add gradients to surfaces. The flat-ink-on-paper feel is the whole
  point.
- Don't soften `box-shadow: 4px 4px 0` to a blurry shadow. Keep it printed.
- Don't add rounded corners to panels. Keep pills only.
- Don't replace the slab face with anything friendlier — `Big Shoulders` IS
  the festival-poster voice.
- Don't make the raccoons sad or anime. The eye-strips are perfect because
  they're *one expression each, riso-stamped*. Resist the urge to commission
  full-body characters.

---

## 3. Prioritized punch list

### Tier 1: ≤30 min each, pure CSS or HTML, huge ROI

| # | Item | File:line | Notes |
|---|---|---|---|
| 1 | Fix `press-in` over-animation | `dashboard.html:851-858` | Remove animation from list items that already exist. Prevents whole-feed re-stamping every 5s tick. **Should ship before any new animation.** |
| 2 | Empty-state copy pass | `dashboard.html:908,912,916,920,944,963,1014` + `DashboardSSE.mag:237,302,322,416,830` | Replace 7 strings with characterful copy. Pure server-side text edit. |
| 3 | Logo hover tilt | `dashboard.html:77-83` | Add `transition` + `:hover` rule. 4 lines. |
| 4 | Stamped completion rotation | `dashboard.html:371-376` | `transform: rotate(-1deg)` on completed `<b>`, `+1deg` on failed. |
| 5 | Pill the queue/conn badges | `dashboard.html:108,139` | Add `border-radius: 999px`. 2 lines. |
| 6 | Role badge in scope violations | `DashboardSSE.mag:252-257` | One line of HTML alongside `<b>{role}</b>`. |
| 7 | Fraunces in `.hero-sub` | `dashboard.html:97-103` | Swap to italic Fraunces, drop tracking. |

### Tier 2: 1-3 hours each, needs small client JS or a server tweak

| # | Item | Touches | Notes |
|---|---|---|---|
| 8 | Hero corner blinking raccoon | static asset (new 2-frame eye PNG) + `dashboard.html` hero | Two PNGs and a CSS keyframe. Single biggest perceived charm bump. |
| 9 | Workflow-complete confetti | `DashboardSSE.mag:736-760` (add `data-celebrate` attr) + ~80 lines client JS in `dashboard.html` | Use existing SSE patch path; no new event channel. |
| 10 | Role badge in presence-workers and notifications | `DashboardSSE.mag:325,853` | Need a way to pick a role for a worker (rotate, or inspect last task picked up). |
| 11 | Halftone-diamond `h2::after` accent | `dashboard.html:273-278` | Replace stripe with inline-SVG diamond row. |
| 12 | Heartbeat `conn .dot` | `dashboard.html:151-162` | New keyframe. |
| 13 | Workitem type-pill micro-rotation | `dashboard.html:520-532` | `nth-child(2n)` rotation. |
| 14 | Tab-stamp click flash | `dashboard.html:186-211` | New keyframe + class added on activateTab. |

### Tier 3: half-day or more, design-led

| # | Item | Notes |
|---|---|---|
| 15 | Riso confetti shapes (diamonds/squares not circles) | Needs ~3 SVG sprites + emitter logic. Pairs with #9. |
| 16 | "Park map" Overview alternate layout | Needs a hand-drawn riso park-map SVG and a layout swap. Aspirational. |
| 17 | New worker-raccoon "generic" eye-strip | If we want to badge workers without coupling them to a role, need a 7th badge in the same style as `static/roles/*.png`. |
| 18 | Ambient "park" sound design | Wood-block tock on complete, paper-shuffle on tab-switch, gated by opt-in toggle. |
| 19 | Sleeping-raccoon empty-state illustration | One PNG used in 2 empty states. |
| 20 | Mascot hello-message rotation | Hero hover-tooltip cycles raccoon greetings ("welcome back", "pending: 3", etc.) |

### Anti-tasks (DO NOT do)

- Replacing the dark substrate with light cream (it would un-tune the logo
  bleed on `dashboard.html:81-82`).
- Adding rounded corners to panels.
- Replacing `Big Shoulders Display` with something softer.
- Adding gradients to ink colors.
- Swapping the SVG turbulence grain for a static raster — it scales with
  viewport for free.

---

## 4. Quick reference — files to touch for any cuteness work

| Concern | File | Notable lines |
|---|---|---|
| All visual style | `static/dashboard.html` | `:root` 16-50, panels 232-278, kanban 444-532, empty 828-843, animations 851-858 |
| Server-rendered card HTML | `src/api/DashboardSSE.mag` | workflows 442-501, completions 707-763, scope-violations 184-263, presence 265-367, workitems 390-440, notifications 821-864 |
| Static images | `static/logo.png`, `static/roles/role-*.png` | role badges injected at `DashboardSSE.mag:484` |
| Source role-badge sheet | `role-badges.png` (repo root) | for re-cropping or extending |
| Riso aesthetic references | `riso.webp`, `riso2.webp`, `bluestripedpot.webp`, `Riso-Print-Pack-Cover-Affinity_*.webp` (repo root) | mood board only |
| SSE event plumbing | `src/api/DashboardSSE.mag:170-181` (`sendElements:to:`) | for adding `pp-celebrate`-style events if needed |
| Client SSE handler | `dashboard.html:1147-1165` (`handlePatchElements`) | hook point for celebrate-on-attribute MutationObserver |

---

## 5. Recommended sequencing

1. **Ship Tier-1 items 1-7 in one PR.** All pure CSS/copy, zero server logic
   risk. The `press-in` fix (item 1) alone makes the dashboard feel calmer
   without removing any animation that users actually enjoy.
2. **Add the blinking hero raccoon (item 8) standalone.** It's the single
   biggest perceived-charm delta and doesn't depend on anything else.
3. **Then confetti (item 9) + role badges spread (item 10).** These together
   give the dashboard *celebration* and *characters*.
4. Tier-3 items as design bandwidth allows. Park-map (item 16) is the
   showstopper but rightly the last thing to attempt.

Total Tier-1 effort: a single afternoon. Total Tier-1+Tier-2: a long weekend.
