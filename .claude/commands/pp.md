Procyon Park orchestration interface. Use this when the user wants to interact with the PP agent orchestration system — managing work items, dispatching workflows, checking status, or coordinating agents.

## Setup

Check if the server is running:
```bash
pp status
```

If not, start it:
```bash
pp serve &
```

Prime yourself with system context (shows conventions, facts, signals, notifications):
```bash
pp prime
```

## Your Role

**Do not use Claude subagents to execute PP tasks.** PP has its own agent harness that spawns Claude processes with role-specific system prompts. Your job is to:

- Create and manage work items
- Start workflows and monitor their progress
- Read tuplespace state and relay information to the user
- Write observations/decisions when the user asks
- Respond when human input is needed

Let PP drive agent execution. You are the **control plane**, not the **data plane**.

## Work Items

Work items are the primary planning artifact. **Epics** contain **stories**. Stories are executed by workflows.

### Create

```bash
pp workitem create <id> --title "Title" --type epic|story [flags...]
```

Flags:
- `--repo R` — target repository (also sets scope)
- `--status S` — initial status (default: `backlog`)
- `--parent P` — parent work item id (auto-links child to parent)
- `--description D` — detailed description
- `--template T` — workflow template (default: `story`; also: `story-lite`)
- `--wave N` — execution wave number (default: 1; lower waves run first)
- `--depends-on X,Y` — comma-separated dependency ids
- `--label L1,L2` — comma-separated labels
- `--batch B` — batch identifier

Example — epic with two stories:
```bash
pp workitem create epic:auth --title "Auth overhaul" --type epic --repo my-repo
pp workitem create story:auth:s1 --title "Add JWT validation" --type story --parent epic:auth --repo my-repo --wave 1 --description "Implement JWT token validation middleware"
pp workitem create story:auth:s2 --title "Add rate limiting" --type story --parent epic:auth --repo my-repo --wave 2 --template story-lite
```

### Read

```bash
pp workitem show <id> [--repo R]
pp workitem list [--repo R] [--status S] [--type T] [--parent P] [--label L]
pp workitem children <id> [--repo R]
```

### Update

```bash
pp workitem update <id> [--status S] [--title T] [--description D] [--wave N] [--depends-on X,Y] [--template T]
pp workitem comment <id> "feedback text"
pp workitem ready <id>          # set status=ready (cascades to children)
pp workitem done <id>           # set status=done (cascades to children)
pp workitem block <id> --reason "why"
```

### Execute

```bash
pp workitem run <id>            # start the work item's workflow template
pp workitem plan <id>           # agentic planning: scout researches, planner decomposes into child stories
pp workitem review <id>         # agentic review: reviewer edits/refines work item tree
```

Status lifecycle: `backlog` → `ready` → `in-progress` → `done` (also: `blocked`, `cancelled`)

## Workflows

### Templates

List available templates:
```bash
pp read template system
```

| Template | Flow | Use for |
|----------|------|---------|
| `full-pipeline` | plan → dispatch-waves → review+test → evaluate → merge | Epics: plans stories, dispatches in waves, review+test cycle |
| `story` | setup → implement → review → [fix cycle] → merge → notify | Standard stories with code review (max 3 review cycles) |
| `story-lite` | setup → implement → merge → notify | Mechanical/low-risk changes, no review step |
| `scout-mission` | setup → scout → done | Research-only task, writes findings, no code changes |
| `multi-scout` | spawns parallel scout-mission children | Multiple research questions in parallel |
| `feature-design` | setup → design → review-design → decompose → stories → finalize | High-level feature design: idea → epic → stories |
| `workitem-plan` | setup → research → decompose → notify | Agentic planning: scout + planner create child work items |
| `workitem-review` | setup → review-design → finalize → notify | Agentic refinement of work item tree |

### Start / Monitor / Cancel

```bash
# Start
pp workflow <template> --param description="..." [--repo R] [--scope S]
pp workflow story --param description="Add feature X" --repo procyon-park

# Monitor
pp workflow status              # list all running workflows
pp workflow status <id>         # detailed status (tokens, tasks, review cycle)
pp workflow wait <id> [--timeout 3600]  # block until complete/failed/cancelled

# Cancel
pp workflow cancel <id> [--scope S]
```

## Reading the Tuplespace

```bash
pp read <category> [scope] [identity]
```

Categories: `workitem`, `task`, `workflow`, `token`, `template`, `convention`, `fact`, `observation`, `decision`, `signal`, `event`, `notification`

Useful reads:
```bash
pp read workflow default           # running workflows
pp read task default               # pending/dispatched agent tasks
pp read observation default        # agent findings
pp read signal default             # verdict signals, worktree paths
pp read convention system          # system rules agents follow
```

If you specify an identity (3rd arg), it reads one specific tuple. Otherwise it scans the whole category+scope.

## Writing to the Tuplespace

```bash
pp observe <identity> <detail> [--tags t1,t2]         # report a finding
pp decide <identity> <detail> [--rationale "..."]      # record a decision
pp event <identity> [--type T] [--summary S]           # emit an event
pp plan <file.json>                                    # submit a structured plan (JSON file)
pp notify <message> [--severity info|warn|urgent]      # notify the user
pp signal <id> <key> <value>                           # upsert a signal tuple
pp task <id> --role <r> --description <d> [--workdir <p>] [--repo <name>]  # create a task directly
```

## Logs and History

```bash
pp log --no-follow --since 3600    # notifications from last hour (single fetch)
pp log                             # follow mode: long-poll for new notifications
pp history --limit 50              # audit trail of all BBS operations
pp history --category observation --scope default --limit 20
pp dashboard                       # live TUI overview
```

## Repo and Worktree Management

```bash
pp repo add <path> --name <name>   # register a repository
pp repo list                       # list registered repos
pp repo remove <name>              # unregister
pp repo info <name>                # show repo details

pp worktree list                   # list active worktrees
pp worktree clean                  # remove stale worktrees
pp clean-branches [--dry-run] [--all]  # remove stale feature branches
```

## Recipes

### Quick: run a single story
```bash
pp workflow story --param description="Fix the login timeout bug" --repo my-repo
pp log --no-follow --since 300     # check progress
```

### Full: plan and execute an epic
```bash
# 1. Create the epic
pp workitem create epic:my-feature --title "Add OAuth support" --type epic --repo my-repo --description "Implement OAuth2 login flow with Google and GitHub providers"

# 2. Have agents plan the decomposition
pp workitem plan epic:my-feature

# 3. Review what the planner created
pp workitem children epic:my-feature
pp workitem review epic:my-feature   # optional: agentic review/refinement

# 4. Mark ready and execute
pp workitem ready epic:my-feature
pp workitem run epic:my-feature

# 5. Monitor
pp workflow status
pp log --no-follow --since 300
```

### Research: send a scout
```bash
pp workflow scout-mission --param description="Survey error handling patterns in the codebase" --repo my-repo
```

## Environment

- `PP_URL` — server URL (default: `http://localhost:7777`)
- `PP_SCOPE` — default scope (default: `default`)
- `PP_PORT` — server port for `pp serve` (default: `7777`)
- `PP_TASK` — current task ID (set automatically by harness for spawned agents)
- `PP_REPO` — default repo name (used as fallback when `--repo` is omitted)
- `PP_REPO_PATH` — repo filesystem path (set by harness)
- `PP_WORKFLOW` — current workflow instance ID (set by harness)
