package tunnel

import "fmt"

// buildArgs returns the argv (excluding the binary itself) for cloudflared.
//
// tokenFilePath is the resolved token file path for the token-file and
// token-command strategies and is empty otherwise. Only file *paths* ever appear
// in argv: the token secret is delivered through the file, never through a flag
// value, so argv is always safe to log or expose in process listings.
func buildArgs(c Config, tokenFilePath string) ([]string, error) {
	// Common flags: no self-update (the plugin owns the binary), loopback-only
	// diagnostics, and bounded machine-readable logs. Debug logging is avoided
	// because it can surface request headers.
	args := []string{
		"tunnel",
		"--no-autoupdate",
		"--loglevel", "info",
		"--output", "json",
		"--metrics", c.MetricsAddr,
	}

	switch c.Mode {
	case ModeQuick:
		args = append(args, "--url", loopbackOriginURL(c.LoopbackPort))
		return args, nil

	case ModeNamed:
		switch c.Strategy {
		case StrategyConfig:
			args = append(args, "--config", c.ConfigFile, "run")
		case StrategyCredentials:
			args = append(args, "run", "--credentials-file", c.CredentialsFile, c.Tunnel)
		case StrategyTokenFile, StrategyTokenCommand:
			if tokenFilePath == "" {
				return nil, fmt.Errorf("tunnel: token strategy requires a resolved token file path")
			}
			args = append(args, "run", "--token-file", tokenFilePath)
		default:
			return nil, fmt.Errorf("tunnel: unknown credential strategy %q", c.Strategy)
		}
		return args, nil

	default:
		return nil, fmt.Errorf("tunnel: unknown mode %q", c.Mode)
	}
}

func loopbackOriginURL(port int) string {
	return fmt.Sprintf("http://%s:%d", loopbackHost, port)
}
