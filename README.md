# Procyon Park

Agent orchestration system built in [Maggie](https://github.com/chazu/maggie). Coordinates swarms of AI coding agents across repositories using a linear tuplespace with Petri net workflow execution.

## Architecture

- **BBS** — Tuplespace (Bulletin Board System) with CUE-based matching, three tuple modalities (linear, persistent, affine), and durable persistence
- **Workflow Engine** — Petri net execution: templates define places, transitions, and token flow. Supports automatic transitions, role-based agent spawning, and built-in actions
- **Scheduler** — Matches pending tasks to available harness slots, spawns Claude Code agents
- **Rule Engine** — Reactive pattern matching across the global tuplespace
- **API Server** — HTTP/JSON interface for all BBS operations
- **CLI (`pp`)** — Agent-facing command-line tool for tuplespace communication

## Quick Start

```bash
# Build
mag build -o pp && codesign -s - pp

# Start the server (auto-creates a 'local' identity on first run)
./pp serve
```

That's it for solo use. In another terminal:

```bash
./pp repo add /path/to/repo --name my-repo
./pp workflow story --param description="Add error handling to login" --repo my-repo
./pp workflow status
./pp log
./pp dashboard
```

### Inviting teammates (multiplayer)

Every mutating request is Ed25519-signed by the caller's identity. To onboard a teammate without copy-pasting hex pubkeys:

```bash
# Admin (you)
./pp identity invite alice --ttl 600
# Share the printed command with Alice over chat/Signal/etc.

# Alice (on her machine)
pp identity accept http://<your-host>:7777 --name alice --token <token>
pp whoami
pp observe alice "I'm on the system"
```

### Managing identities

```bash
pp whoami                       # Show the active identity
pp identity list                # List local keypairs
pp identity use <name>          # Switch the active identity
pp -i <name> <command>          # One-shot per-command override
PP_IDENTITY=<name> <command>    # Env override
pp identity rotate <name>       # Rotate a keypair (signed by old key)
```

### Advanced: custom admin name

If you want an admin identity named something other than 'local', run `pp init <name>` BEFORE the first `pp serve`:

```bash
pp init chazu      # Creates ~/.config/pp/identity/chazu.{key,pub} + server.toml
pp serve           # Starts server; 'chazu' is the pre-enrolled admin
```

### Re-initialising from scratch

```bash
rm -rf ~/.config/pp ~/.pp    # CAUTION: deletes all identities + BBS state
./pp serve                   # Auto-bootstraps fresh 'local' identity
```

## Workflow Templates

| Template | Use | Flow |
|----------|-----|------|
| `story` | Single task with review cycle | setup &rarr; implement &rarr; review &rarr; [pass/fix] &rarr; merge &rarr; notify |
| `story-lite` | Mechanical changes, no review | setup &rarr; implement &rarr; merge &rarr; notify |
| `full-pipeline` | Full epic execution | plan &rarr; dispatch-waves &rarr; review+test &rarr; evaluate &rarr; merge |
| `scout-mission` | Research task | request &rarr; research &rarr; output |
| `feature-design` | Epic decomposition | ideate &rarr; epic &rarr; stories &rarr; refine |
| `multi-scout` | Parallel research | spawn scout-mission children |
| `workitem-plan` | Agentic planning | research &rarr; decompose into child stories |
| `workitem-review` | Agentic review | review and refine work item tree |

See [docs/authoring-workflows.md](docs/authoring-workflows.md) for how to write custom templates.

## Agent Roles

| Role | Purpose |
|------|---------|
| **Scout** | Research topics, write findings (no code changes) |
| **Planner** | Analyze tasks, decompose into parallelizable stories |
| **Implementer** | Write code for a scoped subtask |
| **Reviewer** | Independent code review, write verdict signals |
| **Tester** | Write and run tests from spec |
| **Fixer** | Address review/test findings |
| **Foreman** | Evaluate review+test results, write verdict |

## CLI Commands

```
pp init [<name>]                      First-time setup (create identity + config)
pp whoami                             Show the active local identity

pp serve                              Start the server (auto-bootstraps on first run)
pp workflow <template> [--param K=V]  Start a workflow
pp workflow status                    List running workflows
pp workflow cancel <id>               Cancel a workflow
pp workflow wait <id>                 Block until workflow completes

pp workitem create/show/list/update/comment/run/plan/review
pp observe <identity> <detail>        Write an observation
pp decide <identity> <detail>         Record a decision
pp signal <id> <key> <value>          Write a signal
pp read <category> [scope] [id]       Read from the BBS
pp notify <message>                   Send a notification
pp dismiss                            Signal task completion

pp repo add <path> --name <name>      Register a repository
pp status                             System status
pp log                                Stream notifications
pp history                            Query audit log
pp dashboard                          TUI dashboard

pp identity init <name>               Create a local keypair
pp identity list                      List local identities
pp identity show <name>               Show identity details
pp identity use <name>                Switch the active identity
pp identity rotate <name>             Rotate a keypair
pp identity invite <name> [--ttl N]   Create an invite token (admin)
pp identity accept <url> --name <n> --token <t>   Claim an invite
pp user add <name> --pubkey <hex>     Manually register a user (admin)
pp user revoke <name>                 Revoke a user (admin)

pp -i <name> <command>                One-shot identity override
PP_IDENTITY=<name> <command>          Env identity override
```

## `pp bbs` — direct tuplespace access

`pp bbs` is the operator-facing window into the BBS. Four subcommands:

```
pp bbs list [--category C] [--scope S] [--identity I] [--json]
       List tuples; with no --category, scans every category.
pp bbs get  <category> <scope> <identity>
       Fetch a single tuple as JSON. Exits 1 if not found.
pp bbs put  <category> <scope> <identity> <payload>
            [--pinned] [--ttl SEC] [--modality persistent|linear|affine]
       Upsert a tuple. <payload> is inline JSON or @path/to/file.json.
pp bbs rm   <category> <scope> <identity>
       Remove a tuple. Idempotent (exits 0 on no-such-tuple).
```

Payload sources for `put`: either an inline JSON literal, or `@path/to/file.json`
(curl-style — the file contents are read and sent verbatim).

Modality defaults to `persistent` for pinned categories (`fact`, `convention`,
`template`, `rule`, `ingestion`, `artifact`, `link`, `decision`, `identity`) and
`linear` otherwise. `--pinned` forces `persistent`; `--ttl SEC` forces `affine`
with the given TTL; `--modality M` sets it explicitly.

### Guarantees

- **Server-side category validation.** The CLI cannot construct an invalid
  tuple — the category enum is enforced by the server. Bad `--category`
  values fail fast with the valid set listed, and the process exits
  non-zero.
- **Durable on return.** `put` and `rm` synchronously flush BBS state to
  disk before the HTTP response is sent. A SIGKILL immediately after the
  ack does not lose the mutation.
- **`put` is UPSERT on `(category, scope, identity)`.** Any existing
  tuple with the same triple is consumed before the new one is written;
  the response reports `<id> created` or `<id> updated` accordingly.
  Tuple ids are server-generated — any client-supplied id is ignored.
- **`rm` is idempotent.** Removing a non-existent tuple prints
  `no such tuple` and exits 0.

### Worked examples

```bash
# Insert a fact (persistent by default because 'fact' is a pinned category).
pp bbs put fact procyon-park:architecture bbs-durability \
  '{"detail":"bbs writes are synchronously flushed on CLI put/rm"}'

# List every task tuple across the whole tuplespace.
pp bbs list --category task

# Inspect a specific decision.
pp bbs get decision global plan:command-palette-stories

# Remove a stale signal tuple (safe to re-run — idempotent).
pp bbs rm signal my-repo:feature/x worktree
```

## Project Structure

```
src/
  bbs/           BBS tuplespace, Tuple builder, Categories
  api/           HTTP API server
  cli/           CLI commands, work item CLI, dashboard
  dispatcher/    Dispatcher loop, WorkflowEngine, RuleEngine, Scheduler
    actions/     Extracted workflow actions (create-worktree, merge-worktree, etc.)
  harness/       Claude Code harness (agent spawning)
  roles/         Agent role definitions (declarative configuration)
  collections/   GrowableArray
workflows/       CUE workflow template definitions
docs/            Composition guide, authoring guide
test/            Test suite (116 tests)
```

## Documentation

- [Workflow Composition](docs/workflow-composition.md) — How the Petri net execution model works
- [Authoring Workflows](docs/authoring-workflows.md) — How to write CUE workflow templates
