// Package app wires configuration, the version source, and the orchestration
// backend into the herdr-phone command-line surface. It owns argument parsing,
// command dispatch, and output formatting; the heavy lifting (process
// lifecycle, tunnels, Herdr) lives behind the Runtime interface so the CLI is
// deterministic and testable without a real Cloudflare account, Herdr session,
// or network.
package app

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/matheus3301/herdr-phone/internal/config"
)

// Plugin identifiers. PluginID must match herdr-plugin.toml (enforced by tests).
const (
	PluginID = "matheus3301.phone"

	ActionStart      = "start"
	ActionStartQuick = "start-quick"
	ActionStop       = "stop"
	ActionToggle     = "toggle"
	ActionStatus     = "status"
	ActionSetupLink  = "setup-link"
	ActionDoctor     = "doctor"
)

// errRuntimeUnavailable is returned when a command needs the orchestration
// backend but the process was built without it wired in. In a fully integrated
// binary cmd/herdr-phone/main.go injects internal/daemon; until then commands
// that require live orchestration fail with this explicit error rather than a
// silent no-op.
var errRuntimeUnavailable = errors.New("orchestration backend not configured: this build cannot manage the daemon")

// Environment holds process inputs and injectable dependencies so the whole CLI
// is testable without a real environment, network, or orchestration backend.
type Environment struct {
	Getenv func(string) string
	Args   []string
	Stdout io.Writer
	Stderr io.Writer

	// Runtime is the orchestration backend. It is nil in a foundation-only build;
	// commands that require it then fail with errRuntimeUnavailable.
	Runtime Runtime
}

// runtime returns the configured orchestration backend or an explicit error.
func (e Environment) runtime() (Runtime, error) {
	if e.Runtime == nil {
		return nil, errRuntimeUnavailable
	}
	return e.Runtime, nil
}

// loadConfig loads and validates configuration from the environment.
func (e Environment) loadConfig() (config.Config, error) {
	return config.Load(e.Getenv)
}

// newSignalContext returns a context cancelled on interrupt/termination signals
// so a long-running command (start --foreground, serve) shuts down gracefully.
// It is a variable so tests can substitute a plain cancellable context.
var newSignalContext = func() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
