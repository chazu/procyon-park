# Phase 2 Research: Daemon Architecture

**Date:** 2026-02-23
**Status:** Research document
**Scope:** How to run a Maggie VM as a long-lived daemon process for procyon-park, covering IPC, image management, crash recovery, and lifecycle

---

## Context

Procyon-park reimplements imp-castle in Maggie. The daemon is the central process: a long-running Maggie VM that hosts the tuplespace, manages agent lifecycles, and serves CLI commands over IPC. The existing imp-castle daemon runs as a Go binary at `~/.imp-castle/daemon.pid` with a Unix socket at `~/.imp-castle/daemon.sock`. This document researches the design space for a Maggie-native equivalent.

### What imp-castle's daemon does today

The imp-castle daemon is a background Go process that:
- Monitors agent mailboxes for new messages
- Delivers notifications to agents via tmux
- Manages a PID file (`daemon.pid`) and Unix socket (`daemon.sock`)
- Persists notification state to `daemon/notification-state.json`
- Responds to `pp daemon status`, `pp daemon run`, `pp daemon stop`
- Runs indefinitely until explicitly stopped or the system reboots

### What the procyon-park daemon must do

The procyon-park daemon is more ambitious. It runs a Maggie VM image that hosts:
- The BBS tuplespace (Phase 1)
- Agent lifecycle management (Phase 3)
- Workflow execution (Phase 6)
- CLI command dispatch (Phase 4)

The daemon is the *kernel* of the system. Everything else talks to it.

---

## 1. Long-Running VM Image as Daemon

### The core idea

A Maggie VM boots from an image file, enters a main loop, and serves requests until stopped. The image contains all compiled classes and methods needed for the system. On clean shutdown, the VM saves its state back to the image. On next startup, it resumes from the saved state.

This is the Smalltalk model: the image *is* the system. The daemon is simply the image running.

### Startup sequence

```
1. Read PID file → if stale, remove; if live, abort ("daemon already running")
2. Write PID file with current PID
3. Load image from disk (or embedded bytes)
   - Custom image:  vm.LoadImage("~/.procyon-park/daemon.image")
   - Fallback:      vm.LoadImageFromBytes(embeddedImage)
4. Re-register critical primitives (ReRegisterNilPrimitives, ReRegisterBooleanPrimitives)
5. Wire up Go compiler backend
6. Open IPC socket
7. Enter main loop (event-driven, see Section 5)
8. On signal (SIGTERM/SIGINT): initiate graceful shutdown
```

Maggie already supports this flow. The `cmd/mag/main.go` entry point demonstrates the pattern: load image → compile sources → start server → serve. The daemon variant replaces "serve HTTP" with "serve Unix socket."

### Image embedding vs external file

Two strategies:

**Embedded image** (Go `//go:embed`): The daemon binary includes a compiled image. Advantages: single-file deployment, version-locked image+binary. Disadvantages: binary grows with image size, cannot update image without rebuilding.

**External image file**: The daemon loads `~/.procyon-park/daemon.image` at startup. Advantages: image can be updated independently, enables image evolution over time. Disadvantages: image/binary version mismatch risk.

**Recommendation:** Use both. Embed a base image in the binary for bootstrap. On first run, save the running image to the external path. On subsequent runs, load the external image (which may have accumulated state). Fall back to embedded if the external image is missing or corrupt.

```go
imagePath := filepath.Join(dataDir, "daemon.image")
if err := vm.LoadImage(imagePath); err != nil {
    // Fall back to embedded
    vm.LoadImageFromBytes(embeddedImage)
    // Save so next boot uses external
    vm.SaveImage(imagePath)
}
```

---

## 2. IPC Server Design

### Option A: Unix Socket with JSON-RPC

**How it works:** The daemon listens on a Unix domain socket (e.g., `~/.procyon-park/daemon.sock`). Clients connect, send JSON-RPC 2.0 requests, receive JSON-RPC responses. The wire format is newline-delimited JSON.

**Request:**
```json
{"jsonrpc":"2.0","method":"bbs.out","params":{"category":"fact","scope":"myrepo","identity":"key","payload":{}},"id":1}
```

**Response:**
```json
{"jsonrpc":"2.0","result":{"tuple_id":42},"id":1}
```

**Advantages:**
- Simple to implement (Go `net` package + `encoding/json`)
- Easy to debug (`socat` or `nc` for manual testing)
- Thin client: the CLI just formats JSON and reads JSON
- No code generation, no protobuf toolchain
- Matches the simplicity ethos of a Smalltalk system

**Disadvantages:**
- No schema enforcement (errors at runtime, not compile time)
- No streaming (notifications require polling or a separate channel)
- JSON parsing overhead (negligible for this workload)

**Implementation sketch (Go side):**
```go
listener, _ := net.Listen("unix", socketPath)
for {
    conn, _ := listener.Accept()
    go handleConnection(conn, vmWorker)
}

func handleConnection(conn net.Conn, w *VMWorker) {
    scanner := bufio.NewScanner(conn)
    for scanner.Scan() {
        req := parseJSONRPC(scanner.Bytes())
        result, err := w.Do(func(vm *vm.VM) interface{} {
            return dispatch(vm, req.Method, req.Params)
        })
        writeJSONRPC(conn, req.ID, result, err)
    }
}
```

### Option B: gRPC Connect over Unix Socket

**How it works:** The daemon runs a Connect (HTTP/JSON + gRPC binary) server, but listens on a Unix socket instead of a TCP port. Clients use generated Connect stubs.

**Advantages:**
- Schema-enforced API (protobuf)
- Bidirectional streaming for notifications
- Type-safe client generation
- Maggie already has Connect infrastructure (`server/server.go`)
- Can expose on TCP later for remote access without changing the protocol

**Disadvantages:**
- Heavier dependency chain (protobuf, Connect runtime, code generation)
- More complex debugging (need `grpcurl` or generated client)
- HTTP/2 framing overhead on a local socket (minor)

**Implementation sketch:**
```go
listener, _ := net.Listen("unix", socketPath)
mux := http.NewServeMux()
// Register service handlers (same pattern as server/server.go)
path, handler := procyonv1connect.NewDaemonServiceHandler(svc)
mux.Handle(path, handler)
http.Serve(listener, mux)
```

### Option C: Hybrid

JSON-RPC for the CLI-facing command channel (simple, debuggable). A separate Connect endpoint for streaming (notifications, log tailing, workflow events). Both on the same Unix socket, distinguished by a framing byte or separate socket paths.

### Recommendation

**Start with JSON-RPC over Unix socket (Option A).** Reasons:

1. The CLI is the primary client. CLI commands are request/response. JSON-RPC is the simplest protocol that works.
2. Notifications can be handled via polling initially (the CLI calls `bbs pulse` which is already a poll). Streaming can be added later.
3. The procyon-park system is local-only for the foreseeable future. The benefits of Connect (schema evolution, cross-language clients, TCP exposure) are premature.
4. JSON-RPC is trivially implementable in Maggie itself. A Maggie class can parse JSON-RPC requests and dispatch to methods. This means the daemon's command handling can eventually be written in Maggie, not Go.
5. If gRPC/Connect is needed later (e.g., for remote hub connections), it can be added as a second listener without replacing the JSON-RPC interface.

**Socket path:** `~/.procyon-park/daemon.sock` (configurable via `PP_DAEMON_SOCKET` env var or config file).

---

## 3. Image Checkpointing and Crash Recovery

### The problem

A long-running VM accumulates state: tuplespace contents, agent registries, workflow state. If the daemon crashes, this state is lost unless periodically saved.

### Checkpointing strategy

**Periodic snapshots:** Save the full VM image to disk at regular intervals.

```
daemon.image        ← current snapshot
daemon.image.prev   ← previous snapshot (for rollback)
daemon.image.tmp    ← in-progress write (atomic rename on completion)
```

**Write protocol (crash-safe):**
1. Write to `daemon.image.tmp`
2. `fsync` the file
3. Rename `daemon.image` → `daemon.image.prev`
4. Rename `daemon.image.tmp` → `daemon.image`
5. `fsync` the directory

This ensures that `daemon.image` is always a complete, valid snapshot. If the daemon crashes during step 1 or 2, the previous `daemon.image` is untouched. If it crashes during step 3 or 4, `daemon.image.prev` is available.

**Checkpoint frequency:** Every 5 minutes by default, configurable. Also checkpoint on clean shutdown (before closing the socket).

**Maggie-side API:**
```smalltalk
Daemon checkpointEvery: 5 minutes.
Daemon checkpointNow.
Daemon onShutdown: [Daemon checkpointNow].
```

### What the image captures

The Maggie image format captures:
- All classes, methods, selectors, symbols, strings
- Global variables
- Object graph (instances with slot values)

What it does **not** capture:
- Go-side state (goroutines, channels, file descriptors, sockets)
- In-flight IPC requests
- OS resources (PID file, socket)

This means the checkpoint is a **logical snapshot**, not a process snapshot. On recovery, the daemon:
1. Loads the image (restoring classes, methods, globals, objects)
2. Rebuilds Go-side infrastructure (open socket, start goroutines)
3. The tuplespace is restored (its data is in the object graph)
4. In-flight operations are lost (clients must retry)

### Dual persistence: image + SQLite

For the tuplespace specifically, relying solely on image snapshots means up to 5 minutes of data loss on crash. An alternative: persist tuples to SQLite write-ahead, and use the image only for class/method state.

**Hybrid approach:**
- Tuplespace tuples: written to SQLite on every mutation (WAL mode, fast)
- VM class/method state: saved to image periodically
- On startup: load image for classes/methods, load SQLite for tuples

This matches imp-castle's current pattern (BBS uses SQLite: `bbs.db`). The image adds fast class/method loading that SQLite cannot provide.

**Recommendation:** Use SQLite for tuplespace persistence (critical, transactional data) and image snapshots for VM class state (large, rarely-changing data). This gives the best of both: sub-second tuplespace durability and fast class loading.

---

## 4. Polling Loops vs Event-Driven Design

### The question

Should the daemon's main loop poll for work (check mailboxes, check timers, check socket) or react to events (socket readable, signal received, timer fired)?

### Polling (imp-castle's current approach)

```go
for {
    checkMailboxes()
    checkTimers()
    sleep(pollInterval)
}
```

**Advantages:** Simple, predictable, easy to reason about.
**Disadvantages:** Latency proportional to poll interval. CPU waste when idle. Tuning the interval is a tradeoff (fast = wasteful, slow = laggy).

### Event-driven (recommended for procyon-park)

```go
for {
    select {
    case req := <-ipcRequests:
        handleRequest(req)
    case sig := <-signals:
        handleSignal(sig)
    case <-checkpointTicker.C:
        checkpoint()
    case <-quit:
        return
    }
}
```

**Advantages:** Zero CPU when idle. Instant response to events. Clean composition of multiple event sources via Go's `select`.
**Disadvantages:** Slightly more complex setup (channels for each event source).

### How this maps to Maggie

In the Maggie VM, the event loop should be expressible in Maggie itself:

```smalltalk
Daemon mainLoop
    | ipcChannel signalChannel tickChannel |
    ipcChannel := self ipcRequestChannel.
    signalChannel := self signalChannel.
    tickChannel := Clock tickEvery: 5 minutes.

    [true] whileTrue: [
        Channel select: {
            ipcChannel onReceive: [:req | self handleRequest: req].
            signalChannel onReceive: [:sig | self handleSignal: sig].
            tickChannel onReceive: [:t | self checkpoint]
        }
    ]
```

Maggie's `Channel select:` maps directly to Go's `select`. The daemon loop is event-driven at the Maggie level, backed by Go's efficient channel multiplexing.

### Recommendation

**Event-driven main loop.** Use Go channels as the event delivery mechanism. The main loop is a `select` over IPC requests, OS signals, and periodic timers. No polling. The Maggie-level code uses `Channel select:` for the same pattern.

---

## 5. Startup Sequence

### Full startup flow

```
procyon-park daemon run
│
├─ 1. Parse config (~/.procyon-park/config.toml)
│     - data_dir, socket_path, checkpoint_interval, log_level
│
├─ 2. Check PID file
│     ├─ File exists, PID alive → "daemon already running", exit 1
│     ├─ File exists, PID dead → remove stale PID file, continue
│     └─ File missing → continue
│
├─ 3. Daemonize (if not --foreground)
│     - Fork, setsid, redirect stdout/stderr to log file
│     - Write new PID to PID file
│
├─ 4. Initialize VM
│     ├─ Load image (external → embedded fallback)
│     ├─ Re-register primitives
│     ├─ Wire compiler backend
│     └─ Load any additional source overlays
│
├─ 5. Initialize persistence
│     ├─ Open SQLite database (tuplespace)
│     ├─ Run migrations if needed
│     └─ Load tuplespace state into VM
│
├─ 6. Open IPC socket
│     ├─ Remove stale socket file if exists
│     ├─ Listen on Unix socket
│     └─ Set permissions (0700 - owner only)
│
├─ 7. Set up signal handlers
│     ├─ SIGTERM, SIGINT → graceful shutdown
│     ├─ SIGHUP → reload config (optional)
│     └─ SIGUSR1 → force checkpoint
│
├─ 8. Start background tasks
│     ├─ Checkpoint timer
│     ├─ Stale tuple GC timer
│     └─ Agent liveness checker (Phase 3)
│
├─ 9. Enter main event loop
│     └─ select on: IPC, signals, timers
│
└─ 10. Ready
      - Log "daemon started, PID <pid>, socket <path>"
      - Accept IPC connections
```

### Foreground vs background mode

For development: `procyon-park daemon run --foreground` keeps the process in the foreground with logs to stdout. For production: the default forks to background.

Go daemons typically don't double-fork (the Unix tradition). Instead, use systemd/launchd to manage the process. The `--foreground` flag is always safe.

---

## 6. PID File Management

### Purpose

The PID file serves two functions:
1. **Singleton enforcement:** Prevent multiple daemon instances
2. **Process discovery:** Let the CLI find the daemon's PID for status/stop commands

### Implementation

**Location:** `~/.procyon-park/daemon.pid`

**Contents:** The PID as a decimal string, followed by a newline. Nothing else.

**Locking protocol:**

```go
func acquirePIDFile(path string) error {
    // Open with exclusive creation
    f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
    if err != nil {
        if os.IsExist(err) {
            // File exists - check if process is alive
            existingPID := readPID(path)
            if isProcessAlive(existingPID) {
                return fmt.Errorf("daemon already running (PID %d)", existingPID)
            }
            // Stale PID file - remove and retry
            os.Remove(path)
            return acquirePIDFile(path)
        }
        return err
    }
    fmt.Fprintf(f, "%d\n", os.Getpid())
    f.Close()
    return nil
}

func isProcessAlive(pid int) bool {
    proc, err := os.FindProcess(pid)
    if err != nil {
        return false
    }
    // On Unix, FindProcess always succeeds. Send signal 0 to check.
    return proc.Signal(syscall.Signal(0)) == nil
}
```

**Cleanup:** Remove the PID file on graceful shutdown. If the daemon crashes, the stale PID file is detected on next startup (the PID will not be alive).

**Race condition mitigation:** The `O_CREATE|O_EXCL` open is atomic on the filesystem. Two concurrent `daemon run` commands will not both succeed. However, the check-then-remove-then-retry for stale files has a TOCTOU window. In practice, this is fine for a single-user system. For additional safety, use `flock(2)` on the PID file.

### Comparison with imp-castle

Cub-castle uses the same pattern: `~/.imp-castle/daemon.pid` contains the PID (currently `77944`). The `pp daemon status` command reads this file and sends signal 0 to check liveness. The procyon-park implementation follows the identical pattern.

---

## 7. Graceful Shutdown

### Shutdown sequence

```
Signal received (SIGTERM/SIGINT) or "daemon stop" command
│
├─ 1. Stop accepting new IPC connections
│     - Close the listener (new connections get "connection refused")
│     - Existing connections continue
│
├─ 2. Drain in-flight requests
│     - Wait up to N seconds for active requests to complete
│     - After timeout, cancel remaining requests
│
├─ 3. Final checkpoint
│     - Save VM image to disk
│     - Flush SQLite WAL
│
├─ 4. Stop background tasks
│     - Cancel checkpoint timer
│     - Cancel GC timer
│     - Cancel liveness checker
│
├─ 5. Close IPC socket
│     - Close all client connections
│     - Remove socket file
│
├─ 6. Remove PID file
│
├─ 7. Close databases
│     - Close SQLite connection
│
└─ 8. Exit 0
```

### Implementation pattern

```go
func (d *Daemon) Shutdown(ctx context.Context) error {
    // 1. Stop listener
    d.listener.Close()

    // 2. Wait for in-flight requests (with timeout from ctx)
    d.wg.Wait() // WaitGroup tracks active requests

    // 3. Checkpoint
    d.checkpoint()

    // 4-5. Cleanup
    d.stopTimers()
    os.Remove(d.socketPath)
    os.Remove(d.pidPath)

    // 6. Close DB
    d.db.Close()

    return nil
}
```

### Shutdown timeout

Default: 30 seconds. If in-flight requests don't complete within the timeout, force-close connections. This prevents a hung client from blocking shutdown indefinitely.

### Double-signal handling

First SIGTERM/SIGINT → graceful shutdown. Second SIGTERM/SIGINT (within the shutdown window) → immediate exit. This gives users an escape hatch if graceful shutdown hangs.

```go
sigCh := make(chan os.Signal, 2)
signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

<-sigCh // first signal
go func() {
    <-sigCh // second signal during shutdown
    log.Fatal("forced shutdown")
}()

ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
d.Shutdown(ctx)
```

---

## 8. How Maggie's Eval Service Maps to Daemon IPC

### Current Maggie server architecture

Maggie's `MaggieServer` (in `server/server.go`) wraps a VM in:
- A `VMWorker` that serializes all VM access through a single goroutine
- A `HandleStore` that manages object references with TTL-based lifecycle
- A `SessionStore` for multi-session isolation
- Six gRPC/Connect services (Evaluation, Session, Browsing, Modification, Inspection, Sync)

### Mapping to daemon IPC

The daemon's IPC layer follows the same architecture:

```
CLI command
  → JSON-RPC request over Unix socket
    → Daemon parses request, identifies method
      → VMWorker.Do(func(vm) { ... })
        → VM executes Maggie code
      → VMWorker returns result
    → Daemon formats JSON-RPC response
  → CLI receives response
```

**Key insight:** The `VMWorker` pattern is the critical piece. It ensures the single-threaded Maggie VM is safely accessible from multiple concurrent IPC connections. Each IPC connection runs in its own goroutine, but all VM access is serialized through the worker's channel.

### Daemon-specific services (mapped from imp-castle commands)

| CLI command | JSON-RPC method | VM action |
|------------|----------------|-----------|
| `bd create --title=...` | `beads.create` | Create tuple in tuplespace |
| `bd list` | `beads.list` | Query tuplespace |
| `bd update <id> --status=...` | `beads.update` | Update tuple |
| `bd close <id>` | `beads.close` | Update tuple status |
| `pp bbs out <cat> <scope> <id> <payload>` | `bbs.out` | Write tuple |
| `pp bbs in <cat> <scope> <id>` | `bbs.in` | Consume tuple (blocking) |
| `pp bbs rd <cat> <scope> <id>` | `bbs.rd` | Read tuple (blocking) |
| `pp bbs scan <cat> <scope>` | `bbs.scan` | List matching tuples |
| `pp spawn ...` | `agent.spawn` | Create agent |
| `pp dismiss ...` | `agent.dismiss` | Dismiss agent |
| `pp list` | `agent.list` | List agents |
| `cub status` | `system.status` | System status |
| `pp daemon stop` | `system.shutdown` | Initiate shutdown |

### Blocking operations

Some BBS operations block (`in` waits for a matching tuple). In the JSON-RPC model, this means the connection stays open until the tuple arrives. The VMWorker must handle this without blocking other requests.

**Approach:** The blocking `in` operation registers a waiter in the tuplespace (a Go channel). The VMWorker returns immediately. A separate goroutine waits on the channel and writes the response when the tuple arrives. The IPC connection stays open but the VMWorker is free.

```go
// Inside the VMWorker.Do callback for bbs.in:
waiter := tuplespace.RegisterWaiter(pattern)
// Return the waiter channel to the IPC handler
return waiter

// IPC handler (outside VMWorker):
select {
case tuple := <-waiter:
    writeResponse(conn, tuple)
case <-ctx.Done():
    tuplespace.CancelWaiter(waiter)
    writeError(conn, "timeout")
}
```

---

## 9. Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| IPC protocol | JSON-RPC over Unix socket | Simplest correct solution; debuggable; implementable in Maggie |
| Image strategy | External file + embedded fallback | Allows image evolution; safe bootstrap |
| Tuplespace persistence | SQLite (not image) | Sub-second durability; transactional; matches imp-castle |
| Class/method persistence | Image snapshots | Fast loading; captures full VM state |
| Main loop | Event-driven (Go select / Maggie Channel select) | Zero idle CPU; instant response |
| Checkpoint frequency | 5 minutes + on shutdown | Balances I/O cost and data loss window |
| PID file | ~/.procyon-park/daemon.pid with O_EXCL | Standard Unix pattern; atomic creation |
| Socket | ~/.procyon-park/daemon.sock, mode 0700 | Standard Unix pattern; owner-only access |
| Graceful shutdown | Drain + timeout + double-signal escape | Reliable cleanup with user escape hatch |
| Blocking BBS operations | Waiter channels outside VMWorker | Non-blocking VMWorker; correct concurrency |

---

## 10. Open Questions

1. **Should the daemon also expose a TCP port?** For remote hub connections (multi-machine imp-castle), TCP is needed. But this is Phase 4+ territory. Start Unix-only.

2. **Should the CLI be a compiled Go binary or a Maggie script?** A Go binary is faster to start and can handle the Unix socket connection. A Maggie script would need `mag` to boot a VM just to send an IPC request. **Recommendation:** Go binary for the CLI, Maggie for the daemon. Decision: ONE BINARY ONLY - MAGGIE VM WITH CUSTOM ENTRYPOINT

3. **Image format versioning.** Maggie's image format is at version 4. Procyon-park will extend the VM with new primitives (tuplespace, agent management). Will the image format need new versions? Likely yes. Plan for `ImageVersion = 5` with procyon-park extensions.

4. **Hot reload.** Can the daemon reload its Maggie code without restarting? Maggie's `VMWorker` and the modification service already support live method replacement. The daemon could watch source files and hot-reload changed methods. This is a nice-to-have, not a must-have for Phase 2.

5. **Multiple daemons per user.** The current design assumes one daemon. For development (testing against multiple repos), should multiple daemons be supported? If so, parameterize the socket/PID paths by project or instance name.

---

## References

- Maggie server architecture: `/Users/chazu/dev/go/maggie/server/server.go`
- VMWorker pattern: `/Users/chazu/dev/go/maggie/server/vm_worker.go`
- Image format: `/Users/chazu/dev/go/maggie/vm/image_writer.go`, `image_reader.go`
- CLI entry point: `/Users/chazu/dev/go/maggie/cmd/mag/main.go`
- Distributed runtime roadmap: `/Users/chazu/dev/go/maggie/docs/roadmaps/2026-02-03-distributed-runtime-roadmap.md`
- Cub-castle daemon: `~/.imp-castle/daemon.pid`, `~/.imp-castle/daemon.sock`
- Maggie concurrency: `/Users/chazu/dev/go/maggie/vm/concurrency.go`
- Maggie Channel select: `/Users/chazu/dev/go/maggie/CLAUDE.md`
