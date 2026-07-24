# herdr-phone Backend Code Review

Date: 2026-07-23
Scope: Go backend (`cmd/`, `internal/`), reviewed against `SPEC.md`.
Method: full read of every Go source and test file, targeted verification of the
concrete production composition in `internal/integration` (the real `app.Runtime`
wired by `cmd/herdr-phone/main.go`), and the whole test suite.

Verification run:

- `go test ./...` → 501 passed, 15 packages.
- `go test -race ./...` → 501 passed, 15 packages.
- `go vet ./...` → clean.
- No `TODO`/`FIXME`/`panic("not implemented")` in non-test Go. Versions agree
  (`buildinfo.Version = 0.1.0`, `MinHerdrVersion = 0.7.5`, `HerdrProtocol = 17`,
  matching `herdr-plugin.toml`).

The codebase is well-structured and the cryptographic core (Access JWT
verification, pairing, sessions, CSRF, confirmation nonces) is genuinely solid —
the JWT verifier is not bypassable (exact RS256 string check, required `kid`,
PKCS1v15 over the received segments, exact issuer/audience, correct skew
directions, `exp` required, fail-closed on unknown key). The findings below are
real defects, ranked by severity, each with an exact location and a concrete fix.

The single most important issue is that **the pane lifecycle-generation guard —
the mechanism SPEC §11 relies on to stop operator input reaching a recycled
pane/agent — is opt-in and silently skipped by any request that omits
`expected_generation`.** It was independently found by three review passes.

---

## HIGH

### H1 — Generation guard is opt-in; a request that omits `expected_generation` bypasses the fresh-state check
**Files:** `internal/server/mutations.go:235`, `internal/server/terminalroute.go:70`

The pane-scoped guard runs only `if spec.generationChecked() && req.ExpectedGeneration > 0`.
Live pane generations start at `1` and only increment (`internal/state/engine.go:241`),
so no valid pane ever has generation `0` — meaning `0`/omitted is treated as
"don't check." The confirmations path is correctly unconditional
(`mutations.go:137`), which makes the mutation/terminal side an inconsistency, not
a deliberate design.

**Failure scenario:** an authenticated client POSTs
`{"operation":"agent.send_keys","params":{"pane_id":"pane-1","keys":[...]}}` with
no `expected_generation`. Between the operator's last snapshot and the call,
`pane-1`'s occupant changed (Herdr may reuse pane IDs). The guard is skipped and
the keystrokes/prompt are delivered to the *wrong agent*. Same for `agent.prompt`,
`pane.split`, `pane.move`, `pane.resize`, `pane.focus`, and terminal attach
(`/terminals/{pane_id}` without `expected_generation`). Destructive ops are
protected only because nonce issuance checks generation unconditionally.

**Fix:** for any op where `spec.generationChecked()` is true, require
`req.ExpectedGeneration > 0` and always verify it (reject with
`codeGenerationStale`/bad-request when absent). Apply the same to the terminal
attach path (`terminalroute.go:70`).

### H2 — Idempotency cache has no in-flight reservation; concurrent retries double-execute the mutation
**Files:** `internal/server/mutations.go:202-209` (check) and `:305-317`/`cacheMutation`; store `internal/server/stores.go:130-151`

`s.idem.get(key)` and `s.idem.put(key, …)` are two separate locked operations,
and `put` happens only *after* `Mutate` returns. There is no atomic
reserve-on-miss.

**Failure scenario:** the canonical mobile case — a slow request, the client times
out and retries the *same* `session+request_id` while the first is still in
flight. Both calls miss `get`, both reach `s.deps.Mutator.Mutate`. For the
non-confirmation ops (`agent.prompt`, `agent.send_keys`, `pane.split`,
`tab.create`, `workspace.create`, `worktree.create`, …) the operation runs twice —
a duplicate prompt, a second split/tab/worktree. This directly violates SPEC §12
"a network retry cannot repeat a mutation." (Destructive ops are accidentally
protected because the single-use nonce lets only one of the two consume it.)

**Fix:** add an atomic `getOrReserve(key)` to `idemStore` that, under one lock,
returns the cached entry if present, else inserts a "pending" marker and reports
the caller as owner; concurrent duplicates get the marker and return a
retryable 409 (or block on completion). Clear the reservation if the result ends
up uncached.

### H3 — Retryable mutation errors are cached, so a legitimate retry replays the stale error and never re-attempts
**Files:** `internal/server/mutations.go:288-293`, `:315-319`, `classifyMutateErr` `:340-348`

The mutator-error path calls `cacheMutation` unconditionally, caching the error
for the full `IdempotencyTTL` (5 min) — including errors classified
`retryable=true` (`codeDeadlineExceeded`→504, `codeUnavailable`→503).

**Failure scenario:** a `pane.split` times out (retryable) and is cached. The
client honors `retryable` and retries with the same `request_id`. `idem.get`
hits and replays the cached 504 with `Idempotent-Replay: true` — the mutation is
never actually attempted even though Herdr has since recovered. The op is
permanently un-retryable for 5 minutes.

**Fix:** do not cache retryable errors; leave no cache entry on a retryable
failure so a retry can proceed. Cache only success and terminal/non-retryable
errors.

### H4 — Flapping cloudflared never degrades: infinite fast-restart loop
**File:** `internal/tunnel/supervisor.go:207-208` (`s.backoff.Reset()` / `*consecutive = 0` in the `<-handle.ready()` branch)

The consecutive-failure counter and backoff are reset the instant cloudflared
*first* reaches readiness, with no stability window.

**Failure scenario:** cloudflared emits "registered tunnel connection" then dies
~1s later on a recurring edge/config error. Each cycle: ready → reset
`consecutive=0` → `afterFailure` counts 1 → restart at `Base` delay → ready →
reset. `consecutive` never reaches `maxConsecutive`, so `StateDegraded` is never
entered — the supervisor spins forever at minimum backoff. This violates SPEC §17
("repeated crashes mark the daemon degraded instead of spinning forever").

**Fix:** record the time readiness was reached; only reset
`consecutive`/backoff on exit if the instance stayed ready past a stability
threshold (e.g. 30–60s), not merely on the readiness edge.

---

## MEDIUM

### M1 — Security guards key on a different resource than the one actually dispatched (`target`/`workspace_id` divergence)
**Files:** guard/audit resource in `internal/server/mutations.go:216-219` (`resourceField`); dispatch in `internal/integration/mutate.go:204,214,224,270-272`

For `agent.prompt`/`agent.send_keys`/`agent.rename`/`agent.focus`, the server
computes the guarded/audited resource as `paramString(params, "pane_id")`, but
the dispatcher acts on `targetOrPane(p.Target, p.PaneID)` — if `target` is
supplied it is used *instead of* `pane_id`. For `worktree.remove`/`remove_force`
the confirmation binds `worktree_id` but the dispatcher prefers `workspace_id`
(`worktreeWorkspaceField`).

**Failure scenario:** a client supplies a fresh `pane_id` (passing the generation
guard) plus a different `target`; the generation guard validates `pane_id` while
Herdr input goes to `target`. The audit record also stores the wrong (or empty)
resource. The generation guard is therefore ineffective for the agent ops even
when the client *does* send a generation.

**Fix:** derive the guarded/audited resource from the exact identifier the
dispatcher will use (resolve `target`/`workspace_id` fallbacks in one place before
the guard), or forbid the alternate fields for these ops.

### M2 — The live session token (cookie value) is written to `audit.jsonl`
**Files:** `internal/integration/auth.go:90,140` (`sess.ID` is both the cookie value and `Identity.SessionID`); logged at `internal/server/mutations.go:155,282,296`, `internal/server/terminalroute.go:96-132`; sink `internal/server/audit.go:69`

The session cookie value equals `sess.ID` (`auth.NewSessionCookie(sess.ID, …)`),
and `toServerIdentity(sess.ID, …)` puts it in `Identity.SessionID`, which every
audit record persists as `session_id`.

**Failure scenario:** `audit.jsonl` (mode 0600) accumulates live session tokens
valid up to the 12h TTL. SPEC §7 explicitly forbids audit from containing
"cookies … or pairing values," and SPEC §9.1 keeps session records in memory
only; this leaks a session-hijacking credential onto disk (surviving restarts and
being captured by any backup of the state dir).

**Fix:** audit a non-secret, stable per-session identifier (e.g. a hash of the
session id, or a random public session label generated at creation) instead of
the raw `sess.ID`.

### M3 — The `security` package's middleware, headers, origin checks, bounded reader, and secret redaction are unused dead code (false test confidence), and the redaction has a real leak
**Files:** `internal/security/middleware.go`, `headers.go`, `origin.go`, `bounded.go`, `redact.go`

Only `security.NewANSIFilter` is used in production (via
`internal/integration/terminalfilter.go`). `security.New`/`Middleware.Wrap`,
`RedactSecrets`, `SanitizeForLog`, `SanitizeLogLine`, `NewANSIWriter`, and
`BoundedReader` have zero production callers — `internal/server` re-implements
middleware, headers, origin, and body bounds independently. Their tests
(`middleware_test.go`, `redact_test.go`, `headers_test.go`, `origin_test.go`,
`bounded_test.go`) pass while exercising code no request ever reaches.

The redaction itself is buggy: `headerPattern` (`redact.go:22`) matches the value
as `\s*[:=]\s*\S+`, which stops at the first whitespace. `Authorization: Bearer
<tok>` redacts only `Bearer` and leaves `<tok>`; `Cookie: a=1; b=2` leaves `b=2`.
`redact_test.go:36` only asserts `[REDACTED]` appears, never that the token is
gone, so it passes despite the leak. If this helper is ever wired to a log sink it
will leak credentials.

Separately, **no production log path applies control-char/secret sanitization**
(the daemon log is raw serve stdout/stderr; cloudflared logs use the tunnel
package's own `sanitizeLogText`, not `security`), so SPEC §17 "sanitize control
characters … from every log line" is only partially met.

**Fix:** wire `security.SanitizeForLog` into the daemon/serve log sink (after
fixing the `\S+` → `[^\r\n]*` value match and adding real assertions), or delete
the unused package surface so there is one audited implementation.

### M4 — `cmd.Wait()` is gated behind stdout/stderr EOF, defeating `WaitDelay`: zombie + goroutine leak if a descendant escapes the process group
**File:** `internal/tunnel/process.go:89-100`

`cmd.Wait()` is called only after `scanWG.Wait()` (both scanners hit EOF). EOF
requires every writer of the pipe to exit. `WaitDelay`'s pipe-unblock safety net
runs *inside* `Wait()`, which is never reached.

**Failure scenario:** cloudflared spawns a helper that `setsid`s into its own
session; the group-wide `SIGKILL` (`killGroup` sends to `-pid`) misses it; it
keeps the stdout/stderr write end open → scanners never EOF → `scanWG.Wait()`
blocks forever → `cmd.Wait()` never runs → the leader is never reaped (zombie) and
the goroutine leaks. `stop()` returns its "did not exit after kill" error but the
leak persists.

**Fix:** call `cmd.Wait()` independently of the scanners (let `WaitDelay` close
the pipes), or close the pipe read ends on a bounded timer after kill so the
scanners unblock.

### M5 — Restart backoff sleep ignores the stop signal; graceful shutdown is delayed up to the backoff cap
**Files:** `internal/tunnel/supervisor.go:127-137` (used at `:253`); `internal/daemon/supervise.go:121-131` (used at `:217`)

Both restart sleepers select on `ctx.Done()` and the timer, but not on `stopCh`.
`Stop()` closes `stopCh` without cancelling the serve context.

**Failure scenario:** a child is crash-looping and the supervisor is mid
backoff-sleep (up to `Max` = 30s). `herdr-phone stop` → control socket →
`child.Stop()` closes `stopCh`, but the sleep runs to completion, delaying
teardown ~30s.

**Fix:** make the sleeper also select on `stopCh` (pass it in or make it a method).

### M6 — Control-socket accept loop returns on any transient `Accept` error, permanently disabling the control socket
**File:** `internal/daemon/control.go:142-148`

On any `Accept()` error where `!isClosed()`, the code `return`s (killing `Serve`),
despite the comment calling the error transient.

**Failure scenario:** a temporary `Accept` failure (EMFILE under fd pressure,
EINTR) permanently stops the control socket. `status`/`rotate-pairing`/`stop`
stop working; the daemon can then only be SIGKILLed, defeating socket-based stop
(SPEC §7).

**Fix:** if the error is a temporary `net.Error`, back off briefly and `continue`;
`return` only on a permanent/closed listener.

### M7 — No exclusive lock across reconcile→bind: concurrent starts clobber the socket and orphan a daemon
**Files:** `internal/daemon/control.go:117` (unconditional `os.Remove(path)`); `internal/daemon/daemon.go:126-146`; `internal/integration/runtime.go:103-160`

Reconcile → `CleanupStale` → `Serve` is not atomic and there is no OS advisory
lock.

**Failure scenario:** two `herdr-phone start` invocations race; both `Reconcile`
return Absent/Stale; both `Serve` → the second's `Listen` removes the first
daemon's live socket and rebinds, and `WriteRuntime` overwrites `runtime.json`.
The first daemon keeps running its children but is now unreachable/unstoppable via
socket, breaking "start is idempotent" (SPEC §6).

**Fix:** hold an `flock`/`O_EXCL` lockfile in the state dir across
reconcile+bind so only one daemon can own a state dir.

### M8 — Content hash is order-dependent on unsorted Herdr wire arrays (rebroadcast storm risk)
**Files:** `internal/state/snapshot.go:67-101` (`project`), `:104-114` (`hashTopology`)

`project()` copies `Panes`, `Workspaces`, `Tabs`, `Agents`, `Layouts`,
`Worktrees` in wire order without sorting. `json.Marshal` sorts map keys but not
slices, so the hash changes whenever Herdr returns identical content in a
different array order.

**Failure scenario:** if Herdr enumerates panes/agents from an internal map
(nondeterministic order), `hashTopology` differs every 1.5s poll despite an
unchanged topology → the engine rebroadcasts a full snapshot to every phone client
each poll, defeating "broadcast only changes" (SPEC §11) — battery/bandwidth cost
on the relay.

**Fix:** sort each projection slice by a stable key (pane/workspace/tab/agent id,
layout tab id, worktree path) before hashing.

### M9 — Hot/cold cadence only inspects `Workspace.AgentStatus`, so an active agent can be polled at the 12s cold rate
**File:** `internal/state/engine.go:172-186` (`active()`)

`active()` iterates only `Topology.Workspaces` and tests `w.AgentStatus.Active()`;
it never checks `Topology.Panes[].AgentStatus` or the authoritative
`Topology.Agents[].AgentStatus`.

**Failure scenario:** Herdr reports an agent `working` on a pane while the
enclosing workspace rollup lags at `idle`/empty and no browser is subscribed →
`active()` returns false → the engine polls at 12s while an agent is actively
working, violating "poll every 1.5s while any agent is working/blocked" (SPEC §11).

**Fix:** OR-in per-agent/per-pane status (scan `topo.Agents`/`topo.Panes`) in
`active()`.

### M10 — ANSI filter misses reply-inducing CSI device queries (device-query bypass)
**File:** `internal/security/ansi.go:266-274` (`csiShouldStrip`)

Only final bytes `n`/`c`/`t` are stripped. Reachable reply-inducing sequences pass
through: DECRQM `ESC[?...$p` (final `p` with `$` intermediate), XTVERSION
`ESC[>...q` (final `q`), DECREQTPARM `ESC[...x`. xterm.js answers these and the
reply travels back over the socket into the pane's stdin — the answerback/device
-query injection class SPEC §13 requires stripping. The fuzz oracle
`csiForbiddenAt` (`internal/security/ansi_fuzz_test.go:59-72`) checks only
`n`/`c`/`t`, so it shares the blind spot.

**Fix:** also strip `p` when a `$` intermediate is present, and `q`/`x` under a
`>`/private prefix, while preserving DECSCUSR (`SP q`), DECSTBM, etc.; extend the
fuzz oracle.

### M11 — Blocking keepalive `Ping` in the single terminal WebSocket writer stalls output and teardown
**Files:** `internal/terminal/bridge.go:376-380`; adapter `internal/server/terminalroute.go:216-220`

`writeLoop` is the sole WS writer; on `ping.C` it calls `b.conn.Ping(nil)`, which
in the production `coderConn` blocks until the pong arrives or 30s
(`terminalPingTimeout`). While blocked it cannot drain `outCh`.

**Failure scenario:** on a high-latency/lost pong the 256-slot queue fills and
`enqueue` disconnects a *healthy* client as "too slow" (`bridge.go:437-439`). It
also stalls teardown: after ctx cancel, `run()` waits at `<-b.writerDone`
(`bridge.go:207-212`) and the `SetWriteDeadline(now())` escape only cancels an
in-flight *write*, not the Ping's independent 30s context — cleanup (and the
parked browser reader) can hang up to 30s per session, piling up goroutines under
mobile reconnect churn.

**Fix:** send keepalive pings from a dedicated goroutine, or make the adapter's
Ping non-blocking and track pong deadlines separately, so the writer never blocks
on RTT.

---

## LOW

- **L1 — Pairing single-use lost on rotate entropy failure.**
  `internal/auth/pairing.go:99`: `Verify` returns `true` but discards
  `_ = p.rotateLocked()`. If entropy fails, `rotateLocked` leaves `p.secret`
  unchanged (`:50-57`), so the just-used link can pair again. Propagate the error
  or zero the secret on failure. (Entropy failure is near-impossible → low.)

- **L2 — Event reconnect backoff reset on `subscription_started` → tight reconnect/wakeup storm.**
  `internal/herdr/events.go:123-124,178-186`: a server that accepts then
  immediately drops the subscription loops at ~500ms forever, firing `signal()`
  into the engine each cycle with no backoff. Reset backoff only after the session
  stays up a minimum duration or delivers ≥1 post-start frame.

- **L3 — Sequence check rejects a legitimate first frame with `seq == 0`.**
  `internal/terminal/bridge.go:130,298`: `rec.Seq <= b.lastSeq` with zero-valued
  `lastSeq` cannot distinguish "no frame yet" from a valid `seq 0`, killing the
  session on every connect if Herdr is 0-based. (SPEC's example uses `seq 1`, so
  likely not triggered today — latent.) Use a `haveSeq bool` sentinel.

- **L4 — Rate-limiter bucket map grows unbounded.**
  `internal/server/stores.go:170-204`: unlike `nonceStore`/`idemStore`, buckets
  keyed by session id / client IP are never evicted — slow memory growth over a
  long-lived daemon. GC full+idle buckets or cap the map.

- **L5 — Session GC is never scheduled.**
  `internal/auth/session.go:197-209`: expired sessions are removed only lazily on
  `Get` of the same id or an explicit `GC()`, and nothing calls `GC()` on a timer.
  Abandoned sessions persist in memory. Start an internal janitor or run `GC()`
  from the daemon on a ticker.

- **L6 — `stop` response can be lost to a shutdown race.**
  `internal/daemon/daemon.go:227-236` spawns `Shutdown` asynchronously;
  `ControlServer.Close` (`control.go:242-252`) may close the in-flight conn before
  `writeResponse` flushes `ok`, so `herdr-phone stop` can report failure though
  shutdown proceeds. Write the reply before signalling teardown, or exclude the
  requesting conn from the forced close.

- **L7 — `MetricsAddr` not validated to be loopback.**
  `internal/tunnel/mode.go` `Validate` has no check; the field comment claims "it
  must stay on loopback." A config value like `0.0.0.0:9090` would expose
  cloudflared diagnostics off-loopback (SPEC §3.2). Not user-exposed today, but
  enforce a loopback host in `Validate`.

- **L8 — `HERDR_SOCKET_PATH` env value is not `~`-expanded.**
  `internal/herdr/resolve.go:14-19`: the configured path goes through
  `expandHome`, the env branch returns the raw value, so
  `HERDR_SOCKET_PATH=~/…` fails to dial. Expand the env value too.

- **L9 — Duplicate secret-command implementations; `config.ResolveSecretCommand` is dead.**
  `internal/config/secret.go:27` (64 KiB bound, tested) is never called; the
  tunnel resolves tokens via its own `runTokenCommand`
  (`internal/tunnel/token.go`, 8 KiB bound). Two divergent security-critical paths
  invite drift. Consolidate on one.

- **L10 — `decode` claims strict decoding but ignores unknown params.**
  `internal/integration/mutate.go:316-324` uses plain `json.Unmarshal` (no
  `DisallowUnknownFields`), contradicting its "decoded strictly" comment. Bounded
  by the typed fields + allowlist, so low; either enable strict decoding or fix
  the comment.

- **L11 — Config precedence has no fallthrough.**
  `internal/config/config.go:165-179`: `Path` returns the file under the first
  *set* env var; if that dir lacks `config.toml` it silently uses defaults instead
  of falling back to a lower location that has one (SPEC §8 lists an ordered
  search). Iterate candidates and use the first that exists.

- **L12 — Env expansion: set-but-empty treated as unset; `token_command` argv not expanded.**
  `internal/config/paths.go:22-31` flags an empty env value as "unresolved"
  (deviates from "error on unset"), and `token_command` entries are never
  `~`/env-expanded (`config.go` expand covers only fixed path fields), so
  `token_command = ["~/bin/get-token"]` is passed literally and fails to exec. No
  shell is ever invoked (correct); usability only.

- **L13 — Unknown `kid` forces a JWKS refetch on every call before signature verify.**
  `internal/auth/jwks.go:143-154`, `internal/auth/jwt.go:209`: a stream of tokens
  with random `kid` values drives one outbound JWKS GET per request (sequential
  ones aren't coalesced), risking upstream rate-limiting. Only reachable via direct
  origin access (loopback, behind Access) → low. Negative-cache unknown kids or
  enforce a minimum refresh interval.

- **L14 — No minimum RSA modulus size.**
  `internal/auth/jwt.go:297-315` accepts any non-empty modulus; a weak key from a
  compromised/misconfigured JWKS would be accepted. Mitigated by HTTPS to the
  trusted Cloudflare endpoint. Reject `< ~2048`-bit moduli.

- **L15 — 8-bit C1 control introducers are never filtered.**
  `internal/security/ansi.go:68-69`: bytes ≥ 0x80 are always treated as UTF-8, so
  8-bit OSC (0x9D)/CSI (0x9B)/DCS (0x90) forms bypass the filter. Safe for
  xterm.js in UTF-8 mode, but a real bypass for the operator's real terminal or a
  non-UTF-8 log renderer (both named as sinks in the package doc). Drop lone
  0x90/0x9B/0x9D/0x9E/0x9F, or document the residual risk.

- **L16 — Resize/dimension metadata silently dropped under backpressure.**
  `internal/terminal/bridge.go:414-420` (`sendText`) drops on a full `outCh` while
  the binary frame is force-enqueued, so xterm.js can render at a stale size until
  the next metadata message. Coalesce/prioritize the latest dims or piggyback them
  on the frame.

- **L17 — `isConflict` substring match can misclassify a benign close as a takeover conflict.**
  `internal/terminal/bridge.go:351-359` matches free-text like `"in use"`. Prefer a
  structured conflict field from the controller.

- **L18 — State subscriber bounds are partly ineffective.**
  `internal/state/subscriber.go:31-36` (seed bypasses the `maxBytes` overflow
  check), `:89-98` (`maxItems` is dead because `pending` is always coalesced to ≤1
  before the check), and `internal/state/engine.go:224-226` (snapshot `bytes`
  stays `0` on a `json.Marshal` error, so an unsized snapshot is accounted as free
  and never trips the byte bound). Route the seed through `enqueue`, drop/annotate
  the dead item bound, and assign a conservative size on marshal error.

- **L19 — `handleStatus` TOCTOU can overwrite `Stopping` health in memory.**
  `internal/daemon/daemon.go:169-177` reads `currentHealth()`, checks
  `!= HealthStopping`, then `setHealth(...)` non-atomically; a concurrent
  `Shutdown` can be overwritten (disk `runtime.json` is still correct). Compute and
  set under one lock.

- **L20 — `Handshake` validates protocol but not version.**
  `internal/herdr/ping.go:32-42` checks `Protocol` only; SPEC §10 says "validate
  ping version/protocol." Add a version compatibility check or document that
  protocol is the gate.

- **L21 — CSP `connect-src` allows any WebSocket host.**
  `internal/server/server.go:217` sets `connect-src 'self' ws: wss:`; the bare
  `ws:`/`wss:` schemes permit connecting to any host, weakening the SPEC §9.3
  "explicitly allow the same-origin WebSocket" intent (server-side Origin checks
  still apply). Scope to the origin host.

---

## Tests that pass without exercising real behavior

- **TG1 — Idempotency concurrency untested.** `internal/server/server_test.go`
  `TestMutationIdempotencyReplay` (`:428`) issues the retry only after the first
  fully returns; `TestConcurrentClientsAndMutationsRace` (`:668`) uses distinct
  `request_id`s. The double-execution bug (H2) passes the suite. Add two
  goroutines with the same `session+request_id` against a delaying mutator and
  assert `callCount()==1`.

- **TG2 — Generation-omission bypass untested.**
  `internal/server/server_test.go:445` (`TestMutationGenerationStale`) only sends a
  nonzero *mismatched* value. Add a case that omits `expected_generation` on a
  generation-checked op and asserts the mutator is NOT called (H1).

- **TG3 — JWKS bounded-body test is tautological.**
  `internal/auth/jwks_test.go:147-172` serves `{"keys":[` + garbage — never valid
  JSON — so the parse fails regardless of `WithMaxBody`; it proves "invalid JSON
  fails," not truncation at the limit. Serve valid JSON larger than `maxBody`.

- **TG4 — Redaction test never asserts the secret is gone.**
  `internal/security/redact_test.go:36` asserts `[REDACTED]` appears but not that
  `sometoken` is absent, hiding the `\S+` leak (M3). Assert the token/second cookie
  are removed.

- **TG5 — Directory symlink-escape untested at the composition layer.**
  No `directories_test.go` in `internal/integration/`; the security-critical
  `dirValidator.Resolve` (`directories.go:26`) symlink/`..` confinement is
  unverified there (logic looks correct). Add plant-a-symlink and `..` cases.

- **TG6 — `security` package tests exercise unused code.** middleware/headers/
  origin/bounded/redact tests give confidence in code no production request reaches
  (M3).

- **TG7 — Terminal/ANSI oracles share blind spots.** the seq oracle starts at 5
  (`bridge_test.go:428`, misses L3) and the CSI fuzz oracle checks only `n`/`c`/`t`
  (`ansi_fuzz_test.go:59-72`, misses M10).

---

## Verified correct (not defects)

- Access JWT verification: exact RS256 string check, required `kid`, PKCS1v15 over
  the received `header.payload`, exact issuer, exact audience, correct
  exp/nbf/iat skew directions, `exp` required, fail-closed on unknown key
  (`internal/auth/jwt.go`). No alg-confusion path.
- Central middleware order matches SPEC §9.3 exactly and every non-`/health`
  route is registered through `wrap` (`internal/server/routes.go:97-178`); `/health`
  is the only unauthenticated route and reveals the instance id only to a
  constant-time-verified probe token.
- Confirmation nonces are single-use (delete-before-validate), scoped, and bind
  operation+resource+generation+session+params-hash with constant-time compares
  (`internal/server/stores.go:54-95`); nonce issuance checks generation
  unconditionally, so destructive pane ops are not bypassable via H1.
- Poll overlap: the in-progress guard plus a single `queued` bool guarantees
  exactly one follow-up; `Wake` coalesces (`internal/state/engine.go:106-160`).
- Herdr client: per-request connection, byte-bounded NDJSON framing that tolerates
  fragmentation and UTF-8 splits, timeout/cancel paths that reap the reader
  goroutine and close the conn (`internal/herdr/client.go:120-253`).
- cloudflared argv never carries the token (only the file path), `--no-autoupdate
  --loglevel info --output json --metrics 127.0.0.1:0`; the temp token file is
  0600 and deleted on every exit path after readiness (readiness = connection
  registration, i.e. after the token is read) (`internal/tunnel/args.go`,
  `process.go`, `supervisor.go`); asserted by a real fake-cloudflared in
  `internal/integration/lifecycle_test.go`.
- Reconcile confirms process identity via the control-socket `InstanceID` before
  treating a PID as live, so PID reuse cannot adopt/kill the wrong process
  (`internal/daemon/reconcile.go`); stop is always via the control socket, never an
  arbitrary PID kill.
- Directory confinement and credential-file checks resolve symlinks before the
  canonical-root prefix test and use `Lstat` + uid/`0o077` checks
  (`internal/config/verify.go`, `internal/integration/directories.go`); the
  prefix test uses a separator boundary, preventing sibling-prefix escape.
- `--takeover` is gated behind a scoped, single-use confirmation nonce validated
  before the socket upgrade (`internal/server/terminalroute.go:57-66`).
- Terminal subprocess cleanup: own process group, SIGTERM + stdin close, bounded
  `WaitDelay`, `Wait` reaped only after `readController` returns
  (`internal/terminal/controller.go`, `bridge.go:200-221`) — except the Ping stall
  in M11.
- Audit sink is mode 0600, strips control chars/ESC and bounds every field
  (`internal/server/audit.go`) — except that it records the session token (M2).
