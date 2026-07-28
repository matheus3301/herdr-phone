# Delivery Contract — herdr-phone v0.3.0

Status: implementation contract for the v0.3.0 feature release.
Date: 2026-07-28. Owner: tech-lead (orchestrator). Base tree: `main` @ v0.2.0.

This file is the authoritative brief for the v0.3.0 work packages. It does not
replace SPEC.md; work package D amends SPEC.md to match what is built.

## 1. Mission

Make Herdr Phone effortless to start and reach from the operator's phone, without
lowering the SSH-grade security bar:

1. **No pair link in named mode.** When Cloudflare Access is the edge identity
   (named mode), reaching the public URL and clearing Access is sufficient — the
   relay transparently provisions an app session from the verified Access
   identity. Pairing remains **mandatory** in quick mode (no edge identity there).
2. **One-step start.** Starting the relay prints the ready-to-open access URL
   directly. In named mode that URL is the bare public URL (no `#pair=` fragment).
   In quick mode it is still the pairing link. A keybindable Herdr action toggles
   the daemon start/stop so the operator never pastes terminal commands.
3. **Agent-installable.** A self-contained `docs/install.md` guide a user can
   hand to any coding agent so the agent can install, configure, and start
   Herdr Phone for them.

## 2. Locked decisions

- **Named mode = Access-only.** Cloudflare Access JWT (already re-validated at the
  origin on every authenticated request) becomes the sole interactive gate. The
  app session is auto-provisioned from the verified Access identity. No `/pair`
  round-trip is required to reach `authSession` routes in named mode.
- **Quick mode keeps pairing.** Quick tunnels have no edge identity; the
  single-use pairing secret stays the only app gate. `/pair` is unchanged for
  quick mode and stays live for named mode too (useful to re-bind / recover).
- **Sessions stay in-memory.** Auto-provisioned sessions are ordinary in-memory
  sessions, expiry capped at the earlier of `session_ttl` and the Access JWT
  expiry — identical lifetime rules to a paired session. A daemon restart simply
  causes the next named-mode request to auto-provision again (Access re-auths at
  the edge), so restarts are invisible to the operator.
- **One-step start UX.** `herdr-phone start` prints the access URL. A new
  keybindable action toggles start/stop. No terminal paste required in normal use.
- **No new shell, no weakened middleware, no secret logging.** All existing
  security invariants in AGENTS.md / SPEC.md §9 hold.

## 3. Security model delta (the only part that changes)

Today (`v0.2.0`): named mode = Access JWT **and** a paired app session. The
pairing link is a second factor delivered out-of-band.

v0.3.0: named mode = Access JWT **is** the gate. Rationale the reviewers must
uphold: Cloudflare Access is deny-by-default SSO-grade identity, re-validated
cryptographically at the origin on **every** request and WebSocket handshake
(SPEC §9.2). The pairing secret added a second factor but is operationally the
friction this release removes. The origin-side JWT re-validation (not just the
edge) is what makes dropping pairing safe here: a request that reaches loopback
directly is still rejected without a valid `Cf-Access-Jwt-Assertion`.

**What must NOT change:**
- Quick mode pairing stays mandatory and single-use.
- Access JWT validation is untouched (RS256, kid/issuer/aud/exp/nbf/iat,
  allowed_identities, fail-closed JWKS).
- CSRF, Origin allowlist, `http.CrossOriginProtection`, Host allowlist, rate
  limits, body bounds, CSP — all unchanged and still enforced on auto-provisioned
  sessions exactly as on paired ones.
- The session cookie keeps its `__Host-` / HttpOnly / Secure / SameSite=Strict
  attributes. The CSRF token is still per-session, in-memory only.
- Audit records the non-secret `AuditID`, never the cookie value. Auto-provision
  emits an audit event (e.g. `session.auto`) with Subject + AuditID.

## 4. Work packages (ownership matrix)

| Pkg | Exclusive paths | Deliverable | Required verification |
|-----|-----------------|-------------|-----------------------|
| **A. Backend auth** | `internal/server/**`, `internal/auth/**`, `internal/integration/**` | Access-only auto-session in named mode | `go test -race ./internal/server/ ./internal/auth/ ./internal/integration/` |
| **B. CLI + manifest** | `internal/app/**`, `herdr-plugin.toml`, `internal/buildinfo/**` | one-step start URL, toggle action, version bump | `go test ./internal/app/` |
| **C. Frontend** | `web/**` | named mode skips pairing screen | `cd web && npx vitest run` |
| **D. Docs + SPEC** | `docs/**`, `SPEC.md`, `README.md`, `SECURITY.md` | install.md, README pointer, SPEC/SECURITY amendments | `make verify-plugin`, lint docs |

Shared-file rule: `go.mod`/`go.sum` are **not** expected to change (no new deps).
If any package believes a new dependency is required, STOP and report — do not add
it. `web/mock/relay.ts` is owned by **C** but must mirror any wire-shape change
from **A**; A must flag any such change in its final report.

## 5. Package A — Backend auth (Access-only named mode)

**Goal:** a named-mode request with a valid Access JWT but no app session cookie
is transparently given a session; quick mode is byte-for-byte unchanged.

**Design (follow exactly):**

1. Add to `server.Authenticator` (internal/server/interfaces.go):
   `EnsureSession(r *http.Request) (*Session, error)`. Document: in named mode it
   mints (or reuses) a session bound to the verified Access identity and returns
   the cookie to set; in quick mode it returns `(nil, nil)` so pairing stays the
   only path.

2. Implement in `internal/integration/auth.go` (`authAdapter.EnsureSession`):
   - If `!a.named` → return `(nil, nil)`.
   - Re-read Access claims via the existing `verifyToken(r)` (the middleware
     already verified them; this binds the session to the exact identity). On
     error return it (fail closed).
   - Build `auth.Identity{Email, CommonName}` and `hardExpiry` from claims exactly
     as `Pair` does today.
   - **Reuse before create:** look up an existing live session for the same
     identity to avoid unbounded session accumulation from auto-provisioning. Add
     a method to `auth.SessionStore` (e.g. `GetByIdentity`) that returns the live
     session for a matching identity subject, else create a new one with
     `sessions.Create(id, hardExpiry)`. A reused session must be returned with a
     fresh cookie carrying its existing expiry.
   - Return `&server.Session{Cookie, CSRFToken, Identity, ExpiresAt}` shaped
     exactly like `Pair`'s return (reuse `toServerIdentity`).

3. Wire into the middleware in `internal/server/routes.go` `wrap`:
   - Current step 3 (`authSession`): if `sessionFromRequest` succeeds, proceed as
     today. If it fails **and** `rt.auth == authSession` **and**
     `s.deps.Auth.NamedMode()`, call `EnsureSession(r)`. On success: set the cookie
     on the response (`http.SetCookie(w, sess.Cookie)`), record an audit event
     `session.auto` (Subject + SessionID), and treat the request as authenticated
     with the new session's identity/CSRF for the rest of the pipeline. On error
     or `(nil, nil)`, fall through to the existing `401 no valid session`.
   - Quick mode: identical to today — no session → 401; `/pair` is the only way in.
   - **Ordering constraint:** Origin/CSRF/rate/deadline checks (steps 4–6) must
     run on auto-provisioned sessions exactly as on paired ones. Do not bypass
     CSRF for mutating routes — an auto-provisioned session that has not yet
     surfaced its CSRF token cannot mutate until the SPA reads `GET /session`
     (same rule as a paired session before it learns its token). This is correct:
     the SPA calls `GET /session` on load and recovers the token.

4. Do NOT change: `Pair`, `/pair` route, quick-mode paths, Access JWT verifier,
   CSRF store, cookie attributes.

**Tests (must add, race-clean):**
- Named mode, valid Access, no cookie → request to an `authSession` GET succeeds,
  sets a `__Host-` cookie, returns 200; a second request with that cookie succeeds.
- Auto-provisioned session reuses (does not grow the store unboundedly) across
  repeated cookie-less named requests for the same identity.
- Named mode, invalid/expired Access → still 401, no session minted.
- Quick mode, no cookie → still 401 on `authSession`; `/pair` still works; auto
  provisioning does NOT occur in quick mode.
- Existing tests (`TestEveryAPIRouteRequiresSessionExceptPair`, pair single-use,
  CSRF) still pass; update the fake `Authenticator` in `fakes_test.go` to satisfy
  the new interface method.
- Audit: `session.auto` recorded with non-secret AuditID only.

**Report:** files changed, the exact reuse-key used for identity lookup, any
wire-shape change (there should be none to responses the SPA already parses), and
`go test -race` output for the three packages.

## 6. Package B — CLI + manifest (one-step start, toggle, version)

**Goal:** starting prints the access URL; a keybindable action toggles the daemon;
version becomes 0.3.0.

1. **One-step start.** In `internal/app/commands.go` `runStart`, after a
   successful (or already-running) start, print the access URL. Named mode:
   `res.PublicURL` is the access URL (no pairing needed now). Quick mode:
   `res.PairingURL` (pairing still required). Print a clear line, e.g.
   `Open on your phone: <url>`. Keep the existing `Public URL:` / `Pairing:`
   lines for compatibility. `internal/app/runtime.go` already surfaces both URLs;
   confirm `Start` returns a usable `PairingURL` in quick mode and `PublicURL` in
   named mode without an extra `setup-link` call. If named mode currently only
   fills `PairingURL`, fix the daemon/runtime so `PublicURL` is populated and
   printed as the open target in named mode.

2. **Toggle action.** Add a `toggle` subcommand: if the daemon is healthy/running,
   stop it; otherwise start it (named mode default). Print the resulting state and
   access URL. Wire it in `cli.go` dispatch + usage text. Add a `[[actions]]`
   entry `id = "toggle"`, `title = "Phone: Toggle On/Off"`, `contexts = ["global"]`,
   `command = ["./bin/herdr-phone", "toggle"]` in `herdr-plugin.toml`. This is the
   action a user binds via `[[keys.command]]` `type = "plugin_action"`. Document
   the keybinding snippet in README (Package D writes the doc; you provide the
   exact action id `matheus3301.phone.toggle`).

3. **Version bump to 0.3.0.** Update `internal/buildinfo` `Version = "0.3.0"` and
   `herdr-plugin.toml` `version = "0.3.0"`. Run `go test ./internal/app/` — the
   release-consistency and manifest tests must stay green (they assert version
   agreement across buildinfo/manifest).

**Tests:** `runStart` prints an open-URL line (named → public URL; quick → pairing
URL). `toggle` dispatches start when stopped and stop when running (use the
existing `fakeRuntime`). Update any test asserting exact `start` output.

**Report:** files changed, the toggle action id, sample `start` and `toggle`
output in both modes, `go test ./internal/app/` output.

## 7. Package C — Frontend (skip pairing in named mode)

**Goal:** the SPA must not demand a pairing secret when the relay is in named
mode; it should go straight to recovering/establishing a session.

**Read first:** `web/src/lib/api.ts`, `web/src/lib/store.ts` (`start`), the
pairing route/component (find where `#pair=` and the pairing UI are handled), and
`web/src/lib/normalize.ts` (`sessionFromResponse`).

**Design:**
1. On app start the SPA already calls `api.getSession()`. In named mode the
   backend now auto-provisions a session, so `GET /session` returns 200 with a
   CSRF token + identity **without any prior pairing**. The existing cold-reload
   recovery path in `store.start` therefore already works for named mode.
2. Change the pairing-gate logic: only show the pairing screen / require a
   `#pair=` secret when the mode is **quick**. The mode is available from
   `GET /session`'s `identity.mode` and from capabilities. In named mode, if
   `getSession()` succeeds, skip the pairing UI entirely. If `getSession()` 401s
   in named mode (e.g. Access expired), show the reconnect/re-auth state, NOT the
   pairing form — pairing is not the remedy in named mode.
3. Keep the pairing flow fully intact for quick mode (`identity.mode === "quick"`).
4. `web/mock/relay.ts`: the mock must support both modes. Add a way (env or query)
   to emulate named mode where `GET /session` succeeds without pairing, and keep
   the quick-mode pairing path. Mirror the exact `identity.mode` wire values the
   backend emits (`"named"` / `"quick"`). If Package A changed any response shape
   the SPA parses (it should not), mirror it here.

**Tests:** vitest — named mode: store.start reaches an authenticated, mutable
session without any pair call; quick mode: still requires pairing. Update Playwright
journeys only if a route/screen changed (flag if so; run `npx vitest run`, not the
full Playwright suite — orchestrator runs E2E).

**Report:** files changed, how named vs quick mode is detected, mock changes, and
`npx vitest run` output.

## 8. Package D — Docs + SPEC

**Goal:** agents can install Herdr Phone from a pasted guide; docs reflect the new
auth/start UX. Wait for A/B/C final reports before finalizing behavioral claims.

1. **`docs/install.md`** — a self-contained, copy-pasteable guide for a coding
   agent. Structure: (a) one-line purpose; (b) prerequisites checklist the agent
   must verify (`herdr plugin` works, `cloudflared` present, macOS); (c) exact
   install command `herdr plugin install matheus3301/herdr-phone`; (d) minimal
   named-mode config TOML with placeholders for public_url, team_domain, audience,
   allowed_identities, and one credential strategy; (e) start command
   (`herdr plugin action invoke matheus3301.phone.start`) and how to read the
   printed access URL; (f) the optional keybinding snippet binding
   `matheus3301.phone.toggle`; (g) a troubleshooting table. Tone: imperative,
   agent-executable steps, no marketing. Include the raw-fetch one-liner for
   humans: `curl -fsSL https://raw.githubusercontent.com/matheus3301/herdr-phone/main/docs/install.md`.
2. **README.md** — add an "Install with an agent" subsection pointing to
   `docs/install.md`; update the auth/pairing sections (named mode no longer needs
   pairing; quick still does), the `start`/running section (one-step URL), and add
   the keybinding snippet under Running. Keep the security warning block accurate.
3. **SPEC.md** — amend §6 (start prints access URL; toggle action), §9.1 (named
   mode = Access-only auto-session; pairing mandatory only in quick mode), §9.3
   (middleware step 3 auto-provisions in named mode), and the version/date header.
   Keep the security invariants list accurate.
4. **SECURITY.md** — update the threat model: named mode's interactive gate is
   Cloudflare Access alone (still origin-re-validated every request); quick mode
   unchanged. Note auto-provisioned sessions are in-memory and Access-expiry-capped.

**Report:** files changed and a statement that every behavioral claim matches the
A/B/C final reports.

## 9. Definition of done (orchestrator verifies, not the workers)

- `make check` green (fmt, tidy, vet, typecheck, lint, race, coverage ≥80%,
  frontend tests, embedded build, shell syntax, smoke-install).
- New Go tests for auto-provisioning pass with `-race`.
- Named mode reaches the inbox with no pairing screen; quick mode still pairs
  (verified via vitest + a Playwright smoke if routes changed).
- `herdr plugin action invoke matheus3301.phone.start` prints a ready-to-open URL.
- `matheus3301.phone.toggle` action exists and round-trips start/stop.
- `make verify-plugin` passes with the new manifest action.
- `docs/install.md` is accurate and agent-executable.
- Independent security review finds no critical/high/medium open findings.
- Version is 0.3.0 consistently (buildinfo, manifest, and eventually the tag).

## 10. Explicit non-goals for v0.3.0

- No persistent/on-disk sessions (sessions stay in-memory).
- No change to quick-mode pairing (stays mandatory, single-use).
- No LaunchAgent / start-at-login.
- No multi-device or multi-user session management UI.
- No new dependencies without orchestrator sign-off.
- No actual git commit/tag by workers — the orchestrator handles release after
  explicit user confirmation.
