package integration

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/matheus3301/herdr-phone/internal/auth"
	"github.com/matheus3301/herdr-phone/internal/buildinfo"
	"github.com/matheus3301/herdr-phone/internal/config"
	"github.com/matheus3301/herdr-phone/internal/daemon"
	"github.com/matheus3301/herdr-phone/internal/herdr"
	"github.com/matheus3301/herdr-phone/internal/server"
	"github.com/matheus3301/herdr-phone/internal/state"
	"github.com/matheus3301/herdr-phone/internal/terminal"
	"github.com/matheus3301/herdr-phone/internal/tunnel"
	"github.com/matheus3301/herdr-phone/internal/webui"
)

var errFallbackAssets = errors.New("only the placeholder frontend is embedded; build web/dist (make build-web) or set HERDR_PHONE_DEV=1 to run in development")

// quickReadyTimeout bounds how long serve waits for a Quick Tunnel URL before
// giving up. Named mode proceeds after this even if the edge is still
// connecting (its readiness is then reported as degraded).
const quickReadyTimeout = 60 * time.Second

// swapHandler is the origin's root handler. The HTTP listener is up before the
// public URL (and therefore the real server, which pins the Host allowlist) is
// known, so requests that arrive early get a 503 until the server is installed.
type swapHandler struct {
	mu sync.RWMutex
	h  http.Handler
}

func (s *swapHandler) set(h http.Handler) {
	s.mu.Lock()
	s.h = h
	s.mu.Unlock()
}

func (s *swapHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	h := s.h
	s.mu.RUnlock()
	if h == nil {
		http.Error(w, "starting", http.StatusServiceUnavailable)
		return
	}
	h.ServeHTTP(w, r)
}

// stack holds the fully wired, running subsystems for one serve lifetime.
type stack struct {
	mode        string
	stateDir    string
	publicURL   string
	listener    net.Listener
	httpSrv     *http.Server
	srv         *server.Server
	sup         *tunnel.Supervisor
	tc          *daemon.TunnelChild
	d           *daemon.Daemon
	auditor     *server.FileAuditor
	subscriber  *herdr.Subscriber
	lock        *daemon.StateLock
	gracePeriod time.Duration
}

// buildStack wires every subsystem and starts the background workers (state
// engine, event subscriber, HTTP listener, cloudflared). It blocks only to wait
// for the tunnel URL to be known, then constructs the server and daemon. The
// returned stack is ready for run. On any error it tears down whatever it
// started. serveCtx bounds every background worker; cancel is invoked by the
// daemon's stop path.
func buildStack(serveCtx context.Context, cancel context.CancelFunc, rt *Runtime, cfg config.Config, mode, stateDir string) (_ *stack, err error) {
	socket := resolveHerdrSocket(cfg, rt.env)
	if socket == "" {
		return nil, errors.New("cannot resolve the Herdr socket: set herdr.socket_path or HERDR_SOCKET_PATH")
	}
	bin := resolveHerdrBin(cfg, rt.env)

	// Herdr client + startup handshake (protocol 17). WithBin lets the client run
	// the bare `herdr agent` CLI to discover startable agent kinds (the socket
	// protocol does not expose them).
	client := herdr.NewClient(herdr.NewUnixDialer(socket), herdr.WithBin(bin))
	hsCtx, hsCancel := context.WithTimeout(serveCtx, herdr.DefaultTimeout)
	pong, err := client.Handshake(hsCtx)
	hsCancel()
	if err != nil {
		return nil, fmt.Errorf("herdr handshake failed (is Herdr running?): %w", err)
	}
	// Authoritative, bounded-cache agent-kind discovery, shared by the
	// capabilities document and agent-start validation so both agree and neither
	// hard-codes a stale set.
	kinds := newAgentKinds(client, agentKindsTTL, time.Now)
	capsBase := capabilitiesBase{
		HerdrVersion:  pong.Version,
		HerdrProtocol: pong.Protocol,
		LiveHandoff:   pong.Capabilities.LiveHandoff,
	}

	// Frontend assets. Fail a release start when only the placeholder is embedded.
	assets, err := webui.Handler()
	if err != nil {
		return nil, fmt.Errorf("frontend assets: %w", err)
	}
	if webui.IsFallback() && !rt.development {
		return nil, errFallbackAssets
	}

	// Exclusive state lock for this daemon's whole lifetime, taken before any bind
	// or state mutation so a concurrent serve/start cannot rebind the control
	// socket or clobber runtime.json. ErrStateLocked means another daemon already
	// owns this state dir; the caller maps that to idempotent already-running.
	lock, err := daemon.AcquireStateLock(stateDir)
	if err != nil {
		return nil, err
	}
	st := &stack{
		mode:        mode,
		stateDir:    stateDir,
		lock:        lock,
		gracePeriod: cfg.Cloudflare.GracePeriod,
	}
	// Clean up everything started so far (including releasing the lock) if we fail
	// before returning.
	defer func() {
		if err != nil {
			st.abort()
		}
	}()

	// Loopback listener + HTTP server, up before cloudflared so the origin is
	// reachable as soon as the tunnel connects.
	addr := config.LoopbackHost + ":" + strconv.Itoa(cfg.Server.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("bind loopback %s (is the port free?): %w", addr, err)
	}
	st.listener = listener

	root := &swapHandler{}
	st.httpSrv = &http.Server{
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return serveCtx },
	}
	go func() { _ = st.httpSrv.Serve(listener) }()

	// State engine and event subscriber.
	engine, err := state.New(state.Config{
		Source:   client,
		PollHot:  cfg.Herdr.PollHot,
		PollCold: cfg.Herdr.PollCold,
	})
	if err != nil {
		return nil, fmt.Errorf("state engine: %w", err)
	}
	go func() { _ = engine.Run(serveCtx) }()

	subscriber := client.Subscribe(herdr.LifecycleSubscriptions(), herdr.DefaultBackoff)
	st.subscriber = subscriber
	go func() { _ = subscriber.Run(serveCtx) }()
	go func() {
		for {
			select {
			case <-serveCtx.Done():
				return
			case <-subscriber.Wakeups():
				engine.Wake()
			}
		}
	}()

	// cloudflared supervisor. Start it and wait for the URL to be known.
	tcfg := tunnelConfig(cfg, mode, cfg.Server.Port, stateDir)
	sup, err := tunnel.New(tcfg)
	if err != nil {
		return nil, fmt.Errorf("tunnel: %w", err)
	}
	st.sup = sup
	tc := daemon.NewTunnelChild(sup)
	st.tc = tc
	_ = tc.Start(serveCtx) // start once; the daemon later supervises without restarting.

	// Determine the public URL. Named mode knows it from config and does NOT block
	// serve on the edge connecting — the daemon comes up promptly and `start`
	// separately waits for tunnel readiness before reporting success. Quick mode
	// must learn its ephemeral URL from cloudflared before the server (which pins
	// the Host allowlist) can be built.
	var publicURL string
	if mode == config.ModeQuick {
		url, werr := waitQuickTunnelURL(serveCtx, sup, quickReadyTimeout)
		if werr != nil {
			return nil, werr
		}
		publicURL = url
	} else {
		publicURL = cfg.Cloudflare.PublicURL
	}
	st.publicURL = publicURL

	// Instance identity, generated before the server so the Quick Tunnel probe can
	// return it. The QuickProbeToken (quick mode only) is a random secret the
	// parent `start` process passes in via the environment; it is never written to
	// runtime state or logs.
	instanceID, err := newInstanceID()
	if err != nil {
		return nil, fmt.Errorf("instance id: %w", err)
	}
	probeToken := rt.env.get(envQuickProbeToken)

	// Authentication. The session janitor is bound to the serve context so expired
	// sessions are swept for the daemon's lifetime and the goroutine stops on
	// shutdown.
	authAd, err := buildAuth(cfg, mode, func() string { return st.publicURL })
	if err != nil {
		return nil, err
	}
	authAd.startJanitor(serveCtx)

	// Directory browsing confined to canonical roots.
	roots, err := config.VerifyWorkspaceRoots(cfg.Server.AllowedWorkspaceRoots)
	if err != nil {
		return nil, fmt.Errorf("workspace roots: %w", err)
	}

	// Audit sink (mode 0600 JSONL in the state dir).
	auditor, err := server.NewFileAuditor(stateFile(stateDir, "audit.jsonl"), nil)
	if err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	st.auditor = auditor

	// Server dependency adapters.
	var dHolder atomic.Pointer[daemon.Daemon]
	stateAd := newStateAdapter(engine, client, capsBase, kinds, time.Now)
	daemonStat := &daemonStatusAdapter{
		mode:    mode,
		ready:   func() bool { d := dHolder.Load(); return d != nil && d.Health() == daemon.HealthReady },
		herdr:   func() server.ComponentHealth { return herdrHealth(client) },
		stateFn: func() server.ComponentHealth { return stateHealth(engine) },
	}

	srv, err := server.New(serverConfig(mode, publicURL, cfg.Server.Port), server.Deps{
		Auth:                  authAd,
		State:                 stateAd,
		Mutator:               &mutatorAdapter{client: client, kinds: kinds},
		Daemon:                daemonStat,
		Tunnel:                &tunnelStatusAdapter{mode: mode, sup: sup},
		Directories:           dirValidator{roots: roots},
		Audit:                 auditor,
		Assets:                assets,
		TerminalLauncher:      terminal.ExecLauncher{BinPath: bin, SocketPath: socket},
		TerminalFilterFactory: func() terminal.Filter { return newANSIFilter() },
		QuickProbeToken:       probeToken,
		InstanceID:            instanceID,
	})
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	st.srv = srv
	root.set(srv) // origin now serves the real relay.

	// Daemon: runtime state, control socket, child supervision, graceful stop.
	d := daemon.New(daemon.Options{
		StateDir: stateDir,
		Runtime: daemon.Runtime{
			PID:         os.Getpid(),
			InstanceID:  instanceID,
			Mode:        mode,
			LocalAddr:   listener.Addr().String(),
			PublicURL:   publicURL,
			Version:     buildinfo.Version,
			StartUnixMs: time.Now().UnixMilli(),
			Health:      daemon.HealthStarting,
		},
		Pairing:     authAd,
		ClientCount: srv.Clients,
		OnStop:      cancel,
		// Adopt the lock already held across bind so it spans reconcile→bind→serve
		// and is released only on Shutdown, after the control socket is gone.
		Lock: lock,
	})
	dHolder.Store(d)
	d.AddTunnel(tc)
	d.AddProbe("http", daemon.ProbeFunc(func(context.Context) (bool, string) {
		return true, "listening on " + listener.Addr().String()
	}))
	d.AddProbe("herdr", daemon.ProbeFunc(func(context.Context) (bool, string) {
		h := herdrHealth(client)
		return h.Healthy, h.Detail
	}))
	d.AddProbe("state", daemon.ProbeFunc(func(context.Context) (bool, string) {
		h := stateHealth(engine)
		return h.Healthy, h.Detail
	}))
	st.d = d
	return st, nil
}

// run serves the daemon and blocks until the serve context is cancelled (signal
// or a control-socket stop), then shuts everything down within the grace period.
func (s *stack) run(ctx context.Context) error {
	if err := s.d.Serve(ctx); err != nil {
		s.abort()
		return fmt.Errorf("daemon: %w", err)
	}
	<-ctx.Done()
	s.shutdown()
	return nil
}

// shutdown performs an orderly teardown: stop new HTTP work and live terminals,
// stop the daemon (which stops cloudflared), close the listener and audit sink.
func (s *stack) shutdown() {
	grace := s.gracePeriod
	if grace <= 0 {
		grace = 15 * time.Second
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), grace+5*time.Second)
	defer cancel()

	if s.srv != nil {
		s.srv.Close() // cancels live terminal bridges and drops the state subscription
	}
	if s.httpSrv != nil {
		_ = s.httpSrv.Shutdown(shutCtx)
	}
	if s.d != nil {
		_ = s.d.Shutdown(shutCtx) // stops the tunnel child and closes the control socket
	}
	if s.auditor != nil {
		_ = s.auditor.Close()
	}
}

// abort tears down partially-started resources when buildStack fails.
func (s *stack) abort() {
	if s.srv != nil {
		s.srv.Close()
	}
	if s.httpSrv != nil {
		_ = s.httpSrv.Close()
	} else if s.listener != nil {
		_ = s.listener.Close()
	}
	if s.tc != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.tc.Stop(stopCtx)
		cancel()
	}
	if s.auditor != nil {
		_ = s.auditor.Close()
	}
	// Release the state lock last. The daemon adopts and releases it on Shutdown
	// on the success path, so this only runs when build failed before serving.
	// Release is idempotent.
	if s.lock != nil {
		_ = s.lock.Release()
	}
}

// waitQuickTunnelURL blocks until cloudflared publishes the Quick Tunnel URL, or
// it fails/times out. Quick mode cannot serve without a discovered URL, so every
// non-success path is an error.
func waitQuickTunnelURL(ctx context.Context, sup *tunnel.Supervisor, timeout time.Duration) (string, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-sup.Ready():
		if url := sup.URL(); url != "" {
			return url, nil
		}
		return "", errors.New("quick tunnel became ready without publishing a URL")
	case <-sup.Done():
		return "", fmt.Errorf("quick tunnel failed before publishing a URL: %v", sup.Err())
	case <-timer.C:
		return "", errors.New("timed out waiting for the Quick Tunnel URL")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// buildAuth constructs the composed Authenticator for the mode. Named mode wires
// a JWKS-backed Access verifier; quick mode has no edge identity.
func buildAuth(cfg config.Config, mode string, baseURL func() string) (*authAdapter, error) {
	pairing, err := auth.NewPairing()
	if err != nil {
		return nil, fmt.Errorf("pairing: %w", err)
	}
	sessions := auth.NewSessionStore(cfg.Server.SessionTTL, cfg.Server.IdleLock)
	ad := &authAdapter{
		named:    mode == config.ModeNamed,
		pairing:  pairing,
		sessions: sessions,
		baseURL:  baseURL,
	}
	if ad.named {
		jwks, err := auth.NewJWKSCache(cfg.Access.TeamDomain, auth.WithTTL(cfg.Access.JWKSTTL))
		if err != nil {
			return nil, fmt.Errorf("jwks: %w", err)
		}
		verifier, err := auth.NewVerifier(
			auth.IssuerForTeam(cfg.Access.TeamDomain),
			cfg.Access.Audience,
			cfg.Access.AllowedIdentities,
			jwks,
		)
		if err != nil {
			return nil, fmt.Errorf("access verifier: %w", err)
		}
		ad.verifier = verifier
	}
	return ad, nil
}

func herdrHealth(client *herdr.Client) server.ComponentHealth {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := client.Ping(ctx); err != nil {
		return server.ComponentHealth{Healthy: false, Detail: "unreachable"}
	}
	return server.ComponentHealth{Healthy: true, Detail: "connected"}
}

func stateHealth(engine *state.Engine) server.ComponentHealth {
	if engine.Current() == nil {
		return server.ComponentHealth{Healthy: false, Detail: "no snapshot yet"}
	}
	if err := engine.Stats().LastPollErr; err != nil {
		return server.ComponentHealth{Healthy: false, Detail: "poll error"}
	}
	return server.ComponentHealth{Healthy: true, Detail: "ok"}
}
