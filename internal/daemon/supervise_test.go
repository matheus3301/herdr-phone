package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeChild is a scripted Child for supervisor tests.
type fakeChild struct {
	mu         sync.Mutex
	starts     int
	startErr   func(attempt int) error
	waitResult func(attempt int) error

	waitCh  chan error
	stopped chan struct{}
}

func newFakeChild() *fakeChild {
	return &fakeChild{
		waitCh:  make(chan error, 1),
		stopped: make(chan struct{}),
	}
}

func (c *fakeChild) Name() string { return "fake" }

func (c *fakeChild) Start(ctx context.Context) error {
	c.mu.Lock()
	attempt := c.starts
	c.starts++
	c.mu.Unlock()

	if c.startErr != nil {
		if err := c.startErr(attempt); err != nil {
			return err
		}
	}
	// Fresh wait channel per start.
	c.mu.Lock()
	c.waitCh = make(chan error, 1)
	c.mu.Unlock()

	if c.waitResult != nil {
		if err := c.waitResult(attempt); err != nil {
			c.pushWait(err)
		}
	}
	return nil
}

func (c *fakeChild) pushWait(err error) {
	c.mu.Lock()
	ch := c.waitCh
	c.mu.Unlock()
	select {
	case ch <- err:
	default:
	}
}

func (c *fakeChild) Wait() error {
	c.mu.Lock()
	ch := c.waitCh
	c.mu.Unlock()
	return <-ch
}

func (c *fakeChild) Stop(ctx context.Context) error {
	select {
	case <-c.stopped:
	default:
		close(c.stopped)
	}
	c.pushWait(nil)
	return nil
}

func (c *fakeChild) startCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.starts
}

func daemonNoSleep(ctx context.Context, d time.Duration) {}

func TestChildSupervisorRunsAndStops(t *testing.T) {
	t.Parallel()
	child := newFakeChild()
	sup := NewChildSupervisor(child, RestartPolicy{MaxConsecutive: 3}, WithSleeper(daemonNoSleep))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	waitState(t, sup, SupRunning)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := sup.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if sup.State() != SupStopped {
		t.Errorf("state = %q, want stopped", sup.State())
	}
}

func TestChildSupervisorRestartsOnExit(t *testing.T) {
	t.Parallel()
	child := newFakeChild()
	child.waitResult = func(attempt int) error {
		if attempt == 0 {
			return errors.New("crashed")
		}
		return nil // second attempt stays running
	}
	sup := NewChildSupervisor(child, RestartPolicy{MaxConsecutive: 5}, WithSleeper(daemonNoSleep))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	// Should restart and reach running on the second attempt.
	waitCond(t, func() bool { return child.startCount() >= 2 })
	waitState(t, sup, SupRunning)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	_ = sup.Stop(stopCtx)
}

func TestChildSupervisorDegrades(t *testing.T) {
	t.Parallel()
	child := newFakeChild()
	child.waitResult = func(attempt int) error { return errors.New("always crashes") }
	restarts := 0
	sup := NewChildSupervisor(child, RestartPolicy{MaxConsecutive: 3},
		WithSleeper(daemonNoSleep),
		WithRestartObserver(func(attempt int, err error) { restarts++ }),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	waitState(t, sup, SupDegraded)
	<-sup.Done()
	if child.startCount() != 3 {
		t.Errorf("start count = %d, want 3", child.startCount())
	}
	if sup.Err() == nil {
		t.Error("degraded supervisor should retain the last error")
	}
}

func TestChildSupervisorStartErrorRestarts(t *testing.T) {
	t.Parallel()
	child := newFakeChild()
	child.startErr = func(attempt int) error {
		if attempt < 2 {
			return errors.New("start failed")
		}
		return nil
	}
	sup := NewChildSupervisor(child, RestartPolicy{MaxConsecutive: 5}, WithSleeper(daemonNoSleep))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	waitState(t, sup, SupRunning)
	if child.startCount() < 3 {
		t.Errorf("expected at least 3 start attempts, got %d", child.startCount())
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	_ = sup.Stop(stopCtx)
}

func TestChildSupervisorContextCancelStops(t *testing.T) {
	t.Parallel()
	child := newFakeChild()
	sup := NewChildSupervisor(child, RestartPolicy{MaxConsecutive: 3}, WithSleeper(daemonNoSleep))
	ctx, cancel := context.WithCancel(context.Background())
	go sup.Run(ctx)
	waitState(t, sup, SupRunning)
	cancel()
	<-sup.Done()
	if sup.State() != SupStopped {
		t.Errorf("state after ctx cancel = %q", sup.State())
	}
}

// TestChildSupervisorStopInterruptsBackoff reproduces M5 on the daemon side: a
// stop during a restart backoff sleep returns promptly instead of waiting out
// the backoff cap.
func TestChildSupervisorStopInterruptsBackoff(t *testing.T) {
	t.Parallel()
	child := newFakeChild()
	child.waitResult = func(attempt int) error { return errors.New("always crashes") }
	// Real (stop-aware) sleeper via the default; long base backoff.
	sup := NewChildSupervisor(child, RestartPolicy{Base: 5 * time.Second, Max: 30 * time.Second, MaxConsecutive: 100})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	// Wait until it has failed at least once and is in a backoff sleep.
	waitCond(t, func() bool { return child.startCount() >= 1 && sup.State() == SupRestarting })

	start := time.Now()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	if err := sup.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Stop waited out the backoff (%v); it must interrupt on stop", elapsed)
	}
}

func TestRestartPolicyDelay(t *testing.T) {
	t.Parallel()
	p := RestartPolicy{Base: time.Second, Max: 10 * time.Second, Factor: 2}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second}
	for i, w := range want {
		if got := p.delay(i); got != w {
			t.Errorf("delay(%d) = %v, want %v", i, got, w)
		}
	}
}

func waitState(t *testing.T, sup *ChildSupervisor, want SupervisorState) {
	t.Helper()
	waitCond(t, func() bool { return sup.State() == want })
}

func waitCond(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
