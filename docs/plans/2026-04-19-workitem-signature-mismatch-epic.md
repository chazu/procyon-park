# Epic: Fix workitem CLI signature mismatch (admin blocked from creating workitems)

Date: 2026-04-19
Priority: **P0 — user is blocked from filing workitems today**
Related areas: `src/cli/WorkItemCLI.mag`, `src/cli/CLIBase.mag`, `src/cli/CliPP.mag`, `src/api/Server.mag`, `src/api/SignatureVerifier.mag`

---

## Summary

`pp workitem create` (and every other mutating `pp workitem …` subcommand) fails with
`{"error": "Signature verification failed"}` for the admin identity — and in fact for
**every** identity — whenever the server has `enforce_signatures=true`. Commands like
`pp observe`, `pp decide`, `pp workflow start` work fine for the same admin, same scope,
same keypair.

### Why — root cause (confirmed by reading source)

`WorkItemCLI` is declared as `WorkItemCLI subclass: Object` (see `src/cli/WorkItemCLI.mag:4`)
and carries **its own** private `post:body:` method at `src/cli/WorkItemCLI.mag:789`:

```smalltalk
method: post: path body: dict [
  "POST JSON and print response."
  | url jsonBody response result |
  url := baseUrl, path.
  jsonBody := Json encode: dict.
  [
    response := Shell capture: 'curl -s -X POST "', url, '" -H "Content-Type: application/json" -d ''', jsonBody, ''''.
    ...
```

Note: `Shell capture: 'curl …'` with **zero** `X-PP-Actor` / `X-PP-Timestamp` /
`X-PP-Signature` headers. The server's `signedRouteOnly:` handler
(`src/api/Server.mag:1398`) sees no signature, `actorCtx := nil`, and when
`enforceSignatures` is true it returns 401 `"Signature verification failed"`.

Contrast with the working path — `CliPP class>>postAndPrint:cfg:path:body:`
(`src/cli/CliPP.mag:1241`) routes through `signedPost:path:body:cfg:` →
`CLIBase signedPost:body:baseUrl:` (`src/cli/CLIBase.mag:31`), which:

1. loads the active identity via `IdentityStore loadCurrent`,
2. canonicalises `POST\n<path>\n<ts>\nsha256(body)`,
3. signs with the identity's Ed25519 key, and
4. attaches `X-PP-Actor`, `X-PP-Timestamp`, `X-PP-Signature` hex-encoded.

**The admin identity is fine. The scope auth is fine. The server already accepts
admin signatures on workitem routes** (`handleWorkitemCreate:actor:` does no extra
per-scope-user check — it just trusts `actorCtx`). The CLI simply never gets a
chance to prove who it is because `WorkItemCLI>>post:body:` strips all headers.

### Secondary obstacle

When the admin tried to work around this by registering themselves as a scope
user (`pp user add chazu --pubkey …`), they hit a different error:
`identity already registered for chazu` (`src/api/Server.mag:2411`). That rejection
is correct for non-admin replays, but it removes the self-rescue escape hatch
admins would normally reach for. Once the CLI-signing bug is fixed, admins
don't need to register themselves at all — admin signatures already pass
`SignatureVerifier doVerify:` (admin branch, `src/api/SignatureVerifier.mag:77-82`).

### Tertiary UX bug

`WorkItemCLI>>post:body:` also does not route through `CLIBase>>handleResponse:path:`,
so server errors bypass `printAuthHint:` (`src/cli/CLIBase.mag:242`). The user sees
the raw JSON error but the CLI *always exits 0* because nothing returns a non-zero
status. When `PP_SCOPE` is unset or the body is somehow dropped, the command can
print nothing visible and still exit 0 — a silent success for a failed write.

---

## User Stories

### Story 1 — Admin can create workitems again (root-cause fix)

**As** an admin onboarding a new repo,
**I want** `PP_SCOPE=mu pp workitem create mu-cache-key --title X --type story --repo mu --estimate small` to succeed,
**So that** I can file the first workitem for a brand-new scope without pre-registering myself as a scope user.

**Acceptance criteria:**
- `WorkItemCLI` no longer owns a curl-based `post:body:`. Every mutating call
  (`cmdCreate:`, `cmdUpdate:`, `cmdComment:`, `cmdSetStatus:`, `cmdBlock:`,
  `cmdRun:`, `cmdRewave:` — i.e. every call site of `self post: '/api/…'`
  currently at lines 116, 287, 504, 526, 551, 578, 597, 619, 641, 729) routes
  through `CLIBase>>signedPost:body:` (or `CliPP class>>postAndPrint:cfg:path:body:`).
- `postAndFormat:path:body:format:` (line 805) — used by `cmdShow:` on `/api/rdp`,
  which is unsigned read — should use `unauthedPost:` so we keep a single curl-free
  code path.
- `pp workitem create` succeeds under `enforce_signatures=true` when the caller is
  the admin (signature verifies via admin branch).
- `pp workitem create` succeeds when the caller is a registered scope user.
- `pp workitem update|comment|ready|done|block|run|rewave` likewise succeed.

### Story 2 — Regression test: admin workitem create in a fresh scope

**As** a PP maintainer,
**I want** a test that proves admin can create a workitem in a scope with no
registered users,
**So that** we never silently regress the admin-onboarding path.

**Acceptance criteria:**
- New test under `test/cli/` (or wherever `test/` hosts CLI+server integration
  tests — see existing patterns) that:
  1. Boots a server with `enforce_signatures=true`, one admin pubkey.
  2. Runs `pp workitem create` with the admin identity against a fresh scope
     name that has **no** registered users.
  3. Asserts HTTP 200, tuple persisted, actor stamped as `admin:<hex>`.
  4. Runs `pp workitem update` / `comment` / `ready` on the same item with the
     same admin identity and asserts success.
- Test also exercises the scope-user path: register a user, flip identity,
  repeat create — assert 200.
- Test must be runnable via `mag test` following the project's test-dir pattern
  (subdirectories must be listed explicitly — see `feedback_maggie_test_dirs`).

### Story 3 — CLI reports workitem failures and returns non-zero

**As** an operator running a shell script that calls `pp workitem create`,
**I want** the command to exit non-zero and print a clear message on any
server error or missing prerequisite (no `PP_SCOPE`, empty response, 4xx/5xx),
**So that** CI and wrapper scripts don't silently pass over dropped writes.

**Acceptance criteria:**
- Every `pp workitem …` mutating subcommand returns a non-zero shell exit code
  when the server response carries `error` or the HTTP layer fails.
- Error messages go through `CLIBase>>printAuthHint:path:` so signature /
  revocation / admin-only failures surface operator-friendly hints (this falls
  out of routing through the shared helper).
- `pp workitem create` with no `PP_SCOPE` or an explicitly empty scope prints
  `"Error: no scope set (set PP_SCOPE or pass --repo)"` and exits 1, rather
  than silently completing.
- A test (unit or light integration) covers: 401 response → exit 1, empty
  response → exit 1, missing scope → exit 1.

### Story 4 — Migration / release note

**As** an existing PP user on an older pp CLI,
**I want** a release note explaining why workitem creation was failing and how
to upgrade,
**So that** I know the fix applies to me and I don't need server-side changes.

**Acceptance criteria:**
- `CHANGELOG.md` `[Unreleased]` gets a `### Fixed` entry describing the
  signature mismatch and that the fix is purely client-side.
- Note calls out that admins no longer need to register themselves as scope
  users to write workitems — they never should have needed to.
- Note points at the one server-side edge case still in play: `pp user add
  <name>` where `<name>` already exists still 409s; callers who genuinely need
  to replace a key must pass `replace: true` (already supported by
  `handleUserAdd:`). No server change required.

### Story 5 (optional, recommended follow-up) — Fold `WorkItemCLI` into `CLIBase`

**As** a PP maintainer,
**I want** `WorkItemCLI` to inherit from `CLIBase` (like every other
instance-style CLI class),
**So that** it can't drift away from the canonical HTTP + signing path again.

**Acceptance criteria:**
- Declaration changes to `WorkItemCLI subclass: CLIBase` (check Maggie
  inheritance syntax — existing users of `CLIBase` show the pattern).
- The redundant `initialize`, `baseUrl`, `scope`, `flagValue:in:`, and ad-hoc
  `post:body:` are removed in favour of the inherited ones.
- Existing `WorkItemCLI>>postAndFormat:path:body:format:` stays (it's
  display-only) but its HTTP call moves to `unauthedPost:path:body:`.
- Mark explicitly **optional** for this epic; do not block Story 1 on it.

---

## Acceptance criteria — epic level

1. `PP_SCOPE=mu pp workitem create mu-cache-key --title X --type story --repo mu --estimate small`
   returns HTTP 200 and persists a pinned `workitem` tuple for admin chazu with
   the existing `~/.config/pp/server.toml` admin allowlist — **no other
   registration step required.**
2. All `pp workitem …` mutating subcommands reach the server with
   `X-PP-Actor`/`X-PP-Timestamp`/`X-PP-Signature` headers.
3. A regression test covering admin + fresh scope lives under `test/` and runs
   green in CI.
4. Failed workitem commands exit non-zero and emit hinted error text.
5. No server-side code in `src/api/Server.mag` or
   `src/api/SignatureVerifier.mag` needs to change. If a reviewer disagrees,
   that disagreement is surfaced as an Open Question below rather than
   smuggled into implementation.

---

## Technical context

### Files directly involved

| File | Role in this bug |
|------|------------------|
| `src/cli/WorkItemCLI.mag` | Root cause — owns unsigned curl-based `post:body:` at line 789, used by every mutating subcommand. |
| `src/cli/CLIBase.mag` | Canonical signing helper (`signedPost:body:`, `signedPost:body:baseUrl:`, `printAuthHint:path:`). Target of refactor. |
| `src/cli/CliPP.mag` | Class-side signed POST wrappers (`postAndPrint:cfg:path:body:`, `signedPost:path:body:cfg:`). Alternative target of refactor. |
| `src/api/Server.mag` | Already routes `/api/workitem/*` through `signedRouteOnly:` (line 331-340); admin branch already accepted server-side. Probably **no change**. |
| `src/api/SignatureVerifier.mag` | Handles admin via pubkey-hex actor header; scope-user via `user:<name>` identity lookup. Probably **no change**. |
| `src/identity/IdentityStore.mag` | `loadCurrent` source of the signing keypair — referenced by CLIBase, unchanged. |

### Patterns observed

- Every other CLI class that writes to the server talks through either
  `CLIBase` (instance-side) or `CliPP class` (class-side). `WorkItemCLI` is the
  only outlier; it predates the multiplayer-identity epic that introduced
  signing and was apparently never migrated.
- The server already has a uniform `signedPost:fields:do:` / `signedRouteOnly:do:`
  discipline; there is no "workitem auth is different" reality on the server
  side. The bug is fully client-side.
- `CLIBase>>handleResponse:path:` already knows how to render auth errors
  with operator hints (`printAuthHint:` at line 242) — routing through it
  gets us free UX.

### Server-side confirmation that no backend change is needed

- `handleWorkitemCreate:actor:` (`Server.mag:1984`) does **not** require
  `actorCtx isAdmin not`; it accepts any verified signer.
- `SignatureVerifier>>doVerify:adminList:skewSeconds:requireOldPub:` recognises
  admin by pubkey-hex actor header *before* consulting the per-user identity
  tuple — so admins don't need a `user:chazu` identity tuple to write
  anything, workitems included.

---

## Open questions

1. **Refactor depth for Story 1.** Minimum viable fix: swap the body of
   `WorkItemCLI>>post:body:` to call `CLIBase signedPost:body:baseUrl:` (or
   `CliPP class postAndPrint:cfg:path:body:`). Cleaner: inherit from
   `CLIBase` (Story 5). Recommend shipping the minimum fix under Story 1 and
   deferring Story 5 so we can restore admin workitem writes within one PR.

2. **Admin `X-PP-Actor` form.** `CLIBase signedPost:body:` sends `actorHeader :=
   rec at: 'name'` — a human name. `SignatureVerifier` admin branch compares
   `admins includes: actorHeader`, i.e. expects the hex pubkey when
   matching admin. Need to verify empirically whether the admin's name
   resolves to the hex allowlist entry somewhere, or whether admins must
   explicitly sign with their hex pubkey as actor header. Two paths:
   - (a) If the admin allowlist is keyed by name → no further action.
   - (b) If it's keyed by hex → need a client-side "I am admin, use
     pubkey-as-actor" mode, possibly via `~/.config/pp/identity/current`
     carrying an `is_admin: true` / `admin_pubkey_hex:` flag, or a
     `--as-admin` flag on the CLI. Resolve this before implementation.
     *(Scout note: the CLIBase `printAuthHint:` at line 254 literally says
     "Sign with the hex pubkey listed in ~/.config/pp/server.toml under
     [admin].admins" — suggesting (b) is the expected answer. Implementation
     should either auto-detect admin identity on load and switch the actor
     header, or document the manual invocation.)*

3. **`pp user add chazu --pubkey …` 409.** Current behaviour is correct for
   non-admin callers but is an unhelpful dead-end for admins bootstrapping.
   After Story 1 lands, this becomes a non-issue (admin doesn't need to
   register). Do we leave the 409 as-is, or add `--replace` to the CLI and a
   hint in the rejection message? **Recommend**: leave the server alone, add
   `pp user add … --replace` to the CLI surface and hint the operator in the
   409 response text (purely client-side).

4. **Test harness for Story 2.** Does PP already have an integration-test
   helper that boots `src/api/Server.mag` with a fixed admin key and exercises
   the CLI, or will this be the first such test? If the latter, scope
   increases modestly — the scout did not walk `test/` in detail. Flagging
   for planning to size.

5. **Silent-success detection for missing `PP_SCOPE`.** `CLIBase>>initialize`
   already defaults `scope := 'default'` when `PP_SCOPE` is unset, so the
   literal "scope unset" case becomes "wrote to scope `default`". The user's
   reported silent-0-exit symptom is therefore more likely the curl call
   failing silently (bad shell quoting of `jsonBody`) in
   `WorkItemCLI>>post:body:`. Verify during implementation: the real UX fix
   may be simpler than adding explicit scope checks — routing through
   `CLIBase` alone may resolve both the auth and the silent-exit problems.

---

## Recommended implementation order

1. **Story 1 (blocker lift)** — shortest path: replace
   `WorkItemCLI>>post:body:`'s implementation with a delegation to
   `CLIBase>>signedPost:body:` (needs a CLIBase handle — pass one via
   `new` or construct one lazily). Same for `postAndFormat:` → route the
   HTTP part through `CLIBase>>unauthedPost:path:body:`.
2. **Story 3 (exit codes + hints)** — naturally flows from routing through
   `CLIBase`'s `handleResponse:path:`; additionally plumb a non-zero exit
   through the `WorkItemCLI>>runWith:` dispatcher.
3. **Story 2 (regression test)** — land alongside Stories 1 and 3.
4. **Story 4 (changelog)** — with the PR.
5. **Story 5 (inheritance refactor)** — separate follow-up PR.
