# Herdr Contract — Research for a Browser-Based Mobile Remote UI

**Status:** Research only. No implementation.
**Date:** 2026-07-23
**Authoritative pin:** Herdr **v0.7.5**, wire **protocol 17**, **schema_version 1** (stable channel, macOS aarch64).

## Sources

Every claim below is grounded in one of these. Prefer the machine-readable schema over prose docs where they disagree.

| Ref | Source | Notes |
|-----|--------|-------|
| `[bin]` | `/opt/homebrew/bin/herdr` (`herdr 0.7.5`) | installed CLI, the authority for syntax |
| `[schema]` | `herdr api schema --json` → saved `/tmp/herdr-schema.json` (248 KB) | embedded JSON Schema; `protocol:17`, `schema_version:1`; top keys `error_response`, `event`, `request`, `subscription_event`, `success_response` |
| `[status]` | `herdr status` | client/server v0.7.5, protocol 17, `compatible: yes` |
| `[skill]` | `/Users/matheus/.claude/skills/herdr/SKILL.md` | agent-automation guidance shipped with the binary |
| `[live]` | live socket probes against `/Users/matheus/.config/herdr/herdr.sock` | verified envelopes below |
| `[d-socket]` | https://herdr.dev/docs/socket-api/ | socket API prose |
| `[d-plugins]` | https://herdr.dev/docs/plugins/ | plugin contract |
| `[d-cli]` | https://herdr.dev/docs/cli-reference/ | CLI reference |
| `[d-agent]` | https://herdr.dev/docs/agent-skill/ | agent skill |
| `[d-install]` | https://herdr.dev/docs/install/ | platforms / versions |
| `[d-index]` | https://herdr.dev/docs/ , https://herdr.dev/docs/quick-start/ , https://herdr.dev/docs/configuration/ , https://herdr.dev/docs/integrations/ , https://herdr.dev/docs/preview/windows-beta/ | index of published docs |

> Upstream Git source is **not** checked out locally (installed via Homebrew). The binary's embedded `api schema` is the definitive, versioned contract and is used in its place. Re-run `herdr api schema --json` to regenerate against any future binary.

---

## 1. Version & compatibility constraints

- Current stable: **0.7.5** for Linux (x86_64/aarch64) and macOS (Intel/Apple silicon). Windows is **preview/beta only** with a protocol "subject to change." `[d-install]`
- Wire protocol is **17**; schema_version **1**. `[schema][status]`
- `ping` returns the live handshake — build a remote UI around this rather than assuming: `[live]`
  ```json
  {"id":"r1","result":{"type":"pong","version":"0.7.5","protocol":17,
    "capabilities":{"live_handoff":true,"detached_server_daemon":true}}}
  ```
- Client/server must match protocol; `herdr status` reports `compatible: yes|no`. If an update changes the protocol, Herdr prompts to stop the old server. `[status][d-install]`
- **UI guidance:** on connect, call `ping`, assert `protocol == 17` (or a known-good set), and tolerate unknown JSON fields — the socket-API doc explicitly says "handle unknown fields gracefully." `[d-socket]` Gate features on the `capabilities` map, not the version string.

---

## 2. Transport & socket

- **Transport:** newline-delimited JSON (one request per line, one response/event per line) over a **Unix domain socket** (named pipe on Windows). `[d-socket][live]`
- **Default path:** `~/.config/herdr/herdr.sock`; per-session: `~/.config/herdr/sessions/<name>/herdr.sock`. Verified live: `/Users/matheus/.config/herdr/herdr.sock` (srw-, 0 bytes). `[d-socket][live]`
- **Resolution order** (client): `--session <name>` → `HERDR_SOCKET_PATH` → `HERDR_SESSION=<name>` → default session socket. `[d-socket]`
- Plugins receive the path via `HERDR_SOCKET_PATH`. `[d-plugins]`

### Envelopes (verified live)

Request:
```json
{"id":"req_1","method":"ping","params":{}}
```
Success:
```json
{"id":"req_1","result":{"type":"pong", ...}}
```
Error (`error_response` schema: `{id, error:{code,message}}`, both required):
```json
{"id":"","error":{"code":"invalid_request","message":"invalid request: missing field `pane_id` ..."}}
```
Notes: every `result` is a tagged object with a `type` discriminator. Malformed requests come back with `id:""`. Server errors are also emitted by the CLI as JSON on stderr with exit 1; CLI usage/syntax errors exit 2. `[skill][live]`

### Error codes
`not_found`, `invalid_params`, `invalid_request`, `feature_disabled`, `platform_unsupported`, `plugin_disabled`, `stream_conflict`. `[d-socket][live]`

---

## 3. Is direct socket access safe?

**Reading:** safe and cheap. `ping`, `*.list`, `*.get`, `pane.read`, `session.snapshot`, `events.subscribe` are non-mutating.

**Mutating / dangerous:** the socket exposes the *entire* control surface with **no auth, no sandbox**. The plugin doc is explicit: plugins (and by extension anything holding the socket) run "with your environment, and can call the full Herdr CLI"; Herdr "does not review or sandbox what a plugin does." `[d-plugins]` The socket is a local, unauthenticated trust boundary equal to the logged-in user.

Concrete hazards a remote UI must fence off (per `[skill]` safety rules):
- `server.stop` / `server.stop` method — kills the server and every pane process. Never expose casually.
- `server.live_handoff` — process/session handoff; disruptive.
- Closing workspaces/tabs/panes you did not create.
- Targeting the **UI-focused** pane by omission can hit a pane owned by the user or another client. Always send an explicit `pane_id` or the caller's `--current`/`$HERDR_PANE_ID`.

**Recommendation for the mobile remote:** do **not** hand a browser a raw Unix socket. Front it with a thin local broker/plugin process that (a) holds the socket, (b) exposes an authenticated transport (loopback HTTP+WebSocket or a tunnel), (c) allowlists methods and pane/workspace scopes, and (d) blocks/guards `server.stop`, `server.live_handoff`, and cross-client `close`. Browsers cannot open AF_UNIX sockets anyway, so a bridge is mandatory regardless.

---

## 4. Method inventory (89 methods, protocol 17) `[schema]`

Grouped by domain. `params` shapes are in the schema `request.$defs`; result shapes in `success_response`.

- **Server:** `ping`, `server.stop`, `server.live_handoff`, `server.reload_config`, `server.agent_manifests`, `server.reload_agent_manifests`
- **Client/Notify:** `notification.show`, `client.window_title.set`, `client.window_title.clear`
- **Session:** `session.snapshot` (empty params — full topology snapshot; ideal for initial UI hydration)
- **Workspace:** `workspace.create`, `.list`, `.get`, `.focus`, `.rename`, `.move`, `.report_metadata`, `.close`
- **Worktree:** `worktree.list`, `.create`, `.open`, `.remove`
- **Tab:** `tab.create`, `.list`, `.get`, `.focus`, `.rename`, `.move`, `.close`
- **Pane (topology):** `pane.split`, `.swap`, `.move`, `.zoom`, `.layout`, `.process_info`, `.neighbor`, `.edges`, `.focus_direction`, `.resize`, `.list`, `.current`, `.get`, `.focus`, `.rename`, `.close`
- **Pane (I/O):** `pane.send_text`, `.send_keys`, `.send_input`, `.read`, `.wait_for_output`, `.graphics.set/.clear/.info`
- **Pane (agent authority):** `pane.report_agent`, `.report_agent_session`, `.report_metadata`, `.clear_agent_authority`, `.release_agent`
- **Layout:** `layout.export`, `.apply`, `.set_split_ratio`
- **Agent:** `agent.list`, `.get`, `.read`, `.explain`, `.send_keys`, `.rename`, `.view.set/.clear`, `.focus`, `.start`, `.prompt`, `.wait`
- **Popup:** `popup.close`
- **Events:** `events.subscribe`, `events.wait`
- **Integration:** `integration.install`, `integration.uninstall`
- **Plugin:** `plugin.link`, `.list`, `.unlink`, `.enable`, `.disable`, `.action.list`, `.action.invoke`, `.log.list`, `.pane.open`, `.pane.focus`, `.pane.close`

CLI ↔ method mapping is 1:1 in spirit (`herdr pane read` → `pane.read`). Full CLI flag surface is in `[d-cli]` / `herdr <group>` help. `[skill]`

---

## 5. Topology & ID model (for the UI tree) `[skill][live]`

Opaque, stable, hierarchical string handles:
- workspace `w1`; tab `w1:t1`; pane `w1:p1`.

Rules the UI must respect:
- Closed tab/pane IDs are **not reused**.
- A pane moved to another workspace gets a **new** workspace-qualified pane ID; old ID resolves only for the moved process's inherited context. After `pane.move`, follow `result.move_result.pane.pane_id`; old value is `result.move_result.previous_pane_id`.
- Creation results expose next-step IDs: `workspace.create` → `result.workspace`, `result.tab`, `result.root_pane`; `tab.create` → `result.tab`, `result.root_pane`; `pane.split` → `result.pane`.
- `workspace.list` returns per-workspace `agent_status`, `focused`, `active_tab_id`, `pane_count`, `tab_count`, `label`, `number` (verified live).
- **Hydrate the whole tree with `session.snapshot`**, then keep it live via events (§7).

Agent identity is a naming layer over panes: names match `[a-z][a-z0-9_-]{0,31}`, unique among live agents, and follow the pane's current occupant (cleared on exit/release/replace). Agent methods accept a live agent name **or** the hosting pane ID — never terminal IDs or bare kind labels. `[skill]`

---

## 6. Pane terminal I/O contract

This is the crux for a terminal view. **There is no raw PTY byte-stream over the JSON socket.** The socket exposes *rendered snapshots + change signals*; raw attach is a separate CLI-only channel.

### 6a. Reading output — `pane.read` (snapshot model) `[schema][live]`
Params (`PaneReadParams`): `pane_id`\* (string), `source`\* (`ReadSource`), `format` (`ReadFormat`), `lines` (int|null), `strip_ansi` (bool).
- `ReadSource` enum: `visible`, `recent`, `recent_unwrapped`, `detection`.
- `ReadFormat` enum: `text`, `ansi`.
Result (`type:"pane_read"`, `read` = `PaneReadResult`): `pane_id`, `workspace_id`, `tab_id`, `source`, `format`, `text`, `revision`, `truncated`. Verified live:
```json
{"id":"r2","result":{"type":"pane_read","read":{"pane_id":"w6:p3","workspace_id":"w6",
  "tab_id":"w6:t2","source":"visible","format":"text","text":"...", "revision":..., "truncated":...}}}
```
Source semantics `[skill]`: `visible` = rendered viewport; `recent` = scrollback with soft wraps; `recent_unwrapped` = wraps joined (best for logs/transcripts); `detection` = plain bottom-buffer snapshot used for agent classification. Use `format:"ansi"` when color/styling is meaningful (feed to xterm.js). **Alt-screen caveat:** rows that leave the alternate screen never enter host scrollback; larger `lines` cannot recover them.

### 6b. Change signal — `revision` + `pane_output_changed`
Each pane carries a monotonic `revision`. The `pane_output_changed` event payload is `{type, pane_id, workspace_id, revision}` — **no text**. `[schema]` The `pane.output_changed` subscription supports `min_revision` to resume without missing frames. `[schema]`

**Terminal-view loop:** subscribe to `pane.output_changed` for the visible pane → on each event, `pane.read` the delta (`source:"recent_unwrapped"` or `visible`, `format:"ansi"`), diffing on `revision`. This is a signal-then-fetch model, not a push stream of bytes. `pane_output_changed` deliberately omits text to keep the event bus cheap.

### 6c. Sending input `[schema]`
- `pane.send_text` — `PaneSendTextParams{pane_id*, text*}`: literal text, no Enter.
- `pane.send_keys` — logical keys (`enter`, `tab`, `esc`, `backspace`, `ctrl+h`, `alt+x`, `shift+tab`, `f1`, …). Validated before any byte is written. `[d-cli]`
- `pane.send_input` — `PaneSendInputParams{pane_id*, text?, keys?[]}`: combined text+keys in one atomic call. **Best fit for a soft-keyboard mobile UI** (send typed text and control keys together, honoring bracketed-paste).
- `pane.run` (CLI) atomically sends command + Enter.

### 6d. Wait / match
- `pane.wait_for_output` — `PaneWaitForOutputParams`: substring or Rust-regex match over a chosen source with `timeout_ms`; matches already-present output immediately. `[skill][schema]`

### 6e. Raw terminal attach (out of band)
`herdr terminal attach|session observe|session control` and `agent attach` give raw interactive PTY control (detach `ctrl+b q`). These are **CLI-only** — not represented as JSON socket methods — so a browser terminal cannot use them directly; it must go through the snapshot+events model above (or the bridge must proxy a PTY itself). `[d-cli][schema]`

### 6f. Graphics
`pane.graphics.set/clear/info` exist (image/graphics protocol surface) — relevant only if the mobile UI renders inline images; otherwise ignore. `[schema]`

---

## 7. Event & subscription model `[schema][live]`

### Subscribe
`events.subscribe` — `EventsSubscribeParams{subscriptions*: Subscription[]}`. Streams over the same connection: first reply is `{"id":"<id>","result":{"type":"subscription_started"}}` (verified live), then `subscription_event` frames arrive as events fire. The connection stays open for the life of the subscription.

**Each pane-scoped subscription requires its target key** (verified: omitting `pane_id` → `invalid_request: missing field 'pane_id'`).

`Subscription` supports **26 types** (superset of the 3 "derived" kinds), each with scope fields:
```
workspace.created|updated|metadata_updated|renamed|moved|closed|focused
worktree.created|opened|removed
tab.created|closed|focused|renamed|moved
pane.created|closed|updated|focused|moved|exited|agent_detected
pane.output_matched   (needs pane_id + match: OutputMatch)
pane.agent_status_changed (needs pane_id; optional agent / agent_status filter)
pane.scroll_changed   (needs pane_id)
layout.updated
pane.output_changed   (needs pane_id; optional min_revision)
```
`OutputMatch` is an internally-tagged enum: `{type:"substring",value}` or `{type:"regex",value}` (verified: passing a bare string errors).

The `subscription_event` schema also defines rich derived payloads: `PaneAgentStatusChangedEvent`, `PaneOutputMatchedEvent`, `PaneScrollChangedEvent` (+ `PaneScrollInfo{viewport_rows, offset_from_bottom, max_offset_from_bottom}` — feeds a mobile scrollback indicator), and `PaneReadResult`.

### Full event catalog (top-level `EventData`, 25 types) `[schema]`
```
workspace_created/updated/metadata_updated/closed/renamed/moved/focused
worktree_created/opened/removed
tab_created/closed/renamed/moved/focused
pane_created/closed/updated/focused/moved/output_changed/exited/agent_detected/agent_status_changed
layout_updated
```
`AgentStatus` enum: `idle`, `working`, `blocked`, `done`, `unknown`.

### One-shot wait
`events.wait` — `EventsWaitParams{match_event*: EventMatch, timeout_ms?}`. `EventMatch` variants are scoped by `workspace_id` / `tab_id` / `pane_id` (+ `label`, `min_revision`, `agent`, `agent_status` where applicable). Use for imperative "wait until X" flows; use `events.subscribe` for a live UI feed.

**UI guidance:** one persistent `events.subscribe` connection carrying workspace/tab/pane lifecycle + `pane.agent_status_changed` for the whole session drives the sidebar/tree; a second per-focused-pane `pane.output_changed` subscription drives the active terminal view. Persist `revision` to resume with `min_revision` after reconnect.

---

## 8. Agent automation contract `[skill][schema][d-cli]`

Lifecycle states (`AgentStatus`): `idle`, `working`, `blocked`, `done`, `unknown`.
- `idle` = ready for input **and** its tab was seen in a focused Herdr UI. `done` = same idle state after *unseen* background work finished. Focusing a tab/pane/agent marks it seen; **CLI/socket reads do not mark seen** — important for a remote UI: reading a pane won't clear a `done` badge.
- `blocked` = Herdr recognized an approval/question UI. `unknown` = agent present but unclassifiable (not proof of completion).

Key methods:
- `agent.list` / `agent.get <target>` / `agent.read` (same source/format options as `pane.read`).
- `agent.start` — `AgentStartParams`: requires an existing **available shell** pane (`--kind`, `--pane`); never creates/splits layout; ~30 s startup timeout; returns only once the expected agent is detected and ready.
- `agent.prompt` — `AgentPromptParams{target*, text*, wait?}`: atomically submits text + encoded Enter honoring live bracketed-paste. `--wait` settles on first `idle`/`done`/`blocked`; `--until STATUS` for state-specific waits; a prompt from a non-working state must show a lifecycle change within 5 s or returns `agent_prompt_stalled`.
- `agent.wait`, `agent.send_keys`, `agent.focus`, `agent.rename`, `agent.view.set/clear`, `agent.explain`.

Supported agent kinds (from `[d-cli]`, live via `server.agent_manifests`): `pi, claude, codex, gemini, cursor, devin, agy, cline, omp, mastracode, opencode, copilot, kimi, kiro, droid, amp, grok, hermes, kilo, qodercli, maki`.

**Agent-authority (reporting) methods** — `pane.report_agent`, `pane.report_agent_session`, `pane.report_metadata`, `pane.release_agent`, `pane.clear_agent_authority` — let an external supervisor *assert* agent state/metadata onto a pane (`--source`, `--seq`, `--ttl-ms`). A mobile UI generally *consumes* status; it needs these only if it hosts/relabels agents itself.

---

## 9. Workspace / tab / pane / worktree / session lifecycle (mutating) `[d-cli][schema]`

- **Workspace:** `create [--cwd --label --env --focus]`, `list/get/focus/rename/move/close`, `report_metadata`. Create also creates first tab + root pane.
- **Tab:** `create/list/get/focus/rename/move/close`.
- **Pane:** `split --direction right|down [--ratio --cwd --env --focus]`, `resize`, `zoom`, `move` (to tab / new-tab / new-workspace), `swap`, `focus`, `rename`, `close`, plus `layout`/`neighbor`/`edges`/`process_info` for geometry.
- **Worktree** (git-backed, checkout provenance): `list`, `create [--branch --base --path --label]`, `open (--path|--branch)`, `remove [--force]`; scoped by `--workspace` or `--cwd`. Emits `worktree_created/opened/removed` events.
- **Session** (named server instances): CLI `session list/attach/stop/delete`; socket `session.snapshot` for full-topology read. Named sessions each have their own socket (§2). Use a named test session for experiments; never `server.stop` a live session from within it.

Mutation-safety rules the UI must encode `[skill]`: prefer explicit `pane_id`/`--current`; use `--no-focus` for background ops; never close what the user didn't create; never `server.stop` unless explicitly intended.

---

## 10. Plugin contract (how a plugin process invokes Herdr) `[d-plugins][schema]`

A **plugin = a directory with `herdr-plugin.toml`** + launchable argv commands (bash / JS / Lua / Rust binary / anything). Required manifest fields: `id`, `name`, `version`, `min_herdr_version`; optional `description`, `platforms`, build commands, entrypoints.

**What a plugin can invoke — everything.** No SDK, no restricted set, no sandbox. Two equivalent paths:
- **CLI** via `HERDR_BIN_PATH` (recommended, portable) — "every command in the CLI reference is available."
- **Raw socket** via `HERDR_SOCKET_PATH` (the 89 methods above).

Injected env (all plugin processes): `HERDR_SOCKET_PATH`, `HERDR_BIN_PATH`, `HERDR_ENV=1`, `HERDR_PLUGIN_ID`, `HERDR_PLUGIN_ROOT` (read-only source), `HERDR_PLUGIN_CONFIG_DIR` (user-editable), `HERDR_PLUGIN_STATE_DIR` (runtime state), `HERDR_PLUGIN_CONTEXT_JSON`. Optional when available: `HERDR_WORKSPACE_ID`, `HERDR_TAB_ID`, `HERDR_PANE_ID`, `HERDR_PLUGIN_ACTION_ID`, `HERDR_PLUGIN_EVENT`(+`_JSON`), `HERDR_PLUGIN_ENTRYPOINT_ID`.

Entry-point types in the manifest:
- `[[actions]]` — `{id, title, contexts:["workspace"|"tab"|"pane"|...], command}`; invoked via `plugin.action.invoke`.
- `[[events]]` — `{on:"<event>", command}` runs on lifecycle events.
- `[[panes]]` — `{id, title, placement, command}`; `placement ∈ overlay(default)|popup|split|tab|zoomed`; opened via `plugin.pane.open`.
- `[[link_handlers]]` — `{id, title, pattern(regex), action}`; modified-click is Control on all platforms; handler receives `clicked_url` + `link_handler_id`.
- `[[keys.command]]` — `{key:"prefix+l", type:"plugin_action", command:"<plugin>.<action>"}`.

Lifecycle: **build** (during GitHub `plugin install`, no socket/context; `plugin link` skips it) → **startup** (`[[startup]]` runs once per enabled plugin after session restore + socket ready, `HERDR_PLUGIN_EVENT=startup`) → **runtime** (actions/events/panes/links).

Version gate: Herdr refuses to link/install when `min_herdr_version` > current binary. Set it to the oldest version whose APIs/events/manifest fields you use — for this project's contract, **0.7.x / protocol 17**.

Plugin management methods: `plugin.link/unlink/enable/disable/list`, `plugin.action.list/invoke`, `plugin.pane.open/focus/close` (`PluginPaneOpenParams{plugin_id*, entrypoint*, placement, direction, cwd?, env, focus, target_pane_id?, width?, height?, workspace_id?}`), `plugin.log.list`. Integrations: `integration.install/uninstall`.

---

## 11. Architecture implications for the mobile remote UI

1. **Bridge is mandatory.** Browsers can't open AF_UNIX sockets. Ship a Herdr **plugin** (or standalone local process) that holds `HERDR_SOCKET_PATH` and exposes loopback HTTP + WebSocket (or a tunnel) to the phone. Register it as a plugin so it inherits socket/env, gets a stable lifecycle (`[[startup]]`), and appears in `plugin.list`.
2. **Translate transports faithfully.** Phone WS frame → bridge → newline-JSON on the socket; socket responses/`subscription_event` frames → WS push. Preserve `id` correlation and the `{type}` discriminator.
3. **Authenticate + allowlist at the bridge.** The socket is unauthenticated and unsandboxed (§3, §10). Add a token/PIN, and allowlist methods; hard-block `server.stop`, `server.live_handoff`, and non-owned `*.close`.
4. **Hydrate then subscribe.** Initial load: `ping` (assert protocol 17 + capabilities) → `session.snapshot`. Then one persistent `events.subscribe` for lifecycle + agent-status drives the tree; a per-focused-pane `pane.output_changed` subscription drives the terminal.
5. **Terminal view = signal-then-fetch.** No PTY byte stream on the socket. On `pane.output_changed`, `pane.read` (`format:"ansi"`, `source:"recent_unwrapped"`/`visible`) and render (e.g. xterm.js), diffing on `revision`; resume with `min_revision` after reconnect.
6. **Input:** prefer `pane.send_input` (text + control keys atomically) for a soft keyboard; `agent.prompt` for agent panes.
7. **Reconnect resilience:** IDs are stable and non-reused; persist `pane_id`+`revision`. Handle `pane_exited`/`pane_closed`/`workspace_closed` to prune the tree.
8. **Don't rely on reads to clear `done`.** Only focus marks a pane "seen"; a remote "mark read" must call a focus method (`pane.focus`/`tab.focus`/`agent.focus`) if it wants the `done` badge cleared.

---

## 12. Open items / to verify before building

- Exact `success_response` result shapes per method (`success_response.$defs` in `[schema]`) — enumerate the ones the UI consumes (`session.snapshot`, `pane.read`, `workspace.list`, `agent.get`) field-by-field.
- Whether `pane.output_changed` coalesces rapid updates (rate/debounce) — affects read cadence.
- Backpressure / max concurrent subscriptions per connection, and whether one connection may hold many `events.subscribe` calls (`stream_conflict` error code hints at limits).
- Behavior of `server.live_handoff` and `detached_server_daemon` capability for surviving server restarts under a long-lived remote session.
- Confirm no auth exists on the socket in 0.7.5 (assumed from docs; treat as unauthenticated).
