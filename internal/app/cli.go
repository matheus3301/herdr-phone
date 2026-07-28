package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/matheus3301/herdr-phone/internal/buildinfo"
)

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usage = `` + buildinfo.DisplayName + ` ` + buildinfo.Version + ` — use your Mac's Herdr session from your phone

Usage:
  herdr-phone <command> [flags]

Commands:
  start [--quick] [--foreground]   Start the relay daemon and print the URL to open
  stop                             Gracefully stop the relay daemon
  toggle                           Stop the relay if running, otherwise start it
  status [--json]                  Show relay status
  setup-link                       Rotate the pairing secret and print a new link
  doctor                           Validate config, Herdr, cloudflared, and state
  serve                            Run the daemon in the foreground (internal)
  version                          Print the version
  help                             Show this help

Flags:
  -h, --help       Show this help
  -v, --version    Print the version

Configuration is loaded from $HERDR_PLUGIN_CONFIG_DIR/config.toml,
$XDG_CONFIG_HOME/herdr-phone/config.toml, or
$HOME/.config/herdr-phone/config.toml. See config.example.toml for the full
reference.
`

// Main dispatches a command and returns the process exit code.
func Main(env Environment) int {
	if env.Stdout == nil {
		env.Stdout = os.Stdout
	}
	if env.Stderr == nil {
		env.Stderr = os.Stderr
	}
	if len(env.Args) == 0 {
		fmt.Fprint(env.Stderr, usage)
		return exitUsage
	}

	cmd := env.Args[0]
	rest := env.Args[1:]

	switch cmd {
	case "help", "--help", "-h":
		fmt.Fprint(env.Stdout, usage)
		return exitOK
	case "version", "--version", "-v":
		if code, handled := requireNoArgs(env, cmd, rest); handled {
			return code
		}
		fmt.Fprintf(env.Stdout, "%s %s\n", buildinfo.Name, buildinfo.Version)
		return exitOK
	case ActionStart:
		return runStart(env, rest)
	case "serve":
		return runServe(env, rest)
	case ActionStop:
		if code, handled := requireNoArgs(env, cmd, rest); handled {
			return code
		}
		return runStop(env)
	case ActionToggle:
		if code, handled := requireNoArgs(env, cmd, rest); handled {
			return code
		}
		return runToggle(env)
	case ActionStatus:
		return runStatus(env, rest)
	case ActionSetupLink:
		if code, handled := requireNoArgs(env, cmd, rest); handled {
			return code
		}
		return runSetupLink(env)
	case ActionDoctor:
		if code, handled := requireNoArgs(env, cmd, rest); handled {
			return code
		}
		return runDoctor(env)
	default:
		fmt.Fprintf(env.Stderr, "herdr-phone: unknown command %q\n\n%s", cmd, usage)
		return exitUsage
	}
}

// requireNoArgs enforces that a subcommand takes no positional arguments and
// handles a local -h/--help flag. It returns (exitCode, handled): when handled
// is true the caller should return exitCode immediately.
func requireNoArgs(env Environment, cmd string, rest []string) (int, bool) {
	for _, a := range rest {
		if a == "-h" || a == "--help" {
			fmt.Fprint(env.Stdout, usage)
			return exitOK, true
		}
	}
	if len(rest) > 0 {
		fmt.Fprintf(env.Stderr, "herdr-phone %s takes no arguments (got %q)\n\n%s", cmd, strings.Join(rest, " "), usage)
		return exitUsage, true
	}
	return exitOK, false
}

// parseBoolFlags parses a small set of known boolean flags from args, returning
// the set of seen flags. Unknown flags or any positional argument are errors.
func parseBoolFlags(args []string, allowed ...string) (map[string]bool, error) {
	allow := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		allow[a] = struct{}{}
	}
	seen := make(map[string]bool)
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			return nil, fmt.Errorf("unexpected argument %q", a)
		}
		name := strings.TrimPrefix(a, "--")
		if _, ok := allow[name]; !ok {
			return nil, fmt.Errorf("unknown flag %q", a)
		}
		seen[name] = true
	}
	return seen, nil
}
