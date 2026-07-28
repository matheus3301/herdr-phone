package integration

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/matheus3301/herdr-phone/internal/app"
	"github.com/matheus3301/herdr-phone/internal/config"
	"github.com/matheus3301/herdr-phone/internal/herdr"
	"github.com/matheus3301/herdr-phone/internal/tunnel"
)

// Doctor runs the live environment diagnostics the CLI cannot perform itself:
// Herdr reachability and protocol, cloudflared presence, tunnel-config
// resolvability, and state-directory writability. It never prints secrets.
func (rt *Runtime) Doctor(ctx context.Context, cfg config.Config) (app.DoctorReport, error) {
	var checks []app.DoctorCheck
	add := func(name string, ok bool, detail string) {
		checks = append(checks, app.DoctorCheck{Name: name, OK: ok, Detail: detail})
	}

	// Herdr socket + handshake.
	socket := resolveHerdrSocket(cfg, rt.env)
	if socket == "" {
		add("Herdr", false, "socket path unresolved: set herdr.socket_path or HERDR_SOCKET_PATH")
	} else {
		client := herdr.NewClient(herdr.NewUnixDialer(socket))
		hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		pong, err := client.Handshake(hctx)
		cancel()
		if err != nil {
			add("Herdr", false, "unreachable at "+socket+" ("+err.Error()+")")
		} else {
			add("Herdr", true, fmt.Sprintf("v%s protocol %d at %s", pong.Version, pong.Protocol, socket))
		}
	}

	// cloudflared availability.
	bin := cfg.Cloudflare.Binary
	if bin == "" {
		bin = "cloudflared"
	}
	if path, err := exec.LookPath(bin); err != nil {
		add("cloudflared", false, "not found ("+bin+"); install it, e.g. `brew install cloudflared`")
	} else {
		add("cloudflared", true, path)
	}

	// Named mode's identity gate. Surfaced on every doctor run because named mode
	// is Access-only: the allowlist is the last identity filter the origin applies.
	if c, ok := accessIdentityGateCheck(cfg); ok {
		checks = append(checks, c)
	}

	// Tunnel configuration resolvability for the configured mode.
	tcfg := tunnelConfig(cfg, cfg.Cloudflare.Mode, cfg.Server.Port, "")
	if err := tcfg.Validate(); err != nil {
		add("Tunnel config", false, err.Error())
	} else {
		add("Tunnel config", true, "mode "+cfg.Cloudflare.Mode)
	}

	// State directory.
	if stateDir, err := resolveStateDir(rt.env); err != nil {
		add("State directory", false, err.Error())
	} else if err := ensureStateDir(stateDir); err != nil {
		add("State directory", false, err.Error())
	} else {
		add("State directory", true, stateDir)
	}

	// Surface the residual macOS orphan-tunnel risk (informational; the note is
	// secret-free). On macOS a SIGKILLed daemon can leave cloudflared running
	// until the next start reconciles the pidfile and terminates the orphan.
	if runtime.GOOS == "darwin" {
		add("macOS tunnel note", true, tunnel.MacOSKillWindowNote)
	}

	return app.DoctorReport{Checks: checks}, nil
}

// accessIdentityGateName labels the named-mode identity-gate check.
const accessIdentityGateName = "Access identity allowlist"

// accessIdentityGateCheck reports how tightly the origin filters identities in
// named mode. It returns ok=false when the check does not apply (quick mode has
// no edge identity, so the single-use pairing secret is its gate and an
// allowlist would mean nothing there).
//
// An empty allowlist permitted by allow_any_identity is a legitimate but
// wide-open configuration: since v0.3.0 dropped pairing as a second factor,
// every identity the Cloudflare Access policy admits reaches a shell-equivalent
// surface. It is reported as a passing check with a WARNING detail rather than a
// failure, because the operator declared it deliberately and config validation
// already refuses the same state when undeclared — a red doctor for a config the
// operator explicitly chose would only teach them to ignore doctor. (app's
// DoctorCheck has no warn severity, and this package does not own it.)
//
// The identities themselves are never printed: doctor output is copied into bug
// reports and panes, and the count is all an operator needs to see.
func accessIdentityGateCheck(cfg config.Config) (app.DoctorCheck, bool) {
	if cfg.Cloudflare.Mode != config.ModeNamed {
		return app.DoctorCheck{}, false
	}
	switch {
	case cfg.Access.HasIdentityAllowlist():
		return app.DoctorCheck{
			Name: accessIdentityGateName,
			OK:   true,
			Detail: fmt.Sprintf("%d identity/identities allowed, matched exactly at the origin",
				len(cfg.Access.AllowedIdentities)),
		}, true
	case cfg.Access.AllowAnyIdentity:
		return app.DoctorCheck{
			Name: accessIdentityGateName,
			OK:   true,
			Detail: "WARNING: allow_any_identity = true with an empty allowed_identities — " +
				"every identity your Cloudflare Access policy admits gets a shell-equivalent " +
				"session, and Access is the only gate (named mode needs no pairing). " +
				"Set auth.access.allowed_identities to your own email unless the Access policy " +
				"itself is the intended boundary",
		}, true
	default:
		// Unreachable from the CLI (invalid configuration is rejected before doctor
		// runs), so this only guards a caller that skipped validation.
		return app.DoctorCheck{
			Name: accessIdentityGateName,
			OK:   false,
			Detail: "empty auth.access.allowed_identities without auth.access.allow_any_identity — " +
				"this configuration is invalid in named mode",
		}, true
	}
}
