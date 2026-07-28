# herdr-phone Product and Implementation Specification

Status: implementation contract for v0.3.0

Repository: `https://github.com/matheus3301/herdr-phone`

Date: 2026-07-28

## 1. Mission

Build a public-ready Herdr plugin that lets one authenticated operator use the
Herdr session running on their Mac from a phone. The operator must be able to see
Spaces/workspaces, tabs, panes, worktrees, and agents; open a real interactive
terminal; prompt and control agents; and safely create, rename, move, resize,
zoom, split, and close Herdr resources.

The plugin is a Go 1.26 relay with an embedded React and TypeScript PWA. It starts
and supervises `cloudflared`, supports named and quick tunnels, binds its origin
to loopback only, and never exposes Herdr's local socket directly to a browser.
Named tunnels use Cloudflare Access as the edge identity layer and validate the
Access JWT again at the origin on every request, which is what lets Access alone
be the interactive gate there. Quick tunnels require explicit opt-in and strong
application-level pairing because they have no Access protection at all.

The product is remote shell access by design. Its security bar is therefore the
same as an SSH client, not a read-only dashboard.

## 2. Locked Product Decisions

- Product and plugin name: `herdr-phone` / **Herdr Phone**.
- Plugin id: `matheus3301.phone`.
- Version: `0.3.0`.
- Host platform for v0.3.0: macOS, amd64 and arm64.
- Backend: Go 1.26, pinned in both `go.mod` and `mise.toml`.
- Frontend: React, TypeScript, Vite, Tailwind CSS v4, shadcn/ui primitives, and
  xterm.js, embedded into the Go binary.
- Front doors: Cloudflare named tunnels and explicitly enabled Quick Tunnels.
- Edge auth for named tunnels: Cloudflare Access.
- **App auth in named mode: Access-only.** Cloudflare Access is the sole
  interactive gate. A request that clears the origin's Access JWT verification and
  carries no session cookie is transparently given an HttpOnly app session bound
  to the verified Access identity; no pairing round-trip is required. This is safe
  only because the JWT is re-validated at the origin on *every* request and
  WebSocket handshake (section 9.2), not merely accepted once at the edge.
- **App auth in quick mode: pairing is mandatory.** A quick tunnel has no edge
  identity, so the single-use pairing link followed by an HttpOnly session remains
  the only way in and is unchanged.
- App sessions are in-memory in both modes, whether paired or auto-provisioned,
  with identical lifetime rules. Persistent or on-disk sessions are a non-goal.
- One-step start: `start` prints the URL to open on the phone, and a keybindable
  `toggle` action turns the daemon on and off.
- Herdr scope: one configured/running Herdr session in v0.3.0. Named-session
  aggregation is deliberately deferred to avoid surprising blast radius.
- Remote controls: full safe parity for the operations listed in section 15.
- Distribution: normal Herdr community plugin plus checksum-verified releases.
- License: MIT. Prior AGPL projects are behavioral research only; copy no AGPL
  source.

## 3. Authoritative External Contracts

Implementation must follow these verified contracts as of 2026-07-23.

### 3.1 Herdr v0.7.5

References:

- `https://herdr.dev/docs/plugins/`
- `https://herdr.dev/docs/cli-reference/`
- `https://herdr.dev/docs/socket-api/`
- `https://herdr.dev/docs/agent-automation/`
- `https://herdr.dev/docs/persistence-remote/`
- Installed `herdr 0.7.5`, protocol 17, schema version 1.
- `herdr api schema --json` from the installed binary.

Required contracts:

- A plugin is a manifest plus out-of-process argv commands. There is no plugin
  SDK and no sandbox.
- Plugin processes receive `HERDR_BIN_PATH`, `HERDR_SOCKET_PATH`,
  `HERDR_PLUGIN_CONFIG_DIR`, `HERDR_PLUGIN_STATE_DIR`, and invocation context.
- The raw socket is newline-delimited JSON over a Unix domain socket.
- `session.snapshot` returns the complete topology and agent bootstrap state.
- `events.subscribe` provides lifecycle events on a long-lived connection.
- Herdr IDs are opaque. A cross-workspace pane move may change the pane ID; the
  client must use the returned replacement ID.
- Agent states are `idle`, `working`, `blocked`, `done`, and `unknown`.
- `herdr terminal session control <target> [--takeover] [--cols N] [--rows N]`
  is the supported interactive bridge. It emits newline-delimited records:

```json
{"type":"terminal.frame","seq":1,"encoding":"ansi","width":80,"height":24,"full":true,"bytes":"...base64..."}
{"type":"terminal.closed","reason":"..."}
```

- The controller accepts newline-delimited stdin commands:

```json
{"type":"terminal.input","text":"literal UTF-8"}
{"type":"terminal.input","bytes":"...base64..."}
{"type":"terminal.resize","cols":100,"rows":30,"cell_width_px":8,"cell_height_px":16}
{"type":"terminal.scroll","direction":"up","lines":3,"source":"wheel"}
{"type":"terminal.release"}
```

- Only one terminal controller owns input and resize. A second controller must
  receive a conflict unless the user explicitly confirms takeover.
- Set `min_herdr_version = "0.7.5"`. On startup, call `ping`, verify protocol 17,
  and tolerate unknown response fields.

Verified absence of a conversation surface (`herdr api schema --json`, protocol
17, schema version 1, 89 methods):

- Herdr exposes **no** message, transcript, conversation, role, tool-call,
  interaction, approval, diff, or test-result data. There is no method, event, or
  result shape for any of them.
- The only agent-observable signals are the five lifecycle states, per-pane and
  per-agent identity/context, and rendered terminal text from `pane.read` /
  `agent.read` (bounded, `revision`-stamped, with a `truncated` flag).
- `agent.explain` returns an unconstrained value (`"explain": true` in the schema),
  so it is not a stable contract and is not consumed.
- `pane.report_agent*` are inbound authority assertions a supervisor writes; they
  are not a read source for messages.
- `session.snapshot` carries **no** top-level worktree array. The only worktree
  context in a snapshot is `WorkspaceInfo.worktree`
  (`repo_key`, `repo_name`, `repo_root`, `checkout_path`, `is_linked_worktree`).
  Note what is absent: there is **no branch**. A branch (and a worktree's
  prunable/detached state, and the full inventory including checkouts that are not
  open) requires a separate `worktree.list` call, which the relay does not make —
  so nothing on the wire or in the UI may present a branch name as fact. The
  relay therefore does not model a top-level worktree array at all: a field for
  one would decode empty forever and invite consumers to build on it.
  `worktree.remove` takes the *workspace* a worktree is open in, and git refuses
  to remove a repository's main checkout, so `is_linked_worktree` is exactly the
  removable case and is what gates the destructive control.
- `AgentInfo` additionally reports `launch_pending`, `title`, `state_labels`, and
  `tokens`. The first two are consumed; `state_labels` and `tokens` are
  agent-manifest-derived free text and are deliberately not yet on the wire.

Consequently the relay must never synthesize semantic structure from terminal
bytes. It advertises the absence of every semantic capability and serves the one
thing it can prove: explicitly labelled observed terminal output (§12.1).

### 3.2 Cloudflared 2026.7.2 contract

References:

- `https://github.com/cloudflare/cloudflared`
- `https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/`
- The installed `cloudflared 2026.7.2` help output.

Required contracts:

- Named tunnel tokens can be supplied through `--token-file` or
  `TUNNEL_TOKEN_FILE`; do not put a token in argv.
- Locally managed tunnels can use `--config`, `--credentials-file`, and a tunnel
  name or UUID.
- Quick Tunnel syntax is `cloudflared tunnel --url http://127.0.0.1:PORT`.
- Quick Tunnels are ephemeral, have no uptime guarantee, and are for testing.
- `--no-autoupdate` prevents cloudflared replacing itself behind the plugin.
- `--metrics 127.0.0.1:0` keeps diagnostics on loopback.
- `--loglevel info --output json` gives bounded machine-readable lifecycle logs;
  debug logging is forbidden because it can expose request headers.
- SIGTERM initiates graceful shutdown; a second signal or the grace-period expiry
  ends it.
- WebSockets are supported through the tunnel.

### 3.3 Cloudflare Access

References:

- `https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/self-hosted-public-app/`
- `https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/application-token/`
- `https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/validating-json/`

The origin must validate `Cf-Access-Jwt-Assertion` using the team JWKS endpoint,
RS256, `kid`, issuer, application audience, and time claims. Identity comes only
from verified claims, never from convenience forwarding headers.

### 3.4 Frontend libraries

The design and implementation use the current documented Vite integrations for
Tailwind v4 and shadcn/ui, and xterm.js with `FitAddon`, `onData`, `onBinary`, and
`onResize`. All dependencies are locked and bundled. No runtime CDN is allowed.

## 4. Prior-Art Synthesis

Detailed clean-room audits live in `docs/research/`.

Adopt:

- From `herdr-shortcut`: spec-first delivery, typed Herdr adapter, injected
  runners, bounded output, strict config, secret redaction, checksum-verified
  release fallback, version consistency tests, and least-privilege CI.
- From `herdr-mobile-relay`: mutation deadlines, pane lifecycle generations,
  bounded per-client queues with latest-state coalescing, explicit capabilities,
  fragment-based pairing handoff, mobile reconnect handling, health/readiness
  split, and dual-process supervision.
- From Collie: thin plugin plus independent relay process, snapshot as truth and
  events as wakeups, blocked-first agent triage, strict loopback/Host/Origin
  layering, in-flow mobile key dock, tri-state modifiers, fixture-driven UI
  tests, and honest remote-shell threat documentation.
- From `herdr-remote`: simple status grouping, typed messages, audit trail, and
  reconnect/backoff principles.

Reject:

- Auth disabled by default, bearer tokens in query strings or localStorage,
  trusting proxy identity headers, LAN binding, and permissive origins.
- Polling full terminal text when Herdr provides a real controller stream.
- Positional/regex-driven approval automation without a fresh-state guard.
- Runtime CDN imports, `innerHTML` terminal rendering, missing CSP, unverified
  downloads, shell-evaluated config, blocking subprocess work, and unbounded
  queues.
- Automatic discovery of every named Herdr session.
- Blind notification actions that approve agent prompts without opening context.

## 5. Repository Deliverables

```text
.
|-- .github/
|   |-- dependabot.yml
|   |-- ISSUE_TEMPLATE/
|   |-- pull_request_template.md
|   `-- workflows/{ci,release}.yml
|-- cmd/herdr-phone/main.go
|-- internal/
|   |-- app/            # CLI and orchestration
|   |-- auth/           # pairing, sessions, Access JWT/JWKS
|   |-- buildinfo/      # one version source
|   |-- config/         # TOML loading and validation
|   |-- daemon/         # lifecycle, control socket, runtime state
|   |-- herdr/          # typed socket client and models
|   |-- security/       # middleware, redaction, ANSI filtering
|   |-- server/         # HTTP, WebSocket, protocol, audit
|   |-- state/          # snapshot cache, event wakeups, generations
|   |-- terminal/       # Herdr terminal-controller bridge
|   `-- tunnel/         # cloudflared process and modes
|-- web/
|   |-- src/
|   |-- public/
|   |-- e2e/
|   |-- package.json
|   `-- package-lock.json
|-- docs/research/
|-- scripts/{build,verify-plugin,smoke-install}.sh
|-- CODE_OF_CONDUCT.md
|-- CONTRIBUTING.md
|-- LICENSE
|-- Makefile
|-- README.md
|-- SECURITY.md
|-- SPEC.md
|-- config.example.toml
|-- go.mod
|-- go.sum
|-- herdr-plugin.toml
`-- mise.toml
```

## 6. Plugin Manifest and CLI

Manifest:

- `id = "matheus3301.phone"`, name `Herdr Phone`, version `0.3.0`.
- `min_herdr_version = "0.7.5"`, platforms `macos`.
- Build command `sh scripts/build.sh`.
- Global actions: `start`, `start-quick`, `stop`, `toggle`, `status`,
  `setup-link`, and `doctor`.
- No default keybinding and no long-running plugin pane. `toggle` is the action
  intended for the operator to bind via `[[keys.command]]` with
  `type = "plugin_action"` and `command = "matheus3301.phone.toggle"`.

CLI:

```text
herdr-phone start [--quick] [--foreground]
herdr-phone stop
herdr-phone toggle
herdr-phone status [--json]
herdr-phone setup-link
herdr-phone doctor
herdr-phone version
herdr-phone help
herdr-phone serve                 # internal foreground daemon entrypoint
```

`start` must be idempotent. If the daemon is healthy, return its current mode and
URL. If state is stale, reconcile via the control socket and process identity
before replacing it.

`start` must end by printing the single URL to open on the phone. In named mode
that is the bare public URL, because Access is the gate and no pairing secret is
involved; in quick mode it is the single-use pairing URL, because pairing is the
only gate. The `Public URL:` and `Pairing:` lines are retained in both modes.

`toggle` stops the daemon when it is running and otherwise starts it in the
configured mode, printing the resulting state and, on start, the same open URL. It
reuses the status/start/stop paths rather than duplicating their logic.

`stop` must request graceful shutdown through the private control socket, not kill
an arbitrary PID. `setup-link` rotates a single-use pairing secret and prints both
a URL and best-effort terminal QR code; it remains available in named mode as a
re-bind/recovery path even though pairing is not required there.

## 7. Process Architecture and Lifecycle

```text
Herdr plugin action
  -> herdr-phone start
      -> validate config, Herdr, cloudflared, and state lock
      -> spawn detached herdr-phone serve
      -> wait for private readiness
      -> print the URL to open (named: public URL; quick: pairing URL)

herdr-phone serve
  -> loopback HTTP server
  -> Herdr state engine
  -> cloudflared child
  -> private Unix control socket
  -> terminal-controller children on demand
```

The daemon owns every child context and process group. SIGINT/SIGTERM stops new
requests, closes WebSockets, releases terminal controllers, asks cloudflared to
terminate, waits for a bounded grace period, then kills remaining process groups.
Use `exec.CommandContext`, a nonzero `WaitDelay`, and no shell.

The state directory contains only mode `0600` files and a mode `0600` Unix socket:

- `runtime.json`: pid, instance id, mode, local address, public URL, version,
  start time, and health; never secrets.
- `control.sock`: status, rotate-pairing, and stop commands.
- `audit.jsonl`: bounded sanitized structural and input metadata; never terminal
  content, commands, JWTs, cookies, or pairing values.
- `herdr-phone.log` and `cloudflared.log`: rotated, sanitized, bounded logs.
- Temporary tunnel-token file: `0600`, deleted immediately after cloudflared has
  read it and become ready.

Do not automatically start at login in v0.3.0. Document an optional future
LaunchAgent, but do not generate one silently.

## 8. Configuration

Load strict TOML from:

1. `$HERDR_PLUGIN_CONFIG_DIR/config.toml`.
2. `$XDG_CONFIG_HOME/herdr-phone/config.toml`.
3. `$HOME/.config/herdr-phone/config.toml`.

Unknown keys are errors. Defaults apply before decoding. Expand `~` and explicit
environment variables, erroring on an unset variable. Never execute a shell.

```toml
[server]
host = "127.0.0.1"
port = 8787
session_ttl = "12h"
idle_lock = "30m"
allowed_workspace_roots = ["~"]

[cloudflare]
mode = "named"
binary = "cloudflared"
public_url = "https://herdr.example.com"
config_file = ""
tunnel = ""
credentials_file = ""
token_file = ""
token_command = []
quick_enabled = false
grace_period = "15s"

[auth.access]
enabled = true
team_domain = "example.cloudflareaccess.com"
audience = ""
allowed_identities = []
jwks_ttl = "1h"

[herdr]
socket_path = ""
binary = ""
poll_hot = "1500ms"
poll_cold = "12s"

[ui]
theme = "system"
terminal_font_size = 13
```

Validation:

- `server.host` must be exactly `127.0.0.1` in production.
- Port is 1-65535 and must be free.
- Named mode requires an HTTPS `public_url`, Access enabled, and exactly one
  cloudflared credential strategy: config/credentials, token file, or token
  command.
- Quick mode requires `quick_enabled = true` and ignores Access configuration;
  pairing remains mandatory.
- Secret commands are argv arrays. Their output is bounded, trimmed once, never
  logged, and written only to a temporary mode `0600` token file.
- Credential and token files must be regular files owned by the current user and
  not readable by group/other.
- Durations are positive and bounded. Poll hot is at least 250 ms.
- Allowed workspace roots must exist and resolve without escaping by symlink.

## 9. Authentication and Request Security

### 9.1 App session establishment

An app session is an opaque, random, HttpOnly, Secure, SameSite=Strict
`__Host-herdr_phone` cookie. Session records live only in daemon memory and expire
at the earlier of the configured TTL, the idle-lock period, and the verified Access
JWT expiry. Each session carries a CSRF token the SPA keeps in memory, never
localStorage. These rules are identical for every session regardless of how it was
established — but in named mode the expiry of a session is not the end of access,
because a new one is provisioned on the next request. See *Named-mode session
lifetime is delegated to Cloudflare Access* below.

**Named mode: Access-only auto-provisioning.** Cloudflare Access is the
interactive gate. A request on a session-authenticated route that clears the
origin's Access JWT verification (section 9.2) but presents no valid session
cookie must be given a session bound to the verified Access identity, and the
cookie set on that same response. Requirements:

- The Access claims are re-read at provisioning time, so the session is bound to
  the exact identity that authorized the request and its hard expiry is capped at
  that token's `exp`. Any verification error fails the request closed; no session
  is minted.
- An identity carrying neither `email` nor `common_name` is not provisionable —
  an unattributable session could be neither audited nor reused.
- **Reuse before create.** A live session already bound to the same identity
  subject (verified `email`, else `common_name`) is returned instead of a new one,
  so repeated cookie-less requests cannot accumulate sessions. A reused session
  keeps its existing expiry and is handed back with a fresh cookie carrying it.
- Provisioning emits an audit event (`session.auto`) recording the subject and the
  non-secret audit id — never the cookie value.
- Every later middleware step applies unchanged (section 9.3). In particular a
  brand-new session has not surfaced its CSRF token yet, so a mutating request
  still fails CSRF until the SPA reads `GET /session` — exactly the rule a paired
  session obeys before it learns its token.
- Pairing is therefore not required in named mode. A daemon restart is invisible
  to the operator: the next request re-provisions from the still-valid Access
  identity.

**Named-mode session lifetime is delegated to Cloudflare Access.** This is a
deliberate, accepted consequence of auto-provisioning, and it must be documented
rather than implied away: the same mechanism that makes a daemon restart invisible
makes *every* app-side session ending invisible.

- `server.idle_lock` does **not** re-lock a named-mode session. An idle-expired
  session is dropped from the store, and the next request is simply given a new one
  from the unexpired Access identity. No re-authentication is prompted.
- `DELETE /session` (in-app "End session" / logout) does **not** end named-mode
  access. It revokes that session record and clears the cookie, but the next
  request auto-provisions again. It is a device-local cookie clear, not a
  revocation.
- `server.session_ttl` caps the absolute lifetime of a *single* session, not of
  access. A re-provisioned session starts a fresh TTL, still capped at the Access
  token's `exp`.
- What therefore bounds named-mode access is Cloudflare Access alone: the Access
  session duration configured in Zero Trust, an Access-session revocation, a policy
  or `allowed_identities` change that stops matching the identity, or stopping the
  daemon (which drops all in-memory sessions and tears down the tunnel).
- **Quick mode is unaffected:** `idle_lock`, `session_ttl`, and logout all fully
  apply there, because nothing re-provisions a session without the single-use
  pairing secret.

The UI and the docs must not present `idle_lock` or logout to a named-mode operator
as a security boundary; the honest controls are Access-side revocation and `stop`.
Giving app-side revocation real teeth in named mode (for example, refusing to
auto-provision for an identity that just logged out) would be a deliberate behavior
change to specify here first — it is not the current contract, and it must not be
assumed by anything that relies on this section.

**Quick mode: pairing is mandatory and unchanged.** A quick tunnel has no edge
identity, so no session is ever auto-provisioned there and a cookie-less request
is rejected. Every daemon instance creates a 256-bit random pairing secret;
`setup-link` prints `https://host/#pair=<base64url-secret>`, and fragments are not
sent in HTTP requests. The SPA removes the fragment from history before calling
`POST /api/v1/pair`. The secret is single-use and constant-time compared; success
rotates it and sets the session cookie. Quick mode has no Access identity and
displays `Quick Tunnel operator` in the audit UI.

`POST /pair` stays live in named mode as a re-bind/recovery path, and named mode
still requires a valid Access JWT before pairing and on every subsequent HTTP
request and WebSocket handshake. Pairing is never a way around Access.

### 9.2 Access JWT

- Read only `Cf-Access-Jwt-Assertion`.
- Fetch JWKS over HTTPS with a bounded client, cache TTL, stale-key fallback no
  longer than one TTL, and singleflight refresh.
- Accept RS256 only and verify `kid`, signature, exact issuer, audience, expiry,
  not-before, and issued-at skew.
- If `allowed_identities` is nonempty, require the verified `email` or
  `common_name` claim to match exactly.
- Fail closed when JWKS is unavailable and no valid cached key exists.

### 9.3 Central middleware

All routes pass through one security middleware. The only unauthenticated route
is minimal `GET /health`, returning `ok` and no topology/version/instance detail.

Enforce, in order:

1. Host allowlist: exact public host or explicit loopback development host.
2. Access JWT in named mode.
3. App session cookie, except `/pair`. In **named mode** a missing or invalid
   session cookie is not a rejection: step 2 has already re-validated the Access
   JWT at the origin, so the session is auto-provisioned from that verified
   identity (section 9.1), its cookie is set on the response, and the request
   continues with the new session's identity and CSRF token. This is why an
   expired, idle-locked, or logged-out session does not end named-mode access —
   "invalid cookie" and "no cookie" are the same case here, and both re-provision
   while the Access identity holds. Anything other than a usable session — a
   verification error, a nil session, an empty cookie — leaves the request
   unauthenticated and it is rejected. In **quick mode** this step is unchanged: no
   session means `401`, `/pair` is the only way in, and an expired, idle-locked, or
   logged-out session genuinely locks the operator out until they pair again.
4. Exact Origin allowlist on every WebSocket and every mutating HTTP request.
5. Go `http.CrossOriginProtection` plus CSRF custom header/token.
6. Method and content-type allowlist, bounded bodies, rate limits, and deadlines.

Steps 4 through 6 apply identically to an auto-provisioned session and a paired
one. Auto-provisioning grants no exemption from Origin, `CrossOriginProtection`,
CSRF, rate limiting, or deadlines.

Set CSP, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`,
`X-Frame-Options: DENY`, `Permissions-Policy`, and no-store on API responses. CSP
must use only self-hosted assets, disallow objects/base/framing, and explicitly
allow the same-origin WebSocket. No `unsafe-eval` or runtime CDN.

## 10. Herdr Adapter

Implement one typed package as the only owner of Herdr wire names.

- Resolve the socket from config, then `HERDR_SOCKET_PATH`, then the default.
- Resolve the binary from config, then `HERDR_BIN_PATH`, then `herdr` on PATH.
- Use one Unix connection per normal request, bounded NDJSON, string request ids,
  result type validation, and five-second default timeouts.
- Keep one reconnecting `events.subscribe` connection. Events only wake the
  state engine; snapshots remain source of truth.
- Validate ping version/protocol and expose capability errors clearly.
- Never forward arbitrary browser-supplied method names or raw params.
- Preserve structured Herdr errors with bounded, control-free messages.
- Use explicit IDs on every mutation. Never rely on Herdr UI focus.

## 11. State Engine

The state engine polls `session.snapshot` every 1.5 seconds while any agent is
working/blocked or a browser is active, relaxing to 12 seconds when idle. Herdr
events trigger a debounced immediate poll. If a poll is already running, queue
exactly one follow-up. A missed event therefore costs one interval, never state
correctness.

Normalize Herdr wire objects into a versioned app snapshot. Compute a stable
content hash and broadcast only changes. Every pane has a local lifecycle
generation incremented when it exits, closes, moves to a new ID, or changes
terminal occupant. Mutations and terminal input carry the expected generation;
the server checks it immediately before dispatch.

Per-client outbound queues are bounded by item count and bytes. Consecutive
snapshot updates coalesce to the newest snapshot. A slow client is disconnected
rather than blocking others.

## 12. HTTP and WebSocket Protocol

Prefix all routes with `/api/v1`.

Read routes:

- `POST /pair`
- `GET /session`
- `DELETE /session`
- `GET /snapshot` with ETag and gzip
- `GET /panes/{pane_id}/read?source=visible|recent|recent-unwrapped&lines=N`
- `GET /runs` for the bounded, content-free run inbox (§12.1)
- `GET /runs/{pane_id}?expected_generation=N[&source=...][&lines=N]` for one run
  plus bounded observed output (§12.1)
- `GET /directories?path=...` for directories only, confined to configured roots
- `GET /capabilities`
- `GET /events` WebSocket for snapshot/state updates
- `GET /terminals/{pane_id}` WebSocket for an interactive terminal

Mutation route:

- `POST /confirmations`: prepare a single-use, 30-second nonce bound to operation,
  resource id, lifecycle generation, session, and normalized params.
- `POST /mutations`: execute an allowlisted typed operation.

Mutation envelope:

```json
{
  "request_id": "uuid",
  "operation": "pane.split",
  "deadline_unix_ms": 1780000000000,
  "expected_generation": 7,
  "confirmation": "optional-single-use-nonce",
  "params": {}
}
```

Responses use `{request_id, accepted, result}` or `{request_id, error:{code,
message,retryable}}`. The server deadline is shorter than the client deadline and
is checked again immediately before touching Herdr. Cache idempotent results by
session/request id for five minutes so a network retry cannot repeat a mutation.

A `request_id` is chosen by the client, so every idempotency entry — reservation
and cached response alike — is bound to a fingerprint of the operation, the
asserted `expected_generation`, and the normalized params. A reused id presenting
a different fingerprint is rejected with a non-retryable `conflict`; it can never
replay a response belonging to a different payload, and a cached success can never
be handed back for a generation that was never validated. Key order and
whitespace are not payload differences: the fingerprint is computed from
canonicalized params.

A failed mutation preserves the upstream structured distinction rather than
flattening every failure into one code. The relay-side context cause wins first
(`deadline_exceeded`, `unavailable`); otherwise the Herdr code maps to a stable
API code:

| Upstream | API code | Status | Retryable |
|---|---|---|---|
| `not_found` | `not_found` | 404 | no |
| `invalid_params`, `invalid_request` | `bad_request` | 400 | no |
| `feature_disabled`, `platform_unsupported`, `plugin_disabled`, `incompatible` | `unsupported` | 501 | no |
| `stream_conflict` | `conflict` | 409 | no |
| `timeout` | `deadline_exceeded` | 504 | yes |
| `connect`, `transport`, `canceled` | `unavailable` | 503 | yes |
| anything else / no code | `internal` | 502 | no |

Only the code crosses the boundary. Upstream error *messages* are never
forwarded, because a Herdr message can quote pane content, a path, or a command;
every returned message comes from a static table. Retryable failures are never
cached, so a legitimate retry re-attempts once Herdr recovers.

### 12.1 Structured run contract

Version 1. Herdr supplies no semantic conversation data (§3.1), so the contract is
authoritative about identity and status and explicit about what it cannot know.

`GET /runs` returns
`{contract_version, capabilities, snapshot_hash, runs[], truncated}`. Each run is
`{run_id, pane_id, pane_generation, agent_incarnation, workspace_id,
workspace_label, tab_id, tab_label, terminal_id, agent_kind, agent_name,
display_agent, title, status, interactive_ready, launch_pending, focused, cwd,
foreground_cwd, worktree{repo_name, repo_root, checkout_path,
is_linked_worktree}, revision, state_change_seq}`.

- `run_id` is opaque. Operations are addressed by `pane_id` plus
  `expected_generation`, never by parsing it. It binds the pane id, the pane
  generation, **and** the occupant digest, because a generation alone is not
  unique across pane recycling: a departed pane's generation entry is dropped, so
  a pane id that reappears restarts at generation 1. Folding the incarnation in
  means anything keyed on the id — a client-side run partition holding
  instruction history, a list key — can never let a new occupant inherit a dead
  run.
- `agent_incarnation` is a digest of the pane's occupant identity (terminal id,
  agent kind, bound agent session). It is a digest, not the raw fingerprint,
  because the session reference may be a filesystem path. It changes exactly when
  `pane_generation` changes, so a run invalidates on either.
- `status` is exactly one of `idle`, `working`, `blocked`, `done`, `unknown`. An
  upstream value outside that set is reported as `unknown`; an unrecognized state
  must never read as completion.
- The run projection is derived from the same snapshot and the same generation map
  the mutation guard uses, so a run's reported generation and a mutation guard can
  never disagree. A pane with no live generation is not addressable and is
  therefore not a run. An empty shell pane is not a run.
- The list carries **no** output. Full output must never appear in a
  topology-shaped projection or in the snapshot. `truncated` reports that the
  `max_runs` bound applied, so a short list is never read as complete.

`GET /runs/{pane_id}` returns `{contract_version, capabilities, run, parts[]}`.
`expected_generation` is mandatory: live generations start at 1, so a missing,
zero, or unparseable value is rejected rather than silently skipping the
fresh-state check. The guard runs before any Herdr call.

`parts` is ordered oldest-to-newest. This build emits exactly one part type:

```json
{"type":"observed_terminal_output","source":"recent-unwrapped","format":"text",
 "lines":17,"bytes":1234,"truncated":false,"text":"..."}
```

It is terminal output Herdr rendered, labelled as such. It carries no role and
must never be presented as an assistant message. A client must ignore unknown
part types and must never interpret a part as a message unless its type says so.
Adding a part type or a field does not bump `contract_version`.

`lines` is how many lines `text` **actually contains** — not the bound that was
requested. Clients render it as a statement of fact ("the last N lines this pane
rendered"), so echoing the request back would make that a lie whenever the pane
rendered fewer lines than were asked for. The `lines` query parameter is still
clamped to `max_output_lines`, and that clamp is what the relay asks Herdr for;
the two numbers are different things. `bytes` is the length of `text` after
sanitization, and `truncated` reports that the byte bound dropped older output.

Errors are stable and sanitized:

| Condition | Code | Status |
|---|---|---|
| missing/zero/unparseable `expected_generation` | `generation_stale` | 400 |
| pane gone, or generation mismatch | `generation_stale` | 409 |
| live guarded pane with no agent run | `run_unavailable` | 404 |
| upstream `not_found` on the read | `run_unavailable` | 404 |
| upstream timeout | `deadline_exceeded` | 504 |
| upstream socket fault | `unavailable` | 503 |
| unclassified read failure | `run_read_failed` | 502 |

Capabilities are advertised on `GET /capabilities` (as `runs`) and repeated on
every run response so a single response is self-describing:
`{contract_version, supported, structured_messages, structured_tool_calls,
structured_interactions, structured_diffs, structured_tests, structured_plans,
observed_terminal_output, part_types[], output_sources[], max_output_bytes,
max_output_lines, max_runs}`. Every semantic flag is `false` for Herdr 0.7.5. The
UI gates presentation on these flags and fails closed to observed terminal output;
it must never infer structure the relay did not advertise.

Bounds are server defaults, like `max_pane_read_lines`: 400 output lines
(200 default), 64 KiB of output text, 200 runs. Oversized output keeps the most
recent tail — cut on a UTF-8 rune boundary — and reports `truncated: true`.

No new event type or poll is introduced. Run identity and status are part of the
hashed topology projection, so any change to a pane's or agent's status, occupant,
or context already rebroadcasts on `GET /events`; the client treats that as a
wakeup and refetches `/runs`. Snapshot remains truth and events remain wakeups: a
missed event costs one poll interval, never correctness.

Run responses are `Cache-Control: no-store`. Nothing is stored: no transcript,
output, or run state is cached, persisted, or written to disk, so each request
reads fresh from Herdr and there is no run-state store to bound or race.
`detection` is not an accepted output source — it is Herdr's classifier buffer,
not operator-facing output.

## 13. Terminal Bridge

For each terminal WebSocket, start:

```text
HERDR_BIN_PATH terminal session control PANE_ID --cols C --rows R
```

Add `--takeover` only after a scoped confirmation nonce. The Go bridge parses
Herdr's NDJSON controller output, validates monotonically increasing sequence
numbers, base64-decodes frame bytes, filters dangerous terminal controls, and
sends binary WebSocket frames to xterm.js. Metadata/close records are text JSON.

Browser binary messages are terminal input bytes. Browser text messages are
typed resize, scroll, release, and ping commands. Bound frame size, input size,
resize rates, and pending writes. One writer goroutine owns each subprocess stdin
and one writer goroutine owns each WebSocket. Use ping/pong and close deadlines.

Filter OSC 52, OSC 8, title-setting/reporting, DCS/APC/PM, device status queries,
and answerback sequences before bytes reach the browser or logs. Preserve SGR,
UTF-8 text, cursor positioning, erasure, alternate-screen behavior, and mouse
input needed by a real terminal. Test fragmented/incomplete escape sequences.

Reconnect starts a fresh controller, whose first `full` frame reconstructs the
screen. Do not replay or persist terminal bytes. Never log terminal content.

## 14. Frontend Design Contract

### 14.1 Subject and job

This is a **field console for a live herd of coding agents**, used one-handed on
a phone. Its single job is to put the operator into the right live terminal and
make agent attention unmistakable without turning the product into a generic
admin dashboard.

### 14.2 Visual system

Palette:

- **Deck** `#101820`: primary shell and terminal surround.
- **Bulkhead** `#192732`: raised controls and topology layers.
- **Mist** `#DCE7E4`: primary text.
- **Brass** `#E3B341`: active selection and focus.
- **Tide** `#50A8A3`: connected/working state.
- **Flare** `#F1745E`: blocked, destructive, and disconnected state.

Typography:

- Spline Sans Variable for navigation and interface copy.
- Commit Mono Variable for terminal, IDs, statuses, and utility labels.
- Fonts are bundled locally. The terminal font remains independently configurable.

Shape and spacing:

- Tight 4/8 px rhythm, large 44 px touch targets, modest 10 px radii.
- No gradient hero, dashboard card grid, glassmorphism, or decorative statistics.
- Use lines, inset seams, and stacked edges to evoke a compact field instrument.

Signature element: the **topology ribbon**. Workspace, tab, and pane are rendered
as three offset, horizontally scrollable layers whose visible edges encode the
current hierarchy. Swiping a layer changes that level; tapping its label opens the
full switcher. It is navigation, status, and product identity in one element.

### 14.3 Layouts

Phone terminal:

```text
+--------------------------------+
| HERDR PHONE       online  2 !  |
| /space-api/                  > |
|   tab: auth-refactor          >|
|     pane: claude [working]     |
|--------------------------------|
|                                |
|          LIVE TERMINAL         |
|                                |
|--------------------------------|
| esc ctrl alt  tab   ^  paste   |
| [ message / command... ] [send]|
+--------------------------------+
```

Phone herd view:

```text
+--------------------------------+
| HERD                    switch |
| Needs you                      |
| ! claude  auth-refactor        |
|   "Approve this command?"      |
| Working                        |
| ~ codex   api tests            |
| ~ opencode mobile UI           |
| Quiet                          |
| . 3 agents                     |
|--------------------------------|
| Terminal        Herd     Spaces|
+--------------------------------+
```

Desktop/tablet expands the topology ribbon into a left rail while retaining the
same terminal and control shelf. Do not create a separate desktop product.

### 14.4 Interaction rules

- Terminal is the default route and always gets the largest area.
- Controls sit in document flow above the software keyboard, never over terminal
  content. Respect `safe-area-inset-*` and visual viewport keyboard changes.
- The key dock includes Esc, Tab, Ctrl, Alt, Shift, arrows, Enter, paste, and a
  tri-state modifier cycle: off, next key, locked.
- Blocked agents lead the Herd view; working agents follow; quiet agents collapse.
- Opening an agent shows the terminal before any response controls. Do not offer
  blind one-tap approvals from push notifications in v0.3.0.
- Structural destructive actions use shadcn AlertDialog and a server confirmation
  nonce. Terminal danger-pattern warnings are advisory and require a second tap,
  but never pretend to sandbox an authorized shell.
- Empty and error states name the exact recovery action. Reduced motion is
  honored. Focus rings, screen-reader names, and keyboard navigation are required.
- PWA manifest uses `display: standalone`, relative scope, maskable icons, and
  `viewport-fit=cover`. The shell can be cached; API/terminal data cannot.

## 15. Required Remote Features

Spaces/workspaces:

- List, switch/focus, create with cwd and label, rename, and confirmed close.
- Show worktree provenance and aggregate agent status.

Tabs:

- List in authoritative server order, switch/focus, create, rename, move, and
  confirmed close.

Panes:

- List and render layout, focus, split right/down, resize, zoom, swap, move to
  another tab/new tab/new workspace, rename, and confirmed close.
- Open a fully interactive terminal, resize with the viewport, scroll, reconnect,
  and explicitly take over an existing controller.

Agents:

- Blocked-first list with state, kind, name, title, workspace/tab/pane, cwd, and
  last state transition.
- Focus/open terminal, prompt, send validated logical keys, rename, and start a
  discovered agent kind in an available pane.
- Starting an agent uses server-discovered kinds; never hard-code a stale subset.

Worktrees:

- List, create, open, and confirmed remove. Dirty removal requires a second,
  explicit force confirmation.

Explicitly excluded from the browser:

- `server.stop`, live handoff, plugin install/uninstall, integration install,
  arbitrary socket methods, arbitrary server process launch, and raw filesystem
  file reads.

## 16. Frontend Architecture

- React routes: terminal, herd, spaces, settings/about, pairing, reconnect, and
  offline.
- The SPA must not demand a pairing secret in named mode. It reads the mode the
  relay states on the wire — `identity.mode` on `GET /session` and `POST /pair`,
  the daemon `mode` on `GET /capabilities` — treating only the exact string
  `"named"` as named mode so an absent or unrecognized value fails closed to the
  mode that still requires a secret. In named mode `GET /session` succeeds on a
  cold load with no prior pairing, so the existing recovery path is the whole
  flow. When it fails, the SPA shows the Access reconnect state (whose remedy is a
  top-level reload, the only thing that can obtain a fresh Access identity), not a
  pairing form. Pairing stays reachable as an escape hatch, and is the fallback
  when the SPA has never observed this relay's mode — withholding it from a
  quick-mode operator would lock them out.
- The last observed mode may be cached in browser storage: it is not a credential.
  The session cookie, the CSRF token, and a pairing secret must never be.
- One typed API module generated or checked from a shared JSON/TypeScript schema.
- `useSyncExternalStore` for connection and snapshot state; no heavy data library.
- WebSocket state updates with ETag HTTP fallback. Never trust `navigator.onLine`.
- Reconnect with jittered exponential backoff and immediate revalidation on
  `visibilitychange`, `pageshow`, `focus`, `online`, `freeze`, and `resume`.
- xterm.js and `FitAddon` are mounted/disposed deterministically. Use ResizeObserver
  and VisualViewport; debounce resize without starving the last update.
- All terminal output stays inside xterm.js. Never feed it into `innerHTML` or
  React markup.
- shadcn/ui components are source-owned and visually retokened. Use Sheet,
  AlertDialog, Dialog, DropdownMenu, Button, Input, Badge, and ScrollArea where
  they improve accessibility; do not reproduce the stock shadcn dashboard look.
- No analytics, telemetry, third-party fonts, or runtime network dependencies.

## 17. Reliability and Observability

- `/health` is process liveness. Private control-socket status includes readiness
  for HTTP, Herdr, tunnel, and state engine.
- Quick mode must verify the public URL reaches the same one-time instance probe
  before printing the pairing link.
- Named mode verifies local readiness and cloudflared connectivity; public Access
  prevents an unauthenticated public self-probe unless an optional service token
  is configured, so do not weaken Access for health checks.
- Supervise state subscription and cloudflared independently. Cloudflared exit
  makes the relay unavailable and triggers bounded exponential restart; repeated
  crashes mark the daemon degraded instead of spinning forever.
- Audit every structural mutation with verified identity, resource ids, result,
  request id, and timestamp. Record terminal input only as byte count and category,
  never content.
- Audit an observed-output read (`run.read`) with identity, pane id, outcome, and a
  byte count only. A failed read records the error code and no byte count. Run
  output is terminal content: never log or persist any of it.
- Sanitize control characters and terminal escape sequences from every log line.
- Expose current versions, protocol, mode, public URL, tunnel health, Herdr health,
  and connected client count only to an authenticated session and local status.

## 18. Testing Requirements

Go tests are deterministic, parallel where safe, and require no real Cloudflare,
Herdr, browser, or credentials.

Required Go coverage:

- Config defaults, precedence, unknown-key rejection, path and permission checks,
  mode validation, token command overflow, and redaction.
- Access JWT signature/algorithm/issuer/audience/time/identity checks, JWKS refresh,
  rotation, stale-cache bounds, and failure closure.
- Pairing single use, rotation, session expiry, cookie attributes, CSRF, Host,
  Origin, CrossOriginProtection, rate limits, and route-wide middleware coverage.
- Named-mode auto-provisioning: a cookie-less request with a valid Access JWT
  succeeds and sets a `__Host-` cookie that the next request reuses; repeated
  cookie-less requests for one identity reuse a single session rather than growing
  the store; an invalid or expired JWT still yields `401` with no session minted;
  quick mode never auto-provisions and still requires `/pair`; and the
  `session.auto` audit record carries only the non-secret audit id.
- Herdr NDJSON fragmentation, UTF-8 chunk boundaries, timeout cleanup, result type,
  snapshot decoding, event reconnect, protocol mismatch, and every required
  mutation argv/params.
- State poll overlap, one queued wakeup, hot/cold cadence, hashing, lifecycle
  generations, move ID replacement, client queue coalescing, and backpressure.
- Cloudflared named/config/token/quick argv, no secret in argv/logs, URL detection,
  readiness, restart, signal handling, and temporary-token deletion using a fake
  binary on PATH.
- Terminal controller frame parsing, sequence validation, input/resize/scroll,
  conflict/takeover, deadline/generation guard, reconnect, process cleanup,
  backpressure, and ANSI security filtering including fragmented OSC/DCS.
- HTTP body limits, ETags, compression, mutation idempotency, confirmation nonces,
  deadlines, all operation allowlists, and audit redaction.
- Idempotency binding: a reused `request_id` with a different operation, asserted
  generation, or params is rejected rather than replayed; a canonically identical
  retry still replays exactly once.
- Structured upstream error preservation: each Herdr code maps to its own API
  code/status/retryable outcome, retryable failures are not cached, and no
  upstream message is forwarded.
- Run contract: exact JSON wire for the list and detail responses, capability
  advertisement on all three surfaces, mandatory/stale/absent generation
  rejection before any Herdr call, `run_unavailable` versus `run_read_failed`
  distinction, output byte/line bounds and UTF-8-safe tail truncation, control
  stripping of observed output and display fields, absence of output in the
  inbox, no-store headers, route-table coverage, and audit records proven free of
  run output.
- Manifest/version/build path agreement and Go 1.26 agreement.

Frontend tests:

- Unit tests for stores, reconnect, modifiers, mutation confirmation, safe-area and
  keyboard layout logic, API decoding, and terminal lifecycle.
- Component tests for pairing, blocked-first herd, topology ribbon, create flows,
  all error/empty/offline states, and accessible dialogs.
- Playwright mobile journeys on Chromium Pixel 7 and WebKit iPhone 15 sizes:
  pair, reconnect, switch workspace/tab/pane, terminal input/resize, create
  workspace/tab, split pane, prompt an agent, confirm close, and takeover.
- Desktop smoke at 1440x900, reduced-motion, keyboard-only, and dark/light system
  preference.
- Screenshot review at 390x844, 430x932, 768x1024, and 1440x900.

Run `go test -race ./...`, frontend tests, Playwright, TypeScript, lint, build,
`govulncheck`, and plugin verification in CI. Target at least 80% meaningful Go
statement coverage and enforce a reasonable initial compressed frontend budget.

## 19. Build, CI, and Release

- `mise.toml` pins Go 1.26 and the Node version used by CI.
- Local build runs locked frontend install/build, then embeds `web/dist` into one
  static Go binary.
- `scripts/build.sh` builds from source only when compatible Go and Node are
  present; otherwise it downloads the exact release archive and verifies SHA-256.
- Never auto-install cloudflared. `doctor` gives exact Homebrew/manual guidance.
- GoReleaser publishes darwin amd64/arm64 archives, checksums, and SBOM.
- CI runs macOS and Linux compile/test where portable, but the manifest advertises
  only macOS for v0.3.0.
- Workflows use least privileges, lockfile installs, immutable version agreement,
  no secret-requiring tests, and no commit mutation.
- Release tags are annotated or signed, match manifest and binary version, and
  point to `main`.

Stable developer commands:

```text
make help
make fmt
make lint
make typecheck
make test
make test-race
make test-web
make test-e2e
make coverage
make build-web
make build
make verify-plugin
make check
make clean
```

## 20. Documentation Requirements

README must include install, Cloudflare Access and named tunnel setup, Quick Tunnel
risk/opt-in, macOS Keychain token command, config reference, start/stop/status/toggle,
how sign-in differs by mode (Access-only in named, mandatory pairing in quick), PWA
install on iOS/Android, feature guide, security model, architecture, troubleshooting,
development, and release instructions.

`docs/install.md` must be a self-contained, imperative guide a coding agent can
execute end to end: prerequisite verification commands, the plugin install command,
a minimal named-mode config with placeholders and exactly one credential strategy,
Keychain token storage that never echoes the token, start plus how to read the
printed access URL, the optional `toggle` keybinding, and a troubleshooting table.

SECURITY.md must explicitly say the tool grants remote shell-equivalent access,
list supported versions, explain private reporting, document the Access JWT and
per-mode app-session defenses (including auto-provisioned sessions' in-memory,
Access-expiry-capped lifetime), and provide immediate tunnel-token/session
revocation steps.

## 21. Non-Goals for v0.3.0

- Windows host support, native iOS/Android apps, APNs, or background push actions.
- Multi-user collaboration or simultaneous terminal controllers.
- Automatic named-tunnel/DNS/Access provisioning in the Cloudflare account.
- Automatic cloudflared installation or self-update.
- Starting at login or surviving a full host reboot without user configuration.
- Multi-session Herdr aggregation.
- Persistent or on-disk app sessions; sessions stay in daemon memory.
- Dropping or weakening pairing in quick mode.
- Parsing agent-specific approval screens into native controls.
- File browsing beyond directory selection, file upload, clipboard image transfer,
  or arbitrary downloads.
- Exposing Herdr server/plugin/integration administration.

## 22. Definition of Done

Implementation is complete only when:

- Every required file exists with real content and no TODO/FIXME/fake paths.
- `mise.toml` and `go.mod` pin Go 1.26.
- `make check` and `go test -race ./...` pass.
- Frontend unit, component, and required Playwright mobile journeys pass.
- The plugin manifest validates against Herdr 0.7.5 in isolated state.
- A fake-cloudflared end-to-end test covers named and quick startup/teardown.
- A local Herdr smoke proves snapshot, events, terminal controller, input, resize,
  create workspace/tab, split, agent prompt, confirmed close, and reconnect.
- Named mode rejects missing/invalid Access configuration and verifies JWTs, and
  reaches an authenticated session from a valid Access identity alone, with no
  pairing step.
- Quick mode cannot start without explicit config opt-in and mandatory pairing,
  and never auto-provisions a session.
- No secret appears in argv, logs, state, browser storage, test snapshots, or git.
- The UI is usable at 390 px width with a software keyboard open and has passed
  screenshot review, keyboard accessibility, and reduced-motion checks.
- README commands match the binary and implementation.
- A final security/code review finds no high-severity issues.

Final delivery report must list architecture, implemented flows, verification
commands and results, deliberate spec deviations, and remaining non-goal limits.
