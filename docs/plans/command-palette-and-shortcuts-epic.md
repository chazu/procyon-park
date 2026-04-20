# Epic: Command Palette & Keyboard Shortcuts for the Procyon Park Dashboard

**Status:** Draft
**Owner:** TBD
**Created:** 2026-04-19
**Target surface:** `static/dashboard.html` (single-file, vanilla JS)

---

## Summary

The Procyon Park dashboard currently exposes three tabs (Overview, Board, History) and a dense set of live-updating cards: active workflows, recent completions, scope violations, presence, activity/notifications, work items, and history rows. Everything is reachable only by pointer — clicking tabs, hunting through scrolling feeds, or typing into per-panel filter inputs.

This epic adds a **command palette** (⌘K / Ctrl-K) and a **vim-style leader shortcut layer** so a keyboard-first operator can reach anywhere, act on anything, and discover what is reachable without leaving the home row.

The palette is a single in-page overlay that indexes every navigable and actionable surface of the dashboard:

- **Tabs** (Overview / Board / History) — navigate
- **Active workflows** — scroll to + highlight a `.wf-card`
- **Work items** (epic/story/task) — switch to Board, scroll to + highlight card
- **Agents / identities** from presence — focus the presence entry
- **Recent notifications** — scroll to + highlight a notification row
- **Dashboard actions** — toggle scope (mine/team), clear filters, focus a filter input, re-query history, open cheatsheet, copy viewer identity

A second lightweight layer gives **leader chords** (`g b` → Board, `g h` → History, `g o` → Overview, `/` → focus active-panel filter, `?` → cheatsheet, `Esc` → dismiss overlay/cheatsheet) and a discoverable **`?` cheatsheet modal** that lists every binding, grouped.

### Why

- **Parity with operator expectations.** Every modern operator-facing tool (Linear, Raycast, GitHub, Slack, VS Code) now ships ⌘K. Procyon Park's dashboard is an operator tool; mouse-only navigation is friction-as-a-feature at a bad time.
- **Discovery.** The dashboard grew 5 panels, 2 filter bars, 3 tabs, and a scope toggle. A palette turns "what can I do here?" from a scan into a search.
- **Keyboard-native culture.** The codebase aesthetic is monospace and terminal-descended; a `risograph-printed index card` palette reinforces the identity instead of diluting it with glass-morphism drift.

---

## User Stories

### Navigation

1. **As an operator**, I want to press `⌘K` (or `Ctrl-K`) from anywhere on the dashboard and see a search field over the page, **so that** I can jump to any surface without clicking.
2. **As an operator**, I want to type `board` (or any fuzzy substring) and hit `Enter`, **so that** I land on the Board tab in one motion.
3. **As an operator**, I want to press `g` then `b` (vim-style leader chord), **so that** I can switch to Board without opening the palette.
4. **As an operator**, I want `g o`, `g b`, `g h` to go to Overview / Board / History respectively, **so that** tab navigation is 2 keystrokes.

### Finding things

5. **As an operator**, I want to type a workflow ID fragment in the palette, **so that** I can jump straight to that `.wf-card` in the Active Workflows list (page scrolls, card pulses).
6. **As an operator**, I want to type a work-item title fragment, **so that** I can jump to that card on the Board (auto-switches tab, scrolls, pulses).
7. **As an operator**, I want agents and worker identities surfaced in the palette, **so that** I can jump to that presence entry from anywhere.
8. **As an operator**, I want the last N notifications surfaced as results, **so that** I can re-read a recent alert without scrolling the feed.

### Acting on things

9. **As an operator**, I want a `Toggle scope: mine/team` action in the palette, **so that** I do not have to reach for the hero-bar radio.
10. **As an operator**, I want `Clear filters` actions (activity, board) in the palette, **so that** I can reset filter state quickly.
11. **As an operator**, I want `Focus filter` actions that jump caret into the relevant filter input for the active tab, **so that** `/` (or palette) immediately lets me type a filter.
12. **As an operator**, I want `Re-query history` as an action when I'm on the History tab, **so that** I can refresh without hunting for the button.
13. **As an operator**, I want `Copy viewer identity` and `Resync SSE state` as actions, **so that** less-common operations are still one palette away.

### Discoverability

14. **As a new user**, I want a subtle `?` affordance in the palette footer, **so that** I can discover all shortcuts without reading docs.
15. **As any user**, I want pressing `?` outside any input to open a cheatsheet modal listing every binding grouped by section (Navigation, Palette, Filters, Actions), **so that** the keyboard surface is self-documenting.
16. **As any user**, I want `Esc` to dismiss whichever overlay is topmost (cheatsheet > palette), **so that** I never feel trapped.

### Safety

17. **As any user typing in a filter input or search field**, I want single-key shortcuts (`g`, `/`, `?`) to be ignored, **so that** they do not steal keystrokes from real input.
18. **As any user**, I want `⌘K` / `Ctrl-K` to still work even while focused in an input, **so that** the palette is truly global.

---

## Acceptance Criteria

### A. Palette overlay

- **A1.** Pressing `⌘K` on macOS or `Ctrl-K` elsewhere from any non-palette context opens a palette overlay centered over the dashboard, regardless of focused element.
- **A2.** The overlay's visual is a single chunky index-card element:
  - thick (2–3px) border in `--rule-hi`
  - offset block drop shadow (`6px 6px 0 var(--terra-deep)`) matching existing `.panel--terra` language
  - `--surface` background with grain (reuse `--grain-light`/`--grain-dark` variables)
  - halftone `::before` corner flag consistent with `.panel`
  - monospace input using `--f-mono`, 16px, with a steady block-cursor character (CSS `caret-color` + a blinking `▊` pseudo-suffix when empty)
  - NO blur/backdrop-filter glass effect
- **A3.** The overlay has a dim `rgba(10,8,6,0.55)` scrim behind it that also closes the palette on click.
- **A4.** Tab-key and arrow-keys move selection; `Enter` activates; `Esc` closes (returns focus to previously focused element).
- **A5.** `⌘K` while the palette is open closes it (toggle behavior).
- **A6.** Opening the palette does **not** alter the underlying DOM state (scroll, filters, tab) until an action runs.

### B. Fuzzy matcher + result rows

- **B1.** Results are live-reordered on every keystroke (debounce ≤ 16ms; measurable under 2ms for 500 items on a modern laptop).
- **B2.** Fuzzy algorithm: subsequence match with bonus for (a) match at word boundary, (b) consecutive runs, (c) prefix match. Vanilla JS, no dependency.
- **B3.** Each row shows, left-to-right:
  1. a 4px accent color bar (left edge), type-colored: `tab=fog`, `workflow=cobalt`, `workitem-epic=terra`, `workitem-story=cobalt`, `workitem-task=moss`, `agent=terra`, `notification=` (by severity — `cobalt`/`sun`/`terra`/`rose`), `action=moss`
  2. a type tag in small caps (`.f-slab`, 9.5px, letter-spacing 0.14em, background matches accent)
  3. primary label (`.f-mono`, 12.5px, `--cream-hi`)
  4. dim subtitle (`.f-mono`, 10.5px, `--cream-dim`) e.g. workflow ID, repo path, timestamp
- **B4.** Selected row is rendered with a solid accent fill (not an outline) and `--cream-hi` text — NOT a lighter gray row. The block shadow does not travel with selection.
- **B5.** Empty-query state shows a curated default list (top: tab switches, then scope toggle, then last 5 active workflows, then `Open cheatsheet`).
- **B6.** No results → riso empty-state line (`.f-serif`, italic, `--fog`) in existing style.
- **B7.** Result limit: top 50 ranked matches (sufficient for perceived instant).

### C. Command registry

- **C1.** A single in-memory array of command objects, each with: `id`, `type`, `label`, `subtitle`, `accent`, `keywords`, `run()`.
- **C2.** Registry is refreshed (a) on palette open, (b) when SSE patches `#dashboard-workflows`, `#dashboard-workitems`, `#dashboard-presence`, `#dashboard-notifications`. Use the existing `handlePatchElements` hook: detect `root.id` already processed and extend to rebuild the corresponding command slice.
- **C3.** Static commands (always present):
  - `nav:overview` / `nav:board` / `nav:history`
  - `action:toggle-scope-mine` / `action:toggle-scope-team`
  - `action:clear-activity-filters` / `action:clear-board-filters`
  - `action:focus-activity-filter` / `action:focus-board-filter` / `action:focus-history-scope`
  - `action:history-query` (runs `histQuery()` if current tab is History; else switches then runs)
  - `action:copy-viewer-identity`
  - `action:resync-sse-state` (calls existing `resyncDashboardState()`)
  - `action:open-cheatsheet`
- **C4.** Dynamic commands are scraped from the DOM (source-of-truth stays the DOM so that SSE patches drive the palette):
  - `workflow:<id>` — from `.wf-card[data-wf-id]` (and fallback to `<b>` text if no data attr)
  - `workitem:<id>` — from `.wi-card` (id/type/title/repo)
  - `agent:<id>` — from `.presence-list li .presence-id`
  - `notif:<idx>` — from last 20 `#dashboard-notifications li.notif-item` (label = first text; subtitle = scope + severity + age)
- **C5.** `run()` for navigation commands calls existing `activateTab(name)`.
- **C6.** `run()` for `workflow:` / `workitem:` / `agent:` / `notif:` scrolls to the target element with `scrollIntoView({block: 'center'})` and briefly adds a `.pp-palette-pulse` class (`outline: 2px solid var(--sun); animation: palette-pulse 0.9s ease-out;`). If the target lives on another tab, the action first calls `activateTab(name)` then defers the scroll one microtask so the view is visible.

### D. Global keybinding layer

- **D1.** Implemented as a single `keydown` listener on `document`, in the capture phase for `⌘K`/`Ctrl-K` only (so it preempts inputs), in the bubble phase for everything else.
- **D2.** Leader chords: pressing `g` (when no input is focused and no palette is open) enters a "pending-g" state with a 1.2s timeout. Next key: `b`/`h`/`o` → activate tab; any other key or timeout → cancel.
- **D3.** While the pending-g state is active, a tiny riso `g_` indicator appears bottom-right (`position: fixed; bottom: 10px; right: 14px;`) so the user knows they are in a chord.
- **D4.** `/` (when no input focused) focuses the active tab's primary filter input:
  - Overview tab → `#filter-text`
  - Board tab → `#board-filter-text`
  - History tab → `#hist-scope`
- **D5.** `?` (when no input focused) opens the cheatsheet modal.
- **D6.** `Esc` closes, in order: cheatsheet > palette > blur active input. Does nothing if none apply.
- **D7.** "Input focused" test: `document.activeElement` is `INPUT`/`TEXTAREA`/`SELECT` or has `contenteditable`. `⌘K`/`Ctrl-K` and `Esc` are exceptions — always handled.
- **D8.** No shortcut fires when a modifier other than Shift is present (except `⌘K`/`Ctrl-K`), so browser shortcuts are preserved.
- **D9.** IME composition state (`e.isComposing`) is respected — shortcuts are ignored mid-composition.

### E. Cheatsheet modal

- **E1.** Opens on `?` (outside inputs) or on clicking the palette footer `?` affordance.
- **E2.** Same riso index-card visual as palette (bigger, max-width 640px).
- **E3.** Content is grouped: Navigation, Command Palette, Filters, Actions. Each row shows binding(s) in a rendered "key cap" chip (monospace, tight border, 2px offset shadow matching accent) next to a plain description.
- **E4.** Driven by a data array — when the command registry gains a binding, the cheatsheet gets it for free.
- **E5.** Closes on `Esc`, scrim click, or `?` toggle.

### F. Scope & interaction invariants

- **F1.** Opening the palette does **not** break SSE — listeners continue.
- **F2.** Palette respects existing `pp-scope-mine` / `pp-scope-team` body classes when enumerating dynamic commands: hidden cards (`display: none`) MUST NOT appear as palette results (so "mine" mode narrows results the same way it narrows the page).
- **F3.** All shortcuts and the palette are bypassed entirely when `document.body` has a new opt-out class `pp-no-shortcuts` (reserved for future embedded contexts).
- **F4.** No new network calls are required for the palette itself. Actions that call existing endpoints (history query, resync) reuse existing functions.

### G. Performance & quality

- **G1.** Palette open-to-interactive < 40ms on a mid-range laptop with 500 indexed commands.
- **G2.** Keystroke → re-render < 16ms for 500 commands.
- **G3.** Zero layout thrash: render the result list by replacing innerHTML on a detached fragment OR by reusing a fixed pool of 50 row elements and rewriting their content.
- **G4.** No framework introduced. No new fonts. No bundler. All additions live in the existing `<style>` and `<script>` blocks of `static/dashboard.html`, following its inline-module style.
- **G5.** Accessibility:
  - Palette root has `role="dialog"` + `aria-modal="true"` + `aria-label="Command palette"`.
  - Result list uses `role="listbox"`; rows `role="option"` with `aria-selected`.
  - Focus is trapped inside palette; restored on close.
  - Cheatsheet is also a `role="dialog"` with focus trap.

---

## Stories (breakdown for implementation)

### Story 1 — Palette overlay shell + fuzzy matcher

Build the inert UI scaffold: overlay DOM, CSS, open/close, input, result rendering, fuzzy matching algorithm, default-state list, empty-state. No commands wired yet beyond `nav:*` as a smoke test.

**Deliverables:** palette opens/closes on ⌘K/Esc; tab navigation works through palette; visuals match acceptance criteria A1–A5, B1–B7, G1–G5.

### Story 2 — Command registry + dynamic scraping

Introduce the command object model. Populate static commands (C3). Add DOM-scrapers for workflows, work items, agents, notifications (C4). Hook into `handlePatchElements` to rebuild the dynamic slice on SSE patches (C2). Implement `run()` for all command types including cross-tab scroll-and-pulse (C5, C6). Honor hidden cards under `pp-scope-mine` (F2).

**Deliverables:** every visible card reachable via palette with one-keystroke-precise match; SSE updates keep the index fresh without flicker.

### Story 3 — Global keybinding layer

Implement the single `document` keydown listener with capture/bubble split, leader-chord state machine (`g` → `b`/`h`/`o`), `/` to focus active-panel filter, `?` to open cheatsheet, `Esc` dismiss ladder, input-focus bypass, IME guard, and the fixed-position pending-chord indicator.

**Deliverables:** all keybindings from D1–D9 observable; no regressions in existing input behavior; scope toggle, filter inputs, history inputs all still type normally.

### Story 4 — Cheatsheet modal + discoverability affordance

Render a `?` chip in the palette footer, build the cheatsheet modal (E1–E5), and source its contents from the same data table the keybinding layer and command registry use so nothing drifts.

**Deliverables:** `?` anywhere opens a complete, grouped, keyboard-navigable cheatsheet in-style.

---

## Technical Context

### Files

- **`static/dashboard.html`** (single file, 1424 lines) — target of all changes. Contains:
  - `<style>` block with riso token set (`--terra`, `--moss`, `--cobalt`, `--rose`, `--fog`, `--sun`, `--cream*`, `--rule*`), card patterns (`.panel`, `.wf-card`, `.wi-card`), and animation vocab (`press-in`).
  - `<script>` block with:
    - `activateTab(name)` — tab router; also writes `location.hash` via `history.replaceState`.
    - `handlePatchElements(e)` — Datastar SSE patch handler; replaces `#dashboard-*` roots wholesale. Must be extended (not wrapped) so the palette rebuilds dynamic commands when `root.id` matches one of the dashboard sections.
    - `resyncDashboardState()` — parallel `/api/scan` fan-out; reusable as a palette action.
    - `applyNotifFilter` / `applyBoardFilter` / `histQuery` — existing action endpoints to wrap.
    - Scope toggle (`setScope`, `applyScope`) + viewer identity (`ppViewerIdentity` from `<meta name="pp-viewer">` or `/api/whoami`).
- **`src/api/DashboardSSE.mag`** — server-side renderer of `.wf-card` / `.wi-card` / presence / notifications. Confirms that cards already carry `data-launched-by`, `data-executed-by`, and `data-repo` attributes we can scrape. If additional attributes are needed (e.g. `data-wf-id`, `data-wi-id`, `data-wi-type`), those are a minor additive change here.

### Data Flow

Server emits Datastar `patch-elements` events over `/api/sse/dashboard`. Each event replaces an entire `#dashboard-<section>` subtree. The palette's dynamic command index is therefore always derived from the current DOM post-patch — we never need a second data source.

### Existing Patterns to Match

- **Riso aesthetic language.** Use existing CSS vars directly; never introduce new hex codes. Borders use `--rule`/`--rule-hi`; shadows are chunky `Xpx Ypx 0 <color>`; halftone corners via `radial-gradient(... 1.1px, transparent 1.5px)` at `background-size: 5px 5px`.
- **Monospace-first typography.** All palette body text in `--f-mono`; type tags and keycaps in `--f-slab` with `text-transform: uppercase` and wide letter-spacing.
- **Animation vocabulary.** Reuse `press-in` keyframes for first mount; add a sibling `palette-pulse` keyframe consistent with the existing `pulse`/`qb-pulse` pattern.
- **No framework, no build step.** Everything stays inline in `dashboard.html`. Precedent: every existing interaction is plain DOM + fetch + EventSource.

### State Persistence

- Palette recent-selection history in `localStorage` under `pp.paletteRecents` (FIFO, cap 20). Used to boost recently-used commands to the top of the empty-query default list.
- Cheatsheet "seen" flag under `pp.cheatsheetSeen` — first open of palette without it triggers a one-time 3s tooltip pointing at the `?` footer affordance.

---

## Open Questions

1. **Work-item deep links.** Clicking a work-item result currently scroll-and-pulses the card. Should it also open a detail drawer? Out of scope for this epic — flag as follow-up once drawers exist.
2. **Spawn actions.** The brief mentions "spawn agent" as a palette action. Procyon Park currently spawns via `pp` CLI (not via dashboard HTTP). Options:
   - (a) defer all spawn commands to a follow-up epic that first adds `/api/spawn` (scope creep);
   - (b) ship a `Copy pp command to spawn <role> in <workflow>` action that writes a ready-to-run CLI string to clipboard (zero-risk, keyboard-first);
   - (c) ship nothing under "spawn" for now and list it explicitly in the follow-ups.
   Recommendation: **(b)** — preserves palette-as-launchpad feel without coupling to server work.
3. **History results in palette.** History rows are server-queried on demand, not streamed. Should typed history queries (e.g. `/hist workflow:foo`) route through the palette? Recommend: **no** for v1 — keep palette results confined to what's already in the DOM. Add a `Run history query…` action that switches to History and focuses `#hist-scope`.
4. **Mobile / narrow viewport.** Palette sized for desktop. At < 720px, fall back to a full-width sheet taking 100% width minus 16px margin? Recommend yes; acceptance criteria A2 already implies desktop geometry, add a `@media (max-width: 720px)` breakpoint.
5. **Keyboard remapping.** Some users may want `g` as a literal key in an input-less context (e.g. muscle memory from other tools). Provide a settings toggle? Recommend: **no** for v1. Document in cheatsheet that chords are ignored in inputs — that covers the 95% case.
6. **Result cap beyond 500 work items.** At very large backlogs, DOM scraping is cheap but ranking 2000 items per keystroke may exceed 16ms. Add an early-exit once the top-50 unambiguously fill? Design the matcher to allow this; flag as perf follow-up if it bites.
7. **`data-wf-id` / `data-wi-id` availability.** Server currently emits `data-launched-by`/`data-executed-by`/`data-repo`. Confirm (or add) stable id attributes on `.wf-card` and `.wi-card` in `DashboardSSE.mag` — would be a tiny additive change required by Story 2.

---

## Non-Goals

- Adding a new backend endpoint.
- Introducing any JS framework, bundler, or external dependency.
- Changing the existing tab routes or SSE contract.
- Full-text search into workflow bodies / task payloads (palette matches labels + subtitles only).
- Server-rendered or SSR'd palette results.
- Themeability / alternate keymaps — one keymap ships.
