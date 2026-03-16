Procyon Park orchestration interface. Use this when the user wants to interact with the PP agent orchestration system — dispatching scouts, checking status, reading tuples, managing workflows, or coordinating agents.

## Setup

First, prime yourself with the current system state:

```bash
pp prime
```

Read the output carefully — it contains the current conventions, facts, signals, and notifications from the tuplespace.

If the server isn't running, start it first:

```bash
pp serve &
```

## How to use pp

`pp` is the Procyon Park CLI. It talks to the PP server over HTTP (default: localhost:7777).

### Check system status
```bash
pp status
```

### Read from the tuplespace
```bash
pp read <category> [scope] [identity]
```

Categories: `fact`, `convention`, `observation`, `decision`, `signal`, `task`, `template`, `workflow`, `token`, `rule`, `event`, `obstacle`, `notification`

Examples:
```bash
pp read convention system              # all conventions
pp read fact myrepo                    # facts about a repo
pp read task default                   # pending tasks
pp read observation default            # current observations
pp read notification                   # all notifications
```

### Write to the tuplespace
```bash
pp observe <identity> <detail> [--tags t1,t2]     # report a finding
pp decide <identity> <detail> [--rationale ...]    # record a decision
pp event <identity> [--type T] [--summary S]       # emit an event
pp notify <message> [--severity info|warn|urgent]   # notify the user
```

### Submit a plan
Write a JSON file with subtasks, then submit it:
```bash
pp plan plan.json
```

Plan JSON format:
```json
{
  "identity": "plan-name",
  "subtasks": [
    {"identity": "subtask-1", "role": "implementer", "description": "..."},
    {"identity": "subtask-2", "role": "reviewer", "description": "..."}
  ]
}
```

Roles: `scout`, `planner`, `implementer`, `reviewer`, `tester`, `fixer`

### Write arbitrary tuples via the HTTP API
For operations not covered by the CLI, use curl against the API:
```bash
# Write a template
curl -s -X POST http://localhost:7777/api/out \
  -H 'Content-Type: application/json' \
  -d '{"category":"template","scope":"system","identity":"my-template","pinned":true,"payload":{...}}'

# Consume a tuple
curl -s -X POST http://localhost:7777/api/inp \
  -H 'Content-Type: application/json' \
  -d '{"category":"task","scope":"default","identity":"task-123"}'
```

## Workflow

A typical interaction pattern:

1. **Start the server** if not running: `pp serve &`
2. **Prime yourself**: `pp prime`
3. **Check what's going on**: `pp status`, `pp read notification`, `pp read task default`
4. **Do work**: use standard tools (Read, Write, Edit, Bash, etc.)
5. **Report findings**: `pp observe <id> <detail>`
6. **Record decisions**: `pp decide <id> <detail>`
7. **Notify the user** when human input is needed: `pp notify <message>`

## Environment Variables

- `PP_URL` — server URL (default: http://localhost:7777)
- `PP_SCOPE` — default scope (default: default)
- `PP_TASK` — current task ID (set automatically by harness)

## Important

- Always start by running `pp prime` to get current system context
- Use `pp read` liberally — the tuplespace is your shared knowledge base
- Report observations about anything unexpected
- The tuplespace is the single source of truth for coordination
