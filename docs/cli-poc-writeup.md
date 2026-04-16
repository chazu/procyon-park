# PoC: Porting `pp` to the Maggie Cli framework

**Story:** `story-1776370822-4391` (validate the Cli framework merged in
maggie commit `7930067` by porting two real `pp` subcommands).

**Scope:** `pp status` and `pp workflow list`. The original code paths in
`SystemCommands.mag` and `WorkflowCommands.mag` are unchanged. The new
implementations live in `src/cli/CliPP.mag` and are activated by setting
`PP_USE_CLI=1`. With the env var unset, every existing user and test
sees zero behaviour change.

## What got exercised

| Framework piece     | Used by                | How                                                                  |
| ------------------- | ---------------------- | -------------------------------------------------------------------- |
| `Cli::Command`      | both                   | `named:doc:`, `run:`, `registerSubcommand:`, `setArgs:`, `execute`   |
| `Cli::Flag`         | `workflow list`        | value-object form: `Cli::Flag name:type:default:doc:` then `flag:`   |
| `Cli::EnvBinding`   | `status`               | three bindings (PP_URL, PP_SCOPE, PP_TASK) → `PPCliConfig` setters   |
| `Cli::Output`       | both                   | `dictText:` for `status`, `table:headers:keys:` for `workflow list`  |
| `Cli::CliError`     | both                   | wrapping `execute` to swallow the signal that bad flags raise        |

## Acceptance criteria, scored

1. **Identical output.** ✗ partial. Same _data_, different _formatting_ — see
   "Awkwardness" below. Same exit code (0 on success). Same flag surface.
2. **Cli::Flag used.** ✓ `workflow list`'s `--status` and `--scope`.
3. **Cli::Output used (table or dict).** ✓ both: `dictText:` (status),
   `table:headers:keys:` (workflow list).
4. **Bonus: Cli::EnvBinding for an env-var option.** ✓ `status` binds
   PP_URL, PP_SCOPE, PP_TASK onto a `PPCliConfig` instance via three
   `Cli::EnvBinding`s and `applyEnvBindings:` from inside the run block.

## What worked

- **`Cli::Command named:doc:run:` reads cleanly.** Three lines describe
  the command shape; cobra wires up everything else. The block receiving
  positional args and returning a `SmallInteger` exit code matches
  Maggie's natural style.
- **`--help` and `--<unknown-flag>` rejection are free.** Cobra
  generates per-command help from `shortDoc:` + flag docs, and rejects
  unknown flags with usage output. No code on the Maggie side. This
  alone is worth the migration.
- **`Cli::Flag` value objects compose well.** Defining the flag spec
  separately from registration lets us imagine programmatically generated
  flags (e.g. one per template parameter). The optional `required:`
  variant is there for when we want to enforce input validation.
- **`Cli::EnvBinding` is exactly the pattern `CLIBase>>initialize`
  hand-rolls today.** `default:` matches the "use this value when env
  unset" semantics line-for-line. Defining bindings on the command and
  calling `applyEnvBindings: cfg` from the run block reads better than
  the imperative `System env: '…'; ifNil: [..]; ifEmpty: [..]` chain.
- **No build-system surprises.** `mag build -o pp` picked up the new
  `Cli::Command`, `Cli::Flag`, `Cli::EnvBinding`, and `Cli::Output`
  references with no extra import / dep declaration. The Maggie image
  already ships them.

## What was awkward

1. **`Cli::Output table:headers:keys:` quotes string cells.** The
   formatter does `(row at: key) printString`, and Maggie's
   `String>>printString` returns the Smalltalk-quoted form (`'foo'`).
   Every string cell rendered as `'full-pipeline'` instead of
   `full-pipeline`:

   ```
   INSTANCE ID                    TEMPLATE         STATUS
   'full-pipeline-1775062444-16'  'full-pipeline'  'running'
   ```

   Workaround would be to coerce all cells to non-strings or
   pre-quote-strip, neither of which scales. **Real fix is a 5-line
   patch in `lib/Cli/Output.mag`** to route cell values through
   `formatValue:` (which already special-cases Strings) instead of
   raw `printString`. File this as a follow-up story against maggie.

2. **`Cli::Output dictText:` reorders keys.** Maggie's `Dictionary keys`
   doesn't preserve insertion order, so `status`'s field listing comes
   out in arbitrary order between runs:

   ```
   tuples  680                ←  inserted first, lands wherever
   uptime  11853s
   task    story-…
   scope   procyon-park
   ```

   Original output put fields in a meaningful order (Tuples, Uptime,
   Task, Scope). For status-style output this matters. **Either
   `dictText:` should accept an explicit key-order argument, or callers
   should print one field at a time.** For now we accept the reorder
   because the user-visible fields are still all there.

3. **`Cli::CliError` panics if uncaught.** Passing `--bogus` raises a
   Maggie exception that the VM turns into a panic if the run block /
   wrapper doesn't catch it. The pattern is well-known (see
   `examples/cli/demo.mag`), but a fresh caller will hit this exactly
   once. **Two paths forward:** lift the catch into the `execute`
   primitive on the Go side (the cleanest fix), or add a one-liner
   note in `docs/cli.md` (cheap stop-gap). The `docs/cli.md` quickstart
   currently does not show the catch.

4. **`Cli::EnvBinding` setters are single-argument.** They use
   `perform: setter with: value`, which can't reach two-keyword
   selectors like `Dictionary>>at:put:`. We had to define a tiny
   `PPCliConfig` class with `#baseUrl:`, `#scope:`, `#taskId:` setters.
   For one-off CLI configs that's fine; for shared config objects it
   means everyone needs a setter shim. Not worth changing — single-arg
   setters are the idiomatic Smalltalk shape — but worth knowing
   before you reach for `at:put:`.

5. **Args-slicing is manual at the boundary.** Because the new
   framework lives alongside the old dispatcher, `CliPP runStatus:` has
   to slice `args[1..]` before calling `setArgs:` so cobra sees its
   leaf command's flags and not `'status'` itself. Likewise
   `runWorkflowList:` slices `args[1..]` to leave cobra with
   `#('list' …)` so the parent/child tree dispatches. **This vanishes
   the moment the Cli root owns the entire `pp` namespace** (i.e. once
   the migration is finished and `Main.start` calls one big root
   command). It's an artefact of the parallel-implementation
   transition, not the framework.

6. **`Main.start` doesn't propagate the exit code.** Both old and new
   code paths return from `Main.start` without invoking `System exit:`,
   so `cmd execute`'s integer return value is dropped. Pre-existing
   PP limitation; calling it out because the new framework actually
   _returns_ a real exit code for the first time.

## Suggested next migration story

**`pp log`** is the right next port. It has:

- A bool flag pair (`--follow` / `--no-follow`) — exercises
  `Cli::Flag type: #bool` and the "default true, --no-X disables"
  pattern in a non-trivial way.
- A typed-string flag (`--since`) — exercises integer-ish flag values
  parsed from strings.
- A natural `Cli::EnvBinding` candidate: `PP_LOG_FOLLOW` /
  `PP_LOG_SINCE` would let agents configure their default log behaviour
  without per-invocation flags.
- An infinite-loop streaming mode (`--follow`) — verifies the run
  block can do non-trivial work and isn't cobra-time-limited.

After `pp log`, the larger surfaces (`pp prime`, `pp task`, `pp gc`)
become mechanical because the framework muscle memory is in place.

**Pre-requisite for the next story:** the `Cli::Output` cell-formatter
fix (item 1 above). Without it, every ported tabular command will look
broken — and `pp history` is a table.

## Files

- `src/cli/CliPP.mag` — the entire PoC, ~230 lines including the
  `PPCliConfig` setter class.
- `src/Main.mag` — eight added lines that gate dispatch on
  `PP_USE_CLI` + `CliPP handles: args`. Old paths untouched.
- `docs/cli-poc-writeup.md` — this file.

## Reproduce

```bash
make build

# Old paths (unchanged):
./pp status
./pp workflow list --status running

# New paths (PP_USE_CLI=1):
PP_USE_CLI=1 ./pp status
PP_USE_CLI=1 ./pp workflow list --status running
PP_USE_CLI=1 ./pp status --help          # generated help
PP_USE_CLI=1 ./pp workflow list --bogus  # cobra error handling
```
