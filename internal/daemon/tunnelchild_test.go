package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeTunnel is a scripted tunnelController for adapter tests. It mirrors the
// behaviour of *tunnel.Supervisor: Start launches once, Ready/Done are one-shot
// channels, and Stop drives the controller to a terminal (Done) state.
type fakeTunnel struct {
	mu       sync.Mutex
	started  int
	url      string
	err      error
	readyCh  chan struct{}
	doneCh   chan struct{}
	logs     []string
	stopOnce sync.Once
}

func newFakeTunnel() *fakeTunnel {
	return &fakeTunnel{
		readyCh: make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

func (f *fakeTunnel) Start(ctx context.Context) {
	f.mu.Lock()
	f.started++
	f.mu.Unlock()
}

func (f *fakeTunnel) Ready() <-chan struct{} { return f.readyCh }
func (f *fakeTunnel) Done() <-chan struct{}  { return f.doneCh }

func (f *fakeTunnel) Stop(ctx context.Context) error {
	f.stopOnce.Do(func() { close(f.doneCh) })
	return nil
}

func (f *fakeTunnel) URL() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.url
}

func (f *fakeTunnel) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *fakeTunnel) RecentLogs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.logs...)
}

func (f *fakeTunnel) markReady(url string) {
	f.mu.Lock()
	f.url = url
	f.mu.Unlock()
	close(f.readyCh)
}

func (f *fakeTunnel) fail(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
	f.stopOnce.Do(func() { close(f.doneCh) })
}

func (f *fakeTunnel) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

func TestTunnelChildImplementsInterfaces(t *testing.T) {
	t.Parallel()
	var _ Child = (*TunnelChild)(nil)
	var _ ReadinessProbe = (*TunnelChild)(nil)
}

func TestTunnelChildStartIsIdempotent(t *testing.T) {
	t.Parallel()
	ft := newFakeTunnel()
	tc := NewTunnelChild(ft)
	if err := tc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := tc.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if ft.startCount() != 1 {
		t.Errorf("controller started %d times, want 1", ft.startCount())
	}
}

func TestTunnelChildReadinessStates(t *testing.T) {
	t.Parallel()
	ft := newFakeTunnel()
	tc := NewTunnelChild(ft)

	if ready, detail := tc.Ready(context.Background()); ready || detail != "starting" {
		t.Errorf("initial readiness = (%v, %q), want (false, starting)", ready, detail)
	}

	ft.markReady("https://named.example.com")
	ready, detail := tc.Ready(context.Background())
	if !ready {
		t.Errorf("expected ready after markReady")
	}
	if detail != "connected: https://named.example.com" {
		t.Errorf("detail = %q", detail)
	}
}

func TestTunnelChildReadinessDegraded(t *testing.T) {
	t.Parallel()
	ft := newFakeTunnel()
	tc := NewTunnelChild(ft)
	ft.fail(errors.New("cloudflared crash loop"))

	ready, detail := tc.Ready(context.Background())
	if ready {
		t.Errorf("degraded tunnel must not be ready")
	}
	if detail == "" || detail == "starting" {
		t.Errorf("degraded detail = %q, want an unavailable message", detail)
	}
}

func TestTunnelChildWaitReturnsErrorOnTerminalExit(t *testing.T) {
	t.Parallel()
	ft := newFakeTunnel()
	tc := NewTunnelChild(ft)

	done := make(chan error, 1)
	go func() { done <- tc.Wait() }()

	wantErr := errors.New("degraded")
	ft.fail(wantErr)

	select {
	case err := <-done:
		if !errors.Is(err, wantErr) {
			t.Errorf("Wait err = %v, want %v", err, wantErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after terminal exit")
	}
}

func TestTunnelChildStopUnblocksWait(t *testing.T) {
	t.Parallel()
	ft := newFakeTunnel()
	tc := NewTunnelChild(ft)

	done := make(chan error, 1)
	go func() { done <- tc.Wait() }()

	if err := tc.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("clean stop Wait err = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after Stop")
	}
}

// TestTunnelChildUnderChildSupervisorNoRestart verifies the intended production
// wiring: a non-restarting ChildSupervisor starts the tunnel exactly once and,
// on the tunnel's terminal exit, degrades without re-starting it.
func TestTunnelChildUnderChildSupervisorNoRestart(t *testing.T) {
	t.Parallel()
	ft := newFakeTunnel()
	tc := NewTunnelChild(ft)
	sup := NewChildSupervisor(tc, RestartPolicy{MaxConsecutive: 1}, WithSleeper(daemonNoSleep))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	waitState(t, sup, SupRunning)
	ft.markReady("https://h")

	// Tunnel terminally fails; the outer supervisor must degrade, not restart.
	ft.mu.Lock()
	ft.err = errors.New("terminal")
	ft.mu.Unlock()
	ft.stopOnce.Do(func() { close(ft.doneCh) })

	waitState(t, sup, SupDegraded)
	<-sup.Done()
	if ft.startCount() != 1 {
		t.Errorf("tunnel started %d times, want exactly 1 (no restart)", ft.startCount())
	}
}

func TestDaemonAddTunnelIntegration(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)
	ft := newFakeTunnel()
	tc := NewTunnelChild(ft)

	d := New(baseOptions(dir))
	d.AddTunnel(tc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Serve(ctx); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	defer d.Shutdown(context.Background())

	client, err := NewClientForStateDir(dir)
	if err != nil {
		t.Fatalf("NewClientForStateDir: %v", err)
	}

	// Before readiness: tunnel probe present and not ready -> degraded overall.
	st, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	found := false
	for _, c := range st.Components {
		if c.Name == "tunnel" {
			found = true
			if c.Ready {
				t.Errorf("tunnel should not be ready yet")
			}
		}
	}
	if !found {
		t.Fatalf("tunnel probe not registered; components=%+v", st.Components)
	}
	if st.Health != HealthDegraded {
		t.Errorf("health = %q, want degraded before tunnel ready", st.Health)
	}

	// After readiness: tunnel ready -> overall ready and URL propagated.
	ft.markReady("https://named.example.com")
	st, err = client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status after ready: %v", err)
	}
	if st.Health != HealthReady {
		t.Errorf("health = %q, want ready", st.Health)
	}
	for _, c := range st.Components {
		if c.Name == "tunnel" && !c.Ready {
			t.Errorf("tunnel component should be ready, detail=%q", c.Detail)
		}
	}

	// Graceful shutdown stops the tunnel child.
	if err := d.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case <-ft.doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("tunnel was not stopped on shutdown")
	}
}
