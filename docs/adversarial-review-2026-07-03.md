# Adversarial Review — 2026-07-03

Three parallel adversarial reviews (performance, UX, correctness) of Procyon
Park, followed by hands-on verification of the highest-severity items. Findings
are grouped by axis; each carries a **status** that is updated as fixes land.

Baseline before any changes: `mag test` green except **8 workflow-refactor**
(WR15c/d/e are pre-existing rot) and **10 multiplayer** failures — both
pre-existing on a clean `main`. Use this as the regression yardstick.

Status legend: ☐ open · ⚙ in progress · ✅ fixed · ⚠ needs user decision · ✗ won't-fix/false-positive

---

## Correctness & Robustness

### C1 ✅ CRITICAL — signature verify-cache bypasses method/path/body binding
`src/api/SignatureVerifier.mag:38-40, 79-83`. The verify cache is keyed only on
`actorHeader|sigHeader` and a cache hit returns the cached `ActorContext` *before*
the canonical (`method||path||ts||sha256(body)`) is ever computed. An on-path
observer who captures one valid signed request can replay the same `(actor,sig)`
against **any other endpoint** (e.g. turn a signed `GET /health` into a signed
`POST /workflow/cancel`) within the skew window (default 120s). **Verified real.**
Fix: bind the cache key to the full canonical so a replayed sig against a
different request misses the cache and is re-verified (and fails).

### C2 ✅ HIGH — Claude harness failure/timeout swallowed, reported as task success
`src/harness/ClaudeHarness.mag:198-209` + `src/worker/WorkerAgent.mag:428-441`.
On a non-zero/timeout (exit 124) exit, `run` only `println`s stderr and returns
normally without setting `failureReason`; WorkerAgent then marks the task
`completed`. Token advances to review/merge over an empty/partial worktree.
Fix: set `failureReason` on non-success exit (distinguish 124 timeout).

### C3 ✅ HIGH — missing `terminal_places` nil-guard stalls the whole tick
`src/dispatcher/WorkflowEngine.mag:242-246`. `at: 'terminal_places'` has no
`ifAbsent:`; a template lacking it yields nil → `nil includes:` DNU. `advance`
iterates all workflows with no per-instance guard, so one bad template throws
every tick and no workflow advances. Fix: `ifAbsent: [#()]` + wrap the
per-instance advance body in `on: Exception do:`.

### C4 ✅ MED-HIGH — `queryParam:` returns `''` so `isNil` default-guards never fire
`src/api/Server.mag:1824-1825, 1943-1946`. `since`/`wait` guarded with `isNil`,
but `queryParam:` returns `''` for absent params, so the default branch is dead
and `'' asInteger` runs → nil comparisons downstream (`nil > 0`, `>= nil`) panic
the handler. Fix: guard with `(x isNil or: [x isEmpty])`.

### C5 ✅ MED — pipeline merge always records `files_changed: 0`
`src/dispatcher/actions/MergeWorktreeAction.mag:59, 71`. The pipeline branch
fast-forwards `main` first, then counts `main...featureBranch` — post-merge the
diff is empty → 0. CaseBuilder scores `files_changed = 0` as a 200-pt noop
penalty, so real pipeline work is scored as a no-op. Fix: count before the merge.

### C6 ⚠ MED — SSE verify passes hex where header path passes raw bytes
`src/api/SignatureVerifier.mag:142` (`fromHex`) vs `:222` (raw). Both use
`verify:data:pub:`; at most one encoding is right, so one path is systematically
broken. Needs confirmation of the primitive's contract before touching — risk of
breaking working auth. **Deferred for user decision.**

### C7 ✅ LOW-MED — spawn `_depth` cap bypassed on the wave-dispatch path
`src/dispatcher/actions/DispatchWavesAction.mag:208, 271`. Child params are built
from scratch and never thread `_depth`; only `SpawnWorkflowAction` increments it,
so wave-dispatched children always start at depth 0 → the max-10 guard can't trip
on epic→waves→epic recursion. Fix: thread + increment `_depth` in child params.

### C8 ✅ LOW — dashboard notification timestamps always blank (SmallInteger)
`src/api/DashboardSSE.mag:1367`. `createdAt isKindOf: Integer` is false for
JSON-decoded `SmallInteger`, so `ts` is always `''`. Fix: check
`SmallInteger`/`BigInteger` like `storyWaveKey:` already does.

### C9 ✅ LOW (partial) — `MergeWorktreeAction` unguarded payload reads + clean-tree TOCTOU
`src/dispatcher/actions/MergeWorktreeAction.mag:30-33, 43-52`. Bare `at:` reads on
worktree-signal fields (nil → DNU) and a check-then-checkout race on the primary
tree. **Fixed the crash risk** (`ifAbsent: ['']` on all four reads). The
clean-tree TOCTOU (narrow the check→checkout window or land in a dedicated
worktree) is a larger structural change left open.

---

## User Experience

### U1 ✅ CRITICAL — `pp gc` / `pp worktree clean` fail-open: server down ⇒ wipe all worktrees
`src/cli/Repo.mag:351-371`. Empty response, JSON-parse error, and network error
all set `shouldRemove := true`, so a server hiccup `rm -rf`s **every** worktree
incl. in-flight ones, and force-deletes branches. Fix: on any probe
failure/ambiguity, treat the worktree as **live** (skip); only remove on a
definitive `completed`/`not_found`.

### U2 ✅ HIGH — `pp help` hides whole subsystems
`src/Main.mag:99-154`. `printUsage` omits `mission`, `bbs`, `doctrine`, `worker`,
`watch`/`unwatch`, `whoami`, and the `identity use|invite|accept` subcommands —
all live. Fix: list every registered command.

### U3 ✅ HIGH — `workflow cancel` help omits the *required* `--reason`
`src/Main.mag:121` + `src/cli/PP.mag:460`. Help shows it optional; impl hard-requires
non-empty `--reason`. Fix: correct both usage strings.

### U4 ☐ HIGH — inconsistent exit codes (most failures exit 0)
`src/Main.mag:90-92` and arg-guards throughout print usage then `^self` (exit 0),
while CliBBS sets exit codes. Breaks scripting/CI. Fix: standardize (usage=2,
not-found=1). *Broad; staged.*

### U5 ✅ HIGH (partial) — `workflow wait` silent for up to an hour + curl quoting + no timeout validation
`src/cli/WorkflowCommands.mag:279-319`. **Fixed**: startup banner + status-on-change
heartbeat, and `--timeout` is now validated (non-numeric/≤0 errors instead of
silently becoming an instant timeout). The curl-vs-HTTP-client quoting divergence
(a `'` in the id) is left as-is — workflow ids are server-generated, low risk.

### U6 ✅ MED — `pp dashboard` macOS-only, fails silently on Linux
`src/cli/SystemCommands.mag:359-365`. **Fixed**: `open` → `xdg-open` → print-URL
fallback chain, so it is no longer a silent no-op off macOS.

### U7 ✅ MED — `pp log` follow-mode spams errors every 2s, no banner
`src/cli/SystemCommands.mag:262-288`. **Fixed**: "Tailing… (Ctrl-C to stop)" banner
+ identical-error coalescing (print once, then every 30th repeat).

### U8 ☐ MED — no consistent `--json`
Only `pp bbs list` and `pp usage` support it. Fix: uniform `--json` on read/list.

### U9 ☐ MED — MissionCLI reads shell to curl with unescaped interpolation
`src/cli/MissionCLI.mag:89,100,111,386,519,543`. Fix: route through CLIBase HTTP.

### U10 ✅ LOW-MED — inconsistent "unknown subcommand" (worker/identity print no usage)
`src/cli/PP.mag:419, 439`. Fix: always reprint the group's usage.

### U11 ✅ LOW — `flagValue:` silently drops a flag missing its value
`src/cli/CLIBase.mag:354-361`. `--role` as last token → nil, reported as "required"
even though typed. Fix: distinguish absent vs value-missing, error on the latter.

### U12 ☐ LOW — divergent/dead help text (Main vs PP printUsage)
Two usage blocks, already disagree. Fix: delete/​delegate the dead `PP>>printUsage`.

---

## Performance & Scalability

### P1 ☐ HIGH — `renderWorkflowsHtml:` is O(workflows × tasks/tokens) per snapshot
`src/api/DashboardSSE.mag:856-915`. Per-workflow helpers each full-scan `allTasks`
/`allTokens`; snapshot cache misses on every write during a swarm. Fix: bucket
tasks/tokens by `workflow_instance` once, O(1) lookup per workflow.

### P2 ✅ HIGH (real-world MED) — `maybeRollupMissions` re-scans workitem/workflow/case per mission
`src/dispatcher/Dispatcher.mag:844-921`. **Fixed**: the sweep now scans
workitem/workflow/case once and threads the snapshots into
`missionAllItemsDone:workitems:` / `missionOutcomeScore:workflows:cases:`
(1-arg back-compat versions retained for the direct test callers). Note the
sweep is throttled to ~5 min and M (in-progress missions) is small, so the
real-world payoff is smaller than the raw O(M×N) suggests.

### P3 ✅ HIGH — `advanceInstance:` full `scanAll: 'task'` per running workflow
`src/dispatcher/WorkflowEngine.mag:263`. Reintroduces the O(W×tasks) the method
header claims to have removed. Fix: scan tasks once in `advance`, thread it in.

### P4 ☐ MED — per-tick full scans of unbounded `case`/`task` for a few unstamped rows
`src/dispatcher/WorkflowEngine.mag:977, 1026`. Fix: indexed stamp column or
`changeCount` gate.

### P5 ☐ MED — `notification` tuples grow unbounded, re-scanned in full per poll
`BBS.mag:896` (never pruned); scanned at `Server.mag:1830,1952,2888` +
`DashboardSSE.mag:184`. `since` filter applied after full decode. Fix: prune in
housekeep; push `since` into the store's indexed `created_at`.

### P6 ✅ MED (partial) — idempotency guards use full `scanAll … detect:` instead of indexed lookup
`src/dispatcher/WorkflowEngine.mag:795, 855, 908`. **Fixed `dispatchArchivist:`**
(the highest-frequency one — fires on every terminal workflow): it writes at the
deterministic id `<instanceId>:task:archivist`, so the guard is now an O(1)
`rdp:` on that id. `dispatchStrategist:` (random id + status filter) and
`findArchivistEnrichment:` (matches by payload, not id) have no clean `rdp:`
replacement and are left as-is.

### P7 ☐ MED — every BBS mutation does a synchronous JSONL append (extra syscall/write)
`src/bbs/BBS.mag:1120-1139`. Fix: persistent handle / batch / opt-out for token writes.

### P8 ✅ LOW-MED (partial) — `copyWith:`-in-loop O(n²) accumulators in SSE render path
`src/api/DashboardSSE.mag:501, 1420`. **Fixed** the two higher-cardinality,
cleanly-local spots (workitems bucket + `watchWorkflowsFor:` accumulator) by
switching to `ArrayList add:`. The mission partition (573-578) feeds `sort:` on
a low-cardinality set and was left as-is to avoid touching the sort path.

---

## Outcome (2026-07-03)

**Fixed & verified (build green; deterministic suites unchanged — workflow-refactor
holds at 8 pre-existing, all mission/doctrine/gitops/dispatch suites at 0):**
C1, C2, C3, C4, C5, C7, C8, C9(crash-guard), U1, U2, U3, U5(heartbeat+validation),
U6, U7, U10, U11, U12, P2, P3, P6(archivist), P8(2 of 3 spots).

Test-suite note: the **multiplayer suite is flaky** — its failure count swings
8→14 across identical runs and `WRGMain` crashes with a *different* error each
time (shared-BBS-state non-determinism). It is NOT a regression signal; use
**workflow-refactor (stable at 8)** and the deterministic dispatcher/mission/
doctrine suites as the yardstick. Worth a separate test-isolation pass.

**Left open — needs a user decision (did not touch):**
- **C6** — SSE `verify:data:pub:` passes hex where the header path passes raw
  bytes. One path is broken, but fixing the wrong one breaks working auth; needs
  confirmation of the primitive's contract + a round-trip test.
- **U4** — standardize exit codes (usage=2, not-found=1). Broad; changes the
  scripting contract for every command that currently exits 0 on error.
- **P5** — prune/retire `notification` tuples. Gated on a retention-policy
  decision (already tracked as the deferred reaper TODO).

**Left open — larger/riskier refactors (staged, noted, not blocking):**
- **P1** — `renderWorkflowsHtml:` O(W×tasks/tokens): bucket by
  `workflow_instance` once. Highest-value remaining perf, but a broadcast-path
  rewrite with thin test coverage — deferred rather than risk a silent dashboard
  break.
- **P4** — per-tick unbounded `case`/`task` scans for unstamped rows (indexed
  stamp column or `changeCount` gate).
- **P7** — synchronous JSONL history append per BBS write (persistent handle /
  batch).
- **U8** — uniform `--json` on read/list commands (feature work).
- **U9** — MissionCLI curl-read shell-interpolation escaping.
- **C9 (TOCTOU half)** — narrow/eliminate the primary-tree check→checkout window
  in the pipeline land.
</content>
</invoke>
