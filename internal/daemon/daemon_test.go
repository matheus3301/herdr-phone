package daemon

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func baseOptions(dir string) Options {
	return Options{
		StateDir: dir,
		Runtime: Runtime{
			PID:         os.Getpid(),
			InstanceID:  "inst-daemon",
			Mode:        "named",
			LocalAddr:   "127.0.0.1:8787",
			PublicURL:   "https://h.example.com",
			Version:     "0.1.0",
			StartUnixMs: 1780000000000,
			Health:      HealthStarting,
		},
	}
}

func TestDaemonServeWritesRuntimeAndSocket(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)
	d := New(baseOptions(dir))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Serve(ctx); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	defer d.Shutdown(context.Background())

	if _, err := os.Stat(RuntimePath(dir)); err != nil {
		t.Errorf("runtime not written: %v", err)
	}
	if _, err := os.Stat(ControlPath(dir)); err != nil {
		t.Errorf("control socket not created: %v", err)
	}
}

func TestDaemonStatusAggregation(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)
	opts := baseOptions(dir)
	opts.Probes = map[string]ReadinessProbe{
		"http":   ProbeFunc(func(ctx context.Context) (bool, string) { return true, "listening" }),
		"herdr":  ProbeFunc(func(ctx context.Context) (bool, string) { return true, "connected" }),
		"tunnel": ProbeFunc(func(ctx context.Context) (bool, string) { return false, "starting" }),
		"state":  ProbeFunc(func(ctx context.Context) (bool, string) { return true, "" }),
	}
	opts.ClientCount = func() int { return 2 }
	d := New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Serve(ctx); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	defer d.Shutdown(context.Background())

	client := NewClient(ControlPath(dir))
	st, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(st.Components) != 4 {
		t.Fatalf("components = %d, want 4", len(st.Components))
	}
	// tunnel is not ready, so overall health is degraded.
	if st.Health != HealthDegraded {
		t.Errorf("health = %q, want degraded", st.Health)
	}
	if st.ClientCount != 2 {
		t.Errorf("client count = %d, want 2", st.ClientCount)
	}
	// Components are sorted by name for stable output.
	if st.Components[0].Name != "herdr" {
		t.Errorf("first component = %q, want herdr (sorted)", st.Components[0].Name)
	}
}

func TestDaemonStatusReadyWhenAllProbesReady(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)
	opts := baseOptions(dir)
	opts.Probes = map[string]ReadinessProbe{
		"http": ProbeFunc(func(ctx context.Context) (bool, string) { return true, "ok" }),
	}
	d := New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Serve(ctx); err != nil {
		t.Fatal(err)
	}
	defer d.Shutdown(context.Background())

	st, err := NewClient(ControlPath(dir)).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Health != HealthReady {
		t.Errorf("health = %q, want ready", st.Health)
	}
}

func TestDaemonPairingRotationHook(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)
	opts := baseOptions(dir)
	rotations := 0
	opts.Pairing = PairingRotatorFunc(func(ctx context.Context) (PairingResult, error) {
		rotations++
		return PairingResult{URL: "https://h.example.com/#pair=fresh-secret"}, nil
	})
	d := New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Serve(ctx); err != nil {
		t.Fatal(err)
	}
	defer d.Shutdown(context.Background())

	pr, err := NewClient(ControlPath(dir)).RotatePairing(context.Background())
	if err != nil {
		t.Fatalf("RotatePairing: %v", err)
	}
	if pr.URL != "https://h.example.com/#pair=fresh-secret" {
		t.Errorf("pairing url = %q", pr.URL)
	}
	if rotations != 1 {
		t.Errorf("rotations = %d, want 1", rotations)
	}
}

func TestDaemonGracefulShutdown(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)
	opts := baseOptions(dir)
	stopped := make(chan struct{}, 1)
	opts.OnStop = func() { stopped <- struct{}{} }

	d := New(opts)

	// Register a child that must be stopped during shutdown.
	child := newFakeChild()
	sup := NewChildSupervisor(child, RestartPolicy{MaxConsecutive: 3}, WithSleeper(daemonNoSleep))
	d.AddChild(sup)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Serve(ctx); err != nil {
		t.Fatal(err)
	}
	waitCond(t, func() bool { return sup.State() == SupRunning })

	if err := d.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("OnStop was not invoked")
	}
	if d.Health() != HealthStopping {
		t.Errorf("health = %q, want stopping", d.Health())
	}
	if sup.State() != SupStopped {
		t.Errorf("child supervisor state = %q, want stopped", sup.State())
	}
	// Control socket removed.
	if _, err := os.Stat(ControlPath(dir)); !os.IsNotExist(err) {
		t.Errorf("socket should be removed, stat err = %v", err)
	}
	// Runtime health persisted as stopping.
	rt, err := LoadRuntime(RuntimePath(dir))
	if err != nil {
		t.Fatalf("LoadRuntime: %v", err)
	}
	if rt.Health != HealthStopping {
		t.Errorf("persisted health = %q, want stopping", rt.Health)
	}
	// Shutdown is idempotent.
	if err := d.Shutdown(context.Background()); err != nil {
		t.Errorf("second Shutdown: %v", err)
	}
}

// TestDaemonConcurrentServeIsExclusive reproduces M7: two daemons on the same
// state dir cannot both serve. The second Serve must fail (state lock held) and
// must not clobber the first's control socket or runtime.json.
func TestDaemonConcurrentServeIsExclusive(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)

	d1 := New(baseOptions(dir))
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	if err := d1.Serve(ctx1); err != nil {
		t.Fatalf("first Serve: %v", err)
	}

	d2 := New(baseOptions(dir))
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	err := d2.Serve(ctx2)
	if !errors.Is(err, ErrStateLocked) {
		t.Fatalf("second Serve err = %v, want ErrStateLocked", err)
	}

	// The first daemon is still reachable (its socket was not clobbered).
	if _, err := NewClientForStateDir(dir); err != nil {
		t.Fatalf("client: %v", err)
	}
	client, _ := NewClientForStateDir(dir)
	if _, err := client.Status(context.Background()); err != nil {
		t.Fatalf("first daemon must remain reachable: %v", err)
	}

	// After the first shuts down and releases the lock, a new daemon can serve.
	if err := d1.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	d3 := New(baseOptions(dir))
	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()
	if err := d3.Serve(ctx3); err != nil {
		t.Fatalf("Serve after release: %v", err)
	}
	_ = d3.Shutdown(context.Background())
}

// TestDaemonStopResponseDelivered reproduces L6: a stop request over the control
// socket receives its ok response even though stop triggers asynchronous
// shutdown that closes the server.
func TestDaemonStopResponseDelivered(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)
	d := New(baseOptions(dir))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Serve(ctx); err != nil {
		t.Fatal(err)
	}

	client, err := NewClientForStateDir(dir)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	// Stop must return nil (the ok reply was flushed before teardown), not a
	// broken-connection error.
	if err := client.Stop(context.Background()); err != nil {
		t.Fatalf("Stop response was lost to the shutdown race: %v", err)
	}

	waitCond(t, func() bool { return d.Health() == HealthStopping })
	waitCond(t, func() bool {
		_, statErr := os.Stat(ControlPath(dir))
		spath, _ := SocketPath(dir)
		_, sErr := os.Stat(spath)
		return os.IsNotExist(statErr) && os.IsNotExist(sErr)
	})
}

func TestDaemonStopViaControlSocket(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)
	opts := baseOptions(dir)
	d := New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Serve(ctx); err != nil {
		t.Fatal(err)
	}

	if err := NewClient(ControlPath(dir)).Stop(context.Background()); err != nil {
		t.Fatalf("control stop: %v", err)
	}
	// Shutdown runs asynchronously from the stop handler; wait for it.
	waitCond(t, func() bool { return d.Health() == HealthStopping })
	waitCond(t, func() bool {
		_, err := os.Stat(ControlPath(dir))
		return os.IsNotExist(err)
	})
}
