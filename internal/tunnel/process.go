//go:build unix

package tunnel

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// maxCapturedLineBytes bounds a single captured cloudflared log line. A longer
// line is truncated and the remainder up to the next newline is discarded, so a
// pathological line can never exhaust memory nor permanently stop log capture.
const maxCapturedLineBytes = 64 * 1024

// realProcess is a single cloudflared run. It owns the child process group,
// derives readiness/URL from structured logs, and shuts the group down with a
// graceful SIGTERM followed by a SIGKILL backstop.
type realProcess struct {
	cmd   *exec.Cmd
	grace time.Duration

	readyCh   chan struct{}
	readyOnce sync.Once

	doneCh chan error

	mu        sync.Mutex
	publicURL string

	cancel context.CancelFunc
}

// startProcess launches cloudflared with the supplied config and (optional)
// token file path. sink receives every parsed log line for buffering. The
// returned procHandle exposes readiness, URL, exit, and shutdown.
func startProcess(parent context.Context, c Config, tokenFilePath string, sink func(LogLine)) (procHandle, error) {
	args, err := buildArgs(c, tokenFilePath)
	if err != nil {
		return nil, err
	}

	// A dedicated child context lets Stop/Cancel escalate to a forced kill via
	// WaitDelay without disturbing the caller's context.
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, c.Binary, args...)
	// Put the child in its own process group so we can signal the whole group
	// (cloudflared plus any helper it spawns) with a single kill.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// When the context is cancelled, force-kill the whole group. Graceful
	// termination is handled explicitly by stop(); this is the backstop path.
	cmd.Cancel = func() error {
		return killGroup(cmd, syscall.SIGKILL)
	}
	// WaitDelay bounds how long Wait blocks for I/O after the process exits (or
	// after ctx is done). Critically, it lets Wait return even when a descendant
	// that escaped the process group keeps the stdout/stderr pipe open: Wait no
	// longer depends on the log copiers reaching EOF.
	cmd.WaitDelay = 2 * time.Second

	p := &realProcess{
		cmd:       cmd,
		grace:     c.GracePeriod,
		readyCh:   make(chan struct{}),
		doneCh:    make(chan error, 1),
		publicURL: namedURL(c),
		cancel:    cancel,
	}

	quickMode := c.Mode == ModeQuick
	onLine := func(b []byte) { p.handleLine(b, sink, quickMode) }
	// Setting cmd.Stdout/Stderr to writers (rather than using StdoutPipe) makes
	// the os/exec-managed copiers subject to WaitDelay, so cmd.Wait() can run
	// independently of log draining.
	stdoutW := newLineWriter(onLine, maxCapturedLineBytes)
	stderrW := newLineWriter(onLine, maxCapturedLineBytes)
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("tunnel: start cloudflared: %w", redactExecError(err))
	}

	go func() {
		// Wait independently of the log writers; WaitDelay bounds any lingering
		// I/O so a stuck descendant cannot block reaping the leader.
		err := cmd.Wait()
		// Flush any trailing partial line the copiers left buffered.
		stdoutW.flush()
		stderrW.flush()
		cancel()
		p.doneCh <- err
		close(p.doneCh)
	}()

	return p, nil
}

func (p *realProcess) handleLine(b []byte, sink func(LogLine), quickMode bool) {
	line := parseLogLine(b)
	if sink != nil {
		sink(line)
	}
	ev := classify(line)
	switch ev.kind {
	case evQuickURL:
		p.mu.Lock()
		if p.publicURL == "" {
			p.publicURL = ev.url
		}
		p.mu.Unlock()
		if quickMode {
			p.markReady()
		}
	case evConnRegistered:
		if !quickMode {
			p.markReady()
		}
	}
}

func (p *realProcess) markReady() {
	p.readyOnce.Do(func() { close(p.readyCh) })
}

func (p *realProcess) ready() <-chan struct{} { return p.readyCh }

func (p *realProcess) url() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.publicURL
}

func (p *realProcess) done() <-chan error { return p.doneCh }

func (p *realProcess) pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// stop performs a graceful, group-wide shutdown: SIGTERM the process group, wait
// up to the grace period (or until ctx is done), then SIGKILL the group as a
// backstop. It always cancels the child context so exec's own WaitDelay fires
// even if signalling races.
func (p *realProcess) stop(ctx context.Context) error {
	if p.cmd.Process == nil {
		p.cancel()
		return nil
	}

	_ = killGroup(p.cmd, syscall.SIGTERM)

	grace := p.grace
	if grace <= 0 {
		grace = defaultGracePeriod
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()

	select {
	case err := <-p.doneCh:
		p.cancel()
		return exitError(err)
	case <-timer.C:
	case <-ctx.Done():
	}

	// Grace expired or caller cancelled: force-kill the whole group.
	_ = killGroup(p.cmd, syscall.SIGKILL)
	p.cancel()

	select {
	case err := <-p.doneCh:
		return exitError(err)
	case <-time.After(2 * time.Second):
		return fmt.Errorf("tunnel: cloudflared did not exit after kill")
	}
}

// killGroup signals the child's whole process group. Setpgid means the group id
// equals the child pid, so a negative pid targets the group.
func killGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, sig); err != nil {
		// Fall back to signalling just the leader if the group send fails (for
		// example the group is already gone).
		return syscall.Kill(pid, sig)
	}
	return nil
}

// namedURL returns the configured public URL for named mode; quick mode starts
// empty and fills the URL in from logs.
func namedURL(c Config) string {
	if c.Mode == ModeNamed {
		return c.PublicURL
	}
	return ""
}

// exitError normalizes cmd.Wait errors: a clean exit, a signalled shutdown, and
// a WaitDelay-bounded I/O drain all count as a non-error stop, while an
// unexpected non-zero exit is surfaced.
func exitError(err error) error {
	if err == nil {
		return nil
	}
	// WaitDelay fired for lingering I/O after the process itself exited (with no
	// other error). The process is gone; treat as a clean stop.
	if errors.Is(err, exec.ErrWaitDelay) {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			// We terminated it on purpose.
			return nil
		}
	}
	return err
}

// lineWriter splits an incoming byte stream into newline-delimited lines, calls
// onLine for each complete line, and bounds each line's length. When a line
// exceeds maxLine the excess is dropped up to the next newline (the line is
// truncated) instead of failing the stream — so an over-long log line can never
// stop capture (unlike bufio.Scanner's ErrTooLong).
type lineWriter struct {
	mu       sync.Mutex
	buf      []byte
	onLine   func([]byte)
	maxLine  int
	dropping bool
}

func newLineWriter(onLine func([]byte), maxLine int) *lineWriter {
	if maxLine <= 0 {
		maxLine = maxCapturedLineBytes
	}
	return &lineWriter{onLine: onLine, maxLine: maxLine}
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			if !w.dropping {
				w.emit() // no-op for an empty line
			}
			w.buf = w.buf[:0]
			w.dropping = false
			continue
		}
		if w.dropping {
			continue
		}
		if len(w.buf) >= w.maxLine {
			// Emit the truncated line now and drop the rest until newline.
			w.emit()
			w.buf = w.buf[:0]
			w.dropping = true
			continue
		}
		w.buf = append(w.buf, b)
	}
	return len(p), nil
}

// emit delivers the current buffered line (a copy) to onLine.
func (w *lineWriter) emit() {
	if w.onLine == nil {
		return
	}
	if len(w.buf) == 0 {
		return
	}
	cp := make([]byte, len(w.buf))
	copy(cp, w.buf)
	w.onLine(cp)
}

// flush emits any trailing partial line (no terminating newline) at EOF.
func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.dropping && len(w.buf) > 0 {
		w.emit()
	}
	w.buf = w.buf[:0]
	w.dropping = false
}
