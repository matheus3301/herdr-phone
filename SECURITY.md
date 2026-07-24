# Security Policy

## This tool grants remote shell-equivalent access

**Read this first.** Herdr Phone is not a read-only dashboard. It puts a fully
interactive terminal for your Herdr session onto the public internet and brokers
keystrokes into live panes. Anyone who can both reach the front door *and* clear
its authentication can run arbitrary commands as your macOS user, exactly as if
they had an SSH session to your Mac.

Treat the public URL, your Cloudflare Access identity, and the pairing link with
the same care you would treat a root login. The plugin's security bar is that of
an SSH client, not a monitoring page. Every control below exists to keep the
door closed by default and to make an authorized session the only way in.

## Supported Versions

The latest tagged release receives security fixes. This is a pre-1.0 project, so
only the most recent minor version is supported.

| Version | Supported |
| ------- | --------- |
| 0.1.x   | ✅        |

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
  Access adds a signed `Cf-Access-Jwt-Assertion` header at the edge.
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

### Application: pairing and session

- Each daemon instance mints a 256-bit single-use pairing secret. The setup link
  carries it in the URL **fragment** (`#pair=…`), which browsers never send in
  HTTP requests; the app strips it from history before pairing.
- Pairing is constant-time compared and single-use; success rotates the secret
  and sets an opaque, HttpOnly, Secure, `SameSite=Strict` `__Host-` session
  cookie. Sessions live only in daemon memory and expire at the earlier of the
  configured TTL and the verified Access JWT expiry.
- All routes pass through one central middleware enforcing, in order: Host
  allowlist, Access JWT (named mode), session cookie, exact Origin allowlist on
  every WebSocket and mutating request, Go `http.CrossOriginProtection` plus a
  CSRF token, and method/content-type/body-size/rate limits.
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
   stopping the daemon (step 1) already invalidates every session and CSRF
   token. Rotating the pairing secret alone does **not** end existing sessions —
   restart is what clears them. To hand out a fresh, single-use pairing link
   without a full restart, run:

   ```sh
   herdr-phone setup-link
   ```

3. **Revoke Cloudflare Access sessions (named mode).** In the Cloudflare
   Zero Trust dashboard, revoke the user's Access sessions (or tighten the
   Access policy). Because the origin re-validates the JWT on every request and
   reconnect, a revoked Access session cannot reconnect after its token expires.

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
(named mode) or valid session; any secret appearing in argv, logs, state,
browser storage, or git; an escape sequence reaching the browser or a log
unfiltered; a mutation executing without its required confirmation nonce or
after a lifecycle-generation change; a run read succeeding without a matching
lifecycle generation, or agent output reaching a log, an audit record, or disk; a
reused `request_id` replaying a response for a different payload; or a way to bind
the origin off loopback.
