# herdr-phone Security Review — Round 2 (remediation verification)

Status: independent re-review, no code changes
Date: 2026-07-24
Scope: verify the Round-1 findings (H1, M1–M5, and the round-1 Lows) against the
current tree on both the **named** (Cloudflare Access) and **Quick Tunnel**
production paths, and hunt for new vulnerabilities introduced by the fixes.
Prior report: `docs/reviews/security.md`.

## Verdict

**All six primary findings (H1, M1–M5) are resolved on the enforced code paths**,
several beyond what Round 1 asked for. The route table and single-middleware
(`server.wrap`) coverage are intact — no new routes and no bypass. The two H1-class
generation gaps and the fail-open terminal filter are also fixed. Remediation
introduced no high- or medium-severity regressions; three low/informational notes
below are new, and four pre-existing lows remain open by design or as accepted
residuals. No remote, unauthenticated code-execution path exists. Nothing blocks a
public v0.1.0.

---

## Findings first

### New (introduced by remediation)

**N1 — Low — Orphan reconciliation can terminate an unrelated same-uid `cloudflared`
after PID reuse.** `internal/tunnel/orphan.go:105-128,170-188`. `reconcileOrphan`
kills the recorded PID's process group once the PID is alive **and**
`verifyProcessIsBinary` matches the process `ps comm` **basename** to the configured
binary basename. If, after an abnormal daemon death, the recorded PID is recycled by
a *different* `cloudflared` the operator runs for another tunnel, the identity check
passes (same basename) and herdr-phone SIGTERM/SIGKILLs that unrelated group. This is
an availability/robustness edge case (same-uid only, requires exact PID reuse by a
same-named binary), not a security breach. `pidRecord.CreatedUnixMs` is already
recorded but not used — comparing it against the live process's start time
(`ps -o lstart=`) would make the identity check conclusive. Fail-safe when identity
is *unconfirmable* is correct; the gap is a false *positive* match.

**N2 — Informational — Orphan identity check shells to `ps` resolved via `PATH`.**
`internal/tunnel/orphan.go:179` runs `exec.CommandContext(ctx, "ps", …)` with a bare
program name. No shell is used and Go 1.19+ refuses relative-cwd resolution, so this
is low risk, but resolving `ps` absolutely (e.g. `/bin/ps`) would remove the residual
`PATH`-integrity dependency for a security-relevant kill decision.

**N3 — Informational — Header redaction can over-redact after newline folding.**
`internal/security/redact.go:29,80-81`. `SanitizeForLog` runs `SanitizeLogLine`
(folding `\r\n` to spaces) *before* `RedactSecrets`, whose `headerPattern` now matches
`[^\r\n]*`. On a value with no remaining newlines the pattern greedily consumes the
rest of the string after a header name, so trailing non-secret text on the same
logical line is also replaced with `[REDACTED]`. This is fail-closed (secrets never
survive) and cosmetic only; noted so it is a deliberate, not surprising, behavior.

### Residual (pre-existing, still open)

- **R1 — Low — `security.Middleware`/`Wrap` (`internal/security/middleware.go`) is
  still dead code.** Production enforces `server.wrap` (`internal/server/routes.go`);
  the `security` package's middleware is exercised only by its own test, so its tests
  give false assurance about code that never runs, and the two can drift. Delete it or
  wire it as the single middleware. (Round-1 L11.)
- **R2 — Low — `scripts/build.sh` verifies the release archive only against a
  same-origin `checksums.txt`** (`scripts/build.sh:113-127`) with no signature. If the
  release source or `HERDR_PHONE_RELEASE_BASE_URL` is attacker-controlled, both files
  are controlled and the check passes. Release now emits SLSA build-provenance
  (`release.yml` `attest-build-provenance`), but `build.sh` does not verify it. Sign
  `checksums.txt` (cosign/minisign) or verify the attestation. (Round-1 M4 sub-note.)
- **R3 — Low — `GET /panes/{id}/read` returns raw pane content unfiltered**
  (`internal/server/snapshot.go:112-121`); safe only while the SPA never feeds it to
  `xterm.js.write()` or `innerHTML`. (Round-1 L5.)
- **R4 — Low — Control socket authorization rests solely on filesystem perms**
  (0600 socket / 0700 dir → same-uid); any same-uid process can `rotate-pairing` or
  `stop`. Matches the documented trust model; a `LOCAL_PEERCRED` uid assertion would be
  belt-and-suspenders. (Round-1 L6.)
- **R5 — Low — Rate-limit key is `RemoteAddr`, always `127.0.0.1` behind cloudflared**
  (`internal/server/routes.go`), so `/health` and `/pair` share one bucket. Availability
  nit; correctly refuses to trust `X-Forwarded-For`. (Round-1 L3.)
- **R6 — Low (mitigated) — `pane_id` is still a positional argv element before the
  flags with no `--` separator** (`internal/terminal/controller.go:61-65`). The
  exploit precondition is now closed: terminal attach requires a mandatory, existing
  generation (below), so only a real opaque Herdr pane id reaches the launcher and a
  `--flag`-shaped id fails the existence check. Adding `--` before the id is cheap
  defense-in-depth. (Round-1 L2.)

---

## Resolved / unresolved matrix

| ID | Round-1 finding | Status | Evidence |
|----|-----------------|--------|----------|
| **H1** | Session bearer written to audit log | **Resolved** | `auth.Session.AuditID` (128-bit, distinct) added; `Get` still keys only on the bearer `ID`; `toServerIdentity` maps `SessionID=AuditID`; audit/context/status never carry the cookie value. `session.go:19-70,131-166,186-204`, `integration/auth.go:92-108,147-164`, `interfaces.go:34-45` |
| **M1** | Mandated redaction dead / regex broken | **Resolved** | Wired at both durable sinks: audit `server/audit.go:89` and cloudflared ring buffer `tunnel/supervisor.go:336` call `security.SanitizeForLog`. Regex fixed: whole-value header capture + dotless `eyJ` tunnel-token pattern `redact.go:16-29` |
| **M2** | CSP `connect-src` scheme-wide `ws:/wss:` | **Resolved** | `buildCSP` scopes `connect-src` to `'self'` + exact `wss://`/`ws://` from `AllowedOrigins`; `script-src 'self'`, `object-src 'none'`, `base-uri 'none'`, `frame-ancestors 'none'`. `server.go:154,205-244` |
| **M3** | DECRQM/XTVERSION reply queries not filtered | **Resolved (hardened)** | `csiShouldStrip(seq)` inspects the whole sequence; strips DECRQM (`$p`), XTVERSION (`>q`), DECREQTPARM (`x`), DECRQCRA (`*y`), plus `n/c/t`; preserves DECSTR/DECSCUSR/DECFRA. Stopping the query at output prevents any xterm reply. `ansi.go:250-390` |
| **M4** | CI actions on mutable tags | **Resolved** | Every `uses:` SHA-pinned with version comment; `govulncheck@v1.6.0` pinned; `attest-build-provenance` (SLSA) added. `.github/workflows/ci.yml`, `release.yml`. Residual R2 (build.sh signature) |
| **M5** | Daemon crash strands public tunnel | **Resolved (within macOS limits)** | New `orphan.go`: pidfile on start; `reconcileOrphan` on next start verifies liveness + binary identity before killing the group, fail-safe if unverifiable; `MacOSKillWindowNote` documents the inherent crash→next-start window. `tunnel/orphan.go`, `supervisor.go:192,341-351`. See N1 |
| L1 | Generation guard skipped when `expected_generation==0` | **Resolved** | Now mandatory and rejected if 0/absent for pane ops and terminal attach. `mutations.go:260-278`, `terminalroute.go:53-90` |
| L2 | `pane_id` argv flag injection | **Mitigated** | Mandatory-generation existence check gates the id before launch; `--` guard not added (R6) |
| L4 | Terminal filter fail-open default | **Resolved** | `server.New` no longer installs a NopFilter default; `handleTerminal` refuses to open when no filter factory; production wires ANSI filter. `terminalroute.go:42-47`, `stack.go:262` |
| L3 | Rate-limit global key behind proxy | **Open (accepted)** | R5 |
| L5 | Pane-read content not ANSI-filtered | **Open (low)** | R3 |
| L6 | Control socket has no peer-cred check | **Open (by design)** | R4 |
| L11 | `security.Middleware` unused | **Open (low)** | R1 |

Round-1 low items L7–L10 (tunnel log-scanner `ErrTooLong` readiness stall, C1
introducer non-interpretation, first-frame `seq==0`, `MetricsAddr`/secret-file perms
enforced at the integration layer) were not re-audited in depth this round; none were
security-severity and no change in this remediation affects them.

---

## Verification detail

### H1 — audit bearer absence (named + quick)
`auth.Session` now carries three independent tokens: `ID` (256-bit bearer / cookie
value), `AuditID` (128-bit non-secret), and `CSRFToken` (`session.go:19-70`). The
store map is keyed only by `ID`; `Get`/`Delete`/`ValidateCSRF` accept only the bearer,
and `AuditID` is never a lookup key, so it cannot be replayed as a credential. `Pair`
sets the cookie to `sess.ID` and everything else (audit records, context identity,
status, `GET /session`) to `AuditID` via `toServerIdentity(sess.AuditID, …)`
(`integration/auth.go:92-108`). A tree-wide search found no audit or log call that
writes the cookie value. Applies identically in Quick mode (the earlier
full-access-bearer-at-rest scenario is gone).

### CSRF reload
`GET /session` now returns the per-session CSRF token and expiry
(`pairing.go:84-100`) so a same-origin reload recovers the in-memory token. This is
safe: the route is `authSession` (requires the HttpOnly, SameSite=Strict session
cookie a cross-site page cannot send), responses are `no-store`, and the CSRF token is
not itself a bearer — a mutating request still needs the cookie. No CSRF weakening and
no new leak (the token reaches only the pair/session JSON bodies).

### M1 — redaction sinks
Both durable/surfaced sinks redact: `FileAuditor.sanitizeField` →
`security.SanitizeForLog` (`audit.go:89`), and `Supervisor.sink` →
`security.SanitizeForLog(line.Raw)` before adding to the ring buffer that backs
`RecentLogs()`/status (`supervisor.go:328-337`). `headerPattern` captures the entire
value (fixing the `Authorization: Bearer <tok>` leak) and a new dotless-`eyJ`
`tunnelTokenPattern` covers cloudflared tokens (`redact.go:16-29`). Note
`logparse.sanitizeLogText` still strips control chars only, but it is never the
storage/surface point — the sink redacts before anything is retained.

### M2 — CSP scoping
`connect-src` is computed once from `AllowedOrigins` (`server.go:219-244`): `'self'`
plus the exact `wss://host` (https origin) or `ws://host` (http/dev origin). No
scheme-wide allowance; origins come from config, not request input.

### M3 — reply-query filtering
`csiShouldStrip` receives the full buffered CSI sequence and disambiguates by private
prefix/intermediate (`$`, `*`, SP, `>`) so reply-inducing queries are dropped while
same-final rendering sequences pass. Because the query never reaches xterm.js, no
auto-reply is generated, which closes the round-1 output→reply→unfiltered-input
re-entry path at its source.

### M4 — supply chain
All actions pinned to 40-char SHAs with trailing version comments in both workflows;
`govulncheck` pinned to `v1.6.0` (source and `-mode=binary` scans);
`actions/attest-build-provenance` adds SLSA provenance. `pull_request` (not
`_target`), default `contents: read`, `contents: write` scoped to publish. Residual:
`build.sh` still trusts a same-origin `checksums.txt` (R2).

### M5 — crash residual
On start the supervisor writes a 0600 pidfile (PID, binary, created-ms) and calls
`reconcileOrphan`, which terminates a still-alive, identity-verified prior child's
group before launching a fresh one, then removes the pidfile
(`orphan.go:45-128`, `supervisor.go:192,341-351`). The macOS-inherent exposure window
(a SIGKILLed daemon leaves cloudflared up until the next start) is documented in
`MacOSKillWindowNote` and surfaced in operator-facing text. See N1 for the PID-reuse
false-positive.

### Routing / middleware / WS origin (regression check)
The route table is unchanged (12 routes + SPA catch-all, each wrapped by
`server.wrap`; `routes.go:34-58`). `wrap` order is intact: Host → Access JWT (named) →
session → exact Origin allowlist (WS + mutating) → `CrossOriginProtection` → CSRF
(mutating session) → content-type/body/rate/deadline. WebSocket handlers use
`InsecureSkipVerify: true` only because `wrap` already enforced the exact Origin
allowlist (fail-closed on empty/`null` Origin) before upgrade
(`routes.go:130-141`, `server.go:188-200`). Control socket still exposes only
`status`/`rotate-pairing`/`stop` (`control.go:33-37`).

---

## Recommended (non-blocking) follow-ups

1. N1 — compare recorded `CreatedUnixMs` to the live process start time in
   `verifyProcessIsBinary` so orphan reconciliation cannot match a reused PID.
2. R2 — sign `checksums.txt` or verify the build-provenance attestation in `build.sh`.
3. R1 — delete the unused `security.Middleware` (or make it the single middleware).
4. R6 — add `--` before `pane_id` in the controller argv.
5. N2 — resolve `ps` to an absolute path in the orphan identity check.
