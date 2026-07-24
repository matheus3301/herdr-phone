package daemon

import (
	"context"
	"math"
	"sync"
	"time"
)

// Child is a supervised subsystem. Implementations wrap a long-lived resource
// (for example the cloudflared tunnel supervisor or the Herdr event
// subscription) behind this small interface so the daemon can supervise them
// uniformly without importing their packages.
type Child interface {
	// Name is a short, stable identifier used in status and logs.
	Name() string
	// Start begins the child's work. It should return quickly; long-running
	// work belongs in the goroutine backing Wait.
	Start(ctx context.Context) error
	// Wait blocks until the child stops on its own, returning nil for a clean
	// stop or an error describing the failure. It must unblock when Stop is
	// called.
	Wait() error
	// Stop requests a graceful shutdown, bounded by ctx.
	Stop(ctx context.Context) error
}

// SupervisorState is the observable state of a child supervisor.
type SupervisorState string

const (
	// SupStopped means the supervisor has not started or has fully stopped.
	SupStopped SupervisorState = "stopped"
	// SupRunning means the child is running.
	SupRunning SupervisorState = "running"
	// SupRestarting means the child failed and a restart is pending.
	SupRestarting SupervisorState = "restarting"
	// SupDegraded means the child failed too many times in a row and the
	// supervisor gave up.
	SupDegraded SupervisorState = "degraded"
)

// RestartPolicy bounds automatic restarts.
type RestartPolicy struct {
	Base           time.Duration // first restart delay; defaults to 1s
	Max            time.Duration // ceiling for a restart delay; defaults to 30s
	Factor         float64       // growth per consecutive failure; defaults to 2
	MaxConsecutive int           // failures before degraded; <=0 disables the cap
}

func (p RestartPolicy) delay(attempt int) time.Duration {
	base := p.Base
	if base <= 0 {
		base = time.Second
	}
	factor := p.Factor
	if factor <= 1 {
		factor = 2
	}
	max := p.Max
	if max <= 0 {
		max = 30 * time.Second
	}
	d := float64(base) * math.Pow(factor, float64(attempt))
	if d > float64(max) {
		d = float64(max)
	}
	return time.Duration(d)
}

// ChildSupervisor runs one Child, restarting it on failure with bounded
// exponential backoff and marking itself degraded after too many consecutive
// failures.
type ChildSupervisor struct {
	child  Child
	policy RestartPolicy

	// sleep waits for d or ctx cancellation; injectable for tests.
	sleep func(ctx context.Context, d time.Duration)
	// onRestart is an optional observer invoked before each restart delay.
	onRestart func(attempt int, err error)

	mu    sync.Mutex
	state SupervisorState
	err   error

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// SupervisorOption configures a ChildSupervisor.
type SupervisorOption func(*ChildSupervisor)

// WithSleeper injects the delay function (used by tests for determinism).
func WithSleeper(fn func(ctx context.Context, d time.Duration)) SupervisorOption {
	return func(s *ChildSupervisor) { s.sleep = fn }
}

// WithRestartObserver registers a callback invoked before each restart delay.
func WithRestartObserver(fn func(attempt int, err error)) SupervisorOption {
	return func(s *ChildSupervisor) { s.onRestart = fn }
}

// NewChildSupervisor creates a supervisor for child with the given policy.
func NewChildSupervisor(child Child, policy RestartPolicy, opts ...SupervisorOption) *ChildSupervisor {
	s := &ChildSupervisor{
		child:  child,
		policy: policy,
		state:  SupStopped,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	// Default to a stop-aware sleeper so a graceful stop interrupts a restart
	// backoff instead of delaying teardown up to the backoff cap.
	s.sleep = s.defaultSleep
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// defaultSleep waits for d, returning early when ctx is done or a stop is
// requested. As a method it observes stopCh, which Stop closes without
// cancelling the serve context.
func (s *ChildSupervisor) defaultSleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-s.stopCh:
	case <-t.C:
	}
}

// Run supervises the child until ctx is cancelled, Stop is called, or the child
// becomes degraded. It blocks; callers typically run it in a goroutine.
func (s *ChildSupervisor) Run(ctx context.Context) {
	defer close(s.doneCh)

	consecutive := 0
	for {
		if s.shouldStop(ctx) {
			s.set(SupStopped, nil)
			return
		}

		if err := s.child.Start(ctx); err != nil {
			s.set(SupRestarting, err)
			if s.giveUpOrWait(ctx, &consecutive, err) {
				return
			}
			continue
		}
		s.set(SupRunning, nil)

		waitErr := s.awaitExit(ctx)
		if waitErr == errDeliberateStop {
			s.set(SupStopped, nil)
			return
		}
		if s.shouldStop(ctx) {
			s.set(SupStopped, nil)
			return
		}
		// The child exited on its own (clean or error). A clean exit still
		// triggers a restart unless it was a deliberate stop, because a
		// long-lived subsystem exiting means the daemon is no longer serving.
		s.set(SupRestarting, waitErr)
		if s.giveUpOrWait(ctx, &consecutive, waitErr) {
			return
		}
	}
}

var errDeliberateStop = context.Canceled

// awaitExit waits for the child to exit or for a stop request. On stop it asks
// the child to stop and returns errDeliberateStop.
func (s *ChildSupervisor) awaitExit(ctx context.Context) error {
	waitDone := make(chan error, 1)
	go func() { waitDone <- s.child.Wait() }()

	select {
	case err := <-waitDone:
		return err
	case <-s.stopCh:
		s.stopChild()
		<-waitDone
		return errDeliberateStop
	case <-ctx.Done():
		s.stopChild()
		<-waitDone
		return errDeliberateStop
	}
}

func (s *ChildSupervisor) stopChild() {
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = s.child.Stop(stopCtx)
}

// giveUpOrWait records a failure and either degrades (returns true) or waits the
// backoff delay before the next attempt.
func (s *ChildSupervisor) giveUpOrWait(ctx context.Context, consecutive *int, err error) (done bool) {
	if s.shouldStop(ctx) {
		s.set(SupStopped, nil)
		return true
	}
	attempt := *consecutive
	*consecutive++
	if s.policy.MaxConsecutive > 0 && *consecutive >= s.policy.MaxConsecutive {
		s.set(SupDegraded, err)
		return true
	}
	if s.onRestart != nil {
		s.onRestart(attempt, err)
	}
	s.sleep(ctx, s.policy.delay(attempt))
	return false
}

// Stop requests a graceful shutdown and waits until Run exits or ctx is done.
func (s *ChildSupervisor) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() { close(s.stopCh) })
	select {
	case <-s.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Done returns a channel closed when Run has exited.
func (s *ChildSupervisor) Done() <-chan struct{} { return s.doneCh }

// State returns the current supervisor state.
func (s *ChildSupervisor) State() SupervisorState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Err returns the most recent child error, if any.
func (s *ChildSupervisor) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *ChildSupervisor) shouldStop(ctx context.Context) bool {
	select {
	case <-s.stopCh:
		return true
	default:
	}
	return ctx.Err() != nil
}

func (s *ChildSupervisor) set(state SupervisorState, err error) {
	s.mu.Lock()
	s.state = state
	if err != nil {
		s.err = err
	}
	s.mu.Unlock()
}
