# Scout critique: doctrine-layer design vs. the real codebase

**Date:** 2026-06-24
**Scope:** adversarial review of `docs/design-doctrine-layer.md` and `epic:doctrine`'s 6 stories
**Mode:** research only — no code, no implementation. Recommendations only.

## TL;DR verdicts

| Area | Verdict | One-line reason |
|------|---------|-----------------|
| (1) Data model fit | **risky** | Category/durability fit is clean, but `out:` is append-only with no unique key — stable-slug reuse duplicates/clobbers. `pinned` classification is ambiguous. |
| (2) Plan-time consumption | **wrong (as scoped)** | `assembleContext:` has no scope-union, no tag-intersection, **no maturity filter**. "Add to softCategories" leaks proposed/rejected doctrine and misses global scope. Wave-2 is badly underestimated. |
| (3) Synthesis / Strategist | **risky** | Mechanism is real (Archivist precedent) but the doc collapses 3–4 stories ("role", "emit schema", "engine-side writer/consumer", "CLI trigger") into one bullet. "CALL function, runs in PP" contradicts "Strategist role." |
| (4) Maturity + conflict | **risky** | `update:` transitions work in both stores. But "propose-new, human merges" is **not** safe with stable slugs + append `out:`. Provenance dangles after `case` GC. |
| (5) Loose coupling / pudl export | **sound** | There is literally zero pp→pudl coupling in the codebase today. Export-as-sink is honest; no hidden read dependency exists. |
| (6) Scope/tags | **risky (net-new)** | No prior art for tag-based querying. Tags are *written* (CaseBuilder/CaseEnricher) but nothing *filters* by them. Tag intersection is new query machinery (scan + in-memory select; no index). |

---

## (1) DATA MODEL FIT — risky

### What's actually true
- **Adding the category is trivial and correct.** `Categories>>valid` (src/bbs/Categories.mag:5) is a flat array; add `'doctrine'`. `BBS>>isDurableCategory:` (src/bbs/BBS.mag:922) is a flat array; add `'doctrine'` so it persists across restart (json path) — identical to how `'case'` is handled.
- **Payload round-trips fine in BOTH stores.** Payloads are JSON-encoded blobs. GansoStore stores the whole tuple as JSON `data` (src/bbs/GansoStore.mag:34–44) and only `category/scope/identity/modality/created_at/expires_at/parent/wf_inst` are STORED generated columns. Nested `tags[]`, `provenance[]`, numbers, strings all round-trip through `Json encode`/`Json decode` on both the SQLite path and the in-memory json path. **No payload-shape concern.**
- **The "byCategory only on json path" note is a non-issue for doctrine reads.** `scan:`/`scanAll:` delegate to `gansoStore scan:` (SQL, indexed by `ix_cat_scope`/`ix_cat`) under ganso and to the in-memory `byCategory` bucket otherwise (src/bbs/BBS.mag:470–505). Both paths work; both are O(matches). Doctrine's expected N is small, so even an unindexed in-memory scan is fine.

### The real problems
- **`out:` is append-only; there is no unique constraint on `(category,scope,identity)`.** `BBS>>out:` (src/bbs/BBS.mag:300) appends; `GansoStore>>putTuple:` (src/bbs/GansoStore.mag:211) is a bare `INSERT`. Only `outAffine:`/`upsertPinned:`/`upsertSignal:` replace-by-key. So if synthesis writes a `proposed` doctrine with the **same stable slug** as an existing `active` one via `out:`, you get **two tuples at the same key**: `scan:` surfaces both, and `rdp:`/`update:` see only the *latest* (the new `proposed`), shadowing the `active` entry. This is the clobber the doc hand-waves in §"Hard part". **The data model as written (stable slug + propose-new) is unsafe with `out:`.**
- **`pinned` classification is genuinely ambiguous and the doc ignores it.** `'case'` is in BOTH `Categories>>pinned` (src/bbs/Categories.mag:25) AND `isDurableCategory:`, yet it is written **linear** via `out:` (WorkflowEngine>>writeCaseSkeleton, src/dispatcher/WorkflowEngine.mag:734) and mutated via **linear** `update:` (CaseEnricher, src/dispatcher/CaseEnricher.mag:40). The `pinned` list is only consulted by the generic Server write path (`Categories isPinned:` → modality, src/api/Server.mag:524) — so a generic `pp put doctrine ...` would write **persistent/pinned**, which (per the writeCaseSkeleton comment, lines 729–733) is **invisible to `rdp:`-based existence checks**. The doc must pick a lane: **doctrine should be linear + durable (mirror `case`), NOT pinned**, and the `pp doctrine` writer must use `out:`/`update:` directly rather than the generic put path.

### Doc edits
- §"Data model": state explicitly **"linear + durable, mirrors `case`; NOT added to `Categories>>pinned`. Written/mutated via `out:`/`update:` directly, never the generic pinned put path."**
- §"Data model": change `identity: <stable slug>` to **`identity: <unique slug, e.g. dctr-<short-id>>`**; reserve the human-readable slug as a payload field (`key`/`title`), because `out:` cannot safely reuse keys.
- §"Hard part": replace the clobber hand-wave with the concrete mechanic above (append-not-replace; rdp returns latest).

---

## (2) PLAN-TIME CONSUMPTION — wrong as scoped; wave-2 underestimated

### What `assembleContext:` actually does (src/roles/Role.mag:173–236)
1. `scanAll: 'convention'` (global) — always included.
2. For each hard/soft category: `scan: cat scope: scope` → `filterByWorkflow:` → `mostRecent: N`.

That is the **entire** retrieval model. Consequences for doctrine:
- **Global doctrine would be invisible.** Only `convention` gets `scanAll` (all scopes). Everything else is scanned at the *workflow's* scope only. The design's whole premise is `scope ∈ {global, this-repo}` — `assembleContext:` does not union scopes for anything but convention.
- **No tag intersection, no maturity filter.** Adding `doctrine` to `softCategories` would pull in `proposed`, `rejected`, and `retired` entries indiscriminately — **unreviewed proposed doctrine would leak straight into planning**, which defeats the human gate. This is a correctness/safety bug, not a polish item.
- **Recency truncation, not relevance.** `mostRecent: N` keeps the tail by monotonic id. Doctrine is curated and long-lived; "most recent N" is exactly the wrong selector, and with `contextBudget(60)` split evenly across categories, doctrine gets a small slice.
- **Scout is the wrong second consumer.** The doc says "planner/scout." `Scout` (src/roles/Scout.mag) has `hardCategories #('fact')`, `softCategories #('convention' 'signal')`, `contextBudget 30` — a *research* role, not a planning role. The genuine plan-time consumer is `Planner` (budget 60). Wiring doctrine into Scout is a separate, smaller decision and probably out of scope for v1.

### The doc's own note understates the work
The doc says adding to `softCategories` "yields raw category read only — the scope/tag intersection + composition needs custom context assembly beyond that." True, but it omits: **(a) global-scope union, (b) maturity gating to `active`-only, (c) relevance (not recency) selection.** This is a **new `assembleDoctrineContext:`-style method** plus a hook into `Planner` prompt assembly — not a `softCategories` edit. Wave-2 is **underestimated**: it is the hardest story in the epic, not a follow-on toggle.

### Doc edits
- §"Later/open" first bullet: rewrite to enumerate the three missing capabilities (global-union, maturity=active gate, relevance selection) and state that doctrine must **bypass** the generic `softCategories` path (which would leak proposed/rejected and miss global scope).
- §"Pipeline skeleton": change "consumed by planner/scout" → **"consumed by planner (scout TBD)"**.

### Story edits
- The wave-2 plan-time story must be **split into two**: (2a) a doctrine retrieval function (`active`-only, scope-union, tag-intersection) with unit tests; (2b) wiring that function into `Planner` context assembly. Size them as the epic's largest, not smallest.

---

## (3) SYNTHESIS / Strategist — risky; under-decomposed

### The real mechanism (Archivist is the precedent)
The Archivist loop is the template the Strategist must follow, and it is **multi-part**:
1. **Role** (read-only/write-to-KB): `Archivist` reads tuples and **emits a JSON observation** `pp observe case-enrichment '{...}'` (src/roles/Archivist.mag:31–35). It does **not** write the durable canonical tuple itself.
2. **Engine-side consumer**: `WorkflowEngine>>findArchivistEnrichment:` + `applyArchivistEnrichment:` + `applyCompletedArchivistEnrichments` (a Dispatcher-tick sweep) consume that observation and merge it via `CaseEnricher` (src/dispatcher/WorkflowEngine.mag:796–940). This wiring was **its own story** ("consuming the archivist's output ... is the integration story" — comment at line 755).
3. **Dispatch**: `dispatchArchivist:` enqueues a `task` tuple with `role='archivist'` (line 742); the harness spawns it. Roles are resolved via `HarnessFactory` (src/harness/HarnessFactory.mag:12–21) — **a new role must be registered there**.

So the Strategist analog is genuinely **3–4 stories**, not one:
- (3a) `Strategist` role definition + register in `HarnessFactory`.
- (3b) The **emit schema** (what JSON the Strategist outputs: clustered/deduped/scored proposed-doctrine objects with provenance) — analogous to the documented Archivist enrichment contract.
- (3c) A **`DoctrineWriter`/consumer** (CaseEnricher analog) that validates the emitted proposals and writes `proposed` doctrine tuples with fresh unique identities. Doctrine should not be written directly by the LLM agent for the same reason enrichment goes through CaseEnricher (validation + additive guarantees + off-critical-path).
- (3d) The **`pp doctrine synthesize`** trigger that enqueues a `strategist` `task` (mirrors `dispatchArchivist:` but CLI-initiated). It maps cleanly onto existing task dispatch.

### The doc contradicts itself
§"Pipeline skeleton" says **"Strategist role: ... (CALL function, runs in PP)"** — an in-process function — but §MVP says **"Strategist role + manual synthesis."** Pick one. The codebase precedent is unambiguous: **an LLM role agent emits, an engine-side pure consumer writes.** "CALL function, runs in PP" is misleading; the clustering/dedup/scoring is the *agent's* job (LLM), and only the validate-and-write is a pure PP function.

### Doc edits
- §"Pipeline skeleton" / §MVP: delete "(CALL function, runs in PP)"; state the two-actor mechanism (agent emits proposal observation → engine `DoctrineWriter` validates + writes proposed tuples), citing the Archivist→CaseEnricher precedent.
- §MVP item 3: split into the four sub-stories above.

### Story edits
- Add stories: **"Register Strategist role"**, **"Strategist emit schema + role prompt"**, **"DoctrineWriter consumer + tick sweep"**, **"`pp doctrine synthesize` enqueues strategist task"**. The current single "Strategist + manual synthesis" story is too coarse to estimate or review.

---

## (4) MATURITY + CONFLICT — risky

- **Transitions are feasible.** `proposed→active→retired/rejected` as a payload field flipped via `BBS>>update:` works in both stores (src/bbs/BBS.mag:587–623 json; GansoStore>>update: src/bbs/GansoStore.mag:187 — read-mutate-`upsert:`, which *does* replace by key). `update:` on a durable category reflushes, so transitions survive restart. **Use `update:` for transitions, addressed by the tuple's unique identity.**
- **Reconciliation punt is NOT safe as written.** "propose-new, human merges against existing" + stable slug + `out:` = duplicate keys / shadowing (see Area 1). It is only safe if **every proposal gets a fresh unique identity** and the human merge is "promote one, `retire`/`reject` the rest." The doc must say this, or reconciliation will silently corrupt the active set.
- **Provenance dangles after GC.** `pp gc` sweeps `case` tuples, default keep 2000 (`SystemCommands>>gcCases:`, src/cli/SystemCommands.mag:433–464). Doctrine `provenance[]` pointing at swept cases will dangle. Doctrine itself is *not* swept (gc targets only `'case'`), which is correct — but provenance resolution must tolerate missing cases.
- **Provenance needs scope, not bare identity.** Cases are scoped per workflow (`rdp: 'case' scope: scope identity: instanceId`). A bare case-identity in `provenance[]` is only resolvable via `findByCategory:identity:` (scope-agnostic, returns latest — src/bbs/BBS.mag:571). Either store `{scope, identity}` pairs or commit to `findByCategory:`.

### Doc edits
- §"Maturity ladder" / §"Hard part": require **fresh unique identities per proposal**; define human-merge as **promote-one + retire/reject-duplicates**.
- §"Data model": note `provenance[]` entries should be resolvable (store scope or rely on `findByCategory:`) and **may dangle after `case` GC** — resolution must be null-tolerant.

### Story edits
- Add a small story: **"Doctrine maturity transitions via `update:` + retire/reject"** (the `pp doctrine promote/retire/reject` verbs), explicitly distinct from synthesis.

---

## (5) LOOSE COUPLING / pudl EXPORT — sound

- **There is no pp→pudl/nous integration in the codebase.** A full `*.mag` grep for `pudl`/`nous` returns only the substring `synchronous` — i.e. **zero** actual references. So "pudl is a downstream export sink, not an upstream read dependency" is not just a good decision, it is **the current reality**: there is no hidden coupling to undo, and no read path to accidentally depend on.
- The DDIL/comms-denial rationale is consistent: keeping Orient local means nothing in the plan-time path can block on an external service that doesn't even exist in the code yet.
- One caveat: the *only* "pudl" tool on PATH in this environment is an unrelated data-lake CLI (`pudl catalog/import...`) with **no `observe` subcommand** (the SessionStart "pudl observe" protocol fails here). If doctrine export is ever built, confirm the actual target tool/protocol — the export sink is currently undefined, so the story should be a **stub/no-op placeholder**, not a real integration.

### Doc edits
- §"Coupling decision": add a line — "no pp→pudl integration exists today; export is greenfield and remains out of MVP. Target protocol is undefined and must be confirmed before any export story."

### Story edits
- Keep pudl export **out of the 6 MVP stories** (the doc already defers it). If a placeholder story exists, mark it **stub only**.

---

## (6) SCOPE/TAGS — risky (net-new query machinery)

- **No prior art for tag-based querying.** Tags are *produced* (`CaseBuilder` builds `tags[]`, src/dispatcher/CaseBuilder.mag:94–110; `CaseEnricher>>mergeTags:` unions them) and *emitted* (Archivist), but **nothing reads-by-tag**. `scan:` filters category+scope only; the only payload-field filters that exist are `filterByWorkflow:` (payload.workflow_instance, Role.mag:251) and `childrenOfParent:` (payload.parent, BBS.mag:509) — both **in-memory `select:` after a scan**.
- So tag intersection is **net-new** but follows an existing pattern: `scan: 'doctrine' scope: ...` → in-memory `select:` on `tags ∩ context`. **No GansoStore generated column / index for tags** (src/bbs/GansoStore.mag:34–50 indexes only parent/wf_inst among payload fields), so it is O(scan). Fine at doctrine's small N; do **not** claim it reuses existing query infrastructure.
- Scope filtering (`global | repo:X | path:X`) has partial prior art (scope is a first-class column), but the **union** of `{global, this-repo}` for a single retrieval does **not** exist anywhere except the convention special-case (`scanAll`).

### Doc edits
- §"Contextuality": state that tag intersection and scope-union are **new retrieval code** (scan + in-memory select, no tag index), modeled on `filterByWorkflow:`/`childrenOfParent:` — not reuse of existing query machinery.

### Story edits
- Fold the tag/scope retrieval into story (2a) above (the doctrine retrieval function); it is the same code.

---

## Consolidated recommendation for `epic:doctrine`'s 6 stories

**Re-scope / re-sequence (proposed wave map):**

- **Wave 1 (foundation):**
  1. `doctrine` category — add to `Categories>>valid` + `BBS>>isDurableCategory:`; **linear + durable, NOT pinned**; unit test round-trip through both stores (json + ganso). *(was MVP-1; tighten the pinned/linear decision)*
  2. `pp doctrine list/show` (read) + `pp doctrine propose` (write via `out:` with **fresh unique id**) + `pp doctrine promote/retire/reject` (via `update:`). *(splits old MVP-2/maturity)*
- **Wave 2 (synthesis loop — split the old single story):**
  3. `Strategist` role + register in `HarnessFactory`.
  4. Strategist emit schema + role prompt (cluster/dedup/score; provenance back to cases).
  5. `DoctrineWriter` consumer + Dispatcher-tick sweep (validate emitted proposals → write `proposed` doctrine), mirroring `CaseEnricher`/`applyCompletedArchivistEnrichments`.
  6. `pp doctrine synthesize` — enqueue a `strategist` `task` (mirror `dispatchArchivist:`).
- **Wave 3 (the payoff — the hardest, do NOT treat as a toggle):**
  7. (2a) doctrine retrieval function: `active`-only + scope-union(`{global,this-repo}`) + tag-intersection; unit-tested in isolation.
  8. (2b) wire (7) into `Planner` context assembly (bypassing the generic `softCategories` path).

**Missing stories the epic should add:**
- Maturity transition verbs (`promote/retire/reject`) as a first-class story (Area 4).
- DoctrineWriter validation/idempotency (fresh-id guard; never reuse an active slug) (Area 1/4).
- Provenance null-tolerance + scope-aware resolution + dangling-after-GC handling (Area 4).
- Explicit "doctrine is exempt from `pp gc`" assertion/test (Area 4) — currently true by omission; lock it with a test.

**Stories to drop/defer from MVP:**
- Any Scout wiring (use Planner only) (Area 2).
- pudl export (already deferred; keep it a stub, target protocol undefined) (Area 5).

**Single biggest correction:** the design treats plan-time consumption as a later, small "add doctrine to softCategories" follow-on. In reality it is the **hardest** story (new retrieval with maturity gate + scope union + tag intersection, none of which `assembleContext:` does) and, if shipped naively, would **leak unreviewed `proposed`/`rejected` doctrine into planning** — defeating the human gate that is the whole point of the maturity ladder.
