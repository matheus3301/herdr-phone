# herdr-phone Security Review

Status: independent security review, no code changes
Date: 2026-07-23
Reviewer scope: SPEC.md, docs/research/security.md, SECURITY.md, configuration, and
every Go/web security boundary for the two production front doors — **named**
(Cloudflare Access + named tunnel) and **Quick Tunnel** (TryCloudflare, no edge
identity).

## Method

I threat-modeled the concrete request paths against the design's own controls:
Access JWT/JWKS, pairing/cookies/CSRF, Host/Origin, proxy-header trust,
WebSockets/CSWSH, confirmation nonces, terminal input and ANSI filtering,
cloudflared credentials/process/logs, the daemon control socket, directory
confinement, audit logs, CSP/PWA, and the dependency/release supply chain. I read
the enforced middleware and the full route table to confirm coverage, and tried to
break each control rather than confirm it. Four boundary areas (terminal/ANSI,
tunnel, daemon+directories, config/secrets/CI) were cross-checked in depth.

## Overall assessment

The core authentication and request-security stack is **strong and matches the
research threat model**. Access JWT validation is correct (RS256-only, `kid` +
JWKS, exact issuer/audience, time claims with bounded skew, fail-closed JWKS with a
one-TTL stale bound and singleflight, identity allowlist). Pairing is single-use,
constant-time, 256-bit, and fragment-delivered. Sessions are opaque, in-memory,
`__Host-` + HttpOnly + Secure + SameSite=Strict. CSRF uses a per-session token in a
custom header plus Go `CrossOriginProtection`. The **enforced** middleware
(`server.wrap`) applies Host → Access JWT → session → Origin allowlist → cross-origin
→ CSRF → content-type/body/rate/deadline **to every route in a single declarative
table**, and the only unauthenticated route is a minimal `/health`. The Herdr
mutation surface is a closed, typed allowlist with no generic passthrough.
Confirmation nonces are single-use and bound to op+resource+generation+session+params.
The terminal bridge and ANSI filter are well built (streaming fragmentation-safe
parser, per-session state, strict single-owner goroutines, bounded buffers). The
daemon never kills an arbitrary PID; directory confinement resolves-then-checks and
withstands symlink/`..`/prefix attacks. cloudflared never receives a token in argv
or env, and temp token files are 0600 and reliably deleted.

**No remote, unauthenticated code-execution path was found.** The findings below are
credential-at-rest exposure, absent/broken defense-in-depth controls, a terminal
reply-injection gap, and supply-chain hardening. Fix H1, M1, M2, M3, M4 before a
public v0.1.0.

---

## Findings (ranked)

### H1 — The session bearer (cookie value) is written verbatim to the audit log

**Severity: High** (Medium in named mode; High in Quick mode)

**Where:**
- `internal/integration/auth.go:90` — the session cookie is `auth.NewSessionCookie(sess.ID, …)`, i.e. the cookie value **is** `sess.ID`.
- `internal/integration/auth.go:128-142` — `toServerIdentity` sets `server.Identity.SessionID = sess.ID` (the same value).
- `internal/server/pairing.go:57-61` (`pair.success`), `internal/server/mutations.go:155-161, 279-287, 296-304`, `internal/server/terminalroute.go:96-102, 114-121, 128-132` — every audit record sets `SessionID: ident.SessionID`.
- `internal/server/audit.go:69` — `sanitizeField(e.SessionID)` only strips control characters; it does **not** redact. The value lands in `audit.jsonl` in cleartext.

**Why it matters.** SPEC §7 states `audit.jsonl` must record "structural and input
metadata; **never** terminal content, commands, JWTs, cookies, or pairing values."
The `SessionID` field is the opaque session **cookie value** — the sole
application-layer bearer credential. It is persisted, in cleartext, to a file that
outlives process memory.

**Exploit path.** Any process that can read `audit.jsonl` (same-uid co-resident
process, a backup, a shipped/attached debug log, or an operator pasting logs) obtains
a live session token valid until the session TTL/idle expiry (up to 12h). It can be
replayed as the `__Host-herdr_phone` cookie. Because `serverConfig`
(`internal/integration/configmap.go:61-77`) **always** allows the loopback host and
`http://127.0.0.1:PORT` origin — even in production — the token can be replayed
straight against the origin over loopback. In **Quick mode** there is no Access JWT,
so the cookie alone is full shell-equivalent access. In named mode a valid Access JWT
is still required, which bounds the impact.

**Minimal fix.** Separate the session **identifier used for audit/display** from the
session **cookie value/secret**. Give `auth.Session` a distinct non-secret `AuditID`
(e.g. a second random value or a truncated hash of the ID) and populate
`server.Identity.SessionID` from that, keeping the cookie value out of every audit
record. Alternatively, log only a short prefix/HMAC of the session, never the raw
value.

---

### M1 — Mandated secret redaction is dead code, and the redactor is broken

**Severity: Medium** (defense-in-depth control absent; regression-prone)

**Where:**
- `internal/security/redact.go` — `RedactSecrets`, `SanitizeLogLine`, `SanitizeForLog` are referenced only by `redact.go` itself and its test. No log/audit sink calls them.
- The real sinks strip control characters only, with **no secret redaction**:
  `internal/server/audit.go:80` (`sanitizeField`) and `internal/tunnel/logparse.go:109` (`sanitizeLogText`), whose raw lines are surfaced via `RecentLogs()`/status.
- `internal/security/redact.go:22` — `headerPattern` ends in `\s*[:=]\s*\S+`. `\S+` stops at the first space, so `Authorization: Bearer <token>` redacts only `Bearer` and **leaks `<token>`**. (The test at `redact_test.go` only asserts the marker is present, not that the secret is gone.)
- `internal/security/redact.go:16` — `jwtPattern` requires three dot-separated segments; a cloudflared tunnel token is a single dotless `eyJ…` base64 blob and would **not** match, despite the comment claiming it does.

**Why it matters.** SPEC §17 and research §3.9 require tokens/JWTs/secrets redacted
from *all* logs. Today the exposure is bounded because tokens are file-delivered and
cloudflared runs at `--loglevel info`, but the mandated control does not run, and its
implementation would miss opaque bearers and tunnel tokens if it did.

**Minimal fix.** Call `security.SanitizeForLog` inside `sanitizeLogText` and before
persisting audit string fields. Fix `headerPattern` to capture the full value
(`\S+(?:[ \t]+\S+)*` or `.*$`), add a dotless `eyJ[A-Za-z0-9_\-]{20,}` pattern, and
strengthen the test to assert the secret substring is absent from the output.

---

### M2 — Content-Security-Policy `connect-src` allows WebSockets to any host

**Severity: Medium** (weakens XSS/exfiltration containment)

**Where:** `internal/server/server.go:217` —
`connect-src 'self' ws: wss:`.

**Why it matters.** SPEC §9.3 and research §3.4 require the CSP to *explicitly* allow
only the same-origin WebSocket. `ws:`/`wss:` are scheme-wide allowances: any script
running in the SPA context may open a WebSocket to an arbitrary attacker host. For an
app whose WebSocket carries a live terminal, this is the exact exfiltration/C2 channel
the policy is meant to close. Note `script-src 'self'` (no `unsafe-inline`/`eval`) is
strict and makes XSS hard, so this is defense-in-depth — but it is a direct weakening
of a documented control.

**Minimal fix.** Scope it to the known origins the server already computes:
`connect-src 'self' wss://<public-host>` in named mode (add `ws://127.0.0.1:PORT
ws://localhost:PORT` for loopback dev), using the same values already derived for the
Origin allowlist. `style-src 'unsafe-inline'` (server.go:214) is a lesser, largely
unavoidable concession for xterm.js/Tailwind inline styles — acceptable, but worth a
note.

---

### M3 — Terminal reply-inducing CSI queries (DECRQM, XTVERSION) are not filtered

**Severity: Medium**

**Where:** `internal/security/ansi.go:266-274` (`csiShouldStrip`) strips only CSI
finals `n` (DSR), `c` (DA), `t` (XTWINOPS). It receives only the final byte, so it
cannot even distinguish these from rendering sequences.

- **DECRQM** `CSI ? Ps $ p` / `CSI Ps $ p` (final `p`) — not stripped.
- **XTVERSION** `CSI > Ps q` (final `q`) — not stripped.

Browser terminal input is **not** filtered on the way to the PTY
(`internal/terminal/bridge.go` `handleInput`), which is correct for an interactive
shell but means auto-generated replies re-enter as input.

**Exploit path.** Hostile relayed output (a coding agent, `git log`, build output)
emits DECRQM/XTVERSION → the filter forwards it → xterm.js auto-answers via `onData`
→ the SPA forwards that answer back over the socket as terminal **input** → it lands
on the shell's input line. The replies carry no CR/LF so they cannot self-execute,
but this is exactly the device-status-query reply-injection class research §3.6
requires stripping, and it can corrupt/pre-seed the operator's command line. (OSC 52,
OSC 8, title set/report, DCS/APC/PM including DECRQSS, and ENQ answerback **are**
correctly stripped.)

**Minimal fix.** Pass the whole buffered CSI sequence to the strip test and qualify by
intermediate/prefix so rendering sequences are preserved: strip final `p` only with a
`$` intermediate (DECRQM); strip final `q` only with a `>` private prefix (XTVERSION);
do not strip bare `p` (DECSTR) or `SP q` (DECSCUSR). Consider also `x` (DECREQTPARM)
and `* y` (DECRQCRA) for the operator's real terminal.

---

### M4 — GitHub Actions are pinned to mutable tags, not commit SHAs

**Severity: Medium** (supply chain; release job holds `contents: write` + token)

**Where:** `.github/workflows/ci.yml` and `release.yml` — every `uses:` is a mutable
major tag: `actions/checkout@v7`, `actions/setup-go@v7`, `actions/setup-node@v5`,
`actions/upload-artifact@v7`, `goreleaser/goreleaser-action@v7`,
`anchore/sbom-action/download-syft@v0` (a `@v0` floating tag is especially loose).

**Why it matters.** SPEC §19 and research §3.12 require immutable, least-privilege
CI. If any tag is repointed (maintainer/action compromise or a tag move), the
**release** job — which elevates to `contents: write` and holds `GITHUB_TOKEN` —
executes attacker-controlled code with publish rights over the shipped binary.

**Minimal fix.** Pin every action to a full 40-char commit SHA (with the version in a
trailing comment). Dependabot already tracks `github-actions` and will bump the SHAs.
Related: `go install …/govulncheck@latest` (`ci.yml:227`, `release.yml:76`) should be
pinned to a released version, and the `scripts/build.sh` release-download path
verifies the archive only against a same-origin `checksums.txt` — sign it
(cosign/minisign) or pin the expected checksum in-repo as `verify-plugin.sh` already
does for the Herdr download.

**Correct today:** default `permissions: contents: read`, `persist-credentials:
false`, `pull_request` (not `pull_request_target`, so forks get no secrets),
`contents: write` scoped to the publish job only, tag/version/manifest agreement
gates, govulncheck source+binary, goreleaser `-trimpath` + `CGO_ENABLED=0`,
Dependabot for gomod/npm/actions, and lockfile installs.

---

### M5 — A supervisor crash can strand a public tunnel (no parent-death coupling)

**Severity: Medium** (macOS residual risk vs. the stated "a crash cannot strand a public tunnel" goal)

**Where:** `internal/tunnel/process.go:54` — the child is placed in its **own**
process group (`Setpgid: true`) with no parent-death signal. Graceful stop and
context-cancel teardown are correct, but if the daemon dies abnormally (SIGKILL, OOM,
unrecovered panic), none of the `stop()`/`cancel()` paths run and cloudflared keeps
the **public tunnel up** as an orphan.

**Why it matters.** SPEC §7 / research §3.8 require that a crash not silently strand a
public tunnel. On Linux the fix is `SysProcAttr.Pdeathsig = SIGKILL`; macOS (the only
v0.1.0 platform) has no kernel equivalent.

**Minimal fix.** Document the residual risk explicitly, and make the daemon's
reconcile-on-restart detect and terminate an orphaned cloudflared before starting a new
one (it already reconciles daemon state; extend it to the tunnel child). Optionally add
a lightweight watchdog. On any future Linux support, set `Pdeathsig`.

---

## Low / informational

- **L1 — Generation guard skipped when `expected_generation == 0`.**
  `internal/server/mutations.go:235` guards pane-scoped mutations only when
  `req.ExpectedGeneration > 0`, and `internal/server/terminalroute.go:70` likewise. A
  client may omit the generation and act on a pane whose lifecycle changed. Herdr IDs
  are opaque and rotate on move, so mis-targeting is unlikely, but SPEC §11 says input
  "carries the expected generation; the server checks it immediately before dispatch."
  Consider requiring a non-zero generation for pane operations.

- **L2 — `pane_id` flows into the controller argv ahead of flags without a `--`
  guard.** `internal/terminal/controller.go:61-65` builds `["terminal","session",
  "control", spec.PaneID, "--cols", …]`. A `pane_id` beginning with `-` is parsed by
  the Herdr CLI as a flag (argument injection). It is auth-gated (the operator already
  has a shell, so this is not privilege escalation) and there is no shell, but it is
  poor hygiene. Insert `--` before the positional id (put flags first), and reject a
  `pane_id` that is not a currently existing pane when no generation is asserted.

- **L3 — Rate limiter uses one global key for unauthenticated routes.**
  `internal/server/routes.go:250` derives the key from `RemoteAddr`, which behind
  cloudflared is always `127.0.0.1`. `/health` and `/pair` therefore share one token
  bucket, so a flood on public `/health` can 429 the operator's pairing. The 256-bit
  pairing secret makes brute force irrelevant; this is an availability nit. Correctly
  does **not** trust `X-Forwarded-For`.

- **L4 — Terminal filter default is fail-open.** `internal/server/server.go:123-124`
  and `internal/terminal/bridge.go:85-87` default a nil `TerminalFilterFactory` to
  `NopFilter{}` (all bytes pass). Production wires the real filter
  (`internal/integration/stack.go:248`), so this is not currently exploitable, but a
  security control should fail closed — require the factory or default to
  `security.NewANSIFilter`.

- **L5 — `/panes/{id}/read` returns raw pane content unfiltered.**
  `internal/server/snapshot.go:112-122` returns pane text without ANSI sanitization
  (the live terminal bridge filters; this REST read does not). Inert as JSON/plain
  text, but if the SPA ever pipes pane-read output into `xterm.js.write()`, the
  stripped classes (OSC 52, etc.) would fire. Keep pane-read output out of the
  emulator, or filter it too.

- **L6 — Control socket relies solely on filesystem permissions (no peer-cred check).**
  `internal/daemon/control.go` — 0600 socket under a 0700 dir means same-uid only, which
  matches the trust model, but any same-uid process can call `rotate-pairing` (reading
  the single-use pairing URL) or `stop`. A `SO_PEERCRED`/`LOCAL_PEERCRED` uid assertion
  would be belt-and-suspenders. The socket-relocation path is well hardened (rejects a
  symlinked dir, verifies owner uid, refuses group/other access). There is a small
  umask window between `net.Listen` and `Chmod(0600)`, not exploitable because the
  parent dir is 0700 and connecting needs the write bit a normal umask denies.

- **L7 — Over-long cloudflared log line permanently stops log parsing.**
  `internal/tunnel/process.go:107-133` uses `bufio.Scanner` with a 256 KiB buffer; a
  longer token returns `ErrTooLong` and ends the stream (the "split" comment is
  incorrect). If it arrives before the readiness marker, readiness is never detected
  and — for the `token_command` strategy — the temp 0600 token file is never deleted.
  Use `bufio.Reader.ReadString('\n')` or resync past `ErrTooLong`.

- **L8 — `MetricsAddr` and caller-supplied secret-file perms are enforced at the
  integration layer, not inside `tunnel`.** `internal/tunnel/mode.go` only defaults
  `MetricsAddr` when empty and does not validate it is loopback, and `validateStrategy`
  does not stat token/credential files. Both are currently safe because
  `configmap.go` never sets `MetricsAddr` (so it defaults to `127.0.0.1:0`) and
  `validateForServe` (`internal/integration/configmap.go:83-97`) calls
  `config.VerifySecretFile` (Lstat + regular + owner-uid + `perm&0o077==0`). Enforcing
  both inside the `tunnel` package would make it safe by construction.

- **L9 — C1 8-bit control introducers (0x9B/0x9D/0x90/0x9C) are not interpreted by the
  ANSI filter.** `internal/security/ansi.go` — deliberately safe here because frames
  reach xterm.js as UTF-8-decoded binary (a lone C1 byte becomes U+FFFD), controller
  stderr is discarded, and audit carries only counts. Sound tradeoff; it depends on
  every downstream consumer staying UTF-8, never raw/8-bit.

- **L10 — First terminal frame with `seq == 0` is rejected.**
  `internal/terminal/bridge.go:298` initializes `lastSeq` to 0 and requires strict
  monotonic increase; ensure the controller's sequence numbering is 1-based or the
  first frame closes the session.

- **L11 — `internal/security/middleware.go` (`Middleware`/`Wrap`) is unused.** The
  production server enforces its own `server.wrap` (`internal/server/routes.go:97`);
  the `security.Middleware` type is exercised only by its own test. The enforced path
  is correct and complete, but the two implementations can drift and the package's
  tests give false assurance about the code that actually runs. Delete the dead
  implementation or wire it as the single middleware.

---

## Boundary coverage summary

| Boundary | Verdict |
|---|---|
| Access JWT / JWKS | Correct: RS256-only, `kid`+JWKS, exact iss/aud, time+skew, identity allowlist, fail-closed with one-TTL stale bound + singleflight |
| Pairing / cookies / CSRF | Correct: single-use 256-bit constant-time pairing; `__Host-`+HttpOnly+Secure+SameSite=Strict; per-session CSRF token in custom header |
| Host / Origin | Correct: exact allowlists, fail-closed on empty/`null` Origin; loopback dev host always allowed (safe in named mode via JWT; relevant to H1 in Quick mode) |
| Proxy headers | Correct: only `Cf-Access-Jwt-Assertion` trusted (cryptographically); `X-Forwarded-*` / convenience email header never trusted |
| WebSockets / CSWSH | Correct: Origin allowlist + `CrossOriginProtection` + SameSite=Strict session cookie all block cross-site upgrades; `InsecureSkipVerify` is safe because middleware checks Origin first |
| Confirmation nonces | Correct: single-use, expiring, bound to op+resource+generation+session+params-hash, constant-time |
| Terminal input / ANSI | Mostly correct (streaming, per-session, bounded, single-owner); gap M3 (DECRQM/XTVERSION), L4/L5/L10 |
| cloudflared creds / process / logs | Correct on the critical path (no token in argv/env, 0600 temp file deleted on readiness, `--no-autoupdate`/info-level); M5/L7/L8 hardening |
| Daemon control socket | Correct: 0600/0700, three commands only, never kills arbitrary PID, hardened relocation; L6 note |
| Directory confinement | Correct: resolve-then-check, symlink/`..`/prefix-safe, directories only, fail-closed |
| Audit logs | H1 (session bearer written) + M1 (no secret redaction) |
| CSP / PWA | M2 (connect-src too broad); otherwise strict (`script-src 'self'`, no eval, `frame-ancestors 'none'`); SW never caches `/api/`; fragment stripped before network; secrets never in storage |
| Dependency / release supply chain | Good posture; M4 (unpinned actions) + govulncheck `@latest` + unsigned release checksums |

## Recommended fix order for v0.1.0

1. **H1** — stop writing the session bearer to `audit.jsonl` (distinct audit id).
2. **M1** — wire and fix `SanitizeForLog` into the audit and cloudflared log sinks.
3. **M2** — scope `connect-src` to the known origins.
4. **M3** — strip DECRQM/XTVERSION (pass the whole sequence to the strip test).
5. **M4** — pin CI actions to commit SHAs; pin govulncheck; sign release checksums.
6. **M5** — reconcile/kill orphaned cloudflared on daemon restart; document the macOS residual.
7. Low items L1–L11 as hardening.
