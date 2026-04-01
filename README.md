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

# Register a repo
./pp repo add /path/to/repo --name my-repo

# Start the server
./pp serve

# In another terminal — run a workflow
./pp workflow story --param description="Add error handling to login" --repo my-repo

# Monitor
./pp workflow status
./pp log
./pp dashboard
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
pp serve                              Start the server
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
