# Multiplayer / Collaborative Features for Procyon Park

**Scout:** research mission 1776559250
**Date:** 2026-04-18
**Scope:** What multiplayer could mean for PP, what's feasible, and what Maggie needs.

---

## Executive Summary

Procyon Park today is a single-user orchestrator: one human, one BBS tuplespace, one
dispatcher tick loop, N forked Claude harness processes on one host. "Multiplayer"
could mean three meaningfully different things:

1. **Team-collaborative single instance** (multiple humans observing/steering one PP)
2. **Federated observability** (multiple PP instances sharing streams read-only)
3. **Distributed compute** (workflows whose tasks run on peer nodes)

Maggie already ships — surprisingly — most of the primitives needed for #3: `Node`,
`RemoteProcess`, remote spawn (CBOR-serialized blocks), `Cluster` with
`HashRing`, `DistributedSupervisor`, cross-node monitors/links. What Maggie does
**not** have is a **distributed TupleSpace** — and PP's entire coordination model
is the BBS tuplespace, so that gap is load-bearing.

The highest-ROI feature is #1 (team-collab) because it requires essentially no
new Maggie primitives — PP already has HTTP+SSE and the BBS is a single
shared resource. The lowest-ROI feature given current maturity is #3, because
it depends on cross-node tuplespace semantics that don't exist yet.

---

## 1. Maggie's Distributed Primitives — What Exists Today

Surveyed `<maggie-repo>/lib/` and `<maggie-repo>/vm/`.

### Present and usable

| Primitive | File | Notes |
|---|---|---|
| `Node connect:` | `lib/Node.mag`, `vm/node_*.go` | TCP to peer node, `ping`, `processNamed:`, `nodeID` (32-byte pubkey) |
| `RemoteProcess` | `lib/RemoteProcess.mag` | `cast:with:` fire-forget, `asyncSend:with:` → `Future` |
| Remote spawn | `vm/remote_spawn.go` | `SpawnBlock` ships CBOR-serialized block + upvalues by content hash |
| Remote channels | `vm/remote_channel.go` | `RemoteChannelRef` proxies send/recv across nodes |
| Remote monitors/links | `vm/remote_monitor.go`, `remote_lifecycle.go` | `__down__`/`__exit__` infrastructure selectors |
| `Cluster` | `lib/Cluster.mag` | Seed-based membership, `onMemberUp:`, `onMemberDown:`, heartbeat via `NodeHealthMonitor` |
| `HashRing` | `lib/HashRing.mag` | Consistent-hash placement (150 vnodes default), `nodeFor:`, `nodesFor:count:` |
| `DistributedSupervisor` | `lib/DistributedSupervisor.mag` | Supervises children on remote nodes, round-robin placement, restart-on-node-failure |
| Wire layer | `vm/dist/` | CBOR, chunker, identity (Ed25519-ish NodeIDs), trust, content store |
| gRPC client | `vm/grpc_primitives.go` | Reflection-based dynamic dispatch to external services |
| CancellationContext | `vm/cancellation.go` | Propagates across `withContext:` forks; TupleSpace has `out:withContext:` hooks |

### Present but single-node

| Primitive | Gap |
|---|---|
| `TupleSpace` (`vm/tuplespace_primitives.go`) | **In-process only.** No wire protocol, no replication, no distributed matching. PP's BBS wraps this directly. |
| `ConstraintStore` | Single-VM, no CRDT semantics |
| `Mutex`/`Semaphore`/`WaitGroup` | Wrap Go sync primitives; no distributed equivalents |
| Registry globals | Per `docs/roadmaps/2026-02-03-distributed-runtime-roadmap.md`: "Dual registry architecture" — strings, dicts, classes live in package-level globals. Prevents running two VMs in one process, and complicates cross-node value identity. |

### Entirely absent

- Distributed TupleSpace / Linda federation
- CRDTs (`G-Set`, `OR-Map`, `LWW-Register`, etc.)
- Distributed GC for remote refs
- Distributed consensus (no Raft/Paxos, no quorum writes)
- Gossip (beyond Cluster heartbeat)
- Capability-based security on remote sends (trust layer exists in `vm/dist/trust.go` but not wired through)

Source: `docs/roadmaps/2026-02-03-distributed-runtime-roadmap.md` explicitly
scopes this as ~3 years of work to reach "Erlang-grade." The roadmap is honest
about shared-state issues (`VM.Globals`, `classVarStorage`, global
string/dict registries) that would bite any serious federation.

---

## 2. Candidate Features — Ranked by Value/Effort

Effort scale: S = days, M = weeks, L = months, XL = requires Maggie VM changes.
Value scale: ★–★★★★★.

### Tier 1 — Near-term, high leverage (no Maggie changes required)

1. **Multi-user dashboard presence + live stream fan-out** — ★★★★ / S
   - PP already has `DashboardSSE` (`src/api/DashboardSSE.mag`) and serves static
     assets. Adding per-connection user identity (cookie / header) and an
     on-dashboard "who else is watching" roster is a pure HTTP layer change.
   - Value: transforms the dashboard from a solo cockpit into a team situation
     room. Zero risk to the coordination fabric.

2. **Shared observation/decision/notification stream (read-only federation)** — ★★★★ / S
   - One PP instance can poll another's `/api/scan` endpoints and mirror
     selected categories (`observation`, `decision`, `notification`, `signal`)
     into its own BBS as read-only tuples. Write-through stays local.
   - Effectively a one-way federation without any distributed-tuplespace
     semantics. Conflicts impossible by construction (read-only).
   - Value: teams running per-project PP instances can subscribe to each
     other's decisions/facts without merging tuplespaces.

3. **Human-in-the-loop fan-out** — ★★★ / S
   - Today `pp notify` is one-to-one. Add multi-observer subscriptions keyed by
     scope/category so two humans can both receive prompts. Implementation is
     pure HTTP/SSE + a subscribers tuple.

4. **Remote `pp` CLI against any PP instance** — ★★ / S
   - Already possible via `PP_URL`. Document it, add auth (shared secret or
     Ed25519 signing via Maggie's existing identity primitives), ship.

### Tier 2 — Medium-term, moderate value (minor Maggie additions)

5. **Cross-instance agent spawning on peer PP nodes** — ★★★★ / M
   - Rather than federating tuplespaces, federate the *dispatcher*: a task in
     scope `peerA:*` is dispatched against peer A's `Scheduler` via HTTP.
     Reuses the existing `ClaudeHarness` spawn path on the peer. Completion
     signals flow back as tuples via the federation channel from feature #2.
   - Enables "my laptop is out of slots; run this reviewer on the build box."
   - Requires: no Maggie changes; just a new `/api/dispatch-remote` endpoint
     and a `RemoteHarnessFactory` that posts to a peer instead of
     `Process fork`ing.

6. **Collaborative workitem editing (last-write-wins)** — ★★★ / M
   - Workitems are tuples with a `version` field today (check
     `src/bbs/WorkItemFields.mag`). Add optimistic concurrency on `/api/out`
     for category `workitem` — reject writes whose `version` is stale; force
     clients to refetch-and-merge. This is CRDT-lite and good enough for
     human-paced editing.

7. **Distributed workflow execution via remote tasks** — ★★★ / M
   - CUE workflow templates already declare `scope` and `role` per task. Add
     an optional `node:` hint. `Scheduler dispatch` consults `HashRing` /
     explicit hint to decide local vs. remote. Uses Maggie `Cluster` directly.
   - Gating: tasks are already async and idempotent via the BBS
     `task-complete` tuple, so remote execution composes cleanly **iff**
     the `task-complete` tuple can be written back to the originating BBS.
     That requires feature #2 (stream federation) plus a writable channel,
     which is still feasible with HTTP, no distributed tuplespace needed.

### Tier 3 — Long-term, requires Maggie VM work

8. **Federated tuplespaces (true Linda federation)** — ★★★★★ / XL
   - The dream: `bbs in: template scope: 'peer:*'` blocks until a peer
     publishes a matching tuple. Requires:
     - Distributed TupleSpace primitive in Maggie (CUE-unification-over-wire)
     - Decision on consistency: eventual (gossip) vs. consensus (CP)
     - Cross-node waiter registration and cancellation
     - Distributed GC for affine/TTL tuples
   - Without this, PP's coordination model stops at a single host.

9. **Petri-net workflow execution across cluster nodes** — ★★★ / XL
   - Depends on #8. Transitions fire when tokens from multiple scopes
     unify — if those scopes live on different nodes, you need distributed
     unification and two-phase commit semantics for transition firing.

10. **Capability-scoped remote agent harness** — ★★★ / L
    - Use Maggie's `vm/dist/trust.go` identity/trust layer to gate which peer
      can dispatch which role. Requires wiring the currently-dormant trust
      machinery into Maggie's `Node` accept path and exposing it as Maggie
      API.

### Tier 4 — Speculative

11. **Gossip-based fact propagation with CRDT merge** — requires new Maggie primitives; valuable for `fact`/`convention` categories but overkill until there are ≥4 instances in the wild.
12. **Remote object references with distributed GC** — Maggie roadmap item, years out.

---

## 3. Recommended Path

**Ship Tier 1 first.** It delivers 70% of what teams actually want (shared
visibility) with zero Maggie changes and low blast radius. In particular:

- Feature #1 (dashboard presence) is a weekend project.
- Feature #2 (read-only stream mirror) can be built entirely from existing
  `/api/scan` and `/api/sse/dashboard` endpoints plus a new
  `federation-peer` category for configuration.

**Tier 2 gates on one design question**: should PP model peers as Maggie
`Node` connections or as HTTP clients? Recommendation: **HTTP first** — PP
already speaks JSON+SSE to the outside world; adding Maggie `Node` links
introduces a second transport with no user-visible benefit until you need
transparent remote processes. Revisit after feature #5 (remote dispatch)
proves the federation pattern.

**Tier 3 is a Maggie-team dependency**, not a PP-team dependency. The right
PP-side move is to keep the BBS interface narrow (it already is:
`out`, `in`, `rd`, `scan`, `inp`, `rdp`) so that swapping in a distributed
tuplespace later is a local change.

---

## 4. Gap Analysis — What Maggie Specifically Needs

Minimum set to unlock Tier 3 in PP, stack-ranked by dependency order:

1. **Per-VM string/dictionary registries** (remove package globals per
   the roadmap). Prerequisite for any cross-VM value identity.
2. **Wire-serializable `Dictionary`/`String`/`Symbol`** via CBOR extension
   tags. Partially present in `vm/dist/` for classes; extend to values.
3. **Distributed TupleSpace** with two modes:
   - Eventual (gossip + LWW) for `fact`/`convention`/`decision`
   - Linear (consensus-backed) for `task`/`token`/`workitem`
4. **Remote waiter protocol** for blocking `in:`/`rd:` across nodes
   (extends `remote_channel.go`'s callback pattern).
5. **Cross-node CancellationContext propagation** so `out:withContext:`
   works when the context lives on a different node.

Nice-to-have:

- CRDT library in Maggie stdlib (`GSet`, `ORSet`, `LWWRegister`, `PNCounter`).
- Trust/capability enforcement wired into `Node processNamed:` — currently
  any connected peer can look up any registered process.
- Maggie-level `become:` semantics for remote refs so a local stub can become
  a live remote proxy without code change at the call site.

---

## 5. Concrete Recommendations for the PP Codebase

**No code changes made in this scouting pass.** For implementors:

- `src/bbs/BBS.mag`: keep the surface small; do not leak `TupleSpace` internals
  to callers. Today `BBS` already owns `space` — good.
- `src/api/Server.mag`: add an `Auth.mag` layer before federation; the current
  server trusts anyone who can reach the port.
- `src/dispatcher/Scheduler.mag`: the `harnessFactory` indirection is the
  right seam for Tier 2 #5 (remote dispatch). A `RemoteHarnessFactory` that
  posts to a peer's `/api/dispatch` satisfies the contract without touching
  the scheduler.
- `workflows/*.cue`: extend the schema with an optional `node` (string) or
  `placement` (enum: `local`, `any`, `pinned`) field per task, defaulting
  to `local`. Keep placement out of rule evaluation — dispatcher-only concern.
- New category needed in `src/bbs/Categories.mag` for Tier 1 #2:
  `federation-peer` (pinned), carrying peer URL + trust material.

---

## 6. Evidence Citations

- Maggie distributed primitives: `<maggie-repo>/lib/{Cluster,Node,RemoteProcess,HashRing,DistributedSupervisor,TupleSpace}.mag`
- Remote VM plumbing: `<maggie-repo>/vm/{remote_spawn,remote_channel,remote_monitor,remote_lifecycle}.go`
- Wire/identity: `<maggie-repo>/vm/dist/{wire,identity,trust,chunker}.go`
- Maggie gap assessment: `<maggie-repo>/docs/roadmaps/2026-02-03-distributed-runtime-roadmap.md`
- PP coordination fabric: `src/bbs/BBS.mag`, `src/dispatcher/{Dispatcher,Scheduler}.mag`, `src/api/Server.mag`, `src/harness/ClaudeHarness.mag`
