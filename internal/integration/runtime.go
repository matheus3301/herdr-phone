package integration

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/matheus3301/herdr-phone/internal/app"
	"github.com/matheus3301/herdr-phone/internal/buildinfo"
	"github.com/matheus3301/herdr-phone/internal/config"
	"github.com/matheus3301/herdr-phone/internal/daemon"
	"github.com/matheus3301/herdr-phone/internal/server"
)

// Runtime is the production app.Runtime: it composes and supervises the whole
// relay for `serve`, and drives the private control socket for start/stop/
// status/setup-link. It is safe to construct once and reuse.
type Runtime struct {
	env         Getenv
	development bool

	// executable returns the binary path to re-exec for a detached serve.
	executable func() (string, error)
	// spawnServe starts a detached `serve` process and returns its pid.
	spawnServe func(exe string, args, environ []string, logPath string) (int, error)
	// environ returns the environment passed to a spawned serve.
	environ func() []string
}

var _ app.Runtime = (*Runtime)(nil)

// Options configure a Runtime.
type Options struct {
	// Getenv reads process environment (defaults to os.Getenv).
	Getenv func(string) string
}

// New builds a production Runtime.
func New(opts Options) *Runtime {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	env := Getenv(getenv)
	return &Runtime{
		env:         env,
		development: env.get("HERDR_PHONE_DEV") == "1",
		executable:  os.Executable,
		spawnServe:  defaultSpawnServe,
		environ:     os.Environ,
	}
}

// Serve builds the full stack and runs it until ctx is cancelled.
func (rt *Runtime) Serve(ctx context.Context, cfg config.Config, opts app.ServeOptions) error {
	mode, err := effectiveMode(cfg, opts.Quick)
	if err != nil {
		return err
	}
	if err := validateForServe(cfg, mode); err != nil {
		return err
	}
	stateDir, err := resolveStateDir(rt.env)
	if err != nil {
		return err
	}
	if err := ensureStateDir(stateDir); err != nil {
		return err
	}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	st, err := buildStack(serveCtx, cancel, rt, cfg, mode, stateDir)
	if err != nil {
		if errors.Is(err, daemon.ErrStateLocked) {
			// Another daemon already owns this state directory. For an idempotent
			// start this is success (the parent adopts the running daemon); for a
			// direct serve it is a clear, non-fatal condition.
			return fmt.Errorf("herdr-phone is already running for this state directory: %w", err)
		}
		return err
	}
	return st.run(serveCtx)
}

// Start ensures a healthy daemon is running, spawning a detached serve process
// when needed, and returns its mode, public URL, and a fresh pairing link. It is
// idempotent: a healthy existing daemon is reused.
func (rt *Runtime) Start(ctx context.Context, cfg config.Config, opts app.StartOptions) (app.StartResult, error) {
	mode, err := effectiveMode(cfg, opts.Quick)
	if err != nil {
		return app.StartResult{}, err
	}
	stateDir, err := resolveStateDir(rt.env)
	if err != nil {
		return app.StartResult{}, err
	}
	if err := ensureStateDir(stateDir); err != nil {
		return app.StartResult{}, err
	}

	rec := daemon.Reconcile(ctx, stateDir, 2*time.Second)
	if rec.Liveness == daemon.LivenessRunning {
		return rt.finishStart(ctx, stateDir, *rec.Status, true)
	}
	if rec.Liveness == daemon.LivenessStale {
		_ = daemon.CleanupStale(stateDir)
	}

	if err := validateForServe(cfg, mode); err != nil {
		return app.StartResult{}, err
	}

	exe, err := rt.executable()
	if err != nil {
		return app.StartResult{}, fmt.Errorf("locate executable: %w", err)
	}
	args := []string{"serve"}
	if opts.Quick {
		args = append(args, "--quick")
	}

	// Quick mode gets a per-daemon public-probe token, passed to the detached
	// serve process only through its environment (never argv/state/logs), so this
	// parent can later confirm the public URL reaches that exact instance.
	environ := rt.environ()
	var probeToken string
	if mode == config.ModeQuick {
		probeToken, err = newProbeToken()
		if err != nil {
			return app.StartResult{}, fmt.Errorf("generate quick probe token: %w", err)
		}
		environ = append(append([]string(nil), environ...), envQuickProbeToken+"="+probeToken)
	}

	logPath := stateFile(stateDir, logFileName)
	pid, err := rt.spawnServe(exe, args, environ, logPath)
	if err != nil {
		return app.StartResult{}, fmt.Errorf("spawn serve: %w", err)
	}

	status, err := waitReady(ctx, stateDir, pid, logPath)
	if err != nil {
		return app.StartResult{}, err
	}

	// Confirm true public readiness before printing a pairing link.
	switch mode {
	case config.ModeQuick:
		if err := rt.probeQuickPublicReady(ctx, status, probeToken, pid, logPath); err != nil {
			return app.StartResult{}, err
		}
	case config.ModeNamed:
		if err := rt.waitNamedTunnelReady(ctx, stateDir, pid, logPath); err != nil {
			return app.StartResult{}, err
		}
	}
	return rt.finishStart(ctx, stateDir, status, false)
}

// probeQuickPublicReady GETs the learned public URL's /health with the probe
// header until it returns this instance's id, proving the Quick Tunnel actually
// routes the public URL to this daemon before a pairing link is printed. It fails
// with a bounded, actionable error.
func (rt *Runtime) probeQuickPublicReady(ctx context.Context, status daemon.StatusResult, token string, pid int, logPath string) error {
	if status.PublicURL == "" {
		return fmt.Errorf("quick tunnel reported no public URL; see %s", logPath)
	}
	pctx, cancel := context.WithTimeout(ctx, quickProbeTimeout)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		if !daemon.ProcessAlive(pid) {
			return fmt.Errorf("the serve process exited during startup; see %s", logPath)
		}
		if err := quickPublicProbe(pctx, status.PublicURL, token, status.InstanceID); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-pctx.Done():
			return fmt.Errorf("the Quick Tunnel public URL %s did not confirm this instance within %s (%v); the tunnel is ephemeral — retry, and see %s",
				status.PublicURL, quickProbeTimeout, lastErr, logPath)
		case <-ticker.C:
		}
	}
}

// waitNamedTunnelReady blocks until the daemon reports the tunnel component
// ready, so a named start never claims success while cloudflared is still
// connecting. On timeout it returns an actionable error; the daemon keeps
// running and retrying in the background.
func (rt *Runtime) waitNamedTunnelReady(ctx context.Context, stateDir string, pid int, logPath string) error {
	client, err := daemon.NewClientForStateDir(stateDir)
	if err != nil {
		return err
	}
	client = client.WithTimeout(2 * time.Second)
	nctx, cancel := context.WithTimeout(ctx, namedTunnelReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !daemon.ProcessAlive(pid) {
			return fmt.Errorf("the serve process exited during startup; see %s", logPath)
		}
		if st, err := client.Status(nctx); err == nil && componentReady(st, "tunnel") {
			return nil
		}
		select {
		case <-nctx.Done():
			return fmt.Errorf("cloudflared did not establish an edge connection within %s; the daemon is running and still retrying — verify cloudflare.public_url and credentials, then run `herdr-phone status` (see %s)",
				namedTunnelReadyTimeout, logPath)
		case <-ticker.C:
		}
	}
}

// componentReady reports whether the named readiness component is ready.
func componentReady(st daemon.StatusResult, name string) bool {
	for _, c := range st.Components {
		if c.Name == name {
			return c.Ready
		}
	}
	return false
}

// quickPublicProbe fetches publicURL/health with the probe header and verifies
// the response body equals the expected instance id (constant-time). It proves
// the public URL reaches this exact instance. The token travels only in the
// dedicated probe header, never a URL.
func quickPublicProbe(ctx context.Context, publicURL, token, instanceID string) error {
	url := strings.TrimRight(publicURL, "/") + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set(server.ProbeHeader, token)
	resp, err := probeHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe returned status %d", resp.StatusCode)
	}
	got := strings.TrimSpace(string(body))
	if instanceID == "" || subtle.ConstantTimeCompare([]byte(got), []byte(instanceID)) != 1 {
		return errInstanceMismatch
	}
	return nil
}

// probeHTTPClient is the bounded client used for the Quick Tunnel public probe.
var probeHTTPClient = &http.Client{Timeout: 10 * time.Second}

// errInstanceMismatch means the public URL answered but is not this instance.
var errInstanceMismatch = errors.New("public URL did not return this instance id")

// Readiness timeouts for the two front-door modes. Both fit within the CLI's
// start deadline.
const (
	quickProbeTimeout       = 60 * time.Second
	namedTunnelReadyTimeout = 90 * time.Second
)

// finishStart rotates a fresh single-use pairing link and assembles the result.
func (rt *Runtime) finishStart(ctx context.Context, stateDir string, status daemon.StatusResult, already bool) (app.StartResult, error) {
	res := app.StartResult{
		Mode:           status.Mode,
		PublicURL:      status.PublicURL,
		AlreadyRunning: already,
	}
	client, err := daemon.NewClientForStateDir(stateDir)
	if err != nil {
		return res, nil
	}
	pr, err := client.RotatePairing(ctx)
	if err == nil {
		res.PairingURL = pr.URL
	}
	return res, nil
}

// Stop requests graceful shutdown through the control socket.
func (rt *Runtime) Stop(ctx context.Context, _ config.Config) (app.StopResult, error) {
	stateDir, err := resolveStateDir(rt.env)
	if err != nil {
		return app.StopResult{}, err
	}
	rec := daemon.Reconcile(ctx, stateDir, 2*time.Second)
	if rec.Liveness != daemon.LivenessRunning {
		if rec.Liveness == daemon.LivenessStale {
			_ = daemon.CleanupStale(stateDir)
		}
		return app.StopResult{WasRunning: false}, nil
	}
	client, err := daemon.NewClientForStateDir(stateDir)
	if err != nil {
		return app.StopResult{}, err
	}
	if err := client.Stop(ctx); err != nil {
		return app.StopResult{}, err
	}
	return app.StopResult{WasRunning: true}, nil
}

// Status reports the current daemon status via the control socket.
func (rt *Runtime) Status(ctx context.Context, _ config.Config) (app.Status, error) {
	stateDir, err := resolveStateDir(rt.env)
	if err != nil {
		return app.Status{}, err
	}
	rec := daemon.Reconcile(ctx, stateDir, 2*time.Second)
	if rec.Liveness != daemon.LivenessRunning || rec.Status == nil {
		return app.Status{Running: false}, nil
	}
	return statusToApp(*rec.Status), nil
}

// RotatePairing rotates the pairing secret on the running daemon.
func (rt *Runtime) RotatePairing(ctx context.Context, _ config.Config) (app.PairingLink, error) {
	stateDir, err := resolveStateDir(rt.env)
	if err != nil {
		return app.PairingLink{}, err
	}
	rec := daemon.Reconcile(ctx, stateDir, 2*time.Second)
	if rec.Liveness != daemon.LivenessRunning {
		return app.PairingLink{}, errRuntimeNotRunning
	}
	client, err := daemon.NewClientForStateDir(stateDir)
	if err != nil {
		return app.PairingLink{}, err
	}
	pr, err := client.RotatePairing(ctx)
	if err != nil {
		return app.PairingLink{}, err
	}
	return app.PairingLink{URL: pr.URL}, nil
}

// statusToApp maps a control-socket status into the CLI status view.
func statusToApp(st daemon.StatusResult) app.Status {
	out := app.Status{
		Running:          true,
		Mode:             st.Mode,
		PublicURL:        st.PublicURL,
		LocalAddress:     st.LocalAddr,
		Version:          st.Version,
		Protocol:         buildinfo.HerdrProtocol,
		PID:              st.PID,
		Health:           string(st.Health),
		ConnectedClients: st.ClientCount,
	}
	if st.StartUnixMs > 0 {
		out.StartedAt = time.UnixMilli(st.StartUnixMs)
	}
	for _, c := range st.Components {
		switch c.Name {
		case "herdr":
			out.HerdrHealthy = c.Ready
		case "tunnel":
			out.TunnelHealthy = c.Ready
		case "state":
			out.StateHealthy = c.Ready
		}
	}
	return out
}

// waitReady polls the control socket until a daemon answers, the spawned process
// exits without any daemon coming up, or ctx expires.
//
// It checks the control socket before the child's liveness on purpose: under
// concurrent starts the state lock lets exactly one spawned serve bind, and the
// losers exit with ErrStateLocked. A loser whose own child has exited must still
// adopt the winning daemon, so a reachable control socket always wins over a dead
// child. Only when no daemon is reachable and our child has exited do we fail.
func waitReady(ctx context.Context, stateDir string, pid int, logPath string) (daemon.StatusResult, error) {
	client, err := daemon.NewClientForStateDir(stateDir)
	if err != nil {
		return daemon.StatusResult{}, err
	}
	client = client.WithTimeout(2 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if st, err := client.Status(ctx); err == nil {
			return st, nil
		}
		if !daemon.ProcessAlive(pid) {
			// Our child is gone; give a racing winner one last chance to answer
			// before declaring failure.
			if st, err := client.Status(ctx); err == nil {
				return st, nil
			}
			return daemon.StatusResult{}, fmt.Errorf("the serve process exited during startup; see %s", logPath)
		}
		select {
		case <-ctx.Done():
			return daemon.StatusResult{}, fmt.Errorf("timed out waiting for the daemon to become ready; see %s", logPath)
		case <-ticker.C:
		}
	}
}

// defaultSpawnServe starts a fully detached `serve` process in its own session,
// redirecting its output to the mode-0600 daemon log. It never waits on the
// child.
func defaultSpawnServe(exe string, args, environ []string, logPath string) (int, error) {
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, fmt.Errorf("open daemon log: %w", err)
	}
	defer logf.Close()
	_ = logf.Chmod(0o600)

	cmd := exec.Command(exe, args...)
	cmd.Env = environ
	cmd.Stdin = nil
	cmd.Stdout = logf
	cmd.Stderr = logf
	// New session so the daemon outlives the invoking shell/plugin action.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	// Detach: release the process handle so no zombie is left and we never wait.
	_ = cmd.Process.Release()
	return pid, nil
}
