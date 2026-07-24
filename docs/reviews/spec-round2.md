# herdr-phone Specification Audit — Round 2

Status: read-only re-audit against `SPEC.md`, verifying remediation of the round-1 findings in `docs/reviews/spec.md`.
Date: 2026-07-24
Scope: full current tree + `docs/reviews/spec.md`. Re-verified C1, H1–H3, M1–M7, related LOW items, and Definition of Done evidence (frontend/backend contract, required UI actions, deterministic Playwright, GoReleaser/Node consistency tests, release workflow). Ran the relevant build/test commands. No code was modified.

Method: writes had settled before the audit (nothing modified in the preceding 30 min; remediation landed ~00:04). Every claim below was verified first-hand by reading the current code and/or executing the command shown.

---

## 1. Verdict

**All round-1 findings (C1, H1–H3, M1–M7) are resolved**, and each is now backed by a real, wired code path plus a test. The two round-1 LOW items in the security/reconnect area that were quick to confirm (L1 `min_herdr_version`, L8 `freeze`) are also fixed. Build, `go test`, `go test -race`, frontend unit, typecheck, and the **full Playwright suite with retries disabled** all pass. The takeover journey — the round-1 flake (M7) — passed 4/4 standalone runs and again in the full suite with `--retries=0`.

The Definition of Done (§22) is now materially met for the items that were previously blocking. The remaining items below are **new/residual LOW-severity observations only** — none reopen a resolved finding, and none block the DoD.

### Verification commands (this audit)

| Command | Result |
|---|---|
| `go build ./...` / `go vet ./...` | pass (exit 0) |
| `go test ./...` | **595 passed**, 15 pkgs, exit 0 |
| `go test -race ./...` | pass (exit 0) |
| `npm test` (vitest) | **128 passed**, 30 files, exit 0 |
| `npm run typecheck` | clean |
| `npm run lint` | exit 0 (3 non-blocking `react-refresh/only-export-components` warnings) |
| `npx playwright test --retries=0` | **45 passed, 6 skipped, 0 failed** |
| `npx playwright test -g "take over" --retries=0` ×4 | 4/4 passed |

(Test counts grew 501→595 Go and 93→128 web since round 1, reflecting the new remediation tests.)

---

## 2. Findings (current)

Only new or still-open items are listed. Resolved round-1 issues are in the matrix (§3), not here.

### LOW

**F1 — Key-dock paste silently concatenates multi-line input (new, introduced by the M5 fix).**
`stripControl` (`web/src/lib/format.ts`) removes every C0/C1 control byte, including `\n` (0x0A) and `\r` (0x0D), and is applied to pasted clipboard text before it reaches the terminal (`web/src/components/key-dock.tsx:50`). Pasting a multi-line block (e.g. `echo a⏎echo b`) yields the single joined string `echo aecho b`, changing command semantics rather than preserving or rejecting the paste. Stripping escapes is a sound safety goal for a remote shell, but collapsing interior newlines is a silent correctness surprise. Consider preserving `\n`/`\t` (or normalizing newlines to a bracketed-paste sequence) while still stripping ESC/other C0. Not a spec violation (§14.4 only requires the paste affordance, which now exists and is tested).

**F2 — Secret-command token still trimmed with `TrimSpace`, not "trimmed once" (round-1 L2, still open).**
`internal/tunnel/token.go:46` uses `strings.TrimSpace`, which strips all surrounding whitespace, whereas §8 specifies output "trimmed once." The spec-correct `trimOneTrailingNewline` exists (`internal/config/secret.go:89`) but is reached only via `ResolveSecretCommand`, which the live tunnel token path does not use. A token with legitimate leading/trailing whitespace would be altered. Out of the primary round-2 scope but part of §8; low impact.

**F3 — CI Playwright `retries: 1` can mask a future flake (observation).**
`web/playwright.config.ts:14` sets `retries: process.env.CI ? 1 : 0`. The takeover determinism fix is solid (verified 4/4 + full-suite `--retries=0`), so this is not currently masking anything, but a retry-on-CI policy means a newly-flaky required journey could still report green in CI, which is in tension with the §18/§22 "journeys pass" (deterministically) intent. Consider `retries: 0` for the required-journey project, or a flake-detection run.

**F4 — Dead-code duplication persists (round-1 L3, partially open; informational).**
The CSP and log-redaction *functionality* is now correctly wired inline (`internal/server/server.go` `buildCSP`; `SanitizeForLog` in `audit.go`/`supervisor.go`), which closed M1/M2. But the parallel implementations remain unused: `internal/security/headers.go` `BuildCSP` and the `trimOneTrailingNewline`/`ResolveSecretCommand` path (F2). Non-functional, but the divergence is what produced the original M1/M2/L2 gaps; consolidating would prevent recurrence.

### Carried-over round-1 LOW items still open (not re-investigated in depth; outside the C1/H1–H3/M1–M7 scope)

L4 (terminal-input generation guard is per-connection, not per-frame), L5 (target-addressed agent ops with generation 0 bypass the guard — non-destructive), L6 (pane generation dropped rather than incremented on exit — behaviorally safe), L7 (blocked-first ordering is frontend-only; last-transition is a seq not a timestamp), L10 (topology "swipe changes level" gesture not implemented). All were LOW/observation in round 1 and remain so.

---

## 3. Requirement / Remediation Matrix

### 3.1 Round-1 findings — remediation status

| ID | Round-1 issue | Status | Evidence (verified this audit) |
|---|---|---|---|
| **C1** | Agent-start unreachable + malformed (no `name`, sheet never mounted) | **RESOLVED** | `StartAgentSheet` mounted at `web/src/components/pane-actions.tsx:127`; collects & validates `name` (`isValidAgentName`, unique, `kindsAvailable`; `agent-actions.tsx:179-203`); dispatches `agent.start {pane_id, kind, name}` + `expectedGeneration`; kinds sourced from capabilities. E2E `start a discovered agent … (C1)` passes. |
| **H1** | Session cookie value written to `audit.jsonl` | **RESOLVED** | Audit now records the distinct non-secret `AuditID` (`internal/auth/session.go:61-64,140`; `internal/integration/auth.go:94,159`), never the cookie/session id. `AuditID` "is never accepted as a cookie or session identifier by the store." |
| **H2** | `tab.move` had no UI | **RESOLVED** | Reorder controls call `tab.move {tab_id, insert_index}` (`web/src/components/topology-ribbon.tsx:319,328`). E2E `reorder tabs (H2)` passes. |
| **H3** | `agent.send_keys` mutation was dead | **RESOLVED** | `AgentKeysSheet` mounted in herd (`web/src/routes/herd.tsx:64`) → `agent.send_keys {pane_id, keys}` (`agent-actions.tsx:130`). E2E `send validated keys to an agent (H3)` passes. |
| **M1** | CSP `connect-src ws:/wss:` wildcard | **RESOLVED** | `connect-src` now scoped to configured origins (`internal/server/server.go:230,239`); `script-src 'self'`, no `unsafe-eval`. (`style-src 'unsafe-inline'` remains but is spec-compliant — §9.3 bans only `unsafe-eval`.) |
| **M2** | Secret log redaction never invoked | **RESOLVED** | `security.SanitizeForLog` wired into `internal/server/audit.go:89` and `internal/tunnel/supervisor.go:336`. |
| **M3** | Create-ws/tab read `root_pane_id`; backend returns `root_pane` object → silent no-op nav (and test mocked wrong shape) | **RESOLVED** | New `rootPaneId()` reads `result.root_pane.pane_id` (`web/src/lib/mutation-result.ts:24`), used at `create-sheets.tsx:29,91`; test fixture corrected to `root_pane: { pane_id }` (`create-sheets.test.tsx:24`). |
| **M4** | `pane.move` to existing tab missing | **RESOLVED** | `pane.move {destination:{type:"tab", tab_id}}` (`web/src/components/pane-actions.tsx:82`). E2E `move a pane to an existing tab (M4)` passes. |
| **M5** | Key dock missing paste affordance | **RESOLVED** | Paste button + `navigator.clipboard` + `onPaste` → `sendText` (`web/src/components/key-dock.tsx:29,39-52,109-111`; wired in `routes/terminal.tsx:114`). E2E `paste from the key dock (M5)` passes. See F1. |
| **M6** | GoReleaser↔build.sh sync "enforced by tests" but no test | **RESOLVED** | `TestGoreleaserArchiveTemplateMatchesBuildScript` and `TestNodeVersionConsistency` (`internal/app/release_consistency_test.go`) now enforce it; `ci.yml:22` declares `NODE_VERSION: '22'`, matching `mise.toml`. |
| **M7** | Takeover Playwright journey flaky | **RESOLVED** | Ownership barrier added (p1 must attach before p2 connects; `p2` pairs with `reset:false`; `web/e2e/journeys.spec.ts:183-205`). Verified 4/4 standalone + full suite, all `--retries=0`. |
| **L1** | `min_herdr_version` not enforced at runtime | **RESOLVED** | `internal/herdr/ping.go:48` rejects when `versionOlderThan(p.Version, MinHerdrVersion)`. |
| **L8** | `freeze` revalidation trigger declared but not registered | **RESOLVED** | `freeze` registered on `document` (`web/src/lib/reconnect.ts:86`). |

### 3.2 Definition of Done (§22) — current evidence

| DoD item | Status | Evidence |
|---|---|---|
| Required files exist, real content, no TODO/FIXME/fake paths | **OK** | grep clean; only "not implemented" is a defensive default branch |
| Go 1.26 pinned in `go.mod` + `mise.toml` | **OK** | enforced `goversion_test.go` |
| `make check` / `go test -race ./...` pass | **OK** | `go test -race ./...` exit 0 (this audit) |
| Frontend unit, component, **required Playwright journeys** pass | **OK** | 128 vitest; full e2e 45 passed/6 skipped/0 failed at `--retries=0`; every §18 journey present incl. pair, reconnect, switch, input/resize, create ws/tab, split, prompt, confirm-close, takeover + new C1/H2/H3/M4/M5 |
| Manifest validates against Herdr 0.7.5 | **OK** | `manifest_test.go` |
| Fake-cloudflared named+quick startup/teardown | **OK** | `internal/tunnel`/`internal/daemon` tests |
| Named mode rejects invalid Access; verifies JWT | **OK** | `config.go`, `auth/jwt.go`, `auth/jwks.go` |
| Quick mode requires opt-in + mandatory pairing | **OK** | `config.go`; pairing in `integration/auth.go` |
| **No secret in argv/logs/state/browser storage/snapshots/git** | **OK** | round-1 H1 leak closed (audit uses non-secret `AuditID`); token via file only; `SanitizeForLog` wired |
| Usable at 390px w/ keyboard; screenshot/keyboard/reduced-motion | **OK** | safe-area + visual-viewport handling; screenshot review spec passes |
| README commands match binary/impl | **OK** | unchanged from round 1 (verified OK) |
| GoReleaser darwin amd64/arm64 + checksums + SBOM | **OK** | `.goreleaser.yml`; `release_consistency_test.go` |
| Release workflow: annotated/signed tag, on `main`, tag=manifest=binary, verify-plugin | **OK** | `.github/workflows/release.yml:39-69` (tag-object check, ancestor-of-main, version match, `make verify-plugin`, pinned `goreleaser-action`) |
| Final security/code review finds no high-severity issues | **OK** | no CRITICAL/HIGH open; only LOW observations (F1–F4) remain |

---

## 4. Conclusion

Round-1's one CRITICAL, three HIGH, and seven MEDIUM findings are all closed with real wiring and tests, and the previously-flaky takeover journey is now deterministic under `--retries=0`. No new CRITICAL/HIGH/MEDIUM issues were introduced by the remediation. What remains are four LOW items (F1 new paste-newline behavior, F2 token trim, F3 CI retry policy, F4 dead-code duplication) plus a handful of carried-over round-1 LOW observations — none of which block the Definition of Done.

*No files other than this report were created or modified during the audit.*
