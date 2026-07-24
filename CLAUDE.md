# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A public Herdr v0.7.5 plugin (`matheus3301.phone`) that exposes **one** local Herdr
session to a phone: a loopback-only Go relay + supervised `cloudflared`, with a
React/TypeScript PWA (Tailwind v4, shadcn/ui, xterm.js) embedded into the Go binary.

This grants **remote shell-equivalent access**. Its security bar is an SSH client's,
not a dashboard's. `SPEC.md` is the implementation contract; `SECURITY.md` is the
threat model; `docs/reviews/*-round2.md` records what was audited and which residuals
were accepted — but those are point-in-time snapshots, so verify a finding still
applies to the tree before acting on it.

## Toolchain

Exactly Go **1.26.5** and Node **22.23.1**, pinned in `mise.toml` (source of truth).
`internal/app/goversion_test.go` and `release_consistency_test.go` fail if `go.mod`,
`mise.toml`, or the CI workflows drift. Run `mise install` once, then `mise exec -- <cmd>`.

Earlier 1.26 patches carry reachable stdlib vulns — never lower the Go floor to "1.26".

## Commands

```sh
make check          # the full local gate; needs no credentials
make test-e2e       # Playwright mobile journeys
make verify-plugin  # validate the manifest against Herdr in isolated state
```

`make check` = fmt-check, tidy-check, vet, typecheck, lint, test-race, coverage
(**80% floor over `./internal/...`**), test-web, build, sh-syntax, smoke-embed,
smoke-install. `make help` lists every target.

No gate needs Cloudflare, Access, a tunnel token, or a live Herdr session.
Playwright needs Chromium/WebKit installed (`npx playwright install chromium webkit`).
The suite is not offline: targets depending on `web-install` run `npm ci`, and
`make verify-plugin` downloads Herdr by default (see Testing).

Narrower loops:

```sh
go test ./internal/server/ -run TestConcurrentClientsAndMutationsRace   # one Go test
go test -race -count=1 ./internal/state/                                # one package, uncached
cd web && npx vitest run src/lib/store.test.ts                          # one vitest file
cd web && npx playwright test -g "take over"                            # one journey
cd web && npx playwright test --project=pixel-7                         # one device project
cd web && npm run dev                                                   # SPA on the mock relay
```

`govulncheck` runs as its own CI job (pinned `v1.6.0`) and in the release workflow,
but is **not** part of `make check` — run `govulncheck ./cmd/... ./internal/...`
locally before a release-shaped change. `npm audit` is manual only; nothing runs it.

## Architecture

Data flows one way: Herdr socket → typed client → state engine → server → browser.
Mutations flow back through a **typed allowlist only**, never a generic RPC proxy.

| Package | Owns |
|---|---|
| `cmd/herdr-phone` | Thin `main`; calls `app.Main`. |
| `internal/app` | CLI dispatch (`start`/`stop`/`status`/`setup-link`/`doctor`/`serve`), plugin-action names, `doctor`. |
| `internal/integration` | **The production composition root.** `buildStack` wires every subsystem and owns teardown order. Read this first to understand runtime shape. |
| `internal/herdr` | Sole owner of Herdr wire names (protocol 17, schema 1). One conn per request; a separate `Subscriber` for `events.subscribe`. |
| `internal/state` | Poll-as-truth engine: `session.snapshot` hot 1.5s / cold 12s; events are only wakeups. Owns pane lifecycle generations and content hashing. |
| `internal/server` | HTTP/WS routes, the single security middleware, mutation allowlist, confirmation nonces, idempotency, audit. |
| `internal/terminal` | Bridges `herdr terminal session control` NDJSON ↔ WebSocket. |
| `internal/auth` | Pairing, in-memory sessions, CSRF, Access JWT + JWKS cache. |
| `internal/tunnel` `internal/daemon` | `cloudflared` supervision/orphan reconciliation; lifecycle, control socket, runtime state, state lock. |
| `internal/config` | Strict TOML (unknown keys are errors), path/permission verification. |
| `internal/security` | ANSI output filtering and log/secret redaction. Both are live: `NewANSIFilter` via `integration/terminalfilter.go`, `SanitizeForLog` in `server/audit.go` and `tunnel/supervisor.go`. |
| `internal/webui` | Embeds the built PWA; selects generated vs. fallback tree. |
| `web/` | The PWA. `web/mock/relay.ts` is a Vite plugin emitting the exact backend wire shapes for dev/preview/Playwright. |

Key invariants that span files:

- **Snapshot is truth, events are wakeups.** A missed event costs one poll interval,
  never correctness. Don't add event-driven state mutation.
- **Lifecycle generations are mandatory**, not opt-in. Every pane mutation and terminal
  attach carries `expected_generation`; absent or `0` is rejected. See
  `internal/server/mutations.go` and `terminalroute.go`.
- **`internal/server/mutations.go` `operations` is the complete allowlist.** Anything
  absent is rejected before any Herdr call. `altResourceField` exists because some
  dispatchers prefer `target`/`workspace_id` over the canonical id — a divergent alt
  is rejected so the guard and the dispatch key on the same identifier.
- The **route table** (`internal/server/routes.go`) is the security contract; every
  route goes through `wrap` in a fixed order (Host → Access JWT → session → Origin →
  `CrossOriginProtection` → CSRF → content-type/body/rate/deadline). A test asserts
  route-wide coverage. WS handlers set `InsecureSkipVerify: true` *only* because `wrap`
  already enforced the exact Origin allowlist before upgrade.
- Frontend state is `useSyncExternalStore` by design. Do not add a data library.

## Security invariants — do not weaken

- Bind `127.0.0.1` only. Never `0.0.0.0`, never a LAN address.
- Named mode requires a valid Access JWT on **every** request and WS handshake, plus
  single-use app pairing. Quick mode is opt-in and still requires pairing. Read only
  `Cf-Access-Jwt-Assertion`; never trust convenience identity headers.
- Never log or persist bearer secrets or terminal content. The audit trail records the
  non-secret `Session.AuditID` (never the cookie value) and terminal input only as a
  byte count and category.
- No generic Herdr RPC proxy; no browser-supplied method names or raw params.
- Destructive ops need a single-use, 30s confirmation nonce bound to operation,
  resource, generation, session, and params. Terminal takeover too.
- **Never invoke a shell.** `exec.CommandContext` + argv arrays + nonzero `WaitDelay`
  + process groups, for `cloudflared`, terminal controllers, and secret commands alike.
- Tunnel tokens never in argv — `--token-file`/`TUNNEL_TOKEN_FILE` or `token_command`,
  written only to a mode-0600 temp file deleted after `cloudflared` reads it.
- Terminal output is ANSI-filtered (OSC 52/8, title set/report, DCS/APC/PM, device-status
  and answerback queries) before reaching the browser or any log. `internal/security/ansi.go`
  has a fuzz oracle — extend it alongside any filter change.
- Local runtime config (`config.toml`, tokens, state dir) lives outside the repo. Do not
  read, create, or commit it.

## Generated files and the embed

`internal/webui/generated/` is **git-ignored except `.gitkeep`**, and `scripts/embed-web.sh`
syncs `web/dist` into it atomically. `internal/webui/dist/` is a committed placeholder
shell that the package falls back to when no real build is embedded.

- **A clean checkout compiles and tests fine without any frontend build** — Go compiles
  against the committed fallback shell, and the `internal/webui` assertion that real
  assets are embedded skips unless `HERDR_PHONE_REQUIRE_WEB=1`. You do not need
  `make build-web` to run `go test ./...`.
- **Release and production builds do need it.** `make build` runs `make build-web` and
  then gates on `HERDR_PHONE_REQUIRE_WEB=1 go test ./internal/webui`, which fails if only
  the placeholder is embedded. GoReleaser runs `make build-web` in a before-hook.
- At runtime `webui.IsFallback()` reports which tree is live, and `buildStack` **refuses
  to serve** the fallback unless `HERDR_PHONE_DEV=1`.
- After `make build-web`, your working tree has real assets in `generated/` — that is
  expected and ignored, not something to commit. `make clean` (or
  `sh scripts/embed-web.sh --clean`) restores marker-only state.

## Testing

Every test is deterministic and needs no real Cloudflare, Access identity, tunnel token,
Herdr session, or browser: fake Herdr harnesses, a fake `cloudflared` on `PATH`, generated
JWKS, and injected `Clock`/`Dialer` seams. Keep it that way.

Playwright runs the **real production bundle** (`npm run build && npm run preview`) against
`web/mock/relay.ts`. That mock must keep emitting the exact backend wire shapes — when you
change `internal/server`, `internal/state/snapshot.go`, `internal/herdr/models.go`, or
`internal/terminal/protocol.go`, update the mock in the same change or the journeys silently
test a fiction. It is a single shared in-memory backend, so `workers: 1`, `fullyParallel:
false`, and `retries: 0` are deliberate — required journeys must pass without retries.

`make verify-plugin` downloads pinned official Herdr v0.7.5 (SHA-256 verified) into throwaway
state; `HERDR_BIN=/path/to/herdr` or `HERDR_USE_PATH=1` overrides it. It never touches an
active Herdr session.

## Release

`v0.1.0` is published. Cut a release by pushing an **annotated** `vX.Y.Z` tag on a commit
already on `main`; the version must match `herdr-plugin.toml` and `internal/buildinfo`
(the workflow verifies tag object, `main` ancestry, and version agreement). GoReleaser
publishes Darwin arm64/amd64 `.tar.gz`, `checksums.txt`, Syft SBOMs, and a keyless
build-provenance attestation. CI never bumps versions or pushes commits.

`.goreleaser.yml`'s archive `name_template` must stay in sync with `scripts/build.sh` —
`TestGoreleaserArchiveTemplateMatchesBuildScript` enforces it.

## Pitfalls

- **Middleware, CSP, Origin, and body limits live in `internal/server`, not
  `internal/security`.** The duplicate implementations that once sat in
  `internal/security` (`Middleware`/`Wrap`, `headers.BuildCSP`, `origin`, `BoundedReader`)
  have been deleted; that package is now only `ansi.go` and `redact.go`. The
  `docs/reviews/*-round2.md` findings about dead security code (backend R4, security R1)
  are resolved — do not act on them.
- README's architecture diagram predates `internal/integration` and `internal/webui`;
  the table above is current.
- `SPEC.md` §5's file tree omits `scripts/embed-web.sh` and several packages. The spec is the
  behavioral contract, not a current inventory.
- Accepted residuals are documented in `docs/reviews/*-round2.md` — check there before
  "fixing" something that was a deliberate call (e.g. control-socket authz via filesystem
  perms, `RemoteAddr` rate-limit keying behind cloudflared).
- Non-goals are explicit and enforced: no multi-session aggregation, no arbitrary socket
  methods, no `server.stop`/plugin administration from the browser, no raw file reads, no
  auto-install of `cloudflared`, no blind one-tap approvals. See `SPEC.md` §21.
