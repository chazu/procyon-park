# Cross-Phase Contradiction Analysis

**Issue:** procyon-park-5rq
**Author:** Whimsy
**Date:** 2026-02-23
**Status:** Complete

## Summary

Review of all 10 research documents (phase-0 through phase-9) identified **12 contradictions or inconsistencies**, categorized by severity:

- **4 Hard Contradictions** — phases make incompatible claims that must be resolved before implementation
- **5 Soft Inconsistencies** — phases use different assumptions or terminology that need alignment
- **3 Minor Gaps** — small discrepancies that are easy to fix

---

## Hard Contradictions

### C1: Workflow State Persistence — BadgerDB vs SQLite

**Phases in conflict:** Phase 6 vs Phase 8

**Phase 6 (Workflows)** repeatedly references BadgerDB as the persistence layer for workflow instances:
> "store store.Store // Persistent KV store (Badger)" (§2, Executor struct)
> "Instances are stored in a Badger KV store" (§5)
> "gate state: separate bucket for crash recovery" (§6)

**Phase 8 (Telemetry)** reveals the ground truth:
> "imp-castle uses a Store interface that abstracts storage behind bucket/key operations. The actual implementation is SQLite (SQLiteStore), despite the original design targeting BadgerDB." (§2)

**Impact:** Phase 6's architecture diagrams and state management design reference Badger-specific concepts (buckets, key-prefix iteration) that may not map to the actual SQLite-backed Store.

**Recommended resolution:** Acknowledge that the Store abstraction is SQLite-backed. For procyon-park, workflow state should live in SQLite (consistent with BBS tuplespace storage). Phase 6's bucket/key patterns translate to SQL tables with indexed columns. No BadgerDB dependency needed.

---

### C2: Wait/Gate Steps Depend on Removed Mail System

**Phases in conflict:** Phase 6 vs Phase 4

**Phase 6 (Workflows)** describes the Wait step as:
> "Polls message store every 5s for messages of type `output` addressed to `daemon` from the agent" (§3.2)

And the Gate step as:
> "Sends approval prompt messages to specified approvers" and "Polls for `workflow_approve` or `workflow_reject` command messages" (§3.5, §6)

**Phase 4 (CLI)** explicitly removes the mail system:
> "Mail commands (`cub mail`, `cub send`): Replaced by BBS tuplespace. Agents communicate via tuples, not mailboxes. This is a simplification — one coordination mechanism instead of two." (§10)

**Impact:** Two of the five workflow step types (Wait and Gate) are designed around a subsystem that won't exist in procyon-park. Their implementations need fundamental redesign.

**Recommended resolution:**
- **Wait step:** Poll the tuplespace for `event` tuples with identity `task_done` matching the agent, instead of polling a mailbox for `output` messages. The BBS already has this event type.
- **Gate step:** Write a `gate_request` tuple for approvers; poll for `gate_response` tuples (approve/reject). CLI commands `pp workflow approve/reject` write these tuples. This aligns with the BBS-native design.

---

### C3: Agent Lifecycle — Go Standalone vs Daemon-Hosted

**Phases in conflict:** Phase 3 vs Phases 2/4

**Phase 3 (Agent Lifecycle)** describes agent management as standalone Go code with direct system calls:
> Spawn: "Create branch → Create worktree → Create tmux session → Launch agent command → Register in JSON file"
> All operations use direct `os/exec`, `tmux` package calls, and JSON file I/O with `flock()`.

**Phase 2 (Daemon)** says the daemon hosts agent lifecycle:
> "The procyon-park daemon is more ambitious. It runs a Maggie VM image that hosts: The BBS tuplespace (Phase 1), Agent lifecycle management (Phase 3), Workflow execution (Phase 6), CLI command dispatch (Phase 4)" (§Context)

**Phase 4 (CLI)** maps agent commands to daemon IPC:
> `pp agent spawn` → JSON-RPC method `agent.spawn` → VM action (§8, table)

But also says:
> "Agent lifecycle: Go + Maggie — Go for tmux/git, Maggie for state" (§1, table)

**Impact:** It's unclear whether agent operations are direct Go calls (Phase 3's model) or daemon-mediated via IPC (Phases 2/4's model). This affects error handling, concurrency, and whether agent operations work when the daemon is down.

**Recommended resolution:** Agent lifecycle operations go through the daemon IPC. The daemon dispatches to Go code for system operations (tmux, git) and uses the Maggie tuplespace for state. Phase 3's Go code becomes the daemon-side implementation behind the IPC interface, not a standalone module. The CLI never calls tmux/git directly — it always talks to the daemon.

---

### C4: CUE Integration Complexity — Simple gowrap vs Deep Integration

**Phases in conflict:** Phase 0 vs Phase 6

**Phase 0 (Project Setup)** describes CUE as a simple gowrap candidate:
> "CUE evaluation — via gowrap" (§6.1)
> "Simple parse-validate-extract API is sufficient" (§4.7)

**Phase 6 (Workflows)** reveals the actual CUE requirements:
> - Module-aware loading with `cue.mod/` directory traversal
> - Two-phase compilation (parse with stubs, resolve with concrete params)
> - `_input` / `_ctx` hidden field injection
> - Re-compilation at each step with updated context
> - CUE unification for the Evaluate step's output validation
> - Schema embedding via `//go:embed`
> - Aspect expansion as a post-load transformation

**Impact:** gowrap generates bindings for simple API surfaces. CUE's usage in the workflow engine requires deep integration with CUE's compiler, module system, and unification engine. Auto-generated bindings won't work.

**Recommended resolution:** CUE integration needs hand-written Go code exposed to Maggie via custom primitives (not gowrap). The Maggie-side workflow loader should call Go functions for CUE compilation, unification, and value extraction. The Go side handles module resolution, context injection, and re-compilation. This is the same approach Phase 0 recommends for process exec — "tight VM integration" demands hand-written primitives.

---

## Soft Inconsistencies

### I1: SQLite Library Choice Not Resolved

**Phases involved:** Phase 0, Phase 1

**Phase 0** recommends `github.com/mattn/go-sqlite3` (CGo) in the dependency list and gowrap config:
> "Recommended Go library: `github.com/mattn/go-sqlite3` (CGo, most mature)" (§4.1)

But also notes the alternative:
> "`modernc.org/sqlite` (pure Go, no CGo — better for cross-compilation)" (§4.1)

**Phase 1** references the existing Go implementation using the pure-Go library:
> "The Store uses `modernc.org/sqlite` (pure Go)" (§4)

**Observation:** Phase 0 lists `mattn/go-sqlite3` in both the gowrap config (§1.2) and required dependencies (§7.1), but the reference implementation already uses `modernc.org/sqlite`. The choice is presented as an open question (§8.2) but the dependency lists assume CGo.

**Recommended resolution:** Use `modernc.org/sqlite` (pure Go). It's what the reference implementation uses, eliminates CGo complexity for SQLite (DuckDB still needs CGo anyway), and simplifies cross-compilation if ever needed.

---

### I2: Environment Variable Prefix Convention — Three Schemes

**Phases involved:** Phase 2, Phase 4, Phase 9

Three different environment variable prefixes are used:

| Phase | Convention | Example |
|-------|-----------|---------|
| Phase 2 | `PROCYON_PARK_` | `PROCYON_PARK_SOCK` |
| Phase 4 | `PP_` | `PP_AGENT_NAME`, `PP_REPO`, `PP_TASK` |
| Phase 9 | `PROCYON_` | `PROCYON_FEATURES_BBS_ENABLED` |

**Impact:** Agents and scripts will need to know which prefix to use for which purpose. Three conventions create cognitive load and potential bugs.

**Recommended resolution:** Standardize on two conventions:
- `PP_` for agent-injected runtime variables (short, typed frequently by agents in tmux)
- `PP_` also for config overrides (e.g., `PP_DAEMON_SOCKET`, `PP_FEATURES_BBS_ENABLED`)

This collapses the three schemes into one. `PP_` is consistent with Phase 4's choice of `pp` as the binary name.

---

### I3: Binary Name — `procyon-park` vs `pp` vs `cub`

**Phases involved:** Phase 0, Phase 4, Phase 9

| Phase | Binary name used |
|-------|-----------------|
| Phase 0 | `procyon-park` (Makefile output) |
| Phase 4 | `pp` (with `procyon-park` as alias) |
| Phase 9 | `cub` (throughout §5: `pp init`, `pp repo add`, `cub doctor`) |

**Impact:** Phase 9's use of `cub` is likely a copy-paste artifact from imp-castle research, but it makes the document confusing when read alongside Phase 4.

**Recommended resolution:** The binary is `pp`. Phase 0's Makefile should produce `pp` (or both `pp` and `procyon-park` as symlink). Phase 9's commands should use `pp init`, `pp repo add`, `pp doctor`.

---

### I4: Tmux Session Naming Prefix

**Phases involved:** Phase 3, Phase 4

**Phase 3:** `cub-{repoName}-{agentName}` (e.g., `cub-procyon-park-Marble`)
**Phase 4:** `pp-procyon-park-Marble` (shown in JSON-RPC response example)

**Recommended resolution:** Use `pp-{repoName}-{agentName}` consistent with the `pp` binary name.

---

### I5: DuckDB Usage Model — Ephemeral vs Buffered

**Phases involved:** Phase 7, Phase 8

**Phase 7 (Analytics)** describes DuckDB as strictly ephemeral:
> "DuckDB is used purely as a computation engine — opened in-memory, query runs, connection closes. No persistent DuckDB state." (§8.5)

**Phase 8 (Telemetry)** uses DuckDB as an in-memory buffer:
> Pipeline accumulates data in an in-memory DuckDB instance, flushing to Parquet every 5 minutes. Data lives in DuckDB between flushes.

**Impact:** Different subsystems legitimately use DuckDB differently, but the Phase 7 claim that there is "no persistent DuckDB state" is misleading when Phase 8 maintains an in-memory buffer.

**Recommended resolution:** Document that DuckDB serves two roles: (1) ephemeral query engine for analytics (Phase 7), and (2) in-memory buffer with periodic flush for telemetry ingestion (Phase 8). Phase 7's "no persistent state" claim should be scoped to the analytics subsystem specifically.

---

## Minor Gaps

### G1: Phase 1 Convention Promotion Code Bug

**Phase 1, §8** has an apparent bug in the `promoteConventions` Maggie code:

```smalltalk
proposals := space scan: (Pattern category: #claim identity: 'convention-proposal').
```

But the category table in §6 lists `convention-proposal` as its own **category**, not as an identity within `claim`. The scan should be:

```smalltalk
proposals := space scan: (Pattern category: #'convention-proposal').
```

Or, using the Maggie symbol naming from the TupleCategory registry:
```smalltalk
proposals := space scan: (Pattern category: #conventionProposal).
```

---

### G2: Phase 3 Documents Current System Without Maggie Adaptation Notes

Phase 3 thoroughly documents imp-castle's Go implementation but provides only a brief §14 ("Maggie Design Implications") as adaptation guidance. Unlike Phase 1 (which provides complete Maggie class designs) or Phase 2 (which shows Maggie code for the event loop), Phase 3 leaves the Maggie architecture largely unspecified.

Key unanswered questions:
- Is the Agent struct a Maggie object or a Go GoObject?
- Does name allocation use the tuplespace or file-based JSON?
- Does the registry move from JSON files to the tuplespace?

**Recommended resolution:** Accept Phase 3 as Go reference documentation. The Maggie adaptation should follow the pattern established by other phases: agent state in the tuplespace (not JSON files), Go for system operations (tmux/git) via hand-written primitives, Maggie for orchestration logic.

---

### G3: Phase 6 `_ctx` vs Tuplespace Context

Phase 6's workflow engine uses a `_ctx` mechanism where step results feed into subsequent step configs via CUE re-compilation. This is elegant but creates an implicit state channel parallel to the tuplespace.

In procyon-park's Maggie-native design, this implicit state could instead be modeled as tuples — each step writes its output as a tuple, and subsequent steps read from the tuplespace. This would unify the two state channels but would require rethinking how CUE config resolution works.

**Recommended resolution:** Keep `_ctx` for CUE-level config resolution (it's a compile-time concern), but also write step results to the tuplespace for observability and coordination. The `_ctx` mechanism is internal to the workflow engine; the tuplespace is the external interface.

---

## Areas of Agreement

For completeness, these areas are **consistent** across all phases:

| Topic | Consensus |
|-------|-----------|
| IPC protocol | JSON-RPC over Unix socket (Phases 2, 4; BBS facts from Pip, Ziggy) |
| BBS tuplespace as coordination backbone | All phases reference tuplespace for coordination |
| Hot storage = SQLite with WAL | Phases 0, 1, 2, 7, 8 agree |
| Warm storage = Parquet via DuckDB | Phases 7, 8 agree |
| Config format = TOML | Phases 0, 9 agree |
| Registry format = JSON | Phase 9 (no conflicting proposals) |
| Template embedding via `//go:embed` | Phases 0, 5, 6 agree |
| Event-driven daemon main loop | Phase 2 (no conflicting proposals) |
| Cobra for CLI framework | Phase 4 (no conflicting proposals) |
| Furniture tuple protection | Phases 1, 7 agree |
| Work tracker shells out to `bd` | Phase 9 (no conflicting proposals) |

---

## Resolution Priority

Ordered by implementation impact:

1. **C2 (Wait/Gate → tuplespace)** — Blocks workflow engine implementation. Must redesign before writing any workflow step code.
2. **C3 (Agent lifecycle → daemon-mediated)** — Affects the entire spawn/dismiss architecture. Must decide before Phase 3 implementation.
3. **C1 (SQLite, not Badger)** — Affects workflow state storage. Easy to resolve (just use SQLite), but must be decided before persistence layer code.
4. **C4 (CUE hand-written primitives)** — Affects Phase 0 dependency planning and build system. Must acknowledge the complexity before starting CUE integration.
5. **I2 (env var prefix)** — Affects all agent-facing documentation and priming templates. Should standardize early.
6. **I1 (SQLite library)** — Affects build system (CGo or not). Decide before first Go dependency.
7. **I3/I4 (naming)** — Cosmetic but affects all documentation. Fix during implementation.
