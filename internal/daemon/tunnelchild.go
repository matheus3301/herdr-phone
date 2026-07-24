package daemon

import (
	"context"
	"sync"
)

// tunnelController is the subset of the tunnel supervisor the daemon adapter
// needs. *tunnel.Supervisor satisfies it structurally, so the daemon package
// does not import the tunnel package and stays independently testable with a
// fake controller.
//
// The controller self-supervises cloudflared (it restarts the child with its
// own backoff and moves to a terminal degraded state after repeated failures),
// so Done closes only at that terminal state, never on an individual restart.
type tunnelController interface {
	// Start launches the supervise loop; it returns immediately.
	Start(ctx context.Context)
	// Ready is closed the first time the tunnel becomes ready.
	Ready() <-chan struct{}
	// Done is closed when the supervise loop has fully exited (stopped or
	// degraded).
	Done() <-chan struct{}
	// Stop requests a graceful shutdown bounded by ctx.
	Stop(ctx context.Context) error
	// URL is the public URL once known.
	URL() string
	// Err is the most recent non-fatal error, if any.
	Err() error
	// RecentLogs returns bounded, sanitized recent log lines (never secrets).
	RecentLogs() []string
}

// TunnelChild adapts a tunnel supervisor to both the Child interface (for
// lifecycle management) and the ReadinessProbe interface (for status
// propagation). Because the tunnel supervisor restarts cloudflared internally,
// wrap it with a non-restarting ChildSupervisor (see Daemon.AddTunnel).
type TunnelChild struct {
	ctrl tunnelController

	startOnce sync.Once
}

// NewTunnelChild wraps a tunnel supervisor. The argument is any value with the
// tunnel supervisor's method set; production callers pass *tunnel.Supervisor.
func NewTunnelChild(ctrl tunnelController) *TunnelChild {
	return &TunnelChild{ctrl: ctrl}
}

// Name identifies the child in status and logs.
func (t *TunnelChild) Name() string { return "tunnel" }

// Start launches the tunnel supervisor exactly once. Repeat calls are no-ops so
// an outer supervisor can never double-start the internally self-supervising
// tunnel.
func (t *TunnelChild) Start(ctx context.Context) error {
	t.startOnce.Do(func() { t.ctrl.Start(ctx) })
	return nil
}

// Wait blocks until the tunnel supervisor terminally exits and returns its last
// error (nil on a clean stop). It unblocks after Stop because Stop drives the
// supervisor to a terminal state.
func (t *TunnelChild) Wait() error {
	<-t.ctrl.Done()
	return t.ctrl.Err()
}

// Stop requests a graceful tunnel shutdown.
func (t *TunnelChild) Stop(ctx context.Context) error {
	return t.ctrl.Stop(ctx)
}

// Ready reports tunnel readiness for status aggregation. It never blocks.
func (t *TunnelChild) Ready(ctx context.Context) (bool, string) {
	select {
	case <-t.ctrl.Ready():
		if url := t.ctrl.URL(); url != "" {
			return true, "connected: " + url
		}
		return true, "connected"
	default:
	}

	select {
	case <-t.ctrl.Done():
		// Terminated without ever becoming ready: degraded or stopped.
		if err := t.ctrl.Err(); err != nil {
			return false, "unavailable: " + sanitizeErr(err)
		}
		return false, "stopped"
	default:
	}

	return false, "starting"
}
