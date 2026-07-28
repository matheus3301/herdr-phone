# Security Policy

## This tool grants remote shell-equivalent access

**Read this first.** Herdr Phone is not a read-only dashboard. It puts a fully
interactive terminal for your Herdr session onto the public internet and brokers
keystrokes into live panes. Anyone who can both reach the front door *and* clear
its authentication can run arbitrary commands as your macOS user, exactly as if
they had an SSH session to your Mac.

Treat the public URL and your Cloudflare Access identity — and, in quick mode, the
pairing link — with the same care you would treat a root login. In named mode,
clearing Cloudflare Access *is* clearing the front door. The plugin's security bar
is that of an SSH client, not a monitoring page. Every control below exists to keep
the door closed by default and to make an authorized session the only way in.

## Supported Versions

The latest tagged release receives security fixes. This is a pre-1.0 project, so
only the most recent minor version is supported.

| Version | Supported |
| ------- | --------- |
| 0.3.x   | ✅        |
| < 0.3   | ❌        |

## Reporting a Vulnerability

Please **do not** open a public issue for security vulnerabilities.

Use GitHub's [private vulnerability reporting][ghsa] on this repository
("Security" tab → "Report a vulnerability"). If that is unavailable, open a
minimal public issue asking a maintainer to open a private advisory, without
including exploit details.

We aim to acknowledge reports within 7 days and to ship a fix or mitigation for
confirmed issues as quickly as is practical.

[ghsa]: https://github.com/matheus3301/herdr-phone/security/advisories/new

## Threat Model and Defenses

The design is documented in depth in
[`docs/research/security.md`](docs/research/security.md). In summary, the
following defenses must all hold; none of them alone is sufficient.

### Front door: Cloudflare tunnel + Access

- The origin binds to **loopback only** (`127.0.0.1`) on a configurable port and
  is reachable from the internet only through an outbound-only Cloudflare Tunnel.
  It is never bound to `0.0.0.0` or a LAN address in production.
- **Named tunnels** sit behind a Cloudflare Access application (deny-by-default).
  Access adds a signed `Cf-Access-Jwt-Assertion` header at the edge, and it is the
  interactive gate: clearing Access is what authorizes an operator. The origin does
  not take the edge's word for it — see the next section.
- **Quick Tunnels** have no Access identity at the edge and are **off by
  default**. They must be explicitly enabled in config and still require app
  pairing. They are for testing only, carry no uptime guarantee, and expose a
  random public hostname; see the README's Quick Tunnel section.

### Origin: independent JWT validation

The edge is only as strong as the origin's refusal to trust forwarded headers.
In named mode the origin **cryptographically validates the Access JWT on every
HTTP request and every WebSocket handshake**:

- Reads only `Cf-Access-Jwt-Assertion` — never the convenience
  `Cf-Access-Authenticated-User-Email` header.
- Fetches the team JWKS over HTTPS, matches the token `kid`, and accepts RS256
  only (no `alg: none`, no algorithm confusion).
- Enforces exact issuer, application audience (AUD), and `exp`/`nbf`/`iat`
  freshness, and, when `allowed_identities` is set, an exact `email` /
  `common_name` match.
- Fails closed when JWKS is unavailable and no valid cached key exists.

Because authorization is re-proven cryptographically on every single request and
handshake — not accepted once at sign-in — Access is strong enough to be the sole
interactive gate in named mode. That is the entire basis for the session model
below.

### Application: sessions differ by mode

An app session is an opaque, HttpOnly, Secure, `SameSite=Strict` `__Host-` cookie
with a per-session CSRF token. **Sessions live only in daemon memory** and expire
at the earlier of the configured TTL and the verified Access JWT expiry. Those
rules are identical however the session was established, and there is no on-disk
or persistent session store.

**Named mode: Cloudflare Access alone is the gate.** A request that clears the
origin's JWT verification but carries no valid session cookie is given a session
bound to the verified Access identity, and no pairing link is involved.

- The Access claims are re-read at provisioning time, so the session is bound to
  the exact identity that authorized that request and its hard expiry is capped at
  that token's `exp`. Any verification failure mints nothing and the request is
  rejected. An identity with neither `email` nor `common_name` is not
  provisionable.
- A live session already bound to the same identity is reused rather than
  duplicated, so a cookie-less client cannot inflate the in-memory session store.
- Provisioning is audited as `session.auto` with the subject and the non-secret
  audit id. The bearer cookie value is never recorded.
- Auto-provisioning grants **no** exemption from any other control: Origin,
  `http.CrossOriginProtection`, CSRF on mutating routes, rate limits, body bounds,
  and deadlines all apply exactly as they do to a paired session. A brand-new
  session cannot mutate until the SPA has read its CSRF token from `GET /session`.
- Trade-off, stated plainly: before v0.3.0 named mode also required an
  out-of-band pairing secret as a second factor. It no longer does. An attacker
  who can fully impersonate an allowed Access identity now reaches the app
  directly, so the strength of your Access policy (SSO, MFA, device posture, a
  tight `allowed_identities`) is the strength of the front door. Harden it
  accordingly.

#### Named mode: Cloudflare Access is the sole session-lifetime authority

Auto-provisioning has a consequence that must not be glossed over. **In named mode
nothing on the app side can end a session, because a new one is provisioned from
the still-valid Access identity on the very next request.**

- **`server.idle_lock` does not re-lock a named-mode session.** The idle session is
  dropped from memory and immediately replaced. No re-authentication is prompted.
- **`server.session_ttl` caps one session, not access.** The replacement session
  starts a fresh TTL, still capped at the Access token's `exp`.
- **The in-app "End session" (`DELETE /session`) does not revoke access in named
  mode.** It revokes that session record and clears the device's cookie; the next
  request re-provisions. Treat it as a device-local sign-out, not a kill switch.
- **Quick mode is unaffected.** There, `idle_lock`, `session_ttl`, and "End session"
  each genuinely lock the operator out, because nothing can re-establish a session
  without the single-use pairing secret.

This is a deliberate, accepted trade: the pairing second factor was removed in
exchange for seamless access, and session lifetime is delegated to Cloudflare
Access along with identity. The practical consequences for an operator:

- **Set the Access application's session duration in Cloudflare Zero Trust to a
  value you would accept as an unattended-terminal window.** It is your idle
  timeout, and it is the only one.
- **To end named-mode access, do one of these** (the same two controls as
  [Immediate Revocation](#immediate-revocation) steps 1 and 3):

  ```sh
  herdr-phone stop        # immediate: drops every in-memory session, kills the tunnel
  ```

  or revoke the user's Access session in the Cloudflare Zero Trust dashboard — or
  remove the identity from the Access policy and from `allowed_identities` — which
  takes effect once the current token expires.

**Quick mode: pairing is mandatory and unchanged.** A Quick Tunnel has no edge
identity, so no session is ever auto-provisioned there.

- Each daemon instance mints a 256-bit single-use pairing secret. The setup link
  carries it in the URL **fragment** (`#pair=…`), which browsers never send in
  HTTP requests; the app strips it from history before pairing.
- Pairing is constant-time compared and single-use; success rotates the secret and
  sets the session cookie. Without it, every session-authenticated route answers
  `401`.

`POST /pair` also stays live in named mode as a re-bind/recovery path, and it is
not a bypass: a named-mode request without a valid Access JWT is rejected before
pairing is considered.

**Every request, in either mode:**

- All routes pass through one central middleware enforcing, in order: Host
  allowlist, Access JWT (named mode), session cookie (auto-provisioned from the
  verified Access identity in named mode; required in quick mode), exact Origin
  allowlist on every WebSocket and mutating request, Go
  `http.CrossOriginProtection` plus a CSRF token, and
  method/content-type/body-size/rate limits.
- A strict Content-Security-Policy serves only self-hosted assets, disallows
  `unsafe-eval` and runtime CDNs, blocks framing, and explicitly allows only the
  same-origin WebSocket.

### Terminal and subprocess hardening

- Structural, destructive actions require a single-use, short-lived server
  confirmation nonce bound to the operation, resource id, lifecycle generation,
  and session. Terminal takeover requires an explicit confirmation.
- Relayed terminal output is filtered for dangerous escape sequences (OSC 52
  clipboard, OSC 8 hyperlinks, title set/report, DCS/APC/PM, device-status and
  answerback queries) before it reaches the browser or any log.
- All subprocesses (`cloudflared`, terminal controllers) run via
  `exec.CommandContext` with a nonzero `WaitDelay` and their own process group;
  no shell is ever invoked.

### Agent runs and observed output

The run API supervises agent runs from the phone. Herdr 0.7.5 supplies no
semantic conversation data, so the relay never manufactures any.

- **Nothing is stored.** No transcript, run state, or agent output is cached,
  persisted, or written to disk. Every run read goes to Herdr and lives only for
  the duration of the response, which is served `Cache-Control: no-store`. Adding
  on-disk transcript storage would require revising this threat model first.
- **No content is logged.** A run read is audited as identity, pane id, outcome,
  and a byte count. Agent output, commands, and titles never enter a log or audit
  record.
- **No inference from bytes.** The relay does not derive message roles, approvals,
  tool calls, diffs, or test results from terminal output, and it does not parse
  agent-specific transcript files. Output is served as one explicitly typed
  `observed_terminal_output` part, and the capability document advertises every
  semantic capability as unsupported so the UI fails closed instead of guessing.
- **Guarded by generation.** A run read requires the canonical `pane_id` and a
  nonzero `expected_generation`, checked before any Herdr call, so a client can
  never read through a recycled pane or a replaced agent. Run identity is bound to
  the pane generation and an agent-incarnation digest; either change invalidates
  the run.
- **Bounded and stripped.** Output is bounded by line count and byte size
  (truncation keeps the most recent tail on a UTF-8 boundary and is reported), and
  every control character except LF and TAB is stripped from it — a repaint
  sequence cannot rewrite what an operator already read in a non-terminal
  renderer. Labels, titles, and paths are folded to a single safe line and
  length-bounded.
- **No session references published.** The agent incarnation is a digest, not the
  raw occupant fingerprint, because Herdr may report an agent session as a
  filesystem path.
- **No new upstream surface.** The run API reads only `pane.read` / `agent.read`
  and the existing snapshot. There is still no generic Herdr RPC and no
  browser-supplied method name.

### Replay and error fidelity

- A `request_id` is client-chosen, so each idempotency entry is bound to a
  fingerprint of the operation, the asserted lifecycle generation, and the
  canonicalized params. A reused id with different content is rejected rather than
  replayed, so it can neither retrieve another payload's response nor obtain a
  cached success for a generation that was never validated.
- Upstream failures keep their distinct meaning (missing resource, unsupported
  feature, conflict, timeout, transport fault) so the client is never told to
  retry something that cannot succeed. Only the upstream *code* crosses the
  boundary; upstream messages are never forwarded, because a Herdr message can
  quote pane content, a path, or a command.

### Secrets

- Tunnel tokens are supplied via `--token-file` / `TUNNEL_TOKEN_FILE` or a
  `token_command`; a token is never placed in argv. Any temporary token file is
  mode `0600` and deleted immediately after `cloudflared` reads it.
- Credential and token files must be regular files owned by you and not readable
  by group or other.
- No secret is written to config, runtime state, logs, the audit trail, browser
  storage, test snapshots, or git. The audit trail records terminal input only
  as a byte count and category, and an observed-output read only as a byte count
  and outcome — never content.

## Immediate Revocation

If you believe a session, device, or tunnel token has been compromised, act in
this order.

1. **Stop the relay immediately.**

   ```sh
   herdr-phone stop
   ```

   This asks the daemon to shut down gracefully over its private control socket:
   it closes WebSockets, releases terminal controllers, and terminates
   `cloudflared`, which tears down the public tunnel. If the CLI is unavailable,
   kill the `herdr-phone serve` process group; the tunnel dies with it.

2. **Invalidate all app sessions.** Sessions live only in daemon memory, so
   stopping the daemon (step 1) already invalidates every session and CSRF token,
   auto-provisioned ones included. Rotating the pairing secret alone does **not**
   end existing sessions — restart is what clears them. To hand out a fresh,
   single-use pairing link without a full restart, run:

   ```sh
   herdr-phone setup-link
   ```

   In **named mode** nothing app-side is sufficient on its own — not this step, not
   `idle_lock`, not the in-app "End session". While the daemon is running and the
   compromised Access identity still holds a valid JWT, the next request simply
   provisions a new session
   ([why](#named-mode-cloudflare-access-is-the-sole-session-lifetime-authority)). Do
   step 3 as well, or leave the daemon stopped until you have.

3. **Revoke Cloudflare Access sessions (named mode).** In the Cloudflare
   Zero Trust dashboard, revoke the user's Access sessions, or remove them from the
   Access policy and from `allowed_identities`. Because the origin re-validates the
   JWT on every request and reconnect, and an auto-provisioned app session is
   capped at that JWT's expiry, a revoked identity loses access once its current
   token expires — and immediately if you also restart the daemon (step 1). In
   named mode this step, not pairing rotation, is the real session revocation.

4. **Rotate the tunnel token (named mode).** In the Cloudflare dashboard, rotate
   the tunnel token, then force-disconnect existing connections. Update your
   `token_file` / `token_command` source and restart:

   ```sh
   herdr-phone start
   ```

   Anyone holding the old token can run the tunnel, so rotation — not just
   restart — is required after suspected token theft.

5. **Abandon a Quick Tunnel.** A Quick Tunnel hostname is disposable. Stopping
   the daemon abandons it permanently; a new run gets a new random hostname.

## Release Integrity and Supply Chain

- **Checksum-verified downloads (mandatory).** When the plugin cannot build from
  source it downloads the release archive and verifies its **SHA-256** against
  the release `checksums.txt` before installing, failing closed on any mismatch.
  It never runs `curl | sh`. The `verify-plugin` script likewise pins and
  checksum-verifies the official Herdr binary it downloads.
- **Build provenance (keyless).** Each release archive and the `checksums.txt`
  carry a GitHub build-provenance attestation signed via Sigstore/OIDC — no
  long-lived signing key exists to steal. Verify an artifact came from this
  repository's release workflow with:

  ```sh
  gh attestation verify <archive.tar.gz> --repo matheus3301/herdr-phone
  ```

- **Pinned, least-privilege CI.** Every GitHub Action is pinned to a full commit
  SHA (with a version comment maintained by Dependabot), `govulncheck` is pinned
  to a released version and run against both source and built binaries, workflows
  default to `contents: read` with write scope confined to the publish job, and
  forks receive no secrets. `go.sum` plus the Go checksum database and the
  committed frontend lockfile pin the dependency tree.
- **Go toolchain floor: 1.26.5+.** CI and the release workflow pin the exact Go
  patch `1.26.5`, and the source-build path in `scripts/build.sh` requires Go
  `1.26.5` or newer (accepting 1.27+ and later majors). Earlier 1.26 patch
  releases contain reachable standard-library vulnerabilities, so an older 1.26.x
  is treated as incompatible and the build falls back to a checksum-verified
  release download rather than compiling against a vulnerable toolchain.

## Reporting Scope

Please treat any of the following as a security vulnerability and report it
privately: a way to reach an authorized control path without a valid Access JWT
(named mode) or valid session; a session auto-provisioned in quick mode, or in
named mode from an unverified, expired, or non-allowlisted identity; an
auto-provisioned session that skips Origin, `CrossOriginProtection`, CSRF, or rate
limiting; any secret appearing in argv, logs, state,
browser storage, or git; an escape sequence reaching the browser or a log
unfiltered; a mutation executing without its required confirmation nonce or
after a lifecycle-generation change; a run read succeeding without a matching
lifecycle generation, or agent output reaching a log, an audit record, or disk; a
reused `request_id` replaying a response for a different payload; or a way to bind
the origin off loopback.

**Known and accepted, so not a vulnerability:** in named mode, access surviving
`server.idle_lock`, `server.session_ttl`, or the in-app "End session", because the
relay re-provisions a session from the still-valid Cloudflare Access identity. This
is the documented posture described in
[Named mode: Cloudflare Access is the sole session-lifetime authority](#named-mode-cloudflare-access-is-the-sole-session-lifetime-authority).
A report that named-mode access continues after an *Access* session revocation, or
after the Access token's `exp`, **is** in scope.
