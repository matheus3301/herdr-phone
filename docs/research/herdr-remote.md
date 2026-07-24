# Audit — `dcolinmorgan/herdr-remote`

Research-only deep audit to inform a new Go plugin for herdr. No code was written or
modified. All claims cite exact files and line numbers against the repository state below.

## Repository state audited

- **Remote:** https://github.com/dcolinmorgan/herdr-remote
- **HEAD:** `f8739731394a10831a2e334e521717ffebe26bf1` — "Merge pull request #14 from dcolinmorgan/beta", 2026-07-20
- **Latest tag:** `v0.6.3` (HEAD is 3 commits past it)
- **Commits:** 101 (first 2026-06-21, last 2026-07-20 — ~1 month of work)
- **Tags:** 15 — `v0.1.0`, `v0.2.0`, `v0.2.1`, `v0.3.0`–`v0.3.6`, `v0.4.0`, `v0.5.0`, `v0.6.0`, `v0.6.2`, `v0.6.3` (**`v0.6.1` tag missing** though referenced in commits `3b3c98c`/`6df2b95`)
- **Authors** (`git shortlog -sne`): effectively solo — Daniel `dcolinmorgan@gmail.com` (87) + `daniel@example.com` placeholder (5) + noreply (4) ≈ 96 commits; Harald Rieber (3), Yechiel Levi (2)
- **Size:** 3.4 MB, 64 tracked files, ~6,566 LOC of source
- **License:** dual — AGPL-3.0-or-later **or** commercial (`LICENSE:1-8`)

## What it is

A herdr plugin that lets you **monitor and approve coding-agent panes from your phone,
menu bar, or Telegram**. It hooks the herdr event `pane.agent_status_changed`
(`herdr-plugin.toml:8-10`) and consists of four loosely-coupled components:

1. **Python relay** (`relay/herdr_relay.py`, 579 LOC) — asyncio bridge on the agent host.
2. **Web PWA** (`web/index.html`, 877 LOC) — full remote client, zero build step.
3. **iOS app** (`herdi-ios/`, SwiftUI, xcodegen) — LAN companion.
4. **macOS app** (`herdi-mac/`, SwiftPM, notch/menu-bar) — local controller w/ SSH fan-out.
5. **Telegram bot** (`relay/herdr_telegram.py`, 543 LOC) — chat-driven control.

Status/terminal output flows **out** to clients; approvals, keystrokes and text flow
**back** into herdr panes.

---

## 1. Architecture

Single-process asyncio server (`relay/herdr_relay.py:578-579`). `main()` (`:555-575`) starts:
a UDP endpoint on `127.0.0.1:8376` for the local plugin hook (`:559`, `UDPPlugin :528-533`,
`on_event.py:18-19`), a 2 s poll loop (`POLL_INTERVAL :44`, `poll_loop :230-236`), a push
task (`:563`), and a combined WebSocket+HTTP server on `0.0.0.0:8375` (`:564`, `WS_PORT :43`).
mDNS advertises `_herdr-remote._tcp` (`:536-552`). Agents are discovered by shelling
`herdr pane list` locally and once per SSH remote in `HERDR_REMOTES`
(`get_all_agents :193-197`, `get_agents_from_host :168-190`). Dependencies are PEP-723 inline
(`:2-5`: `websockets>=14.0`, `zeroconf`, `pywebpush`, `py-vapid`), run via `uv run` — no lockfile.

**Strengths:** clean single event loop; graceful signal-based shutdown (`:568-571`);
pluggable multi-transport ingestion (poll + UDP + WS + HTTP POST).

**Weaknesses:** all `herdr`/`ssh` calls are **blocking `subprocess.run`** on the event loop
(see §9); two independent Swift clients duplicate the protocol model (`herdi-ios/Sources/Models/Agent.swift`
is identical to `herdi-mac/Sources/Agent.swift`) — no shared package.

---

## 2. Protocol

**Transport:** WebSocket is primary (`serve(handle_client, …)` `:564`). The same port also
serves HTTP GET (static web assets, VAPID key) and HTTP POST event injection via
`process_request` (`:314-412`). UDP `127.0.0.1:8376` is the local plugin hook. No SSE — clients
are pushed frames, not polled.

**Server → client** (JSON, `broadcast :215-227`):
- `{"type":"agents","agents":[…]}` (`:245`)
- `{"type":"blocked",pane_id,agent,project,host,prompt,options}` (`:251-257`)
- `{"type":"pane_content",pane_id,content}` (`:471`), plus ack/`error` frames

**Client → server** (`handle_client :442-519`): `respond` (`:448`), `agent_event` (`:461`),
`read_pane` (`:463`), `send_keys` (`:472`), `send_text` (`:485`), `create_tab` (`:498`),
`push_subscribe`/`push_unsubscribe` (`:507-519`).

**Terminal output is not streamed.** Two paths: poll-driven `blocked` prompt (last 20
chrome-filtered lines capped at 500 chars, `read_pane :200-203`, `:255`) and on-demand
`read_pane` returning **raw, unfiltered, un-truncated** output (`:470-471`). No incremental/diff
streaming — every read re-fetches full scrollback.

**Approvals/commands back:** `respond` → `herdr pane send-text` (`:460`); `send_text` →
send-text (`:497`); `send_keys` → send-keys (`:484`).

**Functional bug — allowlist/client key mismatch:** `SAFE_KEYS` (`:74`) contains `"C-c"`,
`"Enter"`, `"Escape"`, etc., but the web client sends `"Space"` and `"ctrl+c"`
(`web/index.html:558,564,583`) and Telegram sends `"Ctrl+c"` (`herdr_telegram.py:193,336`). None
match `SAFE_KEYS`, so `send_keys` interrupt/space is **silently rejected** (`:478-479`) —
Ctrl-key control is effectively broken.

---

## 3. Tunnel / exposure model

**cloudflared** is the exposure mechanism. `start.sh:43-88` runs either a temp
`trycloudflare.com` tunnel (`--url http://localhost:$WS_PORT` `:62`) or a named tunnel (`:50`).
`install-service.sh` supports named/temp/none (`:199-464`), writes ingress config (`:637-645`),
and installs a persistent tunnel service (`:665-719`). Temp URL is scraped from
`/proc/$PID/fd/1` (`start.sh:71`, **Linux-only, fragile**) or `tunnel-stderr.log`
(`install-service.sh:813`). Named URL is `wss://$TUNNEL_HOSTNAME`.

**Weaknesses:** relay binds `0.0.0.0` (`:564`) so it is reachable on the LAN independent of the
tunnel; the tunnel URL is the **only capability needed** to reach the relay (see §4);
`trycloudflare` quick tunnels are unauthenticated public URLs exposing a terminal-control
channel to the internet, and neither doc warns to set a token.

---

## 4. Auth model — the weakest area

**Mechanism:** optional shared secret `HERDR_RELAY_TOKEN` (`:45`), checked in `process_request`
for every request incl. the WS upgrade (`:320-333`) via `Authorization: Bearer` or `?token=`
query. When set, it correctly gates the handshake **before** upgrade and returns 401 on mismatch
(a genuine strength), and write actions are audit-logged as JSONL (`audit :77-97`).

**Critical weaknesses:**
- **No auth by default.** `AUTH_TOKEN` defaults to `""` (`:45`); when empty the entire check is
  skipped (`if AUTH_TOKEN:` `:320`). Anyone with the tunnel/LAN URL can list agents, read panes,
  inject text, and interrupt.
- **Installer never sets a token.** `install-service.sh` omits `HERDR_RELAY_TOKEN` from both
  `config.env` (`:474-485`) and the launchd/systemd env (`:574-584`, `:607-610`). The default
  installed, tunnel-exposed service runs with **no authentication**, contradicting README's manual
  `openssl rand -hex 16` guidance (`README.md:101-104`).
- **Token in URL query string** (`web/index.html:341`; relay `:326-330`) → leaks into cloudflared
  logs, browser history, referrer.
- **Non-constant-time compare** `token != AUTH_TOKEN` (`:331`) — timing side-channel (minor).
- **No pairing flow, no per-client/per-host scoping.** One flat token; any authenticated client
  controls all panes on all hosts. Device identity is only a UA-sniffed label (`:421-437`).
- **iOS client has no auth at all** — no token/key/pairing anywhere in `herdi-ios/Sources`
  (`RelayConnection.swift:55-108`). Any LAN device resolving `_herdi._tcp` can drive live agents.
- **Open Telegram in discovery mode.** If `HERDR_TG_CHAT_ID` is unset, `authorized()` returns
  `True` and handlers use `filters.ALL` (`herdr_telegram.py:65-69,514-517`) — anyone who finds
  the bot controls agents. (Commit `4bce815` later restricted it to an authorized chat ID.)

---

## 5. Terminal rendering

**No terminal emulator anywhere in the stack.** The relay owns no PTY/tmux/scrollback — it
delegates to `herdr pane read --lines N --source recent` (`:201,470`). **No ANSI stripping or
parsing** on the relay, the web client, or either Swift client — escape sequences pass through as
literal text.

- **Web:** pane content dumped into a `<div>` via `el.textContent` (`web/index.html:399`, XSS-safe)
  styled `white-space: pre-wrap` (`:85`); polls every 3 s (`:539`); scroll-to-top `loadMore()` up
  to 5000 lines (`:707,:550`); on-screen key tray + slash-command palette (`:561-698,:713-766`).
- **iOS:** raw monospaced `Text` dump (`AgentDetailView.swift:27-29`, `ApprovalView.swift:13-15`);
  no scrollback buffering; "No output captured" is common because content is optional.
- **macOS:** truncated to last 6 non-empty lines / 500 chars (`RelayConnection.swift:151-162`); a
  naive `+`/`-`/`@@`-prefix diff highlighter (`NotchContentView.swift:566-568,771-810`).

**Weaknesses:** inconsistent filtering between the two relay read paths; `CHROME_RE` is a brittle
regex tied to a specific agent's chrome ("Kiro", `:59-65`); option detection is hardcoded to
Claude-Code TUI strings ("yes, single permission", "trust, always allow", `RelayConnection.swift:169-178`,
`NotchContentView.swift:660-710`) — breaks on any other agent or wording change.

---

## 6. Mobile UX & clients

**iOS** (`herdi-ios/`, iOS 17, Observation + SwiftUI): status-grouped triage — Needs You /
Working / Idle (`AgentListView.swift:8-10`); swipe-to-approve/reject mapped to `options.first`/`.last`
(`:130-143`); keyword-tinted option buttons (`ApprovalView.swift:65-69`); warning/medium haptics
(`HapticManager.swift:7-13`). Clean single-source-of-truth model.

**macOS** (`herdi-mac/`, macOS 14, SwiftPM, `LSUIElement` menu-bar): the **notch UI is the
best-engineered part of either client** — physical-notch detection, simulated notch for external
displays, `NotchPanelShape` squircle, `nonactivatingPanel` above the menu bar, hide-on-fullscreen,
click-outside-to-collapse, hover state machine (`NotchPanel.swift:60-175,231-249`;
`NotchContentView.swift:135-169,832-889`). Runs a dual **direct** (poll local `herdr` CLI every
2 s + SSH fan-out) / **relay** (WebSocket) mode (`RelayConnection.swift:38-125,198-210`).

**Core mobile flaw — notifications only work in the foreground.** Both clients fire a **local**
`UNNotificationRequest` with `trigger: nil` synchronously on a `blocked` WS message
(`NotificationManager.swift:11-17` iOS; `RelayConnection.swift:317-323` mac). **There is no APNs /
remote push** and **no `UIBackgroundModes`** in `Info.plist` (`herdi-ios/Sources/Info.plist:4-33`).
When backgrounded, iOS suspends the WebSocket → no messages → **no alerts**. For an app whose
entire purpose is "alert me when my remote agent is blocked," the alert only works while the app
is open and on-screen. This is the single biggest client weakness.

**Broken widget / Live Activity:** no `@main WidgetBundle` exists; the widget target compiles only
`HerdiWidget.swift` + attributes (`project.pbxproj:231-239`) while `HerdiLiveActivity.swift` (a
`Widget`) is wrongly compiled **into the main app target** (`:250`) where it can never register.
Live Activity is start-once, foreground-updated only, never `end()`ed, no ActivityKit push token
(`LiveActivityManager.swift:8-12`, `RelayConnection.swift:179`).

**Other client bugs:** false "connected" state set immediately after `task.resume()` before any
handshake (`RelayConnection.swift:92-96`); no WebSocket ping/keepalive on either client → dead
sockets undetected; optimistic in-place `Agent` mutation with no server ack
(`AgentDetailView.swift:76-78`); 1 s full `NSMenu` rebuild loop (`HerdiMacApp.swift:162-190`);
`fetchHistory` overloads pane text onto the `prompt` field (`RelayConnection.swift:110-114,195-198`).

---

## 7. Install flow

- **`start.sh`** (foreground dev): loads `~/.config/herdr-remote/config.env` (`:26`), launches
  relay via `uv run` (`:30`), starts cloudflared (`:43-88`), traps signals for cleanup (`:11-20`).
- **`install-service.sh`** (842 LOC, persistent): OS/binary detection (`:13-62`), optional
  cloudflared install (`:127-159`), interactive tunnel config (`:161-465`), writes `config.env`
  (`:474-485`), installs launchd plist (`RunAtLoad`+`KeepAlive` `:549-589`) or systemd `--user`
  unit (`Restart=always` `:597-619`) plus a tunnel service (`:625-730`), then a smoke test
  (`:736-820`).

**Documented onboarding paths diverge:** README's "10-second" path is a prebuilt `Herdi.app` DMG
drag-install (`README.md:9-15`); README's remote path uses the plugin + `start.sh` +
`herdr-demo.pages.dev` (`:40-45`); QUICKSTART's "60-second" path uses `git clone` +
`uv run herdr_relay.py` + a manual `cloudflared` tunnel + `herdr-remote.pages.dev`
(`QUICKSTART.md:1-55`).

**Footguns:** installer never sets a token → auth-less public service; `curl | mv` cloudflared
install with **no checksum verification** (`install-service.sh:139-147`); auto-`kill -9` of
whatever holds the port (`:500-537`); `sed`-injects `RunAtLoad` into arbitrary existing cloudflared
plists (`:329-333`); systemd unit is `--user` with no lingering enabled → dies on logout;
unquoted `for arg in $TUNNEL_ARGS` word-splitting (`:660`); Linux-only `/proc` URL scraping.

**Doc drift:** contradictory demo URLs (`herdr-demo.pages.dev` vs `herdr-remote.pages.dev`,
`README.md:5,45` vs `QUICKSTART.md:33`); env-var name drift (`HERDI_TG_*` in `CLAUDE.md:47` vs
`HERDR_TG_*` in README/QUICKSTART/`.env.example`); stale `HERDR_BIN` default (`CLAUDE.md:69`,
fixed by commit `1c85108`); README changelog stops at v0.6.0 and never documents the Mac-app
rewrite at HEAD.

---

## 8. Tests

`tests/run.sh` (115 LOC) is **smoke-only**: `ast.parse` syntax checks of the four Python files
(`:17,32,37,53`), PEP-723 marker grep (`:21`), executable-bit check (`:25`), Telegram
command-name greps (`:40-43`), env-var reference greps (`:46`), web-app feature greps (`:61`), a
hardcoded-secret grep for two specific strings (`:65`), Swift `-parse`, README link checks. **No
coverage** of the wire protocol, auth enforcement, the `SAFE_KEYS`/`SAFE_RESPONSES` allowlists,
injection, reconnection, or any relay runtime behavior. The installer's smoke test
(`install-service.sh:751-792`) is the only thing that actually connects to a running relay.

---

## 9. Security

- **Remote command injection (high).** `run_herdr` remote path builds `["ssh", …, remote, HERDR,
  *args]` (`:159`); ssh concatenates args into one string run by the remote login shell.
  `send_text` accepts arbitrary text ≤1000 chars (`:490-497`) with **no allowlist**, so `$(...)`,
  `;`, backticks are shell-interpreted on the remote host when `HERDR_REMOTES` is set. (Local path
  uses argv-list form `:161`, shell-safe.)
- **No auth by default + no installer token (high).** §4 (`:45,:320`; `install-service.sh:474-485`).
- **Open Telegram discovery mode (high).** `herdr_telegram.py:65-69,514-517`.
- **Transport is cleartext.** Relay is plaintext ws/HTTP (`:564`); TLS terminated only by
  cloudflared, so **no TLS on the LAN at all**. iOS uses `ws://` with no ATS config
  (`Info.plist:4-33`) — ATS will likely block on-device; macOS bakes `NSAllowsArbitraryLoads=true`
  (`build.sh:58-62`).
- **macOS self-update RCE (high).** `Updater` downloads the release `.dmg` over HTTPS but performs
  **no signature or checksum verification**, then mounts and `cp -R` over the running app and
  relaunches (`Updater.swift:32,97,105-133`); string-inequality version compare (`:83`). App is
  ad-hoc signed, unnotarized, unsandboxed (`build.sh:67-80`). A compromised release asset ⇒ code
  execution.
- **SSH credential handling (macOS).** Password passed to `sshpass -p <password>` as a **CLI arg**
  visible via `ps` (`RelayConnection.swift:107-110`), plus `StrictHostKeyChecking=no` → MITM.
  Keychain items set no `kSecAttrAccessible` (`KeychainHelper.swift`).
- **Web layer.** No CSP (`index.html:3-137`); runtime CDN dependency
  `import … from 'https://esm.sh/cuelume@0.1.2'` with no SRI (`:11`); `innerHTML` XSS surface from
  relay-provided fields (`:505-509,440,454,317`) under a trusted-relay assumption; token in query
  string; relay CORS `Access-Control-Allow-Origin: *` (`:346,385,411`).
- **Broken CORS gate (low).** `if request.path and "OPTIONS" in str(request.headers)` (`:344`)
  tests a substring of the header dump, not the HTTP method.
- **Genuine strengths.** `pane_id` validated against `known_panes` on all write ops
  (`:450,465,475,487`); `respond` allowlisted to `SAFE_RESPONSES` (`:454`); rotating logs +
  audit JSONL; **no secrets ever committed** (secret scan of all refs is clean — VAPID/tokens all
  from env, `:45,48-50`). `.pi-kiro-auth.js` reads kiro OAuth tokens read-only from local SQLite
  and writes them to `~/.pi/agent/auth.json` (`:23-26,76-77`) — no embedded secret but a
  credential-bridge / curl-pipe-to-node footgun.
- **Infra-identity leak.** Tracked `.wrangler` caches (despite `.gitignore:9`, added before the
  ignore rule) leak Cloudflare account ID `fcea65c3989c4078857c60bf0e0c54c9` and account name
  `graffold` (`.wrangler/cache/wrangler-account.json`, `web/.wrangler/cache/*`). Not a credential;
  should be untracked.
- **Deps unpinned** (`>=` ranges, no lock, `:4`).

---

## 10. Operational reliability

- **Event-loop blocking (high).** `run_herdr` uses synchronous `subprocess.run(timeout=15)`
  (`:162`) from async `_poll_once` (`:249`) and `handle_client` (`:460,470,484,497`). A slow SSH
  remote (`ConnectTimeout 5s`, `:159`) or hung `herdr` **freezes the entire relay** — all clients,
  polling, and pushes — for up to 15 s.
- **Backpressure (medium).** `broadcast` awaits each client `send` sequentially (`:218-220`); one
  slow/half-open socket delays all others. `event_queue` is unbounded (`:69`).
- **Reconnection (strength).** Clients auto-reconnect (web 3 s `index.html:344`; Telegram 5 s
  `herdr_telegram.py:505`; TUI 3 s `herdr_tui.py:156`; Swift capped exponential backoff
  `RelayConnection.swift:137-150` + iOS `NWPathMonitor` `:40-51`). Service auto-restart via
  launchd `KeepAlive` / systemd `Restart=always`. Dead-client cleanup (`:216-227`) and stale-pane
  GC (`:268-275`).
- **No liveness detection.** Neither Swift client sends WebSocket pings; dead sockets only noticed
  on next failed send/receive.
- **Logging (strength).** Rotating 5 MB × 3 file + console (`:31-39`) and separate audit JSONL.
- **Broken helper scripts.** `Makefile:7` runs non-existent `relay/herdi_relay.py` (typo) and
  `:4` installs from missing `relay/requirements.txt`; `herdr-dash.sh:14,20` launches
  non-existent `herdr-remote_tui.py` (actual: `herdr_tui.py`). `on_event.py` sends UDP only to
  `127.0.0.1` → plugin push works only for the **local** host; remote hosts rely on 2 s polling.
  Plugin manifests disagree on version (`herdr-plugin.toml:3`=0.5.0 vs `relay/herdr-plugin.toml:3`=0.1.0).

---

## 11. License

`LICENSE` (34 KB, 664 lines) is a **dual license**, which explains its size (`LICENSE:1-8`):
open source under **AGPL-3.0-or-later** (verbatim FSF text, `:11-664`), **or** a commercial
license by contacting the author. No custom clauses inside the GPL body. **Implication for a
re-implementation:** AGPL is strong copyleft with a network-use clause — running a modified relay
as a network service obligates source publication. A clean-room Go plugin must not copy AGPL code;
derive only from this behavioral spec, not from the source text.

---

## 12. Verdict — keep vs. filter for the new Go plugin

### Strengths worth keeping (as design, not code)

- **Relay architecture:** single async loop + signal-based graceful shutdown; multi-transport
  ingestion (plugin event push + poll fallback + client WS).
- **Protocol shape:** simple typed JSON message set (`agents` / `blocked` / `pane_content` +
  `respond` / `send_keys` / `send_text` / `create_tab`); server-side `pane_id` validation against
  known panes; `respond`/key allowlists (concept — but fix the mismatch).
- **Observability:** rotating logs + append-only audit JSONL of every write.
- **Reliability plumbing:** capped-backoff reconnect + network-change trigger; dead-client and
  stale-pane cleanup; launchd/systemd persistence with auto-restart; named-cloudflared-tunnel
  option.
- **UX patterns:** status-grouped triage (Needs You / Working / Idle); swipe-to-approve;
  keyword-tinted option buttons + ⌘-shortcut response grid + custom reply; the macOS notch panel
  engineering; two-tap danger-key confirm; responsive/accessible single-file web client;
  Keychain for macOS SSH secrets.

### Weaknesses to filter out

1. **Blocking subprocess calls on the event loop** — the Go plugin must run all `herdr`/`ssh`
   invocations concurrently (goroutines + `exec.CommandContext` with timeouts).
2. **Auth optional by default** — require a token/pairing out of the box; installer must generate
   and inject it; move it out of the URL query into headers/subprotocol; add per-client/per-host
   scoping; close Telegram discovery mode; give iOS real client auth.
3. **Remote command injection via `ssh host cmd *args` + unbounded `send_text`** — never build
   shell-interpreted remote commands; validate/bound all injected text.
4. **Cleartext transport everywhere** — TLS/`wss` end to end, not just at the tunnel edge.
5. **Foreground-only notifications** — redesign around server-side push (APNs/Web Push with real
   ActivityKit push tokens); fix the broken widget/Live-Activity target.
6. **No terminal semantics** — decide deliberately: either a real ANSI/VT parser + incremental
   streaming, or an explicit plaintext-only contract; do not leave raw escape codes rendering as
   garbage, and unify filtering across read paths.
7. **Self-update without signature verification** (macOS) — sign/notarize and verify update
   integrity (Sparkle EdDSA or equivalent).
8. **No real tests** — cover protocol, auth enforcement, allowlists, injection, reconnection.
9. **Broken/stale scaffolding** — fix Makefile/dash-script paths, plugin version skew,
   Linux-only `/proc` URL scraping, doc drift (demo URLs, `HERDI_`/`HERDR_` env vars,
   `HERDR_BIN`), and untrack the `.wrangler` caches leaking the Cloudflare account identity.
10. **No CSP + `innerHTML` XSS under a trusted-relay assumption + runtime CDN import** — treat the
    relay as untrusted at the client boundary; add CSP, escape output, pin/self-host deps.
11. **Duplicated protocol model across clients** — define the protocol once (a shared schema) and
    generate/share.

### Licensing constraint

The source is **AGPL-3.0-or-later**. Build the Go plugin clean-room from this behavioral audit;
do not port AGPL source. If any AGPL code is reused, the plugin inherits AGPL network-copyleft
obligations.
