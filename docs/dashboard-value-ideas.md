# Procyon Park Web Dashboard — Value-Add Ideas

Scout mission: brainstorm 30 ideas to increase the value of the procyon-park
web dashboard (`static/dashboard.html`, SSE-driven by `src/api/DashboardSSE.mag`
and `src/api/Server.mag`). The dashboard today has three tabs — **Overview**,
**Board**, **History** — showing active workflows, recent completions, scope
violations, presence, an activity feed with scope/workflow/severity filters,
and a work-items board.

## 30 Ideas

### Observability & situational awareness
1. **Agent timeline view** — per-agent swim lane showing spawn, task, observations, dismiss events across time.
2. **Workflow DAG visualization** — render each active workflow as a node graph with current step highlighted.
3. **Heatmap of scope violations** — files/packages most frequently tripped, aggregated over a time window.
4. **Observation browser** — searchable, faceted view over PUDL observations (kind, scope, source agent).
5. **Fact vs. observation badges** — visually distinguish promoted facts from raw observations in the feed.
6. **Tuplespace inspector** — live read-only view of BBS tuples with a filter/search box.
7. **Cost/tokens per workflow** — roll up token usage and dollar cost per story/workflow/agent.
8. **Latency histograms** — p50/p95/p99 for agent task duration, SSE round-trips, dispatcher turnaround.
9. **Role utilization chart** — stacked bar of which roles are busy vs. idle over the last hour.
10. **Stale-worktree detector panel** — list of worktrees older than N minutes with no activity.

### Operator control
11. **One-click dismiss/kill** — dismiss a stuck agent or cancel a workflow step from the UI.
12. **Re-dispatch button** — rerun a failed story with a tweaked prompt without touching the CLI.
13. **Pause/resume workflows** — operator can hold a workflow at the next checkpoint.
14. **Inline verdict override** — flip a story verdict from pass→fix (or vice versa) with reason field.
15. **Notification ack/snooze** — mark urgent notifications as acknowledged so they stop blinking.
16. **Quick-spawn composer** — form to launch a scout/planner/coder with role + prompt + worktree policy.
17. **Broadcast message to agents** — push a tuple into BBS addressed to all active agents.
18. **Permission prompts surface** — show and approve pending `pp` permission prompts in-browser.

### Collaboration & multiplayer
19. **Multi-user presence cursors** — show which human operators are looking at which tab/story.
20. **Comment threads on work items** — humans annotate a story/observation for the next agent to read.
21. **Share-link for a story** — deep-linkable URL that opens the dashboard scoped to one workflow.
22. **@mention human** — agent notification can tag a human; dashboard raises a visible alert.
23. **Read-receipts on notifications** — track which operators have seen an urgent event.

### Insight & retros
24. **Daily digest page** — auto-generated summary: stories shipped, verdicts, top obstacles, hotspots.
25. **Agent scorecard** — per-role success rate, average fix-loops, mean time to dismiss.
26. **Bug/obstacle trend chart** — observation counts by kind over the last 7/30 days.
27. **Workflow replay** — scrub timeline of a completed workflow step by step.
28. **Diff preview inline** — for completed coder stories, show the resulting `git diff` in the Board card.

### Ergonomics
29. **Keyboard shortcuts + command palette** — `Cmd-K` to jump to story, filter scope, open agent.
30. **Dark/light + density toggle** — compact vs. comfortable rows; persisted per user.

---

## Top 10 (ranked)

Ranking is by **(leverage − effort)**: high leverage + low effort floats to
the top. Effort uses T-shirt sizes: **S** ≈ <1 day, **M** ≈ 2–4 days,
**L** ≈ 1+ week. Leverage is 1–5 (5 = transformative).

| # | Idea | Effort | Leverage | Score | Why it matters | Implementation sketch |
|---|------|:------:|:--------:|:-----:|----------------|-----------------------|
| 1 | **Inline diff preview on Board cards** (#28) | S | 5 | ★★★★★ | Closes the loop between "story done" and "did it do the right thing?" without leaving the dashboard. | Extend `DashboardSSE` work-item payload with `git diff --stat` + truncated patch; render under card with `<details>`. |
| 2 | **Keyboard shortcuts + command palette** (#29) | S | 4 | ★★★★ | Power-user speed boost; makes operating many workflows tractable. | Vanilla JS palette overlay indexing tabs, stories, agents; bind `Cmd-K`, `g b`, `g h`, `/` for search. |
| 3 | **Notification ack/snooze** (#15) | S | 4 | ★★★★ | Stops urgent-alert fatigue; operators trust the signal. | Add `ack` field to notification tuple, POST `/api/notifications/:id/ack`, filter acked from live list. |
| 4 | **Quick-spawn composer** (#16) | M | 5 | ★★★★ | Removes CLI barrier for humans starting work; biggest funnel-widener. | HTML form → `POST /api/spawn` → dispatcher enqueues role+prompt+worktree-policy; reuse existing spawn path. |
| 5 | **Observation browser** (#4) | M | 5 | ★★★★ | PUDL observations are the project's memory; currently only visible via CLI. | New `/observations` tab with faceted filters (kind, scope, source); backed by `nous` JSON endpoint or direct read. |
| 6 | **Workflow DAG visualization** (#2) | M | 5 | ★★★★ | Makes multi-step workflows legible; replaces mental model with a picture. | Emit workflow graph JSON on SSE; render with a small force-directed lib (or static SVG per template). |
| 7 | **Daily digest page** (#24) | S | 4 | ★★★★ | Humans returning to the system get oriented in seconds. | Nightly Maggie job aggregates BBS + git log into a static HTML snapshot linked from the nav. |
| 8 | **One-click dismiss/kill** (#11) | S | 4 | ★★★★ | Essential escape hatch when an agent hangs; currently requires CLI. | Button on Agent row → `POST /api/agents/:id/dismiss`; reuse `pp dismiss` plumbing. |
| 9 | **Cost/tokens per workflow** (#7) | M | 4 | ★★★ | Turns "is this worth it" from anecdote into evidence. | Have harness emit a `usage` tuple on each turn; roll up in `DashboardSSE`; column on Board. |
| 10 | **Stale-worktree detector** (#10) | S | 3 | ★★★ | Low-cost hygiene win; prevents worktree sprawl eating disk. | Scan `~/.pp/worktrees` mtime on a ticker, publish list over SSE, panel with "prune" action. |

### How to use this ranking

- **Ship first (this week):** #1, #2, #3, #8 — all **S** effort, ≥4 leverage.
- **Ship next (this sprint):** #4, #5, #6 — **M** effort but each unlocks new user personas (humans spawning work, browsing memory, understanding flows).
- **Ship after instrumentation exists:** #7, #9 — require richer event payloads; worth batching together.
- **Good hygiene filler:** #10 — do when on-call, no deep thinking required.

## Recommendations

- Treat the dashboard SSE payload as a versioned protocol; many ideas above
  (diff preview, DAG, cost rollup) want new fields. A single structural pass
  in `src/api/DashboardSSE.mag` to add `v2` envelopes would de-risk 4–5 of
  the top-10.
- The Board tab is the highest-traffic surface; prioritize improvements there
  (#1, #8, #9) before expanding new tabs (#5, #6).
- A `command palette` (#2) and `quick-spawn composer` (#4) together turn the
  dashboard from a passive monitor into an active control plane — the single
  biggest qualitative jump available.
