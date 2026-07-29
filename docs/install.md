# Install Herdr Phone (agent-executable guide)

Install, configure, and start [Herdr Phone](https://github.com/matheus3301/herdr-phone)
— a Herdr plugin that puts one Mac's Herdr session on the operator's phone behind a
Cloudflare tunnel — on this machine.

Hand this file to a coding agent. Humans can fetch it with:

```sh
curl -fsSL https://raw.githubusercontent.com/matheus3301/herdr-phone/main/docs/install.md
```

## Read before executing

- **This grants remote shell-equivalent access.** Anyone who reaches the public URL
  and clears its authentication can run arbitrary commands as this macOS user. Set up
  named mode with Cloudflare Access, as written below. Do not substitute a Quick
  Tunnel, do not set `auth.access.enabled = false`, and do not widen
  `allowed_identities`.
- **Never print, echo, log, or commit the tunnel token.** Store it in the macOS
  Keychain (step 3) and reference it from config. `config.toml` holds no secret.
- **Never write config into a git repository.** It belongs in the plugin config
  directory resolved in step 3.
- Steps are ordered. If a step's verification fails, stop and report it rather than
  working around it.
- Four values must come from the human operator (they live in the Cloudflare Zero
  Trust dashboard, which you cannot provision — that is a deliberate non-goal):
  the public hostname, the Access **team domain**, the Access application
  **audience (AUD) tag**, and the **tunnel token**. Ask for all four before step 3.

## 1. Verify these prerequisites first

Run every check. All must pass before installing.

| # | Check | Command | Required result |
|---|---|---|---|
| 1 | macOS host | `uname -s` | `Darwin` (Herdr Phone is macOS-only) |
| 2 | Herdr installed | `herdr --version` | v0.7.5 or newer |
| 3 | Herdr `plugin` command works | `herdr plugin --help` | Exit 0 with usage output |
| 4 | `cloudflared` present | `cloudflared --version` | Prints a version |

A running Herdr session is also required — Herdr Phone relays one live session. That
is verified by `doctor` in step 3d, not here.

If check 3 fails, some Homebrew `herdr` 0.7.5 bottles report the right version but
omit the `plugin` command. Install the official Herdr release binary instead. Do not
modify the Herdr installation any other way.

If check 4 fails, install `cloudflared` and re-run the check:

```sh
brew install cloudflared
```

`cloudflared` is **never** installed automatically by this plugin. Do not download it
by any other route.

Also confirm from the operator that a Cloudflare **named tunnel** exists with a
public hostname routing to `HTTP → 127.0.0.1:8787`, and that a **self-hosted Access
application** protects that hostname with an Allow policy matching only their
identity. If either is missing, stop and ask them to create it
([README: Named tunnel with Cloudflare Access](../README.md#named-tunnel-with-cloudflare-access-recommended)).

Tell the operator to set that Access application's **session duration**
deliberately while they are in the dashboard. In named mode it is the only thing
that times their phone out: `server.idle_lock` and the in-app "End session" button
do not end access, because the relay re-provisions a session from the still-valid
Access identity ([details](../README.md#session-lifetime-in-named-mode)). A short
duration is the equivalent of a screen lock on a live shell.

## 2. Install the plugin

```sh
herdr plugin install matheus3301/herdr-phone
```

This builds `bin/herdr-phone` from source when compatible Go (1.26.5+) and Node
(22.12+) toolchains are present, and otherwise downloads the macOS release archive
for this architecture and verifies its SHA-256 against `checksums.txt`, failing
closed on a mismatch. It never runs `curl | sh`.

Verify the plugin and its actions are registered:

```sh
herdr plugin list
```

Expect `matheus3301.phone` with global actions `start`, `start-quick`, `stop`,
`toggle`, `status`, `setup-link`, and `doctor`.

## 3. Write the configuration

### 3a. Store the tunnel token in the Keychain

**Do not handle the token yourself.** Ask the operator to run this in their own
terminal and paste the token at the prompt — `-w` with no value makes `security`
prompt, keeping the token out of shell history and out of any process's argv:

```sh
security add-generic-password -a "$USER" -s herdr-phone-tunnel -w
```

Then verify it round-trips, without printing it:

```sh
security find-generic-password -s herdr-phone-tunnel -w >/dev/null && echo "token stored"
```

If that prints `token stored`, continue. If it fails, ask the operator to re-run
the `add-generic-password` command; do not ask them to paste the token to you.

### 3b. Resolve the config directory

The relay loads config from the first of `$HERDR_PLUGIN_CONFIG_DIR/config.toml`,
`$XDG_CONFIG_HOME/herdr-phone/config.toml`, `$HOME/.config/herdr-phone/config.toml`.
Resolve the directory in that order:

```sh
herdr plugin config-dir matheus3301.phone
```

Write `config.toml` inside whatever that prints. If that subcommand does not exist
on this Herdr build or prints nothing, use
`${XDG_CONFIG_HOME:-$HOME/.config}/herdr-phone` instead and create it with
`mkdir -p`. Either way, the file must **not** live inside a git repository.

### 3c. Write this minimal named-mode config

Replace every `REPLACE_*` placeholder with the operator's real value. Unknown keys
are rejected, so do not add keys speculatively — see
[`config.example.toml`](../config.example.toml) for the full commented reference.

```toml
[server]
# Must stay exactly 127.0.0.1: the origin is reachable only through the tunnel.
host = "127.0.0.1"
# Must match the port the tunnel's public hostname routes to.
port = 8787

[cloudflare]
mode = "named"
# The https URL of the Access-protected public hostname.
public_url = "https://REPLACE_WITH_YOUR_HOSTNAME"
# Exactly one credential strategy. This one reads the token from the Keychain entry
# created in step 3a; it is an argv array run directly, never through a shell.
token_command = ["security", "find-generic-password", "-s", "herdr-phone-tunnel", "-w"]

[auth.access]
enabled = true
# Access team domain, bare hostname with no scheme.
team_domain = "REPLACE_WITH_YOUR_TEAM.cloudflareaccess.com"
# The Access application's Application Audience (AUD) tag.
audience = "REPLACE_WITH_YOUR_ACCESS_AUD_TAG"
# Exact-match allowlist of verified Access identities. Keep it to the operator only.
allowed_identities = ["REPLACE_WITH_YOUR_EMAIL"]
```

Set restrictive permissions on the file you just wrote:

```sh
chmod 600 <config-dir>/config.toml
```

**Alternative credential strategy (only if the Keychain is unusable):** a mode-0600
token file instead of `token_command`. Provide exactly one of the two — never both.

```toml
[cloudflare]
token_file = "~/.config/herdr-phone/tunnel.token"
```

The file must be a regular file owned by this user and unreadable by group or other
(`chmod 600`).

### 3d. Validate before starting

```sh
herdr plugin action invoke matheus3301.phone.doctor
herdr plugin log list --plugin matheus3301.phone
```

`doctor` checks config, Herdr, `cloudflared`, and the state lock, and prints exact
guidance for anything unmet. It never prints secrets. Fix every reported problem
before step 4.

## 4. Start it and read the access URL

```sh
herdr plugin action invoke matheus3301.phone.start
```

Plugin action output goes to the plugin log, so read it there:

```sh
herdr plugin log list --plugin matheus3301.phone
```

Look for the line beginning `Open on your phone:`. In named mode that is the bare
public URL — **no pairing link and no `#pair=` fragment is involved.** Give the URL
to the operator to open on their phone:

```text
herdr-phone started in named mode.
Public URL: https://your-hostname

Open on your phone: https://your-hostname
Cloudflare Access signs you in; no pairing link is needed.
```

The operator signs in through Cloudflare Access on the phone; the relay then
provisions the app session from that verified Access identity, and the origin
re-validates the Access JWT on every subsequent request and WebSocket handshake.
Tell them to add the page to their home screen (iOS Safari: **Share → Add to Home
Screen**; Android Chrome: **⋮ → Install app**).

Confirm health and stop when asked:

```sh
herdr plugin action invoke matheus3301.phone.status
herdr plugin action invoke matheus3301.phone.stop
```

The daemon does not start at login. Starting it is always an explicit action.

## 5. Optional: bind a key to toggle it on and off

`matheus3301.phone.toggle` stops the relay when it is running and starts it (named
mode) when it is not. Binding it to a key makes daily use a single keystroke from
anywhere in Herdr — no terminal and no `herdr-phone` CLI needed. Bind it in the
operator's Herdr keymap (`~/.config/herdr/config.toml`) only if they ask:

```toml
[[keys.command]]
key = "prefix+p"
type = "plugin_action"
command = "matheus3301.phone.toggle"
description = "Phone: toggle on/off"
```

Herdr loads keybindings on config reload (restart Herdr if it does not take effect
immediately). Confirm the binding's key does not collide with an existing one before
writing it, and report the file you changed.

## Troubleshooting

| Symptom | Cause | Action |
|---|---|---|
| `cloudflared: command not found` at start, or `doctor` reports it missing | `cloudflared` is never auto-installed | `brew install cloudflared`, then re-run `doctor` |
| Named mode refuses to start | Named mode requires an `https` `public_url`, `auth.access.enabled = true` with `team_domain` and `audience`, and **exactly one** credential strategy | Run `doctor`; it names the unmet requirement. Check you did not set both `token_command` and `token_file` |
| `403` / Access denied in the phone browser | Cloudflare Access is deny-by-default | Verify the Access policy allows this identity **and** that `allowed_identities` lists their exact email. Both must match |
| `herdr plugin config-dir` not found or empty output | Older Herdr, or the plugin is not linked | Confirm step 2 succeeded (`herdr plugin list`), then fall back to `${XDG_CONFIG_HOME:-$HOME/.config}/herdr-phone/config.toml` |
| `401` / "no valid session" in named mode | The Access session expired; app sessions are capped at the Access JWT expiry | Reload the public URL and sign in to Access again. A pairing link is **not** the remedy in named mode |
| Operator reports "End session" or the idle lock did not lock them out (named mode) | Deliberate: named-mode session lifetime is delegated to Cloudflare Access, and a new session is re-provisioned from the still-valid Access identity | Not a bug — do not try to "fix" it in config. To end access: revoke the Access session in Zero Trust, or `herdr plugin action invoke matheus3301.phone.stop`. To shorten the window, lower the Access application's session duration |
| Start reports config errors mentioning an unknown key | Config is strict TOML | Remove the key; only keys in [`config.example.toml`](../config.example.toml) are valid |
| Unsure whether it is running | — | `herdr plugin action invoke matheus3301.phone.status` |

For the full security model, and for the revocation steps to follow if a session,
device, or tunnel token is compromised, read [SECURITY.md](../SECURITY.md).
