# Changelog

All notable changes to this project are documented in this file.

The format is based on Keep a Changelog, and this project adheres to
Semantic Versioning.

## [Unreleased]

### Security
- **Request-signature verify cache no longer bypasses method/path/body binding.**
  The ed25519 verify cache was keyed only on `actor|signature`, and a cache hit
  returned the cached identity *before* the canonical request was reconstructed.
  An on-path observer who captured one valid signed request could therefore
  replay that `(actor, signature)` pair against *any other* endpoint (e.g. turn a
  signed `GET /health` into a signed `POST /workflow/cancel`) for the length of
  the skew window. The cache key now includes the full canonical
  (`method‖path‖ts‖sha256(body)`), so a replayed signature against a different
  request misses the cache and fails re-verification. (adversarial-review C1)

### Added
- **Merge self-heal validated end-to-end by a live mission run.** (2026-07-01)
- **Story integrates now self-heal too — parallel wave-mates no longer fail on a
  shared-file clash.** The per-story impl→feature merge previously fail-stopped on
  any conflict ("human intervention required"), which bit parallel wave-mates that
  each appended to `CHANGELOG.md` even when their code was otherwise orthogonal.
  The `story` template now runs the same `sync → land / resolve → re_sync`
  self-heal as the full pipeline: before landing, it syncs the parent (feature)
  branch into the story's branch in place; a clean sync fast-forwards, a
  conflicting one dispatches a `resolver` agent to resolve and commit, bounded by
  the same resolver-pass cap before it fails for a human. The `resolver` role and
  `sync-worktree` action are now shared across both merge boundaries (the action
  takes a `sync_from` direction: `main` for the pipeline land, `parent` for the
  story integrate).
- **Full-pipeline landings now self-heal when `main` moves underneath a run.**
  Before merging a feature branch to `main`, the pipeline first syncs `main`
  *into* the feature branch in a dedicated, tree-safe worktree. A non-overlapping
  advance lands automatically as a fast-forward. An overlapping (conflicting)
  advance no longer stops the run for a human: a new **resolver** agent is
  dispatched into a worktree with the merge in progress, resolves the conflict on
  the branch, commits, and the pipeline re-syncs and lands. After a bounded number
  of resolver passes it still fails cleanly for human intervention, so the safety
  net is preserved. This fixes the mission-pipeline conflict where spec tests
  landing on `main` mid-run clashed with the impl branch rewriting the same file.

### Fixed
- **`pp gc` / `pp worktree clean` no longer wipe worktrees when the server is
  unreachable.** The status probe treated an empty response, a JSON-parse error,
  *and* a network error all as "workflow gone → remove", so a single server
  hiccup would `rm -rf` every worktree — including in-flight agent worktrees —
  and force-delete their branches. It now fails safe: a worktree is removed only
  on a definitive `completed`/`not_found`, and every ambiguous probe prints a
  `skip` line and keeps the tree. (adversarial-review U1)
- **A timed-out or crashed agent no longer reports its task as completed.** The
  Claude harness swallowed a non-zero/`timeout` (exit 124) process exit — it only
  printed stderr — so `WorkerAgent` saw no failure and marked the task done,
  advancing the workflow token over an empty/partial worktree. The harness now
  records a `harness-timeout` / `harness-exit-N` failure reason. (C2)
- **A template missing `terminal_places` no longer stalls every workflow.** The
  engine read `terminal_places` with no default and iterated all instances with
  no per-instance isolation, so one malformed (e.g. hot-reloaded) template threw
  on every tick and no workflow advanced. The read now defaults to empty and each
  instance's advance is exception-isolated. (C3)
- **Notification long-poll/stream handles an absent `since`/`wait` correctly.**
  The guards checked `isNil`, but the query-param accessor returns `''` for a
  missing param, so the defaults were dead and a missing `wait` could crash the
  handler. (C4)
- **Pipeline merges record the real `files_changed` (was always `0`).** The
  pipeline branch counted the diff *after* fast-forwarding `main`, when the
  three-dot range is empty; case scoring consequently penalised real pipeline
  work as a no-op. It now counts before the merge. (C5)
- **The spawn-depth recursion cap now applies to wave-dispatched children.**
  `dispatch-waves` built child params from scratch without threading `_depth`, so
  every wave child started at depth 0 and the max-10 guard could never trip on
  epic→waves→epic graphs. (C7)
- Dashboard notification timestamps render again (they were always blank because
  the epoch was checked against `Integer`, which JSON-decoded `SmallInteger`s are
  not). (C8)
- `merge-worktree` no longer crashes on a worktree signal missing a field — the
  `repo_path`/`feature_branch`/`branch`/`workdir` reads are now guarded. (C9)
- `pp dashboard` works off macOS: it falls back to `xdg-open`, then to printing
  the URL, instead of silently doing nothing. (U6)
- `pp log` (follow mode) prints a "Tailing… (Ctrl-C to stop)" banner and
  coalesces a repeating connection error instead of spamming it every 2 s. (U7)
- `pp workflow wait` prints a heartbeat when the status changes (instead of
  hanging silently for up to an hour) and rejects a non-numeric `--timeout`
  instead of treating it as an instant timeout. (U5)
- **Batched wave dispatch now actually runs and rolls up.** A wave whose stories
  shared a `batch` tag was silently broken: `dispatch-waves` built the child
  workflow's params but never instantiated it, so no batched workflow was spawned,
  it was never tracked for wave completion, and its stories were never marked
  done — a batched multi-story mission would hang forever. The batched branch now
  spawns the bundled child, tracks it, and threads the full list of story ids
  (`workitems`), which `completeWorkflow` marks done so the epic and mission roll
  up just like an unbatched dispatch. (Unbatched breakdowns were already fixed
  separately; default mission breakdowns remain unbatched.)
- Dashboard Missions panel: **`done` (completed) missions no longer show in the
  active "Other missions" list.** Like archived missions, they are now filed into
  their own collapsed **Completed** section (with their all-done epic/story tree
  available on expand). The empty-state guard also accounts for a done-only scope.
- Dashboard **approve/reject/cancel** now work from the browser at all. The
  loopback gate read the request authority via `req header: 'Host'`, which
  **always returned empty** — Go promotes the incoming `Host` header to
  `Request.Host` and strips it from the header map — so the gate `403`'d *every*
  browser request regardless of host (only the signed CLI path worked). The gate
  now reads `req host` (new `HttpRequest>>host` VM primitive). This is what makes
  the loopback-equivalence matching below actually take effect.
- Dashboard **approve/reject/cancel** no longer misfire with a `403 "loopback
  only"` when you reach the dashboard on the serving machine via a
  loopback-equivalent authority. The gate (shared by mission approve/reject and
  work-item cancel) now also treats `0.0.0.0[:PORT]`, `localhost.` (trailing-dot
  FQDN), and `[::ffff:127.0.0.1][:PORT]` (IPv4-mapped IPv6 loopback) as local,
  in addition to the existing `localhost` / `127.0.0.1` / `::1` / `[::1]` forms.
  The `127.` check was also tightened to require a dotted-quad (a spoofed
  `Host: 127.evil.com` is now rejected).

### Changed
- **`pp help` now lists every subsystem.** `mission`, `doctrine`, `bbs`,
  `worker`, `watch`/`unwatch`, and `whoami` were live but undocumented, and
  `identity` omitted its `use`/`invite`/`accept` subcommands; `workflow cancel`
  now shows its required `--reason`. The `pp` CLI's own help was collapsed to
  delegate to the single canonical help so the two can no longer drift.
  (adversarial-review U2/U3/U12)
- `pp worker`/`pp identity` now print the subcommand usage after an unknown
  subcommand (like the other command groups). A flag given without its value now
  warns explicitly instead of being reported as "required". (U10/U11)
- **Dispatcher/dashboard scan reductions** (no behaviour change): the workflow
  engine takes one `task` snapshot per tick instead of re-scanning per running
  workflow; mission rollup scans workitem/workflow/case once per sweep instead of
  per mission; the archivist-dispatch idempotency guard is an O(1) id lookup
  instead of a full task scan; and two O(n²) `copyWith:`-in-loop accumulators in
  the SSE render path were replaced with amortised-O(1) appends.
  (P2/P3/P6/P8)
- **`pp serve` memory footprint cut ~88%** under an open dashboard (RSS ~977 MB
  peak → ~115 MB, flat instead of a sawtooth). The per-tick dashboard work that
  drove the growth is eliminated: the SSE loop now caches its snapshot and skips
  the ~9 category scans + HTML re-render unless the BBS actually changed
  (`BBS>>changeCount`) or a token total moved; per-task token totals are read by
  *tailing* each running session `.jsonl` from a byte offset instead of
  re-reading the whole (growing) file every 5 s. The supervisor now launches
  with `GOMEMLIMIT`/`GOGC`/`madvdontneed` so the Go heap is capped and freed
  memory is returned to the OS. The SQLite WAL is kept small
  (`wal_checkpoint(TRUNCATE)` + `journal_size_limit`), collapsing a 41 MB WAL to
  ~4 MB, and the ganso reader pool/watcher were tightened.

### Added
- Mission cards now show an **epic → child-story tree** beneath each
  non-archived mission on the dashboard. The indented list surfaces each story's
  title, status, and wave, built entirely from the tick snapshot already cached
  by the SSE loop — no new BBS scans and no new SSE channels. Stories are ordered
  by wave (numerically) then id, so an unchanged panel stays byte-stable
  tick-to-tick (preserving scroll and any expanded plan). Every field is
  HTML-escaped; archived missions render no tree.
- Mission **rollup to done + outcome score** — an `in-progress` mission now
  advances itself to `done` once every work-item it owns has completed (the epic
  auto-promotes when its stories finish), closing the lifecycle
  (`approved → in-progress → done → archived`). On completion it records an
  `outcome_score` (0–1000) — the mean of its cases' per-workflow outcome scores
  (or a clean-completion baseline when it produced no cases) — so the mission is
  a real outcome anchor for the doctrine correlation backstop. `pp mission show`
  displays it; `done` missions are archivable.
- Mission **workflow provenance** — workflows launched for a mission (the
  `full-pipeline` and every child story workflow it dispatch-waves) now carry a
  `mission_id`, joining the indexed blast radius. `pp mission nuke` finds and
  cancels the whole live workflow tree, and outcome scoring aggregates their
  cases.
- Mission **Execute** (the D5 second trigger) — after a mission is approved and
  decomposed, a light explicit action starts the work: `pp mission run <id>`
  (CLI) or an **Execute** button on the dashboard mission card. It dispatches the
  mission's epic via `full-pipeline` (which dispatch-waves the child stories,
  honoring wave order and `depends_on`) and rolls the mission to `in-progress`.
  Only an approved mission whose epic has been materialized may run; a
  rubber-stamp approval never auto-starts agents. Served by the loopback-gated
  `/api/mission/run` (same posture as approve/reject). Missions now surface the
  `in-progress` status.
- Mission **decompose** — approving a mission now materializes a *story tree*,
  not just a placeholder epic. A mission can carry a structured
  `payload.breakdown` (`[{title, description, wave, depends_on, template,
  estimate}]`); on approve the dispatcher reconciliation materializes one story
  per entry under the epic, each stamped `mission_id` and parented to it, with
  `wave` carried through and `depends_on` (1-based indices into the breakdown)
  mapped to sibling story ids. Deterministic story ids (`story-<mission>-<n>`)
  keep it idempotent; a mission with no breakdown still yields just the epic.
  New `pp mission set-breakdown <id> '<json>'` writes the breakdown (used by the
  `mission-brief` planner, which now emits it alongside the plan), and
  `pp mission show` reports the item count.
- Mission **Decide→Act bridge (Phase B v1)** — approving a mission now *spawns
  and owns* work instead of dead-ending. On the next dispatcher housekeeping
  pass, any `approved` mission with no epic yet has a single epic work-item
  materialized (status `ready`, not auto-dispatched) and gets its `epic_id`
  stamped. The reconciliation keys on mission *status*, so it fires identically
  for the CLI and dashboard approve paths, and is idempotent (the `epic_id`
  guard plus a deterministic `epic-<mission_id>` id mean re-approve and
  dispatcher restarts never spawn a second epic). `pp mission show` now displays
  the `epic:` pointer.
- Mission provenance + teardown — every entity spawned because of a mission
  carries `payload.mission_id`, backed by a new indexed ganso column
  (`ix_mission`), so a mission's whole blast radius is one query.
  `pp mission nuke <id> [--confirm]` tears that blast radius down: **dry-run by
  default** (prints what it would change and touches nothing), and with
  `--confirm` **cancels the mission's running workflows** (cancellation cascades
  to their child workflows/tasks), hard-deletes the work-items, and archives the
  mission.
  Boundary: it reclaims unmerged planning artifacts only — merged commits are
  never touched. `pp workitem create` gained `--mission <id>` to enlist a
  work-item in a mission's blast radius.
- Opt-in dashboard host allowlist for the approve/reject/cancel controls. Set
  `PP_ALLOW_DASHBOARD_HOSTS="host:port,host:port"` (env) or `[security]
  dashboard_hosts` in `~/.config/pp/server.toml` to let an operator who reaches
  the dashboard by a fixed hostname/LAN IP use those controls without a 403.
  Empty (loopback-only) by default; there is no allow-all switch. This remains
  Host-header-based (a convenience over a network-trusted deployment, **not** an
  auth boundary — see README); the long-term fix is signing browser
  approve/reject/cancel through the ed25519 `signedPost:` path.
- Mission archival — a terminal `archived` mission status (symmetric with
  doctrine `maturity:retired`). New `pp mission archive <id> [--reason R]
  [--repo R]` retires a mission from `approved`, `rejected`, or `researching`
  into `archived` via the signed BBS put; `awaiting-approval` missions are
  refused (a pending plan gate must be decided first). `archived` is terminal —
  approve/reject continue to refuse it. The tuple is preserved (no delete), and
  `pp mission list --status archived` lists archived missions. The dashboard
  Missions panel now hides archived missions from the default view, collecting
  them in a collapsed **Archived** section instead of the "Other missions" group.
- Missions dashboard panel — the web dashboard now surfaces missions, with
  `awaiting-approval` ones FOREGROUNDED in their own group. Each mission renders
  a one-line brief plus its plan markdown (rendered to HTML, every line escaped)
  in an expandable card. Awaiting-approval cards carry **Approve** / **Reject**
  controls that POST `/api/mission/approve` | `/api/mission/reject` (unsigned but
  loopback-gated, mirroring `/api/workitem/cancel`); the status flip is consistent
  with the `pp mission approve/reject` CLI (only `awaiting-approval` missions can
  be decided — any other status is refused with 409). The SSE snapshot re-renders
  the panel each tick, so an approved/rejected mission drops out of the awaiting
  list live. Each mission render is independently guarded, so a single malformed
  mission tuple is skipped without blanking the panel.
- Mission artifact + `pp mission` CLI — a durable, high-level Auftragstaktik
  intent artifact. New `mission` tuple category (linear + durable, NOT pinned;
  written/mutated via `out:`/`update:`). `pp mission start "<brief>" [--repo R]`
  mints a fresh `msn-<hex>` mission (status `researching`) and kicks the
  `mission-brief` workflow, passing the brief as the `description` param and the
  mission id so the workflow's agents write their intent/plan back into the
  mission tuple; the launched workflow instance is recorded on the mission. If
  the `mission-brief` template is not yet installed, `start` warns gracefully and
  still creates the mission. `pp mission list [--repo R] [--status S]`,
  `pp mission show <id>`, `pp mission approve <id>`, and
  `pp mission reject <id> [--reason R]` round out the surface; approve/reject are
  gated on `awaiting-approval` status (any other status is refused).
- `mission-brief` workflow — the intake flow `pp mission start` launches:
  interpret-intent (planner) → research (scout) → review (reviewer, adversarial)
  → synthesize-plan (planner), bracketed by create-worktree/merge-worktree.
  Roles are reused (no new role class); mission-specific guidance lives in the
  transition prompts. The mission id is threaded so agents write structured
  intent and the final plan back into the mission tuple. The workflow terminates
  by storing a markdown plan (intent, success criteria, research synthesis,
  proposed high-level breakdown, risks, definition-of-done) and flipping the
  mission to `awaiting-approval` — it does NOT park on an approval gate.
- `pp mission set-intent <id> "<md>"` and `pp mission set-plan <id> "<md>"` —
  agent-facing read-modify-write commands used by the `mission-brief` workflow to
  record the structured intent and the plan back into the mission tuple.
  `set-plan` also flips the mission to `awaiting-approval` in the same write.
- Doctrine divergence backstop (the "lie-detector") — a new off-critical-path
  sweep (`Dispatcher>>maybeFlagDoctrineDivergence`, sibling of
  `maybeFlagDislikedDoctrine`, on the every-30-tick branch with its own
  `on:Exception`) cross-checks each ACTIVE doctrine's HUMAN votes against its
  MEASURED outcomes at the doctrine's CANONICAL `doctrine_scope` and FLAGS two
  divergence classes for human review: SUSPECT (`net >= posVotes` AND
  `ewma_score <= lowOutcome`) — "loved but underperforming (possible
  sycophancy)" — and UNDER-APPRECIATED (`net <= negVotes` AND
  `ewma_score >= highOutcome`) — "disliked but effective". It requires minimum
  evidence on BOTH sides (a triggering net AND outcome `n >= minN`) before
  judging, emits ONE `warn` notification per class recommending review, and is
  IDEMPOTENT via a `doctrine-divergence-flagged:<id>` signal that re-notifies
  only when the divergence WORSENS or the class FLIPS. It is FLAG-ONLY: it NEVER
  auto-retires or auto-adjusts — the correlational signal is treated as
  suggestive, a human adjudicates. `pp doctrine show <id>` now displays the
  outcome stats (`n`, `ewma/1000`, `success/1000`) next to the existing vote
  tally plus a one-line divergence note (agreement vs SUSPECT vs
  UNDER-APPRECIATED). Configurable + guarded via env: `DOCTRINE_OUTCOME_BACKSTOP`
  (default enabled; `0`/`false` disables), `DOCTRINE_OUTCOME_MIN_N` (default 5),
  `DOCTRINE_DIVERGENCE_POS_VOTES` (default 3), `DOCTRINE_DIVERGENCE_NEG_VOTES`
  (default -3), `DOCTRINE_OUTCOME_LOW` (default 0.4) and `DOCTRINE_OUTCOME_HIGH`
  (default 0.75) — outcome thresholds accept a fraction (`0.4`) or a bare
  milli-integer (`400`). (extends `TestDoctrineFlag`, wired into
  `CombinedTestMain`.)
- Per-doctrine outcome attribution — every terminated workflow now contributes a
  single OUTCOME scalar to the outcome stats of each doctrine that was injected
  into it, closing the Observe→Orient loop opened by doctrine-injection capture.
  `CaseBuilder>>buildCase:` computes `outcome_score` (integer milli-units
  `[0,1000]`, no floats): a clean first-pass completion scores ~1000, a thrashy
  completion (retries / review cycles / interventions, or a 0-file no-op) scores
  mid, and a non-completion scores 0 — a pure, unit-testable function
  (`CaseBuilder>>outcomeScoreOf:`). A new off-critical-path sweep
  (`WorkflowEngine>>applyDoctrineOutcomes`, hooked into `Dispatcher>>onTick`
  beside the archivist-enrichment sweep) folds each terminated workflow's score
  into a per-doctrine `doctrine-outcome-stats:<id>` signal at the doctrine's
  CANONICAL `doctrine_scope` — `{n, sum_score, ewma_score, n_success, n_fail,
  last_updated}` with an EWMA (α=3/10) so recent outcomes dominate and a
  stale-but-once-good doctrine decays. The fold is IDEMPOTENT (a
  `doctrine_outcome_applied` stamp on the case means a re-run never
  double-counts), SINGLE-WRITER (only the sweep writes the stats, so no race),
  and NON-FATAL (each case is wrapped in `on:Exception` so a malformed
  case/injection can never abort the sweep or gate a tick). A new reader
  `BBS>>outcomeStatsFor:scope:` returns `{n, ewma_score, success_rate}`
  (null/empty tolerant) for ranking + display. (new `TestDoctrineOutcome` suite,
  wired into `CombinedTestMain`.)
- Doctrine-injection attribution capture — every doctrine-consuming task now
  records WHICH doctrine (id + canonical `doctrine_scope`) was injected into it,
  so a later outcome sweep can attribute the task's result back to the doctrine
  that was in play. A new linear+durable `doctrine-injection` tuple category
  (`Categories>>valid`, `BBS>>isDurableCategory:`; deliberately NOT pinned)
  holds one record per task, UPSERTED keyed by `taskId` (consume-then-write on
  re-assembly). `Role>>assembleContext:` writes the record BEST-EFFORT right
  after `activeDoctrineFor:` selects the doctrine — wrapped in `on:Exception` so
  context assembly NEVER fails if the record cannot be written (mirrors
  `dispatchArchivist:`/`writeCaseSkeleton:`). Each ref carries the doctrine's
  canonical `doctrine_scope` (the payload scope, not BBS storage scope). A new
  `BBS>>injectedDoctrineForWorkflow:scope:` helper returns the de-duplicated
  UNION of `doctrine_refs` across all of a workflow's task records (null/empty
  tolerant) for the outcome sweep to consume. This story ONLY captures
  injection — outcome scoring and flagging are later stories. (new
  `TestDoctrineInjection` suite.)
- Vote-weighted doctrine relevance ranking — practitioner votes now bias which
  scarce doctrine slots reach an agent's Orient context. The deterministic ranker
  (`Role>>rankDoctrine:role:scope:query:cap:netVotes:`) adds a BOUNDED, saturating
  net-vote term to the existing tag+text relevance score: `score = tagScore*1500 +
  textScore + voteTerm`, where `voteTerm` is a sign-preserving log-scale function
  of `net = up - down` (`Role>>doctrineVoteTerm:`), hard-clamped to ±600 — strictly
  below one tag match (1500) and full text relevance (1000). So loved doctrine
  rises and disliked doctrine sinks AMONG comparably-relevant entries, but a
  loved-but-irrelevant entry can never crowd out a relevant one, and a runaway vote
  count cannot dominate. Votes are read once per ranking at each doctrine's
  canonical `doctrine_scope` (`Role>>doctrineNetVotesFor:bbs:`, fault-tolerant: a
  failing scan / malformed payload / no votes all yield net 0). All existing hard
  gates (active-only, scope-union, role targeting) and the universal-confidence
  floor are preserved exactly, and a zero-vote corpus ranks IDENTICALLY to before
  (net=0 contributes 0). Fully deterministic — no randomness; the score→confidence
  →identity total order is unchanged. (new `TestDoctrineVoteWeighting` suite.)
- Doctrine vote surfacing + review-flag sweep — humans can now see per-doctrine
  vote tallies and are alerted when active doctrine is consistently disliked.
  `pp doctrine show <id>` renders a `votes: up N / down M / net K` line and
  `pp doctrine list` shows a compact `+N/-M` vote column per entry, both tallied
  at the doctrine's canonical `doctrine_scope` (`DoctrineCLI>>showVoteTally:`,
  `formatVoteColumn:`, `formatVoteTallyLine:`). A new off-critical-path Dispatcher
  sweep (`maybeFlagDislikedDoctrine`, ~every 5min, env-gated, own exception
  wrapper) FLAGS — never auto-retires — any ACTIVE doctrine whose net vote score
  is `<= DOCTRINE_FEEDBACK_RETIRE_NET` (default -3) with at least
  `DOCTRINE_FEEDBACK_MIN_VOTES` (default 3) total votes, emitting ONE human-facing
  `warn` notification recommending `pp doctrine retire`. It is idempotent (a
  `doctrine-feedback-flagged:<id>` marker signal carrying the net at flag time
  suppresses re-notification unless the net drops further) and disabled by
  `DOCTRINE_FEEDBACK_FLAG=0`. Retire/reject stays human-gated. (new
  `TestDoctrineFlag` suite covering show/list rendering, the flag threshold,
  idempotency, min-votes/active-only gating, the kill-switch, and non-fatal
  malformed-tuple handling.)
- Doctrine voting invitation in agent prompts — doctrine-consuming roles
  (planner/implementer/reviewer/scout/fixer) now see each injected doctrine
  entry's `dctr-` id rendered prominently ("cite this id to vote") plus an
  OPT-IN, SPARSE invitation to `pp doctrine feedback <id> --up|--down --reason`.
  The invitation is deliberately framed as optional and uncommon — abstention is
  the norm — so agents vote only on strong signal rather than rubber-stamping a
  mandatory rate-all. Roles where `consumesDoctrine:` is false (e.g. strategist,
  foreman) do not receive the invitation. (`Role>>renderDoctrineEntry:payload:`,
  `Role>>doctrineVotingInstruction`; new `TestDoctrineVotingPrompt` suite.)
- Doctrine feedback capture + tally — the race-free layer for agent votes on
  doctrine. New `doctrine-feedback` tuple category (linear + durable, NOT pinned;
  added to `Categories>>valid` and `BBS>>isDurableCategory:`) and
  `pp doctrine feedback <id> --up|--down [--reason R]`. Each vote is an
  APPEND-ONLY fresh `dfb-<hex>` tuple (never a read-modify-write of a shared
  aggregate), so concurrent votes never race/clobber. Votes are written at the
  doctrine's OWN `doctrine_scope` (canonical) — a global doctrine voted on from
  any repo converges on the `global` feedback scope rather than splitting — and
  auto-attributed to `PP_TASK`/`PP_WORKFLOW` (degrading to `''`/`manual` when
  unset). Exactly one of `--up`/`--down` is required and `--reason` is mandatory
  on `--down` (friction by design). The pure `feedbackTallyFor:scope:` helper
  returns `{up, down, net, total}` over that same canonical scope (empty/NULL
  tolerant). New `test/cli/test_doctrine_feedback.mag` suite wired into
  `CombinedTestMain`.
- Automatic doctrine synthesis (C2 loop-closure). The Dispatcher now re-orients
  the doctrine layer on its own: every ~5min (`Dispatcher>>maybeAutoSynthesize`,
  alongside housekeeping) it buckets `case` (AAR) tuples by scope and, for any
  scope that has accumulated at least a threshold of NEW cases (past a per-scope
  `doctrine-synth-watermark` signal) and is past its cooldown, enqueues a
  Strategist task via the new `WorkflowEngine>>dispatchStrategist:` — no manual
  `pp doctrine synthesize` poke required. It is idempotent (one in-flight
  strategist per scope), per-scope, cooldown-gated, and strictly
  off-critical-path (its own `on:Exception` wrapper means it can never gate a
  tick step). Configurable via env (read once per sweep, all guarded):
  `DOCTRINE_AUTOSYNTH` (default enabled; `0`/`false` is the kill-switch),
  `DOCTRINE_AUTOSYNTH_THRESHOLD` (default 8), `DOCTRINE_AUTOSYNTH_COOLDOWN_SECS`
  (default 3600). The synthesis prompt now has a single source of truth
  (`Strategist class>>synthesisDescriptionForScope:`), shared by both the manual
  CLI path and the automatic dispatch so the two cannot drift.
- End-to-end / dispatch-level integration test for the `pp doctrine` CLI
  (`test/test_doctrine_cli_e2e.sh`, wired into `test/run_integration.sh`). It
  builds `pp` under a scratch HOME, starts a live server, and drives every
  subcommand reachable via `DoctrineCLI>>runWith:` (propose → list → show →
  promote → crystallize `--as workitem` → crystallize `--as convention` with and
  without the four `--affirm-*` flags → decrystallize → retire → reject). The
  critical assertion is that BOTH `crystallize --as workitem` AND
  `--as convention` reach their own logic — closing the gap that let a
  duplicate-`cmdCrystallize:` merge silently shadow the workitem handler and ship
  (fixed in 33db668). A cheap structural guard additionally fails the suite if
  any `DoctrineCLI` selector is defined twice.
- `pp doctrine crystallize <id> --as convention` — the RARE, human-only
  advisory→binding transition. Converts a matured (`active`) doctrine into a
  BINDING convention, the only verb that changes binding status. Gated behind a
  strict four-clause QUALIFICATION TEST that must be affirmed in full —
  `--affirm-unconditional --affirm-no-false-positive --affirm-mechanical
  --affirm-evidence` (the friction is by design; a wrong convention taxes every
  agent on every task). It never auto-fires and is never Strategist-proposed. On
  pass it writes the convention via the same PINNED path existing conventions use
  (so `bbs scanAll: convention` surfaces it to the execution-time Role consumer)
  and records the forward `crystallized_into` link doctrine→convention; the
  doctrine is kept as the rationale/lineage source (maturity untouched).
  `pp doctrine decrystallize <id> --convention <convId>` reverses it: retires the
  convention and clears the matching link, restoring the doctrine to
  purely-advisory.
- Doctrine now carries a forward provenance edge, `crystallized_into`, completing
  the C2 lineage `case → doctrine → {convention | workitem}`. A doctrine payload
  gains a `crystallized_into: [ {category, scope, identity} ]` array (mirror of
  `provenance`, opposite direction; defaults to `[]`). A new engine-side
  `DoctrineWriter` appends links idempotently via `BBS>>update:` (no duplicate for
  the same `{category, scope, identity}`), and link resolution is null-tolerant —
  a target that later disappears dangles to `nil` instead of crashing readers.
  `pp doctrine show <id>` now lists the downstream artifacts. This adds only the
  link substrate; the verbs that populate it (`to-convention`, `to-workitem`) come
  later.

### Changed
- Doctrine advisory injection (the Orient consumption path) now SUPPRESSES any
  active doctrine that has crystallized INTO a `convention`. Once a doctrine
  becomes a binding convention it is unconditional and already reaches agents as
  a convention at execution time, so re-injecting it as advisory double-counts
  the guidance and wastes a scarce capped slot. The hard candidate gate in
  `Role>>activeDoctrineFor:scope:role:query:` now drops entries whose
  `crystallized_into` includes a `category='convention'` link, freeing the slot
  for non-redundant guidance. Suppression is precise — a `workitem`-only link
  never suppresses (that doctrine kept its judgment clause) — and null-tolerant —
  a missing or empty `crystallized_into` is never suppressed. Storage, synthesis,
  and the crystallize verbs are unchanged; the suppressed doctrine still survives
  as the convention's rationale/lineage record.

### Fixed
- Missions dashboard panel no longer resets scroll or collapses an open plan on
  routine SSE refreshes. The dashboard applies each SSE patch as a wholesale
  element replace (`target.outerHTML = …`), so re-sending the Missions panel
  every ~5 s tick yanked a reader out of a long, expanded `awaiting-approval`
  plan and jumped the view to the top. Three changes fix it: (1) missions now
  render in a deterministic identity order (`DashboardSSE>>sortMissions:`) so BBS
  `scanAll` bucket-ordering churn cannot change the HTML tick-to-tick;
  (2) per-subscriber change-detection (`sendElements:to:key:`) skips re-patching
  the panel when its HTML is byte-identical to the last one sent to that client
  (a freshly-connected client still gets the panel on its first tick); and (3) on
  a *legitimate* update the dashboard now captures the expanded `<details>` state
  and page scroll before the replace and restores them after, so the operator is
  never forced to re-expand the plan or re-find their place. Auto-foregrounding of
  awaiting-approval missions and the approve/reject controls are unchanged.
- `files_changed` in workflow cases no longer always reads 0. No code ever
  produced a `files_changed` observation, so `CaseBuilder` always fell back to
  its default of 0 and undercounted real merged work. `MergeWorktreeAction` now
  computes the count via a new `GitOps changedFileCount:since:in:` (three-dot
  `main...<branch>`, matching the reviewer's `git diff --stat main...HEAD` view)
  and emits it into the `merge-complete` observation at all three merge sites
  (pipeline, standalone, wave-child). The count is taken before the branch is
  force-deleted, and soft-fails to 0 on empty git output so a merge never
  crashes. A 2-file change now records `files_changed=2`, not 0.
- `pp doctrine propose --applies repo` now resolves to the concrete repo scope
  (e.g. `procyon-park`) instead of storing the literal type `repo`. The consumer
  composes applicability by scope-union `{global, <repo>}`, so a stored `repo`
  matched nothing and repo-scoped doctrine reached no role at all — only global
  doctrine ever surfaced. The synthesis prompt now also passes `--repo` and asks
  for a `role:` tag. (Existing `repo`-typed entries were backfilled to their
  concrete scope.)

### Changed
- Doctrine routed to a role is now chosen by relevance to the specific task,
  not by arbitrary order. The (≤6) capped slots go to the doctrine most
  relevant to the task at hand: each candidate that survives the existing hard
  gates (active maturity, global∪this-repo scope union, role targeting) is
  scored by a deterministic hybrid of tag overlap (against the role, repo, and
  the task description's keywords) plus IDF-weighted, length-normalized lexical
  similarity between the task text and the entry's principle+rationale. A small
  universal floor reserves slots for the highest-confidence cross-cutting
  (untagged) doctrine so it always lands. Selection is fully deterministic —
  no randomness — with ties broken by confidence then identity. When a task has
  no description the ranking degrades gracefully to tag overlap, universals, and
  confidence.
- Doctrine (the Orient layer) now biases every Act, not only the Decide step.
  Synthesized doctrine — which is mostly execution-time guidance — is injected
  into the context of the execution roles (implementer, reviewer, scout, fixer)
  in addition to the planner, instead of reaching only the planner where it was
  inert. Injected entries are still gated to `active` maturity and the
  global∪this-repo scope union, are now role-targeted (an entry tagged
  `role:<name>` reaches only that role; untagged entries are universal), and are
  ranked by relevance and capped at 6 per role so agent context does not bloat.
  Strategist proposals now emit a `role:` tag per principle so doctrine becomes
  role-targeted going forward.

### Added
- The dashboard Board now has a "Hide done"/"Show done" toggle in the board
  header that hides every card in the `done` column from the view. It is a
  client-side view filter only — no work item is deleted or mutated — and the
  hide state persists across live SSE board updates and reloads, so done cards
  arriving via the per-tick broadcast respect the current hide/show choice.

### Fixed
- `merge-worktree` now force-deletes the impl branch after merging it into the
  feature branch, so completed standalone workflows no longer leave an orphan
  `impl/<id>` branch behind (`git branch -d` refused it as "not fully merged"
  because feature had not yet landed on main at cleanup time).
- The dispatcher's periodic housekeeping no longer aborts the whole sweep when a
  single malformed/transient tuple raises during the orphan scan; each orphan
  candidate is now handled independently and a skip is logged with its key,
  matching the per-item resilience of the rest of housekeep.
- Story-workflow auto-merge no longer clobbers the dispatcher's working tree.
  The impl→feature merge now runs in an ephemeral git worktree (never switching
  or dirtying the primary checkout), and handles non-fast-forward merges. The
  feature→main merge is guarded — refused if the working tree has uncommitted
  changes rather than overwriting them. Previously every merge ran
  `git checkout` in the live checkout, which raced with the server and aborted
  on any dirty tree.

### Changed
- The dashboard SSE route and per-tick broadcast are always registered again.
  The `PP_NO_SSE` kill-switch (an operational mitigation for the per-tick
  memory leak) has been removed now that the leak is fixed in the Maggie VM
  (tracing string/dictionary GC plus a frame-bound block sweep). Verified
  stable under a 12-subscriber SSE soak: RSS sawtooths under load and the
  collector reclaims mid-load instead of growing unbounded toward an OOM kill.

### Added
- Dashboard **Doctrine** tab — a fetch-on-demand view of the plan-time Orient
  layer. Lists doctrine across all scopes (sorted active → proposed → retired →
  rejected) with maturity badge, applicability scope, tags, confidence, source
  case count, and rationale; filterable by maturity. Fetched on tab activation
  (doctrine is low-churn, so it is not streamed over the broadcast SSE).
- `pp doctrine export` — explicit STUB for exporting active doctrine to pudl as a
  downstream sink. Not implemented (no pp→pudl integration exists; target protocol
  undefined). Keeps the loose-coupling boundary visible: PP owns doctrine; pudl is
  a future export sink, never a read dependency.
- Plan-time doctrine consumption (the payoff): the `planner` role's context
  assembly now injects ACTIVE, applicable doctrine. Applicability is composed by
  scope-union (`global` ∪ this-repo ∪ this-repo:path) from `payload.doctrine_scope`,
  and maturity is gated to `active` only — proposed/rejected/retired never reach
  planning, preserving the human promotion gate. This deliberately bypasses the
  generic soft-category context path (which would leak unreviewed doctrine and
  miss global scope).
- Doctrine synthesis loop: a `Strategist` role (read-only / write-to-KB, like
  the Archivist) reads the swarm's `case` (AAR) tuples, clusters recurring
  generalizable lessons, dedupes against existing doctrine, and proposes new
  `proposed` doctrine via `pp doctrine propose`. `pp doctrine synthesize [--repo R]`
  enqueues a Strategist task (no worktree/merge — it writes doctrine tuples
  straight to the tuplespace). Humans promote proposals to `active`.
- `pp doctrine` CLI — manage the doctrine layer: `list [--scope][--tag][--maturity]`,
  `show <id>`, `propose "<principle>" [--rationale][--applies][--tags][--confidence][--key]`,
  and `promote`/`retire`/`reject <id>` for maturity transitions. Built on the
  generic BBS endpoints (signed `/api/bbs/put`; unsigned `/api/scan`/`/api/rdp`) —
  no doctrine-specific routes. Each proposal gets a fresh unique id (`dctr-<hex>`)
  written linear+durable; the human name lives in `payload.key`.
- `doctrine` tuple category — the foundation for the plan-time doctrine layer
  (the swarm's synthesized "Orient", distilled from `case`/AAR tuples). It is
  linear + durable (mirrors `case`): registered in `Categories>>valid` and
  `BBS>>isDurableCategory:` so entries survive restart, but deliberately NOT in
  `Categories>>pinned` — entries are written/mutated via `out:`/`update:`
  directly so `rdp:`-based existence and maturity checks stay valid (the generic
  pinned put path writes tuples invisible to `rdp:`). Doctrine is exempt from
  `pp gc`, whose retention sweep targets only `case`. See
  `docs/design-doctrine-layer.md`.
- `pp workitem delete <id> [--repo R] [--cascade]` — hard-delete a work item.
  Unlike `cancel` (which only sets `status=cancelled` and preserves the tuple),
  delete removes the tuple from the tuplespace entirely and is irreversible.
  Backed by a new signed `POST /api/workitem/delete` (same ed25519 auth path as
  `update`/`run`); requires an explicit `--repo`/scope (no silent default) for
  the destructive op. `--cascade` also removes all descendant work items, so
  deleting a done epic clears its stories too.
- Board work-item detail modal now renders the item's comments
  (`payload.comments`) when present, as a styled section alongside the existing
  relationship blocks (nothing is shown when there are no comments).
- "Hide from my board" — a non-destructive, per-viewer dismiss action in the
  board detail modal, distinct from the global/permanent "Cancel item". Hidden
  cards are tracked client-side in `localStorage` (`pp.dismissedCards`, keyed by
  `scope|identity` so dismissal is per-board) and stay hidden across SSE
  re-renders and page reloads by reusing the existing `.wi-hidden` filter path.
  A "N hidden / show all" affordance near the board filters reverses it, and the
  dismissed set is pruned to cards currently on the board to stay bounded.
- Board work-item detail modal. Clicking a card on the dashboard Board
  (Kanban) opens a dialog with the item's full detail — title, id, type,
  status, description, wave, labels, parent/children relationships,
  `depends_on`, repo/scope, and human-readable timestamps — fetched from a
  new unsigned `GET /api/workitem/detail?scope=&identity=` read endpoint
  (404 when not found). The modal is focus-trapped and closes on Esc or
  scrim click. A "Cancel item" button logically cancels the item (status →
  `cancelled`) via a new `POST /api/workitem/cancel`; after a confirm step
  the card drops off the active board on the next SSE tick. The cancel route
  is the first browser-initiated mutation: it is unsigned but loopback-gated
  (localhost/127.x/[::1] only; non-loopback callers get 403) for the
  single-operator local dashboard. Cancel is a logical delete only — the
  tuple, history, and relationships are preserved (no hard delete/cascade).
- Workflow staleness reaping. A workflow stuck in `running`/`dispatched`
  with no live task and past a TTL (1h) is now transitioned to `failed`
  (`last_failure_reason: workflow-stale`) by the Dispatcher housekeep pass,
  so it can be cascade-cleaned and moves from Active Workflows to Recent
  Completions. Previously such a workflow was immortal — shown active
  forever, never reaped or gc'd, even with all agents dead.
- `pp gc` now also sweeps stale-running workflows (non-terminal status, no
  live task, past TTL), giving operators a manual escape hatch for zombie
  workflows in addition to the automatic reaper.
- `pp gc` now sweeps cold `case` tuples (per-category retention window). Cases
  are written once per terminal workflow and were previously never garbage-
  collected — an unbounded, monotonic grower. `pp gc` keeps the N most-recent
  cases (`--keep-cases <N>`, default 2000) and sweeps the rest; `--no-cases`
  skips the sweep and `--cases-older-than <hours>` adds an age guard so a burst
  of recent workflows is never pruned. Cases stay retrievable by id while live
  (enricher/archivist read O(1), never by scan).

### Changed
- BBS in-memory `scan:`/`scanAll:` are now O(matches) instead of O(total durable
  tuples): a per-category bucket index (`byCategory`) is maintained alongside the
  existing hash indices. Unbounded growth in one category (e.g. `case`) no longer
  taxes every scan of every other category. (JSON-store path; the SQLite/ganso
  backend already indexes by category.)

### Fixed
- `Repo>>repoForName:` data race fixed (was intermittently bricking ALL task
  dispatch). Its class-side TTL cache (`CachedConfigs`/`CachedExpiries`) was read
  and mutated concurrently from the dispatcher tick, Scheduler dispatch,
  CreateWorktreeAction, and DispatchWavesAction — separate goroutines — with no
  synchronization. The corrupted Dictionary surfaced as `at:ifAbsent:: receiver
  is not a Dictionary` in `scheduler dispatch`/`create-worktree`, so the same
  binary would dispatch one task fine and fail the next. The cache is removed
  (config files are tiny and read at most once per task dispatch); lookups are
  now a race-free stat + parse.
- Stuck-dispatched task reaper no longer false-positive reaps live
  long-running tasks. A local harness fork keeps its task in
  `status='dispatched'`/`worker_id=nil` for its entire run (worker_id is
  multiplayer-only), so the 300s `stuckDispatchedThresholdSeconds` reaped
  any task running longer than 5 minutes out from under a working harness
  (e.g. an ~8-min implementer was reaped at retry 1, breaking the
  workflow). Threshold raised to 2400s (above the 1800s max harness
  timeout + grace); genuinely-dead forks are still reaped, just later.
- Board work-item detail modal now actually opens on card click. The
  click-to-open listener was bound to `#dashboard-workitems`, which the SSE
  patch handler replaces wholesale via `outerHTML` on every tick — detaching
  the listener after the first board render, so clicks silently did nothing.
  It is now delegated from `document` (a stable root) and survives re-renders.
- Dashboard "Active Workflows" panel now requires evidence of liveness (a
  live task, or a recent `started_at`) before showing a `running` workflow,
  instead of trusting the stored status absolutely. Abandoned/zombie
  workflows no longer linger in the active list.
- Guarded unguarded nil/non-Integer field reads in the workflows SSE render
  (`dispatched_at`, plus non-string `launched_by`/`executed_by` coercion)
  that intermittently produced `SSE render failed for workflows: Message
  not understood: ifTrue:` and a per-tick panel flicker.
- Dispatcher housekeep no longer crashes every ~5 minutes. Its workflow-id
  heuristic compared characters with `>=`/`<=`, which the Maggie VM did not
  implement for `Character` (the message returned nil), so `(c >= '0') and:
  [...]` raised `Message not understood: and:` whenever any signal tuple was
  present — aborting the orphan signal/token sweep and terminal-task reaping
  and letting the tuplespace accumulate unbounded. Now compares by integer
  code point (the project convention). The underlying VM gap (`Character`
  `<=`/`>=`) was also fixed upstream in Maggie.

### Security
- Hardened the `pp read` discoverability-hint helper (`warnStderr:`) to
  escape `\`, `$`, and backtick in addition to `"` before the message is
  passed through `/bin/sh`. Recovered from an orphaned Jun-18 retry branch:
  the change was written but lost in a dispatch retry-storm, and the code on
  `main` had regressed to escaping only `"`. Categories are a fixed
  vocabulary so this was not exploitable, but the helper is now safe against
  shell metacharacters if reused.

### Changed
- `pp read` scoped reads now scan once instead of twice. The discoverability
  hint reuses the scan response already fetched for rendering, dropping a
  redundant second non-consuming scan per scoped read. Also recovered from
  the same orphaned Jun-18 branch.

## [0.2.0] - 2026-06-18

### Added
- **Ganso/SQLite backing store (now the default).** The tuplespace is backed by
  a single SQLite database (`~/.pp/data/tuples.db`, JSON `data` + indexed
  generated columns) via the embedded `ganso` coordination toolkit, replacing
  the in-memory index + whole-file `bbs.json` re-encode. Writes are durable
  per-statement (WAL); restart is O(1) (≈60× faster); memory moves to disk.
  `BBS` delegates all store ops behind the backend; set `PP_STORE=json` to use
  the legacy store (rollback). Existing `bbs.json` data is imported on first
  boot. See `ganso.md`.
- After-Action Review (AAR): every terminal workflow writes a durable `case`
  (deterministic skeleton), an Archivist agent enriches it (narrative + lessons
  + confidence), strictly off the critical path.
- `pp read <category>` now accepts `--all` (alias `-A`) to scan every scope
  at once (a non-consuming cross-scope read), instead of only the current
  scope. The default stays scoped — scope isolation is preserved. When a
  scoped read finds nothing but matches exist in other scopes, `pp read`
  prints a discoverability hint pointing at `--all` (the hint count comes
  from a second non-consuming scan and never removes tuples).
- After-Action Review enrichment loop is now closed. When an Archivist task
  completes, the dispatcher reads its structured enrichment (an `observation`
  tuple, identity `case-enrichment`, linked to the workflow instance and
  carrying `aar`/`lessons`/`confidence`/`tags`) and merges it into the case
  via `CaseEnricher`. The handler is strictly off-critical-path: malformed,
  empty, or absent archivist output is a no-op (never a crash), and a
  workflow that completed successfully stays `completed` regardless of the
  archivist outcome.

### Changed
- Migrated the entire codebase to Maggie's 1-based array/string indexing
  (Smalltalk-80 convention), which the language adopted upstream. All
  element access, `copyFrom:to:` slices (now closed intervals), manual
  index loops, and `indexOf:` not-found checks (now `0` instead of `-1`)
  were converted across `src/` and the test suite. Without this, building
  against current Maggie produced silent off-by-one corruption and
  `index 0 out of bounds` crashes throughout the server and CLI.

### Fixed
- AAR `case` tuples now survive a server restart (they are institutional
  memory). The wire story added `case` to `Categories.pinned` but NOT to
  `BBS>>isDurableCategory` — the *separate* list that actually gates the
  disk flush + reload — so cases were written linear, never flushed, and
  vanished on the next `pp serve` boot. Added `case` to `isDurableCategory`
  (kept linear, since case reads/updates use the `rdp:`/`update:` path; a
  pinned tuple would be invisible to those existence checks). New CSW5 test
  writes a case, flushes, and loads a fresh BBS from the same dir to prove
  it reloads.
- AAR enrichment now actually populates the case. The live Archivist agent
  emits its enrichment as a JSON object in the observation's `detail` field,
  but the handler expected flat `aar`/`lessons`/`confidence` keys on the
  payload — so every live enrichment silently no-op'd to empty
  (`aar=''`/`lessons=[]`/`confidence=0`) even though the loop was wired and
  crash-free. The handler now decodes `detail` JSON (falling back to the
  payload for direct-dict callers), and the Archivist prompt was tightened to
  emit exactly `{aar, lessons, confidence, tags}`. Verified live: a completed
  workflow's case is enriched with a real AAR.
- `pp workitem <subcommand>` is no longer misrouted to "Unknown workitem
  command". The dispatcher read the subcommand at `args at: 3`, but the
  handler frame is `[workitem, sub, arg]`, so the subcommand is at
  `args at: 2` (an off-by-one missed in the 1-based migration). Every
  `pp workitem` subcommand (create, create-from, run, ready, …) was broken.
- All `pp` CLI signed writes (`notify`, `observe`, `signal`, `workitem
  create`, …) no longer crash. The request-signing canonical string built
  its line separators with `String with: Character lf`, which now panics
  (`primConcat: argument must be a string`) on current Maggie. Switched to
  `String lf` (the `primLf` primitive), which yields identical bytes so
  existing server-side signature verification stays compatible.
- Restored the **server** build against current Maggie. The remaining 17
  `String with: Character lf` sites (`Main.mag`, `api/Server.mag`,
  `api/SignatureVerifier.mag`) panicked the same way at runtime, so a
  server rebuilt on current Maggie could not verify signed writes (its
  `SignatureVerifier` canonical) — the prior running server worked only
  because it predated the VM change. All 17 migrated to `String lf`
  (byte-identical `\n`), so a freshly built `pp serve` verifies signed
  writes again. This unblocks live deployment of the AAR case/enrichment
  feature.
- `pp worktree clean` no longer crashes the whole command when a single
  worktree fails to process. The sweep now handles each task inside its own
  rescue (logging and continuing on error) and skips malformed, non-string
  directory-listing entries instead of crashing on string concatenation.
  Reports a count of any skipped worktrees.
- Restored buildability against current Maggie. The bundled `alto` Go
  interop shims (`wrap/tcell`, `wrap/terminal`) used the old
  `PrimitiveFunc` signature (`interface{}` receiver) and no longer
  compiled after the VM switched to a typed `*VM` receiver. Since `alto`
  was unused (no `src/` code referenced its symbols), the dependency was
  dropped entirely rather than patched.

### Added
- `pp gc` is now the single command for cleaning up everything stale.
  In addition to the prior behaviour (terminal workflows + their
  associated tuples, merged `feature/*` branches), the default sweep
  now also covers:
    - **Orphan tuples** — tasks/tokens/events whose
      `payload.workflow_instance` references a workflow that no longer
      exists, plus signals whose scope matches the workflow-id
      convention (`<template>-<epoch>-<id>`) but isn't in the live set.
      Survey snapshot found 793 of 1,290 tuples (~62%) were unreachable
      junk because the legacy walk could only reach tuples through an
      existing workflow.
    - **Worktrees** — folds in `pp worktree clean`, the largest
      consumer of `~/.pp` disk (~88 MB per task dir).
    - **Session files** — orphan `~/.pp/sessions/<task>.jsonl` older
      than `--older-than` hours (default 24). Was opt-in via
      `--sessions`, now default-on (use `--no-sessions` to skip).
    - **bbs.json backup rotation** — keeps the newest
      `--keep-backups N` (default 2) and removes the rest.
  Use `--dry-run` to preview, `--no-sessions` / `--no-worktrees` as
  escape hatches.

### Removed
- The unused `alto` dependency and its generated Go interop wrappers
  (`wrap/tcell`, `wrap/terminal`).

### Fixed
- Dashboard "Recent Activity" panel was silently dropping the newest
  notification and producing a `Message not understood: at:ifAbsent:`
  on every SSE tick, which (combined with the unrescued tick loop)
  blacked out the entire dashboard. `renderNotificationsHtml:` iterated
  `recent size to: 1 by: -1`, treating the slice as 1-indexed; Maggie
  arrays are 0-indexed half-open (same convention as `copyFrom:to:`,
  see commit 7c182d6). Loop now walks `(recent size - 1) to: 0 by: -1`,
  reads `created_at` from the tuple top-level (the prior code looked
  inside `payload`, where it never lives, so timestamps were always
  empty), and uses the two-arg `copyFrom:to:` form for the >30
  trim. Each row is wrapped in its own rescue so one malformed
  notification can no longer void the whole panel.
- Dashboard SSE broadcast goroutine no longer dies permanently on a
  single bad tuple. `Server>>startSSETick` now wraps each `tick`
  invocation in `on: Exception do:`, `DashboardSSE>>tick` rescues
  `computeSnapshot` independently, and `computeSnapshot` runs each
  panel's render in its own `safeRender:rootId:do:` so a single failing
  panel falls back to a render-error fragment instead of blanking the
  whole dashboard. Prior behaviour: a single throw permanently killed
  the broadcast fork, leaving every panel stuck on "Connecting…" with
  the HTTP server still running and `/api/dashboard` still returning
  fresh data — fault localised to the broadcast loop.
- `CLIBase>>silentInp:scope:identity:` was posting to the signed
  `/api/inp` route, which silently failed for `task`/`token`/`signal`
  removals (root cause not yet diagnosed; `/api/inp` works fine
  unsigned via curl, and `pp bbs rm` works fine via the unsigned
  `/api/bbs/rm` route). Effect: `pp gc` printed `[orphan task] …`
  and `[orphan signal] …` lines but the live tuple count never
  moved — the workflow-children sweep and the new orphan sweep
  were no-ops in practice. Switched the helper to the unsigned
  `/api/bbs/rm` endpoint that `pp bbs rm` already uses; that
  endpoint also sync-flushes, so a server restart between `pp gc`
  and the next async flush no longer resurrects the tuples.
  Verified end-to-end: a single `pp gc` dropped a stale tuplespace
  from 1,294 to 504 tuples and `~/.pp/worktrees` from 1.5 GB to 0.

- DashboardSSE `tokenCacheLoop` ran every 5 s reading every
  `~/.pp/sessions/<task>.jsonl` (~127 MB / 413 files on the survey
  host) regardless of whether any dashboard client was connected,
  burning ~25 MB/s of allocations the Go heap couldn't return to the
  OS fast enough. Over a few hours pp serve drifted to 32 G of
  committed pages with macOS compressing 12 G of them — system-wide
  memory pressure from a closed dashboard. Fixed three ways:
    - The refresh now early-returns when `sseSubscribers isEmpty`.
    - Per-file mtime cache (`tokenCacheMtimes`) so unchanged session
      files aren't re-parsed.
    - The dedicated `tokenCacheLoop` fork is gone; the work runs
      inline in `tick`, which is already gated on subscribers and
      already runs every 5 s. Permanent goroutine count drops from 3
      to 2.

  Combined with the upstream maggie `petermattis/goid` arm64 fast-path
  upgrade, idle CPU went from ~200% to ~0% and `top` memory from 32 G
  to 1.5 G.

- BBS pinned-upsert paths (`updatePinned:do:`, `upsertPinned:`,
  `upsertSignal:`) had read-modify-write races: two concurrent updaters
  on the same key could lose updates or leave duplicate signal tuples.
  Serialised through a new `upsertMutex` distinct from the index `mutex`
  so the find/remove/write sequence runs as one critical section without
  reentering the non-reentrant index lock.
- BBS `outAffine:` drained legacy duplicates with a tight
  `[(self inp: ...) notNil] whileTrue: []` spin loop. A pathological
  writer could pin a CPU core indefinitely. Replaced with a bounded
  drain (cap 1024) that yields between iterations.

### Changed
- BBS flat `index` switched from `Array` (with O(N) `copyWith:` per write)
  to `ArrayList` (amortized O(1) `add:`). Public `scan:`/`scanAll:`
  still return a fresh `Array` so JSON encoding and existing callers
  are unaffected.
- BBS `saveToDisk` snapshot is now self-contained: each durable tuple
  and its payload are shallow-copied inside the index critical block
  before the JSON encode runs unlocked. Removes the unstated invariant
  that no caller may mutate a payload Dictionary in place.
- Dispatcher housekeeping is now single-pass: one `scanAll:` per
  category (`workflow`/`token`/`task`/`signal`) with tuples bucketed
  by `workflow_instance` (or scope, for signals). Prior implementation
  issued 3 `scanAll:` per terminal workflow inside `cleanWorkflowCascade:`
  — O(N*M) in (terminal workflows × tuples). Now O(M).
- Dispatcher `onTick` takes ONE `bbs scanAll: 'task'` snapshot per tick
  and threads it through `Scheduler>>dispatchTasks:`,
  `reapExpiredClaimsIn:`, and `reapStuckDispatchedIn:`. Prior code took
  three independent task scans every tick. The original `dispatch`,
  `reapExpiredClaims`, and `reapStuckDispatched` selectors remain as
  no-arg trampolines for callers that don't have a snapshot.
- Replaced `victims := victims copyWith: t` accumulators in the
  housekeeping cascade and both reaper filter loops with
  `GrowableArray` (amortized O(1) append) — was O(K²) on K matches.

- Unified the duplicated `maybePromoteParentOf:` (Server.mag) and
  `maybePromoteParent:` (WorkflowEngine.mag) cascade-promotion methods
  into a single `WorkflowEngine>>maybePromoteParent:scope:` impl.
  `Server>>handleWorkitemUpdate:` now delegates via
  `dispatcher workflowEngine maybePromoteParent:scope:`, and the
  unified impl uses the new `BBS>>updatePinned:do:` helper. Net -27
  source lines (deleted Server's 28-line duplicate). Scout survey §1.6.
- Migrated 6 manual payload-clone + `upsertPinned:` sites to use the
  existing `BBS>>updatePinned:scope:identity:do:` block helper (which
  already does the safe payload-copy + atomic replace). Added an
  `actor:do:` overload so the 4 sites that needed actor attribution
  (`handleWorkitemUpdate:`, `handleWorkitemComment:`, `handleUserRevoke:`,
  `handleUserRotate:`) can also use it. Sites in `Server.mag`
  (`updateWorkitemStatus:`, `handleWorkitemUpdate:` plus its child
  cascade, `handleWorkitemComment:`, `handleUserRevoke:`,
  `handleUserRotate:`) and `WorkflowEngine.mag` (`markWorkitemDone:`).
  Net diff: -36 source lines. Scout survey §1.3.
- Replaced `Array new: 0` + sequential `copyWith:` build-up patterns
  with array literal syntax `#('a' 'b' 'c')` where the leading elements
  are static strings. Touches `WorkflowEngine>>buildAffinity:` (5-element
  valid-keys list), `Shell` class methods (`run:`, `capture:`,
  `runChecked:`), `Scheduler>>validateScope:` (4 shell-args sites), and
  the `Main`/`PP`/`Repo`/`CliPP`/`WorkItemCLI` CLI dispatch helpers.
  Pure refactor — no behavior change. Scout survey §1.4/§3.12.

### Fixed
- Slice-by-prefix off-by-one in `ApiServer>>watchWorkflowsFor:` (Server.mag)
  and `DashboardSSE>>renderTodayWindow` (scope-violation aggregation): both
  used `prefix size + 1` against Maggie's exclusive `copyFrom:to:` upper
  bound, dropping the first character of the suffix. Centralised the slice
  in a new `StringUtil>>stripPrefix:from:` helper and replaced all three
  sites (the third was the already-correct
  `Scheduler>>taskIdFromCompleteEvent:`). See scout survey §1.9.
- `Dispatcher>>reapExpiredClaims` no longer crashes the tick when it
  observes a stale `task` tuple snapshot. `BBS>>scanAll:` returns shared
  index references, so a payload mutated by a racing
  `Server>>handleTaskComplete` or `Scheduler>>checkCompleted`
  (status='completed', claim_expires_at=nil) could surface to the reaper
  mid-iteration and a `now > nil` comparison or a missing `payload`
  would panic with "invalid memory address or nil pointer dereference",
  killing the entire sweep. The per-tuple body is now wrapped in a
  defensive try/catch that logs and skips, and the comparison guard
  re-checks `payload notNil` and `expiresAt notNil` before reading.
  Mirrored inside `reapTask:`'s atomic `update:` block. Adds RP8/RP9
  unit tests covering the post-completion stale-tuple and corrupt
  (non-Number) `claim_expires_at` cases.

### Removed
- Dead code per scout §3 cleanup:
  - `Dispatcher>>reconcile` (empty placeholder) and its onTick caller.
  - `Dispatcher>>expireAffineTuples` (empty placeholder) and its
    housekeep caller — TupleSpace already handles affine TTLs natively.
  - `BBS>>workitemTuple:precedesTuple:` (unused after `childrenOfParent:`
    was switched to `sort:`).
  - `BBS` `cueCtx` instance variable — assigned but never read.
  - `BBS>>removeFromIndexUnsafe:` — inlined into its sole caller
    (`update:scope:identity:do:`).
  - Deprecated `Server>>handleWorkitemAddChild:` and the
    `/api/workitem/add-child` route — no in-tree callers remained.
  - Back-compat overload ladders for
    `Dispatcher>>instantiateWorkflow:scope:params:...` and
    `WorkflowEngine>>tryFireTransition:...` — only the widest signature
    is kept; in-tree callers were migrated.

### Performance
- `WorkflowEngine` failure path no longer triggers redundant
  `scanAll: 'workflow'` calls. Added wf-accepting variants
  (`scopeFromWf:`, `launchedByFromWf:`, `workflowStatusFromWf:`) and a
  `failWorkflow:scope:reason:` overload; `tryFireTransition:` action
  exception handlers and the post-action status check now use scope-aware
  rdp lookups instead of full-table scans. See
  `docs/scout-perf-survey-2026-04-28.md` §3.2.
- Memoised four hot paths flagged in
  `docs/scout-perf-survey-2026-04-28.md` §6:
  - `TemplateLoader>>reloadTemplate:into:` now caches parsed payloads
    keyed by template identity + file mtime. WorkflowEngine ticks no
    longer re-parse and re-pin the same CUE template every tick.
  - `ApiServer>>watchWorkflowsFor:` and
    `DashboardSSE>>watchWorkflowsFor:in:` cache the resolved workflow-id
    Array per identity for 2 s, halving the per-request /
    per-subscriber walk over the watches set.
  - `Repo>>repoForName:` is now backed by a class-side TTL cache (5 s)
    with explicit invalidation from `pp repo add` / `pp repo remove`,
    eliminating repeated `~/.pp/repos/<name>.json` reads on the
    Scheduler / WorkflowEngine / CreateWorktreeAction /
    DispatchWavesAction hot paths.
  - `SignatureVerifier>>verify:` caches resolved `ActorContext` keyed by
    `(actor, signature)`. Skew is still checked on every hit; rotation-
    mode (`requireOldPub:`) calls deliberately bypass the cache.

### Changed
- `/api/notifications/stream` long-poll replaced with pub/sub fan-out
  via the new `NotificationHub`. Previously a blocked `pp watch` client
  ran up to 20 iterations × 2 full-table scans (`scanAll: 'notification'`
  + `scanAll: 'watch'`) per 10 s window under the BBS mutex, so a
  handful of concurrent watchers could DOS the rest of the server. Now
  the handler does one initial backlog `scanAll`, then subscribes a
  filter block to `NotificationHub` and sleeps on its own per-subscriber
  pending queue. BBS invokes the hub once per notification write
  (outside the index mutex). See
  `docs/scout-perf-survey-2026-04-28.md` §2.
- Dashboard SSE broadcaster computes ONE snapshot per 2 s tick and fans
  it out to every subscriber. Previously each subscriber re-ran ~12 BBS
  full-index scans (`workflow`/`token`/`task`/`workitem`/`event`/
  `worker`/`notification`/`watch`) and N synchronous session-file reads.
  `DashboardSSE>>tick` now runs all scans once, pre-renders every
  identity-independent panel (workflows/completions/workitems/scope-
  violations/presence/anonymous-notifs), and `broadcastTo:snapshot:`
  enqueues the cached HTML for each subscriber. Per-identity notification
  filtering still runs per signed subscriber but reuses the snapshot's
  cached `notifications` and `watch` arrays. See
  `docs/scout-perf-survey-2026-04-28.md` §4.
- `DashboardSSE>>tokenTotalsFor:in:` no longer reads
  `$HOME/.pp/sessions/<taskId>.jsonl` from disk on the broadcast hot
  path. A background `[self tokenCacheLoop] fork` (started in
  `initBBS:`) rebuilds an in-memory taskId → {input, output} cache every
  5 s; `tokenTotalsFor:in:` now sums cache entries with zero I/O. A slow
  filesystem can no longer stall every SSE subscriber's update.
- BBS index restructured for O(1) lookup. The flat `index` array is
  retained for `scan:` / `scanAll:` full scans, but write/remove paths
  now also maintain three hash indices: `byId` (id → tuple), `byKey`
  (`category|scope|identity` → tuples), and `byCatIdent`
  (`category|identity` → tuples). `findInIndex:` is now an O(1) hash
  lookup, and a new `findByCategory:identity:` resolves tuples whose
  scope is unknown without scanning. Hot callers in `Server`,
  `WorkflowEngine`, and `DispatchWavesAction` that previously did
  `(bbs scanAll: …) detect: [:t | (t at: 'identity') = id]` now use the
  hash lookup. Removes O(n) per-write/per-consume work from every BBS
  mutation. See `docs/scout-perf-survey-2026-04-28.md` §1.1.
- Dispatcher tick no longer blocks on the BBS save-to-disk path. The 10s
  tick now calls `bbs flushAsyncIfDirty`, which forks a fenced background
  write so a multi-MB JSON encode + atomic rename can never stall the
  scheduler / workflow-engine / reaper loop. CLI request paths
  (`outSync:` / `inpSync:`, `/api/bbs/out`, `/api/bbs/rm`) keep their
  synchronous `flushIfDirty` durability contract, but now wait on the
  same fence so the two writers can never race on `bbs.json.tmp`. See
  docs/scout-perf-survey-2026-04-28.md §1.3.
- BBS history rotation no longer forks `stat` on every tuple write.
  `appendHistory:` / `logEngine:` now track `history.jsonl` size in
  memory, stat'ing once lazily on the first call after process start
  and resetting the counter on rotation. Removes one fork+exec per
  `out:` / `outPinned:` / `outAffine:` / `inp:` / `update:` call. See
  `docs/scout-perf-survey-2026-04-28.md` §1.2.

### Added
- `Shell run:timeout:`, `Shell capture:timeout:`, and
  `Shell runChecked:timeout:` variants that wrap any shell command in a
  POSIX watchdog (SIGTERM after N seconds, SIGKILL 1s later). Exits with
  124 on timeout to match GNU `timeout` convention.

### Fixed
- `BBS>>outAffine:` now provides true overwrite semantics: a write at an
  existing `(category, scope, identity)` consumes the prior tuple before
  appending the fresh one. Previously each call appended a new tuple and
  callers (worker register/heartbeat, workflow watch) used a racy
  `inp:`-then-`outAffine:` workaround that could leak duplicate tuples on
  crash, double-counting workers in the dashboard presence panel. The
  workarounds in `Server.mag` (handleWorkerRegister, handleWorkerHeartbeat,
  handleWorkflowWatch) are removed. See
  docs/scout-perf-survey-2026-04-28.md §5.3.
- Dispatcher tick no longer stalls indefinitely on a stuck `git` lock or
  paused NFS mount. All inline `Shell run:` / `Shell capture:` calls on
  the tick path now have wall-clock caps and fail soft: branch-existence
  probe in `DispatchWavesAction` (10s), `GitOps` write/push operations
  (30s/60s), worktree cleanup `rm -rf` in `WorkflowEngine` (30s), and
  BBS persistence mkdir/mv/stat/rotation calls (5–10s). Reference:
  docs/scout-perf-survey-2026-04-28.md §5.5.

### Added
- `pp bbs` subcommand for tuplespace inspection and manipulation
  (`list` / `get` / `put` / `rm`). See README → `pp bbs` for the full
  surface, guarantees (durable writes, category validation, upsert,
  idempotent rm), and worked examples.
- `pp bbs put <category> <scope> <identity> <payload>` and
  `pp bbs rm <category> <scope> <identity>` CLI subcommands, implementing
  the write half of `pp bbs`. `<payload>` accepts either inline JSON or
  `@path/to/file.json`. Optional flags: `--pinned`, `--ttl SEC`,
  `--modality <persistent|linear|affine>` (defaults driven by category —
  pinned categories default to `persistent`). `put` prints
  `<id> created|updated` on success; `rm` is idempotent and prints
  `removed <cat>/<scope>/<identity>` or `no such tuple`. Invalid
  categories surface the server's 400 message (including the valid
  category list) and exit non-zero. `pp bbs` usage text updated to
  document all four subcommands + flags.
- `POST /api/bbs/put` and `POST /api/bbs/rm` — unsigned HTTP routes for local
  CLI inspection/ops. `put` performs an UPSERT (consumes any existing tuple
  with the same `(category, scope, identity)` triple before writing the new
  one) and reports `created:true|false`; `rm` consumes by composite key and
  reports `removed:true|false` (idempotent — repeated `rm` is not an error).
  Both flush BBS state synchronously before responding so a SIGKILL after
  the ack does not lose the mutation. Tuple ids are server-generated; any
  client-supplied id is ignored. Match the unsigned posture of `/api/rdp`
  and `/api/scan` — auth hardening tracked separately.
- `BBS>>outSync:scope:identity:payload:` and `BBS>>inpSync:scope:identity:` —
  synchronous-flush variants of `out:` / `inp:` for CLI-facing mutations
  that need durability before returning to the caller. Wrap the existing
  async-dirty-flag path with a trailing `flushIfDirty`; the default path
  is unchanged so the engine is not serialized on disk I/O. Chose new
  selectors (option b) over a keyword `sync:` arg because `out:` already
  has a dense stack of arities (actor:, launchedBy:, executedBy:) and
  adding a boolean to every one would have doubled the surface area.
- `pp bbs list` and `pp bbs get` read-only subcommands for direct BBS
  tuplespace access. `list` supports `--category`, `--scope`, `--identity`,
  and `--json` filters; invalid `--category` values fail fast with the
  valid set listed. `put` and `rm` are stubbed (exit 2) pending
  story:bbs-cli:write-cmds.
- `pp workitem show <id>` now lists related workflows with their status
  (running / completed / failed). Failed entries include the failure
  reason and a `retry: pp workitem run <id>` hint so operators notice
  mid-run crashes and know how to re-dispatch.
- `pp serve` now writes durable crash logs to `~/.pp/logs/crash-<epoch>.log`
  (plus a rolling append to `~/.pp/logs/pp-serve.log`) when the server
  exits abnormally. Previously a silent crash left no breadcrumb — operators
  had to infer the failure from `pp workflow status` returning
  "no response from server".
- `scripts/pp-supervisor.sh`: auto-restart wrapper for `pp serve` with a
  burst-window circuit breaker (`PP_SUPERVISOR_MAX_RESTARTS` per
  `PP_SUPERVISOR_BURST_WINDOW` seconds) and per-run + rolling log capture.

### Changed
- Dashboard "Recent Activity" list now renders newest-first instead of
  newest-last, surfacing the most recent notification at the top of the
  panel without forcing operators to scroll.
- `Scheduler>>dispatchTask:` now wraps the entire post-dispatch sequence
  (harness run + scope validation + task-complete safety net + BBS status
  update + slot release) in a coarse exception handler. An uncaught fault
  in the forked goroutine previously could — and did — tear down the whole
  server process; it now degrades to an error log and a best-effort slot
  release.

### Fixed
- Dispatcher tick loop is now resilient to malformed tuples. Each step
  (`scheduler checkCompleted`, `workflowEngine advance`, `ruleEngine
  evaluate`, `scheduler dispatch`, reapExpiredClaims, flushIfDirty,
  reconcile, housekeep) runs inside its own `on: Exception do:` so a
  single bad tuple no longer kills the entire tick — the remaining steps
  still run. Added `ifAbsent:` defaults to `WorkflowEngine` reads of
  `start_places`, workflow `payload`/`status`, template `payload`/
  `transitions`, token `place`, and to `RuleEngine`'s `consumes` lookup
  so missing keys degrade gracefully instead of raising. Refs:
  docs/scout-perf-survey-2026-04-28.md §5.1.
- `Scheduler>>checkCompleted` no longer raises `doesNotUnderstand` on every
  drain of `task-complete:<id>` events. The site at `Scheduler.mag:256`
  invoked `String>>copyFrom:` with a single argument; refactored to a
  testable class method `Scheduler taskIdFromCompleteEvent:` using the
  canonical `copyFrom:to:` form (0-indexed half-open). Adds unit coverage
  in `test/dispatcher/test_scheduler_complete_event.mag`. Reference:
  docs/scout-perf-survey-2026-04-28.md §5.2.
- Strict input validation at every pp boundary (pp-input-validation-strict,
  umbrella for four child bugs):
  - `pp workitem run/show/update/comment` now require `--repo <scope>` (or
    `PP_SCOPE`); when omitted, the CLI errors pre-flight and lists the
    scopes where the identity actually lives. No more silent dispatch to
    the `default` scope.
  - `pp workitem update <id>` with no field flags is rejected as a no-op
    error instead of silently stamping `updated_at` and passing an empty
    payload through. The server-side handler is already PATCH-merge, so
    the client now refuses to send vacuous updates.
  - Workflow templates may declare `required: [...]`. `WorkflowEngine`
    validates required params are present and non-empty at instantiation
    and raises with the missing names — no more empty-prompt dispatch.
    `workflows/story.cue` and `workflows/story-lite.cue` now declare
    `description` as required.
  - `ClaudeHarness` refuses to spawn Claude when the task's rendered
    description is empty; sets `failureReason` so `WorkerAgent` marks
    the task failed with an actionable diagnostic ("run pp workitem
    update <id> --description ...") instead of letting the CLI exit 1
    and leaving the state machine to guess.
- Failed workflows that never produced commits now clean up after
  themselves: `WorkflowEngine>>failWorkflow:reason:` removes the worktree
  and deletes `feature/<instance>` + `impl/<instance>` when both branches
  are zero commits ahead of `main`. Previously a crash between
  `create-worktree` and the first implementer commit left an empty
  `feature/<instance>` behind, and re-dispatching the workitem hit
  "branch already exists". Worktrees with unique commits are still
  preserved for recovery.
- `merge-worktree` on standalone story/story-lite/hotfix/spike workflows now
  fast-forwards the feature branch into `main` and pushes `origin/main`
  (best-effort) instead of leaving the commits stranded on `feature/<id>`.
  Previously the action emitted `merge-complete` without touching `main`,
  so work-items closed as "done" while nothing landed. The
  `merge-complete` observation now carries `merged_to_main: 'true' | 'false'`
  so downstream consumers can disambiguate.

### Changed
- `CreateWorktreeAction` records a `standalone` flag (and `parent_branch`
  when applicable) on the `worktree` signal so `MergeWorktreeAction` can
  distinguish wave-children (parent pipeline owns the main merge) from
  standalone workflows (self-managed main merge).
- Added `GitOps pushBranch:in:` helper.
