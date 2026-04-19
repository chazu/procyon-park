# pp serve startup crash (pre-existing on main)

**Discovered:** 2026-04-19 during multiplayer-test-runner green-ing pass
**Severity:** high — blocks `pp serve` on a machine without existing BBS state
**Scope:** `src/Main.mag` startServer path, specifically around `seedTemplates`

## Symptom

```
$ ./pp serve
Procyon Park v0.1.0
===================
BBS: no persisted state found.
BBS initialized.
System conventions seeded.
panic: (vm.SignaledException) 0x14000bdb160

goroutine 1 [running]:
github.com/chazu/maggie/vm.(*VM).signalExceptionObject(...)
github.com/chazu/maggie/vm.(*VM).signalException(...)
github.com/chazu/maggie/vm.(*VM).registerObjectPrimitives.func25(...)  // primError:
...
github.com/chazu/maggie/vm.(*Interpreter).sendDoesNotUnderstand(...)
```

The crash is a Maggie-level `doesNotUnderstand` that bubbles to the VM as
a SignaledException. No human-readable selector name in the trace.

## Repro

1. Move `~/.pp/data` aside: `mv ~/.pp/data ~/.pp/data.bak`
2. Rebuild: `rm -f pp && mag build`
3. Run: `./pp serve`
4. Panics after "System conventions seeded."

Crash does **not** reproduce if BBS state exists on disk (the convention
seeding path is skipped). This masked the bug during the multiplayer rollout —
every restart ran against an existing BBS.

## Bisect state

- Crashes at HEAD (`44e2c59` — test-runner merge)
- Crashes at `f5b8fdc` (pre-test-runner, final ux merge)
- Not yet bisected earlier. The regression likely landed in one of the UX
  epic merges (Server.mag got heavily edited there) or somewhere else that
  altered startup-path behavior.

## Hypothesis

`Main.startServer` after "System conventions seeded" runs:

```
(bbs scanAll: 'template') do: [:t |
  bbs inp: 'template' scope: (t at: 'scope') identity: (t at: 'identity')
].
self seedTemplates: bbs.
```

Then HarnessFactory / Dispatcher / ApiServer construction. Crash is
somewhere in that chain. Likely candidates:
- seedTemplates hitting a selector added somewhere that's not yet
  implemented
- ApiServer init loading admin pubkeys / config.toml when config is
  missing — could call a method on nil
- SignatureVerifier init path referencing a missing selector

## Workaround

Preserve `~/.pp/data` across restarts. If a fresh start is truly needed,
copy existing bbs.json from another working instance.

## Fix approach (for whoever picks this up)

1. Instrument `Main.startServer` with println checkpoints between every
   step after "System conventions seeded." to narrow the crash line.
2. Identify the failing selector — likely one of: an action on an ApiServer
   component, a config-loader method on nil, or a template-loader edge
   case.
3. Bisect from `f5b8fdc` backward through UX merges to find the first
   breaking commit if the selector doesn't point at the culprit directly.

## Related

- Discovered while running `multiplayer-test-runner`'s green-ing pass.
- Maggie-side stdlib gaps (`Object>>identityHash`, `String>>isString`,
  `String>>trimSeparators`) were closed in maggie commit `cc69554` to
  unblock several multiplayer test suites that depended on them. That
  work is orthogonal to this bug — the crash reproduces before and
  after the stdlib additions.
