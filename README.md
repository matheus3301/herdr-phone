# herdr-phone

[![CI](https://github.com/matheus3301/herdr-phone/actions/workflows/ci.yml/badge.svg)](https://github.com/matheus3301/herdr-phone/actions/workflows/ci.yml)
[![Release](https://github.com/matheus3301/herdr-phone/actions/workflows/release.yml/badge.svg)](https://github.com/matheus3301/herdr-phone/actions/workflows/release.yml)
[![Go 1.26](https://img.shields.io/badge/go-1.26-00ADD8?logo=go)](https://go.dev/)
[![Herdr 0.7.5+](https://img.shields.io/badge/herdr-0.7.5%2B-5B4FE9)](https://herdr.dev)
[![cloudflared](https://img.shields.io/badge/cloudflared-2026.7.2-F38020?logo=cloudflare)](https://github.com/cloudflare/cloudflared)
[![License: MIT](https://img.shields.io/badge/license-MIT-green)](LICENSE)

**Herdr Phone** lets one authenticated operator drive the [Herdr](https://herdr.dev)
session running on their Mac from a phone. See your Spaces, tabs, panes,
worktrees, and agents; open a real interactive terminal; prompt and control
coding agents; and safely create, rename, move, resize, zoom, split, and close
Herdr resources — one-handed, from anywhere.

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
> security bar is that of an SSH client, not a read-only dashboard. Treat the
> public URL, your Cloudflare Access identity, and the pairing link like a root
> login. Read [SECURITY.md](SECURITY.md) before you expose it.

## Contents

- [Features](#features)
- [How it works](#how-it-works)
- [Prerequisites](#prerequisites)
- [Install](#install)
- [Choosing a front door](#choosing-a-front-door)
  - [Named tunnel with Cloudflare Access (recommended)](#named-tunnel-with-cloudflare-access-recommended)
  - [Origin JWT configuration](#origin-jwt-configuration)
  - [Quick Tunnel (testing only, opt-in)](#quick-tunnel-testing-only-opt-in)
- [Providing the tunnel token](#providing-the-tunnel-token)
- [Configuration](#configuration)
- [Running: start, stop, status](#running-start-stop-status)
- [Pairing and QR](#pairing-and-qr)
- [Installing the PWA on your phone](#installing-the-pwa-on-your-phone)
- [Feature guide](#feature-guide)
- [Security model](#security-model)
- [Architecture](#architecture)
- [Development](#development)
- [Releasing](#releasing)
- [Troubleshooting](#troubleshooting)
- [Non-goals (v0.1.0)](#non-goals-v010)
- [Contributing](#contributing)
- [License](#license)

## Features

- **Full topology.** Spaces/workspaces, tabs, panes, worktrees, and agents, kept
  live from Herdr's `session.snapshot` with events as wakeups.
- **A real terminal.** A fully interactive xterm.js terminal bridged to Herdr's
  supported terminal controller — resize with the viewport, scroll, reconnect,
  and explicitly take over an existing controller.
- **Blocked-first herd view.** Agents that need you lead; working agents follow;
  quiet agents collapse. Opening an agent shows the terminal before any response
  controls — no blind one-tap approvals.
- **Safe structural controls.** Create, rename, move, resize, zoom, split, swap,
  and confirmed-close, each with an explicit, single-use server confirmation for
  destructive actions.
- **Secure by default.** Loopback-only origin, mandatory pairing in every mode,
  Cloudflare Access with origin-side JWT validation for named tunnels, strict
  Origin/CSRF/CSP, and terminal escape-sequence filtering.
- **Self-contained.** One static binary with the PWA embedded — no runtime CDN,
  no analytics, no telemetry, no hidden network access.
- **macOS, amd64 and arm64** for v0.1.0.

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

- **macOS** (Apple silicon or Intel). v0.1.0 is macOS-only.
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

The plugin registers global actions (`start`, `start-quick`, `stop`, `status`,
`setup-link`, `doctor`) and installs **no** default keybinding and **no**
long-running pane.

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
  gh attestation verify herdr-phone_0.1.0_darwin_arm64.tar.gz \
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
| App pairing | Required | Required |
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
   and prints an authenticated pairing URL.

### Origin JWT configuration

Cloudflare Access is an **edge** control. Herdr Phone does not trust it blindly:
in named mode the origin cryptographically validates the Access JWT on **every**
HTTP request and WebSocket handshake, so a request that somehow reaches loopback
directly is still rejected without a valid token.

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
by Cloudflare for testing and development only. Herdr Phone still requires app
pairing for a Quick Tunnel, but pairing is the *only* barrier — there is no edge
identity in front of it.

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
session_ttl = "12h"
idle_lock = "30m"
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
```

Validation highlights:

- `server.host` must be exactly `127.0.0.1` in production; the port must be free.
- **Named mode** requires an HTTPS `public_url`, `auth.access.enabled = true`,
  and exactly one credential strategy (config/credentials, token file, or token
  command).
- **Quick mode** requires `quick_enabled = true`, ignores Access configuration,
  and still requires pairing.
- Durations are positive and bounded; `poll_hot` is at least 250 ms.
- Allowed workspace roots must exist and must not escape via symlink.

## Running: start, stop, status

```text
herdr-phone start [--quick] [--foreground]   # start (and supervise) the daemon
herdr-phone stop                             # graceful shutdown via the control socket
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
  a detached `herdr-phone serve`; waits for private readiness; and prints an
  authenticated pairing URL. It is **idempotent** — if the daemon is already
  healthy it returns the current mode and URL, reconciling stale state via the
  control socket and process identity before replacing it.
- **`stop`** requests graceful shutdown through the private control socket (it
  does not kill an arbitrary PID). New requests stop, WebSockets close, terminal
  controllers are released, `cloudflared` is asked to terminate within the grace
  period, and remaining process groups are killed.
- **`status`** reports current mode, public URL, and the readiness of HTTP,
  Herdr, the tunnel, and the state engine to an authenticated caller or local
  status. `--json` emits machine-readable output.
- **`doctor`** checks configuration and connectivity and prints exact
  Homebrew/manual guidance if `cloudflared` is missing. It never prints secrets.

The daemon does **not** start at login in v0.1.0 (an optional LaunchAgent is
documented as future work but never generated silently).

## Pairing and QR

Every daemon instance creates a fresh 256-bit single-use pairing secret.
`setup-link` prints both a URL and a best-effort terminal QR code:

```text
https://herdr.example.com/#pair=<base64url-secret>
```

The secret rides in the URL **fragment** (`#pair=…`), which browsers never send
in an HTTP request. Open the link on your phone (scan the QR); the app removes the
fragment from history and exchanges it for an HttpOnly, Secure, `SameSite=Strict`
`__Host-` session cookie. The secret is single-use and rotates on success.

In **named mode** you must first pass Cloudflare Access (sign in as an allowed
identity); pairing then binds the session, and the Access JWT is re-validated on
every subsequent request and reconnect. In **quick mode** pairing is the only
gate. To hand out a fresh link at any time, run `herdr-phone setup-link` again.

## Installing the PWA on your phone

Herdr Phone is a Progressive Web App: `display: standalone`, maskable icons, and
`viewport-fit=cover`. After pairing, add it to your home screen for a full-screen,
app-like experience.

- **iOS (Safari):** tap **Share** → **Add to Home Screen**.
- **Android (Chrome):** tap the **⋮** menu → **Install app** (or **Add to Home
  screen**).

The app shell can be cached offline; API and terminal data are never cached. The
app never trusts `navigator.onLine` — it revalidates on `visibilitychange`,
`pageshow`, `focus`, `online`, `freeze`, and `resume`, and reconnects with
jittered exponential backoff.

## Feature guide

Everything is driven by explicit resource IDs; Herdr UI focus is never relied on.

- **Spaces/workspaces:** list, switch/focus, create with cwd and label, rename,
  and confirmed close. Worktree provenance and aggregate agent status are shown.
- **Tabs:** list in authoritative server order, switch/focus, create, rename,
  move, and confirmed close.
- **Panes:** list and render layout, focus, split right/down, resize, zoom, swap,
  move to another tab / a new tab / a new workspace, rename, and confirmed close.
  Open a fully interactive terminal, resize with the viewport, scroll, reconnect,
  and explicitly take over an existing controller.
- **Agents:** blocked-first list with state, kind, name, title, location, cwd,
  and last transition. Focus/open terminal, prompt, send validated logical keys,
  rename, and start a **server-discovered** agent kind in an available pane (no
  hard-coded kind list).
- **Worktrees:** list, create, open, and confirmed remove. Removing a dirty
  worktree requires a second, explicit force confirmation.

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
  **re-validating the Access JWT** on every request and reconnect.
- **Mandatory pairing** in every mode, with a single-use fragment secret and an
  HttpOnly `__Host-` session cookie; sessions live only in daemon memory.
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
cmd/herdr-phone ──> internal/app          # CLI and orchestration
                    ├──> internal/config    # strict TOML load + validation
                    ├──> internal/auth      # pairing, sessions, Access JWT/JWKS
                    ├──> internal/daemon     # lifecycle, control socket, runtime state
                    ├──> internal/herdr      # typed Herdr socket client and models
                    ├──> internal/state      # snapshot cache, event wakeups, generations
                    ├──> internal/server     # HTTP, WebSocket, protocol, audit
                    ├──> internal/terminal   # Herdr terminal-controller bridge
                    ├──> internal/tunnel     # cloudflared process and modes
                    ├──> internal/security   # middleware, redaction, ANSI filtering
                    └──> internal/buildinfo  # one version source
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
git tag -a v0.1.0 -m "herdr-phone v0.1.0"
git push origin v0.1.0
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
- **Paired but requests fail after a while (named mode).** Sessions expire at the
  earlier of `session_ttl` and the Access JWT expiry. Re-authenticate through
  Access; the origin re-validates the JWT on every request.
- **Quick Tunnel won't start.** Set both `quick_enabled = true` and start with
  `--quick`. Remember a Quick Tunnel has no edge identity and is for testing only.
- **Terminal shows a conflict when opening a pane.** Only one controller owns
  input at a time. Confirm the explicit takeover to seize it.
- **The public URL doesn't reach this instance (quick mode).** Herdr Phone
  verifies the public URL against a one-time instance probe before printing the
  pairing link; a mismatch means a stale or foreign tunnel — stop and restart.
- **The pairing link expired.** The secret is single-use. Run
  `herdr-phone setup-link` for a fresh one.
- **Something is compromised.** Follow the revocation steps in
  [SECURITY.md](SECURITY.md): stop the daemon, revoke Access sessions, and rotate
  the tunnel token.

## Non-goals (v0.1.0)

No Windows host support; no native iOS/Android apps, APNs, or background push
actions; no multi-user collaboration or simultaneous terminal controllers; no
automatic Cloudflare tunnel/DNS/Access provisioning; no automatic `cloudflared`
installation or self-update; no start-at-login or reboot survival without user
configuration; no multi-session Herdr aggregation; no parsing of agent-specific
approval screens into native controls; and no file browsing beyond directory
selection, file upload, clipboard image transfer, or arbitrary downloads. See
[SPEC.md](SPEC.md) §21 for the full list.

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) and the
[Code of Conduct](CODE_OF_CONDUCT.md). Because this tool grants remote
shell-equivalent access, every change is reviewed for its effect on the security
posture.

## License

[MIT](LICENSE) © Matheus Monteiro and contributors.
