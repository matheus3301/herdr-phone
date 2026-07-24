# herdr-phone Specification Audit

Status: read-only conformance audit of the current tree against `SPEC.md` (v0.1.0 implementation contract).
Date: 2026-07-23
Scope: SPEC.md, README.md, SECURITY.md, scripts, CI/release workflows, all Go packages under `internal/` + `cmd/`, and the `web/` frontend.
Method: static read + build/test execution + end-to-end wiring trace (UI control → `internal/server/mutations.go` allowlist → `internal/integration/mutate.go` decode → `internal/herdr/*` wire call). No code was modified.

---

## 1. Verdict

The implementation is **substantially complete and genuinely wired**, not scaffolded. Build, vet, the full Go suite, and the frontend unit suite all pass; the protocol, auth, terminal-bridge, ANSI-filter, confirmation-nonce, idempotency, deadline, and excluded-operation boundaries are real and tested end to end.

However, it does **not** meet the Definition of Done (SPEC §22). One required remote feature is completely non-functional and unreachable from the UI, one explicit security "never" is violated, and several §15 feature actions have no UI or a route/field mismatch that silently no-ops. These are the actionable gaps below.

Verification snapshot:

| Check | Result | Evidence |
|---|---|---|
| `go build ./...`, `go vet ./...` | pass (exit 0) | run 2026-07-23 |
| `go test ./...` | 501 pass, 15 pkgs, exit 0 | no network/Herdr needed |
| `cd web && npm test` (vitest) | 93 pass, 22 files, exit 0 | — |
| Playwright | `web/test-results/.last-run.json` = `{"status":"passed","failedTests":[]}` | flaky, see M7 |
| Version pins | Go 1.26 in `go.mod:3` + `mise.toml:13`; `0.1.0` in `buildinfo.go:9`, `herdr-plugin.toml:3` | agreement tested (`manifest_test.go`, `goversion_test.go`) |

---

## 2. Requirement-to-Evidence Matrix

### 2.1 Definition of Done (§22)

| DoD item | Status | Evidence / note |
|---|---|---|
| Every required file exists, real content, no TODO/FIXME/fake paths | **OK** | grep clean; only "not implemented" is a defensive default branch (`integration/mutate.go:275`) |
| `mise.toml` and `go.mod` pin Go 1.26 | **OK** | `go.mod:3`, `mise.toml:13`, enforced `internal/app/goversion_test.go` |
| `make check` and `go test -race ./...` pass | **OK** | 501 tests pass; all 14 make targets real (`Makefile`) |
| Frontend unit, component, Playwright journeys pass | **PARTIAL** | unit/component pass; takeover journey flaky (M7) |
| Plugin manifest validates against Herdr 0.7.5 | **OK** | `herdr-plugin.toml`, `manifest_test.go` |
| Fake-cloudflared E2E for named + quick startup/teardown | **OK** | `internal/tunnel/*_test.go`, `internal/daemon/*_test.go` |
| Local Herdr smoke: snapshot/events/terminal/input/resize/create ws+tab/split/prompt/close/reconnect | **OK (unit-level)** | protocol + adapter tests cover each op; no real-Herdr smoke harness in tree |
| Named mode rejects missing/invalid Access; verifies JWTs | **OK** | `config.go:494-529`, `auth/jwt.go:202-281`, `auth/jwks.go` |
| Quick mode cannot start without opt-in + mandatory pairing | **OK** | `config.go:484-488`; pairing enforced `integration/auth.go:75-83` |
| No secret in argv/logs/state/browser storage/snapshots/git | **VIOLATED** | session token written to `audit.jsonl` — see **H1** |
| UI usable at 390px with keyboard; screenshot/keyboard/reduced-motion | **OK** | safe-area + visual-viewport handling, reduced-motion, aria labels present |
| README commands match binary/implementation | **OK** | README CLI + make targets all map to real impls |
| Final security/code review finds no high-severity issues | **NOT MET** | H1–H3 below |

### 2.2 Required Remote Features (§15) — end-to-end wiring

| Feature | Backend path | UI control | Status |
|---|---|---|---|
| Workspaces list/switch/create/rename/close(confirm) | real (`mutate.go:24-59`) | real (`topology-ribbon.tsx`, `spaces.tsx`, `create-sheets.tsx`) | **OK** (create nav no-ops — M3) |
| Workspace worktree provenance + aggregate agent status | real (`models.go:37-46`) | real | **OK** |
| Tabs list/switch/create/rename/close(confirm) | real (`mutate.go:62-111`) | real | **OK** |
| **Tabs move** | real (`tab.move {tab_id,insert_index}`) | **none** | **MISSING — H2** |
| Panes focus/split R+D/resize/zoom/swap/rename/close | real (`mutate.go:114-190`) | real (`pane-actions.tsx`) | **OK** |
| Panes move → new tab / new workspace | real | real (`pane-actions.tsx:96-97`) | **OK** |
| **Panes move → existing tab** | real (`MoveToTab`, `mutate.go`) | **none** | **MISSING — M4** |
| Terminal open/resize/scroll/reconnect/takeover | real (`bridge.go`, `terminalroute.go`) | real (`terminal-view.tsx`) | **OK** |
| Agents blocked-first list + metadata/focus/prompt/rename | real | real (`herd.tsx`, `agent-actions.tsx`) | **OK** (ordering FE-only, L7) |
| **Agents start discovered kind in a pane** | real (`agent.start`, kinds discovered `agentkinds.go`) | **unmounted + malformed** | **BROKEN — C1** |
| **Agents send validated logical keys** | real (`agent.send_keys`, validated `keys.go`) | **no caller (dead op)** | **H3** |
| Worktrees list/create/open/remove + force(2-step) | real (`mutate.go:238-272`; both `remove`/`remove_force` need nonce) | real (`worktree-sheet.tsx`) | **OK** |
| Excluded ops NOT reachable (server.stop, handoff, plugin/integration install, arbitrary methods/process, raw file reads) | enforced (allowlist `mutations.go:25-61`, default reject `mutate.go:275`, fixed argv `controller.go:61-68`) | — | **OK** |

### 2.3 Security / Auth / Config / Tunnel (§8, §9, §3.2–3.3, §17)

All core controls verified present and correct: strict TOML with unknown-key rejection (`config.go:293-300`), `~`/env expansion erroring on unset (`paths.go:22-46`), no shell exec anywhere, exactly-one credential strategy for named mode (`config.go:516-529`), 256-bit single-use constant-time pairing secret with rotation (`pairing.go:14,79-101`), `__Host-` HttpOnly/Secure/SameSite=Strict cookie (`session.go:220-230`), full RS256 JWT verification with JWKS TTL/stale-bound/singleflight and fail-closed (`jwt.go:202-281`, `jwks.go:140-174`), central ordered middleware (`routes.go:97-179`), token never in argv with 0600 temp file deleted on ready (`args.go:34-38`, `supervisor.go:199-201`), bounded exponential restart → degraded (`backoff.go`, `supervisor.go:242-255`), and full ANSI filtering of OSC 52/8/DCS/APC/PM/DSR/answerband (`security/ansi.go`). Deviations are H1, M1, M2, L1–L3 below.

---

## 3. Ranked Actionable Gaps

### CRITICAL

**C1 — "Start a discovered agent kind" (§15) is unreachable and malformed.**
`StartAgentSheet` is defined once (`web/src/components/agent-actions.tsx:104`) and **never imported or mounted** anywhere in the app. The herd empty state even tells the user to "Start an agent from a shell pane in Spaces" (`web/src/routes/herd.tsx:95-96`), but `web/src/routes/spaces.tsx` exposes no such control — a broken promise. Compounding it, the sheet dispatches `agent.start` with `{ pane_id, kind }` and **no `name`** (`agent-actions.tsx:113`), while the backend rejects any empty/invalid name (`ValidAgentName`, `internal/herdr/agents.go:143`). So even if it were mounted, every call would fail `invalid_params`. This is a required §15 feature that cannot be exercised.
Fix: mount a start-agent control on a shell pane, include a validated `name`, and populate kinds from `GET /capabilities` (kinds are correctly server-discovered — `internal/integration/agentkinds.go`).

### HIGH

**H1 — Active session token is written to `audit.jsonl` (violates §7 "never … cookies" and §9.1 "sessions live only in daemon memory").**
`sess.ID` is used verbatim as both the cookie value (`internal/integration/auth.go:90` `NewSessionCookie(sess.ID, …)`) and as `Identity.SessionID` (`auth.go:92,140`), which is recorded to the on-disk audit file on `pair.success` and `session.revoke` (`internal/server/pairing.go:60,99`; persisted `internal/server/audit.go:69`). The audit file is mode 0600, but the raw cookie value in a file is exactly what §7 forbids: anyone who can read `audit.jsonl` can hijack a live session.
Fix: audit a non-reversible handle (hash or short prefix of the session id), never the cookie value.

**H2 — `tab.move` has no UI (§15 "tabs … move").**
The backend fully supports it (`tab.move {tab_id, insert_index}`, `internal/integration/mutate.go`), and the operation name is in the TypeScript union (`web/src/lib/types.ts:380`), but **no component calls it** (grep: zero callers). Required feature is unreachable.

**H3 — `agent.send_keys` validated-key mutation is dead (§15 "send validated logical keys").**
The operation exists and is validated server-side (`internal/herdr/agents.go:61-72`, `keys.go`), and is in the TS union (`web/src/lib/types.ts:392`), but **nothing invokes it** — the key dock encodes chords and writes raw bytes to the terminal socket (`key-dock.tsx` → `terminal-socket.ts`). Logical keys do reach a *focused, open* terminal, but the specified validated agent-level operation is unused, so sending keys to an agent without an open terminal WS (e.g. from the herd view) is not possible.
Fix: wire the dock (or an agent action) to `agent.send_keys` for the validated path, or document the terminal-socket route as the intended substitute.

### MEDIUM

**M1 — CSP is looser than §9.3.** `internal/server/server.go:216-217` emits `connect-src 'self' ws: wss:` (wildcards every WebSocket host, not "the same-origin WebSocket") and `style-src 'unsafe-inline'`. The spec-correct, origin-scoped builder already exists but is unused (`internal/security/headers.go:20-45`, `BuildCSP` + `WebSocketConnectSrc`). Fix: wire `security.BuildCSP` with the public URL.

**M2 — Secret log redaction is never invoked (§17).** `internal/security/redact.go` implements `RedactSecrets`/`SanitizeForLog` for JWTs, `pair=`, and Authorization/Cookie headers, but no production log path calls it — logging relies only on control-char stripping. If any line ever carries a token, it will not be redacted. Fix: route daemon/cloudflared/server log writers through `security.SanitizeForLog`.

**M3 — Create-workspace/create-tab navigation silently no-ops (route/field mismatch).** The frontend reads `res.result.root_pane_id` (`web/src/components/create-sheets.tsx:28,90`), but the backend returns `root_pane` — a `Pane` object, not an id (`internal/herdr/mutations.go:15-19,95-99`). `paneId` is always `undefined`, so the app creates the workspace/tab but never navigates into the new pane. The unit test masks this by mocking the wrong shape `{ root_pane_id: "w9:p1" }` (`create-sheets.test.tsx:21`), giving false confidence. Fix: read `result.root_pane.pane_id` and correct the test fixture.

**M4 — `pane.move` to an *existing* tab is missing (§15).** Only "new tab" and "new space" are offered (`web/src/components/pane-actions.tsx:96-97`); the backend `MoveToTab` (`destination.type:"tab"`) has no caller.

**M5 — Key dock is missing the required "paste" affordance (§14.4).** The dock has Esc/Tab/Ctrl/Alt/Shift (tri-state ✓)/arrows/Enter/Space/^C, but no paste control (`web/src/components/key-dock.tsx`; grep for `paste`/`clipboard` in components is empty). OS paste works only inside the composer textarea; the specified dock button is absent.

**M6 — GoReleaser↔build.sh archive-name agreement is claimed "enforced by tests" but is not (build/release drift).** `.goreleaser.yml:36` states the naming "must stay in sync with scripts/build.sh (enforced by tests)", but no such test exists (no `TestGoreleaser*`). The templates currently match, but a rename would silently break the checksum-verified offline-download fallback (`scripts/build.sh:96` vs `.goreleaser.yml:38`) with no CI signal.

**M7 — Takeover Playwright journey is flaky (§18/§22 require deterministic pass).** At audit start `web/test-results/` contained failure screenshots for `journeys-takeover-…-pixel-7`; a rerun then produced `.last-run.json = passed` with the artifacts cleaned. The takeover spec (`web/e2e/journeys.spec.ts:127-148`) is the most race-prone (multi-page, `reset:false`, in-code "avoids a takeover race" comments, 15–20s waits). Transient-fail-then-pass is a flake, not a deterministic pass.

### LOW

- **L1 — `min_herdr_version` (0.7.5) is not enforced at runtime.** `Handshake` checks only protocol 17 (`internal/herdr/ping.go:32-42`); `MinHerdrVersion` (`herdr/doc.go:22`) is never compared to `pong.Version`. Mitigated because Herdr gates plugin launch on the manifest `min_herdr_version`, and §3.1 literally only requires verifying protocol 17. The unused constant is the smell.
- **L2 — Token trims all whitespace, not "once" (§8).** `internal/tunnel/token.go:46` uses `strings.TrimSpace`; the spec-correct single-trailing-newline trim (`internal/config/secret.go:89-99`) is dead code.
- **L3 — Duplicate, divergent implementations.** `internal/security` (`Middleware`/`BuildCSP`/`RedactSecrets`), `internal/config/secret.go` (`ResolveSecretCommand`), and `internal/herdr/resolve.go` are fully implemented but unused in prod (which reimplements them in `server/`, `integration/`, `tunnel/`). This divergence directly produced M1, M2, and L2. Consolidate or delete.
- **L4 — Terminal-input generation guard is per-connection, not per-frame.** Checked once at WS upgrade (`internal/server/terminalroute.go:70-80`); later input bytes aren't re-checked. Impact bounded — the controller closes on pane exit.
- **L5 — Generation guard skippable for agent ops addressed by target.** `internal/server/mutations.go:235` only runs for `pane_id`+`generation>0`; an `agent.prompt`/`send_keys` addressed by agent name with generation 0 bypasses it (`integration/mutate.go:204,214`). Non-destructive ops only.
- **L6 — Pane generation is dropped, not incremented, on exit/close/move (§11 wording).** `internal/state/engine.go:250-255` removes the id instead of incrementing; behaviorally safe (stale generation lookups fail the guard).
- **L7 — Blocked-first ordering is frontend-only; "last state transition" is a seq, not a timestamp.** `internal/herdr/reads.go:138-139`, `models.go:108` (`StateChangeSeq`). All data is in the snapshot; only presentation/field type differs from §15 wording.
- **L8 — `freeze` revalidation trigger declared but not registered (§16).** Listed in `REVALIDATE_EVENTS` but not subscribed in `onRevalidate` (`web/src/lib/reconnect.ts`).
- **L9 — CI names nonexistent test filters.** `.github/workflows/ci.yml:77` runs `-run 'Manifest|ConfigExample|Goreleaser|GoVersion|NodeVersion'`, but `Goreleaser`/`NodeVersion` tests don't exist (silent no-op). No Node-22 version-agreement test across `mise.toml`/`ci.yml`/`build.sh`.
- **L10 — Topology-ribbon "swipe changes level" not implemented (§14.2).** Three offset layers render and chips scroll + tap-to-switch works, but horizontal swipe does not change hierarchy level (`web/src/components/topology-ribbon.tsx`).
- **L11 — Frontend component/route test coverage gaps.** No tests for worktree flows, `terminal-socket` lifecycle/takeover-nonce, `pane-actions`/`agent-actions`/`worktree-sheet`/`rename-dialog`/`terminal-view`/`key-dock`/`composer` components, or `spaces`/`terminal`/`settings` routes.
- **L12 — Minor structural drift from §5.** Two version constants (`buildinfo.go:19` and `herdr/doc.go:22`) vs "one version source"; packages `internal/integration/` and `internal/webui/` exist beyond the §5 tree (reasonable additions — orchestration and frontend embedding — but undocumented in the tree). Informational.

---

## 4. Deliberate Deviations (reviewed as acceptable / by design)

- **`GET /health` returns `instanceID` when a constant-time-validated probe token header is presented** (`internal/server/routes.go:220-237`). This reconciles §9.3 ("no instance detail") with §17 (Quick mode must self-probe the same one-time instance); the detail is gated behind a secret and never logged. Acceptable.
- **`server.host` is forced to exactly `127.0.0.1` unconditionally** (`config.go:446-448`), stricter than "in production." Acceptable (safer).
- **Embedded frontend is a committed fallback placeholder in a fresh checkout** (`internal/webui/dist/.fallback` present; real build lives in `web/dist`). `scripts/build.sh` copies `web/dist` into the package before compile, and a release start **refuses** to run on the placeholder (`internal/integration/stack.go:108`, `errFallbackAssets`). Safe by design, but build-step-dependent — a binary built without `build.sh` embeds the placeholder.
- **`start-quick` is `start --quick`, not a separate subcommand** (per manifest action mapping). Acceptable.

---

## 5. Recommended Priority Order

1. **C1** — make agent-start reachable and well-formed (required feature, currently 0% functional).
2. **H1** — stop writing the session token to `audit.jsonl` (explicit security "never").
3. **H2, H3** — add `tab.move` UI and wire `agent.send_keys` (required §15 actions).
4. **M1, M2** — tighten CSP and enable secret log redaction (both are already-written code left unwired).
5. **M3** — fix `root_pane` field read + test fixture (silent create-navigation no-op).
6. **M4–M7** — pane-move-to-tab, paste key, GoReleaser sync test, de-flake takeover journey.
7. **L1–L12** — cleanup, dead-code consolidation, and coverage.

*No files other than this report were created or modified during the audit.*
