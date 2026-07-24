# Herdr Remote-Access Plugin — Threat Model & Secure-by-Default Architecture

> **Status:** Research / design (no code). macOS-first.
> **Component under analysis:** A Herdr remote-access plugin, written in **Go 1.26**, that serves an **embedded React UI**, brokers **full terminal input** and **Herdr control** over WebSockets, and is exposed to the internet through **cloudflared** (Cloudflare Tunnel), using **Cloudflare Access** for edge identity.
> **Scope of this document:** the trust boundaries, the attacker capabilities, and the controls that must be true *by default* for this design to be safe. Every non-obvious control is tied to official Cloudflare or Go guidance (see [Citations](#citations)).

---

## 1. System overview and trust boundaries

```
┌────────────┐  wss/https   ┌──────────────────┐  outbound-only   ┌───────────────────────────┐
│  Browser   │─────────────▶│ Cloudflare edge  │◀────────────────│ cloudflared (subprocess)  │
│ React UI   │              │  + Access (IdP)  │   tunnel (mTLS)  │        on macOS host       │
└────────────┘              └──────────────────┘                  │            │              │
      ▲                             │ injects Cf-Access-Jwt-       │            ▼              │
      │ CSP / SameSite              │ Assertion (signed JWT)       │   Go plugin HTTP server   │
      │ cookies                     ▼                              │  (127.0.0.1:PORT)         │
      │                    ┌──────────────────┐                    │            │              │
      └────────────────────│  Go plugin serves│                    │            ▼              │
                           │  React + WS + API│                    │   PTY / Herdr control     │
                           └──────────────────┘                    └───────────────────────────┘
```

### 1.1 Trust boundaries (where controls must live)

| # | Boundary | What crosses it | Who can be hostile |
|---|----------|-----------------|--------------------|
| B1 | Browser ↔ Cloudflare edge | HTTP/WS + Access session cookie | Remote attacker, malicious web page (CSRF/CSWSH), stolen device |
| B2 | Cloudflare edge ↔ origin (via tunnel) | Requests + `Cf-Access-Jwt-Assertion` header | Anyone who can reach the origin *without* going through the edge |
| B3 | Go plugin ↔ cloudflared subprocess | Tunnel token / credentials, stdout/stderr, process lifecycle | Local user, other local processes, compromised dependency |
| B4 | Go plugin ↔ PTY / Herdr control | Terminal bytes (both directions), control commands | The remote operator (assumed authorized) **and** any process whose output is relayed (untrusted) |
| B5 | Build/CI ↔ published binary | Source, modules, embedded assets | Supply-chain attacker |

**The single most important assertion in this document:** Cloudflare Access is an **edge** control. It is only as strong as boundary **B2** is closed. If an attacker can deliver a request to the origin without transiting the edge, every "identity provided at the edge" guarantee evaporates. The design must therefore treat forwarded identity as *unverified until the origin cryptographically validates it*, and must make direct origin reachability impossible by default.

---

## 2. Assets, actors, and attacker capabilities

### 2.1 Assets
- **A1 — Shell/PTY on the macOS host.** Full command execution as the running user. Highest value.
- **A2 — Herdr control plane** (panes, tabs, workspaces, agent commands). Lateral control over other running agents.
- **A3 — Secrets:** Cloudflare Tunnel token / credentials JSON, `cert.pem`, Access service-token secret, the team JWKS/AUD config, any TLS material.
- **A4 — The operator's *real* local terminal** (the one running `cloudflared`/the plugin, and the one reading logs). Target of terminal-escape injection.
- **A5 — Session identity / cookies** (`CF_Authorization`) and any app-level session state.
- **A6 — Integrity of the shipped binary and its dependency tree.**

### 2.2 Actors
- **Authorized operator** (expected): authenticates via Access IdP or service token, drives the terminal/Herdr.
- **Remote unauthenticated attacker**: reaches the public hostname; may attempt Access bypass, replay, phishing.
- **Malicious web origin**: a page the operator visits in the same browser, attempting CSRF / cross-site WebSocket hijacking against the plugin.
- **Local co-resident process / user** on the macOS host: attempts to reach `127.0.0.1:PORT` directly, steal secrets from disk, or forge identity headers at B2.
- **Untrusted output producer**: a coding agent, a `git log`, a build tool whose bytes flow through the PTY (A4/B4) and can carry hostile escape sequences.
- **Supply-chain attacker**: compromises a Go module, the build, or an embedded asset (B5).

### 2.3 Assumed capabilities
The remote attacker can send arbitrary HTTP/WS to the public hostname and can host a malicious page. The local attacker can open sockets to loopback and read world-readable files. We do **not** assume the attacker has root or has already achieved code execution on the host (that is the outcome we are preventing), except in the supply-chain section where we assume a hostile dependency.

---

## 3. Threat analysis and controls, by area

Each subsection states the threat, the concrete failure, and the secure-by-default control with citations.

### 3.1 Cloudflare Access — forwarded-identity trust (B1/B2)

**How identity is delivered.** When a request transits Access, Cloudflare adds an application token as the `Cf-Access-Jwt-Assertion` request header, and browsers also carry it as the `CF_Authorization` cookie. Cloudflare explicitly recommends validating the **header** rather than the cookie, since the cookie is not guaranteed to be passed [CF-AppToken]. The JWT is RS256-signed; its payload carries the claims the origin must check: `aud` (the per-application **AUD tag**), `email` (IdP-verified user, or `common_name` for service tokens), `iss` (`https://<team-name>.cloudflareaccess.com`), and `exp`/`iat`/`nbf` [CF-AppToken].

**Threat T-ACC-1 — Blind header trust / identity spoofing.** The plugin reads `Cf-Access-Jwt-Assertion` (or the convenience header `Cf-Access-Authenticated-User-Email`) and trusts its contents without verifying the signature. Any party that can reach the origin at B2 forges the header and is authenticated as any user.

**Control.** The origin **must** cryptographically validate the JWT itself. Cloudflare's guidance is explicit: "You should validate the token with your public key to ensure that the request came from Access and not a malicious third party" [CF-ValidateJWT]. Validation must:
1. Fetch signing keys from the team JWKS endpoint `https://<team-name>.cloudflareaccess.com/cdn-cgi/access/certs`, and **match the `kid`** in the JWT header to a cert in `public_certs` — Cloudflare warns *not* to read `public_cert` directly (stale-cache risk) and *not* to hard-code the key [CF-ValidateJWT].
2. Verify the signature with `RS256` only (reject `alg: none` and algorithm-confusion).
3. Enforce `aud` == the application's AUD tag, `iss` == the team domain, and `exp`/`nbf`/`iat` freshness [CF-AppToken][CF-ValidateJWT].
4. Derive the operator's identity from the **verified `email`/`common_name` claim inside the JWT**, never from the unsigned `Cf-Access-Authenticated-User-Email` header, which carries no cryptographic proof of origin [CF-AccessWorkers].

**Threat T-ACC-2 — Local bypass of the edge (B2 open).** cloudflared, the plugin, or a misconfigured firewall leaves the origin reachable other than through the tunnel (loopback open to other local users, LAN binding, a second ingress). The attacker skips the edge entirely and either (a) forges identity headers, or (b) relies on the plugin having no origin-side check because "Access already did it."

**Control (defense in depth — both layers required):**
- *Network layer:* the origin must not be publicly routable. Cloudflare Tunnel is the recommended mechanism precisely because it "connects your resources to Cloudflare without a publicly routable IP address, by creating an outbound-only connection," letting the host firewall "allow only outbound connections and block all inbound traffic" [CF-PreventExternal]. Bind the Go server to `127.0.0.1` only, on a random high port, and — because loopback is shared by all local users on macOS — treat loopback as **hostile** (see T-ACC-2 network note below).
- *Application layer:* JWT validation from T-ACC-1 runs on **every** request regardless of source, so even a request that reaches loopback directly is rejected without a valid Access JWT. (Optionally, cloudflared's own `originRequest.access` can be configured so the connector validates the Access JWT before proxying [CF-OriginParams], but this does not replace origin-side validation — it adds a second gate.)

> **macOS loopback note:** binding to `127.0.0.1` keeps the port off the network, but any local user/process can still connect to loopback. Loopback binding is necessary, not sufficient. The application-layer JWT check (plus, for the terminal/control endpoints, a per-session bearer secret established only after JWT validation) is what actually closes B2 against a co-resident local attacker.

**Threat T-ACC-3 — Service-token handling.** Non-interactive automation authenticates with a service token (`CF-Access-Client-Id` / `CF-Access-Client-Secret`), which requires a policy with action **Service Auth**, otherwise Access forces an IdP login [CF-ServiceTokens]. Risks: the secret is displayed only once and cannot be recovered; "revoke existing tokens" only ends sessions, so full revocation **requires deleting the token** [CF-ServiceTokens].

**Control.** If service tokens are supported, document that the origin still validates the resulting JWT (service tokens populate `common_name`, empty `email`/`sub` [CF-AppToken]); store the secret only in the OS keychain; and treat rotation as delete-and-recreate, never "revoke sessions." Prefer *not* enabling service tokens unless a concrete automation need exists — every service token is a bearer credential that bypasses interactive MFA.

**Edge-session hardening (informational).** Cloudflare binds `CF_Authorization` to a separate binding cookie so "a stolen `CF_Authorization` cookie [cannot] be reused by an attacker"; the binding cookie is stripped at the edge and never reaches the origin [CF-AuthCookie]. This protects the edge session but is **not** an origin control — it is one more reason origin validation must stand on its own.

### 3.2 Cloudflared tunnels — named vs. quick, and secrets (B2/B3)

**Threat T-TUN-1 — Quick tunnel used beyond testing.** A quick/TryCloudflare tunnel (`cloudflared tunnel --url http://localhost:PORT`) prints a random `trycloudflare.com` hostname and needs no Cloudflare account/DNS setup [CF-TryCloudflare]. Cloudflare states plainly: "Quick Tunnels are intended for testing and development only. For production use, create a remotely-managed tunnel," and "We don't guarantee any SLA or uptime of TryCloudflare" [CF-TryCloudflare]. Critically, a quick tunnel has **no Access application attached by default** — Access is a separate, explicit configuration step applied to named tunnels/hostnames [CF-SelfHostedApp]. A quick tunnel therefore exposes the terminal/Herdr control plane to the entire internet with **no identity at the edge at all**.

**Control (secure-by-default):**
- **Default to named, remotely-managed tunnels** with a Cloudflare Access application in front. Access applications are "deny by default — a user must match an Allow policy before they are granted access" [CF-SelfHostedApp].
- If quick tunnels are offered as a convenience, they must be:
  - **off by default**, gated behind an explicit, loud opt-in;
  - **refused to start unless the origin JWT check is active** (but note a quick tunnel has no Access in front, so there is no JWT — meaning a quick tunnel must instead fall back to a strong app-level auth secret, or simply be blocked for any endpoint that touches A1/A2);
  - surfaced in the UI/CLI with a persistent warning quoting Cloudflare's "testing and development only / no SLA" language [CF-TryCloudflare], the random-public-hostname fact, and the quick-tunnel limits (200 in-flight-request cap → HTTP 429; no Server-Sent Events) [CF-TryCloudflare] which will silently break a long-lived terminal stream.
- Do **not** advertise a quick-tunnel hostname as a durable endpoint; it is disposable and unauthenticated.

> Cloudflare's developer docs do not carry a verbatim "quick tunnels have no access control" warning, and do not label TryCloudflare itself as an abuse vector. The "no access control by default" statement above is a documented *inference* (Access is an explicit add-on for named tunnels), not a Cloudflare quote — flagged here for accuracy.

**Threat T-TUN-2 — Tunnel credential theft (A3/B3).** For remotely-managed tunnels, "a remotely-managed tunnel only requires a token to run" and "anyone with the token can run the tunnel" [CF-TunnelTokens]. For locally-managed tunnels the equivalent secret is the credentials JSON (`<UUID>.json`), which "functions as a token authenticating the tunnel," plus `cert.pem` (account cert used to create/delete tunnels and change DNS) [CF-LocalTunnel][CF-CreateLocalTunnel]. Theft lets an attacker run the tunnel (impersonate the origin) or, with `cert.pem`, manipulate DNS/routing.

**Control.**
- Store the tunnel token / credentials JSON / `cert.pem` in the **macOS Keychain**, not in world-readable files or shell history; if a file must exist, `~/.cloudflared` contents should be `0600`, owned by the operator.
- **Rotate tunnel tokens regularly**; on suspected compromise, "immediately rotate the token, then force-disconnect all existing connections" via the connections API [CF-TunnelTokens].
- Never pass the token as a plaintext CLI argument that lands in `ps`/history; prefer a config/credentials file with locked permissions.

**Threat T-TUN-3 — Weak origin hop / ingress over-exposure.** `originRequest.noTLSVerify: true` "will allow any certificate from the origin to be accepted," disabling TLS verification on the cloudflared→origin hop [CF-OriginParams]. Loose ingress rules route unexpected hostnames/paths to the local service.

**Control.** Keep `noTLSVerify` at its default `false`; because the origin is loopback plaintext behind the tunnel, prefer HTTP-to-loopback with the outbound-only tunnel providing transport security to the edge, and if TLS-to-origin is used, pin it with `caPool`/`originServerName` [CF-OriginParams]. Ingress rules must point only to the intended local port and terminate with a catch-all `http_status:404` rule (Cloudflare requires a concluding catch-all; unspecified hostname/path matches everything) [CF-ConfigFile].

### 3.3 WebSockets, CSRF, and Origin checks (B1)

**Threat T-WS-1 — Cross-Site WebSocket Hijacking (CSWSH).** WebSockets are **not** protected by the Same-Origin Policy or CORS, and browsers automatically include cookies in the WS handshake [OWASP-WS]. A malicious page the operator visits can open a `wss://` connection to the plugin's public hostname; the browser attaches the `CF_Authorization` cookie automatically, the edge lets it through, and the attacker now has a live terminal/Herdr channel — full RCE via CSRF.

**Control.** The server **must validate the `Origin` header on every WS handshake against an explicit allowlist** — "the browser sets this header and malicious JavaScript cannot override it," which is why it is reliable server-side [OWASP-WS]. In Go, if using `gorilla/websocket`, **never** set `CheckOrigin` to `return true` (the common insecure pattern that disables origin checking and exposes CSWSH); implement an explicit allowlist [GO-Gorilla]. Note the safe default only fires when an `Origin` header is present, so also require the per-session bearer secret (below) rather than relying on cookies alone [OWASP-WS]. Use `wss://` only; "never use unencrypted `ws://` connections in production" [OWASP-WS].

**Threat T-WS-2 — CSRF on state-changing HTTP (Herdr control, destructive actions).** A cross-site form/`fetch` triggers a state-changing request; the ambient Access cookie authenticates it.

**Control.** Layer three defenses:
1. **Go 1.25+ built-in `http.CrossOriginProtection`** — `http.NewCrossOriginProtection().Handler(mux)` rejects non-safe cross-origin browser requests using the `Sec-Fetch-Site` header (or Origin-vs-Host comparison), with no tokens/cookies required [GO-Go125][GO-NetHTTP]. Register trusted origins via `AddTrustedOrigin` [GO-NetHTTP]. **Caveat:** GET/HEAD/OPTIONS are always allowed as "safe methods," so the app must "not perform any state changing actions due to requests with safe methods," and requests lacking `Sec-Fetch-Site`/`Origin` are treated as same-origin and allowed [GO-NetHTTP] — hence keep the token defense below for high-value actions.
2. **Origin/Referer verification** as recommended by OWASP (compare source origin to target origin strictly) [OWASP-CSRF].
3. **SameSite cookies + custom-header requirement** for the app's own session/bearer: `SameSite=Strict` (or `Lax`) and `__Host-` prefix as defense-in-depth (SameSite "does not replace a proper CSRF defense"); require a custom header like `X-CSRF-Token` since "requests with custom headers are automatically subject to the same-origin policy" [OWASP-CSRF].

**Session model.** After the Access JWT is validated on the first request, mint a short-lived, high-entropy **per-session bearer secret** and require it on the WS upgrade and on every state-changing API call. This closes the co-resident-local-attacker gap on loopback (§3.1 T-ACC-2) and the cookie-only CSWSH gap (T-WS-1). Compare the secret with `crypto/subtle.ConstantTimeCompare` to avoid timing leaks [GO-Subtle].

### 3.4 CSP and clickjacking (B1)

**Threat T-CSP-1 — XSS in the React UI escalates to terminal RCE.** Any injected script in the SPA context can drive the terminal/Herdr WS. Rendering untrusted subprocess output into the DOM (not just the terminal emulator) is an injection sink.

**Control — strict Content-Security-Policy served with the app:**
- `default-src 'self'`; `object-src 'none'`; `base-uri 'none'`.
- **No `'unsafe-inline'`, no `'unsafe-eval'`** — both "undo" the browser's default blocking of inline/`eval` execution [MDN-CSP]. Use per-response **nonces** for any required inline script (a nonce also neutralizes a stray `unsafe-inline`, since "if a directive contains a nonce and `unsafe-inline`, the browser ignores `unsafe-inline`") [MDN-CSP].
- **`connect-src 'self' wss://<host>`** — `connect-src` governs `WebSocket` connections, and note that "`connect-src 'self'` does not resolve to websocket schemes in all browsers," so the `wss://` origin must be listed explicitly [MDN-ConnectSrc].
- **`frame-ancestors 'none'`** to block framing/clickjacking — note it does **not** fall back to `default-src`, so it must be set explicitly ("a policy that declares `default-src 'none'` still allows the resource to be embedded by anyone") [MDN-FrameAncestors]. Add `X-Frame-Options: DENY` as legacy defense-in-depth [MDN-XFO].

Because the React assets are embedded in the Go binary and served from the same origin, a nonce-based strict CSP is fully achievable with no external CDN dependencies.

### 3.5 Command injection and the PTY (B4)

**Threat T-CMD-1 — Shell metacharacter injection.** The design's whole purpose is to run commands, so "command injection" here is really *authorization* (only the validated operator may drive A1/A2) plus *not accidentally adding a shell*. The real defect would be the plugin constructing shell strings from any input.

**Control.** Use `os/exec` directly: the package "intentionally does not invoke the system shell and does not expand any glob patterns or handle other expansions, pipelines, or redirections" — arguments pass verbatim as argv elements, so classic `;`/`|`/`$()`/backtick injection does not apply [GO-OsExec]. **Never** route input through `sh -c` / `bash -c`, which re-introduces full shell interpretation [GO-OsExec]. The interactive shell the operator drives should be a **PTY spawned as a single known program** (e.g., the user's login shell) whose bytes are relayed, not a per-keystroke `sh -c` call.

**Threat T-CMD-2 — PATH / relative-executable hijack.** A malicious executable in the working directory is picked up instead of the system tool.

**Control.** Rely on Go 1.19+ behavior: `os/exec`/`LookPath` "will not resolve a program using an implicit or explicit path entry relative to the current directory," returning `ErrDot` instead [GO-OsExec]; check with `errors.Is(err, ErrDot)`. This closes the class the Go team described where "if you cd into a directory and run `ls`, you might get a malicious copy from that directory" [GO-PathSecurity]. Resolve program paths absolutely; do not run the plugin from an attacker-writable CWD.

### 3.6 Terminal escape sequences (A4 / B4) — the highest-subtlety risk

**Threat T-ESC-1 — ANSI/OSC escape injection from relayed output.** The terminal channel relays bytes from programs the operator does not control (coding agents, `git log`, `kubectl` logs, build output). Malicious escape sequences in that output can attack **both** the browser terminal emulator (xterm.js) **and** the operator's *real* local terminal (the one running the plugin/cloudflared and reading logs). Documented, exploited techniques include:
- **DECRQSS / answerback echoback → command injection.** Some terminals echo back data sent to them; where echoback is near/fully complete, "an attacker who could find a way to write to the terminal ... could almost certainly execute commands" — demonstrated via a malicious git commit message triggering RCE through `git log` [DGL-ANSI]. CVEs: iTerm2 CVE-2022-45872 (CVSS 9.8), mintty CVE-2022-47583/CVE-2023-39726, SwiftTerm CVE-2022-23465 [DGL-ANSI].
- **OSC 52 clipboard write.** Lets a remote attacker write to the clipboard; a base64 payload with an embedded newline auto-executes on the victim's next paste [DGL-ANSI].
- **OSC 8 hyperlinks** (less CVE-2022-46663) and **window/tab-title set-then-report** injection (ConEmu CVE-2022-46387 / CVE-2023-39150) [DGL-ANSI].

**Control.**
- **Sanitize/allowlist escape sequences before they reach any terminal.** Strip or refuse the dangerous classes on relayed output: answerback/DECRQSS-triggering status queries, title set+report, OSC 52 clipboard, OSC 8 hyperlinks, ReGIS/DECRQSS device-control strings — pass through only the SGR color/styling and cursor-movement sequences the UI needs. Industry guidance is that "output must be escaped and input sanitized" [CyberArk-ANSI][ElReg-ANSI].
- **Never write untrusted subprocess bytes to the operator's real terminal or to log tailing without sanitizing first** — logs are read in a terminal, so log records are an escape-injection sink (see §3.9).
- In xterm.js, disable clipboard-write (OSC 52) and constrain OSC 8 / title handling; render into the browser emulator, not the host terminal, and keep the emulator updated. Modern coding-agent CLIs have already been hit by this exact class (ANSI-escape-injection → RCE) [Ganev-Codex].

### 3.7 Secrets management (A3)

**Threat T-SEC-1 — Secret exposure at rest / in memory / in transit.** Tunnel tokens, service-token secrets, JWKS/AUD config, session bearers leak via world-readable files, process args, logs, or memory scraping.

**Control.**
- **At rest:** macOS Keychain for the tunnel token/credentials/service-token secret; any on-disk fallback `0600`. Never in the embedded binary, never in shell history/`ps`.
- **In comparisons:** use `crypto/subtle.ConstantTimeCompare` for tokens/HMACs, not `==`/`bytes.Equal`, to avoid timing side-channels [GO-Subtle].
- **In memory:** be aware Go's GC may copy/move values, so a plain `[]byte` cannot be reliably zeroed; Go 1.26 ships an *experimental* `runtime/secret` (`GOEXPERIMENT=runtimesecret`) for securely erasing cryptographic temporaries — until it is stable there is no standard guarantee of zeroization [GO-Go126]. Minimize secret lifetime and blast radius rather than relying on wiping.
- **Never log secrets** (see §3.9); redact JWTs/tokens in any diagnostic output.

### 3.8 Subprocess lifecycle — cloudflared and PTYs (B3/B4)

**Threat T-PROC-1 — Orphaned / runaway / unkillable subprocesses.** cloudflared or a spawned shell outlives the plugin, keeps the tunnel open after "shutdown," or wedges on unclosed pipes — leaving A1/A2 reachable with no supervisor.

**Control.**
- Spawn every subprocess with `exec.CommandContext`, whose context "is used to interrupt the process ... if the context becomes done before the command completes" [GO-OsExec]. Tie the context to the plugin's lifecycle and to session teardown.
- Set a non-zero **`WaitDelay`** so `Wait` is bounded against "a child process that fails to exit after the associated Context is canceled, and a child process that exits but leaves its I/O pipes unclosed"; the child "will be terminated using os.Process.Kill" [GO-OsExec].
- For interactive shells, put the child in its own **process group** (`SysProcAttr.Setpgid`) and, via a custom `Cancel`, signal the negative PID so the whole group dies — `Cancel`/`WaitDelay` are the documented hooks for this [GO-OsExec]. This prevents a killed shell from leaving live grandchildren.
- On plugin exit / crash / session end, guarantee cloudflared is torn down (or explicitly detached with a documented, deliberate policy) so a crash does not silently strand a public tunnel.

### 3.9 Logs (A4 / observability)

**Threat T-LOG-1 — Secret leakage and escape injection via logs.** Logs capture JWTs/tokens; logs contain raw relayed terminal bytes; someone tails the log in a terminal → the log becomes an escape-injection vector (§3.6) and a secret-disclosure vector.

**Control.**
- **Redact** tokens, JWTs, service-token secrets, and session bearers before logging.
- **Sanitize control characters / escape sequences** out of any logged terminal or subprocess output — treat log sinks as terminals for the purposes of §3.6 [DGL-ANSI][CyberArk-ANSI].
- Log **security-relevant events** for the destructive-controls audit trail (§3.10): who (verified `email`/`common_name` from the JWT), what action, when, from which Origin/session — enough to reconstruct an incident, with no secret material.
- File permissions `0600`; do not ship logs off-host without the operator's consent.

### 3.10 Destructive controls (A1/A2)

**Threat T-DST-1 — One request from a hijacked session performs an irreversible action** (kill all panes, delete workspace, run a destructive command, rotate/disable auth).

**Control.**
- **Deny-by-default authorization** mirroring Access's model ("deny by default — a user must match an Allow policy" [CF-SelfHostedApp]): destructive actions require the validated JWT **and** the per-session bearer **and** pass CSRF/Origin checks.
- **Confirmation + typed intent** for irreversible actions; never bind them to safe methods (GET/HEAD/OPTIONS) — recall Go's CSRF protection always allows safe methods [GO-NetHTTP].
- **Rate-limit and audit** every destructive action (§3.9). Consider a "read-only by default" session mode that must be explicitly elevated before destructive controls unlock.

### 3.11 Reconnect / replay (B1)

**Threat T-RPL-1 — Replay of a captured handshake or command.** A recorded WS upgrade or state-changing request is replayed after the operator disconnects; or a reconnect re-establishes a session using a stale/leaked token.

**Control.**
- Bind sessions to **short-lived, single-use-per-connection nonces** and rotate the per-session bearer on each reconnect; expire on the JWT's `exp` [CF-AppToken] at the latest.
- Validate the JWT **on every reconnect**, not just the first connect (freshness via `exp`/`nbf`/`iat` [CF-AppToken]).
- Make state-changing operations **idempotent or nonce-guarded** so a replayed command cannot re-execute.
- Do not persist long-lived bearer tokens client-side; rely on the Access session + a fresh app bearer per connection. (Cloudflare's edge binding-cookie mechanism already resists stolen-`CF_Authorization` replay at the edge [CF-AuthCookie], but the origin must still guard its own bearer.)

### 3.12 Supply chain (B5)

**Threat T-SUP-1 — Malicious/vulnerable dependency, tampered module, or hostile embedded asset.** A compromised Go module or React dependency ships in the binary; or a module is swapped after tagging.

**Control.**
- **`govulncheck ./...`** in CI — it "only surfaces vulnerabilities that actually affect you, based on which functions ... are transitively calling vulnerable functions," drawing from the Go vulnerability database at `vuln.go.dev` [GO-Vuln].
- **Module integrity:** rely on `go.sum` + the checksum database (`sum.golang.org`), a tamper-proof Merkle-tree transparent log ensuring "a proxy or origin server can't ... start giving you the wrong code without getting caught" and that even an author "can't move their tags around ... without the change being detected" [GO-ModMirror]. Keep `GOSUMDB=sum.golang.org`; scope `GOPRIVATE` narrowly for any private modules [GO-ModRef].
- **Reproducible, non-mutating builds:** enforce `-mod=readonly` (default in modern Go; set `GOFLAGS=-mod=readonly`) so builds "report an error if go.mod needs to be updated" rather than silently pulling new code [GO-ModRef].
- **Pin and audit React/JS dependencies** (lockfile, integrity hashes) since they are embedded and served same-origin; a compromised front-end dependency runs in the privileged SPA context (§3.4).
- Benefit from Go 1.26 runtime hardening (heap-base ASLR on 64-bit, secure crypto randomness) as defense-in-depth against exploitation of any residual bug [GO-Go126].
- **HTTP server hardening as a baseline:** a bare `http.Server` has all timeouts defaulting to zero (no timeout), exposing Slowloris/resource-exhaustion; set `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, and a sane `MaxHeaderBytes` explicitly [GO-NetHTTP].

---

## 4. Secure-by-default posture (summary of defaults)

| Setting | Insecure but tempting | Secure default (this design) |
|---|---|---|
| Tunnel type | Quick/TryCloudflare | Named, remotely-managed, **behind Access** [CF-SelfHostedApp][CF-TryCloudflare] |
| Origin binding | `0.0.0.0` / LAN | `127.0.0.1` + outbound-only tunnel; loopback treated as hostile [CF-PreventExternal] |
| Identity | Trust `Cf-Access-*` headers | **Validate the Access JWT** (JWKS `kid`, `aud`, `iss`, `exp`) [CF-ValidateJWT][CF-AppToken] |
| Convenience email header | Trust `Cf-Access-Authenticated-User-Email` | Ignore; use verified `email` claim [CF-AccessWorkers] |
| WS origin | `CheckOrigin: return true` | Explicit Origin allowlist + per-session bearer [GO-Gorilla][OWASP-WS] |
| CSRF | Cookie-only | Go `CrossOriginProtection` + Origin/Referer + token + SameSite/`__Host-` [GO-NetHTTP][OWASP-CSRF] |
| CSP | `unsafe-inline`/`unsafe-eval` | `default-src 'self'`, nonces, `frame-ancestors 'none'`, explicit `connect-src wss://` [MDN-CSP][MDN-ConnectSrc][MDN-FrameAncestors] |
| Subprocess | `sh -c "<input>"` | `exec.CommandContext`, argv-only, no shell, `WaitDelay`, process group [GO-OsExec] |
| Terminal output | Raw passthrough | Escape-sequence sanitization both directions [DGL-ANSI] |
| Secrets | Files/args/logs | Keychain, `0600`, `subtle.ConstantTimeCompare`, redacted logs [GO-Subtle] |
| Destructive actions | One unauth'd request | Deny-by-default + confirm + audit + rate-limit, never on safe methods [GO-NetHTTP][CF-SelfHostedApp] |
| Build | `go get` latest, mutable | `govulncheck`, `-mod=readonly`, checksum DB, pinned JS deps [GO-Vuln][GO-ModMirror][GO-ModRef] |

---

## 5. Residual risks and open questions

- **Loopback is shared on macOS.** The app-layer bearer is the real control at B2; if it is ever bypassed (e.g., a local logic bug), a co-resident process gets the terminal. Consider a Unix-domain socket with filesystem permissions instead of a TCP loopback port to further restrict local reach.
- **`runtime/secret` is experimental** in Go 1.26 — no standard-library guarantee of secret zeroization yet [GO-Go126]. Treat in-memory secrets as potentially recoverable.
- **Quick-tunnel convenience vs. safety** is a genuine tension: it has no edge identity by default and is unsuitable for A1/A2 exposure. Recommend blocking destructive/terminal endpoints entirely under a quick tunnel.
- **Cloudflare doc gaps:** there is no verbatim Cloudflare warning that quick tunnels lack access control, and no developer-docs framing of TryCloudflare as an abuse vector; those points are documented inferences, not quotes (flagged in §3.2).
- **xterm.js escape coverage** must be verified against the specific version bundled; the sanitizer allowlist is the durable control, not the emulator's own hardening.

---

## Citations

**Cloudflare — official developer docs (`developers.cloudflare.com`)**
- [CF-AppToken] Access application token (JWT claims: `Cf-Access-Jwt-Assertion`, `CF_Authorization`, `aud`/`email`/`iss`/`exp`, RS256, `common_name` for service tokens): https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/application-token/
- [CF-ValidateJWT] Validate JSON Web Tokens (JWKS certs endpoint, `kid` matching, "validate with your public key … not a malicious third party", do-not-hardcode-key): https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/validating-json/
- [CF-AuthCookie] Authorization cookie / binding cookie (stolen-cookie replay defense): https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/
- [CF-AccessWorkers] Custom headers for Access-protected origins (`cf-access-authenticated-user-email` usage): https://developers.cloudflare.com/cloudflare-one/tutorials/access-workers/
- [CF-ServiceTokens] Service tokens (`CF-Access-Client-Id`/`Secret`, Service Auth policy, revoke = delete): https://developers.cloudflare.com/cloudflare-one/access-controls/service-credentials/service-tokens/
- [CF-SelfHostedApp] Self-hosted app / "deny by default": https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/self-hosted-public-app/
- [CF-PreventExternal] Prevent external connections to origin (outbound-only tunnel, block inbound): https://developers.cloudflare.com/learning-paths/prevent-ddos-attacks/advanced/prevent-external-connections/
- [CF-TryCloudflare] TryCloudflare / quick tunnels ("testing and development only", "no SLA", random hostname, 200-request cap, no SSE): https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/trycloudflare/
- [CF-TunnelTokens] Tunnel tokens ("only requires a token to run", "anyone with the token can run the tunnel", rotate/force-disconnect): https://developers.cloudflare.com/tunnel/advanced/tunnel-tokens/
- [CF-LocalTunnel] Local-management terms (credentials JSON as tunnel auth): https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/local-tunnel-terms/
- [CF-CreateLocalTunnel] Create a locally-managed tunnel (`cert.pem`, credentials JSON, `~/.cloudflared`): https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/create-local-tunnel/
- [CF-ConfigFile] Configuration file / ingress rules (catch-all requirement): https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/configuration-file/
- [CF-OriginParams] originRequest parameters (`noTLSVerify`, `caPool`, `originServerName`, `access`): https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/origin-parameters/

**Go — official (`go.dev`, `pkg.go.dev`)**
- [GO-OsExec] `os/exec` (no shell invocation, `ErrDot`/Go 1.19 relative-path fix, `CommandContext`, `Cancel`/`WaitDelay`, `LookPath`): https://pkg.go.dev/os/exec
- [GO-PathSecurity] Go blog, "Executable path security" (PATH-in-cwd rationale): https://go.dev/blog/path-security
- [GO-Go125] Go 1.25 release notes (`CrossOriginProtection` CSRF): https://go.dev/doc/go1.25
- [GO-NetHTTP] `net/http` (`CrossOriginProtection`, `AddTrustedOrigin`, safe-methods caveat; `Server` timeouts `ReadHeaderTimeout`/`ReadTimeout`/`WriteTimeout`/`IdleTimeout`): https://pkg.go.dev/net/http
- [GO-Gorilla] `gorilla/websocket` (`CheckOrigin` default and origin-considerations): https://pkg.go.dev/github.com/gorilla/websocket
- [GO-Subtle] `crypto/subtle` (`ConstantTimeCompare`): https://pkg.go.dev/crypto/subtle
- [GO-Vuln] govulncheck & Go vulnerability database: https://go.dev/doc/security/vuln/
- [GO-ModMirror] Go blog, module mirror & checksum database (Merkle-tree tamper-proofing): https://go.dev/blog/module-mirror-launch
- [GO-ModRef] Go modules reference (`GOSUMDB`, `GOPRIVATE`, `-mod=readonly`, `GOFLAGS`): https://go.dev/ref/mod
- [GO-Go126] Go 1.26 release notes (experimental `runtime/secret`, heap-base ASLR, crypto randomness): https://go.dev/doc/go1.26

**Web application security — OWASP / MDN**
- [OWASP-WS] OWASP WebSocket Security Cheat Sheet (CSWSH, cookies on handshake, Origin allowlist, `wss://`): https://cheatsheetseries.owasp.org/cheatsheets/WebSocket_Security_Cheat_Sheet.html
- [OWASP-CSRF] OWASP CSRF Prevention Cheat Sheet (synchronizer token, double-submit, SameSite/`__Host-`, Origin/Referer, custom headers): https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html
- [MDN-CSP] MDN Content-Security-Policy (`unsafe-inline`/`unsafe-eval`, nonces, `object-src`): https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Content-Security-Policy
- [MDN-ConnectSrc] MDN `connect-src` (governs WebSocket; `'self'` scheme caveat): https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Content-Security-Policy/connect-src
- [MDN-FrameAncestors] MDN `frame-ancestors` (no fallback to `default-src`): https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Content-Security-Policy/frame-ancestors
- [MDN-XFO] MDN `X-Frame-Options`: https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/X-Frame-Options

**Terminal escape-sequence security**
- [DGL-ANSI] David Leadbeater, "ANSI Terminal security in 2023 and finding 10 CVEs" (DECRQSS echoback RCE, OSC 52/OSC 8, title-report; iTerm2 CVE-2022-45872, mintty, SwiftTerm, less CVE-2022-46663, ConEmu CVE-2022-46387/CVE-2023-39150): https://dgl.cx/2023/09/ansi-terminal-security
- [CyberArk-ANSI] CyberArk, "Don't Trust This Title: Abusing Terminal Emulators with ANSI Escape Characters": https://www.cyberark.com/resources/threat-research-blog/dont-trust-this-title-abusing-terminal-emulators-with-ansi-escape-characters
- [ElReg-ANSI] The Register, ANSI escape sequence risks coverage: https://www.theregister.com/2023/08/09/ansi_escape_sequence_risks/
- [Ganev-Codex] Real-world ANSI-escape-injection → RCE in a coding-agent CLI: https://dganev.com/posts/2026-02-12-ansi-escape-injection-codex-cli/

---

*Prepared as research; no code is included or implied to be production-ready. Two Cloudflare claims (quick tunnels lacking access control by default; TryCloudflare as an abuse vector) are documented inferences rather than verbatim Cloudflare statements, and are flagged inline.*
