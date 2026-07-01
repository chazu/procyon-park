# Scout findings: web-dashboard mission approval returns 403 "loopback only"

**Date:** 2026-06-30
**Scout mission:** root-cause `POST /api/mission/approve` (and `/reject`) returning
`403 "mission approval is restricted to the local dashboard (loopback only)"` from the
web dashboard.
**Status:** read-only investigation. No source modified. Recommendations only.

---

## 1. Exact code path (verified, with citations)

- `src/api/Server.mag:375-376` — routes `POST /api/mission/approve|reject` to
  `handleMissionDecisionRoute:to:`. These are registered via the **raw**
  `httpServer route:method:handler:`, i.e. **NOT** through `signedPost:` — so there is
  no ed25519 signature check on these endpoints at all.
- `src/api/Server.mag:2504-2508` — `handleMissionDecisionRoute:to:` reads
  `host := req header: 'Host'` and, `(self isLoopbackHost: host) ifFalse: [ ^403 ]`.
- `src/api/Server.mag:2599-2622` — `isLoopbackHost:` is the gate. It lowercases/trims the
  **client-supplied `Host` header** and returns true only for:
  - `host = 'localhost'`
  - `host startsWith: 'localhost:'`
  - `host startsWith: '127.'`
  - `host = '::1'`
  - `host startsWith: '::1:'`
  - `host startsWith: '[::1]'`
  otherwise **false**.
- Same gate protects `POST /api/workitem/cancel` (`src/api/Server.mag:2473-2476`).
- Browser side: `static/dashboard.html:1524-1541` (`postMissionDecision`) does
  `fetch('/api/mission/' + action + '?' + params, { method:'POST', credentials:'same-origin' })`
  — **relative URL, no explicit Host, no signature headers**. The browser fills the
  `Host` header from **whatever authority is in the address bar** (`window.location.host`).

The method comment (`Server.mag:2606-2610`) already flags this as a spoofable,
best-effort stopgap and names the follow-ups: a `remoteAddr` primitive and/or
browser request signing.

---

## 2. WHY the operator's browser hits the guard

The `Host` header is **exactly** the authority the operator typed into the browser
(or that a bookmark/redirect produced). `isLoopbackHost:` passes **only** if that authority
is a literal loopback spelling. Two distinct root causes produce the 403:

### 2a. POLICY case — server binds all interfaces, operator reaches it by non-loopback authority

`pp serve` creates the listener with `httpServer := HttpServer new: port`
(`src/api/Server.mag:23`) and starts it with `httpServer start` (`Server.mag:175`).
**Nothing in the pp codebase passes a bind address** — there is no `127.0.0.1` / bind-host
option anywhere in `src/` or in the `pp serve` flag surface (`src/Main.mag:157-205`;
only `--port` exists). The pre-flight check `checkPortFree:` probes with
`lsof -nP -iTCP:<port> -sTCP:LISTEN` (`src/Main.mag:278`), i.e. it treats the port as
bound on **all** interfaces.

The Maggie `HttpServer` primitive lives in the VM (compiled `alto` binary, not in this
repo's Go tree — only 35 dorado/display `.go` files are present), so the bind address
could not be confirmed from source in this workspace. **Strongly-supported hypothesis:**
the primitive uses Go `http.Server` with addr `":<port>"`, which listens on `0.0.0.0`
(all interfaces). This is the Go default and is consistent with the absence of any
bind-host knob in pp.

**Consequence:** the dashboard is reachable — and is being reached — via a non-loopback
authority, so the `Host` header is *legitimately* non-loopback. Any of these produce a 403:

- **LAN IP**: `http://192.168.x.x:7777/` → `Host: 192.168.x.x:7777`
- **machine hostname / mDNS**: `http://mymac.local:7777/` → `Host: mymac.local:7777`
- **reverse proxy / tunnel** (nginx, Caddy, Tailscale Funnel, ngrok, Cloudflare Tunnel):
  the proxy forwards its own front-end `Host` (e.g. `pp.example.com`) unless explicitly
  rewritten to `localhost`. The pp server reads `Host`, never `X-Forwarded-Host`, so a
  proxied request is non-loopback.

If the operator is genuinely on another machine / behind a proxy, **the guard is doing
its job** — see §4. The fix there is to give them an *authenticated* path, not to widen
the host allowlist.

### 2b. BUG case — genuine local access via a loopback-equivalent authority the allowlist misses

Even when the operator is physically **on the serving machine**, the browser frequently
sends an authority that is loopback-equivalent but not in the allowlist. These are
**genuine misfires** (see §3 detection gaps). The most common:

- Operator opens `http://0.0.0.0:7777/` (the address printed by many "listening on"
  banners; on macOS/Linux 0.0.0.0 routes to the local host) → `Host: 0.0.0.0:7777` →
  **not matched** → 403.
- Operator opens `http://<hostname>.local:7777/` or the bare machine hostname, which
  resolves to `127.0.0.1` via `/etc/hosts` / mDNS → **not matched** (no DNS resolution
  is done) → 403.

---

## 3. Detection gaps for GENUINE local access (`isLoopbackHost:` under-matches)

`isLoopbackHost:` is a string-prefix allowlist over the Host authority. It is both
**over-permissive** (spoofable — accepts `Host: 127.evil.com` because of the bare
`startsWith: '127.'`) and **under-permissive** for real loopback access. Concrete misses:

| Host header (genuine local) | Loopback-equivalent? | Matched? | Gap |
|---|---|---|---|
| `0.0.0.0:7777` | yes (local machine) | **no** | not in allowlist |
| `mymac.local:7777` | yes (mDNS → 127.0.0.1) | **no** | no DNS resolution |
| `<bare-hostname>:7777` | often (→127.0.0.1 in /etc/hosts) | **no** | no DNS resolution |
| `localhost.` (trailing-dot FQDN, RFC-valid) | yes | **no** | `= 'localhost'` and `startsWith 'localhost:'` both fail |
| `[::ffff:127.0.0.1]:7777` (IPv4-mapped IPv6 loopback) | yes | **no** | only `[::1]` prefix handled |
| `[::1]:7777` | yes | yes (`startsWith '[::1]'`) | ok |
| `127.0.0.1:7777` / `127.x.x.x` | yes | yes (`startsWith '127.'`) | ok (but also over-matches `127.evil.com`) |

Case/whitespace are handled (`asLowercase trimBoth`, `Server.mag:2613`), so those are
**not** a source of misfire. IPv6 bracket+port `[::1]:7777` **is** matched. The real
gaps are `0.0.0.0`, hostname/`*.local` (needs resolution the guard never does), the
trailing-dot FQDN, and IPv4-mapped IPv6.

---

## 4. POLICY vs BUG — how to tell them apart

- **BUG (guard misfires on genuine loopback):** operator is on the serving machine but
  reached the UI via `0.0.0.0`, the machine hostname, `*.local`, `localhost.`, or an
  IPv4-mapped IPv6 loopback. The request never left the box; the host-string heuristic
  simply can't recognize the spelling. Fixable by broadening the *equivalence test* —
  but broadening a **string** test still can't distinguish local-hostname from
  remote-hostname without resolving it, and resolving a client-supplied name is itself
  spoofable.
- **POLICY (guard doing its job):** operator is on a different machine (LAN IP, real
  hostname from elsewhere) or behind a proxy/tunnel. Host is legitimately non-loopback.
  Here the correct answer is **authentication**, not host-allowlisting: the endpoint is a
  mutating plan-gate flip and must not be openable to any LAN peer just because they can
  reach the port.

**Core defect:** the `Host` header is neither necessary nor sufficient for "is this the
trusted local operator." It is spoofable (a remote attacker can send
`Host: localhost` and pass) **and** incomplete (a real local operator using `0.0.0.0`
or a hostname fails). The header cannot carry an authorization decision.

---

## 5. Options (each preserves an authorization boundary; none merely removes the guard)

### (a) Configurable host allowlist, env-/config-gated — e.g. `PP_ALLOW_APPROVE_HOSTS`
- **Touches:** `src/api/Server.mag` (`isLoopbackHost:` reads an extra allowlist from env
  or `loadSecurityConfig`/`server.toml`, `Server.mag:46-54`); docs; optionally
  `src/Main.mag` help text.
- **How:** operator opts in their access authority(s), e.g.
  `PP_ALLOW_APPROVE_HOSTS=mymac.local:7777,192.168.1.50:7777`.
- **Security tradeoff:** still **Host-header-based → still spoofable**. Any client that
  can reach the port can send a matching `Host` and pass. It converts a hard-coded
  allowlist into an operator-managed one (fixes the *usability* misfire in §2b/§3) but
  does **not** add a real authorization boundary. Acceptable only as a stopgab paired
  with network-level trust (bind to loopback, or firewalled LAN). Low effort.

### (b) Real peer-address check via a new Maggie `remoteAddr` primitive
- **Touches:** Maggie VM (Go) — add `HttpRequest>>remoteAddr` exposing the TCP peer IP
  (out of this repo; requires a VM change + rebuild); then `src/api/Server.mag`
  (`isLoopbackHost:` → check the *socket* peer address `127.0.0.1`/`::1`, not the header).
- **Security tradeoff:** **Not spoofable** for the local-vs-remote question — the peer
  socket address is set by the kernel, not the client. Correctly and robustly answers
  "did this connection originate on this machine." **Caveat:** behind a reverse proxy the
  peer is the proxy (always loopback if co-located), so it authenticates the *proxy*, not
  the end user — proxied multi-user deployments still need per-user auth. Also does not by
  itself authenticate *which* local user (any local process could POST). Best *networking*
  answer; medium effort (blocked on a VM primitive, the comment's named follow-up).

### (c) Signed browser approve requests (reuse the CLI's ed25519 path)
- **Touches:** `static/dashboard.html` (compute `POST\npath\nts\nsha256(body)` canonical
  bytes and set `X-PP-Actor`/`X-PP-Timestamp`/`X-PP-Signature`, mirroring
  `src/cli/CLIBase.mag:162-188`); `src/api/Server.mag` (re-register
  `/api/mission/approve|reject` through `signedPost:fields:do:` like every other mutating
  route, `Server.mag:299` etc., replacing the loopback gate); key-provisioning path for
  the browser.
- **Security tradeoff:** **Strongest** — approval becomes *authenticated and
  authorized* (admin/registered-identity signature verified by `SignatureVerifier`,
  `src/api/SignatureVerifier.mag`), independent of network location. Works correctly
  through proxies/tunnels and for remote operators. **Big caveat / cost:** the browser
  today holds **no ed25519 key** — the dashboard is served over plain HTTP, `fetch`es use
  only `credentials:'same-origin'`, and even the SSE stream (`/api/sse/dashboard`,
  `dashboard.html:1499`) is not signed from the browser (EventSource can't set headers).
  Delivering a signing key into the browser safely (WebCrypto keygen + one-time
  registration, or a localhost signing helper) is a real design effort and the highest
  implementation cost. This is the correct long-term destination.

### (d) Explicit opt-in flag to allow non-loopback approval (trusted single-operator)
- **Touches:** `src/api/Server.mag` (`loadSecurityConfig` + gate: if
  `[security].allow_remote_approve = true` / `PP_ALLOW_REMOTE_APPROVE=1`, skip the
  loopback check); docs.
- **Security tradeoff:** a **blunt** switch — when on, the mutating endpoint is open to
  **anyone who can reach the port** with no auth at all. Only safe when the port is
  otherwise protected (bound to a trusted interface, VPN/Tailscale ACL, firewall). Fixes
  the misfire trivially but **removes the boundary** in the on-position; must be
  off-by-default and loudly documented. Lowest effort, highest foot-gun.

---

## 6. Recommendation

**Short term (ship now):** combine **(a)** and a corrected equivalence test, keeping the
loopback default. Specifically:
1. In `isLoopbackHost:` also treat `0.0.0.0[:port]`, `localhost.` (trailing dot), and
   `[::ffff:127.0.0.1][:port]` as loopback, and tighten the `127.` check to require a
   digit/`:`/end after the dot so `127.evil.com` no longer passes. This closes the §3
   genuine-local misfires **without** widening to arbitrary hostnames.
2. Add an **opt-in, config-gated** `PP_ALLOW_APPROVE_HOSTS` / `server.toml`
   `[security].approve_hosts` allowlist so an operator who reaches the dashboard by a
   fixed hostname/LAN IP can enumerate it explicitly. Document plainly that this is
   Host-based and therefore only a convenience over a network-trusted deployment, not an
   auth boundary.

**Correct long term (do this to earn a real boundary):** **(c) signed browser approve
requests**, re-registering `/api/mission/approve|reject` through the existing
`signedPost:fields:do:` machinery so approval is *authenticated* (ed25519 via
`SignatureVerifier`) rather than *host-gated*. This is the only option that preserves a
genuine authorization boundary for a mutating plan-gate flip across proxies, tunnels, and
remote operators. Pair with **(b)** (`remoteAddr` peer check) if/when the VM primitive
lands, as defense-in-depth for the "is this even local" question.

**Explicitly do NOT** ship **(d)** alone or any change that removes the guard without
substituting authentication — that turns a spoofable-but-narrow gate into an open
mutation endpoint on every interface the server binds.

### Why not "just widen the allowlist and be done"
Because the endpoint **mutates** the plan gate (`handleMissionDecisionScope:...`,
`Server.mag:2534-2538` flips `mission.status` awaiting-approval → approved/rejected via
`bbs update:`). Host-string widening (a/d) improves usability but leaves the endpoint
authorizable by anyone who can reach the port. Only signing (c) or a peer-address check
(b) restores a boundary that a malicious LAN peer cannot trivially satisfy.

---

## 7. Appendix — key citations

- `src/api/Server.mag:375-376` — approve/reject routes registered raw (unsigned).
- `src/api/Server.mag:2497-2516` — `handleMissionDecisionRoute:to:` (Host read + 403).
- `src/api/Server.mag:2518-2545` — `handleMissionDecisionScope:...` (the mutation).
- `src/api/Server.mag:2599-2622` — `isLoopbackHost:` (the gate + allowlist).
- `src/api/Server.mag:2465-2482` — sibling `/api/workitem/cancel` same gate.
- `src/api/Server.mag:23, 175` — `HttpServer new:` / `start`, no bind address passed.
- `src/Main.mag:157-205, 272-289` — `pp serve` flags (`--port` only), `checkPortFree:`.
- `static/dashboard.html:1524-1541` — browser approve fetch (relative URL, no signing).
- `static/dashboard.html:1499` — SSE `EventSource('/api/sse/dashboard')`, unsigned.
- `src/cli/CLIBase.mag:162-188` — ed25519 canonical bytes + `X-PP-*` headers (reuse for c).
- `src/api/SignatureVerifier.mag:6-8, 42-152` — verifier that (c) would route through.
- `src/api/Server.mag:46-54` — `loadSecurityConfig` / `server.toml` (home for (a)/(d) config).
