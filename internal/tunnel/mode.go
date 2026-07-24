// Package tunnel manages the cloudflared child process that fronts the loopback
// relay origin. It supports Cloudflare named tunnels (config/credentials,
// token-file, and token-command credential strategies) and explicitly enabled
// Quick Tunnels. Execution is argv-only (no shell), secrets never reach argv or
// logs, and readiness/URL are derived from bounded structured log parsing.
package tunnel

import (
	"fmt"
	"net"
	"time"
)

// Mode selects how cloudflared exposes the loopback origin.
type Mode string

const (
	// ModeNamed uses a persistent named Cloudflare tunnel. It is the production
	// front door and is expected to sit behind Cloudflare Access.
	ModeNamed Mode = "named"
	// ModeQuick uses an ephemeral *.trycloudflare.com Quick Tunnel. It must be
	// explicitly enabled and has no edge identity layer.
	ModeQuick Mode = "quick"
)

// Strategy selects how a named tunnel's credentials are supplied to cloudflared.
type Strategy string

const (
	// StrategyConfig runs `cloudflared tunnel --config <file> run`; the config
	// file references the credentials file and tunnel.
	StrategyConfig Strategy = "config"
	// StrategyCredentials runs `cloudflared tunnel run --credentials-file <file> <tunnel>`.
	StrategyCredentials Strategy = "credentials"
	// StrategyTokenFile runs `cloudflared tunnel run --token-file <file>` with a
	// caller-provided token file.
	StrategyTokenFile Strategy = "token_file"
	// StrategyTokenCommand runs an argv command to obtain the token, writes it to
	// a temporary 0600 file, and runs `cloudflared tunnel run --token-file <tmp>`.
	StrategyTokenCommand Strategy = "token_command"
)

// Default values applied when a field is left zero.
const (
	defaultBinary          = "cloudflared"
	defaultMetricsAddr     = "127.0.0.1:0"
	defaultGracePeriod     = 15 * time.Second
	defaultStabilityWindow = 45 * time.Second
	loopbackHost           = "127.0.0.1"
)

// Config is the validated input required to build and run cloudflared. It is a
// self-contained value so this package does not depend on the config package;
// the application layer maps user configuration onto it.
type Config struct {
	Mode   Mode
	Binary string // cloudflared binary path or name; defaults to "cloudflared"

	// LoopbackPort is the 127.0.0.1 origin port cloudflared proxies to.
	LoopbackPort int

	// PublicURL is the operator-facing HTTPS URL. Required (and reported as the
	// public URL) for named mode; ignored for quick mode where the URL is
	// discovered from cloudflared's logs.
	PublicURL string

	// Strategy selects the named credential source. When empty it is inferred
	// from the populated fields below and must resolve to exactly one strategy.
	Strategy Strategy

	ConfigFile      string   // StrategyConfig
	CredentialsFile string   // StrategyCredentials
	Tunnel          string   // StrategyCredentials: tunnel name or UUID
	TokenFile       string   // StrategyTokenFile: caller-provided token file
	TokenCommand    []string // StrategyTokenCommand: argv producing the token on stdout

	// QuickEnabled must be true for ModeQuick to start. It guards accidental
	// exposure over an unauthenticated Quick Tunnel.
	QuickEnabled bool

	// GracePeriod bounds graceful cloudflared shutdown before a forced kill.
	GracePeriod time.Duration

	// StabilityWindow is how long an instance must stay ready before its success
	// resets the restart backoff and consecutive-failure counter. A shorter
	// lifetime counts as a failure, so a cloudflared that reaches readiness and
	// then dies repeatedly (flapping) still degrades instead of spinning at the
	// minimum backoff forever. Empty means defaultStabilityWindow.
	StabilityWindow time.Duration

	// MetricsAddr is passed to `--metrics`; it must be a loopback host so
	// cloudflared diagnostics never bind off-loopback.
	MetricsAddr string

	// TokenDir is the directory used for temporary 0600 token files created by
	// StrategyTokenCommand. Empty means the OS temp directory.
	TokenDir string

	// PidDir is the directory in which the cloudflared pidfile is written so a
	// later start can reconcile and terminate an orphaned child (for example
	// after the daemon was SIGKILLed on macOS, which has no parent-death
	// signal). Empty falls back to TokenDir, then the OS temp directory.
	PidDir string
}

func (c *Config) applyDefaults() {
	if c.Binary == "" {
		c.Binary = defaultBinary
	}
	if c.MetricsAddr == "" {
		c.MetricsAddr = defaultMetricsAddr
	}
	if c.GracePeriod <= 0 {
		c.GracePeriod = defaultGracePeriod
	}
	if c.StabilityWindow <= 0 {
		c.StabilityWindow = defaultStabilityWindow
	}
}

// metricsAddrIsLoopback reports whether the configured --metrics host is a
// loopback address, so cloudflared diagnostics can never be exposed off-host.
func metricsAddrIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Validate applies defaults and checks the configuration. It returns a nil error
// only when cloudflared can be started deterministically from the fields.
func (c *Config) Validate() error {
	c.applyDefaults()

	if c.LoopbackPort < 1 || c.LoopbackPort > 65535 {
		return fmt.Errorf("tunnel: loopback port %d out of range 1-65535", c.LoopbackPort)
	}
	if !metricsAddrIsLoopback(c.MetricsAddr) {
		return fmt.Errorf("tunnel: metrics address %q must bind a loopback host", c.MetricsAddr)
	}

	switch c.Mode {
	case ModeQuick:
		if !c.QuickEnabled {
			return fmt.Errorf("tunnel: quick mode requires quick_enabled=true")
		}
		return nil
	case ModeNamed:
		strategy, err := c.resolveStrategy()
		if err != nil {
			return err
		}
		c.Strategy = strategy
		if c.PublicURL == "" {
			return fmt.Errorf("tunnel: named mode requires a public_url")
		}
		return c.validateStrategy(strategy)
	case "":
		return fmt.Errorf("tunnel: mode is required (named or quick)")
	default:
		return fmt.Errorf("tunnel: unknown mode %q", c.Mode)
	}
}

// resolveStrategy returns the single named credential strategy, using the
// explicit Strategy field when set and otherwise inferring it from populated
// fields. Ambiguous or missing strategies are errors.
func (c Config) resolveStrategy() (Strategy, error) {
	if c.Strategy != "" {
		switch c.Strategy {
		case StrategyConfig, StrategyCredentials, StrategyTokenFile, StrategyTokenCommand:
			return c.Strategy, nil
		default:
			return "", fmt.Errorf("tunnel: unknown credential strategy %q", c.Strategy)
		}
	}

	var found []Strategy
	if c.ConfigFile != "" {
		found = append(found, StrategyConfig)
	}
	if c.CredentialsFile != "" || c.Tunnel != "" {
		found = append(found, StrategyCredentials)
	}
	if c.TokenFile != "" {
		found = append(found, StrategyTokenFile)
	}
	if len(c.TokenCommand) > 0 {
		found = append(found, StrategyTokenCommand)
	}

	switch len(found) {
	case 0:
		return "", fmt.Errorf("tunnel: named mode requires exactly one credential strategy (config, credentials, token file, or token command)")
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("tunnel: named mode requires exactly one credential strategy, found %d", len(found))
	}
}

func (c Config) validateStrategy(strategy Strategy) error {
	switch strategy {
	case StrategyConfig:
		if c.ConfigFile == "" {
			return fmt.Errorf("tunnel: config strategy requires config_file")
		}
	case StrategyCredentials:
		if c.CredentialsFile == "" || c.Tunnel == "" {
			return fmt.Errorf("tunnel: credentials strategy requires credentials_file and tunnel")
		}
	case StrategyTokenFile:
		if c.TokenFile == "" {
			return fmt.Errorf("tunnel: token file strategy requires token_file")
		}
	case StrategyTokenCommand:
		if len(c.TokenCommand) == 0 {
			return fmt.Errorf("tunnel: token command strategy requires a non-empty token_command argv")
		}
	default:
		return fmt.Errorf("tunnel: unknown credential strategy %q", strategy)
	}
	return nil
}

// usesTokenCommand reports whether a per-attempt temporary token file must be
// produced from TokenCommand.
func (c Config) usesTokenCommand() bool {
	return c.Mode == ModeNamed && c.Strategy == StrategyTokenCommand
}
