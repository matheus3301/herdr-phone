package tunnel

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/matheus3301/herdr-phone/internal/security"
)

// State is the observable lifecycle state of the supervised cloudflared child.
type State string

const (
	// StateIdle means the supervisor has not started yet.
	StateIdle State = "idle"
	// StateStarting means cloudflared is running but not yet ready.
	StateStarting State = "starting"
	// StateReady means cloudflared has an edge connection (named) or a
	// published Quick Tunnel URL (quick).
	StateReady State = "ready"
	// StateDegraded means cloudflared crashed too many times in a row and the
	// supervisor stopped retrying.
	StateDegraded State = "degraded"
	// StateStopped means the supervisor was asked to stop and has exited.
	StateStopped State = "stopped"
)

// procHandle is a single running cloudflared instance. process.go provides the
// real implementation; tests provide a fake.
type procHandle interface {
	ready() <-chan struct{}
	url() string
	done() <-chan error
	stop(ctx context.Context) error
	// pid returns the OS process id of the running child, or 0 if unavailable.
	pid() int
}

// runner starts a cloudflared instance. It is injected so the supervisor can be
// unit-tested without executing a binary.
type runner interface {
	start(ctx context.Context, c Config, tokenFile string, sink func(LogLine)) (procHandle, error)
}

type execRunner struct{}

func (execRunner) start(ctx context.Context, c Config, tokenFile string, sink func(LogLine)) (procHandle, error) {
	return startProcess(ctx, c, tokenFile, sink)
}

// Supervisor runs and restarts cloudflared with bounded exponential backoff. It
// manages per-attempt token files (deleting a temporary token immediately after
// readiness) and exposes readiness, URL, state, and recent logs.
type Supervisor struct {
	cfg    Config
	runner runner

	backoff         Backoff
	maxConsecutive  int
	stabilityWindow time.Duration
	pidDir          string
	logs            *ringBuffer

	// sleep waits for d or until ctx/stop is signalled; injectable for tests.
	// The default (defaultSleep) also observes stopCh so a graceful stop
	// interrupts a restart backoff instead of waiting out the full delay.
	sleep func(ctx context.Context, d time.Duration)
	// now returns the current time; injectable so the stability window is
	// testable without real sleeps.
	now func() time.Time

	mu    sync.Mutex
	state State
	url   string
	err   error

	readyCh   chan struct{}
	readyOnce sync.Once

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// Option configures a Supervisor.
type Option func(*Supervisor)

// WithBackoff overrides the default restart backoff policy.
func WithBackoff(b Backoff) Option {
	return func(s *Supervisor) { s.backoff = b }
}

// WithMaxConsecutiveFailures sets how many back-to-back failures (without an
// intervening readiness) move the supervisor to the degraded state. Zero or
// negative disables the cap.
func WithMaxConsecutiveFailures(n int) Option {
	return func(s *Supervisor) { s.maxConsecutive = n }
}

// WithLogCapacity bounds the number of retained recent log lines.
func WithLogCapacity(n int) Option {
	return func(s *Supervisor) { s.logs = newRingBuffer(n) }
}

// WithStabilityWindow overrides how long an instance must stay ready before its
// success resets the restart backoff and failure counter (flapping protection).
func WithStabilityWindow(d time.Duration) Option {
	return func(s *Supervisor) { s.stabilityWindow = d }
}

func withRunner(r runner) Option {
	return func(s *Supervisor) { s.runner = r }
}

func withSleeper(fn func(ctx context.Context, d time.Duration)) Option {
	return func(s *Supervisor) { s.sleep = fn }
}

func withClock(now func() time.Time) Option {
	return func(s *Supervisor) { s.now = now }
}

// New validates cfg and returns a Supervisor. Call Start to launch cloudflared.
func New(cfg Config, opts ...Option) (*Supervisor, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	s := &Supervisor{
		cfg:             cfg,
		runner:          execRunner{},
		backoff:         Backoff{Base: time.Second, Max: 30 * time.Second, Factor: 2, Jitter: 0.2},
		maxConsecutive:  5,
		stabilityWindow: cfg.StabilityWindow,
		pidDir:          pidDirFor(cfg),
		logs:            newRingBuffer(200),
		state:           StateIdle,
		readyCh:         make(chan struct{}),
		stopCh:          make(chan struct{}),
		doneCh:          make(chan struct{}),
		now:             time.Now,
	}
	// Default to a stop-aware sleeper (method value closes over stopCh).
	s.sleep = s.defaultSleep
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// pidDirFor resolves the directory for the cloudflared pidfile.
func pidDirFor(cfg Config) string {
	if cfg.PidDir != "" {
		return cfg.PidDir
	}
	if cfg.TokenDir != "" {
		return cfg.TokenDir
	}
	return os.TempDir()
}

// defaultSleep waits for d, or returns early when the serve context is done or a
// graceful stop is requested. As a method it observes stopCh, so a stop during a
// restart backoff interrupts the wait instead of delaying teardown.
func (s *Supervisor) defaultSleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-s.stopCh:
	case <-timer.C:
	}
}

// Start launches the supervise loop in a goroutine. It returns immediately; use
// Ready to wait for the first successful readiness.
func (s *Supervisor) Start(ctx context.Context) {
	s.setState(StateStarting)
	go s.run(ctx)
}

func (s *Supervisor) run(ctx context.Context) {
	defer close(s.doneCh)
	// A cloudflared recorded by a prior daemon that died abnormally (see
	// MacOSKillWindowNote) is terminated here — only after verifying identity —
	// before we start a fresh child.
	reconcileOrphan(s.pidDir, s.cfg.GracePeriod, s.sink)
	// On any exit (stopped/degraded) the child is gone; drop the pidfile so it is
	// never mistaken for an orphan on the next start.
	defer removePidRecord(s.pidDir)

	consecutive := 0
	for {
		if s.stopRequested() || ctx.Err() != nil {
			s.setState(StateStopped)
			return
		}

		tokenFile, cleanup, err := s.prepareToken(ctx)
		if err != nil {
			s.setErr(err)
			if done := s.afterFailure(ctx, &consecutive); done {
				return
			}
			continue
		}

		handle, err := s.runner.start(ctx, s.cfg, tokenFile, s.sink)
		if err != nil {
			cleanup()
			s.setErr(err)
			if done := s.afterFailure(ctx, &consecutive); done {
				return
			}
			continue
		}
		s.recordPid(handle)

		exitErr := s.waitAttempt(ctx, handle, cleanup, &consecutive)
		if exitErr == errStopped {
			s.setState(StateStopped)
			return
		}
		if s.stopRequested() || ctx.Err() != nil {
			s.setState(StateStopped)
			return
		}
		if done := s.afterFailure(ctx, &consecutive); done {
			return
		}
	}
}

var errStopped = errors.New("tunnel: supervisor stopped")

// waitAttempt blocks until the running instance becomes ready and then exits, or
// exits before readiness, or the supervisor/ctx is stopped. It deletes the
// temporary token file the moment readiness is observed. It returns errStopped
// when a deliberate stop occurred, otherwise the exit error (possibly nil).
func (s *Supervisor) waitAttempt(ctx context.Context, handle procHandle, cleanup func() error, consecutive *int) error {
	for {
		select {
		case <-handle.ready():
			// Token has been consumed by cloudflared; remove it immediately.
			_ = cleanup()
			s.mu.Lock()
			s.url = handle.url()
			s.state = StateReady
			s.err = nil
			s.mu.Unlock()
			s.signalReady()
			// Do NOT reset backoff/consecutive on the readiness edge. A
			// cloudflared that reaches readiness and then dies ~1s later would
			// otherwise reset every cycle and never degrade (flapping). Only a
			// run that stays ready past the stability window counts as success.
			readyAt := s.now()
			select {
			case err := <-handle.done():
				if s.now().Sub(readyAt) >= s.stabilityWindow {
					s.backoff.Reset()
					*consecutive = 0
				}
				return err
			case <-s.stopCh:
				return s.gracefulStop(handle, cleanup)
			case <-ctx.Done():
				return s.gracefulStop(handle, cleanup)
			}
		case err := <-handle.done():
			// Exited before ever becoming ready.
			_ = cleanup()
			s.setErr(err)
			return err
		case <-s.stopCh:
			return s.gracefulStop(handle, cleanup)
		case <-ctx.Done():
			return s.gracefulStop(handle, cleanup)
		}
	}
}

func (s *Supervisor) gracefulStop(handle procHandle, cleanup func() error) error {
	stopCtx, cancel := context.WithTimeout(context.Background(), s.cfg.GracePeriod+2*time.Second)
	defer cancel()
	_ = handle.stop(stopCtx)
	_ = cleanup()
	return errStopped
}

// afterFailure records a failed attempt, applies backoff, and reports whether
// the supervisor should give up (degraded) or has been stopped.
func (s *Supervisor) afterFailure(ctx context.Context, consecutive *int) (done bool) {
	if s.stopRequested() || ctx.Err() != nil {
		s.setState(StateStopped)
		return true
	}
	*consecutive++
	if s.maxConsecutive > 0 && *consecutive >= s.maxConsecutive {
		s.setState(StateDegraded)
		return true
	}
	s.setState(StateStarting)
	s.sleep(ctx, s.backoff.Next())
	return false
}

// prepareToken produces the token file path for this attempt. For the
// token-command strategy it runs the command and writes a temporary 0600 file;
// the returned cleanup removes it. For all other strategies it returns the
// configured path (or empty) and a no-op cleanup.
func (s *Supervisor) prepareToken(ctx context.Context) (path string, cleanup func() error, err error) {
	noop := func() error { return nil }
	if !s.cfg.usesTokenCommand() {
		return s.cfg.TokenFile, noop, nil
	}
	token, err := runTokenCommand(ctx, s.cfg.TokenCommand)
	if err != nil {
		return "", noop, err
	}
	return writeTokenFile(s.cfg.TokenDir, token)
}

func (s *Supervisor) sink(line LogLine) {
	if line.Raw == "" {
		return
	}
	// Layered defense: parseLogLine already stripped control characters; run the
	// stored line through the shared security redactor so any secret-shaped
	// substring (JWT/tunnel token/pairing) is removed before it can surface via
	// RecentLogs()/status.
	s.logs.add(security.SanitizeForLog(line.Raw))
}

// recordPid writes the running child's pidfile so a later start can reconcile it
// as an orphan if this daemon dies without unwinding.
func (s *Supervisor) recordPid(h procHandle) {
	pid := h.pid()
	if pid <= 0 {
		return
	}
	_ = writePidRecord(s.pidDir, pidRecord{
		PID:           pid,
		Binary:        s.cfg.Binary,
		CreatedUnixMs: s.now().UnixMilli(),
	})
}

// Ready returns a channel closed the first time cloudflared becomes ready.
func (s *Supervisor) Ready() <-chan struct{} { return s.readyCh }

func (s *Supervisor) signalReady() {
	s.readyOnce.Do(func() { close(s.readyCh) })
}

// URL returns the public URL: the configured named URL, or the discovered Quick
// Tunnel URL once available.
func (s *Supervisor) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.url
}

// State returns the current lifecycle state.
func (s *Supervisor) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Err returns the most recent non-fatal error, if any.
func (s *Supervisor) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// RecentLogs returns a bounded snapshot of recent sanitized cloudflared log
// lines. It never contains secrets: tokens are delivered via files, not logs.
func (s *Supervisor) RecentLogs() []string {
	return s.logs.snapshot()
}

// Stop requests a graceful shutdown and waits until the supervise loop exits or
// ctx is done.
func (s *Supervisor) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() { close(s.stopCh) })
	select {
	case <-s.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Done returns a channel closed when the supervise loop has fully exited.
func (s *Supervisor) Done() <-chan struct{} { return s.doneCh }

func (s *Supervisor) stopRequested() bool {
	select {
	case <-s.stopCh:
		return true
	default:
		return false
	}
}

func (s *Supervisor) setState(st State) {
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()
}

func (s *Supervisor) setErr(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}
