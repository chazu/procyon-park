---
name: procyon-park
description: Use when working in a procyon-park (pp) workspace, spawning/coordinating agents, starting workflows, or reading/writing BBS tuplespace entries — including when PP_TASK/PP_URL/PP_SCOPE/PP_WORKFLOW env vars are set, AGENTS.md references `pp`, or the user mentions "pp", "bbs", "tuplespace", "workflow", or "procyon-park".
---

# Procyon Park (pp)

## Overview

Procyon Park is a multi-agent coordination system. The `pp` CLI talks to a long-running HTTP server (`pp serve`, default `http://localhost:7777`) that hosts:

- A **BBS tuplespace** — CUE-validated tuples in three modalities (linear, persistent, affine).
- A **workflow engine** — Petri-net templates that drive multi-step work (research → plan → implement → review → merge).
- A **scheduler / harness** that spawns Claude Code agents into git worktrees and feeds them context tuples.
- An **identity layer** — every mutating request is Ed25519-signed; multiple humans can share one server via invite tokens.

The whole binary is compiled from Maggie (`.mag`) sources — a Smalltalk-like language. There is no Go source in this repo.

## Detecting this environment

You are inside a pp-managed session when any of these hold:
- `PP_TASK`, `PP_URL`, `PP_SCOPE`, `PP_WORKFLOW`, `PP_REPO`, or `PP_ACTOR` is set in the environment.
- The repo's `AGENTS.md` references `pp` commands.
- `pp status` succeeds against the configured `PP_URL`.

If `pp status` fails with a connection error, the server isn't running — start it with `pp serve` (or check `~/.pp/logs/pp-serve.log`).

## High-level command surface

| Command | Purpose |
|---|---|
| `pp serve [--port 7777]` | Start the HTTP server (auto-bootstraps a `local` identity on first run) |
| `pp status` | System overview |
| `pp prime [--role <role>]` | Print the canonical agent priming context |
| `pp workflow <tmpl> [--param k=v]` / `status` / `list` / `cancel` / `wait` | Start and manage workflow instances |
| `pp workitem create\|show\|list\|update\|run\|plan\|review` | Manage hierarchical work items |
| `pp task <id> --role <r> --description <d>` | Create a one-off task (no workflow) |
| `pp observe <identity> <detail>` | Record an observation tuple |
| `pp decide <identity> <detail> [--rationale ..]` | Record a decision tuple |
| `pp event <identity> [--type T] [--summary S]` | Emit an event tuple |
| `pp signal <id> <key> <value>` | Upsert a signal (workflow coordination) |
| `pp verdict <pass\|fix\|exhausted>` | Reviewer verdict (uses `$PP_WORKFLOW`) |
| `pp read <category> [scope] [identity]` | Convenience read |
| `pp notify <message> [--severity ..]` | Push a user-facing notification |
| `pp dismiss` | Signal task completion (frees the harness slot) |
| `pp log` / `pp history` / `pp audit` | Streaming and historical activity |
| `pp dashboard` | Open the web dashboard (`http://localhost:7777/dashboard/`) |
| `pp repo add\|list\|remove\|info` | Tracked repositories |
| `pp worktree list\|clean` / `pp clean-branches` | Worktree and branch hygiene |
| `pp gc [--dry-run]` | Sweep terminal workflows, orphan tuples, merged branches, old sessions |
| `pp identity init\|show\|list\|use\|rotate\|invite\|accept` | Local keypairs and multiplayer onboarding |
| `pp user add\|revoke` | Admin-gated server-side user registry |
| `pp -i <name> <cmd>` / `PP_IDENTITY=<name> <cmd>` | One-shot identity override |
| `pp session [<taskId>]` / `pp usage [<taskId>]` | Inspect Claude Code session logs and token usage |
| `pp bbs list\|get\|put\|rm` | Direct tuplespace access (operator surface) |

There is no per-subcommand help (`pp foo help` errors). Run the bare subcommand to see its usage line, or read `pp help` for the master list.

## Workflow templates (`workflows/*.cue`)

The default templates live in `workflows/`:

| Template | Use |
|---|---|
| `story` | Single task with review cycle: setup → implement → review → [pass/fix] → merge → notify |
| `story-lite` | Mechanical changes, no review |
| `full-pipeline` | Full epic execution: plan → dispatch-waves → review+test → evaluate → merge |
| `scout-mission` | Research only, writes a finding doc; no code changes |
| `multi-scout` | Spawn multiple `scout-mission` children in parallel |
| `feature-design` | Epic decomposition: ideate → epic → stories → refine |
| `workitem-plan` / `workitem-review` | Agentic planning / review of a work-item tree |
| `doc-update`, `hotfix`, `spike` | Specialized flows |

Author guidance: `docs/authoring-workflows.md`. Composition model: `docs/workflow-composition.md`.

## BBS tuplespace

Tuples are addressed by `(category, scope, identity)` and carry a JSON payload. The CLI exposes four operations:

```
pp bbs list [--category C] [--scope S] [--identity I] [--json]
       List tuples; with no --category scans every category.
pp bbs get  <category> <scope> <identity>
       Fetch a single tuple as JSON. Exits 1 if not found.
pp bbs put  <category> <scope> <identity> <payload>
       [--pinned] [--ttl SEC] [--modality persistent|linear|affine]
       UPSERT a tuple. <payload> is inline JSON or @path/to/file.json.
pp bbs rm   <category> <scope> <identity>
       Idempotent (exits 0 on no-such-tuple).
```

### Modalities

- **persistent** — survives reads, never expires. Default for pinned categories.
- **linear** — consumed on read. Default for non-pinned categories.
- **affine** — auto-expires after `--ttl SEC`.

`--pinned` forces persistent; `--ttl SEC` forces affine; `--modality M` sets it explicitly.

### Pinned categories (default to persistent)

`fact`, `convention`, `template`, `rule`, `ingestion`, `artifact`, `link`, `decision`, `identity`.

### Server guarantees

- **Server-side category validation** — the CLI cannot construct an invalid tuple; bad `--category` fails fast with the valid set listed.
- **Durable on return** — `put` and `rm` synchronously flush BBS state to disk before the HTTP response. A SIGKILL right after the ack does not lose the mutation.
- **`put` is UPSERT on `(category, scope, identity)`** — any existing tuple with the same triple is consumed before the new one is written. Tuple ids are server-generated (client-supplied ids are ignored).
- **`rm` is idempotent** — removing a non-existent tuple prints `no such tuple` and exits 0.

### Examples

```bash
# Insert a fact (persistent because 'fact' is pinned).
pp bbs put fact procyon-park:architecture bbs-durability \
  '{"detail":"bbs writes are synchronously flushed on CLI put/rm"}'

# List every task tuple across the tuplespace.
pp bbs list --category task

# Inspect a specific decision.
pp bbs get decision global plan:command-palette-stories

# Remove a stale signal (safe to re-run).
pp bbs rm signal my-repo:feature/x worktree
```

## How agents coordinate (high-level)

Spawned agents almost never use `pp bbs` directly. The high-level verbs project onto BBS tuples for them:

- `pp observe` → `observation` tuple. Use for unexpected findings, drift, or anything worth surfacing.
- `pp decide` → `decision` tuple (pinned). Use to record a chosen path with rationale.
- `pp event` → `event` tuple. Use for milestones (`task_done`, etc.).
- `pp signal <id> <key> <value>` → upsert a `signal` tuple. Reviewers/foremen use this to write verdicts (`pp signal verdict:$PP_WORKFLOW decision pass`) and cycle counts.
- `pp verdict <pass|fix|exhausted>` → shorthand for the reviewer verdict signal (uses `$PP_WORKFLOW`).
- `pp dismiss` → mark this agent's task complete; the workflow advances and the harness slot is freed.

Stay tightly scoped to your assigned task. Do not modify code outside its boundary unless the task description requires it.

## Agent environment

Variables injected by the harness when spawning a Claude Code agent:

| Var | Example | Meaning |
|---|---|---|
| `PP_URL` | `http://localhost:7777` | Server URL for `pp` commands |
| `PP_SCOPE` | `default` or `<repo>:<feature>` | Default scope for tuples |
| `PP_TASK` | `story-1776989899-1675` | Current task id |
| `PP_REPO` | `procyon-park` | Repo name |
| `PP_REPO_PATH` | `/path/to/worktree` | Working directory |
| `PP_WORKFLOW` | `full-pipeline-1774626675` | Workflow instance id (for `signal`/`verdict`) |
| `PP_ACTOR` | `chazu/<workflow>/Implementer` | Observational identity stamped into tuples |

There are no `PP_AGENT_NAME` / `PP_BRANCH` / `PP_WORKTREE` vars in the current harness — agent identity is derived from `<operator>/<workflow>/<role>` and the working directory is set via `PP_REPO_PATH`.

Worktrees live under `~/.pp/worktrees/`.

## Session completion (MANDATORY when ending work)

1. **File observations / decisions** for anything unresolved or interesting (`pp observe`, `pp decide`).
2. **Run quality gates** if code changed (tests / lint / build for the affected repo).
3. **Push to remote** — the work is not complete until origin has it:
   ```bash
   git pull --rebase && git push && git status   # must say "up to date with origin"
   ```
4. **Reviewers**: write the verdict signal (`pp verdict pass|fix|exhausted` or `pp signal verdict:$PP_WORKFLOW decision <d>`).
5. **`pp dismiss`** — signals the workflow engine to advance and frees the harness slot.

Skipping the push is the most common failure mode.

## Identity and multiplayer

Every mutating request is Ed25519-signed. The first `pp serve` auto-creates a `local` identity; `pp init <name>` before first serve creates a custom-named admin instead. To onboard a teammate without copy-pasting hex pubkeys:

```bash
# Admin
pp identity invite alice --ttl 600   # share the printed command via Signal/etc.

# Alice
pp identity accept http://<host>:7777 --name alice --token <token>
pp whoami
```

Manual: `pp user add alice --pubkey <hex>` / `pp user revoke alice`.

## Building pp itself (only when hacking on pp)

```bash
mag build -o pp && codesign -s - pp        # plain build
make build                                  # same, via Makefile
make full                                   # full rebuild (slower; rebuilds image)
make install                                # copies pp + static/ to ~/.pp/static
```

Source lives under `src/` as `.mag` files (`bbs/`, `api/`, `cli/`, `dispatcher/`, `harness/`, `roles/`, …). There is no Go to edit here. If you need a new VM primitive, rebuild `cmd/mag/maggie.image` in the upstream maggie repo first.

Logs:
- `~/.pp/logs/pp-serve.log` — rolling serve log when run under `scripts/pp-supervisor.sh`.
- `~/.pp/logs/crash-<epoch>.log` — per-crash dump (exception class + message).

## Pitfalls

- **Workflows, not raw `pp bbs in`.** The old "atomic claim" pattern is gone; coordination flows through workflow templates and the dispatcher. Don't hand-roll claim logic.
- **`pp bbs put` is UPSERT.** It silently overwrites the prior tuple at `(category, scope, identity)`. To append a new piece of evidence, vary the identity (or use `observe`/`event`, which auto-key).
- **No subcommand help.** `pp <foo> help` errors. Run the bare subcommand for its usage line, or `pp help` for the master list.
- **Always push before `pp dismiss`.** `git status` must show up-to-date with origin. Dismissing without pushing strands the work on a local branch.
- **Stay scoped.** The dispatcher gives you a worktree (`PP_REPO_PATH`) and a task description. Don't sprawl outside it.
- **Identity is signed; don't copy-paste keys.** Use `pp identity invite` / `pp identity accept` for onboarding.
- **`pp gc` is destructive by design.** It sweeps merged branches, completed worktrees, and old session files. Run with `--dry-run` first when in doubt.
- **Don't use an external issue tracker.** Tasks are tuples / work items / workflow instances. `bd`/Linear/etc. don't apply unless `AGENTS.md` says so.

## Mining scout reports for observations

When reading a scout-mission finding (e.g. `docs/scout-*.md` on a feature branch), look for two kinds of observation material and record them:

- **Globally-applicable heuristics** — design principles, bug-class framings, general engineering lessons that would help on any codebase. Record via `pudl observe --scope global --kind pattern` (or `antipattern`/`suggestion`). Example: "prefer schemas where the invalid state is unrepresentable, not merely unreachable."
- **Repo-scoped facts** — concrete findings about the current codebase: invariants, race conditions, drift risks, hot paths, unexpected couplings. Record with `--scope <repo>:<path>` and the matching `--kind` (`fact`, `bug`, `obstacle`, `suggestion`).

Scout reports are dense with material of both kinds; one pass can yield several observations. Do this opportunistically while reading — don't batch to the end — so insights aren't lost when the doc scrolls away.

For pp-internal observations (not global heuristics), `pp observe <identity> <detail>` writes directly to the tuplespace and shows up on the dashboard.

## Quick reference: read AGENTS.md first

The repo's `AGENTS.md` is the source of truth for project-specific workflow. Read it before acting if it exists — it overrides generic guidance here. (In `procyon-park` itself, `AGENTS.md` currently only contains the GitNexus block; the canonical pp guidance is `pp prime`.)
