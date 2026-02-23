# Phase 3 Research: Agent Lifecycle & Identity

## Overview

This document captures the agent lifecycle management architecture as implemented in imp-castle. It covers agent identity, spawn/dismiss/respawn flows, tmux session management, git worktree isolation, registry persistence, name generation, environment injection, liveness detection, and crash recovery.

**Source:** imp-castle codebase — `internal/agent/`, `internal/tmux/`, `internal/git/`, `internal/state/`, `internal/impname/`

---

## 1. Agent as First-Class Object

### Core Struct (`internal/agent/agent.go`)

```go
type Agent struct {
    Name         string    // Unique identifier (e.g., "Marble", "reviewer-1")
    Role         string    // One of: king, cub, reviewer, merge-handler
    TmuxSession  string    // tmux session name for execution environment
    Worktree     string    // Path to isolated git worktree
    Branch       string    // Git branch name (pattern: agent/{name}/{taskID})
    Task         string    // Associated beads task ID
    EpicID       string    // Parent epic ID if task is part of epic
    CreatedAt    time.Time // Creation timestamp
    Status       string    // One of: active, stopped, dead
}
```

### Valid Roles

| Role | Purpose | Naming | Scope |
|------|---------|--------|-------|
| `king` | Orchestrator | Singleton, no suffix | Global (one per system) |
| `cub` | Worker | Cutesy name from pool (e.g., "Marble") | Per-repo |
| `reviewer` | Code reviewer | Numeric suffix (e.g., "reviewer-1") | Per-repo |
| `merge-handler` | Merge operations | Numeric suffix | Per-repo |

### Status Transitions

```
active  →  stopped  (graceful shutdown)
active  →  dead     (crash detected — tmux session gone)
stopped →  (pruned) (cleanup after timeout)
dead    →  (respawned or pruned)
```

---

## 2. Spawn Flow

### Entry Points

- `Spawn(ctx, role, taskID, baseBranch)` — spawns in current repo
- `SpawnInRepo(ctx, role, taskID, repoName, repoRoot, baseBranch)` — spawns in specified repo
- `spawnKing()` — spawns global king (no worktree, works from cwd)

### Spawn Sequence

1. **Validate role** against `ValidRoles`
2. **Allocate name:**
   - Cubs: random cutesy name from `ImpNames` pool via `impname.NextName()`
   - Others: numeric suffix via `state.NextAgentNumber()` (e.g., `reviewer-1`)
3. **Epic detection:** `getParentEpic(taskID)` checks if task has a parent epic
   - If epic found: base branch becomes `feature/{epicID}`
4. **Create branch:** `git.GenerateBranchName()` produces `agent/{name}/{taskID}`
   - Appends numeric suffix if branch exists: `-2`, `-3`, etc. (max 99 attempts)
5. **Create worktree:** `git.CreateWorktree()` at `~/.imp-castle/worktrees/{repo}/{name}/`
6. **Create tmux session:** `tmux.CreateSession()` with environment variables injected
7. **Launch agent command:** default `auggie -w {{workspace}} --allow-indexing`
8. **Wait for shell prompt:** polls tmux output for `>`, `$`, `%` characters
9. **Send priming instruction** to the agent
10. **Register** agent in `~/.imp-castle/agents/{repo}.json`

### Epic-Aware Branching

When a task belongs to an epic, the agent branches from `feature/{epicID}` instead of `main`. This allows multiple agents to work on sibling tasks under the same epic, and their branches merge back to the feature branch (not directly to main).

```
main
 └─ feature/epic-42
      ├─ agent/Marble/task-100
      ├─ agent/Sprocket/task-101
      └─ agent/Gizmo/task-102
```

---

## 3. Dismiss Flow

### Entry: `Kill(ctx, repoName, agentName, opts)`

1. **Kill tmux session** — terminates the agent's execution environment
2. **Merge branch** to target (feature branch if epic, else main)
   - Uses `--no-ff` to preserve branch history
   - If merge fails, cleanup is aborted (work preservation)
3. **Release cub name** back to pool (cub role only)
4. **Remove worktree** via `git worktree remove --force`
5. **Delete branch** — only after successful merge
6. **Delete mailbox** at `~/.imp-castle/mail/{repo}/{name}.json`
7. **Unregister** from agent registry

### Critical Design Decision

Branch merging happens **before** resource cleanup. If the merge fails, the entire dismiss is aborted so work is never lost.

---

## 4. Respawn Flow (Crash Recovery)

### Entry: `RespawnAgent(ctx, repoName, agentName, opts)`

**Precondition:** Agent must be `stopped` or `dead` (not `active`).

### Respawn Sequence

1. **Validate** agent is not currently active
2. **Preserve work** (`preserveWork()`):
   - Check if worktree exists
   - If dirty: commit with message `"{taskID}: WIP - Agent respawn recovery"`
   - Push branch to origin
3. **Cleanup for respawn** (`cleanupForRespawn()`):
   - Kill tmux session if exists
   - Release cub name
   - Remove worktree
   - **Intentionally preserve branch** (needed for new agent)
   - Delete mailbox
   - Unregister from registry
4. **Spawn new agent** branched from the preserved branch
5. **Send continuation message** via mailbox to new agent

### Key Insight

The branch is explicitly preserved during respawn cleanup, unlike dismiss where it's deleted. This is the mechanism that allows crash recovery without work loss.

---

## 5. Tmux Session Management (`internal/tmux/`)

### Session Lifecycle

| Function | Purpose |
|----------|---------|
| `CreateSession(name, workdir, env)` | Creates detached session with env vars |
| `SessionExists(name)` | Checks if session is alive (liveness probe) |
| `KillSession(name)` | Terminates session |
| `ListSessions()` | Lists all active sessions |

### Command Execution

| Function | Purpose |
|----------|---------|
| `SendKeys(session, keys)` | Sends text + Enter (500ms delay) |
| `SendKeysLiteral(session, keys)` | Sends text without Enter |
| `SendControlKeys(session, ...keys)` | Sends control sequences (C-a, C-k, etc.) |
| `RunCommand(session, command)` | Alias for SendKeys |
| `PreserveInput(session)` | Saves current line for later restoration |
| `CapturePane(session, lines)` | Captures scrollback (used for prompt detection) |

### Environment Variable Injection

Variables are set at session creation time via tmux `-e` flags, not inherited from the parent process. This ensures agents have isolated environments.

### Session Naming Convention

- Agents: `cub-{repoName}-{agentName}`
- King: `cub-king`

---

## 6. Git Worktree Management (`internal/git/`)

### Protected Branches

```go
ProtectedBranches = ["main", "master", "develop", "development", "init", "HEAD"]
```

Defense-in-depth: `IsProtectedBranch()` performs case-insensitive checks. Agents never checkout protected branches directly.

### Worktree Operations

| Function | Purpose |
|----------|---------|
| `CreateWorktree(ctx, repoPath, worktreePath, branchName, baseBranch)` | Creates isolated worktree with new branch |
| `RemoveWorktree(ctx, repoPath, worktreePath)` | Force-removes worktree, falls back to manual delete |
| `IsValidWorktree(dirPath)` | Validates directory has `.git` file and responds to git commands |
| `PruneWorktrees(ctx, repoPath)` | Runs `git worktree prune` |

### Branch Operations

| Function | Purpose |
|----------|---------|
| `GenerateBranchName(ctx, repoPath, agentName, taskID)` | Produces `agent/{name}/{taskID}` with dedup suffix |
| `MergeBranch(ctx, repoPath, source, target)` | Merges with `--no-ff`, preserves/restores current branch |
| `PushBranch(ctx, worktreePath, branchName)` | Pushes with `-u origin` |
| `HasUncommittedChanges(ctx, worktreePath)` | Dirty check |
| `CommitAll(ctx, worktreePath, message)` | Stage-all and commit |

### Orphan Detection

`ListOrphanedWorktrees(baseDir)` finds directories under `~/.imp-castle/worktrees/` that are no longer valid git worktrees (missing `.git` file or git commands fail).

---

## 7. Agent Registry & Persistence (`internal/state/`)

### Registry Structure

```
~/.imp-castle/
  ├── agents/{repoName}.json          # Agent registry per repo
  ├── agents/{repoName}-cubnames.json  # Cub name allocation per repo
  ├── mail/{repoName}/{agentName}.json # Mailboxes
  └── king.json                        # Global king registry
```

### Concurrency Control

All state files use `unix.Flock()` for exclusive file locking. The `withFileLock(path, fn)` helper acquires the lock, runs the function, and automatically releases.

### Registry Operations

| Function | Purpose |
|----------|---------|
| `RegisterAgent(repoName, agent)` | Adds/updates agent in registry |
| `UnregisterAgent(repoName, agentName)` | Removes agent from registry |
| `GetAgent(repoName, agentName)` | Retrieves single agent |
| `ListAgents(repoName)` | Returns all agents for repo |
| `UpdateAgentStatus(repoName, agentName, status)` | Updates status field |
| `NextAgentNumber(repoName, role)` | Returns next numeric suffix for role |

### King Registry

The king is global (not per-repo). Stored at `~/.imp-castle/king.json`. Only one king can exist at a time.

### Mailbox System

Messages between agents use JSON files at `~/.imp-castle/mail/{repo}/{agent}.json`.

```go
type Message struct {
    ID        string    // Random 16-char hex
    From      string
    To        string
    Body      string
    Timestamp time.Time
    Read      bool
}
```

Key operations: `SendMail`, `ReadMail`, `MarkRead`, `DeleteMailbox`, `ClearMailbox`. The king can aggregate mail across repos with `ReadMailAllRepos`.

---

## 8. Name Generation (`internal/impname/`)

### Cub Name Pool

50 cutesy names for cub agents:

```
Sprocket, Gizmo, Nibbles, Fizz, Bumble, Twitch, Pip, Jinx, Spark, Rascal,
Widget, Cog, Whimsy, Flicker, Doodle, Tinker, Pebble, Ziggy, Noodle, Scuttle,
Quirk, Giggles, Wren, Skitter, Blip, Moxie, Pickle, Zephyr, Crumble, Fern,
Wisp, Bramble, Cricket, Marble, Rustle, Tadpole, Acorn, Biscuit, Clover,
Dewdrop, Echo, Fable, Glimmer, Hazel, Ivy, Juniper, Kudzu, Lark, Moss, Nettle
```

### Allocation

- `NextName(repoName, agentID)` — randomly selects from available names, marks as in-use
- `ReleaseName(repoName, name)` — returns name to pool
- Registry stored at `~/.imp-castle/agents/{repoName}-cubnames.json`
- Fallback to numeric naming (`cub-1`, `cub-2`) if pool exhausted
- File-locked for concurrent access safety

---

## 9. Environment Variable Injection

### Injected at Spawn Time (via tmux `-e` flags)

| Variable | Value | Purpose |
|----------|-------|---------|
| `PP_AGENT_ROLE` | `king`, `cub`, `reviewer`, `merge-handler` | Agent's role |
| `PP_AGENT_NAME` | `Marble`, `reviewer-1`, etc. | Agent's identity |
| `PP_REPO` | Repository name | Which repo the agent operates on |
| `PP_TASK` | Beads task ID or empty | Assigned task |

### System Variables

| Variable | Purpose |
|----------|---------|
| `PP_STARTUP_DELAY` | Duration to wait for shell prompt (default: 2s) |
| `AR_DEBUG` | Enables debug logging in git and state modules |

---

## 10. Liveness Detection

### Mechanism

`getActualStatus(agent)` performs a tmux session existence check:

1. If stored status is `active` and `tmux.SessionExists()` returns false → actual status is `dead`
2. Otherwise returns stored status

This is used by:
- `Prune()` — identifies crashed agents for cleanup
- Status queries — shows accurate agent state

### Branch Preservation for Dead Agents

Dead agent branches are preserved for `DefaultBranchMinAge` (24 hours) to allow time for respawn before cleanup. This is configurable via the `--branch-age` flag on prune.

---

## 11. Cleanup & Maintenance

### Prune Flow (`Prune()`)

1. Run `git worktree prune` for all repos
2. For current repo: find agents with status `stopped` or `dead`
3. For `active` agents: verify tmux session (detect crashes)
4. Remove worktrees
5. Delete branches (only if older than 24h)
6. Optionally delete mailboxes
7. Unregister agents
8. Find and remove orphaned worktrees

### Orphan Cleanup

| Function | Purpose |
|----------|---------|
| `CleanOrphanedWorktrees()` | Removes worktree directories without valid git repos |
| `CleanOrphanedMailboxes()` | Removes mailboxes for unregistered agents |
| `PruneWorktreesAllRepos()` | Runs `git worktree prune` globally |

---

## 12. Configuration & Directory Layout

### imp-castle Home Directory

```
~/.imp-castle/
  ├── agents/              # Agent registries (per repo)
  │   ├── {repo}.json      # Agent registry
  │   └── {repo}-cubnames.json  # Cub name allocation
  ├── mail/                # Mailboxes (per repo/agent)
  │   └── {repo}/
  │       └── {agent}.json
  ├── worktrees/           # Git worktrees (per repo/agent)
  │   └── {repo}/
  │       └── {agent}/
  ├── daemon/              # Daemon state files
  ├── workflows/           # Global workflow definitions
  ├── instructions/        # Role-specific instructions
  ├── king.json            # Global king registry
  ├── daemon.pid           # Daemon process ID
  └── daemon.sock          # Daemon Unix socket
```

### Agent Command Configuration

`~/.imp-castle.toml` defines the agent command:
- Default: `auggie -w {{workspace}} --allow-indexing`
- `{{workspace}}` is replaced with the agent's worktree path at spawn time

### Path Conventions

| Pattern | Example |
|---------|---------|
| tmux session (agent) | `cub-procyon-park-Marble` |
| tmux session (king) | `cub-king` |
| worktree path | `~/.imp-castle/worktrees/procyon-park/Marble/` |
| branch name | `agent/Marble/procyon-park-709` |
| feature branch | `feature/epic-42` |

---

## 13. Design Principles

1. **Isolation via worktrees** — Each agent gets a separate git worktree, preventing interference between agents working in parallel.

2. **Work preservation over cleanup** — Merging happens before resource deletion. If merge fails, dismiss aborts entirely.

3. **Defense-in-depth for protected branches** — Case-insensitive checks prevent agents from ever checking out `main`, `master`, etc.

4. **File locking for concurrency** — Unix `flock()` on all state files prevents corruption from simultaneous access by multiple agents.

5. **Epic-aware branching** — Tasks under an epic share a feature branch, reducing branch sprawl and simplifying integration.

6. **Graceful degradation** — Name pool exhaustion falls back to numeric naming. Missing tmux sessions are detected as crashes rather than causing errors. Prune continues past individual failures.

7. **24-hour branch retention** — Dead agent branches are preserved for a day, giving operators time to inspect or respawn before cleanup.

---

## 14. Maggie Design Implications

For Procyon Park (Maggie), the agent lifecycle system provides these key patterns to replicate or adapt:

- **Agent identity** is a struct with name, role, status, and resource references (session, worktree, branch)
- **Spawn** is a multi-step orchestration: name allocation → branch creation → worktree creation → session creation → command launch → registration
- **Dismiss** reverses spawn but merges work first: session kill → merge → name release → worktree removal → branch deletion → mailbox deletion → unregistration
- **Respawn** preserves work (commit + push), cleans resources (except branch), then spawns fresh from the preserved branch
- **Liveness** is detected by checking if the execution environment (tmux session) still exists
- **Concurrency** is handled by file locking on all shared state
- **Communication** between agents uses a mailbox system with file-backed JSON messages
