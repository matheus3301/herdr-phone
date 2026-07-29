# herdr-phone

[![CI](https://github.com/matheus3301/herdr-phone/actions/workflows/ci.yml/badge.svg)](https://github.com/matheus3301/herdr-phone/actions/workflows/ci.yml)
[![Release](https://github.com/matheus3301/herdr-phone/actions/workflows/release.yml/badge.svg)](https://github.com/matheus3301/herdr-phone/actions/workflows/release.yml)
[![Go 1.26](https://img.shields.io/badge/go-1.26-00ADD8?logo=go)](https://go.dev/)
[![Herdr 0.7.5+](https://img.shields.io/badge/herdr-0.7.5%2B-5B4FE9)](https://herdr.dev)
[![cloudflared](https://img.shields.io/badge/cloudflared-2026.7.2-F38020?logo=cloudflare)](https://github.com/cloudflare/cloudflared)
[![License: MIT](https://img.shields.io/badge/license-MIT-green)](LICENSE)

**Herdr Phone** lets one authenticated operator supervise the
[Herdr](https://herdr.dev) session running on their Mac from a phone. An
attention-first inbox shows which coding agents need you, which are working, and
which changed while you were away. Start a scoped agent run, send instructions,
inspect truthful observed output, manage workspaces, and open the full console
only when direct terminal control is necessary.

It is a Go 1.26 relay with a React + TypeScript PWA embedded into the binary. It
starts and supervises `cloudflared`, binds its origin to loopback only, and
never exposes Herdr's local socket to a browser. Named tunnels use Cloudflare
Access as the edge identity layer, and the origin re-validates the Access JWT
on every request. Quick Tunnels require explicit opt-in.

> ### ⚠️ This is remote shell access, by design
>
> Herdr Phone puts an interactive terminal for your Mac on the public internet.
> Anyone who reaches the front door and clears its authentication can run
> arbitrary commands as your user — the same power as an SSH session. Its
> security bar is that of an SSH client, not a read-only dashboard. In named
> mode, clearing Cloudflare Access **is** clearing the front door, so treat the
> public URL and your Cloudflare Access identity — and, in quick mode, the
> pairing link — like a root login. In named mode your Cloudflare Access session
> duration is also the only thing that times you out: neither `idle_lock` nor the
> in-app **End session** button ends access there
> ([details](#session-lifetime-in-named-mode)). Read [SECURITY.md](SECURITY.md)
> before you expose it.

## Contents

- [Features](#features)
- [How it works](#how-it-works)
- [Prerequisites](#prerequisites)
- [Install](#install)
  - [Install with an agent](#install-with-an-agent)
- [Verifying a downloaded release](#verifying-a-downloaded-release)
- [Choosing a front door](#choosing-a-front-door)
  - [Named tunnel with Cloudflare Access (recommended)](#named-tunnel-with-cloudflare-access-recommended)
  - [Origin JWT configuration](#origin-jwt-configuration)
  - [Quick Tunnel (testing only, opt-in)](#quick-tunnel-testing-only-opt-in)
- [Providing the tunnel token](#providing-the-tunnel-token)
- [Configuration](#configuration)
- [Running: start, stop, status](#running-start-stop-status)
- [Signing in: Access and pairing](#signing-in-access-and-pairing)
  - [Session lifetime in named mode](#session-lifetime-in-named-mode)
- [Installing the PWA on your phone](#installing-the-pwa-on-your-phone)
- [Feature guide](#feature-guide)
- [Security model](#security-model)
- [Architecture](#architecture)
- [Development](#development)
- [Releasing](#releasing)
- [Troubleshooting](#troubleshooting)
- [Non-goals (v0.3.0)](#non-goals-v030)
- [Contributing](#contributing)
- [License](#license)

## Features

- **Agent-first inbox.** Runs are grouped as Needs you, Working, Updated, Idle,
  and Status unknown. Opening a run never changes focus on the Mac.
- **Run control, not terminal cosplay.** Send an instruction, retain drafts
  through connection failures, distinguish rejection from uncertain delivery,
  and never retry a possibly-delivered instruction automatically.
- **Truthful observed output.** A versioned, generation-bound run API returns
  identity, context, status, and bounded terminal output. Terminal bytes are
  always labelled as recent output, never fabricated into assistant messages,
  tool calls, approvals, diffs, or test results.
- **Start run.** Choose an existing workspace, create a workspace, or branch a
  linked worktree; select a server-discovered agent kind; then see every launch
  step and recover from partial success without undoing valid resources.
- **Workspace management.** Inspect worktree provenance, tabs, panes, agents,
  and lifecycle generations, with complete advanced move, split, resize, zoom,
  swap, rename, focus, and confirmed-close controls.
- **Full console fallback.** A lazy-loaded xterm.js console is one tap away for
  blocked, unknown, or recovery states. It resizes, scrolls, reconnects, detects
  pane replacement, and requires explicit confirmation to take over input.
- **Safe structural controls.** Create, rename, move, resize, zoom, split, swap,
  and confirmed-close, each with an explicit, single-use server confirmation for
  destructive actions.
- **One-step start.** `start` prints the single URL to open on your phone, and a
  keybindable `toggle` action turns the relay on and off without a terminal.
- **Secure by default.** Loopback-only origin; Cloudflare Access as the gate for
  named tunnels, with the origin re-validating the JWT on every request and
  reconnect; mandatory single-use pairing in quick mode, which has no edge
  identity; strict Origin/CSRF/CSP; and terminal escape-sequence filtering.
- **Self-contained.** One static binary with the PWA embedded — no runtime CDN,
  no analytics, no telemetry, no hidden network access.
- **macOS, amd64 and arm64** for v0.3.0.

## How it works

```text
 phone (installed PWA)
    │  https / wss  (public hostname)
    ▼
 Cloudflare edge  ── Access (named mode): signed Cf-Access-Jwt-Assertion
    │  outbound-only tunnel (no inbound ports opened on your Mac)
    ▼
 cloudflared (child process, supervised by the daemon)
    │  http / ws to 127.0.0.1:PORT  (loopback only)
    ▼
 herdr-phone serve (Go daemon)
    ├─ validates Access JWT + app session on every request
    ├─ state engine: snapshot as truth, events as wakeups
    ├─ terminal bridge: `herdr terminal session control <pane>` per WebSocket
    └─ typed Herdr socket adapter (the only owner of Herdr wire names)
    │  newline-delimited JSON over the Herdr Unix socket
    ▼
 Herdr server (your panes, tabs, workspaces, agents)
```

The browser never touches Herdr's Unix socket. The daemon owns every child
process and tears them down on exit; killing the daemon tears down the tunnel.

## Prerequisites

- **macOS** (Apple silicon or Intel). v0.3.0 is macOS-only.
- [**Herdr**](https://herdr.dev) **v0.7.5+** with a working `plugin` command
  (verify with `herdr plugin`).
- [**cloudflared**](https://github.com/cloudflare/cloudflared). It is **never**
  installed automatically. Install it with Homebrew (`brew install cloudflared`)
  or from Cloudflare's releases; `herdr-phone doctor` prints exact guidance.
- A **Cloudflare account** with Zero Trust enabled, for a named tunnel and an
  Access application. (Quick Tunnels need no account but are testing-only — see
  below.)
- Optional for building from source: **Go 1.26.5 or newer** (the 1.26 series;
  1.26.5+ specifically, because earlier 1.26 patch releases contain reachable
  standard-library vulnerabilities — the build script treats an older 1.26.x as
  incompatible and falls back to a checksum-verified release download) and
  **Node.js 22.23.1** (the version pinned in `mise.toml`; the frontend's Vite 7
  requires Node ≥ 22.12, so building from source needs Node 22.12+ on the 22 line
  or 24+). Prebuilt releases need neither.

## Install

```sh
herdr plugin install matheus3301/herdr-phone
```

At install time the plugin builds `bin/herdr-phone` from source when compatible
Go **and** Node toolchains are present — building the embedded frontend and
linking it into one static binary — and otherwise downloads and **verifies the
SHA-256 checksum** of the macOS release archive for your architecture. It never
runs `curl | sh` and never installs `cloudflared`.

The plugin registers global actions (`start`, `start-quick`, `stop`, `toggle`,
`status`, `setup-link`, `doctor`) and installs **no** default keybinding and
**no** long-running pane.

### Install with an agent

[`docs/install.md`](docs/install.md) is a self-contained, imperative guide written
for a coding agent: prerequisite checks, the install command, a minimal named-mode
config with placeholders, Keychain token storage, start, and a troubleshooting
table. Hand it to your agent as-is, or fetch it yourself:

```sh
curl -fsSL https://raw.githubusercontent.com/matheus3301/herdr-phone/main/docs/install.md
```

You still create the Cloudflare tunnel and Access application yourself — the
plugin never provisions anything in your Cloudflare account, and the guide tells
the agent which four values (hostname, team domain, AUD tag, tunnel token) to ask
you for.

## Verifying a downloaded release

Every release publishes macOS `.tar.gz` archives, a `checksums.txt`, an SBOM, and
a signed build-provenance attestation.

- **Checksum (mandatory, automatic).** When it cannot build from source, the
  install script downloads the release archive and verifies its **SHA-256**
  against `checksums.txt` before installing, failing closed on any mismatch. To
  check an archive yourself:

  ```sh
  shasum -a 256 -c checksums.txt
  ```

- **Build-provenance attestation (recommended).** Each archive and the
  `checksums.txt` is attested with GitHub's keyless build provenance
  (Sigstore/OIDC — no long-lived signing key). Confirm an artifact was built by
  this repository's release workflow, not swapped after the fact:

  ```sh
  gh attestation verify herdr-phone_0.3.0_darwin_arm64.tar.gz \
    --repo matheus3301/herdr-phone
  ```

  This checks the file's digest against a provenance statement produced by the
  herdr-phone release workflow on GitHub, with no trust placed in any downloaded
  key. Checksum verification stays mandatory and independent of this step.

## Choosing a front door

Herdr Phone supports two front doors. **Named tunnels with Cloudflare Access are
the default and the only mode appropriate for real use.** Quick Tunnels are for
local testing and are off unless you explicitly enable them.

| | Named tunnel + Access | Quick Tunnel |
| --- | --- | --- |
| Edge identity | Cloudflare Access (deny-by-default) | **None** |
| Hostname | Stable, yours | Random `*.trycloudflare.com`, changes each run |
| Uptime | Production-grade | **No SLA; testing only** |
| App pairing | Not required — Access is the gate | **Required** (single-use link) |
| Origin JWT re-validation | Every request and WebSocket handshake | n/a (no Access) |
| Default | ✅ enabled | ❌ off; explicit opt-in |

### Named tunnel with Cloudflare Access (recommended)

Herdr Phone does **not** provision anything in your Cloudflare account (that is a
deliberate non-goal). You create the tunnel and the Access application once in
the Cloudflare dashboard, then point Herdr Phone at them.

1. **Create a remotely-managed tunnel.** In the Zero Trust dashboard, go to
   **Networks → Tunnels → Create a tunnel → Cloudflared**. Give it a name and
   copy the **tunnel token** (you will store it securely — see
   [Providing the tunnel token](#providing-the-tunnel-token)).

2. **Add a public hostname** to the tunnel that routes to the loopback origin
   Herdr Phone binds:

   - Subdomain/domain: e.g. `herdr.example.com`
   - Service: **HTTP** → `127.0.0.1:8787` (match `server.port` in your config)

3. **Create a self-hosted Access application** for that hostname
   (**Access → Applications → Add an application → Self-hosted**). Access
   applications are deny-by-default; add an **Allow** policy that matches only
   your identity (for example, your email). Note two values from the application:

   - the **Application Audience (AUD) tag**, and
   - your **team domain** (`your-team.cloudflareaccess.com`).

4. **Configure Herdr Phone** (see [Configuration](#configuration)):

   ```toml
   [server]
   host = "127.0.0.1"
   port = 8787

   [cloudflare]
   mode = "named"
   public_url = "https://herdr.example.com"
   # Provide exactly one credential strategy — token file, token command, or a
   # locally-managed config/credentials pair. See "Providing the tunnel token".
   token_file = "~/.config/herdr-phone/tunnel.token"

   [auth.access]
   enabled = true
   team_domain = "your-team.cloudflareaccess.com"
   audience = "your-application-audience-tag"
   allowed_identities = ["you@example.com"]
   ```

5. **Start it:**

   ```sh
   herdr-phone start
   ```

   The daemon validates the config, starts `cloudflared`, waits for readiness,
   and prints the URL to open on your phone. In named mode that is the bare
   public URL: sign in through Cloudflare Access and you are in — no pairing
   link is needed.

### Origin JWT configuration

Cloudflare Access is an **edge** control. Herdr Phone does not trust it blindly:
in named mode the origin cryptographically validates the Access JWT on **every**
HTTP request and WebSocket handshake, so a request that somehow reaches loopback
directly is still rejected without a valid token. That origin-side re-validation
is precisely why named mode can treat Access as the gate and skip pairing:
authorization is re-proven per request, not accepted once at sign-in.

Set these under `[auth.access]`:

- `enabled = true` — required in named mode.
- `team_domain` — your `*.cloudflareaccess.com` domain. Used to derive the JWKS
  endpoint (`https://<team_domain>/cdn-cgi/access/certs`) and the expected issuer.
- `audience` — the exact **Application Audience (AUD) tag** of your Access
  application. The origin enforces `aud` equality.
- `allowed_identities` — optional. When non-empty, the verified `email` (or
  `common_name` for service tokens) claim must match one of these exactly.
- `jwks_ttl` — how long signing keys are cached (default `1h`), with a bounded
  stale-key fallback and single-flight refresh.

The origin reads only the signed `Cf-Access-Jwt-Assertion` header, accepts RS256
only, verifies `kid`/issuer/audience/`exp`/`nbf`/`iat`, and **fails closed** when
JWKS is unavailable and no valid cached key exists. It never trusts the
convenience `Cf-Access-Authenticated-User-Email` header.

### Quick Tunnel (testing only, opt-in)

A Quick Tunnel (`cloudflared tunnel --url http://127.0.0.1:PORT`) needs no
Cloudflare account and prints a random `*.trycloudflare.com` hostname. **It has
no Cloudflare Access identity at the edge**, no uptime guarantee, and is intended
by Cloudflare for testing and development only. Herdr Phone therefore keeps app
pairing **mandatory** for a Quick Tunnel, but pairing is the *only* barrier —
there is no edge identity in front of it.

Because of that, Quick Tunnels are **off by default**. To use one for local
testing you must both set `quick_enabled = true` in config and start with the
quick flag:

```toml
[cloudflare]
mode = "quick"
quick_enabled = true
```

```sh
herdr-phone start --quick
```

Quick mode ignores Access configuration and shows `Quick Tunnel operator` in the
audit UI. Before it prints a pairing link, Herdr Phone verifies that the public
URL reaches this exact instance. Do not use a Quick Tunnel as a durable endpoint,
and do not use it for anything you would not expose over an unauthenticated URL.

## Providing the tunnel token

A named tunnel needs a credential. Provide **exactly one** strategy under
`[cloudflare]`; the token is never placed on the command line.

**Token file** — a file containing the tunnel token, mode `0600`, owned by you:

```toml
[cloudflare]
token_file = "~/.config/herdr-phone/tunnel.token"
```

**Token command (recommended)** — an argv array run directly (never through a
shell); its bounded output is trimmed once and written only to a temporary
`0600` file that is deleted immediately after `cloudflared` reads it. Store the
token in the **macOS Keychain** and reference it:

```sh
security add-generic-password -a "$USER" -s herdr-phone-tunnel -w "your-tunnel-token"
```

```toml
[cloudflare]
token_command = ["security", "find-generic-password", "-s", "herdr-phone-tunnel", "-w"]
```

Or with the 1Password CLI:

```toml
[cloudflare]
token_command = ["op", "read", "op://Private/Herdr Phone Tunnel/token"]
```

**Locally-managed tunnel** — point at a `cloudflared` config and credentials
file plus a tunnel name or UUID:

```toml
[cloudflare]
config_file = "~/.cloudflared/config.yml"
credentials_file = "~/.cloudflared/<UUID>.json"
tunnel = "<name-or-uuid>"
```

Credential and token files must be regular files owned by you and not readable by
group or other. No token is ever written to config, logs, state, or argv.

## Configuration

Configuration is strict TOML. Unknown keys are errors. It is loaded from the
first of:

1. `$HERDR_PLUGIN_CONFIG_DIR/config.toml`
2. `$XDG_CONFIG_HOME/herdr-phone/config.toml`
3. `$HOME/.config/herdr-phone/config.toml`

Find the exact directory Herdr uses for this plugin:

```sh
herdr plugin config-dir matheus3301.phone
```

`~` and explicit environment variables are expanded (an unset variable is an
error); a shell is never executed. See
[`config.example.toml`](config.example.toml) for the full commented reference.

```toml
[server]
host = "127.0.0.1"           # must be exactly 127.0.0.1 in production
port = 8787                  # 1–65535 and must be free
session_ttl = "12h"          # caps one session; in named mode a new one is provisioned after
idle_lock = "30m"            # quick mode only — does not re-lock a named-mode session
allowed_workspace_roots = ["~"]

[cloudflare]
mode = "named"               # "named" or "quick"
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
enabled = true               # required in named mode
team_domain = "your-team.cloudflareaccess.com"
audience = ""
allowed_identities = []
jwks_ttl = "1h"

[herdr]
socket_path = ""             # resolved from config, then HERDR_SOCKET_PATH, then default
binary = ""                  # resolved from config, then HERDR_BIN_PATH, then `herdr` on PATH
poll_hot = "1500ms"          # at least 250ms
poll_cold = "12s"

[ui]
theme = "system"
terminal_font_size = 13

[experimental]
agent_output_parsing = false                      # off by default; see below
agent_output_parsers = ["claude", "opencode"]
max_interpreted_turns = 60
```

Validation highlights:

- `server.host` must be exactly `127.0.0.1` in production; the port must be free.
- **Named mode** requires an HTTPS `public_url`, `auth.access.enabled = true`,
  and exactly one credential strategy (config/credentials, token file, or token
  command).
- **Quick mode** requires `quick_enabled = true`, ignores Access configuration,
  and still requires pairing.
- **`session_ttl` and `idle_lock` bound a session, and in named mode a session is
  not the same thing as access.** When either elapses, the next request is simply
  given a new session from your still-valid Cloudflare Access identity. They are
  real limits in quick mode; in named mode the limit that matters is the Access
  session duration you set in Cloudflare — see
  [Session lifetime in named mode](#session-lifetime-in-named-mode).
- Durations are positive and bounded; `poll_hot` is at least 250 ms.
- Allowed workspace roots must exist and must not escape via symlink.
- `experimental.agent_output_parsers` accepts only `claude` and `opencode`; an
  unrecognized name fails startup rather than silently parsing nothing.

### Experimental: reading the agent's output as a chat

Off by default. With `agent_output_parsing = true`, the relay pattern-matches the
on-screen output of Claude Code and OpenCode and the run page renders as a
conversation — the agent's apparent prose, its tool calls, and the question or
approval it looks like it is waiting on, with tappable answers.

Be clear about what this is before you turn it on:

- **It is a guess.** Herdr publishes no conversation API, so this reads pixels'
  worth of text off a terminal. It will misread things, and it will break when
  Claude Code or OpenCode changes its interface.
- **It never pretends otherwise.** The chat carries a standing "experimental
  reading" label, the relay advertises it as `heuristic_interpretation` rather than
  as structured data, and the raw terminal output stays on the page with the console
  one tap away. Turning the flag back off restores the previous run page exactly.
- **Answering is deliberate.** Tapping a detected option shows you the literal
  keystroke it will send and waits for you to confirm. Nothing is ever sent on one
  tap, and the key comes from the option's number — never from text scraped off the
  screen.
- **OpenCode prompts are shown but not answerable.** OpenCode marks the selected
  button with terminal styling that the relay's text read discards, so there is no
  way to know what pressing Enter would choose. Those prompts are surfaced with a
  link to the console instead of a guess.

```toml
[experimental]
agent_output_parsing = true
agent_output_parsers = ["claude"]   # narrow it if you only trust one grammar
```

Restart the relay after changing it: the capability is read at startup.

## Running: start, stop, status

```text
herdr-phone start [--quick] [--foreground]   # start (and supervise) the daemon
herdr-phone stop                             # graceful shutdown via the control socket
herdr-phone toggle                           # stop if running, otherwise start
herdr-phone status [--json]                  # mode, URL, and health
herdr-phone setup-link                       # rotate the pairing secret; print URL + QR
herdr-phone doctor                           # diagnose config, Herdr, and cloudflared
herdr-phone version
herdr-phone help
```

Invoke these from Herdr as plugin actions, or run the built binary directly when
developing. As Herdr actions, output goes to the plugin log:

```sh
herdr plugin action invoke matheus3301.phone.start
herdr plugin action invoke matheus3301.phone.status
herdr plugin log list --plugin matheus3301.phone
```

- **`start`** validates config, Herdr, `cloudflared`, and the state lock; spawns
  a detached `herdr-phone serve`; waits for private readiness; and prints the one
  URL to open on your phone. It is **idempotent** — if the daemon is already
  healthy it returns the current mode and URL, reconciling stale state via the
  control socket and process identity before replacing it.

  ```text
  herdr-phone started in named mode.
  Public URL: https://herdr.example.com
  Pairing:    https://herdr.example.com/#pair=<base64url-secret>

  Open on your phone: https://herdr.example.com
  Cloudflare Access signs you in; no pairing link is needed.
  ```

  In **named mode** the open target is the bare public URL. In **quick mode** it
  is the single-use pairing link, because pairing is the only gate there. The
  `Public URL:` and `Pairing:` lines are still printed in both modes.
- **`stop`** requests graceful shutdown through the private control socket (it
  does not kill an arbitrary PID). New requests stop, WebSockets close, terminal
  controllers are released, `cloudflared` is asked to terminate within the grace
  period, and remaining process groups are killed.
- **`toggle`** stops the relay when it is running and starts it in the configured
  mode when it is not, printing the resulting state (and, on start, the same open
  URL). It is the action to bind to a key — see below.
- **`status`** reports current mode, public URL, and the readiness of HTTP,
  Herdr, the tunnel, and the state engine to an authenticated caller or local
  status. `--json` emits machine-readable output.
- **`doctor`** checks configuration and connectivity and prints exact
  Homebrew/manual guidance if `cloudflared` is missing. It never prints secrets.

### Binding a key to toggle it

`matheus3301.phone.toggle` is a global plugin action with no default keybinding.
Bind it in your Herdr keymap to turn the relay on and off without typing a
command:

```toml
[[keys.command]]
key = "prefix+p"
type = "plugin_action"
command = "matheus3301.phone.toggle"
```

The daemon does **not** start at login in v0.3.0 (an optional LaunchAgent is
documented as future work but never generated silently).

## Signing in: Access and pairing

How you get in depends on the front door, because the two modes have different
identity guarantees.

**Named mode — Cloudflare Access only.** Open the public URL on your phone and
sign in to Cloudflare Access as an allowed identity. That is all: the relay
provisions an app session from the verified Access identity and sets an HttpOnly,
Secure, `SameSite=Strict` `__Host-` session cookie. **No pairing link and no
`#pair=` fragment is involved.** The Access JWT is still re-validated at the
origin on every subsequent request and WebSocket handshake, and the session lives
only in daemon memory, expiring at the earlier of `session_ttl` and the Access
JWT's own expiry. Restarting the daemon simply causes the next request to
provision again, so restarts need no action from you.

**Quick mode — the pairing link is mandatory.** A Quick Tunnel has no edge
identity, so the single-use pairing secret is the only gate. Every daemon
instance mints a fresh 256-bit secret, and `start` (or `setup-link`) prints both a
URL and a best-effort terminal QR code:

```text
https://<random>.trycloudflare.com/#pair=<base64url-secret>
```

The secret rides in the URL **fragment** (`#pair=…`), which browsers never send
in an HTTP request. Open the link on your phone (or scan the QR); the app removes
the fragment from history and exchanges it for the session cookie. The secret is
single-use and rotates on success — run `herdr-phone setup-link` for a fresh one.

`setup-link` and `POST /pair` stay live in named mode as a re-bind and recovery
path, but you do not need them there — and a pairing link is never a way *around*
Access, since a named-mode request without a valid Access JWT is rejected before
pairing is even considered.

### Session lifetime in named mode

> **In named mode, Cloudflare Access is the only thing that ends your session.**
> `server.idle_lock` does not re-lock it, and the in-app **End session** button does
> not end access: the relay transparently provisions a new session from your
> still-valid Access identity on the very next request. Access continues until the
> Access session expires in Cloudflare, you revoke it, or you stop the daemon.

This is the deliberate trade this mode makes — the pairing second factor is gone,
so nothing app-side is holding a lock. Concretely, in named mode:

| Control | Effect in named mode |
| --- | --- |
| `server.idle_lock` | **No effect on access.** The idle session is dropped and immediately re-provisioned. |
| `server.session_ttl` | Caps one session, not access. The replacement starts a fresh TTL, capped at the Access token's expiry. |
| **End session** / `DELETE /session` | Clears this device's cookie. The next request re-provisions. |
| Access session duration (Zero Trust) | **The real limit.** Set it deliberately — it is your idle timeout. |
| Revoke Access session, or drop the identity from the policy / `allowed_identities` | **Real revocation**, effective once the current token expires. |
| `herdr-phone stop` | **Immediate.** Drops every in-memory session and tears down the tunnel. |

So configure the Access application's session duration in Cloudflare Zero Trust to
something you would accept as an unattended-terminal window, and treat
`herdr-phone stop` as the real "lock the door" action.

In **quick mode** all of this behaves as you would expect: `idle_lock`,
`session_ttl`, and **End session** each genuinely end access, because without the
single-use pairing secret nothing can re-establish a session.

## Installing the PWA on your phone

Herdr Phone is a Progressive Web App: `display: standalone`, maskable icons, and
`viewport-fit=cover`. Once you are signed in, add it to your home screen for a
full-screen, app-like experience.

- **iOS (Safari):** tap **Share** → **Add to Home Screen**.
- **Android (Chrome):** tap the **⋮** menu → **Install app** (or **Add to Home
  screen**).

The app shell can be cached offline; API and terminal data are never cached. The
app never trusts `navigator.onLine` — it revalidates on `visibilitychange`,
`pageshow`, `focus`, `online`, `freeze`, and `resume`, and reconnects with
jittered exponential backoff.

## Feature guide

Everything is driven by explicit resource IDs and pane generations; reading the
phone UI never relies on or silently changes Herdr UI focus.

- **Agents:** the default route is an attention inbox. `done` is displayed as
  Updated, never as success; unknown remains separate from Idle.
- **Runs:** a run binds an opaque ID to a pane generation and agent incarnation.
  Its detail view keeps exact workspace, worktree, tab, pane, and agent context
  next to the composer. Replacing the pane freezes the old run instead of
  silently rebinding it to the new occupant.
- **Instructions:** accepted instructions enter the local runline. Rejected
  instructions remain editable. A timeout or disconnect becomes Delivery
  unknown with an explicit warning and choice, never an automatic retry.
- **Observed output:** supported relays use `GET /api/v1/runs` and the
  generation-guarded run detail endpoint. Older relays fall back by capability
  to snapshot projection plus `pane.read`; fallback IDs are internal only.
- **Start run:** choose the objective, execution location, and agent. Workspace,
  pane, agent, and prompt creation remain visible independent operations, so a
  later failure never hides or deletes earlier success.
- **Workspaces:** inspect active runs and linked-worktree provenance first, then
  use the advanced view for tabs, panes, layout, agent startup, and destructive
  controls. Worktree removal is available only when Herdr identifies the
  workspace as a removable linked checkout.
- **Console:** direct terminal control is an expert fallback rather than primary
  navigation. The real xterm.js controller preserves lifecycle generation across
  reconnects and names pane replacement instead of retrying forever.

Structural destructive actions use a confirmation dialog plus a single-use server
nonce bound to the operation, resource id, lifecycle generation, and session.
Terminal danger-pattern warnings are advisory and require a second tap — Herdr
Phone never pretends to sandbox an authorized shell.

Deliberately **not** exposed to the browser: stopping the Herdr server, live
handoff, plugin/integration administration, arbitrary socket methods, arbitrary
process launch, and raw filesystem file reads.

## Security model

Herdr Phone grants remote shell-equivalent access, so its defenses are layered
and fail closed. In brief:

- **Loopback-only origin**, reached from the internet only through an
  outbound-only Cloudflare Tunnel. Never bound to a LAN address in production.
- **Cloudflare Access** in front of named tunnels, with the origin
  **re-validating the Access JWT** on every request and reconnect. In named mode
  Access is the interactive gate: the app session is provisioned from the verified
  Access identity, capped at that token's expiry — and Access is also the **sole
  session-lifetime authority**, since `idle_lock` and logout do not stop a new
  session from being provisioned (see
  [Session lifetime in named mode](#session-lifetime-in-named-mode)).
- **Mandatory single-use pairing in quick mode**, which has no edge identity — a
  fragment-borne secret exchanged for an HttpOnly `__Host-` session cookie.
  Sessions in either mode live only in daemon memory.
- One central middleware enforcing Host allowlist → Access JWT → session cookie →
  exact Origin allowlist → `http.CrossOriginProtection` + CSRF token → method,
  content-type, body-size, rate-limit, and deadline checks.
- A **strict CSP** with only self-hosted assets, no `unsafe-eval`, no runtime CDN,
  no framing, and an explicit same-origin WebSocket allowance.
- **Terminal escape-sequence filtering** (OSC 52/OSC 8, title set/report,
  DCS/APC/PM, device-status and answerback queries) before bytes reach the
  browser or any log.
- **No shell** for subprocesses; `exec.CommandContext`, `WaitDelay`, and process
  groups tear down `cloudflared` and terminal controllers reliably.
- **No secret** in argv, logs, the audit trail, browser storage, snapshots, or
  git. The audit trail records terminal input only as a byte count and category.

See [SECURITY.md](SECURITY.md) for the full model, the trust boundaries in
[`docs/research/security.md`](docs/research/security.md), and **immediate
revocation steps** for a compromised session, device, or tunnel token.

## Architecture

```text
cmd/herdr-phone ──> internal/app           # CLI dispatch and doctor
                    └──> internal/integration # production composition and teardown
                         ├──> internal/config   # strict TOML load + validation
                         ├──> internal/auth     # pairing, sessions, Access JWT/JWKS
                         ├──> internal/daemon   # lifecycle and runtime state
                         ├──> internal/herdr    # typed Herdr socket client and models
                         ├──> internal/state    # snapshot/run projections + generations
                         ├──> internal/server   # routes, middleware, run API, audit
                         ├──> internal/terminal # Herdr terminal-controller bridge
                         ├──> internal/tunnel   # cloudflared process and modes
                         ├──> internal/security # redaction and ANSI filtering
                         └──> internal/webui    # embedded production PWA
web/                # React + TypeScript + Vite + Tailwind v4 + xterm.js PWA,
                    # embedded into the Go binary (no runtime CDN)
```

The **state engine** polls `session.snapshot` (1.5 s while active, relaxing to
12 s when idle); Herdr events only trigger a debounced immediate poll. Snapshots
are the source of truth, so a missed event costs one interval, never correctness.
Per-pane lifecycle generations guard every mutation and terminal input against a
pane that exited, closed, moved to a new ID, or changed occupant.

## Development

The repository pins its toolchain with [mise](https://mise.jdx.dev): the **Go
1.26** series and **Node.js 22.23.1**. Run `mise install` once to match them, or
prefix commands with `mise exec --`. CI and the release workflow pin the exact Go
patch **1.26.5** (building from source requires **Go 1.26.5+**, because earlier
1.26 patch releases contain reachable standard-library vulnerabilities) and
**Node.js 22.23.1** (Vite 7 requires Node ≥ 22.12; a source build accepts Node
22.12+ on the 22 line or 24+).

```sh
make help          # list targets
make check         # fmt, tidy, vet, typecheck, lint, race tests, coverage,
                   #   frontend tests, embedded build, shell syntax, install smoke
make fmt           # format Go sources
make lint          # go vet + frontend lint
make typecheck     # TypeScript typecheck (tsc --noEmit)
make test          # go test ./...
make test-race     # go test -race ./...
make test-web      # frontend unit and component tests
make test-e2e      # Playwright mobile journeys (Chromium Pixel 7, WebKit iPhone 15)
make screenshots   # explicitly refresh tracked light/dark visual-review captures
make coverage      # coverage.txt with an enforced 80% Go threshold
make build-web     # locked frontend install + build into web/dist
make build         # build ./bin/herdr-phone with the frontend embedded
make verify-plugin # validate the manifest against Herdr in isolated state
make clean         # remove build, coverage, and frontend artifacts
```

`make check` runs every gate that needs no network or credentials. Tests are
deterministic and require no real Cloudflare account, Access identity, tunnel
token, Herdr session, or browser: Go tests use fakes (Herdr, a fake
`cloudflared` on `PATH`, generated JWKS, injected clocks) and the frontend uses
component tests plus Playwright on mobile viewports.

`make verify-plugin` links the plugin into a throwaway Herdr state directory and
confirms it and its global actions are discoverable, without touching your active
Herdr session. By default it downloads the **pinned official Herdr v0.7.5**
(verified SHA-256) for reproducibility; set `HERDR_BIN=/path/to/herdr` to use a
specific binary, or `HERDR_USE_PATH=1` to use a `herdr` from your `PATH`.

The install script (`scripts/build.sh`) builds from source when compatible Go and
Node toolchains are present and otherwise downloads a checksum-verified release
archive; `scripts/smoke-install.sh` exercises that download-and-verify path
offline with locally generated fake assets.

## Releasing

Releases are cut by pushing an **annotated** (or signed) `vX.Y.Z` tag whose
version matches `herdr-plugin.toml` and the binary's build info, on a commit that
is already on `main`:

```sh
git tag -a v0.3.0 -m "herdr-phone v0.3.0"
git push origin v0.3.0
```

The release workflow requires an annotated/signed tag whose commit is on `main`,
verifies the version match, runs the full quality gates, plugin verification, and
`govulncheck` (source and built binaries), and uses GoReleaser to publish macOS
`amd64`/`arm64` `.tar.gz` archives, `checksums.txt`, and an SBOM. It then records a
keyless GitHub build-provenance attestation over the archives and `checksums.txt`
(see [Verifying a downloaded release](#verifying-a-downloaded-release)). CI never
bumps versions or pushes commits.

Supply-chain hygiene: every GitHub Action is pinned to a full commit SHA (with a
version comment, kept current by Dependabot), `govulncheck` is pinned to a
released version, workflows default to `contents: read` with write scope only on
the publish job, and forks never receive secrets (`pull_request`, not
`pull_request_target`).

## Troubleshooting

- **`herdr plugin` is missing / plugin commands not found.** Some Homebrew
  `herdr` 0.7.5 bottles report the right version but omit the `plugin` command.
  Verify with `herdr plugin`. If absent, install the official Herdr release
  binary. This plugin never modifies your Herdr installation automatically.
- **`cloudflared` not found.** It is never auto-installed. Run
  `herdr-phone doctor` for exact guidance, or `brew install cloudflared`.
- **Named mode refuses to start.** It requires an HTTPS `public_url`,
  `auth.access.enabled = true` with `team_domain` and `audience`, and exactly one
  tunnel credential strategy. `doctor` reports which requirement is unmet.
- **`403` / Access denied in the browser.** Your Cloudflare Access policy must
  allow your identity, and `allowed_identities` (if set) must list your exact
  email. Access is deny-by-default.
- **`401` / requests fail after a while (named mode).** Your Cloudflare Access
  session expired. Reload the public URL and re-authenticate through Access; the
  origin re-validates the JWT on every request. A pairing link is not the remedy in
  named mode.
- **"End session" or the idle lock did not lock me out (named mode).** Expected,
  and deliberate: the relay re-provisions a session from your still-valid Access
  identity. To actually end access, revoke the Access session in Cloudflare Zero
  Trust or run `herdr-phone stop`. See
  [Session lifetime in named mode](#session-lifetime-in-named-mode).
- **Quick Tunnel won't start.** Set both `quick_enabled = true` and start with
  `--quick`. Remember a Quick Tunnel has no edge identity and is for testing only.
- **Terminal shows a conflict when opening a pane.** Only one controller owns
  input at a time. Confirm the explicit takeover to seize it.
- **The public URL doesn't reach this instance (quick mode).** Herdr Phone
  verifies the public URL against a one-time instance probe before printing the
  pairing link; a mismatch means a stale or foreign tunnel — stop and restart.
- **The pairing link expired (quick mode).** The secret is single-use. Run
  `herdr-phone setup-link` for a fresh one.
- **Something is compromised.** Follow the revocation steps in
  [SECURITY.md](SECURITY.md): stop the daemon, revoke Access sessions, and rotate
  the tunnel token.

## Non-goals (v0.3.0)

No Windows host support; no native iOS/Android apps, APNs, or background push
actions; no multi-user collaboration or simultaneous terminal controllers; no
automatic Cloudflare tunnel/DNS/Access provisioning; no automatic `cloudflared`
installation or self-update; no start-at-login or reboot survival without user
configuration; no persistent or on-disk app sessions (they stay in daemon memory);
no multi-session Herdr aggregation; no parsing of agent-specific approval screens
into native controls *in the default build* (see
[the experimental opt-in](#experimental-reading-the-agents-output-as-a-chat), which
is off unless you enable it and is never treated as authoritative); no blind one-tap
approvals in any configuration; and no file browsing beyond directory selection,
file upload, clipboard image transfer, or arbitrary downloads. See
[SPEC.md](SPEC.md) §21 for the full list.

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) and the
[Code of Conduct](CODE_OF_CONDUCT.md). Because this tool grants remote
shell-equivalent access, every change is reviewed for its effect on the security
posture.

## License

[MIT](LICENSE) © Matheus Monteiro and contributors.
