# herdr-phone Backend Code Review — Round 2

Date: 2026-07-24
Scope: Go backend (`cmd/`, `internal/`), re-reviewed against `SPEC.md` and the
round-1 report (`docs/reviews/backend.md`).
Method: full re-read of the changed production composition
(`internal/server`, `internal/tunnel`, `internal/daemon`, `internal/state`,
`internal/security`, `internal/auth`, `internal/integration`), verification that
each prior HIGH/MEDIUM fix reaches the real wiring, confirmation that each has a
meaningful regression test, and a fresh regression hunt over the new code.

## Verification run

- `go build ./...` → success.
- `go vet ./...` → clean.
- `go test ./...` → 595 passed (up from 501; ~93 new tests).
- `go test -race ./...` → **594 passed, 1 intermittent failure** in
  `internal/server/TestConcurrentClientsAndMutationsRace` on the *first* run only;
  green on the isolated package (5×) and on 9 subsequent full `-race ./...` runs.
  Root-caused below as a flaky test, not a production defect (finding R1).

## Bottom line

**All four HIGH findings (H1–H4) and all eleven MEDIUM findings (M1–M11) are
resolved in the production composition and covered by meaningful regression
tests.** Several LOW items were also fixed as a bonus (L4 rate-limiter eviction,
L21 CSP `connect-src` scoping). The fixes are correct and did not introduce a
production regression. Round 2 raises one flaky-test issue and four low/minor
observations, none blocking.

---

## New findings (round 2)

### R1 — LOW (test reliability) — `TestConcurrentClientsAndMutationsRace` is flaky under parallel `-race` load
**File:** `internal/server/server_test.go:718-729`

Each of four event-WebSocket clients calls `c.readFrame(t)` exactly three times
(`:727-729`), and `readFrame` `t.Fatalf`s on error. The event hub coalesces
consecutive snapshots to the newest (bounded 1-item queue — correct per SPEC §11),
so a client that reads slower than the pusher pushes legitimately receives *fewer
than three* frames. Under a saturated `go test -race ./...` (15 packages in
parallel) the third `readFrame` then blocks until harness teardown closes the
socket, surfacing as `read frame hdr: EOF`.

- **Impact:** intermittent CI failures (~1 observed in ~13 full `-race` runs); no
  production defect — the coalescing it trips over is the specified behavior.
- **Fix:** read with a bounded deadline and assert "≥1 frame and the latest hash
  is observed" rather than a fixed count, or drive the pusher/reader with explicit
  synchronization as `findings_test.go:TestConcurrentIdenticalRequestsExecuteOnce`
  already does.

### R2 — LOW — A retryable failure on a *destructive* op cannot actually be retried (nonce already spent)
**File:** `internal/server/mutations.go:249-258` (nonce consume) vs `:300-357` (reserve / retryable release)

The H3 fix correctly stops caching retryable errors so a retry re-attempts — but
for confirmation-required ops the single-use nonce is consumed at `:254`, *before*
the reservation and the final deadline check. If the op then fails retryably
(deadline/unavailable) the error carries `retryable=true`, yet a retry with the
same `request_id` hits `nonces.consume` again with a spent nonce and gets
`403 confirmation invalid`. So the "retry can re-attempt" guarantee (H3) does not
extend to destructive ops.

- **Impact:** low; pre-existing (the nonce was always consumed before dispatch),
  not introduced by the round-1 fixes. A well-behaved client must request a fresh
  confirmation on a retryable destructive-op failure.
- **Fix:** consume the nonce only after the reservation and pre-dispatch deadline
  check succeed, or return a distinct code that tells the client to re-confirm
  rather than a bare `retryable=true`.

### R3 — INFO (minor over-strictness) — `agent.start` rejects a divergent `target` although its dispatcher ignores `target`
**Files:** `internal/server/mutations.go:60` (`altResourceField:"target"`) vs `internal/integration/mutate.go` `agent.start` (dispatches on `PaneID`, never `target`)

The M1 fix marks `agent.start` with `altResourceField:"target"`, so a request
carrying `target != pane_id` is now rejected with `conflicting resource
identifiers`. Unlike the other agent ops, `agent.start`'s dispatcher does not use
`target` at all, so the rejection is conservative rather than necessary. Safe
(fails closed), but a client that sends `target` for consistency would be refused
where it was previously (harmlessly) ignored.

- **Fix (optional):** drop `altResourceField` from `agent.start`, or keep it and
  document that `target` is not accepted for `agent.start`.

### R4 — LOW (residual of M3) — `security` package still ships unused middleware/headers/origin/bounded
**Files:** `internal/security/middleware.go`, `headers.go`, `origin.go`, `bounded.go`

The *actionable* half of M3 is fixed: `security.SanitizeForLog`/`RedactSecrets`
are now wired into `internal/server/audit.go:89` and
`internal/tunnel/supervisor.go`, and the redaction leak itself is fixed
(`redact.go:29` now matches the whole header value `[^\r\n]*`, plus a new
`tunnelTokenPattern`). However `security.New`/`Middleware.Wrap`, `headers`,
`origin`, and `BoundedReader` remain unused in production (the server
re-implements them in `internal/server`), so their tests still assert against code
no request reaches.

- **Impact:** low; maintenance/false-confidence hazard and divergence risk, no
  live vulnerability.
- **Fix:** delete the unused surface, or route the server through it.

### R5 — INFO (minor) — `SanitizeForLog` can over-redact a combined multi-field log line
**File:** `internal/security/redact.go:38-49` composed with `SanitizeLogLine` (`SanitizeForLog = RedactSecrets(SanitizeLogLine(s))`)

`SanitizeLogLine` collapses `\n`/`\r` to spaces first; `RedactSecrets`'
`headerPattern` then matches a header value as `[^\r\n]*`, which — with no newlines
left — extends to the end of the whole string. For a single field this is exactly
right; for a pre-concatenated multi-field line it would redact everything after a
header keyword. Safe (fail-closed), and not triggered today because audit fields
are sanitized individually, but worth noting before any caller passes composite
strings.

- **Fix (optional):** redact before newline-collapsing, or bound the header value
  to a token/`;`-delimited run.

---

## Prior-finding resolution table

| ID | Title | Status | Production fix (file:line) | Regression test |
|----|-------|--------|----------------------------|-----------------|
| **H1** | Generation guard opt-in (omitted `expected_generation` bypasses check) | ✅ Resolved | Mandatory + always-verified: `internal/server/mutations.go:264-278`; terminal attach `internal/server/terminalroute.go:64-88` | `server/findings_test.go:12` (mutation), `:31` (terminal attach) |
| **H2** | Idempotency cache had no in-flight reservation → concurrent retries double-execute | ✅ Resolved | Atomic `reserve`/`release`/`complete` (`stores.go:167-198`) wired at `mutations.go:303-320,352-376` | `server/findings_test.go:43` (concurrent, `callCount==1`), `:249` (reservation released on validation failure) |
| **H3** | Retryable errors cached → legitimate retry replays stale error | ✅ Resolved | `mutations.go:349-355` releases (does not cache) on retryable | `server/findings_test.go:107` (`callCount==2`, no replay) |
| **H4** | Flapping cloudflared never degrades (reset on readiness edge) | ✅ Resolved | Stability window; reset only if ready ≥ `stabilityWindow` (`tunnel/supervisor.go:257-267`; default 45s `tunnel/mode.go:48,117`) | `tunnel/supervisor_test.go:233` (`TestSupervisorFlappingDegrades`), `:157`; `tunnel/lifecycle_test.go:267-285` |
| **M1** | Guards key on `pane_id`/`worktree_id` but dispatch acts on `target`/`workspace_id` | ✅ Resolved | `divergentAlt` rejects divergent alt id on both mutation (`mutations.go:243-246`) and confirmation (`:154-158`); `altResourceField` map `:56-67,86-87` | `server/findings_test.go:143` (divergent target), `:159` (matching allowed), `:172` (worktree workspace) |
| **M2** | Live session cookie value written to `audit.jsonl` | ✅ Resolved | Separate 128-bit non-secret `AuditID` (`auth/session.go:22,61-64,140,153`); server sees `AuditID`, not cookie (`integration/auth.go:94,108,159`) | `auth/session_test.go:136` (`AuditID != ID`); `server/audit_test.go:97` |
| **M3** | Redaction leak + dead security pkg + no log sanitization | ✅ Resolved (residual R4) | Redaction fixed (`redact.go:29`), wired into `server/audit.go:89` and `tunnel/supervisor.go` | `security/redact_test.go` (asserts token removed); residual unused middleware/headers/origin/bounded → R4 |
| **M4** | `cmd.Wait()` gated behind pipe EOF → zombie/goroutine leak | ✅ Resolved | Writers (not `StdoutPipe`) so copiers obey `WaitDelay`; `cmd.Wait()` runs independently (`tunnel/process.go:76-99`; `exec.ErrWaitDelay` handled `:218-221`) | `tunnel` lifecycle/process tests (fake cloudflared executed) |
| **M5** | Restart backoff sleep ignored stop signal | ✅ Resolved | Sleepers select on `stopCh` (`tunnel/supervisor.go:167-178`; `daemon/supervise.go` sleeper) | `tunnel`/`daemon` supervise tests (stop during backoff) |
| **M6** | Control accept loop died on any transient error | ✅ Resolved | Bounded backoff + `continue`; returns only when closed (`daemon/control.go:149-166`) | `daemon/control_test.go` (transient accept error) |
| **M7** | No exclusive lock → concurrent starts clobber socket/runtime | ✅ Resolved | `flock(LOCK_EX\|LOCK_NB)` state lock across bind (`daemon/statelock.go`; acquired `daemon/daemon.go:139-145`) | `daemon` tests (`ErrStateLocked` on second acquire) |
| **M8** | Content hash order-dependent on unsorted wire slices | ✅ Resolved | `project()` sorts every slice incl. nested layout panes/splits (`state/snapshot.go:111-121,135-137`) | `state/review_fixes_test.go:11` (`TestHashStableUnderReordering`) |
| **M9** | Hot/cold cadence ignored per-agent/per-pane status | ✅ Resolved | `active()` also scans `Agents` and `Panes` (`state/engine.go`) | `state` cadence test (agent active, workspace idle) |
| **M10** | ANSI filter missed reply-inducing CSI queries (`$p`/`>q`/`x`) | ✅ Resolved | `csiShouldStrip` handles DECRQM/XTVERSION/DECREQTPARM/DECRQCRA, preserves DECSTR/DECSCUSR/DECFRA (`security/ansi.go`) | `security/ansi_test.go:98-103`; `ansi_fuzz_test.go:107-155` oracle extended |
| **M11** | Blocking keepalive `Ping` in single terminal writer stalled output/teardown | ✅ Resolved | Ping moved to a dedicated `pingLoop` goroutine; `writeLoop` only drains (`terminal/bridge.go:199,366-397`) | `terminal/bridge_test.go` (ping does not block frames) |

Legend: ✅ Resolved = fix present in the production composition and covered by a
regression test that fails against the old behavior.

---

## Regression hunt over the new code (no defects found)

- **Idempotency reservation (`stores.go:167-198`):** `reserve` resolves
  cached/in-flight/new under one lock; `release` only deletes a still-`pending`
  entry (cannot clobber a completed cache); `get` treats `pending` as a miss so
  the authoritative check is `reserve`. Pre-dispatch validation runs before the
  reservation and needs no cleanup; the post-reservation path always
  completes/releases (with a `defer` safety net at `mutations.go:314-320`).
  Verified by `TestReservationNotLeakedOnValidationFailure`.
- **Mandatory generation (H1):** `divergentAlt` closes the `target`/empty-`pane_id`
  side-channels — an empty canonical resource with a non-empty alt is rejected,
  and a missing pane id fails the existence check; no bypass path remains.
- **State lock (M7):** genuine `flock`, mode 0600, kernel-released on abnormal
  exit; acquired before `WriteRuntime`/`Listen`, released on every error path.
- **Orphan reconciliation (new — `tunnel/orphan.go`):** guards PID reuse with a
  `ps -p <pid> -o comm=` binary-identity check and refuses to kill an unverified
  PID; the pidfile is written atomically (temp + rename) at mode 0600. Narrow
  residual: `comm` base-name matching could match an unrelated `cloudflared`, but
  the code errs toward not killing — acceptable.
- **`process.go` line writers (new):** replacing `StdoutPipe` with
  `newLineWriter` keeps readiness/URL classification intact (fake-cloudflared
  lifecycle tests pass) and lets `WaitDelay` bound a stuck descendant.
- **`active()` (M9):** now returns hot more often (correct); no path makes it
  return cold while an agent is working.
- **Auth `AuditID` (M2):** the session map is still keyed by the bearer `ID`;
  `Get`/`ValidateCSRF` are unchanged; `AuditID` is display/audit-only. No lookup
  regression.
- **CSP scoping (L21) / rate-limiter eviction (L4):** fixed and tested
  (`findings_test.go:185`, `:214`) without weakening other headers or the token
  bucket semantics.
