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

Prime yourself with system context:
```bash
pp prime
```

## Work Items

Work items are the primary planning artifact. Epics contain stories. Stories are executed by workflows.

```bash
# Create
pp workitem create <id> --title "Title" --type epic|story [--repo R] [--status backlog] [--parent P]
pp workitem create <id> --title "Title" --type story --parent <epic> --repo R --wave N --template story|story-lite

# Read
pp workitem show <id> [--repo R]
pp workitem list [--repo R] [--status S] [--type T] [--parent P]
pp workitem children <id>

# Update
pp workitem update <id> [--status S] [--title T] [--description D] [--wave N]
pp workitem comment <id> "feedback text"
pp workitem ready <id>          # set status=ready (cascades to children)
pp workitem done <id>           # set status=done
pp workitem block <id> --reason "why"

# Execute
pp workitem run <id>            # start workflow for this work item
pp workitem plan <id>           # agentic planning (scout + planner create children)
pp workitem review <id>         # agentic review (reviewer edits/refines)
```

Status: `backlog` → `ready` → `in-progress` → `done` (also: `blocked`, `cancelled`)

## Workflows

List available templates:
```bash
pp read template system
```

Templates:
- `full-pipeline` — plan → dispatch-waves → review+test → evaluate → merge
- `story` — implement → review → fix cycle → merge (has review cap)
- `story-lite` — implement → merge (no review, for mechanical changes)
- `scout-mission` — research agent writes findings doc
- `feature-design` — idea → epic → stories → review → finalize
- `workitem-plan` — scout researches, planner decomposes into child work items
- `workitem-review` — reviewer refines work item tree

Start a workflow:
```bash
pp workflow <template> --param description="..." [--repo R] [--scope S]
pp workflow story --param description="Add feature X" --repo procyon-park
```

Monitor:
```bash
pp workflow status <id>
pp workflow wait <id> [--timeout 3600]    # blocks until complete
pp workflow cancel <id>
```

## Reading the Tuplespace

```bash
pp read <category> [scope] [identity]
```

Categories: `workitem`, `task`, `workflow`, `token`, `template`, `convention`, `fact`, `observation`, `decision`, `signal`, `event`, `notification`

```bash
pp read observation default        # agent findings
pp read workflow default           # running workflows
pp read signal default             # verdict signals, worktree info
```

## Writing to the Tuplespace

```bash
pp observe <identity> <detail>     # report a finding
pp decide <identity> <detail>      # record a decision
pp notify <message>                # notify the user
pp signal <id> <key> <value>       # upsert a signal
```

## Logs and History

```bash
pp log --no-follow --since 3600    # notifications from last hour
pp history --limit 50              # audit trail
pp dashboard                       # live TUI overview
```

## Typical Workflow

1. **Create work items**: `pp workitem create epic:my-feature --title "..." --type epic --repo R`
2. **Add stories**: `pp workitem create story:my-feature:s1 --parent epic:my-feature --type story ...`
3. **Or have agents plan**: `pp workitem plan epic:my-feature`
4. **Review**: `pp workitem review epic:my-feature`
5. **Mark ready**: `pp workitem ready epic:my-feature`
6. **Execute**: `pp workitem run epic:my-feature`
7. **Monitor**: `pp workflow status`, `pp log --no-follow --since 300`

## IMPORTANT: Agent Dispatch

**Do not use Claude subagents to execute PP tasks.** When you start a workflow or run a work item, PP dispatches agents via its own harness. Your role is to:

- Create and manage work items
- Start workflows
- Monitor status
- Write observations/decisions as needed
- Respond when human input is needed

Let PP drive agent execution.

## Environment

- `PP_URL` — server URL (default: http://localhost:7777)
- `PP_SCOPE` — default scope (default: default)
- `PP_PORT` — server port (default: 7777)
