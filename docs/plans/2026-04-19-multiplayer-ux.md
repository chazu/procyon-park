# Multiplayer UX Overhaul Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Procyon Park solo-by-default with auto-bootstrapped identity, and give multiplayer onboarding real primitives (`pp init`, `pp whoami`, `pp identity use`, `-i`/`PP_IDENTITY`, `pp identity invite`/`accept`), plus a rewritten README.

**Architecture:** Keep `enforce_signatures=true` default. First `pp serve` auto-creates a `local` identity, writes it to `server.toml[admin].admins`, and registers it as an identity tuple — so the single-user install is "multiplayer with one pre-enrolled admin" and needs no user action. Add CLI commands to inspect and switch the active identity without editing files. Add a server-side invite-token flow so onboarding a teammate is two commands (`pp identity invite <name>` → `pp identity accept <url>`), with no hex pubkey copy-paste.

**Tech Stack:** Maggie (Smalltalk-dialect) CLI + API server; Ed25519 signing via `maggie-crypto-primitives`; TOML config via `Toml decodeFile:` / `Toml encode:`; tests use the hand-rolled `TestX` runner pattern already in `test/`.

---

## File Structure

### New files
- `src/cli/CliInit.mag` — `pp init` and shared bootstrap helper
- `src/cli/CliIdentityUse.mag` — `pp identity use`, `pp identity invite`, `pp identity accept`
- `src/cli/CliWhoami.mag` — `pp whoami`
- `src/api/InviteStore.mag` — invite token issuance and claim
- `test/test_identity_switch.mag` — `use`, `whoami`, `-i`/`PP_IDENTITY` override
- `test/test_invite_store.mag` — token issuance, claim, expiry
- `test/test_auto_bootstrap.mag` — first-run server bootstrap

### Modified files
- `src/api/Server.mag` — auto-bootstrap in `loadSecurityConfig`; `/api/invite/create` + `/api/invite/claim` routes
- `src/identity/IdentityStore.mag` — `setCurrent:` method
- `src/cli/CLIBase.mag` — `-i <name>` flag / `PP_IDENTITY` env var override on `currentIdentity`
- `src/cli/PP.mag` — dispatch `init`, `whoami`, `identity use|invite|accept`
- `src/cli/CliIdentity.mag` — nothing (keep init/show/rotate/list); new commands live in CliIdentityUse.mag
- `README.md` — new Quick Start (solo + multiplayer sections)
- `test/MultiplayerTestMain.mag` — register new test classes

---

## Task 1: Add `IdentityStore>>setCurrent:` primitive

**Why first:** Every other identity command (`use`, `init`, `accept`, bootstrap) needs to flip the `current` pointer. Right now only `init:force:` sets it, and only when unset.

**Files:**
- Modify: `src/identity/IdentityStore.mag` (add classMethod)

**Test strategy note:** Maggie has no `System setenv:` primitive and no class variables, so there's no clean in-process way to sandbox filesystem tests against a fake `$HOME`. `setCurrent:` is a pure 3-line wrapper over `ensureDir` + `writeFileContents` — we skip the unit test and exercise it end-to-end via Task 3's subprocess-based integration test (which invokes the built `pp` binary with `HOME=/tmp/...`).

- [ ] **Step 1: Implement `setCurrent:`**

In `src/identity/IdentityStore.mag`, add alongside the other classMethods (place it near `loadCurrent` so related logic groups together):

```smalltalk
  "--- setCurrent: name -------------------------------------------------

   Point the `current` file at `name`. Caller is responsible for
   ensuring the key files exist — this is a pure pointer rewrite."

  classMethod: setCurrent: name [
    self ensureDir.
    File writeFileContents: self currentPath contents: name mode: 8r644.
    ^true
  ]
```

- [ ] **Step 2: Verify existing tests still pass**

```bash
rm -f pp-test-identity && mag build -o pp-test-identity test/test_identity_store.mag && ./pp-test-identity
```

Expected: "ALL TESTS PASSED" (existing tests unaffected).

- [ ] **Step 3: Commit**

```bash
git add src/identity/IdentityStore.mag
git commit -m "feat(identity): add IdentityStore>>setCurrent: primitive"
```

---

## Task 2: `pp whoami` command

**Files:**
- Create: `src/cli/CliWhoami.mag`
- Modify: `src/cli/PP.mag` (dispatch)
- Modify: `src/Main.mag` (add file if needed — check existing bootstrap)

- [ ] **Step 1: Check how source files are loaded**

```bash
grep -rn "CliIdentity" ./src/Main.mag ./maggie.toml
```

If there's an explicit include list, we'll add the new files there. If it's directory-auto-discovery, nothing to do. (Expected: directory-based — Maggie's `mag build` discovers `.mag` files in the configured roots.)

- [ ] **Step 2: Write the CliWhoami class**

Create `src/cli/CliWhoami.mag`:

```smalltalk
"CliWhoami — `pp whoami`. Print the active local identity's name,
 hex pubkey, and proquint. Returns exit 0 if an identity is loaded,
 1 if none is configured (with a hint to run `pp init`)."

CliWhoami subclass: Object

  classMethod: run: args [
    | rec |
    rec := IdentityStore loadCurrent.
    rec isNil ifTrue: [
      'No active identity. Run `pp init` (solo) or `pp identity init <name>` (advanced).' println.
      ^1
    ].
    ('name:     ', (rec at: 'name')) println.
    ('pubkey:   ', (rec at: 'pubHex')) println.
    ('proquint: ', (rec at: 'proquint')) println.
    ^0
  ]
```

- [ ] **Step 3: Wire into PP.mag**

In `src/cli/PP.mag>>runWith:`, add alongside the other command dispatches (near the `identity` line):

```smalltalk
    cmd = 'whoami'    ifTrue: [^CliWhoami run: args].
```

And in `printUsage`, add to the commands list:

```smalltalk
    '  whoami                                           Show the active local identity' println.
```

- [ ] **Step 4: Build and smoke test**

```bash
rm -f pp && mag build -o pp && codesign -s - pp
./pp whoami
```

Expected: either current identity details printed, or the "No active identity" hint.

- [ ] **Step 5: Commit**

```bash
git add src/cli/CliWhoami.mag src/cli/PP.mag
git commit -m "feat(cli): add pp whoami"
```

---

## Task 3: `pp identity use <name>` command

**Files:**
- Create: `src/cli/CliIdentityUse.mag` (also houses `invite`/`accept` later; empty stubs now)
- Modify: `src/cli/PP.mag`
- Create: `test/test_identity_switch.sh` (shell-based integration test — isolates via `HOME=`)

**Test strategy:** In-process Maggie tests can't fake `$HOME` (no `setenv:` primitive). So for CLI/filesystem integration, we use a shell script that:
1. Builds `pp` into a temp binary
2. Runs `pp` commands with `HOME=/tmp/pp-switch-<ts>` prefixes
3. Asserts by grepping the output / filesystem

- [ ] **Step 1: Implement CliIdentityUse**

Create `src/cli/CliIdentityUse.mag`:

```smalltalk
"CliIdentityUse — `pp identity use <name>` switches the active local
 identity by rewriting ~/.config/pp/identity/current. Verifies the
 named identity actually exists on disk first."

CliIdentityUse subclass: Object

  classMethod: run: args [
    | name rec |
    args size < 1 ifTrue: [
      'Usage: pp identity use <name>' println.
      ^1
    ].
    name := args at: 0.
    rec := IdentityStore load: name.
    rec isNil ifTrue: [^1].
    IdentityStore setCurrent: name.
    ('Active identity: ', name) println.
    ^0
  ]
```

- [ ] **Step 2: Wire into PP.mag**

In `PP.mag>>cmdIdentity:`, add the new subcommand:

```smalltalk
    sub = 'use'    ifTrue: [^CliIdentityUse run: rest].
```

And update the usage string in that method:

```smalltalk
      'Usage: pp identity <init|show|rotate|list|use|invite|accept> [args...]' println.
```

- [ ] **Step 3: Write the shell-based integration test**

Create `test/test_identity_switch.sh`:

```bash
#!/usr/bin/env bash
# Integration test for `pp identity use` — builds a fresh pp binary and
# exercises identity switching under a scratch HOME.
set -euo pipefail

cd "$(dirname "$0")/.."
TMP=$(mktemp -d "/tmp/pp-switch-XXXXXX")
trap 'rm -rf "$TMP"' EXIT

rm -f pp-int && mag build -o pp-int >/dev/null && codesign -s - pp-int

PP="env HOME=$TMP ./pp-int"

$PP identity init alice >/dev/null
$PP identity init bob --force >/dev/null
# First-init wins: current should be alice
WHOAMI=$($PP whoami | grep '^name:' | awk '{print $2}')
[[ "$WHOAMI" == "alice" ]] || { echo "FAIL: expected alice, got $WHOAMI"; exit 1; }

$PP identity use bob >/dev/null
WHOAMI=$($PP whoami | grep '^name:' | awk '{print $2}')
[[ "$WHOAMI" == "bob" ]] || { echo "FAIL: expected bob after use, got $WHOAMI"; exit 1; }

echo "PASS: identity switch"
```

Make it executable:

```bash
chmod +x test/test_identity_switch.sh
```

- [ ] **Step 4: Run the integration test**

```bash
./test/test_identity_switch.sh
```

Expected: `PASS: identity switch`. (Depends on Task 2's `pp whoami` — run Task 2 first if not already done.)

- [ ] **Step 5: Commit**

```bash
git add src/cli/CliIdentityUse.mag src/cli/PP.mag test/test_identity_switch.sh
git commit -m "feat(cli): add pp identity use <name>"
```

---

## Task 4: `-i <name>` / `PP_IDENTITY` override on CLIBase

**Goal:** Per-command identity override without editing the `current` pointer. Priority order: `-i` flag > `PP_IDENTITY` env > `current` pointer.

**Files:**
- Modify: `src/cli/CLIBase.mag`
- Modify: `src/cli/PP.mag` (strip `-i` flag in dispatcher)
- Modify: `test/test_identity_switch.sh` (extend integration test)

- [ ] **Step 1: Modify `CLIBase>>currentIdentity` and `initialize`**

In `src/cli/CLIBase.mag`:

Add `identityOverride` to `instanceVars:` at line 11:

```smalltalk
  instanceVars: baseUrl client scope taskId identityRecord identityResolved identityOverride
```

Update `initialize` (line 98), append:

```smalltalk
    identityOverride := System env: 'PP_IDENTITY'.
    (identityOverride notNil and: [identityOverride isEmpty]) ifTrue: [identityOverride := nil]
```

Update `currentIdentity` (line 112):

```smalltalk
  method: currentIdentity [
    identityResolved ifFalse: [
      identityResolved := true.
      [
        (identityOverride notNil and: [identityOverride isEmpty not])
          ifTrue: [identityRecord := IdentityStore load: identityOverride]
          ifFalse: [identityRecord := IdentityStore loadCurrent]
      ] on: Exception do: [:e | identityRecord := nil]
    ].
    ^identityRecord
  ]
```

Add a setter used by Task 5's `-i` flag wiring:

```smalltalk
  method: overrideIdentity: name [
    "Override the active identity for this CLI instance (used by -i <name>).
     Invalidates any memoised record so the next currentIdentity reloads."
    identityOverride := name.
    identityResolved := false.
    identityRecord := nil
  ]
```

- [ ] **Step 2: Wire `-i` flag in PP.mag dispatcher**

In `src/cli/PP.mag>>runWith:`, before dispatching, strip a leading `-i <name>` and call the override:

```smalltalk
  method: runWith: args [
    | cmd wtArgs idx i idArg stripped |
    args isEmpty ifTrue: [self printUsage. ^self].
    "Strip -i <name> from anywhere in args and apply to this CLI."
    stripped := Array new: 0.
    i := 0.
    [i < args size] whileTrue: [
      idArg := args at: i.
      ((idArg = '-i' or: [idArg = '--identity']) and: [i + 1 < args size]) ifTrue: [
        self overrideIdentity: (args at: i + 1).
        i := i + 2
      ] ifFalse: [
        stripped := stripped copyWith: idArg.
        i := i + 1
      ]
    ].
    args := stripped.
    "... existing dispatch below unchanged ..."
```

(Leave the remainder of the method as-is after the `args := stripped` line.)

- [ ] **Step 3: Extend the integration test**

Append to `test/test_identity_switch.sh`, after the existing assertions:

```bash
# current is bob — but PP_IDENTITY should override
WHOAMI=$(env PP_IDENTITY=alice HOME=$TMP ./pp-int whoami | grep '^name:' | awk '{print $2}')
[[ "$WHOAMI" == "alice" ]] || { echo "FAIL: PP_IDENTITY override, got $WHOAMI"; exit 1; }

# -i flag should override too
WHOAMI=$($PP -i alice whoami | grep '^name:' | awk '{print $2}')
[[ "$WHOAMI" == "alice" ]] || { echo "FAIL: -i override, got $WHOAMI"; exit 1; }

echo "PASS: identity override"
```

- [ ] **Step 4: Run**

```bash
./test/test_identity_switch.sh
```

Expected: both `PASS` lines. Any FAIL exits non-zero.

- [ ] **Step 5: Commit**

```bash
git add src/cli/CLIBase.mag src/cli/PP.mag test/test_identity_switch.sh
git commit -m "feat(cli): support -i/--identity flag and PP_IDENTITY override"
```

---

## Task 5: Auto-bootstrap on first `pp serve`

**Goal:** If `server.toml` has no admins AND there are no identity tuples in the BBS AND no `~/.config/pp/identity/current` exists, `pp serve` silently:
1. Generates a `local` identity under `~/.config/pp/identity/`
2. Points `current` at it
3. Writes `server.toml` with `[admin].admins = ["<hex>"]` and `[security].enforce_signatures = true`
4. Seeds a pinned identity tuple via `bbs upsertPinned: 'identity' scope: '/' identity: 'user:local' payload: {name,pubkey,role} actor: nil` — matching the canonical shape used by `handleUserAdd` at `Server.mag:2336`
5. Prints a one-line "Bootstrapped as 'local'" message (not the scary banner)

**Files:**
- Modify: `src/api/Server.mag` (replace the bootstrap banner with actual bootstrap)
- Create: `test/test_auto_bootstrap.sh` (shell-based integration test)

**Test strategy:** Same as Task 3 — the bootstrap logic reads `$HOME` and writes files; we test by invoking `pp serve` under a scratch HOME and then asserting filesystem + a follow-up `pp whoami`.

- [ ] **Step 1: Study current behavior**

Re-read `src/api/Server.mag:76-89` — the `loadSecurityConfig` method. We'll replace the banner with an auto-bootstrap branch. Also read `handleUserAdd` around line 2314 to copy the canonical identity-tuple construction.

- [ ] **Step 2: Implement `bootstrapIfNeeded` in Server.mag**

In `src/api/Server.mag`, replace the block at lines 76–89 (the `PP BOOTSTRAP REQUIRED` banner inside `loadSecurityConfig`) with:

```smalltalk
    (adminList isEmpty and: [(bbs scanAll: 'identity') isEmpty]) ifTrue: [
      self bootstrapIfNeeded
    ]
```

And add the method below `loadSecurityConfig`:

```smalltalk
  method: bootstrapIfNeeded [
    "First-run convenience: create a 'local' identity, make it admin,
     and seed a matching identity tuple. Skipped if the user already
     has a current pointer (they've run `pp identity init` manually)."
    | home idDir currentPointer ident pubHex tomlPath tomlCfg adminCfg secCfg payload admins |
    home := System env: 'HOME'.
    home isNil ifTrue: [home := ''].
    idDir := home, '/.config/pp/identity'.
    currentPointer := idDir, '/current'.
    (File exists: currentPointer) ifTrue: [
      "User has an identity but hasn't registered it. Tell them how."
      '' println.
      ('Warning: local identity exists but is not registered as admin.') println.
      ('  Add its pubkey to ~/.config/pp/server.toml under [admin].admins') println.
      ^self
    ].
    "Generate 'local' identity via IdentityStore."
    (IdentityStore init: 'local' force: false) ifFalse: [^self].
    ident := (IdentityStore load: 'local') at: 'identity'.
    pubHex := ident pubHex.
    "Write server.toml."
    tomlPath := home, '/.config/pp/server.toml'.
    admins := (Array new: 0) copyWith: pubHex.
    adminCfg := Dictionary new.
    adminCfg at: 'admins' put: admins.
    secCfg := Dictionary new.
    secCfg at: 'enforce_signatures' put: true.
    secCfg at: 'skew_seconds' put: 120.
    tomlCfg := Dictionary new.
    tomlCfg at: 'admin' put: adminCfg.
    tomlCfg at: 'security' put: secCfg.
    File writeFileContents: tomlPath contents: (Toml encode: tomlCfg) mode: 8r644.
    "Seed pinned identity tuple matching handleUserAdd's shape
     (category=identity, scope=/, identity=user:<name>)."
    payload := Dictionary new.
    payload at: 'name' put: 'local'.
    payload at: 'pubkey' put: pubHex.
    payload at: 'role' put: 'admin'.
    bbs upsertPinned: 'identity'
        scope: '/'
        identity: 'user:local'
        payload: payload
        actor: nil.
    "Re-read adminList so this server process picks it up without restart."
    adminList := admins.
    '' println.
    ('Procyon Park bootstrapped as ''local'' (admin pubkey: ', pubHex, ')') println.
    ('  Local identity: ', idDir, '/local.{key,pub}') println.
    ('  Config: ', tomlPath) println.
    '' println
  ]
```

**Cross-reference:** the target shape of the identity tuple is exactly `handleUserAdd` (Server.mag:2314) — `bbs upsertPinned: 'identity' scope: '/' identity: 'user:', name payload: payload actor: actor`.

- [ ] **Step 3: Write the shell integration test**

Create `test/test_auto_bootstrap.sh`:

```bash
#!/usr/bin/env bash
# Integration test: first-run `pp serve` in an empty $HOME auto-bootstraps
# a 'local' identity, writes server.toml, and subsequent CLI calls are
# signed successfully.
set -euo pipefail

cd "$(dirname "$0")/.."
TMP=$(mktemp -d "/tmp/pp-boot-XXXXXX")
DATA=$(mktemp -d "/tmp/pp-data-XXXXXX")
PORT=$((7000 + RANDOM % 500))
trap 'kill $SERVER 2>/dev/null; rm -rf "$TMP" "$DATA"' EXIT

rm -f pp-int && mag build -o pp-int >/dev/null && codesign -s - pp-int

# Start server with scratch HOME so ~/.config/pp and ~/.pp are both isolated.
env HOME=$TMP PP_DATA_DIR=$DATA ./pp-int serve --port $PORT >/tmp/pp-boot.log 2>&1 &
SERVER=$!
sleep 3

# Assertions
[[ -f "$TMP/.config/pp/server.toml" ]] || { echo "FAIL: server.toml not written"; cat /tmp/pp-boot.log; exit 1; }
[[ -f "$TMP/.config/pp/identity/local.key" ]] || { echo "FAIL: local.key not generated"; exit 1; }
grep -q "admins =" "$TMP/.config/pp/server.toml" || { echo "FAIL: admins key missing"; cat "$TMP/.config/pp/server.toml"; exit 1; }

WHOAMI=$(env HOME=$TMP PP_URL=http://localhost:$PORT ./pp-int whoami | grep '^name:' | awk '{print $2}')
[[ "$WHOAMI" == "local" ]] || { echo "FAIL: whoami returned $WHOAMI"; exit 1; }

# Prove signed calls work end-to-end.
OUT=$(env HOME=$TMP PP_URL=http://localhost:$PORT ./pp-int observe local "bootstrap smoke test" 2>&1)
echo "$OUT" | grep -q '"error"' && { echo "FAIL: signed observe rejected: $OUT"; exit 1; }

echo "PASS: auto-bootstrap"
```

Notes:
- Requires `pp serve --port <n>` flag; if it doesn't exist, pass via `PP_PORT` env var instead (check `Server.mag` for what's supported).
- `PP_DATA_DIR` may not exist — if not, the test just shares the default `~/.pp` under the scratch HOME, which is fine.

```bash
chmod +x test/test_auto_bootstrap.sh
```

- [ ] **Step 4: Run the integration test**

```bash
./test/test_auto_bootstrap.sh
```

Expected: `PASS: auto-bootstrap`. If `--port` or data-dir env isn't supported, adjust flag names to match the actual server CLI.

- [ ] **Step 5: Commit**

```bash
git add src/api/Server.mag test/test_auto_bootstrap.sh
git commit -m "feat(api): auto-bootstrap local identity on first pp serve"
```

---

## Task 6: `pp init` — explicit bootstrap with custom name

**Goal:** Wrapper around the bootstrap logic that lets the user pick a name other than `local`. Runs client-side before the server starts — writes identity files and server.toml, then optionally invokes `pp serve`.

**Files:**
- Create: `src/cli/CliInit.mag`
- Modify: `src/cli/PP.mag` (dispatch)

- [ ] **Step 1: Implement CliInit**

Create `src/cli/CliInit.mag`:

```smalltalk
"CliInit — `pp init [name]`. Ergonomic wrapper that:
   1. Creates a local Ed25519 identity named <name> (default: $USER or 'local')
   2. Writes ~/.config/pp/server.toml with that pubkey as sole admin
   3. Prints next-step guidance

 Idempotent: if an identity already exists, prints a reminder instead of clobbering.
 Meant as the first thing a new user runs. `pp serve` auto-bootstraps to
 'local' if the user never runs this — pp init is the non-default-name path."

CliInit subclass: Object

  classMethod: run: args [
    | name rec ident home tomlPath tomlCfg adminCfg secCfg pubHex |
    name := args size > 0
      ifTrue: [args at: 0]
      ifFalse: [
        | user |
        user := System env: 'USER'.
        (user isNil or: [user isEmpty]) ifTrue: ['local'] ifFalse: [user]
      ].
    home := System env: 'HOME'.
    home isNil ifTrue: [home := ''].
    tomlPath := home, '/.config/pp/server.toml'.
    "Short-circuit if already initialised."
    (File exists: tomlPath) ifTrue: [
      'Procyon Park is already initialised:' println.
      ('  config:   ', tomlPath) println.
      rec := IdentityStore loadCurrent.
      rec notNil ifTrue: [('  identity: ', (rec at: 'name')) println].
      ^0
    ].
    "Create identity."
    (IdentityStore init: name force: false) ifFalse: [^1].
    rec := IdentityStore load: name.
    ident := rec at: 'identity'.
    pubHex := ident pubHex.
    "Write server.toml."
    adminCfg := Dictionary new.
    adminCfg at: 'admins' put: ((Array new: 0) copyWith: pubHex).
    secCfg := Dictionary new.
    secCfg at: 'enforce_signatures' put: true.
    secCfg at: 'skew_seconds' put: 120.
    tomlCfg := Dictionary new.
    tomlCfg at: 'admin' put: adminCfg.
    tomlCfg at: 'security' put: secCfg.
    File createDirectory: (home, '/.config/pp').
    File writeFileContents: tomlPath contents: (Toml encode: tomlCfg) mode: 8r644.
    '' println.
    ('Procyon Park initialised. Identity: ', name) println.
    ('  pubkey:   ', pubHex) println.
    ('  proquint: ', ident proquint) println.
    ('  config:   ', tomlPath) println.
    '' println.
    'Next: `pp serve` to start the server.' println.
    ^0
  ]
```

- [ ] **Step 2: Wire into PP.mag**

Add to `runWith:`:

```smalltalk
    cmd = 'init'      ifTrue: [^CliInit run: (args size > 1 ifTrue: [args copyFrom: 1] ifFalse: [Array new: 0])].
```

Add to `printUsage`:

```smalltalk
    '  init     [<name>]                                First-time setup (create identity + config)' println.
```

- [ ] **Step 3: Smoke test**

```bash
rm -f pp && mag build -o pp && codesign -s - pp
# Test in isolated HOME
HOME=/tmp/pp-init-test ./pp init alice
HOME=/tmp/pp-init-test ./pp whoami
# Verify idempotence
HOME=/tmp/pp-init-test ./pp init alice
rm -rf /tmp/pp-init-test
```

Expected: first call bootstraps alice; second reports already-initialised.

- [ ] **Step 4: Commit**

```bash
git add src/cli/CliInit.mag src/cli/PP.mag
git commit -m "feat(cli): add pp init for explicit first-time setup"
```

---

## Task 7: Invite store (server side)

**Goal:** New BBS-backed short-lived invite tokens. Admin creates; invitee claims by POSTing a fresh pubkey against the token.

**Design:**
- New pinned BBS category: `invite`. Tuples use `scope: '/'`, `identity: 'invite:<name>'` (mirroring the `user:<name>` convention) with payload `{name, token, expires_at, created_by}`.
- `POST /api/invite/create` — admin-gated. Body: `{name, ttl_seconds?}` (default 600). Creates a random 32-byte token, stores the invite via `bbs upsertPinned:scope:identity:payload:actor:`, returns `{name, token, expires_at, url}`.
- `POST /api/invite/claim` — UNSIGNED (invitee has no identity yet). Body: `{name, token, pubkey}`. Verifies token matches + not expired + removes the invite tuple (via `inp:scope:identity:`) + writes an identity tuple (`upsertPinned: 'identity' scope: '/' identity: 'user:<name>'`) so the new user is immediately registered.

**Files:**
- Create: `src/api/InviteStore.mag`
- Modify: `src/api/Server.mag` (routes)
- Modify: `src/bbs/Categories.mag` (add `invite` to valid + pinned)
- Create: `test/test_invite_store.mag`

- [ ] **Step 1: Add `invite` category**

Check `src/bbs/Categories.mag`:

```bash
grep -n "'invite'\|valid\|pinned" ./src/bbs/Categories.mag
```

Add `'invite'` to both the `valid` list and the `pinned` list (matching the pattern used for `'identity'`).

- [ ] **Step 2: Write the test file**

Create `test/test_invite_store.mag`:

```smalltalk
"Tests for InviteStore: create, claim, expiry, double-claim."

TestInviteStore subclass: Object
  instanceVars: passed failed failures

  classMethod: new [
    ^super new init
  ]

  method: init [
    passed := 0. failed := 0. failures := Array new: 0.
    ^self
  ]

  method: assert: c message: m [
    c ifTrue: [passed := passed + 1]
      ifFalse: [failed := failed + 1. failures := failures copyWith: m.
                ('FAIL: ', m) println]
  ]

  method: run [
    self testCreateGeneratesToken.
    self testClaimConsumesInvite.
    self testDoubleClaimFails.
    self testExpiredClaimFails.
    ^self report
  ]

  method: testCreateGeneratesToken [
    | bbs store inv |
    bbs := BBS new.
    store := InviteStore new bbs: bbs.
    inv := store create: 'bob' ttl: 60 createdBy: 'alice'.
    self assert: (inv at: 'token') size >= 32 message: 'token long enough'.
    self assert: (inv at: 'name') = 'bob' message: 'name preserved'.
    self assert: (bbs scanAll: 'invite') size = 1 message: 'invite tuple stored'
  ]

  method: testClaimConsumesInvite [
    | bbs store inv result |
    bbs := BBS new.
    store := InviteStore new bbs: bbs.
    inv := store create: 'bob' ttl: 60 createdBy: 'alice'.
    result := store claim: 'bob' token: (inv at: 'token') pubkey: 'deadbeef'.
    self assert: result notNil message: 'claim succeeded'.
    self assert: (bbs scanAll: 'invite') isEmpty message: 'invite consumed'.
    self assert: (bbs scanAll: 'identity') size = 1 message: 'identity registered'
  ]

  method: testDoubleClaimFails [
    | bbs store inv first second |
    bbs := BBS new.
    store := InviteStore new bbs: bbs.
    inv := store create: 'bob' ttl: 60 createdBy: 'alice'.
    first := store claim: 'bob' token: (inv at: 'token') pubkey: 'aa'.
    second := store claim: 'bob' token: (inv at: 'token') pubkey: 'bb'.
    self assert: first notNil message: 'first claim ok'.
    self assert: second isNil message: 'second claim rejected'
  ]

  method: testExpiredClaimFails [
    | bbs store inv result |
    bbs := BBS new.
    store := InviteStore new bbs: bbs.
    inv := store create: 'bob' ttl: 0 createdBy: 'alice'.
    "ttl:0 means already expired at creation time."
    result := store claim: 'bob' token: (inv at: 'token') pubkey: 'cc'.
    self assert: result isNil message: 'expired claim rejected'
  ]

  method: report [
    ('PASSED: ', passed printString) println.
    ('FAILED: ', failed printString) println.
    ^failed = 0
  ]
```

- [ ] **Step 3: Verify failure**

```bash
rm -f pp-test-invite && mag build -o pp-test-invite test/test_invite_store.mag && ./pp-test-invite
```

Expected: class not defined.

- [ ] **Step 4: Implement InviteStore**

Create `src/api/InviteStore.mag`:

```smalltalk
"InviteStore — ephemeral invite tokens for onboarding a teammate.

 An invite is a pinned BBS tuple in category 'invite' with payload:
   { name, token, expires_at, created_by }
 Creating an invite generates a random 32-byte hex token. Claiming the
 invite consumes the tuple and seeds an 'identity' tuple in its place
 with the invitee's pubkey.

 Tokens are plaintext — this is not a cryptographic channel. The
 confidentiality property comes from the admin sharing the token
 out-of-band (Slack, Signal, etc). TTL defaults to 10 minutes."

InviteStore subclass: Object
  instanceVars: bbs

  classMethod: new [
    ^super new
  ]

  method: bbs: aBBS [bbs := aBBS. ^self]

  method: randomToken [
    "Generate 32 random bytes, hex-encode. Reuses Identity's RNG via
     the obvious trick: derive a throwaway identity and take its pubHex."
    | throwaway |
    throwaway := Identity generate.
    ^throwaway pubHex
  ]

  method: create: name ttl: ttlSeconds createdBy: adminName [
    | token now expires payload |
    token := self randomToken.
    now := DateTime now epochSeconds.
    expires := now + ttlSeconds.
    payload := Dictionary new.
    payload at: 'name' put: name.
    payload at: 'token' put: token.
    payload at: 'expires_at' put: expires.
    payload at: 'created_by' put: adminName.
    bbs upsertPinned: 'invite'
        scope: '/'
        identity: 'invite:', name
        payload: payload
        actor: nil.
    ^payload
  ]

  method: claim: name token: presented pubkey: pubkey [
    "Consume the invite tuple, register the identity, return the new
     identity payload. Returns nil on token mismatch / missing / expired."
    | tuple payload storedToken expires now idPayload |
    tuple := bbs rdp: 'invite' scope: '/' identity: 'invite:', name.
    tuple isNil ifTrue: [^nil].
    payload := tuple at: 'payload' ifAbsent: [^nil].
    payload isNil ifTrue: [^nil].
    storedToken := payload at: 'token' ifAbsent: [^nil].
    storedToken = presented ifFalse: [^nil].
    expires := payload at: 'expires_at' ifAbsent: [0].
    now := DateTime now epochSeconds.
    now > expires ifTrue: [^nil].
    "Consume the invite."
    bbs inp: 'invite' scope: '/' identity: 'invite:', name.
    "Seed identity tuple matching handleUserAdd's shape."
    idPayload := Dictionary new.
    idPayload at: 'name' put: name.
    idPayload at: 'pubkey' put: pubkey.
    idPayload at: 'role' put: 'user'.
    bbs upsertPinned: 'identity'
        scope: '/'
        identity: 'user:', name
        payload: idPayload
        actor: nil.
    ^idPayload
  ]
```

Caveat on `randomToken`: using `Identity generate` as an RNG is a hack — it works because Ed25519 keygen pulls from a CSPRNG, but a dedicated primitive would be cleaner. Before finalising, grep `<maggie-repo>/vm/*.go` for `crypto/rand` — if a `Random bytes:` or `Bytes randomHex:` primitive exists, use it.

- [ ] **Step 5: Run test, verify passes**

```bash
rm -f pp-test-invite && mag build -o pp-test-invite test/test_invite_store.mag && ./pp-test-invite
```

Expected: all 4 pass. Fix any tuple-API mismatches (BBS `out:`/`inp:`/`rdp:` exact signatures).

- [ ] **Step 6: Wire routes into Server.mag**

Add instance var for the InviteStore and initialize it after BBS is ready. Register two routes inside `registerRoutes`:

```smalltalk
    "Invite: create is admin-gated; claim is intentionally unsigned."
    self signedPost: '/api/invite/create' fields: #('name') do: [:body :actorCtx |
      | ttl created response |
      actorCtx isAdmin ifFalse: [
        ^self errorResponse: 401 message: 'admin signature required'
      ].
      ttl := body at: 'ttl_seconds' ifAbsent: [600].
      created := inviteStore create: (body at: 'name') ttl: ttl createdBy: actorCtx name.
      response := Dictionary new.
      response at: 'name' put: (created at: 'name').
      response at: 'token' put: (created at: 'token').
      response at: 'expires_at' put: (created at: 'expires_at').
      response at: 'url' put: baseUrl, '/api/invite/claim'.
      self okResponse: response
    ].
    self post: '/api/invite/claim' fields: #('name' 'token' 'pubkey') do: [:body |
      | result response |
      result := inviteStore
        claim: (body at: 'name')
        token: (body at: 'token')
        pubkey: (body at: 'pubkey').
      result isNil ifTrue: [
        ^self errorResponse: 400 message: 'invalid or expired invite'
      ].
      response := Dictionary new.
      response at: 'name' put: (result at: 'name').
      response at: 'status' put: 'registered'.
      self okResponse: response
    ].
```

Add to `instanceVars:` and initialize: check the existing init flow in Server.mag for the right spot.

Also check: Server.mag has a `baseUrl` ivar — if not, construct from port (e.g. `'http://localhost:', port printString`).

Also: the `ActorContext isAdmin` / `name` accessors used in the handler block must exist — check `src/api/ActorContext.mag` before writing the handler. If they don't, use whatever the existing admin-gated handlers do (e.g. `handleUserAdd`'s actor check pattern at `Server.mag:2314`).

- [ ] **Step 7: Build full server, verify nothing regresses**

```bash
rm -f pp && mag build -o pp && codesign -s - pp
./pp
```

Expected: existing commands still work.

- [ ] **Step 8: Commit**

```bash
git add src/api/InviteStore.mag src/api/Server.mag src/bbs/Categories.mag test/test_invite_store.mag
git commit -m "feat(api): invite token flow for onboarding new identities"
```

---

## Task 8: `pp identity invite` and `pp identity accept`

**Files:**
- Modify: `src/cli/CliIdentityUse.mag` (add the two new classes)
- Modify: `src/cli/PP.mag` (dispatch)

- [ ] **Step 1: Extend CliIdentityUse.mag**

Append to `src/cli/CliIdentityUse.mag`:

```smalltalk
CliIdentityInvite subclass: Object

  classMethod: run: args [
    "pp identity invite <name> [--ttl <seconds>]
     Admin-only. POSTs /api/invite/create and prints a one-liner the
     admin can paste into chat for the invitee."
    | name ttl body baseUrl response result cmd rest |
    cmd := Cli::Command named: 'invite' doc: 'Create an invite token for a teammate.'.
    cmd flag: (Cli::Flag name: 'ttl' type: #integer default: 600 doc: 'Seconds until expiry').
    cmd envBinding: (Cli::EnvBinding
      envName: 'PP_URL' setter: #baseUrl: type: #string
      default: 'http://localhost:7777').

    cmd run: [:runArgs |
      | cfg |
      runArgs size < 1 ifTrue: [
        'Usage: pp identity invite <name> [--ttl <seconds>]' println. 1
      ] ifFalse: [
        cfg := PPCliConfig new. cmd applyEnvBindings: cfg.
        name := runArgs at: 0.
        ttl := cmd flagValue: 'ttl'. ttl isNil ifTrue: [ttl := 600].
        body := Dictionary new. body at: 'name' put: name. body at: 'ttl_seconds' put: ttl.
        response := CliUserHelper
          signedAdminPost: cfg path: '/api/invite/create' body: body.
        response = false ifTrue: [1] ifFalse: [
          "signedAdminPost prints the JSON — also print paste-ready line."
          '' println.
          'Share with invitee:' println.
          ('  pp identity accept ', cfg baseUrl, ' --name ', name,
            ' --token <token-from-above>') println.
          0
        ]
      ]
    ].
    rest := args size > 0 ifTrue: [args] ifFalse: [Array new: 0].
    cmd setArgs: rest.
    ^[cmd execute] on: Cli::CliError do: [:e | 1]
  ]


CliIdentityAccept subclass: Object

  classMethod: run: args [
    "pp identity accept <url> --name <name> --token <token>
     Generates a fresh local identity, POSTs it against the invite token.
     On 2xx, the identity is active and the user can immediately pp observe."
    | cmd rest |
    cmd := Cli::Command named: 'accept' doc: 'Accept an invite and register locally.'.
    cmd flag: (Cli::Flag name: 'name' type: #string default: '' doc: 'Identity name').
    cmd flag: (Cli::Flag name: 'token' type: #string default: '' doc: 'Invite token').

    cmd run: [:runArgs |
      | url name token rec ident body jsonBody response result client |
      runArgs size < 1 ifTrue: [
        'Usage: pp identity accept <url> --name <name> --token <token>' println. 1
      ] ifFalse: [
        url := runArgs at: 0.
        name := cmd flagValue: 'name'.
        token := cmd flagValue: 'token'.
        ((name isEmpty) or: [token isEmpty]) ifTrue: [
          'Error: --name and --token are required.' println. 1
        ] ifFalse: [
          "Generate local identity."
          (IdentityStore init: name force: false) ifFalse: [1] ifTrue: [
            rec := IdentityStore load: name.
            ident := rec at: 'identity'.
            body := Dictionary new.
            body at: 'name' put: name.
            body at: 'token' put: token.
            body at: 'pubkey' put: ident pubHex.
            jsonBody := Json encode: body.
            client := HttpClient new.
            response := nil.
            [response := client post: url, '/api/invite/claim'
                          body: jsonBody
                          contentType: 'application/json']
              on: Exception do: [:e |
                ('Error: ', e messageText) println. ^1].
            [result := Json decode: response] on: Exception do: [:e |
              ('Error decoding: ', e messageText) println. ^1].
            (result at: 'error' ifAbsent: [nil]) notNil ifTrue: [
              ('Rejected: ', (result at: 'error')) println.
              "Roll back local identity so retry is clean."
              File delete: (IdentityStore keyPath: name).
              File delete: (IdentityStore pubPath: name).
              1
            ] ifFalse: [
              "Remember the server URL in identity config? For now just print."
              IdentityStore setCurrent: name.
              ('Accepted. Active identity: ', name) println.
              ('Server: ', url) println.
              0
            ]
          ]
        ]
      ]
    ].
    rest := args size > 0 ifTrue: [args] ifFalse: [Array new: 0].
    cmd setArgs: rest.
    ^[cmd execute] on: Cli::CliError do: [:e | 1]
  ]
```

- [ ] **Step 2: Wire into PP.mag**

In `cmdIdentity:`:

```smalltalk
    sub = 'invite' ifTrue: [^CliIdentityInvite run: rest].
    sub = 'accept' ifTrue: [^CliIdentityAccept run: rest].
```

- [ ] **Step 3: End-to-end smoke test**

```bash
rm -f pp && mag build -o pp && codesign -s - pp
# Terminal 1: start server (auto-bootstraps 'local')
./pp serve &
sleep 2
# Create an invite
./pp identity invite alice --ttl 600
# Read the token from output, then claim in another tmpHome:
TOKEN=<copy-from-output>
HOME=/tmp/pp-accept-test ./pp identity accept http://localhost:7777 --name alice --token $TOKEN
HOME=/tmp/pp-accept-test ./pp whoami
HOME=/tmp/pp-accept-test ./pp observe alice "hello from invitee"
kill %1
rm -rf /tmp/pp-accept-test
```

Expected: whoami prints alice; observe returns a tuple JSON.

- [ ] **Step 4: Commit**

```bash
git add src/cli/CliIdentityUse.mag src/cli/PP.mag
git commit -m "feat(cli): pp identity invite/accept for zero-hex-pastes onboarding"
```

---

## Task 9: README rewrite

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Replace the Quick Start section**

In `README.md`, replace lines 14–33 with:

```markdown
## Quick Start

```bash
# Build
mag build -o pp && codesign -s - pp

# Start the server (auto-creates a 'local' identity on first run)
./pp serve
```

That's it for solo use. In another terminal:

```bash
./pp repo add /path/to/repo --name my-repo
./pp workflow story --param description="Add error handling to login" --repo my-repo
./pp workflow status
./pp log
./pp dashboard
```

### Inviting teammates (multiplayer)

Every mutating request is signed by the caller's Ed25519 identity. To onboard a teammate:

```bash
# Admin (you)
./pp identity invite alice --ttl 600
# Share the printed command with Alice over chat

# Alice
pp identity accept http://<your-host>:7777 --name alice --token <token>
pp whoami
pp observe alice "I'm on the system"
```

### Managing identities

```bash
pp whoami                       # Show the active identity
pp identity list                # List local keypairs
pp identity use <name>          # Switch the active identity
pp -i <name> <command>          # One-shot per-command override
PP_IDENTITY=<name> <command>    # Env override
pp identity rotate <name>       # Rotate a keypair (signed by old key)
```

### Re-initialising from scratch

```bash
rm -rf ~/.config/pp ~/.pp    # CAUTION: deletes all identities + BBS state
pp init <your-name>          # Optional: choose a custom admin name
pp serve
```

If you never run `pp init`, `pp serve` auto-bootstraps an identity named `local`.
```

- [ ] **Step 2: Update the CLI Commands section**

Replace lines 64–84 with an expanded list including `init`, `whoami`, and `identity use/invite/accept`.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: rewrite README Quick Start around solo-by-default + invite flow"
```

---

## Task 10: Wire integration tests into the aggregate runner

**Files:**
- Create: `test/run_integration.sh` (calls all the shell integration tests) OR extend an existing entry point.
- Modify: `test/MultiplayerTestMain.mag` (register `TestInviteStore` — this is the one Maggie-level test class we DO write, since it doesn't touch `$HOME`).

- [ ] **Step 1: Register `TestInviteStore` in the aggregate runner**

```bash
grep -n "TestIdentityStore\|TestSignatureVerifier" ./test/MultiplayerTestMain.mag
```

Add `TestInviteStore` following the established pattern.

- [ ] **Step 2: Build the aggregate runner and verify**

```bash
rm -f pp-test-multi && mag build -o pp-test-multi test/MultiplayerTestMain.mag && ./pp-test-multi
```

Expected: all tests pass including TestInviteStore.

- [ ] **Step 3: Create a shell runner for the integration tests**

Create `test/run_integration.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
echo "=== identity switch ==="
./test/test_identity_switch.sh
echo "=== auto bootstrap ==="
./test/test_auto_bootstrap.sh
echo "=== invite end-to-end ==="
./test/test_invite_e2e.sh
echo "All integration tests passed."
```

```bash
chmod +x test/run_integration.sh
./test/run_integration.sh
```

Note: `test/test_invite_e2e.sh` was written during Task 8 smoke-testing — if it doesn't exist yet, skip that line for now.

- [ ] **Step 4: Commit**

```bash
git add test/MultiplayerTestMain.mag test/run_integration.sh
git commit -m "test: wire TestInviteStore + integration runner"
```

---

## Self-Review

**Spec coverage:**
- ✅ Auto-bootstrap on first `pp serve` — Task 5
- ✅ `pp init` — Task 6
- ✅ `pp whoami` — Task 2
- ✅ `pp identity use` — Task 3
- ✅ `-i`/`PP_IDENTITY` — Task 4
- ✅ `pp identity invite`/`accept` — Tasks 7–8
- ✅ README rewrite — Task 9

**Risk areas / assumptions to verify during execution:**
1. `Toml encode:` round-trip fidelity — the decoder expects `[admin].admins = [...]`; confirm encoder produces that shape, not inline tables.
2. `BBS out:/inp:/rdp:` exact signatures vs. Tuple factory — the examples above may need adaptation to the actual API. Use existing `/api/user/add` handler as ground truth.
3. `Identity generate` for invite token randomness — replace with a proper RNG primitive if one exists.
4. `ApiServer new bbs: port:` constructor shape in the bootstrap test — check actual init signature.
5. Error classes / `ActorContext isAdmin` / `name` — confirm these exist before Task 7 step 6.
6. Maggie `System setenv:value:` — if this doesn't exist, tests may need a different isolation strategy (e.g., running in a subprocess with `HOME=` prefix).
