# Phase 0 Research: Project Setup & Go Interop Primitives

## Overview

Procyon-park is a cleanroom reimplementation of imp-castle in Maggie (a Smalltalk dialect implemented in Go). This document covers the project structure, build system, and the Go interop primitives Maggie needs to support imp-castle's functionality.

## 1. Project Structure

### 1.1 Recommended Layout

```
procyon-park/
├── maggie.toml              # Maggie project manifest
├── go.mod                   # Go module (for custom binary build)
├── Makefile                 # Build orchestration
├── src/                     # Maggie source files (.mag)
│   ├── Main.mag             # Entry point
│   ├── bbs/                 # Tuplespace implementation
│   ├── agent/               # Agent lifecycle
│   ├── daemon/              # Background daemon
│   ├── cli/                 # CLI commands
│   ├── config/              # Configuration
│   ├── workflow/            # Workflow engine
│   └── telemetry/           # OTEL integration
├── lib/                     # Shared Maggie libraries
├── wrap/                    # Generated Go wrapper code (gowrap output)
├── docs/                    # Documentation
│   └── research/            # Research documents
└── test/                    # Test files
```

### 1.2 Maggie Project Manifest (maggie.toml)

```toml
[project]
name = "procyon-park"
namespace = "ProcyonPark"
version = "0.1.0"

[source]
dirs = ["src", "lib"]
entry = "Main.start"

[image]
output = "procyon-park.image"
include-source = true

[go-wrap]
output = "wrap"

[[go-wrap.packages]]
import = "modernc.org/sqlite"
include = ["Open"]

[[go-wrap.packages]]
import = "github.com/marcboeker/go-duckdb"
include = ["NewConnector", "NewAppender"]

[[go-wrap.packages]]
import = "github.com/BurntSushi/toml"
include = ["Decode", "DecodeFile"]

# Note: CUE is NOT a gowrap candidate. See §4.7 for details on hand-written primitives.
```

## 2. Build System: Single Binary Packaging

### 2.1 How Maggie Builds Work

Maggie produces a single binary by:

1. **Bootstrap**: Compiles core `.mag` library files into a binary image (`maggie.image`)
2. **Embed**: Uses Go's `//go:embed` directive to embed the image in the binary
3. **Build**: `go build` produces a self-contained executable (~21-29MB)

The pattern in Maggie's own build:

```makefile
all: mag

bootstrap:
    go run ./cmd/bootstrap/

mag: bootstrap
    cp maggie.image cmd/mag/
    go build -o mag ./cmd/mag/
```

The main.go uses:
```go
//go:embed maggie.image
var embeddedImage []byte
```

### 2.2 Custom Binary via gowrap

For procyon-park, we need a **custom binary** that includes wrapped Go packages. Maggie's gowrap system handles this:

1. Define packages in `maggie.toml` `[go-wrap]` section
2. Run `mag wrap` to generate Go glue code + Maggie stubs
3. Run `mag build -o procyon-park` to produce custom binary

The `mag build` command:
- Creates a temp directory with build scaffolding
- Generates `go.mod` importing all wrapper packages
- Generates `main.go` that calls `RegisterPrimitives` for each wrapper
- Runs `go build` to produce the custom binary

### 2.3 Recommended Build Pipeline for Procyon-Park

```makefile
all: pp

wrap:
    mag wrap  # Generates Go wrappers from maggie.toml

image: wrap
    mag $(SRC_DIRS) --save-image procyon-park.image

pp: image
    mag build -o pp

test:
    mag test src/

clean:
    rm -rf wrap/ procyon-park.image pp
```

## 3. Go Interop Primitives

### 3.1 Maggie's Primitive System Architecture

Maggie exposes Go functionality through **primitives** — Go functions registered as methods on Maggie classes. The system uses arity-specialized wrappers for performance.

**Core types** (in `vm/method.go`):

```go
type PrimitiveFunc func(vm interface{}, receiver Value, args []Value) Value
type Method0Func  func(vm interface{}, receiver Value) Value
type Method1Func  func(vm interface{}, receiver Value, arg1 Value) Value
type Method2Func  func(vm interface{}, receiver Value, arg1, arg2 Value) Value
// ... up to Method8Func
```

**Registration pattern** (in `vm/*_primitives.go`):

```go
func (vm *VM) registerMyPrimitives() {
    c := vm.MyClass
    c.AddMethod1(vm.Selectors, "doSomething:", func(_ interface{}, recv Value, arg Value) Value {
        // Go implementation
        return result
    })
    c.AddClassMethod0(vm.Selectors, "new", func(_ interface{}, recv Value) Value {
        // Class-side method
        return newInstance
    })
}
```

All primitive registration functions are called during `VM.bootstrap()` in `vm/vm.go`.

**GoObject wrapping** (in `vm/go_object.go`):

Arbitrary Go values can be wrapped as Maggie objects:

```go
// Register a Go type as a Maggie class
serverClass := vm.RegisterGoType("HttpServer", reflect.TypeOf((*http.Server)(nil)))

// Wrap a Go object as a Maggie value
magValue := vm.RegisterGoObject(goObj)

// Retrieve the Go object from a Maggie value
goObj, ok := vm.GetGoObject(magValue)
```

Type conversion between Maggie and Go is automatic for basic types (bool, int, float, string, slices, maps).

### 3.2 Two Approaches to Go Interop

Maggie offers two approaches for Go interop:

#### Approach A: Hand-written Primitives

Write Go functions directly in `vm/*_primitives.go` files. Used for core operations requiring tight VM integration.

**Pros**: Full control, optimal performance, access to VM internals
**Cons**: More Go code to maintain, requires recompiling Maggie itself

**Existing examples**: File I/O, HTTP, gRPC, concurrency primitives — all 20+ primitive files in `vm/`.

#### Approach B: gowrap (Automatic Wrapper Generation)

Define Go packages in `maggie.toml` and let gowrap generate bindings automatically.

**How gowrap works**:
1. **Introspect** (`gowrap/introspect.go`): Analyzes Go package API using `golang.org/x/tools/go/packages`
2. **Generate Go glue** (`gowrap/gen_go.go`): Wrapper functions with Maggie↔Go type conversion
3. **Generate Maggie stubs** (`gowrap/gen_mag.go`): `.mag` class stubs with `<primitive>` markers
4. **Build custom binary** (`gowrap/build.go`): Scaffolding for `go build` with all wrappers linked

**Naming conventions** (`gowrap/naming.go`):
- `encoding/json` → `Go::Json`
- `net/http` → `Go::Http`
- `ReadAll` → `readAll`
- Multi-param → `selector:with:` style

**Pros**: Rapid integration, auto-generated code, scales well
**Cons**: Less control, may need manual tweaks for complex APIs

### 3.3 Recommendation

**Use gowrap for most interop**, falling back to hand-written primitives only where tight VM integration is needed (e.g., process exec with cancellation context, or custom error handling).

## 4. Required Go Interop Primitives

### 4.1 SQLite Bindings

**Purpose**: BBS tuplespace storage, beads issue tracking, configuration persistence.

**imp-castle usage**: SQLite at `~/.imp-castle/bbs/tuplespace.db` (WAL mode) for the Linda-style tuplespace.

**Recommended Go library**: `modernc.org/sqlite` (pure Go, no CGo). This is what the reference implementation already uses (Phase 1), eliminates CGo complexity for SQLite, and simplifies the build. DuckDB still needs CGo regardless.

**Maggie interop design**:

```smalltalk
"Open database"
db := Sqlite open: 'path/to/db.sqlite'.

"Execute SQL"
db exec: 'CREATE TABLE tuples (id INTEGER PRIMARY KEY, category TEXT, scope TEXT, identity TEXT, payload TEXT)'.

"Parameterized query"
rows := db query: 'SELECT * FROM tuples WHERE category = ?' with: #('fact').

"Iterate results"
rows do: [:row |
    row at: 'category'  "=> 'fact'"
].

"Transaction"
db transaction: [
    db exec: 'INSERT INTO tuples (category, scope) VALUES (?, ?)' with: #('claim', 'repo').
].

"Close"
db close.
```

**gowrap approach**: Wrap `database/sql` + SQLite driver. Register `Sqlite` class with methods for open/exec/query/close. Error returns become `Failure` results.

**Key features needed**:
- WAL mode support (PRAGMA journal_mode=WAL)
- Parameterized queries (prevent SQL injection)
- Transaction support
- FTS5 for payload full-text search (used by BBS)
- Row iteration

### 4.2 DuckDB Bindings

**Purpose**: Analytics warm tier — archiving tuples to Parquet, analytical queries over historical data.

**imp-castle usage**: `pp bbs analytics` subcommand uses DuckDB to query Parquet archives.

**Recommended Go library**: `github.com/marcboeker/go-duckdb`

**Maggie interop design**:

```smalltalk
"Open DuckDB (in-memory for analytics)"
duck := DuckDB open.

"Query Parquet files directly"
rows := duck query: 'SELECT category, COUNT(*) FROM read_parquet(''archive/*.parquet'') GROUP BY category'.

"Export to Parquet"
duck exec: 'COPY (SELECT * FROM tuples) TO ''archive/tuples.parquet'' (FORMAT PARQUET)'.

duck close.
```

**gowrap approach**: Wrap `go-duckdb` via `database/sql` interface (same pattern as SQLite). The key value of DuckDB is its ability to query Parquet files directly without loading them.

### 4.3 Process Exec (for git/tmux)

**Purpose**: Spawning and managing external processes — git operations, tmux session management, running shell commands.

**imp-castle usage**:
- `os/exec` for running git commands (worktree create, branch operations)
- tmux integration: `CreateSession`, `SendKeys`, `CapturePane`, `SendControlKeys`
- Waits for shell prompt readiness by polling tmux output

**Recommended Go library**: Standard `os/exec` package.

**Maggie interop design**:

```smalltalk
"Simple exec (blocking)"
result := Process exec: 'git' args: #('status').
result exitCode.   "=> 0"
result stdout.     "=> '## main\n...'"
result stderr.     "=> ''"

"Exec with working directory"
result := Process exec: 'git' args: #('log' '--oneline' '-5') dir: '/path/to/repo'.

"Exec with environment"
result := Process exec: 'tmux' args: #('new-session' '-d' '-s' 'agent-1')
    env: (Dictionary new at: 'PP_AGENT_NAME' put: 'Sprocket'; yourself).

"Async exec (non-blocking)"
proc := Process execAsync: 'long-running-command' args: #().
proc wait.         "Block until done"
proc isAlive.      "Check if still running"
proc terminate.    "Kill process"

"Piped exec"
stdout := Process exec: 'tmux' args: #('capture-pane' '-p' '-t' 'session').

"Exec with timeout (using CancellationContext)"
ctx := CancellationContext withTimeout: 5000.
result := Process exec: 'slow-command' args: #() context: ctx.
```

**Implementation approach**: Hand-written primitives (not gowrap) because:
- Need tight integration with Maggie's Process/CancellationContext
- Need to handle stdout/stderr/exitcode as Maggie values
- tmux key-sending needs careful delay handling (500ms before Enter)

**Key features needed**:
- Blocking and async execution
- Working directory and environment control
- stdout/stderr capture (separately)
- Exit code access
- Process termination/signaling
- Timeout via CancellationContext integration
- Pipe support for chaining commands

### 4.4 Unix Socket Server/Client

**Purpose**: Daemon IPC — the pp daemon listens on a Unix socket for CLI commands.

**imp-castle usage**: Unix socket at `~/.imp-castle/daemon.sock` for daemon communication.

**Recommended Go library**: Standard `net` package (`net.Listen("unix", path)`).

**Maggie interop design**:

```smalltalk
"Server (daemon side)"
server := UnixSocket listen: '/path/to/daemon.sock'.
server onConnect: [:conn |
    data := conn read.
    response := self handleRequest: data.
    conn write: response.
    conn close.
].
server close.

"Client (CLI side)"
conn := UnixSocket connect: '/path/to/daemon.sock'.
conn write: '{"command": "status"}'.
response := conn read.
conn close.
```

**Implementation approach**: gowrap for basic socket operations, with a thin Maggie wrapper class for the higher-level API. Could also leverage Maggie's existing HTTP primitives if the daemon uses HTTP-over-Unix-socket (common pattern).

**Alternative**: If the daemon uses HTTP, use Maggie's existing `HttpServer`/`HttpClient` primitives over a Unix socket transport. This simplifies the API to standard HTTP request/response.

WE HAVE CHOSEN TO USE HTTP RATHER THAN UNIX SOCKETS

### 4.5 File I/O

**Purpose**: Configuration files, state persistence (JSON, TOML), log files, PID files.

**Current Maggie support**: **Already implemented** in `vm/file_primitives.go`. Complete API:

```smalltalk
"Reading/Writing"
contents := File readFileContents: 'config.toml'.
File writeFileContents: '/path/to/file' contents: data.
File appendToFile: '/path/to/log' contents: 'log entry\n'.

"File/Directory testing"
File exists: path.
File isDirectory: path.
File isFile: path.

"Directory operations"
File listDirectory: '/path/to/dir'.
File createDirectory: '/path/to/new/dir'.
File delete: path.
File rename: oldPath to: newPath.

"Path manipulation"
File basename: path.    File dirname: path.
File extension: path.   File join: a with: b.
File absolutePath: path.
File workingDirectory.  File homeDirectory.
```

**Gap analysis**: The existing File primitives cover imp-castle's needs. No additional work required here.

### 4.6 TOML Parsing

**Purpose**: Configuration (`~/.imp-castle.toml`), project manifests (`maggie.toml`), lock files.

**imp-castle usage**: `github.com/BurntSushi/toml` for config parsing.

**Recommended Go library**: `github.com/BurntSushi/toml` (same as imp-castle).

**Maggie interop design**:

```smalltalk
"Parse TOML string"
config := Toml parse: tomlString.
config at: 'project' at: 'name'.  "=> 'procyon-park'"

"Parse TOML file"
config := Toml parseFile: 'config.toml'.

"Generate TOML string"
tomlString := Toml generate: config.

"Write TOML file"
Toml writeFile: 'output.toml' value: config.
```

**Implementation approach**: gowrap for `github.com/BurntSushi/toml`. The key challenge is mapping Go `map[string]interface{}` to Maggie Dictionary and handling nested structures. Maggie's `GoToValue`/`ValueToGo` conversion handles basic types automatically.

### 4.7 CUE Evaluation

**Purpose**: Workflow schema validation — imp-castle uses CUE language for workflow definitions.

**imp-castle usage**: `cuelang.org/go` for parsing and validating workflow CUE files.

**Recommended Go library**: `cuelang.org/go` (official CUE Go SDK).

**Maggie interop design**:

```smalltalk
"Parse CUE file"
value := Cue parseFile: 'workflow.cue'.

"Validate against schema"
result := Cue validate: value against: schema.
result isSuccess.  "=> true/false"

"Extract values"
steps := value at: 'steps'.
steps do: [:step |
    step at: 'type'.     "=> 'spawn'"
    step at: 'params'.   "=> Dictionary"
].

"Evaluate CUE expression"
result := Cue evaluate: 'x: 1 + 2, y: x * 3'.
```

**Implementation approach**: Hand-written Go primitives (NOT gowrap). Phase 6 reveals that the actual CUE requirements far exceed a simple parse-validate-extract API:

- Module-aware loading with `cue.mod/` directory traversal
- Two-phase compilation (parse with stubs, resolve with concrete params)
- `_input` / `_ctx` hidden field injection
- Re-compilation at each step with updated context
- CUE unification for the Evaluate step's output validation
- Schema embedding via `//go:embed`
- Aspect expansion as a post-load transformation

gowrap generates bindings for simple API surfaces, but CUE's usage in the workflow engine requires deep integration with CUE's compiler, module system, and unification engine. The Maggie-side workflow loader should call Go functions for CUE compilation, unification, and value extraction. The Go side handles module resolution, context injection, and re-compilation. This is the same approach used for process exec — "tight VM integration" demands hand-written primitives.

## 5. Existing Maggie Primitives Inventory

Maggie already has 20+ primitive files covering:

| Category | File | Key Classes |
|----------|------|-------------|
| **Arithmetic** | `integer_primitives.go`, `float_primitives.go` | SmallInteger, Float |
| **Collections** | `array_primitives.go`, `dictionary_primitives.go` | Array, Dictionary |
| **Strings** | `string_primitives.go`, `symbol_primitives.go`, `character_primitives.go` | String, Symbol, Character |
| **Logic** | `boolean_primitives.go` | True, False |
| **Blocks** | `block_primitives.go` | Block |
| **Concurrency** | `concurrency.go`, `mutex.go`, `waitgroup.go` | Process, Channel, Mutex, WaitGroup, Semaphore, CancellationContext |
| **File I/O** | `file_primitives.go` | File |
| **HTTP** | `http_primitives.go` | HttpServer, HttpRequest, HttpResponse |
| **gRPC** | `grpc_primitives.go` | GrpcClient |
| **Exceptions** | `exception.go` | Exception handling |
| **Reflection** | `class_reflection_primitives.go` | Class introspection |
| **Compiler** | `compiler_primitives.go` | Dynamic eval, globals, fileIn/fileOut |
| **Objects** | `object_primitives.go` | Base object operations |
| **Context** | `context_primitives.go` | Execution context |
| **Debugging** | `debugger_primitives.go` | IDE/debugger support |
| **Docs** | `docstring_primitives.go` | Documentation strings |
| **Messages** | `message_primitives.go` | Message objects |
| **Weak Refs** | `weak_reference_primitives.go` | Weak references |

**What's already covered for procyon-park**: File I/O, HTTP, gRPC, concurrency (Process/Channel/Mutex/WaitGroup/Semaphore/CancellationContext), collections, strings, dynamic evaluation.

**What's missing**: SQLite, DuckDB, process exec (os/exec), Unix sockets, TOML parsing, CUE evaluation, JSON serialization.

## 6. Interop Layer Design

### 6.1 Hybrid Approach

Use both interop mechanisms strategically:

| Primitive | Approach | Rationale |
|-----------|----------|-----------|
| **SQLite** | gowrap + thin Maggie wrapper | Standard database/sql interface maps well to auto-generation |
| **DuckDB** | gowrap | Same database/sql pattern as SQLite |
| **Process exec** | Hand-written primitive | Needs CancellationContext integration, stdout/stderr separation |
| **Unix sockets** | gowrap or HTTP-over-Unix | Reuse existing HTTP primitives if possible |
| **File I/O** | Already exists | No work needed |
| **TOML** | gowrap | Simple parse/generate API |
| **CUE** | Hand-written primitive | Deep integration with CUE compiler, module system, and unification engine (see §4.7) |
| **JSON** | Hand-written or gowrap | May already be partially available via Dictionary#asJson |

LETS DEFINITELY IMPLEMENT ROBUST JSON SUPPORT IN MAGGIE IF IT ISNT THERE ALREADY

### 6.2 Namespace Convention

All Go-interop classes for procyon-park should live under a consistent namespace:

```
ProcyonPark::Db::Sqlite      -- SQLite database
ProcyonPark::Db::DuckDB      -- DuckDB analytics
ProcyonPark::Sys::Exec       -- Process execution
ProcyonPark::Sys::Socket     -- Unix socket IPC
ProcyonPark::Config::Toml    -- TOML parsing
ProcyonPark::Config::Cue     -- CUE evaluation (hand-written, not gowrap)
```

Or, if using gowrap's default naming (for gowrap-compatible packages only):

```
Go::Sqlite                    -- from modernc.org/sqlite
Go::Duckdb                    -- from github.com/marcboeker/go-duckdb
Go::Toml                      -- from github.com/BurntSushi/toml
```

### 6.3 Error Handling Convention

All interop primitives should use Maggie's Result pattern:

```smalltalk
result := Sqlite open: 'db.sqlite'.
result isSuccess ifTrue: [
    db := result value.
    "... use db ..."
] ifFalse: [
    'Failed: ', result error
].
```

This maps to the Go pattern of `(value, error)` returns. gowrap auto-detects error returns and wraps them as `Failure` results.

## 7. Go Module Dependencies

### 7.1 Required Dependencies

```
modernc.org/sqlite                -- SQLite (pure Go, no CGo)
github.com/marcboeker/go-duckdb   -- DuckDB analytics
github.com/BurntSushi/toml        -- TOML parsing
cuelang.org/go                    -- CUE evaluation (hand-written primitives, not gowrap)
```

### 7.2 Dependencies from imp-castle (for reference)

imp-castle's go.mod uses:
- `cuelang.org/go v0.15.4` — CUE for workflow schemas
- `github.com/BurntSushi/toml v1.6.0` — TOML config
- `github.com/dgraph-io/badger/v4 v4.9.1` — Key-value store (message store)
- `github.com/spf13/cobra v1.10.2` — CLI framework
- `golang.org/x/sys v0.41.0` — System calls

**Note**: imp-castle uses BadgerDB for its key-value message store, but procyon-park should use SQLite for the BBS tuplespace (as imp-castle's BBS already does — the `bbs.db` and `tuplespace.db` files at `~/.imp-castle/bbs/` are SQLite databases).

### 7.3 CGo Consideration

`go-duckdb` requires CGo. `modernc.org/sqlite` is pure Go and does not require CGo. This means CGo is only needed for DuckDB (analytics tier, deferrable). The core system (BBS tuplespace, config, work tracking) can be built without CGo.

For DuckDB's CGo requirement:
- Cross-compilation is harder (need C toolchain for target platform)
- Build times are longer
- Binary is slightly larger

## 8. Key Risks and Open Questions

### 8.1 gowrap Maturity

The gowrap system exists and works for basic wrapping, but its handling of complex APIs (CUE's type system, DuckDB's appender API) may need manual adjustment. Plan for a prototype phase to validate gowrap output for each library.

### 8.2 SQLite Library Choice

**Decided:** Use `modernc.org/sqlite` (pure Go). This is what the reference implementation already uses, eliminates CGo complexity for SQLite (DuckDB still needs CGo), and simplifies cross-compilation if ever needed.

### 8.3 Process Exec Security

Running arbitrary commands (git, tmux, shell) from Maggie code is a security boundary. Maggie's **process-level restriction** system (fork with restricted globals) can sandbox which classes are visible, but exec primitives should also:
- Validate command paths
- Prevent shell injection (use exec directly, not via shell)
- Respect CancellationContext for timeouts

### 8.4 Image Size

Embedding all wrapped Go packages will increase the binary size. Current Maggie binary is ~21-29MB. Adding SQLite + DuckDB + CUE could push this to 40-60MB. This is acceptable for a CLI tool.

## 9. Summary and Next Steps

### What Maggie Already Provides
- File I/O (complete)
- HTTP server/client
- gRPC client/server
- Full concurrency primitives (Process, Channel, Mutex, WaitGroup, Semaphore, CancellationContext)
- Dynamic code evaluation (Compiler evaluate:)
- Module/namespace system
- Project manifest (maggie.toml)
- Image persistence
- gowrap for automatic Go package wrapping

### What Needs to Be Built
1. **SQLite bindings** — via gowrap or hand-written primitives
2. **DuckDB bindings** — via gowrap
3. **Process exec primitives** — hand-written (needs VM integration)
4. **Unix socket primitives** — via gowrap or reuse HTTP primitives
5. **TOML parsing** — via gowrap
6. **CUE evaluation** — hand-written primitives (deep integration with CUE compiler, module system, unification)

### Recommended Implementation Order
1. Process exec (critical for git/tmux — blocks agent lifecycle)
2. SQLite (critical for BBS tuplespace — blocks most subsystems)
3. TOML (needed for config — blocks daemon/CLI)
4. Unix sockets (needed for daemon IPC)
5. CUE (needed for workflow engine)
6. DuckDB (analytics tier — can be deferred)
