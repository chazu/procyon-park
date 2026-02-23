# Phase 9 Research: Configuration, Repository Registry & Work Tracking

**Issue:** procyon-park-cia
**Author:** Fable
**Date:** 2026-02-23
**Status:** Complete

## Overview

This document covers the supporting systems that tie the imp-castle architecture together: TOML configuration with layered precedence, the repository registry, the work tracker abstraction, and onboarding/setup validation. These are not glamorous subsystems but they are load-bearing — every command, every agent spawn, every daemon poll touches configuration or registry state.

---

## 1. TOML Configuration

### Current Implementation (imp-castle)

**File:** `internal/config/config.go`

The existing system is minimal — almost aggressively so. A single `UserConfig` struct with one field:

```go
type UserConfig struct {
    AgentCommand string `toml:"agent_command"`
}
```

**Config file:** `~/.imp-castle.toml`
**Parsing:** `BurntSushi/toml`
**Loading:** `sync.Once` singleton — loaded once, cached for process lifetime.

The config package also serves as the canonical source for all filesystem paths (`BaseDir`, `AgentsDir`, `MailDir`, `WorktreesDir`, etc.), making it the de facto layout authority.

### Procyon Park Design

#### 1.1 Two-Layer Config: Global + Per-Repo

```
~/.procyon-park/config.toml        # Global config
<repo>/.procyon-park/config.toml   # Per-repo overrides
```

**Global config** sets defaults for the entire installation:
```toml
[agent]
command = "claude -w {{workspace}}"
max_concurrent = 4

[daemon]
poll_interval = "10s"
http_port = 8765

[telemetry]
enabled = true
endpoint = "localhost:4317"
```

**Per-repo config** overrides specific values for a repository:
```toml
[agent]
command = "claude -w {{workspace}} --model sonnet"
max_concurrent = 2
```

#### 1.2 Merge Strategy

Per-repo values override global values at the leaf level (not deep merge). If a per-repo config sets `[agent].command`, only that field is overridden — other `[agent]` fields still come from global. This is simple and predictable.

```go
type Config struct {
    Agent     AgentConfig     `toml:"agent"`
    Daemon    DaemonConfig    `toml:"daemon"`
    Telemetry TelemetryConfig `toml:"telemetry"`
    Hub       HubConfig       `toml:"hub"`
}

func LoadConfig(repoPath string) (*Config, error) {
    cfg := defaultConfig()
    // Load global first
    loadTOML(globalConfigPath(), cfg)
    // Overlay per-repo
    if repoPath != "" {
        loadTOML(filepath.Join(repoPath, ".procyon-park", "config.toml"), cfg)
    }
    // Apply env overrides
    applyEnvOverrides(cfg)
    return cfg, nil
}
```

#### 1.3 Recommendations

- **Keep `BurntSushi/toml`** — battle-tested, zero-dependency TOML parser for Go.
- **Singleton with repo context:** The current `sync.Once` pattern works for global config but needs a per-repo cache keyed by repo path: `map[string]*Config`.
- **No hot-reloading:** Config is read at startup. Agents are short-lived. The daemon can re-read on SIGHUP if needed, but don't add file-watching complexity.
- **Validation at load time:** Return errors for unknown keys or invalid values rather than silently falling through to defaults.

---

## 2. Feature Flags with Precedence

### Current Implementation

imp-castle has no feature flag system. The `internal/cli/feature.go` is about feature *branches* (epic management), not feature flags.

### Procyon Park Design

#### 2.1 Precedence Order (highest to lowest)

```
Environment variable  >  Per-repo config  >  Global config  >  Compiled default
```

This is the standard 12-factor precedence. Environment variables always win, allowing operators to override anything at runtime without touching files.

#### 2.2 Implementation

Feature flags are simple boolean or string values in the config, not a separate system:

```toml
# In config.toml
[features]
bbs_enabled = true
workflows_enabled = false
telemetry_otel = true
hub_discovery = false
```

Environment override convention:
```
PROCYON_FEATURES_BBS_ENABLED=true
PROCYON_FEATURES_WORKFLOWS_ENABLED=false
```

```go
type FeaturesConfig struct {
    BBSEnabled       bool `toml:"bbs_enabled"`
    WorkflowsEnabled bool `toml:"workflows_enabled"`
    TelemetryOTEL    bool `toml:"telemetry_otel"`
    HubDiscovery     bool `toml:"hub_discovery"`
}

// Feature checks are simple method calls, no indirection:
func (c *Config) BBSEnabled() bool {
    return c.Features.BBSEnabled
}
```

#### 2.3 Why Not a Feature Flag Service?

imp-castle is a local tool, not a SaaS product. Feature flags here gate incomplete subsystems during development, not A/B tests. A simple config key with env override is the right level of complexity. If/when multi-node hub needs coordinated rollouts, that's a hub-level concern — not a config-level one.

#### 2.4 Recommendations

- **Flat booleans in config** — no nesting, no typed variants, no percentage rollouts.
- **Environment override via simple naming convention:** `PROCYON_` prefix + TOML path with `_` separators, uppercased.
- **Compile-time defaults in Go code** — the `defaultConfig()` function is the source of truth for what's on by default.
- **No feature flag registry or metadata** — just read the config. The code that checks a flag knows what it means.

---

## 3. Repository Registry

### Current Implementation (imp-castle)

**Files:** `internal/repo/registry.go`, `internal/repo/resolve.go`

The registry is a JSON file at `~/.imp-castle/repos.json` mapping absolute paths to repo metadata:

```go
type Repo struct {
    Name       string    `json:"name"`
    Path       string    `json:"path"`
    AddedAt    time.Time `json:"added_at"`
    HasBeads   bool      `json:"has_beads"`
    MainBranch string    `json:"main_branch,omitempty"`
}

type RepoRegistry struct {
    Repos map[string]*Repo `json:"repos"` // keyed by absolute path
}
```

**Key behaviors:**
- Path resolution includes symlink eval and worktree-to-main-repo resolution via `git rev-parse --git-common-dir`.
- Name collision handling: appends `@parent-dir` (e.g., `myapp@work` vs `myapp@personal`).
- Main branch detection: `git symbolic-ref refs/remotes/origin/HEAD`, defaulting to `main`.
- Repo resolution: CLI commands accept `--repo <name-or-path>`, falling back to current directory.

### Procyon Park Design

#### 3.1 Keep the JSON Registry

The current design is sound. A single JSON file is simple, human-readable, and sufficient for the expected scale (tens of repos, not thousands).

```
~/.procyon-park/repos.json
```

#### 3.2 Extended Repo Metadata

```go
type Repo struct {
    Name       string    `json:"name"`
    Path       string    `json:"path"`
    AddedAt    time.Time `json:"added_at"`
    HasBeads   bool      `json:"has_beads"`
    MainBranch string    `json:"main_branch"`
    // New fields for Procyon Park
    BBSScope   string    `json:"bbs_scope,omitempty"`   // Tuplespace scope name (defaults to Name)
    HubNodeID  string    `json:"hub_node_id,omitempty"` // For future multi-node
}
```

#### 3.3 Resolution Logic

Keep the existing three-step resolution:
1. If `--repo` flag is provided → look up by name, then by path.
2. If no flag → detect from current working directory via git.
3. Error if repo not registered (prompting `pp repo add`).

#### 3.4 Staleness Detection

Add a staleness check during `repo list`:
- Does the path still exist?
- Is it still a git repo?
- Has the main branch changed?

This is advisory, not blocking — stale repos get a warning marker, not auto-removal.

#### 3.5 Recommendations

- **Keep JSON, not TOML** for the registry — it's structured data, not user-authored config.
- **File locking** on writes (already done via `withFileLock` in the state package). Reuse the same pattern.
- **Atomic writes** — write to temp file, then rename. Prevents corruption on crash.
- **No database** — the registry is tiny and rarely changes. A JSON file is the right tool.

---

## 4. Work Tracker Interface & Beads Integration

### Current Implementation (imp-castle)

**Files:** `internal/worktracker/tracker.go`, `internal/worktracker/default.go`, `internal/worktracker/beads/beads.go`

The work tracker is a clean interface:

```go
type WorkTracker interface {
    GetTask(id string) (*Task, error)
    CreateTask(title, description, taskType string) (string, error)
    CloseTask(id string) error
}
```

The beads implementation shells out to the `bd` CLI:
```go
func (t *Tracker) GetTask(id string) (*worktracker.Task, error) {
    cmd := exec.Command("bd", "show", id, "--json")
    // ...parse JSON output...
}
```

A global default tracker is set via `sync.Once`:
```go
var defaultTracker WorkTracker
func SetDefault(t WorkTracker) { defaultTracker = t }
func Default() WorkTracker     { return defaultTracker }
```

### Procyon Park Design

#### 4.1 Expanded Interface

The current interface is too narrow. The daemon and king need more operations:

```go
type WorkTracker interface {
    // Core CRUD
    GetTask(id string) (*Task, error)
    CreateTask(opts CreateTaskOpts) (string, error)
    CloseTask(id string) error
    UpdateTask(id string, opts UpdateTaskOpts) error

    // Queries
    ListReady() ([]Task, error)      // Unblocked tasks
    ListByStatus(status string) ([]Task, error)
    ListByParent(epicID string) ([]Task, error)

    // Dependencies
    AddDependency(taskID, dependsOnID string) error
}

type CreateTaskOpts struct {
    Title       string
    Description string
    TaskType    string // task, bug, feature, epic
    Priority    int    // 0-4
    Labels      []string
    Parent      string // epic ID
}

type UpdateTaskOpts struct {
    Status      *string
    Assignee    *string
    Notes       *string
    Title       *string
    Description *string
}
```

#### 4.2 Beads Integration

Keep shelling out to `bd`. This is the right call because:
- `bd` handles its own storage, locking, and sync.
- The `bd` CLI is the source of truth — embedding beads logic would create two sources of truth.
- The overhead of `exec.Command` is negligible for the frequency of work tracker operations.

However, add `--json` output parsing for all commands (not just `show`):
```go
func (t *Tracker) ListReady() ([]worktracker.Task, error) {
    cmd := exec.Command("bd", "ready", "--json")
    // ...
}
```

#### 4.3 Tracker Selection

The default tracker is set during daemon/CLI startup based on repo capabilities:

```go
func initWorkTracker(repoPath string) worktracker.WorkTracker {
    if hasBeadsDir(repoPath) {
        return beads.New()
    }
    return noop.New() // No-op tracker that returns errors
}
```

#### 4.4 Recommendations

- **Do not embed beads logic** — always shell out to `bd`.
- **Expand the interface** to cover the operations the king and workflow engine actually need.
- **Use pointer fields in update opts** to distinguish "not set" from "set to zero value".
- **Consider a `noop` tracker** for repos without beads — it returns clear errors rather than panicking.
- **Test with a mock tracker** — the interface makes this trivial.

---

## 5. Onboarding & Setup Validation

### Current Implementation (imp-castle)

**File:** `internal/cli/init.go`

The `pp init` command:
1. Creates directory structure at `~/.imp-castle/`.
2. Creates CUE workflow module infrastructure.
3. Generates Ed25519 node identity (for future hub).
4. Prints next steps.

Repo onboarding is `pp repo add <path>`, which:
1. Resolves the path to the main git repo.
2. Detects beads presence.
3. Detects main branch.
4. Registers in `repos.json`.

### Procyon Park Design

#### 5.1 `pp init` (System Setup)

```
pp init
```

Actions:
1. Create `~/.procyon-park/` directory structure.
2. Write default `config.toml` with commented examples.
3. Generate node identity (Ed25519 keypair → UUID v5).
4. Initialize BBS tuplespace storage.
5. Print next steps.

Idempotent — safe to re-run. Never overwrites existing config or identity.

#### 5.2 `pp repo add` (Repository Onboarding)

```
pp repo add .
```

Actions:
1. Resolve path → absolute → symlink-eval → main-repo-root.
2. Validate: is it a git repo?
3. Detect: has `.beads/` dir? What's the main branch?
4. Register in `repos.json`.
5. Create `<repo>/.procyon-park/` directory if it doesn't exist.
6. Optionally initialize beads if not present.

#### 5.3 `cub doctor` (Validation)

A diagnostic command that checks system health:

```
cub doctor
```

Checks:
- [ ] `~/.procyon-park/` exists with correct structure
- [ ] `config.toml` parses without errors
- [ ] Node identity exists and is valid
- [ ] BBS tuplespace is accessible
- [ ] Git is available and at a sufficient version
- [ ] `bd` CLI is available (if beads repos exist)
- [ ] tmux is available
- [ ] Registered repos still exist and are git repos
- [ ] Daemon socket is reachable (if daemon is expected to be running)
- [ ] No orphaned worktrees
- [ ] No stale agent registrations

Output format: green checkmarks / red X marks with actionable messages.

#### 5.4 Recommendations

- **Idempotent setup** — users should be able to run `pp init` at any time without fear.
- **`cub doctor` is essential** — multi-agent systems have many moving parts. A single diagnostic command saves hours of debugging.
- **No interactive prompts in `pp init`** — just do the right thing with defaults. Interactive setup wizards are a bad UX for CLI tools.
- **Minimal prerequisites:** Git, tmux, a shell. Everything else is optional.

---

## 6. Hub System (Future Multi-Node)

### Current Implementation (imp-castle)

**File:** `internal/identity/identity.go`

The identity system generates an Ed25519 keypair and derives a UUID v5 node ID. This is the seed for multi-node hub support, though no hub protocol exists yet.

The identity is stored at:
```
~/.imp-castle/identity/node.json   # Public identity (ID, public key, hostname)
~/.imp-castle/identity/node.key    # Private key (Ed25519)
```

### Procyon Park Design

#### 6.1 Hub Architecture (Research-Level)

The hub would allow multiple procyon-park nodes to:
- Discover each other on a local network or via explicit peering.
- Share tuplespace tuples across nodes (BBS federation).
- Distribute work across machines.
- Maintain a consistent view of repo state.

#### 6.2 Design Principles

1. **Local-first:** Everything works without a hub. The hub is additive, not required.
2. **Pull-based federation:** Nodes pull tuples they're interested in, rather than broadcasting everything.
3. **Cryptographic identity:** Nodes sign tuples with their Ed25519 key. Other nodes verify signatures before accepting remote tuples.
4. **Scope-based filtering:** Nodes only sync tuples for repos they both have registered.

#### 6.3 Config Surface

```toml
[hub]
enabled = false
listen_addr = "0.0.0.0:9876"
peers = ["192.168.1.10:9876", "192.168.1.11:9876"]
auto_discover = false  # mDNS/DNS-SD discovery
```

#### 6.4 Recommendations

- **Gate behind a feature flag** (`hub_discovery = false` by default).
- **Do not build hub until BBS, daemon, and workflows are stable** — it's a layer-4 concern.
- **Keep the identity system** from day one — it's cheap and prevents a painful migration later.
- **Protocol:** gRPC or simple HTTP/JSON. Don't invent a wire protocol. gRPC gives you streaming and code generation; HTTP/JSON gives you debuggability. Either works.
- **No consensus protocol** — the tuplespace model is eventually consistent by design. No need for Raft/Paxos.

---

## 7. Directory Layout Summary

```
~/.procyon-park/
├── config.toml              # Global configuration
├── repos.json               # Repository registry
├── daemon.pid               # Daemon PID file
├── daemon.sock              # Daemon Unix socket
├── identity/
│   ├── node.json            # Public node identity
│   └── node.key             # Ed25519 private key
├── agents/
│   └── <repo>.json          # Per-repo agent registries
├── worktrees/
│   └── <repo>/
│       └── <agent>/         # Git worktrees
├── instructions/
│   ├── king.md              # King role instructions
│   └── cub.md               # Cub role instructions
├── workflows/
│   └── *.cue                # CUE workflow definitions
├── daemon/
│   └── notification-state.json
└── bbs/
    └── <storage files>      # BBS tuplespace data

<repo>/
└── .procyon-park/
    └── config.toml           # Per-repo config overrides
```

---

## 8. Critical Design Decisions

| Decision | Recommendation | Rationale |
|----------|---------------|-----------|
| Config format | TOML | Human-authored config; TOML is Go-friendly with `BurntSushi/toml` |
| Registry format | JSON | Machine-managed structured data; JSON is simpler to read/write programmatically |
| Feature flags | Config booleans + env override | Local tool, not SaaS; simplest thing that works |
| Work tracker | Shell out to `bd` | Single source of truth; avoids dual-storage bugs |
| Config precedence | env > repo > global > default | Standard 12-factor; env always wins |
| Hub protocol | gRPC or HTTP/JSON (TBD) | Defer decision until hub is actually needed |
| Config reloading | None (read at startup) | Agents are short-lived; daemon can SIGHUP |
| Validation | `cub doctor` command | Better UX than scattered error messages |

---

## 9. Differences from imp-castle

| Aspect | imp-castle | Procyon Park |
|--------|-----------|--------------|
| Config scope | Global only | Global + per-repo |
| Config fields | 1 (agent_command) | Full config struct |
| Feature flags | None | Config booleans with env override |
| Work tracker interface | 3 methods | 8+ methods (expanded for king/workflows) |
| Setup validation | None | `cub doctor` command |
| Hub support | Identity only | Identity + config surface (gated) |
| Env override | None | `PROCYON_*` convention |

---

## 10. Open Questions

1. **Config migration:** If the config schema changes between versions, should we version the config file and provide automatic migration? Or just document breaking changes?
2. **Per-repo vs per-worktree config:** Should worktrees be able to override config independently from their parent repo? Current design says no — config is at the repo level.
3. **BBS storage backend:** Should the BBS tuplespace storage location be configurable, or always under `~/.procyon-park/bbs/`? Configurability adds complexity but enables shared storage on NFS/network drives for the hub use case.
