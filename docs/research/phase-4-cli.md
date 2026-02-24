# Phase 4 Research: CLI Design

**Issue:** procyon-park-3dk
**Author:** Ziggy
**Date:** 2026-02-23
**Status:** Complete

## Overview

This document covers the CLI architecture for procyon-park: how the user (and agents) invoke commands, how those commands reach the daemon, and how results are formatted and returned. The CLI is the primary interface to the entire system — every agent lifecycle operation, tuplespace interaction, workflow trigger, and configuration change flows through it.

**Scope:** Thin Go binary vs Maggie script vs single binary with subcommands, IPC protocol for CLI-to-daemon communication, command structure, agent-facing commands, argument parsing, output formatting.

---

## 1. Architecture Decision: Go CLI Binary

### The question

Three options for the CLI:

1. **Thin Go binary** — Go handles argument parsing, connects to daemon over IPC, formats output. The daemon (Maggie VM) does all real work.
2. **Maggie script** — The CLI is a Maggie program. Each invocation boots a Maggie VM, parses args, connects to daemon.
3. **Single binary with everything** — One Go binary contains both CLI and daemon, selected by subcommand (`procyon-park daemon run` vs `procyon-park agent spawn`).

### Decision: Option 3 — Single Go binary, thin CLI layer, full Maggie image

**Rationale:**

1. **Startup latency.** A Go binary starts in ~5ms. Booting a Maggie VM to parse CLI args would add 100-500ms per invocation. Agents call CLI commands hundreds of times per session. This latency compounds.

2. **Distribution simplicity.** One binary to install, one binary to version. No risk of CLI/daemon version mismatch.

3. **The daemon is Maggie.** The daemon runs a long-lived Maggie VM (Phase 2 research). The CLI doesn't need Maggie — it just formats a JSON-RPC request, sends it over a Unix socket, and formats the response. This is trivial Go code.

4. **Consistency with imp-castle.** imp-castle uses exactly this pattern: single Go binary (`cub`), Cobra for CLI, Unix socket for daemon IPC. It works well.

5. **Agent-friendliness.** AI agents in tmux panes need fast, predictable CLI responses. A Go binary is the fastest option.

### What lives where

| Component | Language | Runs where |
|-----------|----------|------------|
| CLI arg parsing | Go (Cobra) | In the binary, every invocation |
| IPC client | Go (net.Dial unix) | In the binary, every invocation |
| Command dispatch | Maggie | In the daemon VM |
| Tuplespace operations | Maggie | In the daemon VM |
| Agent lifecycle | Go + Maggie | Go for tmux/git, Maggie for state |
| Output formatting | Go | In the binary, every invocation |

The CLI binary is deliberately "dumb." It knows how to:
1. Parse arguments into a structured request
2. Connect to the daemon socket
3. Send a JSON-RPC request
4. Receive a JSON-RPC response
5. Format the response for the terminal

All business logic lives in the daemon's Maggie VM.

---

## 2. IPC Protocol: JSON-RPC over Unix Socket

### Why JSON-RPC

Phase 2 research already recommended JSON-RPC over Unix socket. The CLI design reinforces this choice:

1. **Debuggable.** `echo '{"jsonrpc":"2.0","method":"bbs.scan","params":{"category":"fact"},"id":1}' | socat - UNIX-CONNECT:~/.procyon-park/daemon.sock` — instant debugging without special tools.

2. **Thin client.** The CLI only needs `encoding/json` and `net`. No protobuf compiler, no generated stubs, no gRPC runtime.

3. **Implementable in Maggie.** The daemon-side dispatcher can be written in Maggie: parse JSON → dispatch to method → return JSON. This means CLI command handling can evolve without recompiling the Go binary.

4. **Sufficient for the workload.** CLI commands are request/response. No streaming needed. Blocking BBS operations (`in`, `rd`) hold the connection open until the tuple arrives — this is naturally supported by keeping the socket open.

### Wire format

Newline-delimited JSON-RPC 2.0 over a Unix domain socket.

**Request:**
```json
{"jsonrpc":"2.0","method":"agent.spawn","params":{"role":"cub","task":"procyon-park-3dk","repo":"procyon-park"},"id":1}
```

**Response (success):**
```json
{"jsonrpc":"2.0","result":{"name":"Marble","session":"pp-procyon-park-Marble","worktree":"/path/to/worktree"},"id":1}
```

**Response (error):**
```json
{"jsonrpc":"2.0","error":{"code":-32000,"message":"agent not found","data":{"requested":"Gizmo","available":["Marble","Sprocket"]}},"id":1}
```

### Method naming convention

Methods use dot-separated `<service>.<action>` names:

| Service | Methods |
|---------|---------|
| `agent` | `spawn`, `dismiss`, `list`, `show`, `respawn`, `pause`, `resume` |
| `bbs` | `out`, `in`, `rd`, `scan`, `pulse`, `seed-available` |
| `workflow` | `run`, `list`, `show`, `cancel`, `approve`, `reject` |
| `system` | `status`, `shutdown`, `ping`, `version` |
| `config` | `get`, `set`, `list` |
| `repo` | `register`, `unregister`, `list`, `status` |
| `telemetry` | `enable`, `disable`, `status`, `export` |

### Socket path

`~/.procyon-park/daemon.sock` (default). Overridable via:
1. `--socket` flag on any CLI command
2. `PROCYON_PARK_SOCK` environment variable
3. `daemon.socket` in config file

### Connection lifecycle

```
CLI invocation
  → Open Unix socket connection
  → Write JSON-RPC request + newline
  → Read JSON-RPC response (newline-terminated)
  → Close connection
  → Format output
  → Exit with appropriate code
```

One connection per CLI invocation. No connection pooling. The daemon handles many concurrent connections via goroutines (each dispatched to the VMWorker for serialized VM access).

### Auto-start daemon

If the CLI cannot connect to the socket, it should attempt to start the daemon automatically:

```go
func ensureDaemon(socketPath string) error {
    conn, err := net.Dial("unix", socketPath)
    if err == nil {
        conn.Close()
        return nil // daemon is running
    }
    // Start daemon in background
    cmd := exec.Command(os.Args[0], "daemon", "run", "--background")
    cmd.Start()
    // Wait up to 5s for socket to appear
    return waitForSocket(socketPath, 5*time.Second)
}
```

This eliminates the need for users to explicitly start the daemon before using other commands.

---

## 3. CLI Framework: Cobra

### Why Cobra

1. **Scale.** ~40 subcommands organized in groups. Cobra handles this well with command trees, help generation, and shell completion.

2. **Battle-tested.** Powers kubectl (100+ commands), docker, gh, terraform. The command patterns we need are identical.

3. **imp-castle precedent.** imp-castle already uses Cobra with 25 command files. The team knows the patterns.

4. **Testability.** Cobra separates command definition from execution. Commands can be tested by programmatically invoking them with args.

5. **Shell completion.** Cobra generates bash/zsh/fish completions automatically. Critical for human users.

### Alternatives considered

**Kong (alecthomas/kong):** Struct-based, cleaner API. But smaller ecosystem, fewer examples at our scale. Good for 10-20 commands, less proven at 40+.

**urfave/cli:** v2 is legacy, v3 is actively developed. Less feature-rich than Cobra for command grouping and help customization. Not recommended for new projects at this scale.

### Command registration pattern

Follow imp-castle's pattern: each command group in its own file under `internal/cli/`, registered in `root.go`:

```go
// internal/cli/root.go
func init() {
    rootCmd.AddCommand(agentCmd)
    rootCmd.AddCommand(bbsCmd)
    rootCmd.AddCommand(workflowCmd)
    rootCmd.AddCommand(daemonCmd)
    rootCmd.AddCommand(configCmd)
    rootCmd.AddCommand(repoCmd)
    rootCmd.AddCommand(telemetryCmd)
    rootCmd.AddCommand(primeCmd)   // agent-facing
    rootCmd.AddCommand(statusCmd)  // convenience alias
    rootCmd.AddCommand(versionCmd)
}
```

---

## 4. Command Structure

### Design principle: noun-verb at two levels

```
pp <noun> <verb> [args] [flags]
```

Two levels of nesting. No deeper. The binary name is `pp` (short for procyon-park) for ergonomics — agents type this hundreds of times.

**Why `pp`?** `procyon-park` is 12 characters. `pp` is 2. Agents type commands in tmux. Every saved keystroke matters when an agent runs 200+ commands per session. The full name `procyon-park` should also work as an alias.

### Command groups

#### Agent Lifecycle (`pp agent`)

```
pp agent spawn [--role <role>] [--task <id>] [--repo <repo>]
pp agent dismiss <name> [--force]
pp agent list [--repo <repo>] [--all-repos] [--status <status>]
pp agent show <name>
pp agent respawn <name>
pp agent attach <name>
pp agent logs <name> [--lines <n>]
pp agent stuck <name>
```

**Agent-facing shortcut:** `pp prime` (not under `agent` group) — outputs role-specific instructions. Agents call this at startup.

#### BBS / Tuplespace (`pp bbs`)

```
pp bbs out <category> <scope> <identity> [payload-json]
pp bbs in <category> [scope] [identity] [--timeout <duration>]
pp bbs rd <category> [scope] [identity] [--timeout <duration>]
pp bbs scan [category] [scope] [identity]
pp bbs pulse [--agent-id <name>]
pp bbs seed-available <scope>
```

These mirror imp-castle's BBS commands exactly. Agents already know these commands — changing them would break muscle memory (and instructions).

#### Workflow (`pp workflow`)

```
pp workflow run <name> [--param key=value ...]
pp workflow list
pp workflow show <name>
pp workflow cancel <id>
pp workflow approve <id>
pp workflow reject <id>
```

#### Daemon (`pp daemon`)

```
pp daemon run [--foreground]
pp daemon stop
pp daemon status
pp daemon restart
```

#### Config (`pp config`)

```
pp config get <key>
pp config set <key> <value>
pp config list
pp config edit
```

#### Repo (`pp repo`)

```
pp repo register <path> [--name <name>]
pp repo unregister <name>
pp repo list
pp repo status [<name>]
```

#### Telemetry (`pp telemetry`)

```
pp telemetry enable
pp telemetry disable
pp telemetry status
pp telemetry export [--format <format>]
```

#### Top-level convenience commands

```
pp prime              # Agent priming (standalone, not under agent)
pp status             # System overview (alias for daemon status + agent list)
pp list               # Alias for agent list (imp-castle compatibility)
pp version            # Version info
pp init               # First-time setup
pp completion <shell> # Shell completion
pp ping               # Health check
```

### Total command count: ~42

| Group | Commands |
|-------|----------|
| agent | 8 |
| bbs | 6 |
| workflow | 6 |
| daemon | 4 |
| config | 4 |
| repo | 4 |
| telemetry | 4 |
| top-level | 6 |
| **Total** | **42** |

---

## 5. Agent-Facing Commands

### Context: What agents see

Agents are AI models running in tmux panes. They receive instructions via `pp prime` and execute CLI commands to interact with the system. The CLI must be optimized for agents as much as humans.

### Environment variables (injected at spawn)

| Variable | Example | Purpose |
|----------|---------|---------|
| `PP_AGENT_NAME` | `Marble` | Agent identity |
| `PP_AGENT_ROLE` | `cub` | Agent role |
| `PP_REPO` | `procyon-park` | Repository name |
| `PP_TASK` | `procyon-park-3dk` | Assigned task ID |
| `PP_WORKTREE` | `/path/to/worktree` | Working directory |
| `PP_BRANCH` | `agent/Marble/procyon-park-3dk` | Git branch |

These follow imp-castle's naming but with `PP_` prefix instead of `PP_`.

### Agent workflow (typical session)

```bash
# 1. Get instructions
pp prime

# 2. Check tuplespace for context
pp bbs scan fact $PP_REPO
pp bbs scan convention $PP_REPO

# 3. Claim work atomically
pp bbs in available $PP_REPO $PP_TASK --timeout 5s

# 4. Signal claim
pp bbs out claim $PP_REPO $PP_TASK '{"agent":"'$PP_AGENT_NAME'","status":"in_progress"}'

# 5. Do work (git, edit files, run tests)
# ... uses git, not pp ...

# 6. Signal completion
pp bbs out event $PP_REPO task_done '{"task":"'$PP_TASK'","agent":"'$PP_AGENT_NAME'"}'

# 7. Check for notifications
pp bbs pulse --agent-id $PP_AGENT_NAME

# 8. Request dismissal
pp bbs out event $PP_REPO dismiss_request '{"agent":"'$PP_AGENT_NAME'"}'
```

### Agent-specific design considerations

1. **Fast startup.** Every `pp bbs out` must complete in <100ms. Agents call this dozens of times. The Go binary + Unix socket path achieves this easily.

2. **JSON payloads as positional args.** Agents construct JSON inline: `pp bbs out fact repo "key" '{"data":"value"}'`. The CLI must accept raw JSON as the last positional argument without requiring escaping.

3. **Wildcard matching.** `pp bbs scan ? procyon-park` uses `?` as a wildcard for any category. This must be handled by the CLI (pass `?` to the daemon, not expanded by the shell). Document that agents should quote or escape `?` if their shell expands it.

4. **Blocking with timeout.** `pp bbs in` blocks until a matching tuple is found. The `--timeout` flag is critical — agents must not hang indefinitely. Default timeout: 30s.

5. **Notification piggybacking.** Every BBS command that includes `--agent-id` returns notifications as a side effect. Notifications are printed to stderr so they don't interfere with stdout parsing.

6. **Exit codes for atomic claiming.** `pp bbs in` exits 0 if a tuple was consumed, exits 1 if timeout (no matching tuple). Agents use exit codes in conditionals:

```bash
if pp bbs in available $PP_REPO $PP_TASK --timeout 5s; then
    # Claimed successfully
    pp bbs out claim $PP_REPO $PP_TASK '{"agent":"'$PP_AGENT_NAME'"}'
else
    # Already claimed by another agent
    pp bbs out obstacle $PP_REPO "task-already-claimed" '{"task":"'$PP_TASK'"}'
fi
```

---

## 6. Argument Parsing

### Positional arguments

Most BBS commands use positional arguments matching the tuple structure:

```
pp bbs out <category> <scope> <identity> [payload]
```

- `category`, `scope`, `identity` are plain strings
- `payload` is optional JSON (validated by the daemon, not the CLI)
- The CLI passes these through to the JSON-RPC request without interpretation

### Flags

Global flags (on root command, inherited by all subcommands):

```
--socket <path>     Unix socket path (default: ~/.procyon-park/daemon.sock)
--output <format>   Output format: table, json, text (default: auto-detect)
--quiet             Suppress non-essential output
--verbose           Enable debug output
--no-color          Disable color output
```

Command-specific flags use `--long-name` style (no short flags except `-o` for output and `-q` for quiet). This avoids ambiguity for agents.

### Flag parsing rules

1. **No automatic abbreviation.** `--stat` does not match `--status`. Agents must use full flag names. This prevents ambiguity when new flags are added.

2. **No short flags except `-o` and `-q`.** Short flags save keystrokes but reduce readability. Agents benefit more from explicit, greppable flag names.

3. **`--` to separate flags from positional args.** Required when JSON payloads start with `-`.

4. **Boolean flags.** `--force` means true. `--no-force` means false. No `=true`/`=false` syntax.

---

## 7. Output Formatting

### TTY detection

```go
if isatty.IsTerminal(os.Stdout.Fd()) {
    defaultFormat = "table"  // Human at terminal
} else {
    defaultFormat = "json"   // Piped or redirected
}
```

When stdout is a terminal, use human-friendly formatting. When piped, use JSON. This serves both humans and scripts without explicit flags.

### Table format (human-friendly)

```
$ pp agent list
NAME       ROLE    STATUS   TASK               UPTIME
Marble     cub     active   procyon-park-3dk   2h 15m
Sprocket   cub     active   procyon-park-709   1h 42m
king       king    active   —                  4h 30m
```

- Fixed-width columns, right-aligned numbers
- Status uses color when terminal supports it (green=active, red=dead, yellow=stopped)
- Timestamps as relative durations ("2h 15m" not "2026-02-23T14:30:00Z")
- No box drawing (simple, parseable by agents even in table mode)

### JSON format (machine-friendly)

```json
{
  "agents": [
    {
      "name": "Marble",
      "role": "cub",
      "status": "active",
      "task": "procyon-park-3dk",
      "uptime_seconds": 8100
    }
  ]
}
```

- Compact JSON (no pretty-printing unless `--output json-pretty`)
- ISO 8601 timestamps
- Numeric durations in seconds
- Consistent field naming (snake_case)

### Text format (minimal)

```
$ pp agent list --output text
Marble cub active procyon-park-3dk
Sprocket cub active procyon-park-709
king king active -
```

Space-separated, one line per record. Easy to `awk` and `cut`.

### Error output

All errors go to stderr. Error format depends on output mode:

**Table/text mode (stderr):**
```
error: agent 'Gizmo' not found
  Available agents: Marble, Sprocket, king
  Try: pp agent list
```

**JSON mode (stderr):**
```json
{"error":{"code":"AGENT_NOT_FOUND","message":"agent 'Gizmo' not found","available":["Marble","Sprocket","king"]}}
```

### Notification output

BBS notifications (from `--agent-id` piggybacking) go to stderr:

```
[notification] New task assigned: procyon-park-abc
[notification] King requests status update
```

This keeps stdout clean for the primary command output.

---

## 8. Exit Codes

| Code | Meaning | Example |
|------|---------|---------|
| 0 | Success | Command completed successfully |
| 1 | General error | Daemon returned an error |
| 2 | Usage error | Invalid arguments, unknown command |
| 3 | Connection error | Cannot reach daemon |
| 4 | Timeout | Blocking operation timed out (e.g., `bbs in` with no match) |
| 5 | Not found | Requested resource doesn't exist |
| 130 | Interrupted | SIGINT (Ctrl+C) |

Agents use exit codes to make decisions:

```bash
pp bbs in available $PP_REPO $PP_TASK --timeout 5s
case $? in
    0) echo "Claimed" ;;
    4) echo "Timeout — task already claimed" ;;
    3) echo "Daemon not running" ;;
    *) echo "Unexpected error" ;;
esac
```

---

## 9. Go Package Structure

### Recommended layout

```
internal/
├── cli/                  # Cobra command definitions
│   ├── root.go           # Root command, global flags, Execute()
│   ├── agent.go          # pp agent *
│   ├── bbs.go            # pp bbs *
│   ├── workflow.go        # pp workflow *
│   ├── daemon.go          # pp daemon *
│   ├── config.go          # pp config *
│   ├── repo.go            # pp repo *
│   ├── telemetry.go       # pp telemetry *
│   ├── prime.go           # pp prime
│   ├── status.go          # pp status
│   ├── version.go         # pp version
│   ├── init_cmd.go        # pp init
│   ├── completion.go      # pp completion
│   └── output.go          # Shared output formatting helpers
├── ipc/                   # JSON-RPC client
│   ├── client.go          # Unix socket connection, request/response
│   └── types.go           # Request/Response structs
├── daemon/                # Daemon process management
│   ├── daemon.go          # Main event loop, VM hosting
│   ├── ipc_server.go      # JSON-RPC server
│   └── startup.go         # PID file, socket, initialization
└── output/                # Output formatters
    ├── table.go           # Table formatter
    ├── json.go            # JSON formatter
    └── text.go            # Plain text formatter
```

### Command file anatomy

Each command file follows a consistent pattern:

```go
// internal/cli/agent.go
package cli

import "github.com/spf13/cobra"

var agentCmd = &cobra.Command{
    Use:   "agent",
    Short: "Manage agents",
}

var agentSpawnCmd = &cobra.Command{
    Use:   "spawn",
    Short: "Spawn a new agent",
    Args:  cobra.NoArgs,
    RunE: func(cmd *cobra.Command, args []string) error {
        role, _ := cmd.Flags().GetString("role")
        task, _ := cmd.Flags().GetString("task")
        repo, _ := cmd.Flags().GetString("repo")

        resp, err := ipcCall("agent.spawn", map[string]interface{}{
            "role": role,
            "task": task,
            "repo": repo,
        })
        if err != nil {
            return err
        }

        return formatOutput(cmd, resp)
    },
}

func init() {
    agentCmd.AddCommand(agentSpawnCmd)
    agentSpawnCmd.Flags().String("role", "cub", "Agent role")
    agentSpawnCmd.Flags().String("task", "", "Task ID to assign")
    agentSpawnCmd.Flags().String("repo", "", "Repository name")
}
```

### The ipcCall helper

```go
// internal/ipc/client.go
func Call(socketPath, method string, params interface{}) (json.RawMessage, error) {
    conn, err := net.Dial("unix", socketPath)
    if err != nil {
        return nil, fmt.Errorf("cannot connect to daemon: %w", err)
    }
    defer conn.Close()

    req := Request{
        JSONRPC: "2.0",
        Method:  method,
        Params:  params,
        ID:      1,
    }

    encoder := json.NewEncoder(conn)
    if err := encoder.Encode(req); err != nil {
        return nil, err
    }

    var resp Response
    decoder := json.NewDecoder(conn)
    if err := decoder.Decode(&resp); err != nil {
        return nil, err
    }

    if resp.Error != nil {
        return nil, resp.Error
    }
    return resp.Result, nil
}
```

This is the entire IPC client. ~30 lines. The CLI stays thin.

---

## 10. Comparison with imp-castle CLI

### What stays the same

| Aspect | imp-castle | procyon-park |
|--------|-----------|--------------|
| Framework | Cobra | Cobra |
| IPC | Unix socket | Unix socket |
| Binary | Single Go binary | Single Go binary |
| BBS commands | `pp bbs out/in/rd/scan` | `pp bbs out/in/rd/scan` |
| Agent spawn | `pp spawn` | `pp agent spawn` |
| Priming | `pp prime` | `pp prime` |

### What changes

| Aspect | imp-castle | procyon-park | Why |
|--------|-----------|--------------|-----|
| Binary name | `cub` (3 chars) | `pp` (2 chars) | Even shorter for agents |
| Command grouping | Flat (25 top-level) | Two-level noun-verb (7 groups) | Better organization at 42 commands |
| IPC protocol | Custom JSON struct | JSON-RPC 2.0 | Standard protocol, error codes, method naming |
| Mail system | `cub mail/send` | Replaced by BBS tuples | Tuplespace subsumes mailboxes |
| Output format | Human-only | Auto-detect + `--output` flag | Agents need JSON output |
| Exit codes | 0 or 1 | Semantic codes (0-5, 130) | Agents need specific error types |
| Daemon auto-start | Manual | Automatic on first command | Better UX |
| Env var prefix | `PP_` | `PP_` | New project identity |

### What's removed

- **Mail commands** (`cub mail`, `cub send`): Replaced by BBS tuplespace. Agents communicate via tuples, not mailboxes. This is a simplification — one coordination mechanism instead of two.

- **Feature branch command** (`pp feature`): Subsumed into `pp agent spawn` with epic detection. Not a separate command group.

- **Migrate command** (`cub migrate`): No legacy data to migrate.

---

## 11. Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| CLI language | Go | Fast startup, single binary, proven pattern |
| CLI framework | Cobra | Scale, ecosystem, imp-castle precedent |
| Binary name | `pp` | Minimal keystrokes for agents |
| IPC protocol | JSON-RPC 2.0 over Unix socket | Simple, debuggable, Maggie-implementable |
| Command structure | Two-level noun-verb | 42 commands need grouping; two levels sufficient |
| Output format | Auto-detect TTY + `--output` flag | Serves humans and agents |
| Exit codes | Semantic (0-5, 130) | Agents need specific error types |
| Daemon auto-start | On first CLI command | Eliminates manual daemon management |
| Mail system | Removed (use BBS) | One coordination mechanism, not two |
| Notifications | Stderr piggybacking | Clean stdout for command output |

---

## 12. Open Questions

1. **Should `pp` auto-install shell completions?** On first run, `pp init` could install completions for the user's shell. Or leave it manual. Auto-install is convenient but modifies dotfiles.

2. **Should the CLI embed a minimal daemon for offline use?** If the daemon is down and the user runs `pp bbs scan`, should the CLI fall back to reading SQLite directly? This adds complexity but improves resilience. Recommendation: no — keep the CLI thin, auto-start the daemon.

3. **Configuration file format.** Phase 9 research uses TOML. The CLI should respect the same config hierarchy: flags > env vars > repo config > global config > defaults.

4. **Should `pp` support plugins?** kubectl and gh have extension mechanisms. For procyon-park, this is premature. The single binary covers all needs. Plugins can be added later if needed.

5. **Tab completion for dynamic values.** Cobra supports custom completion functions. `pp agent dismiss <TAB>` could complete with active agent names by querying the daemon. Worth implementing but not in the first pass.

---

## References

- Phase 0 research: `docs/research/phase-0-project-setup.md` — project structure, Go interop
- Phase 2 research: `docs/research/phase-2-daemon.md` — daemon architecture, IPC protocol decision
- Phase 3 research: `docs/research/phase-3-agent-lifecycle.md` — agent struct, spawn/dismiss flows
- Phase 9 research: `docs/research/phase-9-config-and-registry.md` — config hierarchy
- imp-castle CLI: `internal/cli/` (25 command files, 4,857 lines)
- imp-castle daemon IPC: `internal/daemon/daemon.go` (Unix socket + JSON requests)
- Cobra documentation: https://cobra.dev/
- JSON-RPC 2.0 specification: https://www.jsonrpc.org/specification
