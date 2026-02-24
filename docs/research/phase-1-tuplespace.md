# Phase 1 Research: Tuplespace (BBS) in Maggie

## Overview

This document covers the design for reimplementing imp-castle's Linda-style tuplespace (BBS) as a native Maggie subsystem. The BBS is the coordination backbone of the imp-castle multi-agent system — agents communicate exclusively through shared tuples rather than direct messaging.

The goal is to express the tuplespace in Maggie's object model so that agents written in Maggie can coordinate natively, while retaining SQLite persistence through Go interop.

---

## 1. Tuple Representation as Maggie Objects

### Reference: Go Implementation

The Go `Tuple` struct carries:

```go
type Tuple struct {
    ID         int64
    Category   string
    Scope      string
    Identity   string
    Instance   string    // defaults to "local"
    Payload    string    // JSON
    Lifecycle  string    // "furniture" | "session" | "ephemeral"
    TaskID     *string
    AgentID    *string
    CreatedAt  time.Time
    UpdatedAt  time.Time
    TTLSeconds *int
}
```

### Maggie Design

Tuples become first-class Maggie objects. The core class hierarchy:

```smalltalk
Object subclass: #Tuple
  instanceVars: 'id category scope identity instance payload
                 lifecycle taskId agentId createdAt updatedAt ttlSeconds'.

"Factory methods"
Tuple class >> category: cat scope: sc identity: ident payload: pl [
  ^ self new
    category: cat;
    scope: sc;
    identity: ident;
    payload: pl;
    instance: 'local';
    lifecycle: #session
]

"Convenience for common categories"
Tuple class >> fact: scope identity: ident content: content [
  ^ self category: #fact scope: scope identity: ident
         payload: (Dictionary new at: #content put: content; yourself)
]

Tuple class >> claim: scope identity: ident agent: agent status: status [
  ^ self category: #claim scope: scope identity: ident
         payload: (Dictionary new at: #agent put: agent; at: #status put: status; yourself)
]
```

**Lifecycle as a symbol**, not a string — Maggie symbols are interned and identity-compared:

```smalltalk
Object subclass: #Lifecycle.
Lifecycle class >> furniture [ ^ #furniture ]
Lifecycle class >> session   [ ^ #session ]
Lifecycle class >> ephemeral [ ^ #ephemeral ]
```

**Payload representation**: In the Go implementation, payloads are JSON strings. In Maggie, payloads should be native `Dictionary` objects. Serialization to/from JSON happens at the persistence boundary (Go interop layer), not in the Maggie domain model.

```smalltalk
"Creating a tuple with structured payload"
t := Tuple category: #obstacle scope: 'my-repo' identity: 'build-fails'
       payload: (Dictionary new
         at: #detail put: 'Missing dependency foo';
         at: #task put: 'task-123';
         yourself).

"Accessing payload fields"
t payload at: #detail.  "=> 'Missing dependency foo'"
```

### Design Decision: Immutable vs Mutable Tuples

Tuples in a Linda system are conceptually immutable once written to the space — they are read or consumed, never modified in-place. Maggie tuples should follow this:

- `Tuple` instances are **mutable during construction** (builder pattern via cascaded sends)
- Once passed to `TupleSpace >> out:`, the space retains ownership; the caller should treat the tuple as handed off
- `in:` and `rd:` return fresh `Tuple` instances (copies from the store)

This matches the Go implementation where `Out()` inserts into SQLite and `In()`/`Rd()` return new structs from query results.

---

## 2. The Four Linda Operations

### Reference: Go Implementation

| Op     | Go Method | Behavior |
|--------|-----------|----------|
| `out`  | `Space.Out(ctx, Tuple)` | Insert tuple, wake matching waiters |
| `in`   | `Space.In(ctx, Pattern)` | Destructive read (blocks); cannot consume furniture |
| `rd`   | `Space.Rd(ctx, Pattern)` | Non-destructive read (blocks) |
| `scan` | `Space.Scan(ctx, Pattern)` | Return all matches (non-blocking) |

### Maggie Design

The `TupleSpace` class wraps the four operations:

```smalltalk
Object subclass: #TupleSpace
  instanceVars: 'store waiters mutex'.

"OUT — write a tuple into the space"
TupleSpace >> out: aTuple [
  | id |
  mutex critical: [
    id := store insert: aTuple.
    aTuple id: id.
    self wakeWaitersFor: aTuple.
  ].
  ^ id
]

"IN — destructive blocking read"
TupleSpace >> in: aPattern [
  ^ self in: aPattern timeout: 5 seconds
]

TupleSpace >> in: aPattern timeout: aDuration [
  | result |
  mutex critical: [
    result := store findAndDelete: aPattern.
    result ifNotNil: [ ^ result ].
    "No match — register a waiter"
  ].
  ^ self waitFor: aPattern destructive: true timeout: aDuration
]

"RD — non-destructive blocking read"
TupleSpace >> rd: aPattern [
  ^ self rd: aPattern timeout: 5 seconds
]

TupleSpace >> rd: aPattern timeout: aDuration [
  | result |
  mutex critical: [
    result := store findOne: aPattern.
    result ifNotNil: [ ^ result ].
  ].
  ^ self waitFor: aPattern destructive: false timeout: aDuration
]

"SCAN — non-blocking match-all"
TupleSpace >> scan: aPattern [
  ^ store findAll: aPattern
]
```

### Furniture Protection

The Go implementation prevents `in()` from consuming furniture tuples (returns `ErrFurnitureDelete`). In Maggie:

```smalltalk
TupleSpace >> in: aPattern timeout: aDuration [
  | result |
  mutex critical: [
    result := store findOne: aPattern.
    result ifNotNil: [
      result isFurniture
        ifTrue: [ ^ Failure reason: 'Cannot consume furniture tuple' ].
      store delete: result id.
      ^ result
    ].
  ].
  ^ self waitFor: aPattern destructive: true timeout: aDuration
]
```

The waiter wake-up path in `out:` must also enforce this — a destructive waiter skips furniture matches:

```smalltalk
TupleSpace >> wakeWaitersFor: aTuple [
  waiters copy do: [:w |
    (w pattern matches: aTuple) ifTrue: [
      (w isDestructive and: [aTuple isFurniture])
        ifFalse: [
          w isDestructive ifTrue: [ store delete: aTuple id ].
          w resume: aTuple.
          waiters remove: w.
          ^ self  "First match wins"
        ]
    ]
  ]
]
```

---

## 3. Pattern Matching Design

### Reference: Go Implementation

```go
type Pattern struct {
    Category      *string  // nil = wildcard
    Scope         *string
    Identity      *string
    Instance      *string
    PayloadSearch *string  // FTS5 MATCH query
}
```

Nil fields are wildcards. Non-nil fields use exact equality. `PayloadSearch` triggers FTS5 full-text search on the payload column.

### Maggie Design

Patterns are first-class objects with `nil` as wildcard:

```smalltalk
Object subclass: #Pattern
  instanceVars: 'category scope identity instance payloadSearch'.

"Full wildcard — matches everything"
Pattern class >> any [
  ^ self new
]

"Match specific category in scope"
Pattern class >> category: cat scope: sc [
  ^ self new category: cat; scope: sc
]

"With FTS payload search"
Pattern class >> category: cat scope: sc payloadSearch: query [
  ^ self new category: cat; scope: sc; payloadSearch: query
]
```

**In-memory matching** (used for waiter wake-up):

```smalltalk
Pattern >> matches: aTuple [
  category ifNotNil: [ category = aTuple category ifFalse: [ ^ false ] ].
  scope ifNotNil: [ scope = aTuple scope ifFalse: [ ^ false ] ].
  identity ifNotNil: [ identity = aTuple identity ifFalse: [ ^ false ] ].
  instance ifNotNil: [ instance = aTuple instance ifFalse: [ ^ false ] ].
  "payloadSearch requires FTS — not evaluated in-memory"
  ^ true
]
```

**SQL matching** (used for store queries): The `Store` translates `Pattern` fields into a SQL WHERE clause. Nil fields are omitted; non-nil fields become `column = ?` bindings. `payloadSearch` joins against the FTS5 virtual table.

### Design Decision: Structured vs FTS Payload Matching

The Go implementation relies on FTS5 for payload search, which is text-oriented. For Maggie, we could add **structured payload matching** using dictionary key/value patterns:

```smalltalk
"Future extension — not in Go implementation"
Pattern class >> category: cat scope: sc payloadKey: key value: val [
  ^ self new category: cat; scope: sc;
    payloadSearch: (key, ':', val) "Translate to FTS query"
]
```

However, for Phase 1, we should match the Go implementation exactly: string-based FTS5 queries via `payloadSearch`. Structured matching can be added later.

---

## 4. SQLite Persistence via Go Interop

### Reference: Go Implementation

The `Store` uses `modernc.org/sqlite` (pure Go) with:
- WAL journal mode for concurrent reads
- Single connection (`SetMaxOpenConns(1)`)
- FTS5 virtual table for payload search
- Schema versioning with migrations
- Pragmas: `journal_mode=WAL`, `busy_timeout=5000`, `synchronous=NORMAL`

### Maggie Design: Go Interop Layer

Maggie has a Go interop system (`GoObject` registry in `vm/gowrap.go`). The tuplespace persistence layer should be a **Go-implemented store exposed to Maggie via GoWrap**:

```
┌─────────────────────────────────────┐
│  Maggie: TupleSpace, Tuple, Pattern │  ← Pure Maggie objects
├─────────────────────────────────────┤
│  Maggie: Store (GoObject wrapper)   │  ← Thin Maggie interface
├─────────────────────────────────────┤
│  Go: TupleStore                     │  ← Go implementation
│  - SQLite via modernc.org/sqlite    │
│  - FTS5 index management            │
│  - Schema migrations                │
│  - Prepared statement caching       │
├─────────────────────────────────────┤
│  SQLite (WAL mode)                  │  ← On-disk storage
└─────────────────────────────────────┘
```

**Why Go for persistence**: SQLite bindings, prepared statement management, and schema migrations are complex infrastructure that Go handles well. Pure Maggie SQLite bindings would be a large undertaking with little benefit. The Go interop boundary is the natural seam.

**GoWrap interface** — the Go `TupleStore` exposes these methods to Maggie:

```go
// Registered via GoObject registry
type TupleStore struct {
    db *sql.DB
}

func (s *TupleStore) Insert(category, scope, identity, instance, payload, lifecycle string, ttl int) int64
func (s *TupleStore) FindOne(category, scope, identity, instance, payloadSearch *string) *TupleRow
func (s *TupleStore) FindAndDelete(category, scope, identity, instance, payloadSearch *string) *TupleRow
func (s *TupleStore) FindAll(category, scope, identity, instance, payloadSearch *string) []TupleRow
func (s *TupleStore) Delete(id int64) bool
```

Maggie's `Store` class wraps this:

```smalltalk
Object subclass: #Store
  instanceVars: 'goStore'.

Store class >> open: dbPath [
  ^ self new goStore: (GoObject wrap: 'TupleStore' with: dbPath)
]

Store >> insert: aTuple [
  ^ goStore insert: aTuple category
              scope: aTuple scope
              identity: aTuple identity
              instance: aTuple instance
              payload: (aTuple payload asJson)
              lifecycle: aTuple lifecycle
              ttl: aTuple ttlSeconds
]

Store >> findOne: aPattern [
  | row |
  row := goStore findOne: aPattern category
                   scope: aPattern scope
                   identity: aPattern identity
                   instance: aPattern instance
                   payloadSearch: aPattern payloadSearch.
  row ifNil: [ ^ nil ].
  ^ Tuple fromRow: row
]
```

### Schema

Identical to the Go implementation's v2 schema:

```sql
CREATE TABLE IF NOT EXISTS tuples (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  category    TEXT NOT NULL,
  scope       TEXT NOT NULL DEFAULT '',
  identity    TEXT NOT NULL DEFAULT '',
  instance    TEXT NOT NULL DEFAULT 'local',
  payload     TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(payload)),
  lifecycle   TEXT NOT NULL DEFAULT 'session'
              CHECK(lifecycle IN ('furniture','session','ephemeral')),
  task_id     TEXT,
  agent_id    TEXT,
  created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
  updated_at  DATETIME NOT NULL DEFAULT (datetime('now')),
  ttl_seconds INTEGER
);

-- Indices for pattern matching
CREATE INDEX idx_tuples_category ON tuples(category);
CREATE INDEX idx_tuples_scope ON tuples(scope);
CREATE INDEX idx_tuples_identity ON tuples(identity);
CREATE INDEX idx_tuples_instance ON tuples(instance);
CREATE INDEX idx_tuples_lifecycle ON tuples(lifecycle);
CREATE INDEX idx_tuples_task_id ON tuples(task_id);
CREATE INDEX idx_tuples_created_at ON tuples(created_at);
CREATE INDEX idx_tuples_cat_scope ON tuples(category, scope);

-- FTS5 for payload search
CREATE VIRTUAL TABLE IF NOT EXISTS tuples_fts USING fts5(
  payload, content='tuples', content_rowid='id'
);

-- Auto-sync triggers
CREATE TRIGGER tuples_ai AFTER INSERT ON tuples BEGIN
  INSERT INTO tuples_fts(rowid, payload) VALUES (new.id, new.payload);
END;
CREATE TRIGGER tuples_ad AFTER DELETE ON tuples BEGIN
  INSERT INTO tuples_fts(tuples_fts, rowid, payload) VALUES ('delete', old.id, old.payload);
END;
CREATE TRIGGER tuples_au AFTER UPDATE ON tuples BEGIN
  INSERT INTO tuples_fts(tuples_fts, rowid, payload) VALUES ('delete', old.id, old.payload);
  INSERT INTO tuples_fts(rowid, payload) VALUES (new.id, new.payload);
END;
```

---

## 5. Blocking Semantics Using Maggie Channels/Processes

### Reference: Go Implementation

Blocking uses goroutines + channels:
1. `In()`/`Rd()` register a `waiter{pattern, destructive, ch}` on the space
2. `Out()` iterates waiters, sends matched tuple on `ch`
3. Caller blocks on `select { case t := <-w.ch: ... case <-ctx.Done(): ... }`

### Maggie Design

Maggie has native channels and processes (goroutine-mapped). The blocking model maps directly:

```smalltalk
Object subclass: #Waiter
  instanceVars: 'pattern destructive channel'.

Waiter class >> pattern: p destructive: d [
  ^ self new
    pattern: p;
    destructive: d;
    channel: Channel new  "Unbuffered channel"
]

"Resume sends the matched tuple to the blocked caller"
Waiter >> resume: aTuple [
  channel send: aTuple
]
```

**Blocking with timeout** using Maggie's `Channel select:` with a timeout channel:

```smalltalk
TupleSpace >> waitFor: aPattern destructive: aBoolean timeout: aDuration [
  | waiter timeoutCh result |
  waiter := Waiter pattern: aPattern destructive: aBoolean.
  mutex critical: [
    waiters add: waiter.
  ].

  "Create a timeout channel"
  timeoutCh := Channel new.
  [aDuration wait. timeoutCh send: #timeout] fork.

  "Block until match or timeout"
  result := Channel select: {
    waiter channel onReceive: [:tuple | tuple].
    timeoutCh onReceive: [:_ |
      mutex critical: [ waiters remove: waiter ifAbsent: [] ].
      Failure reason: 'Timeout waiting for tuple match'
    ]
  }.
  ^ result
]
```

### Cancellation

Maggie's `CancellationContext` provides structured cancellation:

```smalltalk
TupleSpace >> in: aPattern withContext: ctx [
  | waiter |
  waiter := Waiter pattern: aPattern destructive: true.
  mutex critical: [
    "Try immediate match first"
    | existing |
    existing := store findAndDelete: aPattern.
    existing ifNotNil: [ ^ existing ].
    waiters add: waiter.
  ].

  result := Channel select: {
    waiter channel onReceive: [:tuple | tuple].
    ctx onCancel: [
      mutex critical: [ waiters remove: waiter ifAbsent: [] ].
      Failure reason: 'Cancelled'
    ]
  }.
  ^ result
]
```

### Design Decision: Process-per-Waiter vs Channel-per-Waiter

The Go implementation uses one goroutine per blocking call (implicit in Go's `select`). Maggie should use the same model — the forked timeout process is lightweight (mapped to a goroutine). No thread pool or reactor pattern needed.

---

## 6. Tuple Categories

### Standard Vocabulary

From the Go seed data (`seed.go`), the standard categories are:

| Category | Purpose | Lifecycle |
|----------|---------|-----------|
| `fact` | Established knowledge about a scope | furniture |
| `convention` | Agreed-upon patterns/rules | furniture |
| `claim` | Agent claiming ownership of work | session |
| `available` | Work available for atomic claiming | session |
| `obstacle` | Something blocking progress | session |
| `need` | Something an agent requires | session |
| `artifact` | Output produced by an agent | session |
| `event` | Significant occurrence (task_done, dismiss) | session |
| `notification` | Directed message to an agent | ephemeral |
| `convention-proposal` | Proposed convention (pre-quorum) | session |

### Maggie Design

Categories as symbols with a registry:

```smalltalk
Object subclass: #TupleCategory
  classVars: 'Registry'.

TupleCategory class >> initialize [
  Registry := Dictionary new.
  #(fact convention claim available obstacle need artifact
    event notification conventionProposal) do: [:name |
      Registry at: name put: (self new name: name)
  ]
]

TupleCategory class >> fact         [ ^ Registry at: #fact ]
TupleCategory class >> convention   [ ^ Registry at: #convention ]
TupleCategory class >> claim        [ ^ Registry at: #claim ]
TupleCategory class >> available    [ ^ Registry at: #available ]
"... etc"

"Default lifecycle per category"
TupleCategory >> defaultLifecycle [
  ^ (Dictionary new
      at: #fact put: #furniture;
      at: #convention put: #furniture;
      at: #notification put: #ephemeral;
      yourself)
    at: name ifAbsent: [ #session ]
]
```

This gives us type-safe category usage while keeping the symbol-based flexibility of the Go implementation.

---

## 7. Lifecycle Classes

### Semantics

| Lifecycle | Duration | `in()` consumable? | GC behavior |
|-----------|----------|---------------------|-------------|
| **furniture** | Permanent | No | Never collected; protected from deletion |
| **session** | Daemon session | Yes | Archived to Parquet on task completion, then deleted |
| **ephemeral** | Short-lived | Yes | Deleted when TTL expires; archived first |

### Maggie Design

```smalltalk
Tuple >> isFurniture [ ^ lifecycle = #furniture ]
Tuple >> isSession   [ ^ lifecycle = #session ]
Tuple >> isEphemeral [ ^ lifecycle = #ephemeral ]

"Furniture protection"
Tuple >> assertConsumable [
  self isFurniture ifTrue: [
    ^ Failure reason: 'Furniture tuples cannot be consumed via in()'
  ]
]
```

### Lifecycle Transitions

In the Go implementation, tuples don't change lifecycle. However, the **convention promotion** system in GC effectively transitions `convention-proposal` (session) tuples into `convention` (furniture) tuples by deleting the proposals and creating new furniture tuples. This is a **create-new, delete-old** pattern, not mutation.

---

## 8. TTL and Garbage Collection

### Reference: Go Implementation

The GC runs every 60 seconds and performs:

1. **Expired ephemeral collection**: Delete ephemeral tuples past their TTL (archive first)
2. **Completed task archival**: Synthesize knowledge from session tuples, then archive
3. **Stale claim cleanup**: Claims >1h with task_done or >2h abandoned
4. **Convention promotion**: When 2+ agents propose same convention, promote to furniture
5. **Systemic obstacle detection**: Escalate when 2+ obstacles in same scope
6. **Unclaimed need detection**: Needs unclaimed >10 minutes
7. **Repeated failure detection**: 3+ failures in a scope
8. **Workflow warnings**: Tasks matching previously failed patterns

### Maggie Design

GC as a periodic process:

```smalltalk
Object subclass: #TupleGC
  instanceVars: 'space store interval'.

TupleGC class >> on: aSpace interval: aDuration [
  ^ self new space: aSpace; store: aSpace store; interval: aDuration
]

TupleGC >> start [
  [
    [ true ] whileTrue: [
      self collectExpiredEphemeral.
      self cleanupStaleClaims.
      self promoteConventions.
      self detectSystemicObstacles.
      self detectUnclaimedNeeds.
      interval wait.
    ]
  ] fork
]

TupleGC >> collectExpiredEphemeral [
  "Query for expired ephemeral tuples"
  | expired |
  expired := store query:
    'SELECT * FROM tuples
     WHERE lifecycle = ''ephemeral''
       AND ttl_seconds IS NOT NULL
       AND datetime(created_at, ''+'' || ttl_seconds || '' seconds'') < datetime(''now'')'.
  expired do: [:row |
    "Archive before deletion"
    self archive: row.
    store delete: row id.
  ]
]

TupleGC >> cleanupStaleClaims [
  "Delete claims older than 1h with corresponding task_done,
   or older than 2h regardless"
  | stale |
  stale := store query:
    'SELECT c.* FROM tuples c
     WHERE c.category = ''claim''
       AND (
         (c.created_at < datetime(''now'', ''-1 hour'')
          AND EXISTS (SELECT 1 FROM tuples e
                      WHERE e.category = ''event''
                        AND e.identity = ''task_done''
                        AND json_extract(e.payload, ''$.task'') = c.identity))
         OR c.created_at < datetime(''now'', ''-2 hours'')
       )'.
  stale do: [:row | store delete: row id]
]

TupleGC >> promoteConventions [
  "Group convention-proposal tuples by content.
   When 2+ independent agents propose the same thing, promote to furniture."
  | proposals groups |
  proposals := space scan: (Pattern category: #conventionProposal).
  groups := proposals groupBy: [:p | p payload at: #content].
  groups keysAndValuesDo: [:content :props |
    | agents |
    agents := (props collect: [:p | p payload at: #agent]) asSet.
    agents size >= 2 ifTrue: [
      "Promote: create furniture convention, delete proposals"
      space out: (Tuple category: #convention
                        scope: (props first scope)
                        identity: (props first identity)
                        payload: (Dictionary new at: #content put: content; yourself)).
      props do: [:p | store delete: p id]
    ]
  ]
]
```

### Design Decision: GC in Maggie vs Go

For Phase 1, the GC should run in Maggie (as a forked process) calling the Go store for queries. This keeps the GC logic visible and modifiable in Maggie. The expensive parts (SQL queries, archival) are in Go.

For synthesis (LLM knowledge extraction), this is more appropriate as a higher-level concern and should be deferred to a later phase when LLM integration is designed.

---

## 9. Mail-as-Tuple Concept

### Reference: Go Implementation

The Go BBS implements notifications as tuples with category `notification`:

```go
func (sp *Space) DrainNotifications(agentID string) ([]Tuple, error) {
    // Atomic: DELETE FROM tuples WHERE category='notification' AND scope=?
    // Returns deleted tuples (exactly-once delivery)
}
```

Notifications are **piggybacked** on every RPC response — after handling any BBS operation, the server drains and returns pending notifications for the requesting agent.

### Maggie Design

Notifications as a thin layer over the tuplespace:

```smalltalk
Object subclass: #AgentMailbox
  instanceVars: 'space agentId'.

AgentMailbox class >> for: anAgentId in: aSpace [
  ^ self new agentId: anAgentId; space: aSpace
]

"Send notification to an agent"
AgentMailbox >> send: aMessage [
  space out: (Tuple new
    category: #notification;
    scope: agentId;
    identity: (UUID generate asString);
    payload: aMessage;
    lifecycle: #ephemeral;
    ttlSeconds: 300  "5 minute TTL"
  )
]

"Drain all pending notifications (exactly-once)"
AgentMailbox >> drain [
  | notifications |
  notifications := space scan: (Pattern category: #notification scope: agentId).
  notifications do: [:n |
    space store delete: n id.  "Atomic delete"
  ].
  ^ notifications
]
```

### Cross-Pollination

The Go implementation has a `CrossPollinator` that detects scope references in tuple payloads and auto-generates notifications. In Maggie:

```smalltalk
Object subclass: #CrossPollinator
  instanceVars: 'space rateLimiter'.

"Called after every out() — checks if payload references other scopes"
CrossPollinator >> checkCrossReferences: aTuple [
  | referencedScopes |
  referencedScopes := self extractScopeReferences: aTuple payload.
  referencedScopes do: [:scope |
    (rateLimiter allow: aTuple agentId to: scope) ifTrue: [
      (AgentMailbox for: scope in: space) send: (Dictionary new
        at: #type put: 'cross-reference';
        at: #source put: aTuple scope;
        at: #tuple_id put: aTuple id;
        yourself)
    ]
  ]
]
```

---

## 10. Architectural Layers

### Complete Stack

```
┌───────────────────────────────────────────────────┐
│  Agent Code (Maggie)                              │
│  - Reads/writes tuples via TupleSpace API         │
│  - Uses Pattern objects for matching              │
│  - Blocks on in()/rd() via Maggie channels        │
├───────────────────────────────────────────────────┤
│  TupleSpace (Maggie)                              │
│  - out/in/rd/scan operations                      │
│  - Waiter management (channel-based blocking)     │
│  - Furniture protection                           │
│  - In-memory pattern matching for wake-ups        │
├───────────────────────────────────────────────────┤
│  TupleGC (Maggie process)                         │
│  - Periodic collection of expired tuples          │
│  - Convention promotion                           │
│  - Stale claim cleanup                            │
│  - Escalation detection                           │
├───────────────────────────────────────────────────┤
│  AgentMailbox (Maggie)                            │
│  - Notification send/drain                        │
│  - Cross-pollination                              │
├───────────────────────────────────────────────────┤
│  Store (Maggie → Go interop)                      │
│  - Thin Maggie wrapper over GoObject              │
│  - JSON serialization at boundary                 │
├───────────────────────────────────────────────────┤
│  TupleStore (Go)                                  │
│  - SQLite operations                              │
│  - FTS5 queries                                   │
│  - Schema migrations                              │
│  - Prepared statement caching                     │
├───────────────────────────────────────────────────┤
│  SQLite (WAL mode)                                │
│  - tuples table + indices                         │
│  - tuples_fts virtual table                       │
│  - Triggers for FTS sync                          │
└───────────────────────────────────────────────────┘
```

### What Lives in Maggie vs Go

| Layer | Language | Rationale |
|-------|----------|-----------|
| Tuple, Pattern, TupleCategory | Maggie | Domain objects — benefit from Maggie's object model |
| TupleSpace (4 ops + waiters) | Maggie | Core coordination logic — should be inspectable/modifiable |
| TupleGC | Maggie | Policy logic — benefits from late-binding and live modification |
| AgentMailbox, CrossPollinator | Maggie | Higher-level coordination — pure Maggie |
| Store (SQL operations) | Go via GoWrap | Infrastructure — SQLite bindings, prepared statements |
| Schema migrations | Go | One-time setup, complex SQL |
| FTS5 management | Go | Tightly coupled to SQLite internals |
| Archival (Parquet/DuckDB) | Go | External library dependency |

---

## 11. Key Design Decisions Summary

### D1: Payload as Dictionary, not JSON String
Payloads are native Maggie `Dictionary` objects. JSON serialization happens only at the Go interop boundary. This gives agents rich, typed access to payload data without constant parsing.

### D2: Channel-based Blocking
Maggie's native `Channel` and `Process` primitives map directly to Go's goroutines and channels. No need for a custom blocking mechanism — the language provides it.

### D3: Go Interop for Persistence
SQLite operations stay in Go. The Maggie layer is a thin wrapper that translates between Maggie objects and Go method calls. This avoids reimplementing SQLite bindings in Maggie.

### D4: GC in Maggie
Garbage collection policy runs as a Maggie process. This allows live modification of GC behavior (e.g., changing intervals, adding new collection strategies) without recompiling Go code.

### D5: Furniture Protection at Space Level
The `TupleSpace` enforces furniture immutability, not the store. This keeps the Go store simple (pure CRUD) and puts policy in Maggie where it's inspectable.

### D6: Convention Promotion via GC
Following the Go implementation: convention proposals are session tuples that the GC promotes to furniture when quorum is reached. No special API needed.

### D7: Deferred Synthesis
LLM-based knowledge extraction (synthesis) is deferred to a later phase. The GC archives session tuples without synthesizing. This keeps Phase 1 focused.

### D8: Same Schema, Same DB
The Maggie BBS uses the identical SQLite schema as the Go implementation. This means the same database file can be used by both systems during migration, and tools like `pp bbs` continue to work.

---

## 12. Open Questions for Later Phases

1. **Image persistence**: Should the tuplespace state be part of a Maggie image snapshot? Hot tuples in the image, cold tuples in SQLite?
2. **Distribution**: The Go implementation has a gRPC distribution layer. How does this interact with Maggie's process model?
3. **Synthesis integration**: When Maggie has LLM bindings, how should synthesis be triggered — as a GC hook or explicit API?
4. **Process restriction**: Should tuplespace access be restricted per-process (sandboxing)? Maggie's process restriction primitives could enforce this.
5. **Live migration**: Strategy for migrating from Go BBS to Maggie BBS with zero downtime.

---

## References

- imp-castle `internal/bbs/` package — complete reference implementation
  - `space.go` — Core Linda primitives, tuple model, waiter management
  - `store.go` — SQLite persistence, schema, migrations
  - `server.go` — JSON-RPC 2.0 server
  - `gc.go` — Garbage collection, convention promotion
  - `archive.go` — Parquet archival via DuckDB
  - `synthesis.go` — LLM knowledge extraction
  - `escalation.go` — Systemic obstacle/need detection
  - `feedback.go` — Analytics feedback loop
  - `crosspoll.go` — Cross-scope notification injection
  - `context.go` — Agent context building
  - `seed.go` — Furniture initialization
- Maggie language — `/Users/chazu/dev/go/maggie`
  - `CLAUDE.md` — Comprehensive language reference
  - `docs/MAGGIE_DESIGN.md` — Language design document
  - `vm/gowrap.go` — Go interop infrastructure
  - `concurrency.md` — Concurrency primitives documentation
