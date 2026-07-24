# Research Audit: `0cv/herdr-mobile-relay`

**Purpose.** Deep, citation-backed audit of [github.com/0cv/herdr-mobile-relay](https://github.com/0cv/herdr-mobile-relay) to inform a **Go Herdr plugin backed by cloudflared** that lets a phone monitor and control Herdr agents. Research only — no code was written to that project. Citations are `path:line` against the upstream repo (audited at release **0.8.6**, HEAD `4ad54ae`).

---

## 1. Summary & Provenance

Herdr Mobile Relay is a per-computer remote-control system for [Herdr](https://herdr.dev) agents (Codex, Claude Code, OpenCode, and any installed Herdr integration). Each computer runs a **local Python relay** that polls the `herdr` CLI, serves an installable PWA, and accepts authenticated WebSocket connections; **Cloudflare Tunnel** provides the public HTTPS/WSS endpoint without opening an inbound port. There is **no central broker** — the phone connects to each relay directly and merges agents client-side (`README.md:9`).

- **Language split.** Backend: one 6,627-line PEP-723 Python script (`relay/herdr_relay.py`) + helpers (`relay/update_support.py` 1,097 lines, `relay/stable_state.py` 250, `relay/on_event.py` 53), fronted by ~15 interdependent bash scripts (`relay/*.sh`). Frontend: Svelte 5 + Vite 8 + Tailwind 4 PWA (`frontend/`), shipped as a committed single-bundle `web/`.
- **Provenance / license.** AGPL-3.0-or-later. It is a **modified fork of `dcolinmorgan/herdr-remote`** (`LICENSE:1-6`), Copyright (C) 2026 Christophe Vidal. AGPL §13 (network use) matters: **a Go derivative that keeps AGPL code must offer its source to remote users.** A clean-room Go reimplementation of the *ideas* (not the code) avoids the copyleft obligation; this audit is written to enable that.
- **Maturity.** Young but active and disciplined: 118 commits over **2026-07-09 → 2026-07-22** (a ~2-week burst), primary author Christophe Vidal (71 commits) plus the original herdr-remote author Daniel (44) and two minor contributors. Releases are commit-tagged in messages, not git tags: `0.4.2` → `0.7.0` → `0.8.6` (`git log`). CI is green-gated (`README.md:3`). No published CVEs; security posture is self-asserted (`README.md:218-229`).

---

## 2. Architecture

Single asyncio process (`websockets>=14`, deps declared inline via PEP 723 at `relay/herdr_relay.py:2-5`). One `serve(handle_client, WS_HOST, WS_PORT, process_request=…)` on `127.0.0.1:8375` serves **both** the PWA over HTTP and the WS control channel on one port (`relay/herdr_relay.py:6602`); `process_request` branches on `is_websocket_upgrade` (`:4695-4707`).

- **Two ingest paths.** (1) A **poll loop** shells `herdr pane list` + `herdr tab list` on an adaptive interval — 2s when any agent is active, ≥15s idle (`:99-100`, `:4340-4354`), shortened on demand via a `poll_wakeup` event (`:4336`). (2) A **UDP event path**: the plugin hook (`relay/on_event.py:36-53`) fires a datagram to `127.0.0.1:8376` on `pane.agent_status_changed`, so blocked/finished states arrive instantly instead of at next poll (`herdr-plugin.toml:136-138`). Both mutate the same transition caches under one short-held `agent_state_application_lock`; slow enrichment happens after release and re-validates state revision (`:687-727`).
- **Fan-out with backpressure.** `broadcast` → single `outbound_queue` → **per-client** FIFO buffers (`ClientOutboundBuffer`, `:3810-3853`) bounded to 64 items / ~2 MB; overflow fails the client. Notable: consecutive `agents` snapshots **coalesce** — a newer full snapshot replaces the queued one so a slow client gets latest state, not a backlog (`:3829-3835`). Identical snapshots are diffed away before send (`:4305-4314`). Sends are wrapped in a 5s timeout; dead sockets are poisoned and aborted (`:3767-3800`).
- **Subprocess model.** *All* Herdr interaction is `subprocess.run([HERDR, *args], …)` with a list argv and **never `shell=True`** (`:730-741`); blocking calls offloaded via `asyncio.to_thread` (`:749-792`). `HERDR` is resolved from `HERDR_BIN` or a fixed candidate list (`:80-94`).
- **Supervision.** Long-lived loops run under `supervise()` (`:6547-6580`) which restarts a crashed loop with backoff and logs a traceback, so a background failure can't silently freeze a still-"healthy" WS.
- **Health endpoints (unauthenticated).** `/health` liveness, `/healthz` rich JSON (instance, version, release, revision, protocol, inventory readiness), `/readyz` returns 503 until a pane inventory succeeds (`:4710-4750`). This clean liveness/readiness split is the contract the updater and stable-tunnel wizard verify against.

**Adopt:** single-port PWA+WS; `/health`+`/healthz`+`/readyz` split; per-client bounded queues with latest-snapshot coalescing for high-churn state; adaptive poll + explicit wake; supervised background workers; list-argv subprocess (no shell).
**Avoid:** a fresh `herdr` subprocess per operation — `send_question_keys` spawns **one process per keystroke with a 150 ms sleep between** (`:5172-5187`), so a multi-select answer is dozens of forks; poll uses two non-atomic subprocesses (panes then tabs) that can momentarily disagree; a hung Herdr can stall poll ~30 s (two 15 s timeouts). A Go plugin talking to Herdr over its **Unix socket / long-lived RPC** (the relay already knows the socket path, `:103-108`, but only uses it to validate event origin) would be far cheaper.

---

## 3. Protocol

`PROTOCOL_VERSION = 2` (`relay/herdr_relay.py:378`), advertised in a `push_config` handshake alongside a **`capabilities` array** (`clear_activities`, `directory_browser`, `self_update`, `structured_questions`, `slash_commands`, conditional `app_deploy`) so the client feature-detects independently of the version number (`:369-375`, `:6317-6332`). The frontend injects `APP_PROTOCOL_VERSION` at build time and must match exactly (`frontend/src/lib/config.ts:19`, `frontend/vite.config.ts:109`).

- **Mutating vs read messages.** An explicit `MUTATING_MESSAGE_TYPES` frozenset (`:379-399`) gates writes: mutations from a protocol-mismatched client are rejected, **reads are always allowed** (forward compatibility), and `install_update` is deliberately exempt so a stale app can still trigger the relay update that fixes it (`:6366-6368`).
- **Two meanings of "protocol_mismatch".** App↔relay (above) vs relay↔herdr — the latter is detected in Herdr stderr and surfaced with an actionable remediation string telling the user to run `herdr server live-handoff` (`:807-821`).
- **Questions/approvals/prompts/keys.** Structured questions (Claude Code + Codex only) are parsed into an interaction with a **content-hashed `id`** (sha256 of semantic content, `:1525-1544`) so ordinary redraws aren't seen as new questions and stale answers are rejected when the hash changes (`:5562-5569`). Answers are executed by **driving the TUI cursor** (Up/Down to move focus, Enter to toggle, Ctrl+U + `send-text` for "Other"). Approvals map to positional keys — Enter for first option, Escape for last, `Down*n+Enter` otherwise (`:3737-3749`) — because the agent menus ignore letter shortcuts. Approvals are **idempotent**, keyed on `event_id` with a `dispatched_unknown` phase to prevent double-approval on retry (`:5796-5821`).
- **Request correlation.** Every command carries a validated `request_id` (`[A-Za-z0-9._:-]{1,120}`, else auto-generated, `:4782-4786`) and resolves via `command_result`. The client uses a **two-phase timeout**: an `accepted` phase resets the timer to a fresh 10 s window before final confirmation (`frontend/src/lib/store.ts:779-789`).

**Adopt:** versioned protocol + capability array; gate only mutations on version match, exempt self-update; content-hashed interaction IDs; idempotent `event_id` approvals; two-phase accepted/confirmed timeouts; clear mismatch remediation strings.
**Avoid:** exact-equality protocol check with no negotiation window — every bump hard-breaks all older clients in lockstep (`frontend/src/lib/protocol.ts:6`); positional approval mapping silently sends the wrong answer if a menu's option order differs (`:3737-3749`); untyped `Record<string,any>` message handling on the client.

---

## 4. Terminal Rendering

The distinctive engineering is making a desktop TUI legible on a phone — done in two halves.

**Backend (scrollback reconstruction).** Herdr exposes only a scrolling viewport, so the relay stitches full scrollback per pane and persists it to `~/.cache/herdr-mobile-relay/claude-history/<pane>.json` (survives restarts, atomic 0600 writes, 7-day prune tied to live inventory) (`relay/herdr_relay.py:1148-1199`). The algorithm (`merge_claude_history`, `:1211-1280`):
- `claude_tail_overlap` finds the largest `k` where the stored tail equals the new frame's head — the invariant of scrolling output, immune to earlier-repeating content (`:1100-1113`);
- fallback `claude_sequence_match` uses `difflib` with a **latest-anchor tie-break** so repeated session text doesn't pin to a stale early match (`:1080-1097`);
- an ambiguous divergent tail is **refused once then rebased** — distinguishing a transient scroll from a real rewrite like Claude collapsing an answered approval box (`:1259-1270`);
- while a question is active it serves the **live frame verbatim** (never merges a mutating question viewport into scrollback) (`:1283-1296`).
Question/approval detection is **regex scraping of vendor TUI chrome**, including reading raw SGR codes (`\x1b[48;…m` backgrounds, `\x1b[38;5;6m` cyan) to find the highlighted tab or "unanswered" state (`:583-631`, `:1371-1760`). A "form still at pane tail" liveness check prevents resurrecting a closed question from scrollback (`:1406-1413`). The backend tags how many trailing lines are chrome via `terminal_chrome_metadata` (`:1122-1128`).

**Frontend (mobile reflow).** A bespoke ANSI-to-HTML renderer, **no xterm.js** (`frontend/src/lib/terminal.ts`, 594 lines). `renderTerminalContent` strips Codex desktop input boxes, trims box-drawing chrome, reflows wrapped lines into paragraphs, collapses separator/decoration runs, and normalizes near-white/near-black colors for light-theme contrast (`terminal.ts:135-159,236-280,575-594`). Using the backend-supplied `desktop_footer_lines`/`desktop_prompt_lines`, `claudeMobileTerminalContent` slices the persistent footer/status/prompt block off the scrollback (`terminal.ts:103-133`). Sticky-bottom autoscroll snaps to bottom only within 48 px, defers frames while the composer is focused, and re-reads the pane every 3 s (`frontend/src/components/TerminalView.svelte:122,129-182`).

**Adopt:** tail-overlap-first + fuzzy-fallback stitching with latest-anchor tie-break; persist stitched history per pane; serve live frame verbatim during questions; backend-supplied chrome line-counts as a clean client contract; the whole mobile-reflow pipeline (footer stripping, border trim, paragraph reflow); sticky-within-48px + defer-while-composing; no-xterm keeps the bundle tiny.
**Avoid:** the entire question/approval + chrome layer is a large pile of **regexes coupled to specific Claude/Codex TUI layouts** and agent-name sniffing (`/\bclaude\b/i`) — it will silently mis-strip when CLIs change; magic thresholds (`history_tail<=3`, `refusals>=2`) are tuned to current output; no cursor addressing means full-screen apps (vim/htop) won't render; reading up to **10,000 ANSI lines per Claude pane every ~4 s** (`:1299-1307`) is bandwidth-heavy over a tunnel. Isolate all TUI scraping behind one well-tested module and prefer any structured signal Herdr/agents expose.

---

## 5. Mobile UX (PWA)

- **Install.** `display:standalone`, relative `start_url`/`scope:"./"` (works behind any path), maskable icons, iOS `apple-mobile-web-app-capable` + `viewport-fit=cover` for the notch (`frontend/public/manifest.webmanifest`, `frontend/index.html:5-12`). Installed-mode detection gates `register_app_origin` (`store.ts:276-282,1180-1185`).
- **Service worker.** Deliberately **not a caching worker** — push + notification routing only, no `fetch` handler, so **no offline shell** (reasonable for a control app). `skipWaiting`+`clients.claim` for instant takeover; `_headers` forces `no-cache` on `sw.js`/`/`/`index.html`/`version.json` (`frontend/public/sw.js`, `frontend/public/_headers`).
- **Push.** VAPID web-push with **per-relay SW scopes** (`./push/<slug>/`) so each computer has an isolated subscription (`frontend/src/lib/push.ts:58-87,152`). Blocked-agent notifications carry an **"Approve once" action button** that acts via the SW without opening the app (`frontend/src/App.svelte:332-358`, `sw.js:42-72`). App badge + document-title count + haptic vibration on newly-blocked agents (`App.svelte:130-139`).
- **Structured questions.** Backend-driven single/multi-select forms with an "Other" free-text option, "Question N of M" progress, and Previous/Submit/"Chat about this" buttons **gated on capabilities** `can_go_back`/`can_chat`; drafts cached per `pane::interaction` and restored when a question re-arrives (`frontend/src/components/QuestionForm.svelte`, `frontend/src/lib/questions.ts`).
- **Slash commands.** A relay-provided catalog (fetched once per agent+cwd, validated, capped at 300) filtered client-side with full ARIA combobox keyboard nav (`store.ts:1051-1095`, `TerminalView.svelte:222-249`).
- **Uploads.** Photo/clipboard image over the WS as base64 with a 10 MB cap; server returns a path appended to the composer as `Image: <path>` (`store.ts:1097-1140`).
- **Reconnect matrix.** Exponential backoff with jitter (base 3 s, ×2 cap, 60 s max) plus aggressive `visibilitychange`/`pageshow`/`focus`/`online`/`freeze`/`resume` handling — short backgrounds do a lightweight health revalidation, long ones force a full reconnect (`frontend/src/lib/security.ts:23-118`, `store.ts:329-400`). Essential because mobile browsers suspend WebSockets aggressively.

**Adopt:** per-relay push scopes; actionable "Approve once" notifications; badge+title+haptics; capability-gated structured question forms with draft caching; relay-provided slash catalog; the visibility/freeze/resume reconnect matrix; backoff+jitter.
**Avoid:** base64-over-WS upload inflates ~33% and buffers whole file in memory — prefer a multipart HTTP endpoint; push subscription requires an active WS + VAPID key over the socket, so notifications can't arm until connected — serve the VAPID key over plain HTTP instead; no offline app-shell (add one if you want the UI to open while a tunnel is down).

---

## 6. Tunnel & Auth Model

**Two cloudflared modes.** (1) **Quick tunnel (TryCloudflare)**: `cloudflared tunnel --config /dev/null --url http://127.0.0.1:$PORT` (`relay/start.sh:71`); the public `*.trycloudflare.com` URL is **scraped from cloudflared's log with a regex** (`start.sh:81`). No account. Disposable — a new hostname each run. (2) **Named/stable tunnel**: `cloudflared tunnel --config <cfg> run` (`relay/herdr-mobile-relay-service.sh:71`) with a generated ingress YAML routing `relay-<host>.<domain>` → `http://127.0.0.1:$PORT` and a 404 catch-all (`stable-setup.sh:368-386`). cloudflared terminates public TLS and proxies to the loopback relay, which upgrades HTTP→WS in-process.

**Stable provisioning** (`relay/stable-setup.sh`, ~790 lines — the crown jewel) is a **resumable, ownership-tagged state machine** (`relay/stable_state.py`): every mutating step records an ownership boolean (`created_tunnel`, `created_dns`, `created_credentials`, `created_config`, `service_installed_by_wizard`, …, `stable_state.py:17-38`), writes are atomic (fsync + 0600), and any failure prints the exact rerun command. It **pre-checks DNS via DNS-over-HTTPS and never uses `--overwrite-dns`** (refuses to clobber existing records/tunnels; `stable-setup.sh:151-166,233-236`, asserted by test), cross-checks the created tunnel UUID against the credentials file and `tunnel list`, and confirms before any account mutation (TTY or `HERDR_STABLE_YES=1`).

**Auth.** Token via `Authorization: Bearer` or `?token=` query, compared with **constant-time `hmac.compare_digest`** (`relay/herdr_relay.py:4607-4629`). `main()` **refuses to bind a tokenless relay outside loopback** (`:6584-6585`). Tokens are generated with `openssl rand -hex 16` (fallback `uuidgen`) and stored single-quoted in a 0600 env file via atomic write (`relay/common.sh:103-114,145-168`). Setup secrets ride in the **URL fragment** (`#setup=…`), so the token never reaches a server in the HTTP request and is stripped from the address bar after import (`frontend/src/lib/config.ts:97-125`, `store.ts:156-175`); the relay-URL in that fragment is strictly validated (must be `wss:`, no creds/path/query/fragment).

**Services.** macOS launchd LaunchAgent (`KeepAlive{SuccessfulExit:false}`, `ThrottleInterval 10`) vs Linux systemd **user** service (`Restart=on-failure`, `RestartSec=10`), both dispatched through `service.sh` and both running an inner supervisor that starts relay+cloudflared as children and **restarts both if either dies** — avoiding the "tunnel up, relay down → silent 502" orphan state (`herdr-mobile-relay-service.sh:66-88`).

**Adopt:** ownership-tagged resumable state machine with atomic state + exact rerun message; never `--overwrite-dns`, DoH pre-check, refuse existing records; UUID cross-checking; constant-time token compare; loopback-bind refusal for tokenless configs; fragment-based token handoff; dual-process "restart both if either dies" supervision; legacy-label migration on install.
**Avoid:** log-scraping the quick-tunnel URL by regex (brittle); `sed`-based YAML "parsing" (`stable-setup.sh:102-114`); **`source`-ing the env file in bash** = arbitrary code execution if tampered (parse, don't eval); token passed in argv to helpers (briefly visible in `ps`); token in `?token=` query can land in proxy/access logs — prefer header-only.

---

## 7. Install Flow

`herdr plugin install 0cv/herdr-mobile-relay` clones the repo and runs a confirmed `[[build]]` step (`herdr-plugin.toml:30-31`). `plugin-build.sh` (POSIX `sh`, **soft-fails so it never blocks registration**) installs `uv` user-level if missing and **pre-warms** the PEP-723 env (`uv sync --script herdr_relay.py`) so the first Quick Start doesn't pause on downloads; cloudflared is intentionally *not* installed here because it opens a public tunnel and deserves an interactive yes (`plugin-build.sh:9-14,66-79`). A detached `nohup` waiter (`plugin-post-install.sh`) copies itself into `~/.cache` (herdr deletes the staging checkout right after build), polls the plugin registry until the new version + `setup` action appear, then opens the setup pane. Runtime is `uv run herdr_relay.py` — no venv, no requirements.txt (`start.sh:58`). QR is rendered via `uv run --with segno` and is **allowed to fail** (fallback: always print the link, `common.sh:486-500`). Teardown (`stable-teardown.sh`) removes **only wizard-owned resources** gated on the ownership booleans, requires typing `teardown`, and — since cloudflared can't reliably delete a DNS route — verifies via DoH and records `dns_cleanup_required` with manual instructions if the record remains (`:135-204`).

**Adopt:** "runnable at install, soft-fail, never block registration"; pre-warm the runtime env; ownership-boolean teardown (only remove what you created); verify a chosen app origin actually serves Herdr (fetch `/manifest.webmanifest`) before pointing the phone at it; QR "allowed to fail, always print the link".
**Avoid:** the detached-waiter + copy-to-cache + mkdir-lock + embedded-Python-registry-parse dance is intricate and race-prone — a Go binary can watch the registry directly; heavy `uv run python -c '...'` for trivial tasks (URL-encode, JSON) adds startup cost + injection surface; **cloudflared/uv auto-download with no checksum/signature verification** (`setup.sh:57-59`, `curl|sh`) is a supply-chain gap. A single static Go binary collapses most of this.

---

## 8. Tests, CI & Release Integrity

- **CI** (`.github/workflows/check.yml`): backend on Python 3.10 **and** 3.14, frontend on Node 24, Playwright chromium+webkit, and a conditional `web-release-check` when the committed bundle changes. `backend-check` also runs ruff, `compileall`, and **`bash -n`/`sh -n` on every shell script**.
- **`tests/test_relay.py`** — 7,650 lines, ~269 methods: auth/HTTP surface, `web_asset_path` symlink-escape rejection, protocol negotiation, **mutation-deadline machinery** (rechecks deadline before spawning, stops keys once expired, timeout→dispatched_unknown mapping), terminal stitching/chrome, structured-question identity across redraws + stale-answer rejection, uploads, approvals, push, updates, supervision restart, slow-client isolation, half-open socket replacement, SIGHUP profile reload, UDP event validation, activity tombstones.
- **`tests/test_stable_setup.sh`** — 12 end-to-end bash cases against **stubbed `cloudflared`/`curl`/`uv`/`systemctl` on a fake PATH+HOME**: success, existing-config reuse untouched (cksum), creation-confirmation gate, occupied-hostname rejection, interrupted-route resume, health-mismatch suppresses QR, teardown ownership + DNS retention, and asserts `--overwrite-dns` is never used.
- **`tests/test_update_support.py`** — dirty checkout blocks update, update job verifies restart/health before success, failed update rolls back + records outcome, lock prevents overlapping installers, app-deploy rejects wrong origin / uncommitted assets.
- **Frontend** — 86 vitest unit tests + 24 Playwright mobile journeys on **Pixel 7 (chromium) + iPhone 15 (webkit)** with SW blocked and a server mirroring prod brotli/no-cache/nosniff headers.
- **Release integrity** (`frontend/scripts/*.mjs`): `validate-build.mjs` asserts brotli assets byte-match their source (decompress + equals), exactly one `app.js`/`app.css` (no hash-splitting), manual `?v=` cache-busting, `version.json` matches `herdr-plugin.toml` + `build-versions.json`, `_headers` preserves `no-cache`, PWA manifest contract; `check-size.mjs` enforces an **80 KiB gzip ceiling** on initial payload.

**Adopt:** fake-binary-on-PATH stub harness for anything that shells out to cloudflared (ideal for Go integration tests too); update tests that assert health-verified restart + rollback; committed-bundle integrity gate (brotli round-trip, single-asset, size ceiling, version cross-consistency) — port to Go `embed.FS`; dual-engine mobile Playwright; `bash -n`/`sh -n` syntax gate.
**Avoid:** 7,650-line, 2-mega-class test file (Go's package tests split naturally); bash stubs must hand-track real cloudflared JSON shape → drift risk vs the real binary.

---

## 9. Security Assessment

**Strengths.** Constant-time token compare (`:4629`); loopback-bind refusal without a token (`:6584`); **allowlist static serving** with `\`/NUL/`..` rejection, explicit servable-file set, and post-resolve `is_relative_to(web_root)` re-check (`:4643-4692`); home-confined cwd resolution for all directory/launch ops (`:2811-2825`); **server-side launch profiles** — the client sends only a `profile_id` that must exist in the server-built map, the executable comes from `shutil.which`, argv is `shlex.join`'d and run through Herdr, never a shell (`:2696-2736,4960-4961,6014-6034`); agent name validated `[a-z][a-z0-9_-]{0,31}`; upload MIME allowlist + 10 MB cap + 0600 files (`:2001-2036`); VAPID/subscription files 0600 with atomic writes; and the **mutation-deadline + lifecycle-generation + state-revision guard trio** (see below).

**The most valuable security idea — mutation guards** (`:133-141,753-792,687-727`). The phone abandons an approval after 12 s / commands 15 s / questions 20 s. The relay sets its *own* shorter deadlines (9/12/16 s) as `contextvars`, re-reads the deadline **just before spawning `herdr`**, and aborts if expired — so **it never delivers keys after the UI reported a timeout** (which would execute a command the user believes was cancelled). A per-pane `lifecycle_generations` counter aborts input aimed at a pane that was cleared/replaced (input can't land in a *different* agent that reused the pane), and a state-revision guard cancels superseded publications. This is the single most valuable pattern to port: a `context.WithDeadline` shorter than the client timeout threaded to the exec call + a per-pane generation checked immediately before dispatch.

**Concrete weaknesses to avoid.**
1. **Origin check collapses to token-only.** `origin_allowed` returns True for *any* origin whenever an `AUTH_TOKEN` exists (`:4616-4623`), and setup scripts never wire `HERDR_ALLOWED_ORIGINS`. Combined with the token accepted in `?token=` query, CSRF/DNS-rebind protection leans entirely on token secrecy. A Go plugin that controls both app and relay should default to **strict same-origin**, header-only tokens.
2. **No CSP or security headers.** `frontend/public/_headers` sets only `Cache-Control` — no Content-Security-Policy, HSTS, X-Frame-Options, or frame-ancestors. The *entire* XSS defense rests on one function's escape-then-style discipline in `terminal.ts` (the only `{@html}` sink); one future unescaped sink breaks it with no backstop. Add a strict CSP/HSTS at the cloudflared/Go response layer.
3. **WebAuthn is theater.** The "Require Device Unlock" feature (`frontend/src/lib/security.ts:163-247`) is unverified **local presence only** — client-generated challenge, assertion never checked server-side — and does nothing to protect the tokens.
4. **Plaintext tokens in `localStorage`** (`frontend/src/lib/config.ts:93-95`), readable by any XSS and persistent. Prefer cloudflared Access / short-lived cookies so bearer tokens don't live in the browser at all.
5. **Unauthenticated `/healthz`** leaks instance id, versions, revision, protocol, and topology to anyone who can reach the tunnel unless Cloudflare Access is layered on (`:4716-4736`).
6. **Local UDP events are spoofable** — only the `socket_path` field is checked (`:6535-6541`); fine for single-user hosts, risky on shared machines.
7. **Ops shell risks** (§6/§7): env-file `source`, `sed`-based YAML, unverified `curl|sh` installers, token in argv/query.

---

## 10. Update & Deploy Model

Self-update (`relay/update_support.py`) is **transactional and revision-pinned**: it reads `git ls-remote … refs/heads/main`, validates a 40-hex SHA, parses a strict `MAJOR.MINOR.PATCH` from that revision's `herdr-plugin.toml`, and only offers an update when the remote semver is strictly greater **and** the install is eligible (managed marketplace plugin, or a clean checkout on canonical `main`; a dirty/forked/branched checkout is reported **blocked**, never modified) (`update_support.py:343-444`). The install runs **out of process** via `systemd-run --user` / `launchctl submit` so the updater survives restarting the thing it updates; it uses `merge --ff-only` (never a blind pull), verifies the installed manifest+HEAD match the advertised revision, reinstalls the service, then **verifies the running `/healthz`** reports the target version/revision and isn't `-dirty` — and **auto-rolls-back** (git reset / reinstall previous ref) on any failure, persisting a state machine surfaced live to clients (`:571-716`). The client adds TOCTOU protection by re-confirming `expected_version`/`expected_revision` before install (`relay/herdr_relay.py:4123-4136`).

App deploy targets a separate Cloudflare Pages app via **pinned `wrangler@4.112.0`**, verifies the committed `web/` bundle matches the release and isn't dirty, deploys only the configured project/branch, **polls the public origin until it reports the new version**, and keeps Cloudflare credentials on the owner machine — never on the phone (`update_support.py:860-960`, `configure-app-deploy.sh:42-43`). The client's About card compares three versions (running bundle vs origin `version.json` vs upstream GitHub raw) to distinguish "just reload" from "needs deploy" (`frontend/src/lib/updates.ts:124-189`).

**Adopt:** revision-pinned semver-gated updates from a canonical remote; ff-only / clean-checkout requirement; out-of-process runner; health-verified restart + auto-rollback + persisted state; TOCTOU expected-version confirmation; pinned deploy tool + committed-bundle verification + public-version poll before reload; three-way version model.
**Avoid:** `launchctl submit` is deprecated (use a supported launchd mechanism); hand-rolled `.env` regex parsing; hard-coded upstream GitHub raw URL (make configurable, resilient to rate-limits); for a Go plugin that serves its own embedded bundle, drop the whole Cloudflare Pages "deployment owner" dance in favor of "relay writes new bundle to its embed dir + reload".

---

## 11. Operational Reliability

The strongest operational pattern is the **four-stage gate before advertising the endpoint** (`stable-setup.sh:750-786`, `common.sh:243-277`): (1) local `/healthz` reachable + complete, (2) `/readyz` ready (with `live-handoff` guidance on protocol mismatch), (3) public DNS resolves via DoH, (4) public `https://<host>/healthz` **instance/version/protocol equal the local relay's** (`stable_state.py:167-175`) — proving the tunnel points at *this* relay, not a stale/foreign one, before the QR is ever printed. Every failure path suppresses the QR. Dual-process supervision (restart both if either dies) prevents tunnel-orphan 502s. Independent, separately-timed DNS vs HTTPS waits with distinct diagnostics and env-tunable timeouts. Day-to-day: `service-status/logs`, `rotate-token`, `setup-link`, `status`, `stable-teardown` (plus plugin-action equivalents).

**Adopt:** the four-stage local→ready→public-DNS→public-identity gate (replicate exactly — compare public `/healthz` instance id to local before showing the QR); dual-process restart-both supervision; transactional self-update; separately-timed DNS/HTTPS waits.
**Avoid:** fixed 10 s restart backoff with no crash-loop cap (add capped exponential backoff); `curl` dependency for health polling (Go has native HTTP); macOS LaunchAgent + Files-and-Folders TCC can silently block reading project dirs (design around it); the reliability layer spread across ~15 interdependent bash scripts (`set -euo pipefail`, `set +e` toggling, hardcoded PATH per script) is a large subtle surface — **collapsing it into one typed, testable Go binary is the primary architectural win of porting.**

---

## 12. Top Recommendations for a Go + cloudflared Herdr Plugin

**Port these (highest value first):**
1. **Mutation-deadline + lifecycle-generation + state-revision guards** — never deliver input after the client timed out or into a replaced pane (§9).
2. **Four-stage public-identity gate** before showing the QR — verify the tunnel resolves to *this* relay (§11).
3. **Revision-pinned, out-of-process, health-verified auto-rollback updates** (§10).
4. **Per-client bounded outbound queues with latest-snapshot coalescing** + adaptive poll/wake (§2).
5. **Ownership-tagged resumable tunnel state machine**, never `--overwrite-dns`, DoH pre-check (§6).
6. **Allowlist static serving** with post-resolve root re-check; **server-side launch profiles** (no client-supplied argv) (§9).
7. **Mobile terminal reflow** (backend chrome line-counts + client paragraph reflow) and **verbatim-during-question** serving (§4).
8. **Capability array + versioned protocol**, gate mutations only, exempt self-update, content-hashed question IDs, idempotent approvals (§3).
9. **Fragment-based token handoff**, per-relay push scopes, actionable "Approve once" notifications, visibility/resume reconnect matrix (§5).
10. **Release-integrity gate** on the embedded bundle (byte-equal compression, single asset, size ceiling, version cross-consistency) (§8).

**Fix on the way in (don't inherit):**
- Default to **strict same-origin + header-only tokens**; add **CSP/HSTS**; drop `?token=` query and `localStorage` tokens (§9).
- Talk to Herdr over its **socket/RPC**, not a subprocess-per-keystroke (§2).
- Replace **regex TUI scraping** with any structured agent/Herdr signal, and isolate whatever scraping remains behind one tested module (§4).
- Don't treat WebAuthn as authentication; don't expose topology on unauthenticated `/healthz` behind a public tunnel (§9).
- **Verify checksums/signatures** on any downloaded binary; **parse** config files, never `source` them (§6/§7).
- A **Go single static binary** eliminates the uv/PEP-723 bootstrap, the detached-waiter dance, and most of the ~15-script bash surface — treat that consolidation as the core win.

**License note:** the upstream is AGPL-3.0-or-later and a fork of `herdr-remote`. Reimplement the *patterns* above clean-room in Go to stay clear of the copyleft/network-source obligations; do not copy code.

---

*Audited from a shallow clone at HEAD `4ad54ae` (release 0.8.6). No git tags exist; versions are tracked in `herdr-plugin.toml` and commit messages. Backend security-critical paths were read first-hand; backend internals, frontend, and ops surfaces were cross-audited by three parallel readers over the full tree.*
