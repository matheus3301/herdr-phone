# Research: `herdr-shortcut` as scaffolding for a public Herdr remote-access plugin

Date: 2026-07-23
Source repo inspected: `/Users/matheus/www/herdr-shortcut` (git history: 4 commits —
`73c78f6` spec, `271acb0` feat, `fa6c220`/`81f579c` CI/toolchain; no follow-on churn).
Target: `/Users/matheus/www/herdr-phone` (currently only `.claude/settings.local.json`).

This report extracts what to reuse, what to drop, and what contract risks remain
when building a *new, public* Herdr plugin. `herdr-shortcut` is a Go/Bubble-Tea
Herdr **v0.7.5** plugin: a read-only Shortcut task picker that launches a coding
agent in a new Herdr tab. Its domain (Shortcut REST) differs from remote access,
but its **plugin skeleton, credential handling, Herdr CLI adapter, build/release
tooling, and test strategy are directly transferable** and unusually well-hardened.

---

## 1. What `herdr-shortcut` actually is (map)

| Concern | Where |
| --- | --- |
| Plugin manifest | `herdr-plugin.toml` |
| Product/impl contract | `SPEC.md` (28 KB, 21 sections — the model to imitate) |
| Entry point | `cmd/herdr-shortcut/main.go` (thin; delegates to `internal/app`) |
| CLI dispatch + commands | `internal/app/cli.go` (`open`/`tui`/`doctor`/`version`/`help`) |
| App wiring / DI | `internal/app/app.go` |
| Launch orchestration | `internal/app/launch.go`, `internal/app/load.go`, `internal/app/doctor.go` |
| Typed Herdr CLI adapter | `internal/herdr/herdr.go`, `commands.go`, `context.go` |
| Config + validation | `internal/config/config.go`, `paths.go` |
| Token resolution | `internal/config/token.go` |
| Child-env scrubbing | `internal/childenv/childenv.go` |
| HTTP domain client | `internal/shortcut/client.go`, `models.go`, `states.go`, `errors.go` |
| Template engine | `internal/tmpl/tmpl.go` (shared by query/name/label/prompt) |
| Prompt rendering | `internal/prompt/prompt.go` |
| Browser opener | `internal/browser/browser.go` |
| Version single-source | `internal/buildinfo/buildinfo.go` |
| TUI (Bubble Tea) | `internal/tui/*.go` (~4k LOC: `model.go`, `update.go`, `view.go`, `render.go`, hit-testing) |
| Install build script | `scripts/build.sh` |
| Manifest verify script | `scripts/verify-plugin.sh` |
| No-Go fallback smoke | `scripts/smoke-nogo-install.sh` |
| Dev tasks | `Makefile` |
| Release config | `.goreleaser.yml` |
| CI / release | `.github/workflows/ci.yml`, `release.yml` |
| Toolchain pin | `mise.toml`, `go.mod` (Go 1.26) |
| Cross-file consistency tests | `internal/app/manifest_test.go`, `goversion_test.go`, `attribution_test.go` |
| OSS metadata | `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `LICENSE`, `.github/ISSUE_TEMPLATE/`, `dependabot.yml` |

---

## 2. Reusable scaffolding & proven patterns (carry over)

### 2.1 Spec-first, contract-driven repo — `SPEC.md`
`SPEC.md` is the standout artifact. It pins **verified external contracts as of a
dated snapshot** (`SPEC.md:46`), enumerates deliverables (`SPEC.md:117-154`),
defines the CLI surface, config schema, error/redaction rules, testing matrix,
CI/release, and a Definition of Done (`SPEC.md:701-721`). **Recommendation:** author
an equivalent `SPEC.md` for herdr-phone *before* code, re-verifying the Herdr
version's contracts. Keep the "Authoritative External Contracts" + dated section.

### 2.2 Plugin manifest shape — `herdr-plugin.toml`
Proven manifest structure: `id`/`name`/`version`/`min_herdr_version`/`platforms`,
a `[[build]]` running `sh scripts/build.sh`, `[[actions]]` with `contexts`, and a
popup `[[panes]]` (`herdr-plugin.toml:1-37`). Note `min_herdr_version = "0.7.5"`
and platforms `["linux","macos"]` (Windows deliberately excluded, `SPEC.md:175`).
**Reuse the skeleton; change ids/actions/panes for remote-access verbs.**

### 2.3 Typed Herdr CLI adapter — `internal/herdr/`
This is the single most reusable subsystem. Key patterns:
- **`Runner` injection** (`herdr.go:33`) — non-zero exit reported via
  `CommandResult.ExitCode`, not `err`; `err` reserved for start/cancel. Enables
  fully offline tests with fake runners.
- **`HERDR_BIN_PATH` first, `herdr` on PATH only for standalone dev**
  (`herdr.go:44-52`). Mandated by `SPEC.md:80`.
- **JSON envelope + `result.type` discriminator parsing** (`herdr.go:57-140`):
  `run`/`runTyped` validate the type field and bound output.
- **Bounded output** via `cappedBuffer` (1 MiB, `herdr.go:20-21,220-236`); error
  snippets sanitized single-line and capped (`snippet`, `herdr.go:174-189`).
- **Capability discovery, not hard-coding**: `AgentKinds` parses the `kinds:` line
  from bare `herdr agent` (which exits non-zero on stderr), tolerates unrelated
  help lines, and **refuses to fall back to a compiled-in list** on failure
  (`commands.go:277-340`). Mirror this for whatever the remote-access plugin must
  discover (protocols, hosts, transports).
- **Ambiguous-resource reconciliation** (`commands.go:129-200`, `IsAmbiguousTab`):
  a timed-out/lost `tab create` may have created a tab, so partial IDs are returned
  and the caller *resumes* rather than creating a duplicate. `launch.go:88-111` and
  `ensureAgentStarted` (`launch.go:197-229`) implement idempotent retry, name-
  collision re-generation, and liveness reconciliation via `agent list`. **This is
  the exact discipline a remote-access launcher needs** for connect/attach flows
  where a dropped response must not double-create a session.
- **Identity verification** after mutation (`checkIdentity`, `commands.go:261-275`):
  confirm Herdr acted on the resource you asked for.
- **Popup context loss workaround** (`context.go:19-91`): a popup regenerates its
  own plugin context pointing at the popup pane, so the `open` action forwards the
  *original* workspace/cwd via custom `--env` vars that the popup then prefers.
  Any plugin that opens a popup and then acts on the invoker's context needs this.

### 2.4 Credential model — `internal/config/token.go` + `internal/childenv/childenv.go`
Even if the remote-access plugin's secret is an SSH key path or a session token
rather than an API token, reuse the model verbatim:
- Precedence: env var, then a `token_command` **argv array run without a shell**,
  trimming one trailing newline (`token.go:56-104`).
- **No plaintext secret field in TOML** (`config.go:5`, `SPEC.md:255-258`).
- Errors never include command output/secret (`token.go:71-76,94-95`); stdout is
  bounded and **fails closed on overflow** so a truncated prefix can't become a
  wrong secret (`token.go:99-103`).
- `childenv.Sanitized` strips the secret from **every** child process env
  (`childenv.go`), used by the Herdr adapter (`herdr.go:196`), token runner
  (`token.go:91`), and browser opener (`browser.go:99`). For remote access, extend
  the `sensitive` set and also scrub it on the created tab via a `--env NAME=`
  clear, as `TokenScrubEnv` does (`commands.go:119-146`).
- Defense-in-depth **redaction** of the secret from any surfaced body/prompt
  (`client.go:377-401`, `prompt.go:161-166`) — redact on raw bytes *before*
  whitespace normalization (`client.go:383-385`).

### 2.5 Config loading & validation — `internal/config/config.go`, `paths.go`
- Three-tier path precedence: `$HERDR_PLUGIN_CONFIG_DIR` → `$XDG_CONFIG_HOME` →
  `$HOME/.config/...` (`config.go:110-124`, `SPEC.md:214-217`).
- **Pointer-field raw structs** so "unset" is distinguishable from "zero"
  (`config.go:177-198`), defaults applied first (`Default()`, `config.go:89-106`).
- **Reject unknown keys** via `md.Undecoded()` (`config.go:206-213`) — catches typos.
- Field-specific actionable errors; HTTPS-only with a loopback HTTP exception for
  tests (`validateBaseURL`, `config.go:354-379`; `isLoopbackHost`, `paths.go:51-59`).
- `ExpandPath` expands `~`/env vars, **errors on unset vars** instead of silently
  collapsing, and never mutates stored paths (`paths.go:14-46`).

### 2.6 Shared minimal template engine — `internal/tmpl/tmpl.go`
A ~140-line `{key}` engine (no `text/template`, no shell) validated at config time
against an allowed-key set, with `{{`/`}}` escapes and parse errors surfaced as
config errors. One engine serves query, agent-name, tab-label, and prompt
(`config.go:299-326`). **Reuse as-is** for any user-configurable string templates.

### 2.7 Output-sanitization discipline — `internal/prompt/prompt.go`
If the remote-access plugin ever forwards user/remote data into a terminal or an
agent prompt, reuse the sanitization: strip control/format/bidi/line-separator
runes (`isUnsafeRune`, `prompt.go:171-215`), flatten single-line fields, cap each
field and the total with a truncation marker (`prompt.go:22-31,219-234`), pass as a
**single argv value — never a shell, temp file, or env var** (`SPEC.md:470-471`).

### 2.8 Subprocess-launch safety — `internal/browser/browser.go`
Strict allowlist validation before spawning: https-only, no userinfo, no non-
default port, no path traversal, anchored path regex, host-suffix check
(`browser.go:64-94`), `exec.CommandContext` with sanitized env, no shell. The
template for any "open external thing" verb (e.g. opening a remote URL/host).

### 2.9 Build / install / release tooling
- **`scripts/build.sh`**: build from source when Go ≥1.26 present (with
  `GOTOOLCHAIN=local` to avoid an old Go auto-downloading a toolchain,
  `build.sh:41-53`), else download the exact release archive and **verify SHA-256
  before install** (`build.sh:88-108`); `set -eu`, temp dir + cleanup traps, fails
  closed, never `curl|sh`/`eval` (`SPEC.md:527-541`). Release base URL overridable
  only for the local smoke test, checksum still enforced (`build.sh:72-76`).
- **`scripts/verify-plugin.sh`**: validates the manifest against a **pinned,
  checksum-verified official Herdr v0.7.5** in fully isolated `HOME`/XDG state,
  unsetting any live session/socket/token (`verify-plugin.sh:26-38,60-69`), then
  `plugin link` + `plugin list --json` and asserts the plugin/action/pane appear.
- **`scripts/smoke-nogo-install.sh`**: hides Go from `PATH`, generates fake release
  assets served over `file://`, and asserts the download+checksum+install path works.
- **`.goreleaser.yml`**: static `CGO_ENABLED=0 -trimpath -s -w` binaries for
  darwin/linux × amd64/arm64, `.tar.gz` + `checksums.txt` + SBOM; archive
  `name_template` **must stay in sync with build.sh** (enforced by a test).
- **`Makefile`**: `make check` = fmt-check, tidy-check, vet, test-race, coverage
  (80% gate), build, sh-syntax, smoke-nogo — every gate that needs no network/creds.

### 2.10 CI / release — least privilege & version gating
- `ci.yml`: `permissions: contents: read`, concurrency cancel-in-progress, Ubuntu+
  macOS matrix, pinned action majors, `setup-go` cache, race tests (cached + a
  separate uncached `-count=1` job), `govulncheck`, `verify-plugin`, goreleaser
  `check`, coverage artifact (no third-party token).
- `release.yml`: split `verify` (read-only) and `release` (`contents: write`) jobs;
  requires an **annotated/signed tag on `main`**, asserts tag == manifest ==
  binary version (`release.yml:27-53`), re-runs all gates, scans built binaries
  with `govulncheck -mode=binary`, never pushes commits from CI.

### 2.11 Single-source version + drift-guard tests — the highest-leverage pattern
`buildinfo.Version` is the one source of truth; `internal/app/manifest_test.go`
asserts manifest ⇆ `buildinfo` ⇆ action/pane ids ⇆ build command ⇆ goreleaser
template ⇆ `config.example.toml` ⇆ `prompt.DefaultTemplate` all agree.
`goversion_test.go` fails if `mise.toml`/`go.mod`/scripts/workflows/docs disagree
on Go 1.26. `attribution_test.go` walks repo files to lock author name.
**Recommendation:** replicate this "consistency test" habit — it is what keeps a
public plugin's manifest, docs, and binary from silently drifting.

### 2.12 Testability architecture (carry the philosophy)
Everything is injected: `Environment` struct with `Getenv`/`Args`/writers plus
optional `HTTPClient`/`HerdrRunner`/`TokenRunner`/`Now`/`Browser`/`DirValidator`
(`app.go:31-48`). Bubble Tea commands, `httptest`, injectable clocks/sleepers/
runners give deterministic tests with **no real network, Herdr, or account**
(`SPEC.md:544-572`). The e2e table test drives a fake Shortcut client + fake Herdr
runner through the full flow for every discovered kind (`internal/app/e2e_test.go`).

---

## 3. Patterns that should NOT carry over

1. **Domain-specific code is not scaffolding.** `internal/shortcut/*`,
   `internal/prompt/*`, the story-picker `internal/tui/*`, and the Shortcut query
   template are Shortcut-specific. Reuse their *shapes* (HTTP hardening, template
   engine, sanitization), not the code. In particular do **not** keep the Shortcut
   base URL, `Shortcut-Token` header, `owner:{member}` query, or story models.
2. **`browser.go`'s hard-wired `shortcut.com` allowlist** (`browser.go:22,76`) is
   correct for Shortcut and wrong for a general remote-access tool. Keep the
   validate-before-spawn structure; redefine the allowlist for the new domain.
3. **Read-only, no-write posture** (`SPEC.md:41-43,692-700`) is a Shortcut design
   choice. A remote-access plugin is inherently stateful/interactive; its non-goals
   and threat model differ and must be re-derived, not inherited.
4. **The ~4k-LOC bespoke Bubble Tea TUI with custom hit-testing** (`internal/tui/`,
   `view.go` 823 LOC, `update.go` 767 LOC) is heavy. Only adopt it if the remote-
   access plugin genuinely needs an interactive picker; a headless/argv command
   (like `open`) is far cheaper. Do not port hit-testing speculatively.
5. **Single-target toolchain pin (Go 1.26 only)** is defensible for a solo project
   but brittle for a public plugin expecting outside contributors on older Go.
   Consider supporting a small range unless the strict pin is intentional; if kept,
   keep the `goversion_test.go` guard.
6. **`min_herdr_version = "0.7.5"` and every v0.7.5 CLI assumption are frozen in
   time.** Do not copy the version numbers, pinned Herdr asset checksums
   (`verify-plugin.sh:62-66`), or the `kinds:` list (`SPEC.md:78`) — re-verify
   against the Herdr version herdr-phone targets.
7. **`Windows` exclusion + `open`/`xdg-open` only** (`browser.go:42-49`) — re-decide
   platform support for remote access (Windows clients are plausible here).

---

## 4. Unresolved contract risks for the remote-access plugin

These are the assumptions that would break a new plugin and must be re-verified,
not copied:

1. **Herdr CLI/JSON contract is version-locked and unverified here.** The adapter
   hard-codes `result.type` discriminators (`tab_created`, `agent_started`,
   `agent_prompted`, `workspace_list`, `plugin_pane_opened`/`ok`), flag names, and
   the "popup returns bare `ok`" quirk (`commands.go:54-93`). Remote-access verbs
   (attach/connect/forward/detach) likely use *different* commands and envelopes —
   **confirm each against the live Herdr version's `socket-api`/`cli-reference`
   docs and real output** before assuming this adapter's shapes transfer.
2. **`agent start --kind`/`agent prompt` is the wrong surface for remote access.**
   The whole launch algorithm (`SPEC.md:473-514`, `launch.go`) targets coding-agent
   harnesses. A remote-access plugin's core action is undefined here — the biggest
   open contract question is *which Herdr primitive* creates/attaches a remote
   session (a pane running an `ssh`-like command? `pane run`, which this repo
   explicitly forbids for agents at `SPEC.md:503`? a new remote API?).
3. **"Homebrew bottle reports 0.7.5 but omits `plugin`"** (`SPEC.md:656-659`,
   `PluginCommandAvailable` at `commands.go:378-381`, guarded in `verify-plugin.sh`
   and `doctor`). This foot-gun is version-specific; confirm whether the target
   Herdr version still ships broken bottles and keep the `doctor` capability probe.
4. **Ambiguous-mutation semantics assume tab/agent idempotency.** The reconcile-
   don't-duplicate logic (`IsAmbiguousTab`, `agentLiveOnPane`) depends on `agent
   list` exposing `interactive_ready`/`launch_pending` lifecycle flags
   (`commands.go:23-33`). Remote sessions need an analogous liveness signal; if the
   target Herdr's session-list lacks it, the safe-retry guarantee **cannot** be
   reproduced and must be redesigned.
5. **Pinned Herdr download checksums will rot** (`verify-plugin.sh:62-66`). New OS/
   arch or a new Herdr release requires refreshed pins; a public plugin also needs
   an `HERDR_USE_PATH`/`HERDR_BIN` escape hatch (already present) documented.
6. **Secret model may not fit remote access.** The token model assumes a single
   short-lived string. SSH keys, agent-forwarding, known-hosts, and multi-host
   credentials are a materially larger threat surface — the `childenv` scrub list
   and redaction are necessary but **not sufficient**; a fresh threat model is
   required (host-key verification, no key material in argv/logs/panes).
7. **80% coverage + "no network in tests" is only achievable with a fake transport.**
   For remote access, a fake SSH/transport server (analogous to the fake Herdr
   runner + `httptest`) must exist or the test discipline collapses. Budget for it.
8. **Naming/label templates cap at 32 chars & `[a-z][a-z0-9_-]{0,31}`**
   (`commands.go:12-13,405-453`). If remote sessions are named by host/user, the
   sanitizer/uniqueness scheme transfers but the length ceiling must be re-checked
   against the target Herdr.

---

## 5. Concrete recommendations (ordered)

1. **Write `herdr-phone/SPEC.md` first**, modeled on `SPEC.md`, with a dated
   "Authoritative External Contracts" section re-verified against the target Herdr
   version and the remote-access transport. Resolve risk #2 (core Herdr primitive)
   there before any code.
2. **Copy, then adapt, these files near-verbatim:** `internal/herdr/*` (adapter),
   `internal/config/token.go` + `internal/childenv/childenv.go` (creds),
   `internal/tmpl/tmpl.go` (templates), `internal/buildinfo/buildinfo.go`,
   `scripts/build.sh`, `verify-plugin.sh`, `smoke-nogo-install.sh`, `Makefile`,
   `.goreleaser.yml`, `.github/workflows/*`, `dependabot.yml`, `mise.toml`, and the
   consistency tests in `internal/app/manifest_test.go` + `goversion_test.go`.
3. **Reuse the `Environment` DI + fake-runner test architecture** (`app.go`,
   `e2e_test.go`) and stand up a fake remote transport for hermetic tests (risk #7).
4. **Re-derive the manifest, threat model, allowlists, and non-goals** for remote
   access; do not inherit Shortcut's read-only posture or `shortcut.com` allowlist.
5. **Only port the Bubble Tea TUI if an interactive picker is truly needed**;
   otherwise prefer argv commands + `doctor`.
6. **Keep the security hygiene as hard requirements:** no secrets in argv/env/logs/
   prompts, sanitized child env, redaction, checksum-verified downloads, least-
   privilege CI, `govulncheck` on source and built binaries.

---

## Appendix: notable exact references

- Idempotent launch + ambiguous-tab resume: `internal/app/launch.go:53-229`,
  `internal/herdr/commands.go:129-200`.
- Capability discovery (no fallback): `internal/herdr/commands.go:277-340`,
  `internal/app/cli.go:137-152`.
- Popup context forwarding: `internal/herdr/context.go:19-91`, `cli.go:104-126`.
- Token precedence + fail-closed overflow: `internal/config/token.go:56-104`.
- HTTP hardening (same-origin redirect block, bounded body, retry allowlist,
  Retry-After overflow cap): `internal/shortcut/client.go:83-141,261-271,294-464`.
- Prompt sanitization + caps: `internal/prompt/prompt.go:100-234`.
- Consistency tests: `internal/app/manifest_test.go`, `goversion_test.go`,
  `attribution_test.go`.
- Install/verify/smoke scripts: `scripts/build.sh`, `verify-plugin.sh`,
  `smoke-nogo-install.sh`.
- Release gating: `.github/workflows/release.yml:27-84`.
- Definition of Done template: `SPEC.md:701-721`.
</content>
</invoke>
