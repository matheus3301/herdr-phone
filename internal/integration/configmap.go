package integration

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/matheus3301/herdr-phone/internal/config"
	"github.com/matheus3301/herdr-phone/internal/server"
	"github.com/matheus3301/herdr-phone/internal/tunnel"
)

// tunnelStabilityWindow is how long cloudflared must stay ready before the run
// counts as stable (resetting restart backoff/failure counting). A cloudflared
// that reaches readiness then dies repeatedly still degrades instead of spinning
// at the minimum backoff.
const tunnelStabilityWindow = 45 * time.Second

// Sentinel errors surfaced to the CLI.
var (
	errNoStateDir        = errors.New("cannot resolve a state directory: set HERDR_PLUGIN_STATE_DIR, XDG_STATE_HOME, or HOME")
	errQuickNotEnabled   = errors.New("--quick requires cloudflare.quick_enabled = true")
	errRuntimeNotRunning = errors.New("herdr-phone is not running")
)

// effectiveMode resolves the front-door mode for this run. --quick forces quick
// mode (which requires the explicit quick_enabled opt-in regardless of the
// configured default); otherwise the configured mode applies.
func effectiveMode(cfg config.Config, quick bool) (string, error) {
	if quick {
		if !cfg.Cloudflare.QuickEnabled {
			return "", errQuickNotEnabled
		}
		return config.ModeQuick, nil
	}
	if cfg.Cloudflare.Mode == config.ModeQuick && !cfg.Cloudflare.QuickEnabled {
		return "", errQuickNotEnabled
	}
	return cfg.Cloudflare.Mode, nil
}

// tunnelConfig maps user configuration onto the tunnel package's self-contained
// Config for the resolved mode and loopback port. Temporary token files and the
// cloudflared pidfile land in the mode-0700 state directory; the pidfile lets a
// later start reconcile and terminate an orphaned child (see the macOS note).
func tunnelConfig(cfg config.Config, mode string, port int, stateDir string) tunnel.Config {
	return tunnel.Config{
		Mode:            tunnel.Mode(mode),
		Binary:          cfg.Cloudflare.Binary,
		LoopbackPort:    port,
		PublicURL:       cfg.Cloudflare.PublicURL,
		ConfigFile:      cfg.Cloudflare.ConfigFile,
		CredentialsFile: cfg.Cloudflare.CredentialsFile,
		Tunnel:          cfg.Cloudflare.Tunnel,
		TokenFile:       cfg.Cloudflare.TokenFile,
		TokenCommand:    cfg.Cloudflare.TokenCommand,
		QuickEnabled:    cfg.Cloudflare.QuickEnabled,
		GracePeriod:     cfg.Cloudflare.GracePeriod,
		StabilityWindow: tunnelStabilityWindow,
		TokenDir:        stateDir,
		PidDir:          stateDir,
	}
}

// serverConfig builds the HTTP server configuration once the public URL is
// known. In named mode the public URL comes from config; in quick mode it is the
// tunnel-discovered *.trycloudflare.com URL. Loopback dev hosts and origins are
// always allowed so a local operator can reach the origin directly (for example
// over an SSH port-forward) without weakening the public-host allowlist.
func serverConfig(mode, publicURL string, port int) server.Config {
	hostPort := "127.0.0.1:" + strconv.Itoa(port)
	localHost := "localhost:" + strconv.Itoa(port)
	cfg := server.Config{
		DevHosts: []string{hostPort, localHost},
		AllowedOrigins: []string{
			"http://" + hostPort,
			"http://" + localHost,
		},
		Quick: mode == config.ModeQuick,
	}
	if publicURL != "" {
		host := hostOnly(publicURL)
		cfg.PublicHost = host
		cfg.AllowedOrigins = append([]string{"https://" + host}, cfg.AllowedOrigins...)
	}
	return cfg
}

// validateForServe applies the runtime-only rules that structural config
// validation cannot: the free-port check happens when the listener binds, and
// credential-file permissions are verified here before any child starts.
func validateForServe(cfg config.Config, mode string) error {
	if mode == config.ModeNamed {
		if cfg.Cloudflare.CredentialsFile != "" {
			if err := config.VerifySecretFile(cfg.Cloudflare.CredentialsFile); err != nil {
				return fmt.Errorf("cloudflare.credentials_file: %w", err)
			}
		}
		if cfg.Cloudflare.TokenFile != "" {
			if err := config.VerifySecretFile(cfg.Cloudflare.TokenFile); err != nil {
				return fmt.Errorf("cloudflare.token_file: %w", err)
			}
		}
	}
	return nil
}
