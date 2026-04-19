---
name: procyon-park
description: Use when working in a procyon-park (pp) workspace, spawning/coordinating agents, or reading/writing BBS tuplespace entries — including when PP_AGENT_NAME/PP_REPO/PP_TASK env vars are set, AGENTS.md references `pp bbs`, or the user mentions "pp", "bbs", "tuplespace", or "procyon-park".
---

# Procyon Park (pp)

## Overview

Procyon Park is a multi-agent coordination system. The `pp` CLI talks to a background daemon (auto-started) that hosts a BBS tuplespace — agents coordinate by writing/reading/consuming tuples rather than via an issue tracker.

The whole binary is compiled from Maggie (`.mag`) sources — a Smalltalk-like language. There is no Go source in this repo.

## Detecting this environment

You are inside a pp-managed session when any of these hold:
- `PP_AGENT_NAME`, `PP_REPO`, `PP_TASK`, `PP_WORKTREE`, `PP_BRANCH` are set
- The repo contains an `AGENTS.md` referencing `pp bbs`
- `pp` is on PATH and `pp ping` succeeds

## Core commands

| Command | Purpose |
|---|---|
| `pp ping` | Health-check daemon (auto-starts it) |
| `pp status` | System overview |
| `pp daemon run\|stop\|status` | Manage daemon lifecycle |
| `pp agent list\|spawn\|dismiss\|show\|status\|respawn\|prune` | Agent management |
| `pp bbs list\|write\|read\|scan\|take` | Tuplespace operations |
| `pp repo ...` | Tracked repositories |
| `pp config get --key X` / `pp config list` | Configuration |

Global flags: `--socket <path>`, `-o/--output text|json`, `-v/--verbose`, `-q/--quiet`.

`--output json` is the reliable machine-readable form — prefer it when parsing.

## BBS tuplespace — the coordination primitive

Tuples are `(category, repo, key, payload)`. Operations:

- `pp bbs scan <category>` — non-destructive listing
- `pp bbs rd <category> <repo> [key]` — non-destructive read
- `pp bbs in <category> <repo> <key> [--timeout 30s]` — **atomic take** (removes tuple; blocks until present or timeout). Only one caller wins.
- `pp bbs out <category> <repo> <key> '<json-payload>'` — write a tuple

Common categories: `task/available`, `claim`, `event`, `fact`, `obstacle`.

## Task workflow (for spawned agents)

```bash
# 1. Find work
pp bbs scan task/available

# 2. Claim atomically — `in` removes the tuple so only one agent wins
if pp bbs in task/available "$PP_REPO" "$PP_TASK" --timeout 5s; then
    pp bbs out claim "$PP_REPO" "$PP_TASK" \
        '{"agent":"'"$PP_AGENT_NAME"'","status":"in_progress"}'
else
    pp bbs out obstacle "$PP_REPO" "task-already-claimed" \
        '{"task":"'"$PP_TASK"'"}'
fi

# 3. Do the work in $PP_WORKTREE on $PP_BRANCH

# 4. Signal completion
pp bbs out event "$PP_REPO" task_done \
    '{"task":"'"$PP_TASK"'","agent":"'"$PP_AGENT_NAME"'"}'
```

Request dismissal when done for good:
```bash
pp bbs out event "$PP_REPO" dismiss_request '{"agent":"'"$PP_AGENT_NAME"'"}'
```

## Agent environment

Variables injected by the daemon on spawn:

| Var | Example | Meaning |
|---|---|---|
| `PP_AGENT_NAME` | `Marble` | Agent identity |
| `PP_AGENT_ROLE` | `cub` | Role |
| `PP_REPO` | `procyon-park` | Repo name |
| `PP_TASK` | `procyon-park-3dk` | Assigned task id |
| `PP_WORKTREE` | `/path/to/worktree` | Working directory |
| `PP_BRANCH` | `agent/Marble/procyon-park-3dk` | Git branch |

Tmux session naming: `pp-<agent>` (global) or `pp-<repo>-<agent>` (repo-scoped).

## Session completion (MANDATORY when ending work)

1. File obstacle/follow-up tuples for anything unresolved.
2. Run quality gates if code changed (tests/lint/build).
3. Emit `task_done` event tuple.
4. **Push to remote**:
   ```bash
   git pull --rebase && git push && git status  # must say "up to date with origin"
   ```
5. Hand off context via a `fact` or `event` tuple.

Skipping the push step is the most common failure — the work is not complete until the remote has it.

## Building pp itself (only when hacking on pp)

`pp` is built from Maggie sources:
```bash
make clean && make && make install   # runs `mag build -o pp`
```
All source lives under `src/` as `.mag` files (cli, bbs, daemon, agent, …). There is no Go to edit. If you need to add a VM primitive, you must rebuild `cmd/mag/maggie.image` in the upstream maggie repo first (`make mag` there, then `go install ./cmd/mag/`).

## Pitfalls

- **`pp bbs in` is destructive by design.** Use `rd` or `scan` if you only want to look.
- **One winner per task.** Let `in` decide — never hand-check "is it claimed" and then claim; that race is the whole point of `in`.
- **Always push before declaring done.** `git status` must show up-to-date with origin.
- **Don't invent commands.** `pp help` lists exactly what exists; subcommand help is not implemented (`pp bbs help` errors). When unsure, read the subcommand's usage line from the error output.
- **Startup noise.** `pp` prints Maggie VM bytecode debug lines to stderr on every invocation. Filter with `2>/dev/null` when capturing output, or use `--output json` and read stdout.
- **Don't use an external issue tracker.** Tasks are tuples. `bd`/Linear/etc. do not apply here unless the project's AGENTS.md says so.

## Mining scout reports for observations

When reading a scout-mission finding (e.g. `docs/scout-*.md` on a feature
branch), look for two kinds of observation material and record them via
`pudl observe`:

- **Globally-applicable heuristics** — design principles, bug-class framings,
  and general engineering lessons that would help on any codebase. Record
  with `--scope global` and `--kind pattern` (or `antipattern`/`suggestion`
  as appropriate). Example: "prefer schemas where the invalid state is
  unrepresentable, not merely unreachable."
- **Repo-scoped facts** — concrete findings about the current codebase:
  invariants, race conditions, drift risks, hot paths, unexpected couplings.
  Record with `--scope <repo>:<path>` and the matching `--kind` (`fact`,
  `bug`, `obstacle`, `suggestion`).

Scout reports are dense with material of both kinds; one scout pass can
yield several observations. Do this opportunistically while reading — don't
batch to the end — so the insights aren't lost when the doc scrolls away.

## Quick-reference: read AGENTS.md first

The repo's `AGENTS.md` is the source of truth for project-specific workflow. Read it before acting if it exists — it overrides generic guidance here.
