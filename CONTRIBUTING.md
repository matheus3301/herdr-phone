# Contributing to herdr-phone

Thanks for your interest in improving `herdr-phone`! This document explains how
to set up a development environment and the quality bar for changes.

`herdr-phone` is a Go 1.26 relay with a React + TypeScript PWA embedded into the
Go binary. It exposes a real interactive terminal for a Herdr session to a phone
through a Cloudflare tunnel. Because it grants remote shell-equivalent access,
security review is part of every change — please read
[SECURITY.md](SECURITY.md) and the specification in [SPEC.md](SPEC.md) first.

## Prerequisites

- **Go 1.26.5 or newer** — the supported backend toolchain (the 1.26 series is
  pinned via `mise.toml`; run `mise install`, or prefix commands with `mise exec
  --`). CI and releases pin the exact patch **1.26.5**, and building from source
  requires **1.26.5+**, because earlier 1.26 patch releases contain reachable
  standard-library vulnerabilities.
- **Node.js 22.23.1** — pinned in `mise.toml` and matching CI, for the embedded
  frontend, with the committed `web/package-lock.json` for reproducible installs.
  Vite 7 requires Node ≥ 22.12, so a source build accepts Node 22.12+ on the 22
  line or 24+ (Node 20/21/23 and 22.x below 22.12 are rejected).
- A POSIX shell (`sh`) for the build and verification scripts.
- Optionally, [Herdr](https://herdr.dev) v0.7.5+ for end-to-end verification.
  `make verify-plugin` can download an official Herdr binary if one is not
  installed.
- Optionally, `cloudflared` for a real tunnel. It is **never** auto-installed;
  `herdr-phone doctor` prints exact Homebrew/manual guidance.

## Getting started

```sh
git clone https://github.com/matheus3301/herdr-phone
cd herdr-phone
mise install        # provisions the pinned Go and Node toolchains
make check
```

`make check` runs every local quality gate that does not require network access
or credentials: formatting, `go mod tidy` verification, `go vet`, TypeScript
typecheck, frontend lint, Go race tests, coverage, frontend unit/component
tests, a frontend build, the embedded Go build, shell syntax, and the offline
install-fallback smoke test.

## Developer commands

`make help` lists them all.

```sh
make fmt          # format Go sources
make lint         # go vet + frontend lint
make typecheck    # TypeScript typecheck (tsc --noEmit)
make test         # go test ./...
make test-race    # go test -race ./...
make test-web     # frontend unit and component tests
make test-e2e     # Playwright mobile journeys (Chromium Pixel 7, WebKit iPhone 15)
make coverage     # write coverage.txt and enforce the Go coverage threshold
make build-web    # locked frontend install + build into web/dist
make build        # build ./bin/herdr-phone with the frontend embedded
make verify-plugin
make check        # the full local gate
make clean        # remove build, coverage, and frontend artifacts
```

The binary embeds `web/dist`, so `make build` runs `make build-web` first. The
frontend is built from the committed lockfile with `npm ci`; do not use `npm
install` in CI or release paths.

## Quality bar

- Keep the code small and idiomatic on both sides. Do not introduce an abstract
  framework, a generic repository layer, or a heavy frontend data library
  (`useSyncExternalStore` is the intended store primitive).
- The config, auth (pairing / Access JWT), Herdr adapter, state engine, tunnel,
  terminal-bridge, and server boundaries must stay separately testable. One
  typed package owns Herdr wire names; the frontend uses one typed API module.
- No panics from user input, Herdr data, terminal dimensions, missing
  environment, or malformed subprocess output.
- **Never invoke a shell** for credentials, Herdr commands, `cloudflared`, or
  config expansion. Use `exec.CommandContext`, argv arrays, `WaitDelay`, and
  process groups.
- **Never weaken the security posture:** loopback-only origin, JWT validation on
  every request in named mode, mandatory pairing in all modes, strict Origin and
  CSRF checks, a strict CSP with no runtime CDN, ANSI escape filtering, and
  confirmation nonces on destructive actions.
- Never place a secret in the repository, fixtures, logs, the audit trail,
  browser storage, snapshots, or CI configuration. Tests must not require a real
  Cloudflare account, Access identity, tunnel token, Herdr session, or browser.
- No TODO/FIXME placeholders, disabled tests, fake paths, or documentation for
  behavior that does not exist.
- New behavior needs deterministic tests. Go statement coverage must stay at or
  above the enforced threshold, and the frontend must stay within its compressed
  bundle budget.

## Testing expectations

- Go tests are deterministic, parallel where safe, and use fakes for Herdr, a
  fake `cloudflared` on `PATH`, `httptest`, injected clocks, and generated JWKS —
  never a real network, account, or credentials.
- Frontend unit/component tests cover stores, reconnect, modifiers, mutation
  confirmation, keyboard/safe-area layout, API decoding, and terminal lifecycle.
- Playwright mobile journeys run on Chromium Pixel 7 and WebKit iPhone 15 sizes.
- See [SPEC.md](SPEC.md) §18 for the full required test matrix.

## Releasing

Releases are cut by pushing a `vX.Y.Z` tag that must be an **annotated or signed
tag object** (the release workflow rejects lightweight tags):

```sh
git tag -a v0.1.0 -m "herdr-phone v0.1.0"   # or: git tag -s v0.1.0 -m ...
git push origin v0.1.0
```

The tag version must equal the version in `herdr-plugin.toml` and the binary's
build info; the workflow verifies this, runs the full gates, `govulncheck`
(source and built binaries), plugin verification, and `goreleaser check`, then
publishes macOS `amd64`/`arm64` archives, `checksums.txt`, and an SBOM. CI never
bumps versions or pushes commits.

## Commit messages

We use [Conventional Commits](https://www.conventionalcommits.org/): `feat:`,
`fix:`, `docs:`, `test:`, `refactor:`, `chore:`, `ci:`.

## Pull requests

1. Fork and branch from `main`.
2. Make focused changes with tests.
3. Run `make check` and ensure it passes.
4. Open a PR using the pull-request template and describe the change and its
   verification, including any impact on the security posture.

By contributing you agree that your contributions are licensed under the
project's [MIT License](LICENSE).
