package tunnel

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeHandle is a scripted procHandle for supervisor unit tests.
type fakeHandle struct {
	readyCh chan struct{}
	doneCh  chan error
	urlVal  string
	pidVal  int
	stopped chan struct{}
}

func newFakeHandle(url string) *fakeHandle {
	return &fakeHandle{
		readyCh: make(chan struct{}),
		doneCh:  make(chan error, 1),
		urlVal:  url,
		stopped: make(chan struct{}),
	}
}

func (f *fakeHandle) ready() <-chan struct{} { return f.readyCh }
func (f *fakeHandle) url() string            { return f.urlVal }
func (f *fakeHandle) done() <-chan error     { return f.doneCh }
func (f *fakeHandle) pid() int               { return f.pidVal }
func (f *fakeHandle) stop(ctx context.Context) error {
	select {
	case <-f.stopped:
	default:
		close(f.stopped)
	}
	select {
	case f.doneCh <- nil:
	default:
	}
	return nil
}

// fakeRunner returns scripted handles and records token files it was given.
type fakeRunner struct {
	mu       sync.Mutex
	handles  []*fakeHandle
	tokens   []string
	starts   int
	makeNext func(attempt int) (*fakeHandle, error)
}

func (r *fakeRunner) start(ctx context.Context, c Config, tokenFile string, sink func(LogLine)) (procHandle, error) {
	r.mu.Lock()
	attempt := r.starts
	r.starts++
	r.tokens = append(r.tokens, tokenFile)
	r.mu.Unlock()

	h, err := r.makeNext(attempt)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.handles = append(r.handles, h)
	r.mu.Unlock()
	return h, nil
}

func (r *fakeRunner) startCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts
}

func noSleep(ctx context.Context, d time.Duration) {}

func TestSupervisorReachesReady(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{makeNext: func(attempt int) (*fakeHandle, error) {
		return newFakeHandle("https://named.example.com"), nil
	}}
	cfg := Config{Mode: ModeNamed, PublicURL: "https://named.example.com", ConfigFile: "/x", LoopbackPort: 8787}
	s, err := New(cfg, withRunner(r), withSleeper(noSleep), WithMaxConsecutiveFailures(3))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	// Drive the first handle to ready.
	waitFor(t, func() bool { return r.startCount() >= 1 })
	r.mu.Lock()
	h := r.handles[0]
	r.mu.Unlock()
	close(h.readyCh)

	select {
	case <-s.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor never became ready")
	}
	if s.State() != StateReady {
		t.Errorf("state = %q, want ready", s.State())
	}
	if s.URL() != "https://named.example.com" {
		t.Errorf("url = %q", s.URL())
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	if err := s.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if s.State() != StateStopped {
		t.Errorf("state after stop = %q", s.State())
	}
}

func TestSupervisorRestartsOnCrashThenReady(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{makeNext: func(attempt int) (*fakeHandle, error) {
		h := newFakeHandle("https://named.example.com")
		if attempt == 0 {
			// Crash immediately before readiness.
			h.doneCh <- errors.New("boom")
		}
		return h, nil
	}}
	cfg := Config{Mode: ModeNamed, PublicURL: "https://named.example.com", ConfigFile: "/x", LoopbackPort: 8787}
	s, _ := New(cfg, withRunner(r), withSleeper(noSleep), WithMaxConsecutiveFailures(5))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	// Second attempt should be started; drive it to ready.
	waitFor(t, func() bool { return r.startCount() >= 2 })
	r.mu.Lock()
	h := r.handles[1]
	r.mu.Unlock()
	close(h.readyCh)

	select {
	case <-s.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor never recovered to ready")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	_ = s.Stop(stopCtx)
}

func TestSupervisorDegradesAfterMaxFailures(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{makeNext: func(attempt int) (*fakeHandle, error) {
		h := newFakeHandle("")
		h.doneCh <- errors.New("always fails")
		return h, nil
	}}
	cfg := Config{Mode: ModeNamed, PublicURL: "https://h", ConfigFile: "/x", LoopbackPort: 8787}
	s, _ := New(cfg, withRunner(r), withSleeper(noSleep), WithMaxConsecutiveFailures(3))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	waitFor(t, func() bool { return s.State() == StateDegraded })
	if s.State() != StateDegraded {
		t.Fatalf("state = %q, want degraded", s.State())
	}
	// It stopped retrying: start count is bounded by the failure cap.
	if got := r.startCount(); got != 3 {
		t.Errorf("start count = %d, want 3", got)
	}
	<-s.Done()
}

func TestSupervisorTokenCommandFileDeletedAfterReady(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{makeNext: func(attempt int) (*fakeHandle, error) {
		return newFakeHandle("https://h"), nil
	}}
	dir := t.TempDir()
	cfg := Config{
		Mode:         ModeNamed,
		PublicURL:    "https://h",
		TokenCommand: []string{"printf", "the-secret-token"},
		TokenDir:     dir,
		LoopbackPort: 8787,
	}
	s, err := New(cfg, withRunner(r), withSleeper(noSleep))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	waitFor(t, func() bool { return r.startCount() >= 1 })
	r.mu.Lock()
	tokenPath := r.tokens[0]
	h := r.handles[0]
	r.mu.Unlock()

	if tokenPath == "" {
		t.Fatal("expected a token file path for token command strategy")
	}
	// The secret path must never appear in argv-visible logs; here we assert the
	// token file existed then is removed after readiness.
	close(h.readyCh)
	select {
	case <-s.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("never ready")
	}
	waitFor(t, func() bool { return !fileExists(tokenPath) })

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	_ = s.Stop(stopCtx)
}

// fixedClock returns a constant time, so a ready-then-crash instance always has
// a measured lifetime of zero (shorter than any stability window).
func fixedClock() func() time.Time {
	base := time.Unix(1_780_000_000, 0)
	return func() time.Time { return base }
}

// TestSupervisorFlappingDegrades reproduces H4: cloudflared reaches readiness
// then dies almost immediately, over and over. Without a stability window the
// readiness edge would reset the failure counter every cycle and never degrade.
// With the window, each short-lived run counts as a failure and the supervisor
// degrades after maxConsecutive.
func TestSupervisorFlappingDegrades(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{makeNext: func(attempt int) (*fakeHandle, error) {
		h := newFakeHandle("https://named.example.com")
		close(h.readyCh) // reach readiness...
		go func() {
			time.Sleep(5 * time.Millisecond) // ...then crash shortly after
			h.doneCh <- errors.New("edge dropped the connection")
		}()
		return h, nil
	}}
	cfg := Config{Mode: ModeNamed, PublicURL: "https://named.example.com", ConfigFile: "/x", LoopbackPort: 8787}
	s, err := New(cfg,
		withRunner(r),
		withSleeper(noSleep),
		withClock(fixedClock()),
		WithStabilityWindow(30*time.Second),
		WithMaxConsecutiveFailures(3),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	waitFor(t, func() bool { return s.State() == StateDegraded })
	<-s.Done()

	// It actually reached readiness (flapping, not just failing to start)...
	select {
	case <-s.Ready():
	default:
		t.Error("expected the flapping tunnel to have reached readiness at least once")
	}
	// ...yet still degraded after exactly maxConsecutive short-lived runs.
	if got := r.startCount(); got != 3 {
		t.Errorf("start count = %d, want 3 (degrade despite flapping readiness)", got)
	}
}

// TestSupervisorStableRunResetsFailures verifies the complement: a run that
// stays ready past the stability window resets the failure counter so a later
// crash does not accumulate toward degraded.
func TestSupervisorStableRunResetsFailures(t *testing.T) {
	t.Parallel()
	clock := &advanceableClock{t: time.Unix(1_780_000_000, 0)}
	var attempts int
	r := &fakeRunner{makeNext: func(attempt int) (*fakeHandle, error) {
		attempts++
		h := newFakeHandle("https://named.example.com")
		close(h.readyCh)
		go func() {
			// Let the supervisor observe readiness, advance the clock beyond the
			// window (a stable run), then crash.
			time.Sleep(5 * time.Millisecond)
			clock.advance(time.Minute)
			h.doneCh <- errors.New("later crash")
		}()
		return h, nil
	}}
	cfg := Config{Mode: ModeNamed, PublicURL: "https://named.example.com", ConfigFile: "/x", LoopbackPort: 8787}
	s, _ := New(cfg,
		withRunner(r),
		withSleeper(noSleep),
		withClock(clock.now),
		WithStabilityWindow(30*time.Second),
		WithMaxConsecutiveFailures(3),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	// Let several stable-then-crash cycles happen; each should reset, so it must
	// NOT degrade. Observe more restarts than maxConsecutive.
	waitFor(t, func() bool { return r.startCount() >= 5 })
	if s.State() == StateDegraded {
		t.Errorf("stable runs must not degrade; state=%q", s.State())
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	_ = s.Stop(stopCtx)
}

// TestSupervisorStopInterruptsBackoff reproduces M5: a graceful stop during a
// restart backoff sleep returns promptly instead of waiting out the backoff.
func TestSupervisorStopInterruptsBackoff(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{makeNext: func(attempt int) (*fakeHandle, error) {
		h := newFakeHandle("")
		h.doneCh <- errors.New("immediate failure") // fail before readiness
		return h, nil
	}}
	cfg := Config{Mode: ModeNamed, PublicURL: "https://h", ConfigFile: "/x", LoopbackPort: 8787}
	// Real (stop-aware) sleeper with a long base backoff.
	s, err := New(cfg,
		withRunner(r),
		WithBackoff(Backoff{Base: 5 * time.Second, Max: 30 * time.Second, Factor: 2}),
		WithMaxConsecutiveFailures(100),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	// Wait until at least one failure has occurred (so we are in a backoff sleep).
	waitFor(t, func() bool { return r.startCount() >= 1 })

	start := time.Now()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	if err := s.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Stop waited out the backoff (%v); it must interrupt on stop", elapsed)
	}
}

type advanceableClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *advanceableClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *advanceableClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func waitFor(t *testing.T, cond func() bool) {
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
