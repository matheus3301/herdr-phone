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
