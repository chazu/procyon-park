# Phase 5 Research: Priming & Agent Instructions

## Overview

The priming system is how imp-castle gives each agent its identity, responsibilities, and communication vocabulary at spawn time. It is the critical bridge between "a generic LLM session" and "an agent that knows its role, tools, and coordination protocol."

The system is implemented in `internal/prime/` (template loading, transforms) and `internal/cli/prime.go` (CLI entry point), with supporting context injection from `internal/bbs/prompt.go` and `internal/bbs/context.go`.

## Architecture

### How `pp prime` Works

The `pp prime` command is invoked by an agent immediately after spawn. The flow:

```
Agent spawns → runs `pp prime` → receives role instructions on stdout → begins work
```

**Resolution order:**

1. Read `PP_AGENT_ROLE` environment variable (e.g., `cub`, `king`, `reviewer`, `merge-handler`)
2. Check for user-customized template at `~/.imp-castle/instructions/<role>.txt`
3. Fall back to embedded templates compiled into the binary via `//go:embed templates/*.txt`
4. If BBS mode is enabled (`PP_FEATURE_BBS_ENABLED`):
   a. Try loading a BBS-specific template (`<role>-bbs.txt`) first
   b. If no BBS template exists, apply surgical regex transforms to the base template
   c. Append BBS protocol addendum (tuplespace CLI reference)
   d. Pre-read tuplespace and inject context (conventions, facts, active claims, obstacles)
5. Print the final instructions to stdout

### File Layout

```
internal/prime/
├── instructions.go              # Template loading, regex transforms, export
├── instructions_test.go         # Unit tests for loading and listing
├── instructions_bbs_test.go     # BBS transform and template tests
└── templates/
    ├── cub.txt                  # Standard (mail-based) worker instructions
    ├── cub-bbs.txt              # BBS-native worker instructions
    ├── king.txt                 # Standard coordinator instructions
    ├── king-bbs.txt             # BBS-native coordinator instructions
    ├── reviewer.txt             # Code review specialist instructions
    └── merge-handler.txt        # Merge conflict handler instructions

internal/cli/prime.go            # Cobra CLI command wiring
internal/bbs/prompt.go           # AgentPromptAddendum() - BBS CLI reference block
internal/bbs/context.go          # BuildAgentContext() - pre-read tuplespace state
internal/config/config.go        # InstructionsDir(), EnsureDirectories()
internal/config/features.go      # IsBBSEnabled() feature flag
```

## Role Templates

### Roles and Their Purposes

| Role | Template(s) | Purpose |
|------|------------|---------|
| `cub` | `cub.txt`, `cub-bbs.txt` | Worker agent that implements tasks |
| `king` | `king.txt`, `king-bbs.txt` | Coordinator that creates tasks, spawns agents, reviews work |
| `reviewer` | `reviewer.txt` | Code review specialist (no BBS variant) |
| `merge-handler` | `merge-handler.txt` | Merge conflict resolution specialist (no BBS variant) |

### Template Anatomy

Each template follows a consistent structure:

1. **Identity statement** - Who the agent is ("You are an IMP agent...")
2. **Environment variables** - What env vars are available (PP_AGENT_NAME, PP_REPO, PP_TASK, etc.)
3. **Responsibilities** - What the agent must do
4. **Workflow** - Step-by-step instructions for the task lifecycle
5. **Completion protocol** - Mandatory signaling steps when done
6. **Communication** - How to coordinate (mail-based or BBS-based)
7. **CLI reference** - Available commands (beads, cub commands)
8. **Label conventions** - How to tag discovered work

### Key Differences: Mail vs BBS Templates

**Mail-based (`cub.txt`, `king.txt`):**
- Communication via `cub send <agent> "<message>"` and `cub mail`
- King directly assigns tasks with `cub send`
- Completion signaled via `cub send daemon --type output`
- Direct agent-to-agent messaging

**BBS-based (`cub-bbs.txt`, `king-bbs.txt`):**
- Communication via tuplespace tuples (`pp bbs out/in/rd/scan`)
- Agents self-organize by scanning for available work
- Atomic claiming protocol prevents duplicate work
- Trail-leaving: agents post obstacles, needs, artifacts as they work
- Completion signaled via `pp bbs out event $PP_REPO task_done`
- Stigmergic coordination — no direct messaging needed

The BBS cub template is fundamentally different in philosophy: agents are "autonomous contributors in a stigmergic multi-agent system" rather than directed workers. The BBS king template shifts from micro-management to "creating conditions for productive work."

## BBS-Aware Instruction Transforms

When BBS mode is enabled but a role has no dedicated `-bbs.txt` template (e.g., `reviewer`), the system performs surgical regex-based replacement on the base template.

### Regex Transform Pipeline

The `ReplaceCommunicationSection()` function applies these transforms in order:

1. **Remove `COMMUNICATION:` section** - Strips the entire mail-based communication block
2. **Remove `IMP COMMANDS:` section** - Strips the mail CLI reference
3. **Replace `cub send daemon --type output`** → BBS event tuple (`pp bbs out event ... task_done`)
4. **Replace "ALWAYS notify king" blocks** → BBS completion event
5. **Replace standalone `cub send king` lines** → BBS event tuple
6. **Replace "notify king" prose** → "write completion event"
7. **Replace "completion mail(s)" prose** → "completion event tuple"
8. **Remove remaining `cub mail` references**

This is implemented with compiled regex patterns (`reDaemonSend`, `reKingNotifyBlock`, `reKingSend`, `reNotifyKing`, `reCompletionMail`, `reImpMail`) that match structural patterns rather than exact strings, making them resilient to template wording changes.

### Section Removal

`removeSection()` removes a named section by:
1. Finding the header line (e.g., `COMMUNICATION:`)
2. Consuming all subsequent non-empty lines
3. Stopping at the next blank line (section boundary)

This is a simple line-based parser, not a full structural parser. It works because templates follow a consistent format with blank lines between sections.

## BBS Context Injection

After template selection and transforms, the priming system appends two blocks:

### 1. BBS Protocol Addendum (`AgentPromptAddendum`)

A static reference block that teaches the agent:
- How the tuplespace works (categories, scopes, identities, payloads)
- The atomic claiming protocol (consume `available` tuple, write `claim` tuple)
- How to signal while working (obstacle, need, artifact tuples)
- The completion protocol (mandatory `task_done` event)
- Pulse cadence for checking notifications
- Notification piggybacking (automatic with `--agent-id`)
- Full BBS CLI reference

The addendum is parameterized with scope and taskID — when a task is directed, concrete IDs replace `<task-id>` placeholders.

### 2. Pre-Read Tuplespace Context (`BuildAgentContext`)

Dynamically built from the current tuplespace state:

```
BBS TUPLESPACE CONTEXT (pre-read at launch):
CONVENTIONS:
  [convention/system] tuple-schema: ...
  [convention/system] category-vocabulary: ...
FACTS:
  [fact/repo] my-repo: main_branch=main, language=go, ...
ACTIVE AGENT ACTIVITY:
  [claim/my-repo] task-123: agent=Sprocket, status=in_progress
  [obstacle/my-repo] flaky-test: task=task-456, detail=...
TASK ESCALATIONS (for task-789):
  [obstacle/my-repo] missing-api-key: task=task-789, ...
```

Built by scanning:
1. **Furniture tuples** (persistent) — conventions and facts from `system` and repo scopes
2. **Active session tuples** — claims, obstacles, needs from the last hour
3. **Task-specific escalations** — obstacles/needs referencing the agent's assigned task

Activity tuples are capped at 10 per category with an omission notice. Payloads are summarized as `key=value` pairs or use a `description` field if present.

## Custom Instruction Overrides

Users can override any template by placing a file at:
```
~/.imp-castle/instructions/<role>.txt
```

The `ExportTemplates()` function copies all embedded templates to this directory for editing:
```go
prime.ExportTemplates()  // Writes all templates to ~/.imp-castle/instructions/
```

Custom templates take absolute priority — no transforms or BBS addendum are applied to custom templates unless BBS mode appends the protocol block afterward.

## Design Decisions and Rationale

### Why Embedded Templates?

Templates are compiled into the binary via `//go:embed`. This ensures:
- Agents always have a working baseline (no file-not-found at runtime)
- Templates are versioned with the binary
- No dependency on filesystem state for core functionality

### Why Regex Transforms Instead of Full BBS Templates for Every Role?

Not every role needs a complete BBS rewrite. The reviewer and merge-handler roles are structurally similar in both modes — only the communication commands change. Regex transforms handle this efficiently without maintaining duplicate templates that would drift out of sync.

The cub and king roles have fundamentally different workflows in BBS mode (stigmergic vs directed), so they warrant separate templates.

### Why Pre-Read the Tuplespace?

Without pre-reading, every agent's first action would be scanning the tuplespace. Pre-reading:
- Eliminates a round-trip at startup
- Ensures agents see the same state as when they were spawned
- Provides immediate context for work selection

### Why Separate `available` and `claim` Tuples?

The atomic claiming protocol uses two-phase claiming:
1. `available` tuple (consumed atomically — only one agent wins)
2. `claim` tuple (written after winning — advisory for others)

The `available` tuple provides the atomic guarantee. The `claim` tuple is informational — it tells other agents what's taken. This separation means even if the claim write fails, the atomic guarantee still holds.

## Implications for Maggie (Procyon Park)

### What to Preserve

1. **Template embedding** (`//go:embed`) — proven, reliable pattern
2. **User-customizable overrides** with fallback chain (custom → embedded)
3. **BBS-native templates** for roles with fundamentally different workflows
4. **Regex-based transforms** for roles that only need communication swaps
5. **Context pre-reading** at spawn time (conventions, facts, activity, escalations)
6. **Parameterized addendum** with scope/taskID substitution
7. **Structured completion protocol** with mandatory steps

### What to Reconsider

1. **Template format** — Plain text with ALL-CAPS section headers works but is fragile for programmatic parsing. Consider a structured format (YAML frontmatter + markdown body, or Go `text/template` with variables) for Maggie.

2. **Section removal logic** — The current `removeSection()` is a simple line-based parser that relies on blank-line section boundaries. This is brittle if templates deviate from the expected format. A more robust approach would use explicit section delimiters or a proper template engine.

3. **Transform ordering** — The regex transforms are applied in a fixed order. If a future transform's output matches a later transform's pattern, there could be double-replacement. Consider making transforms explicitly ordered and idempotent.

4. **Role extensibility** — Adding a new role currently requires creating a template file and recompiling. Consider a plugin or registry pattern where roles can be added at runtime.

5. **BBS context size** — The pre-read context grows linearly with tuplespace size. The 10-per-category cap helps, but for large projects with many agents, the total context could become significant. Consider a relevance-based scoring system or configurable verbosity levels.

6. **Template versioning** — When the binary updates, user-customized templates may become stale (missing new sections, outdated commands). Consider adding a version header or diff-based migration.

7. **No template inheritance** — `cub-bbs.txt` is a complete rewrite of `cub.txt`, not a delta. When the base `cub.txt` gets a new section (like label conventions), the BBS template must be manually updated too. Consider a base-plus-overlay approach.

### Maggie-Specific Considerations

1. **Go `text/template`** — Maggie could use Go's standard template engine with typed data (role, scope, taskID, features, tuplespace context) instead of string concatenation and regex. This would make templates testable and composable.

2. **Template registration** — Instead of hardcoding role names, Maggie could have a `RegisterRole(name string, template []byte)` API that allows dynamic role addition.

3. **Middleware pipeline** — Instead of a monolithic `runPrime()`, model transforms as a middleware pipeline: `loadTemplate → applyBBSTransform → injectContext → render`. Each step is independently testable.

4. **Context budget** — Track token count of injected context and trim or summarize when it exceeds a budget. This prevents context-window bloat for agents in large projects.

5. **Tuplespace as first-class** — Since Maggie is BBS-native from day one, the "mail vs BBS" duality can be eliminated. All templates can assume tuplespace communication, simplifying the transform layer significantly.

## Key Source Files (Absolute Paths)

- `/Users/chazu/.cub/internal/prime/instructions.go` — Template loading, transforms, export
- `/Users/chazu/.cub/internal/prime/instructions_test.go` — Loading/listing tests
- `/Users/chazu/.cub/internal/prime/instructions_bbs_test.go` — BBS transform tests
- `/Users/chazu/.cub/internal/cli/prime.go` — CLI command wiring
- `/Users/chazu/.cub/internal/bbs/prompt.go` — `AgentPromptAddendum()` static block
- `/Users/chazu/.cub/internal/bbs/context.go` — `BuildAgentContext()` dynamic block
- `/Users/chazu/.cub/internal/config/config.go` — `InstructionsDir()`, `EnsureDirectories()`
- `/Users/chazu/.cub/internal/config/features.go` — `IsBBSEnabled()` feature flag
- `/Users/chazu/.cub/internal/prime/templates/cub.txt` — Standard cub template
- `/Users/chazu/.cub/internal/prime/templates/cub-bbs.txt` — BBS cub template
- `/Users/chazu/.cub/internal/prime/templates/king.txt` — Standard king template
- `/Users/chazu/.cub/internal/prime/templates/king-bbs.txt` — BBS king template
- `/Users/chazu/.cub/internal/prime/templates/reviewer.txt` — Reviewer template
- `/Users/chazu/.cub/internal/prime/templates/merge-handler.txt` — Merge handler template
