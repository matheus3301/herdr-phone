# Collie — deep audit (reference for a mobile Herdr remote plugin)

> Research only. No code adopted yet. This is a source-level audit of `AltanS/collie`,
> read as prior art for building our own mobile Herdr remote in `herdr-phone`.
> Collie is *the same product we intend to build*: a phone web UI for a Herdr agent
> herd. Treat it as the reference implementation and the competitor to beat.

## Repository state audited

- **Repo:** https://github.com/AltanS/collie
- **HEAD:** `afa53feede953b56ef69eac34755aa1fe523c682` — `release: 0.14.2` (2026-07-23 14:21 +0200)
- **License:** MIT, © 2026 Altan Sarisin (`LICENSE:1-3`). Permissive — safe to learn from and to
  reuse patterns/snippets with attribution.
- **Age & velocity:** first commit 2026-07-17 (recorded history in the shallow clone), 147 commits,
  13 tags (`v0.10.0`…`v0.14.2`). CHANGELOG traces releases back to `0.1.0` (2026-06-30). ~1 month of
  extremely intense, near-daily releasing.
- **Authorship:** effectively single-author — Altan Sarisin (127 + 10 commits); minor outside
  contributions (Vinicius Carvalho ×6, iFwu ×2, plus @jz-wilson, @diogenesc, @bnivanov, Johnzell via
  PRs #17–#24). Bus-factor 1.
- **Size:** 147 `.ts` + 94 `.tsx` files. Bridge ≈6,900 LOC (16 src + 15 test). Web: 79 test files /
  ~785 test cases / 29 pane fixtures. Test LOC ≈ source LOC on both sides.

## What Collie is (one paragraph)

A **Herdr plugin that is only a thin launcher** (`herdr-plugin.toml`) plus a long-lived
**Bun/TypeScript bridge** running as a `systemd --user` service, serving a **Vite + React 19 +
Tailwind 4 + shadcn PWA**. The bridge talks to Herdr's Unix-domain-socket JSON-RPC API, polls a
snapshot of the herd, exposes a small same-origin HTTP/JSON API + the static PWA, and is published
**tailnet-only via `tailscale serve`** (or a reverse proxy). The phone opens a MagicDNS URL, sees
which agent needs input, and replies with the native keyboard (voice dictation is free via the
OS keyboard — Collie ships no voice code). It is deliberately **single-user, remote-shell-grade
access**, not a multi-tenant product.

---

## 1. Architecture

Reference: `ARCHITECTURE.md`, `README.md#architecture`, `CLAUDE.md`.

```
phone (PWA)
   │  HTTPS over tailnet (MagicDNS)
   ▼
tailscale serve         terminates TLS, injects Tailscale-User-Login   (Variant C: reverse proxy instead)
   │  127.0.0.1:PORT     (bridge binds loopback ONLY)
   ▼
Collie bridge (Bun)     static PWA + small JSON API; polls Herdr; web-push
   │  one-shot newline-delimited JSON-RPC over Unix socket
   ▼
Herdr server            owns panes, agents, terminal state
```

Load-bearing design decisions:

- **`systemd --user` service, NOT a plugin pane** (`ARCHITECTURE.md` §3). A plugin pane dies when
  the pane closes / user detaches / Herdr restarts — exactly when you're on mobile. A network daemon
  must be supervised independently. The plugin manifest exists only to register `[[actions]]` (which
  shell out to `scripts/collie-ctl.sh`) and a `[[build]]` step. No `[[panes]]`, no `[[events]]`.
- **Poll is the source of truth; events are only pokes** (`ARCHITECTURE.md` §5, `bridge/state-engine.ts`,
  `bridge/event-poker.ts`). The bridge polls `session.snapshot` on an interval; a long-lived
  `events.subscribe` stream never mutates state, it only triggers an immediate debounced re-poll and
  lets the interval relax. "A missed event costs one interval, never correctness." No resync logic,
  reconnection is trivial.
- **Two independent recovery loops** designed in from the start: bridge↔Herdr (poll doubles as
  resync) and browser↔bridge (browser polls `/api/snapshot`; next successful poll heals the UI).
- **Single wire boundary.** `bridge/herdr-client.ts` is the *only* module that knows Herdr socket
  method names; it maps to an internal domain model (`bridge/types.ts`). A Herdr API rename is a
  one-file fix.
- **The UI is a static PWA served from `web/dist` on disk** at request time, so a UI rebuild is live
  with no restart (backend changes still need a service restart).

---

## 2. Herdr protocol (verified socket contract)

Reference: `HERDR_API.md` (empirically re-probed against Herdr 0.7.2 / protocol 16, cross-checked vs
`herdr api schema --json`). This file alone is worth the audit — it's a reusable, verified spec.

- **Transport:** Unix socket at `$HERDR_SOCKET_PATH` (default `~/.config/herdr/herdr.sock`).
  Newline-delimited JSON. Request `{id, method, params}`; **`id` must be a string** (int →
  `invalid_request`). **RPC is one-shot** — server closes the connection after one reply; one request
  per connection. **Only `events.subscribe` keeps the connection open.**
- **Methods the bridge uses:** `session.snapshot {}` (one RPC → whole herd: workspaces, tabs, panes,
  agents, layouts, focused ids — new in 0.7.2, with a permanent fallback to the `workspace.list` +
  `pane.list` + `tab.list` trio on older servers); `pane.read {pane_id, source, lines, format}`
  (`source ∈ visible|recent|recent-unwrapped`, `format ∈ text|ansi`; **`format:"text"` is clean, no
  ANSI = no XSS surface**); `pane.send_text {pane_id, text}` (literal, no Enter); `pane.send_keys
  {pane_id, keys}`; `agent.send {target, text}` (literal text, no Enter — follow with an Enter key to
  submit).
- **`pane.send_keys` key grammar (verified, NOT tmux):** bare special keys `Up Down Left Right Tab
  Enter Escape Space Backspace(BS) F1..F12`; single literal chars (digits/letters/punct); `+`-joined
  modifier chords `ctrl+c ctrl+u shift+tab alt+f …` (same grammar as `config.toml [keys]`);
  multi-modifier in any order incl. triple. **Unsupported (→ `invalid_key`):** tmux `C-c`/`BTab`, and
  `PageUp PageDown Home End Insert Delete`. Server validates every key. **Consequence: Ctrl-C is
  `ctrl+c`, not `C-c`; a menu past 9 options can't be answered (`"10"` is unsendable).**
- **Structural RPCs (verified):** `pane.rename`/`tab.rename`/`workspace.rename` (pane.rename accepts
  `null` to clear and emits **no event**; the other two require non-null and emit events);
  `pane.close`/`tab.close` (tab.close is a **bulk pane-close** — kills every pane inside);
  `tab.move`/`workspace.move` (**tabs: array order authoritative, numbers stable — never sort by
  number; workspaces: renumbered, sort-safe**; `insert_index` counts the pre-removal list).
- **Event catalog** (`events.subscribe {subscriptions:[{type, pane_id?}]}`): ack is
  `subscription_started`; event lines are `{event:<snake_case>, data:{…}}` while subscription `type`
  values are dot-form. Pane-scoped events (`pane.agent_status_changed`, `pane.scroll_changed`,
  `pane.output_matched`) **require** `pane_id`; everything else is global.
- **Two critical protocol gaps** that shape the whole design and that we must re-verify on our target
  Herdr build:
  1. **`revision` is a stub on Herdr 0.7.x — always `0`, even for actively-changing panes.** So it's
     useless as a change detector; Collie invents content signatures to compensate (see §5).
  2. **No raw output-stream event** — there is nothing to stream, hence poll-not-stream. (0.7.2's
     `herdr terminal session observe/control` CLI *can* stream live ANSI frames but needs a real
     terminal emulator to render — parked in `ARCHITECTURE.md` §8, deliberately not built.)
- `agent_status ∈ idle | working | blocked | done | unknown` (`HERDR_API.md`, "Object shapes").

**Adopt:** reuse `HERDR_API.md` wholesale as our starting spec, and re-derive it fast with
`herdr api schema --json` against our own server. **Verify first:** whether our Herdr exposes a real
monotonic `revision` and whether `pane.read(format:"ansi")` is SGR-only — both would let us delete
large amounts of Collie's complexity.

---

## 3. Terminal rendering

Reference: `web/src/lib/ansi.ts`, `web/src/components/ansi-output.tsx`.

Collie is a **read-only colored mirror + keystroke injector, not a terminal emulator.** It never runs
a PTY client; it polls `pane.read` and renders inert text.

- **`ansi.ts` is a deliberately minimal SGR-only parser** (`ansi.ts:1-5` documents the bet: Herdr's
  ANSI output is SGR-only — no cursor addressing). `parseAnsi` (`ansi.ts:135-244`) walks char-by-char
  into segments each carrying a **pre-computed `CSSProperties`** (`ansi.ts:107-119`) so render does
  zero allocation. Supports reset/bold/dim/italic/underline/strike/inverse, 16/256/truecolor incl.
  colon-delimited ISO 8613-6 (`ansi.ts:56-95, 205-216`). CSI private markers consumed; **cursor-move
  CSI is parsed-and-discarded**; OSC skipped to BEL/ST.
- **Clever CR handling** (`ansi.ts:121-133, 164-177`): CRLF's `\r` is a no-op terminator, but a
  mid-line `\r` triggers **last-write-wins** (splice out segments since line start), collapsing
  spinner/progress redraws to their final frame. Documents the "empty mirror bug."
- **Muted box-drawing** (`ansi.ts:100-104`): greys pure Unicode rule/box glyphs (excludes ASCII
  `-`/`=` so markdown survives) — materially improves mobile readability.
- **XSS boundary is airtight and intentional** (`ansi-output.tsx:86-88`): **text is always a React
  text node, never `innerHTML`;** only color/weight derive from the parse. `React.memo` + memoized
  `parseAnsi → splitLines → buildBlocks` pipeline (critical at 1.5 s poll cadence). Non-wrap mode
  horizontally pans wide TUI tables (`ansi-output.tsx:71-84`).

**Adopt:** the SGR-only bet (no xterm.js, tiny bundle, no XSS surface), pre-computed styles + memo
boundaries, last-write-wins CR collapse, muted-glyph treatment. **Downside:** correct *only if*
upstream never emits cursor addressing — full-screen TUIs (vim, htop, an alt-screen redraw) render as
garbage. Confirm our `pane.read` contract or budget for a real emulator.

---

## 4. Prompt/harness parsing — the key differentiator

Reference: `web/src/lib/blocks.ts`, `web/src/lib/harness/**`, `HARNESS_CONTRIBUTING.md`,
`web/src/fixtures/panes/`.

This is Collie's most sophisticated and most valuable idea. Instead of showing a raw screenful, it
recognizes a Claude Code TUI dialog at the buffer tail and **lifts it into a typed model rendered as
native tappable buttons**, stripping the agent's own input-box chrome.

- **Block AST** (`blocks.ts:100-105`): discriminated union `raw | prompt-select | wizard |
  preview-select | multi-select`. `splitLines` invariant: joining raw blocks reproduces the mirror
  char-for-char (relied on by find). Core has **no dependency on agent grammars** (`blocks.ts:6-14`).
- **Registry / adapter seam** (`harness/registry.ts`, `harness/types.ts`): a `HarnessAdapter =
  {agent, buildBlocks, extractStatusLine, extractInputDraft}` keyed by Herdr `agent` string. Map
  built *from* the adapter list so keys can't drift; `Object.hasOwn` guards prototype pollution.
  **Only `claude` exists today; every other agent keeps the raw mirror — fail-safe by construction.**
  Adapters are **detection only**; keystroke recipes live in core.
- **Grammar style** (pure functions over `StyledLine[]`, `HARNESS_CONTRIBUTING.md`,
  `fixtures/panes/README.md:97-106`): (1) match *parsed* text not raw bytes (SGR sits between
  glyphs); (2) **footer is the discriminator** (`markers.ts:75-82` maps the bottom hint bar to a
  family); (3) **tail-anchoring** — the dialog must be the last non-blank content, so a scrolled-up
  menu doesn't match. Four detectors ordered by specificity: preview → wizard → multi-select →
  prompt-select → chrome-strip (`harness/claude/index.ts:25-76`). Bails to raw on >9 options
  (unsendable `"10"`) and on free-text rows (they belong to the composer).
- **Content signature + race guard — the safety backbone.** Because `revision` is a stub, each model
  carries a **byte-hash of the on-screen region including the subject above the options**
  (`SIGNATURE_LOOKBACK=40`, `prompt-select.ts:56-62`), normalized to strip transient pointer/checkbox
  state. Every tap runs `harness/guard.ts` `entryGuard` (fresh re-read → re-derive → compare) and
  `pollUntil` (three-valued ok/drifted/timeout) so a stale tap on a drifted dialog aborts before
  anything irreversible is typed. `sanitizeTypedText` strips C0/C1 controls from pasted text.
  Multi-select **Submit is a closed-loop macro** (`multi-select-action.ts:186-223`): walks the pointer
  down, re-reads each step, only Enters when a fresh read confirms `pointer==="submit"`.
- **The capability tier ladder & fail-closed contract** (`HARNESS_CONTRIBUTING.md`): Tier 0 raw
  mirror (free) → Tier 1 read-only lift → Tier 2 interactive (types into a real shell; requires dated
  fixtures + choreography notes + green conformance + maintainer live-verification). **"A detector
  MUST return `null` on anything it doesn't confidently recognise" — a partial lift types a stray
  key into a live terminal.**
- **Two enforcement gates:** (1) **conformance suite** (`harness/conformance.ts`, globs the fixture
  corpus) asserts conservative detection (raw-only on foreign/neutral), tail-anchoring, and
  key-grammar validity (`isValidHerdrKey` rejects `"10"`, PageUp/Home/End/Delete, `C-c`). (2)
  **capability fence** (`harness/fence.test.ts`) fails the build if any `harness/` module except
  `guard.ts` imports the network API — a static I/O-purity guarantee for the parsers.

**Adopt:** the entire approach — registry/adapter seam, footer-discriminator + tail-anchor + bounded
grammar, content-signature freshness (mandatory while `revision` is stubbed), the generic race guard,
the closed-loop keystroke macro, and above all the **conformance + fence CI gates** and **fail-closed
contract**. This is the blueprint for doing keystroke lifting *safely*.

**Downside / brittleness:** hardwired to Claude Code's exact English TUI as of ~2026-07-04. Literal
anchors (`markers.ts:75-82`, `wizard.ts:295-306`) break on a Claude restyle/localization/theme; the
wizard's current-chip detection depends on a background SGR color it may not know on unseen themes.
Every new agent needs a hand-written grammar + fixtures (no generic detector). Fixtures are a
point-in-time snapshot — they pass forever even after the real TUI drifts (**no live/e2e test →
silent grammar rot**; budget periodic re-capture). Several real dialogs silently fall back to raw
(UX cliff). Most of this complexity exists to paper over the stubbed `revision`.

---

## 5. Bridge backend

Reference: `bridge/*.ts`.

- **HTTP server** (`bridge/server.ts`): single `Bun.serve` (`server.ts:98`), 12 MB hard body cap
  (`server.ts:34,103`), hand-rolled linear-`if` routing + two regexes (`server.ts:79,82,105-282`).
  Pane/tab ids from the path are only ever socket-RPC params, never filesystem paths. **Every
  response funnels through `secure()`** (`server.ts:892`) stamping `nosniff` + `no-referrer`.
- **Herdr socket client** (`bridge/herdr-client.ts`): one-shot JSON-RPC (`request()`
  `herdr-client.ts:100`) opens a fresh `Bun.connect({unix})` per call. Robustness worth stealing: a
  `settled` guard + idempotent `finish()` that clears timeout / resolves / closes the FD exactly once
  (no leak on timeout); `TextDecoder({stream:true})` so multibyte UTF-8 split across chunks isn't
  corrupted; 5 s timeout. `subscribeEvents()` is the only long-lived connection with its own terminal
  guard and multi-line drain. Backoff lives in `EventPoker`, not the transport.
- **State engine** (`bridge/state-engine.ts`): polls, builds `EngineSnapshot`, fires
  transition/remove/update listeners. Cadence 1.5 s hot, relaxes to 12 s while the stream is healthy
  (`config.ts:154-155`). **Event-poked polling** — `pokeNow()` polls immediately and queues *exactly
  one* follow-up if a poll is in flight; overlap guard prevents pile-ups. Snapshot fast-path with
  **permanent** fallback triggered only by an `"unknown variant"` error. First-sighting never fires a
  transition (no startup notification storm). Snapshot cached in-engine and served directly, so the
  HTTP layer never hits the socket for a snapshot.
- **Multi-session** (`bridge/sessions.ts`): a `SessionRegistry` of N `SessionRuntime`s; primary
  eager, others **discovered from the filesystem** and disposed when their socket vanishes (rescanned
  15 s). **Security invariant: client-supplied session name is only ever a `Map` key, never a path.**
- **Notifications** (`bridge/notifications.ts`) — the best backend idea for mobile: a
  `NotificationCoordinator` gives each session's whole herd **one notification slot** with a lifecycle
  — **debounce+cancel** (30 s window; an agent that blocks and unblocks never buzzes → infers
  "user at desk" since Herdr has no presence signal), **coalesce** ("claude needs you" or "N agents
  need you"), **retract** (resolving updates/clears the slot with `renotify:false`). Clock-injected
  for tests.
- **Push** (`bridge/push.ts`): zero hard dep — dynamic `import("web-push")`, silently disabled if
  missing/unset. Subscriptions persisted 0600 via tmp+rename, save-chain serialized, dead subs pruned
  on 404/410. **Per-message collapse `topic`** (`collie-herd` 6 h TTL, `collie-update` 3 d) so an
  offline device gets one *current* summary on reconnect, not a replay.
- **Supporting modules:** `audit.ts` (append-only JSONL of writes, 0600, newline-folded + 120-char
  truncated, **never throws**); `uploads.ts` (images single-use, 48 h TTL sweep, MIME allowlist,
  10 MB cap, server-generated filename); `http-cache.ts` (pure ETag/304 + gzip ≥256 B on poll
  endpoints — a real cellular win); `update.ts` (`releaseAvailable` from anonymous GitHub tags +
  `bridgeStale` self-staleness by source mtime/size — catches "rebuilt but not restarted");
  `config.ts` (env validated once at startup with strict int/bool/list parsers, safe loopback
  defaults); `wire.ts` (pure decoders); `types.ts` (domain model decoupled from wire, `STATUS_RANK`
  drives blocked-first triage).

**Adopt:** the single-slot coalesce/debounce/retract coordinator with inferred presence; poll-as-truth
+ events-as-pokes with single-queued follow-up; ETag/304/gzip on poll endpoints; the one-shot socket
adapter as sole wire boundary with idempotent terminal-path cleanup; per-message push collapse topics;
`bridgeStale` self-staleness; name-is-only-a-map-key multi-session discovery.

**Downside:** **`enrichSessionNames` screen-scrapes** each Claude pane's `/rename` name via regex on
rendered box-drawing every poll (`state-engine.ts:39,315`) — N extra `pane.read` round-trips per tick
and brittle to any Claude UI change; prefer a real metadata field. Notify prefs/snooze are
**bridge-wide, not per-device**. `req.formData()` buffers whole (≤10 MB) upload bodies in memory.

---

## 6. Frontend data / polling / PWA

Reference: `web/src/lib/**`, `web/src/hooks/**`, `web/src/sw.ts`.

- **Data layer is React Router loaders + `useRevalidator` polling** (no TanStack/data lib —
  `CLAUDE.md`). `lib/api.ts` is a thin same-origin REST client with per-request-class
  `AbortSignal.timeout` and **client-managed ETag pane caching** whose invariant (record ETag only
  *with* a parsed body, only *after* parse) avoids the "permanent blank pane" bug (`api.ts:129-193`).
- **Adaptive polling** (`hooks/use-polling.ts`): 1.5 s hot (any agent blocked/working or a pane open),
  4 s cold. **Deliberately never gates on `navigator.onLine`** (lies on phones, would wedge polling);
  self-heals a black-holed fetch via a 12 s wall-clock supersede.
- **Loaders keep-previous-data** and discriminate navigation vs revalidation so during an escalated
  outage a *navigation* returns cached data instantly while a *revalidation* keeps fetching to
  discover recovery (`lib/loaders.ts:157-182`).
- **Shared connection-health store** (`lib/connection-health.ts`, `useSyncExternalStore`): one
  module-scoped clock so pill/banner/pane-header/splash can't disagree; **sticky latch** so
  backgrounding mid-outage can't dishonestly downgrade red→amber. Thresholds 4 s amber / 15 s red.
- **PWA self-update — a standout** (`lib/self-update.ts`, `lib/pwa.ts`, `sw.ts`): SW is unreliable
  (never registers over plain HTTP; wedges when a proxy caches `sw.js`), so the bridge stamps its
  on-disk build id (`X-Collie-Build`) on **every** poll response and the client drives its own update
  when it drifts ahead of the running bundle — with hysteresis, a once-per-build reload-loop guard,
  and a **reload safety gate that never yanks the page while there's unsent work** (shows "tap to
  update" instead). Custom `injectManifest` SW so `push`/`notificationclick` actually work; push
  decision logic is split into a pure, testable `lib/push-decision.ts` module.
- **Push client** (`lib/push.ts`): a `PushAvailability` enum for every failure mode; **VAPID
  key-rotation handling** (byte-compares the subscription's `applicationServerKey` and re-subscribes
  on mismatch); no unsubscribe endpoint — server prunes on 410.

**Adopt:** loaders+revalidator (no extra data lib), ETag pane cache with parse-before-record, shared
connection-health store with sticky latch, never gating on `navigator.onLine`, and the
build-stamp-on-every-response self-updater with the unsent-work gate — the right answer for a
self-hosted PWA where the SW can't be trusted. **Downside:** heavy module-scoped mutable caches
(harder to test/reason about); polling is intrinsically laggy vs SSE/websocket; much of the
self-update machinery exists only to work around SW unreliability — reduce it if our transport is
better.

---

## 7. Mobile UX

Reference: `web/src/components/**`, `web/src/hooks/**`.

- **In-flow docks, never overlays** (`components/nav-tray.tsx`, `composer.tsx:85-111`): the
  special-keys pad and quick menus sit *above* the composer so the mirror/prompt stays visible while
  you drive a menu. Keys pad uses physical-keyboard geometry (Esc top-left, inverted-T arrows) + a
  phone-dialer 3×3 digit grid.
- **Tri-state modifier queue** (`lib/key-queue.ts`): modifiers cycle off→once→locked; `composeKey`
  joins in canonical order; a compose mode stages a visible key queue you review then Send as one RPC.
  `isDangerKey` flags `ctrl+c/d/z`.
- **Composer** (`composer.tsx`, ~670 lines): phone-owned input; image upload via picker + clipboard
  paste; agent-aware slash palette; two-tap destructive-command guard; **terminal-draft recovery** (a
  message stranded on the host `❯` line is shown read-only with "Take over", gated behind ~1.5 s
  stabilization + self-echo suppression; on send it sweeps the line with `ctrl+k` + overshoot
  Backspaces).
- **Hardened long-press** (`hooks/use-long-press.ts:24-36`): handles Android `contextmenu`, suppresses
  the follow-up click in capture phase, avoids `touch-action:none` so scroll still cancels — a
  catalog of real mobile gotchas.
- **Idle-lock** (`hooks/use-idle-lock.ts`): 30-min no-interaction lock that **unmounts the router to
  pause polling** (stolen-phone mitigation); wall-clock based so a throttled background tab locks
  correctly; visibility flips don't count as activity.
- **Destructive-action confirm** (`lib/destructive.ts`): small word-boundary-anchored pattern set
  (`rm -r`, `git push --force`, `sudo`, `dd if=`, `mkfs`, redirect-to-system-path) that won't fire on
  look-alikes ("assume", "sudoku"), ordered most-specific-first.
- **Triage:** dashboard leads with **"Needs you"** (blocked agents float to top, working/idle
  collapsed); simultaneous blocks batch into one notification. Session switcher renders only when
  there's a real choice, greys unreachable sessions, shows per-session "N needs you" badges.

**Adopt:** in-flow docks over the mirror, tri-state modifier queue, hardened long-press,
idle-lock-pauses-polling, terminal-draft recovery, word-boundary destructive guard, blocked-first
triage. **Downside:** the composer is a large, subtly-latched state machine (maintenance risk); the
draft-sweep "overshoot Backspace ×N" races the PTY over a poll gap.

---

## 8. Auth / network / security model

Reference: `README.md#security`, `ARCHITECTURE.md` §6, `bridge/server.ts` `checkAccess`/`guard`/`deviceAuth`.

**Honest self-description:** "Collie is remote shell access to your machine, by design… Treat the URL
like a root login." One bridge call types arbitrary keystrokes into a live terminal — no sandbox, no
command allow-list (that would defeat the purpose). Access is **device-level, not person-level**
(Tailscale proves the device, not who holds the phone). The idle-lock is UX, not auth.

Defenses, and the exact enforcement order in `checkAccess` (`server.ts:792`):

1. **Loopback bind only** (`127.0.0.1`, `config.ts:153`) — the trust anchor for every header check.
   Binding `0.0.0.0` makes all identity checks theater.
2. **Host-header allowlist FIRST** (`COLLIE_PUBLIC_HOSTS`, `server.ts:801`) — runs before Origin logic
   to defeat DNS rebinding; effectively mandatory under plain-HTTP serve.
3. **Same-origin / CSRF gate** (`server.ts:805-821`) — request accepted iff `Origin` host == received
   `Host` (loopback always allowed, or an explicit `COLLIE_ALLOWED_ORIGINS`). Writes with no Origin
   from a non-loopback Host are rejected; reads with no Origin pass (the snapshot poll).
4. **Tailscale identity** (`COLLIE_TRUSTED_USER` vs `Tailscale-User-Login`, `server.ts:823-828`) —
   trusted only because `tailscale serve` injects it and the request is loopback; a *present*
   mismatched header is rejected, an *absent* header still passes (loopback tolerance).
5. **Per-device write gate** (`COLLIE_DEVICE_HEADER` + `COLLIE_DEVICE_ALLOWLIST`, `deviceAuth`
   `server.ts:880`) — allow-listed → full; other id → read-only; header absent → on-host operator;
   empty allowlist → every device read-only (fail-closed). **Only sound behind a proxy that
   authenticates the device and *overrides* the header on every request** — never on bare
   `tailscale serve` (which forwards a client-set `X-Device-Id` untouched).
6. **Strict CSP on HTML** (`default-src 'self'`, `script-src 'self'`, `base-uri 'none'`,
   `frame-ancestors 'none'`) + terminal output as React text nodes (the XSS boundary).

Defense-in-depth: audit log of every write (0600); destructive-input confirm; uploaded-image TTL;
`systemd` hardening (`NoNewPrivileges`, `PrivateTmp`, `StartLimitIntervalSec=0`, `RestartSec=5`).
Security evolution is visible and mature in the CHANGELOG: a **0.3.0 four-agent audit pass** (socket
FD leak on RPC timeout fixed, UTF-8 chunk corruption fixed, overlapping-poll pileups fixed, upload
memory pre-check, `COLLIE_PUBLIC_HOSTS`, origin-required-for-writes); **0.9.1 removed one-tap
yes/no notification buttons** because they approved blind without opening the app; 0.4.0/0.5.0
tap-race signature hardening.

**Adopt:** the whole layered gate — loopback anchor, Host-before-Origin ordering, read/write privilege
split (notification management is read-level), device-auth fail-closed matrix, strict CSP + React-text
rendering, honest threat-model docs, audit log that never fails the action, the "never `funnel`" rule.

**Downsides to avoid:**
- **Secure defaults are opt-in-and-warn, not fail-closed.** Empty `COLLIE_TRUSTED_USER` = any tailnet
  device gets full write (only a startup *warning*); empty `COLLIE_PUBLIC_HOSTS` = DNS rebinding not
  blocked. **We should ship fail-closed.**
- **Gating is per-handler, not centralized** — `/api/config` shipped with *no* gate (leaks
  push-enabled + VAPID public key + build id unauthenticated). A new route is unprotected unless the
  author remembers `guard()`. **Centralize the gate.**
- Write-auth collapses silently if anything reaches the port directly or a proxy forwards a
  client-supplied identity header — the loopback + trusted-proxy invariant must be impossible to
  misconfigure.
- **`COLLIE_MULTI_SESSION` defaults on** and fronts *every* named Herdr session under the config root
  through the same URL (incl. sandbox/private sessions) — a real blast-radius surprise.
- Device-level (not person-level) auth + no per-device snooze/prefs limits multi-user use.

---

## 9. Install / update / operational reliability

Reference: `scripts/collie-ctl.sh`, `herdr-plugin.toml`, `systemd/collie.service`, `CLAUDE.md`,
`.github/workflows/`.

- **Install:** `herdr plugin install AltanS/collie` (Herdr clones + runs the `[[build]]` step) or
  `herdr plugin link "$(pwd)"` for dev (builds lazily on first `start`). `start` builds `web/dist`,
  starts the `systemd --user` service, runs `tailscale serve --bg`, prints a URL banner. Requires only
  **Bun** (hard dep), Herdr ≥ 0.7.0, Tailscale, git; Node.js and systemd are *soft* (nohup + pidfile
  fallback without systemd).
- **Control script** (`scripts/collie-ctl.sh`) is the single source of ops truth, shared by the
  plugin actions. Highlights: config-dir resolution reconciled across Herdr-injected vs direct
  invocation (a real past bug where two entry points read different `.env` files); **atomic staged
  build** (`dist-staging` → swap) so a failed build never empties a live `web/dist`; a **real TCP
  readiness probe** via bash `/dev/tcp` (not just "unit active"); `update` re-execs the freshly-pulled
  script (bash reads by byte offset) and **re-links the plugin** so Herdr picks up new actions;
  `unserve` removes only Collie's own port-scoped serve mapping, never a blanket reset.
- **Reliability:** service `Restart=on-failure`, `RestartSec=5`, `StartLimitIntervalSec=0` ("never
  give up restarting — a phone-only operator can't run `reset-failed`"); `loginctl enable-linger` for
  headless boot; `tailscale serve --bg` persists across reboots.
- **Versioning is hook-enforced** (`CLAUDE.md`): version must agree across `herdr-plugin.toml` +
  `package.json` + `web/package.json` + newest CHANGELOG heading (`scripts/check-version.sh`, a
  pre-commit and pre-push hook, and the CI `Version consistency` step). CI (`.github/workflows/ci.yml`)
  runs version check + typecheck + tests on both sides with `--frozen-lockfile`; `release.yml`
  auto-creates a GitHub Release on a `v*` tag.
- **Deployment variants:** A = `tailscale serve` + person identity (default); B = identity-aware proxy
  + per-device auth; C = reverse proxy as sole front door (`COLLIE_SKIP_SERVE=1`, no Tailscale).
  Bridge always binds loopback; only the front door changes.

**Adopt:** the thin-launcher-plugin + supervised-service split; atomic staged build + real readiness
probe; self-re-execing/self-re-linking update; port-scoped serve teardown; hook + CI version
enforcement; the three documented deployment variants. **Downside:** link-mode "the checkout *is* the
plugin" + no native `herdr plugin update` forces the bespoke `update`/re-link dance; a fresh Herdr
install failed with TS2688 until 0.10.3 because only `web/` deps were installed (root types missing) —
watch the two-dependency-tree install.

---

## 10. Testing

- **Bridge** (`bun test`, `bridge/*.test.ts`): ~2,600 test LOC, ~1:1 with source. Core technique:
  **pure-function extraction + injected seams** — anything touching `Bun.serve`/socket/timers/fs/
  web-push is pulled into a pure exported function or hidden behind an injected fake
  (`checkAccess`/`deviceAuth`/`startupWarnings` exported pure; `NotifyClock` fake clock; `FakeHerdr`
  driving `StateEngine`; `GatedHerdr` promise-gates a hung poll to test in-flight/queued-poke races).
- **Web** (Vitest + Testing Library + MSW, no headless browser): 79 files / ~785 cases. Centerpiece is
  the **byte-faithful pane fixture corpus** (`web/src/fixtures/panes/*.txt` via
  `scripts/capture-fixture.sh`) + the **globbed conformance suite** that turns "did we break dialog
  parsing" into a CI gate, plus the import fence. Dedicated regression fixtures encode past bugs
  (numbered-plan-body, self-echo window, wrapped drafts).

**Adopt:** the pure-extraction discipline, the fixture-corpus + conformance gate, the "lessons already
encoded here" fixture README. **Downside — two real gaps:** `herdr-client.ts` (the actual socket
transport, the trickiest code) has **no test**; there is **no integration test standing up
`Bun.serve`**, so a composition bug (like the ungated `/api/config`) isn't caught. And fixtures are a
point-in-time snapshot with no live/e2e test → grammars silently rot when Claude's TUI drifts.

---

## Concrete ideas to adopt (ranked)

1. **Reuse `HERDR_API.md` as our verified protocol spec**; re-derive with `herdr api schema --json`.
   First verify whether our Herdr exposes a real `revision` and SGR-only `pane.read` — both delete
   large amounts of complexity.
2. **Thin launcher plugin + supervised `systemd --user` bridge, poll-as-truth + events-as-pokes.**
   The single most important architectural decision.
3. **Harness-adapter framework**: registry seam, footer-discriminator + tail-anchored pure grammars,
   **content-signature race guard** (mandatory while `revision` is stubbed), **conformance + import-fence
   CI gates**, and the **fail-closed "return null unless certain" contract.**
4. **SGR-only mirror rendered as React text nodes** — no emulator, no XSS surface, tiny bundle.
5. **Single-slot coalesce/debounce/retract notification coordinator** with inferred presence + push
   collapse-topics.
6. **Layered same-origin security** with loopback anchor + Host-before-Origin + read/write privilege
   split — but **ship it fail-closed and centrally-gated** (Collie's two weaknesses).
7. **PWA build-stamp self-updater** with an unsent-work reload gate; ETag/304/gzip on poll endpoints;
   shared connection-health store; never gate polling on `navigator.onLine`.
8. **Mobile UX primitives:** in-flow docks over the mirror, tri-state modifier key queue, hardened
   long-press, idle-lock-pauses-polling, terminal-draft recovery, word-boundary destructive guard.
9. **Ops rigor:** atomic staged build, real TCP readiness probe, self-re-execing update, hook + CI
   version enforcement, three documented deployment variants.
10. **Pure-function + injected-seam + byte-faithful-fixture testing style.**

## Concrete downsides to avoid

1. **Opt-in-and-warn security defaults.** Make trusted-user / host-allowlist / device-auth
   fail-closed by default, not a warning.
2. **Per-handler (decentralized) auth gating** — it shipped an ungated `/api/config`. Centralize.
3. **`COLLIE_MULTI_SESSION` on by default** fronting every session (incl. sandbox) through one URL —
   surprising blast radius. Default to primary-only or make discovery explicit.
4. **TUI screen-scraping for metadata** (session names) — N extra socket reads/poll and brittle to
   Claude UI changes. Use real metadata fields.
5. **Grammar rot with no live test** — fixtures pass forever after the real TUI drifts. Add a periodic
   re-capture / e2e discipline, and keep a generic raw-mirror fallback.
6. **Untested socket transport + no `Bun.serve` integration test.** Cover the wire client and add a
   thin end-to-end server test.
7. **Bridge-wide (not per-device) snooze/prefs**, device-level (not person-level) auth — limits any
   multi-user or shared-device scenario.
8. **Whole-body in-memory upload buffering** (`req.formData()` ≤10 MB each) — a concurrency memory
   spike; stream to disk.
9. **A ~670-line subtly-latched composer** — powerful but a maintenance/regression risk; decompose the
   draft-recovery state machine.
10. **Bus-factor 1** on the upstream project — if we depend on Collie itself (vs. its ideas) that's a
    supply risk; we're building our own, so treat it as reference, not a dependency.

---

*Sources: local clone at HEAD `afa53fe` (release 0.14.2, 2026-07-23). Cited paths are relative to the
Collie repo root. Docs: `README.md`, `ARCHITECTURE.md`, `HERDR_API.md`, `HARNESS_CONTRIBUTING.md`,
`CLAUDE.md`, `CHANGELOG.md`. Verified against the source under `bridge/`, `web/src/`, `scripts/`,
`systemd/`, `herdr-plugin.toml`, `.github/workflows/`.*
