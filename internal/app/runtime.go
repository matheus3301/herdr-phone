package app

import (
	"context"
	"time"

	"github.com/matheus3301/herdr-phone/internal/config"
)

// Runtime is the orchestration backend the CLI drives. The production
// implementation lives in internal/daemon and owns process lifecycle, the
// private control socket, cloudflared, and the Herdr/state/terminal stack. It is
// an interface here so the CLI is fully testable with fakes and so parallel
// packages depend on this minimal contract rather than on the CLI.
//
// Every method takes the already-loaded, validated config so the backend never
// re-reads or re-parses configuration. Methods must honor ctx for cancellation
// and deadlines.
type Runtime interface {
	// Start ensures a healthy daemon is running for cfg and returns its mode,
	// public URL, and a fresh single-use pairing URL. It is idempotent: a healthy
	// existing daemon is reported with AlreadyRunning set rather than replaced.
	Start(ctx context.Context, cfg config.Config, opts StartOptions) (StartResult, error)

	// Serve runs the foreground daemon (the `serve` entrypoint and `start
	// --foreground`) until ctx is cancelled, then shuts down within the grace
	// period.
	Serve(ctx context.Context, cfg config.Config, opts ServeOptions) error

	// Stop requests graceful shutdown through the private control socket. It
	// never kills an arbitrary PID. WasRunning reports whether a daemon was found.
	Stop(ctx context.Context, cfg config.Config) (StopResult, error)

	// Status reports the current daemon status via the control socket.
	Status(ctx context.Context, cfg config.Config) (Status, error)

	// RotatePairing rotates the single-use pairing secret on the running daemon
	// and returns the new pairing link.
	RotatePairing(ctx context.Context, cfg config.Config) (PairingLink, error)

	// Doctor runs environment diagnostics that require live access: Herdr
	// reachability and protocol, cloudflared presence, tunnel credentials, and
	// state directory health. Structural config and file-permission checks are
	// performed by the CLI itself before this is called.
	Doctor(ctx context.Context, cfg config.Config) (DoctorReport, error)
}

// StartOptions controls a start.
type StartOptions struct {
	// Quick selects a Quick Tunnel for this run (requires cloudflare.quick_enabled).
	Quick bool
	// Foreground runs the daemon in the current process instead of detaching.
	Foreground bool
}

// ServeOptions controls a foreground serve.
type ServeOptions struct {
	Quick bool
}

// StartResult describes the outcome of a start.
type StartResult struct {
	Mode           string
	PublicURL      string
	PairingURL     string
	AlreadyRunning bool
}

// StopResult describes the outcome of a stop.
type StopResult struct {
	WasRunning bool
}

// Status is the authenticated status view surfaced by `status`.
type Status struct {
	Running          bool
	Mode             string
	PublicURL        string
	LocalAddress     string
	Version          string
	Protocol         int
	PID              int
	StartedAt        time.Time
	Health           string // e.g. "ready", "degraded"
	HerdrHealthy     bool
	TunnelHealthy    bool
	StateHealthy     bool
	ConnectedClients int
}

// PairingLink is a single-use pairing URL. The secret lives only in the URL
// fragment, which browsers never send to the server.
type PairingLink struct {
	URL string
}

// DoctorCheck is one diagnostic line.
type DoctorCheck struct {
	Name   string
	OK     bool
	Detail string
}

// DoctorReport is the collected environment diagnostics.
type DoctorReport struct {
	Checks []DoctorCheck
}
