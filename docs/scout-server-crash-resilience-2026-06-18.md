# Crash diagnosis: pp serve dies under harness spawn storm

Date: 2026-06-18
Author: main-loop diagnosis (the scout could not run — the server was too
unstable to dispatch it; this was reconstructed directly from the crash logs
and live tuplespace state).
Scope: `procyon-park:src/dispatcher` (dispatch) + `src/harness` (spawn) + `src/api` (resilience)

## TL;DR

`pp serve` crashed **twice**, silently (no Go panic, no stack in the log — the
log just ends mid-line while spawning a harness). Cause is **resource
exhaustion from a harness spawn storm**, not a code panic:

- Many workflows terminated at once (two scouts, a story, several verification
  scout-missions). Each terminal workflow dispatches a **fire-and-forget
  archivist** task (the AAR learning loop) with **no concurrency cap**.
- Simultaneously, a completed story (`story-1781785355-14136`,
  `pp read --all`) left **orphaned, duplicated** review/fix task tuples — two
  tuples sharing the identity `story-1781785355-14136:task:review` — that the
  scheduler kept trying to dispatch.
- Net: a burst of concurrent `claude` child processes (each heavyweight: PTY +
  model session). The log shows the same review task dispatched 3× in a row
  plus fixer + 2 archivists. The OS killed the process (OOM / process-or-fd
  limit) → silent death.
- It **crash-looped**: each restart reloaded the same durable workflow/task
  state and re-spawned the same storm.

Stabilized by `pp bbs rm task …` on the orphaned tuples. After clearing them:
Active 0, Pending 0, uptime steady. No further crashes.

## Evidence

- Crash logs `/tmp/ppserve4.log`, `/tmp/ppserve5.log`: both end abruptly with
  `Starting Claude harness for … task …` lines — no `panic`, no `goroutine`,
  no `fatal`. Silent death = external kill (OOM/limit), not an in-VM panic.
- `ppserve5.log` tail: `story-1781785355-14136:task:review` dispatched three
  consecutive times + fixer + two archivists — the spawn storm in the act.
- Live state at restart: 5 active/dispatched tasks for *completed* workflows,
  including the duplicate-identity review pair.
- Prior precedent: CHANGELOG already records pp serve drifting to ~32 GB RSS
  (dashboard `tokenCacheLoop`); the box runs hot, so a spawn burst tips it over.

## Root issues (ranked)

1. **No cap on concurrent harness spawns.** `Slots: N/8` is displayed but did
   not prevent 6+ concurrent `claude` spawns. Verify the scheduler enforces a
   hard concurrency ceiling on harness creation; queue beyond it. (src/dispatcher
   scheduler dispatch path + src/harness spawn.) HIGH impact, S effort.
2. **Archivist-per-workflow is unbounded.** Every terminal workflow enqueues an
   archivist; a batch of terminations = a batch of agents. Cap archivists
   (single-flight / small pool), or make AAR enrichment in-process/cheap rather
   than a full agent. Ties to the perf scout's case-growth findings. HIGH, M.
3. **Duplicate task tuples under one identity.** Two
   `…14136:task:review` tuples existed — the dispatch/dedup path can write a
   second task for the same (category,scope,identity). Enforce upsert-by-key for
   task dispatch. HIGH, S.
4. **Orphan tasks survive workflow completion.** A workflow at terminal place
   `done` still had pending/dispatched review/fix tasks. Completion/failure
   should sweep the workflow's non-terminal tasks. MED, S. (Overlaps the
   dashboard scout's "no workflow-level reaper" finding.)
5. **No crash-resilience / last-words logging.** A silent death leaves no
   trace. Add: a top-level recover around each `[block] fork` body that logs and
   keeps the process alive where safe; a periodic heartbeat + memory line so the
   next OOM is visible; consider a soft memory watchdog that sheds load
   (pauses new harness spawns) above a threshold. MED, M.

## Recommended sequence

1. Hard concurrency cap on harness spawns (#1) — stops the storm class outright.
2. Upsert-by-key for task dispatch (#3) + sweep orphan tasks on completion (#4).
3. Cap/replace archivist-per-workflow (#2) — also fixes the perf bloat.
4. Resilience + crash-cause logging (#5) — so the next failure is diagnosable.

## Immediate state

Storm cleared manually (orphan tasks removed). Server stable. The two stale
"zombie" workflows from the dashboard scout (`story-lite-…13342` ~48d,
`…13506` cancelled) still sit `running` and should be reaped/gc'd per that doc.
