package daemon

import (
	"testing"

	"github.com/matheus3301/herdr-phone/internal/tunnel"
)

// TestRealSupervisorSatisfiesController proves the production tunnel supervisor
// fits the adapter's controller interface and can be wrapped, without executing
// cloudflared. If tunnel.Supervisor's method set drifts, this fails to compile.
func TestRealSupervisorSatisfiesController(t *testing.T) {
	t.Parallel()
	var _ tunnelController = (*tunnel.Supervisor)(nil)
	tc := NewTunnelChild((*tunnel.Supervisor)(nil))
	var _ Child = tc
	var _ ReadinessProbe = tc
}
