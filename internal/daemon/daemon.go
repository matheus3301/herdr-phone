package daemon

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// ReadinessProbe reports whether one subsystem is ready. Implementations are the
// HTTP server, Herdr adapter, tunnel supervisor, and state engine; the daemon
// only depends on this small interface.
type ReadinessProbe interface {
	// Ready reports readiness and a short, non-secret detail string.
	Ready(ctx context.Context) (ready bool, detail string)
}

// ProbeFunc adapts a function to ReadinessProbe.
type ProbeFunc func(ctx context.Context) (bool, string)

// Ready implements ReadinessProbe.
func (f ProbeFunc) Ready(ctx context.Context) (bool, string) { return f(ctx) }

// PairingRotator rotates the daemon's single-use pairing secret and returns the
// new pairing URL. The URL is secret-bearing and must only travel over the
// local control socket, never to disk or logs.
type PairingRotator interface {
	RotatePairing(ctx context.Context) (PairingResult, error)
}

// PairingRotatorFunc adapts a function to PairingRotator.
type PairingRotatorFunc func(ctx context.Context) (PairingResult, error)

// RotatePairing implements PairingRotator.
func (f PairingRotatorFunc) RotatePairing(ctx context.Context) (PairingResult, error) {
	return f(ctx)
}

// Options configures a Daemon.
type Options struct {
	StateDir string
	Runtime  Runtime

	// Probes are named readiness probes aggregated into status (for example
	// "http", "herdr", "tunnel", "state").
	Probes map[string]ReadinessProbe

	// Pairing rotates the pairing secret when the control socket receives a
	// rotate-pairing command. Optional.
	Pairing PairingRotator

	// ClientCount reports the number of connected browser clients. Optional.
	ClientCount func() int

	// OnStop is invoked when a graceful stop is requested (via the control
	// socket or Shutdown). It should signal the rest of the process to unwind;
	// the daemon itself tears down children and the control socket. Optional.
	OnStop func()

	// Lock, when set, is an already-held exclusive state lock the daemon adopts
	// and releases on Shutdown. Integrators that reconcile before Serve should
	// acquire it with AcquireStateLock and pass it here so the lock spans
	// reconcile+bind. When nil, Serve acquires the lock itself. Optional.
	Lock *StateLock
}

// Daemon coordinates the control socket, runtime state, child supervision, and
// graceful shutdown. It owns no subsystem logic directly.
type Daemon struct {
	opts    Options
	control *ControlServer
	lock    *StateLock

	mu       sync.Mutex
	health   Health
	children []*ChildSupervisor

	stopOnce sync.Once
}

// New builds a Daemon. It does not start the control socket; call Serve.
func New(opts Options) *Daemon {
	health := opts.Runtime.Health
	if health == "" {
		health = HealthStarting
	}
	return &Daemon{
		opts:   opts,
		health: health,
		lock:   opts.Lock,
	}
}

// AddChild registers a child supervisor for lifecycle management. It must be
// called before Serve. The daemon starts and stops these in Serve/Shutdown.
func (d *Daemon) AddChild(sup *ChildSupervisor) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.children = append(d.children, sup)
}

// AddProbe registers a readiness probe under name for status aggregation. It
// must be called before Serve.
func (d *Daemon) AddProbe(name string, probe ReadinessProbe) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.opts.Probes == nil {
		d.opts.Probes = make(map[string]ReadinessProbe)
	}
	d.opts.Probes[name] = probe
}

// AddTunnel registers a tunnel supervisor for full daemon integration: it is
// supervised as a non-restarting child (the tunnel restarts cloudflared
// internally, so the outer supervisor must not restart it) and exposed as the
// "tunnel" readiness probe. Call before Serve.
func (d *Daemon) AddTunnel(tc *TunnelChild) {
	// MaxConsecutive:1 means the outer supervisor never re-Starts the child: on
	// the tunnel's own terminal exit it records degraded and stops, leaving the
	// probe to report the tunnel unavailable.
	d.AddChild(NewChildSupervisor(tc, RestartPolicy{MaxConsecutive: 1}))
	d.AddProbe(tc.Name(), tc)
}

// Serve writes runtime state, opens the control socket, and starts supervising
// children. It returns once the socket is listening; the socket is served in a
// background goroutine. Call Shutdown to stop.
func (d *Daemon) Serve(ctx context.Context) error {
	if d.opts.StateDir == "" {
		return errors.New("daemon: state dir is required")
	}

	// Hold an exclusive advisory lock for this daemon's lifetime so a concurrent
	// start cannot remove/rebind this daemon's control socket or clobber
	// runtime.json. If the caller already acquired the lock (across its own
	// reconcile), it passes it via Options.Lock and we adopt it. Otherwise we
	// acquire it here, before any state mutation or bind.
	if d.lock == nil {
		lock, err := AcquireStateLock(d.opts.StateDir)
		if err != nil {
			return err
		}
		d.lock = lock
	}

	runtimePath := RuntimePath(d.opts.StateDir)
	rt := d.opts.Runtime
	rt.Health = d.currentHealth()
	if err := WriteRuntime(runtimePath, rt); err != nil {
		_ = d.lock.Release()
		d.lock = nil
		return err
	}

	socketPath, err := SocketPath(d.opts.StateDir)
	if err != nil {
		_ = d.lock.Release()
		d.lock = nil
		return err
	}
	cs, err := Listen(socketPath, Handlers{
		Status:        d.handleStatus,
		RotatePairing: d.handleRotatePairing,
		Stop:          d.handleStop,
	})
	if err != nil {
		_ = d.lock.Release()
		d.lock = nil
		return err
	}
	d.control = cs
	go cs.Serve()

	d.mu.Lock()
	children := append([]*ChildSupervisor(nil), d.children...)
	d.mu.Unlock()
	for _, sup := range children {
		go sup.Run(ctx)
	}

	return nil
}

func (d *Daemon) handleStatus(ctx context.Context) (StatusResult, error) {
	components := d.probeComponents(ctx)

	allReady := len(components) > 0
	for _, c := range components {
		if !c.Ready {
			allReady = false
			break
		}
	}

	health := d.reconcileHealth(allReady)

	clientCount := 0
	if d.opts.ClientCount != nil {
		clientCount = d.opts.ClientCount()
	}

	return StatusResult{
		Health:      health,
		Mode:        d.opts.Runtime.Mode,
		PublicURL:   d.opts.Runtime.PublicURL,
		LocalAddr:   d.opts.Runtime.LocalAddr,
		Version:     d.opts.Runtime.Version,
		InstanceID:  d.opts.Runtime.InstanceID,
		PID:         d.opts.Runtime.PID,
		StartUnixMs: d.opts.Runtime.StartUnixMs,
		ClientCount: clientCount,
		Components:  components,
	}, nil
}

func (d *Daemon) probeComponents(ctx context.Context) []ComponentStatus {
	d.mu.Lock()
	probes := make(map[string]ReadinessProbe, len(d.opts.Probes))
	for name, p := range d.opts.Probes {
		probes[name] = p
	}
	d.mu.Unlock()

	names := make([]string, 0, len(probes))
	for name := range probes {
		names = append(names, name)
	}
	sort.Strings(names)

	components := make([]ComponentStatus, 0, len(names))
	for _, name := range names {
		ready, detail := probes[name].Ready(ctx)
		components = append(components, ComponentStatus{Name: name, Ready: ready, Detail: detail})
	}
	return components
}

func (d *Daemon) handleRotatePairing(ctx context.Context) (PairingResult, error) {
	if d.opts.Pairing == nil {
		return PairingResult{}, errors.New("pairing rotation not configured")
	}
	return d.opts.Pairing.RotatePairing(ctx)
}

func (d *Daemon) handleStop(ctx context.Context) error {
	// Trigger shutdown asynchronously so the control response is delivered
	// before the socket closes.
	go func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = d.Shutdown(stopCtx)
	}()
	return nil
}

// Shutdown performs a graceful stop: marks health stopping, invokes OnStop,
// stops all children, closes the control socket, and updates runtime health. It
// is idempotent.
func (d *Daemon) Shutdown(ctx context.Context) error {
	var shutErr error
	d.stopOnce.Do(func() {
		d.setHealth(HealthStopping)
		_ = UpdateRuntimeHealth(RuntimePath(d.opts.StateDir), HealthStopping)

		if d.opts.OnStop != nil {
			d.opts.OnStop()
		}

		d.mu.Lock()
		children := append([]*ChildSupervisor(nil), d.children...)
		d.mu.Unlock()

		var wg sync.WaitGroup
		for _, sup := range children {
			wg.Add(1)
			go func(s *ChildSupervisor) {
				defer wg.Done()
				_ = s.Stop(ctx)
			}(sup)
		}
		waitWithCtx(ctx, &wg)

		if d.control != nil {
			if err := d.control.Close(); err != nil {
				shutErr = err
			}
		}

		// Release the exclusive state lock last, only after the socket is gone,
		// so another daemon cannot bind this state dir until teardown completes.
		if d.lock != nil {
			_ = d.lock.Release()
			d.lock = nil
		}
	})
	return shutErr
}

// Health returns the current aggregate health.
func (d *Daemon) Health() Health {
	return d.currentHealth()
}

func (d *Daemon) currentHealth() Health {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.health
}

func (d *Daemon) setHealth(h Health) {
	d.mu.Lock()
	d.health = h
	d.mu.Unlock()
}

// reconcileHealth atomically derives and stores the aggregate health from
// component readiness. It never overwrites HealthStopping, so a concurrent
// Shutdown cannot be clobbered by an in-flight status probe (the read-check-set
// happens under one lock).
func (d *Daemon) reconcileHealth(allReady bool) Health {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.health == HealthStopping {
		return d.health
	}
	if allReady {
		d.health = HealthReady
	} else {
		d.health = HealthDegraded
	}
	return d.health
}

func waitWithCtx(ctx context.Context, wg *sync.WaitGroup) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
