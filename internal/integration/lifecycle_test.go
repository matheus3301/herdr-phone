//go:build unix

package integration

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/matheus3301/herdr-phone/internal/app"
	"github.com/matheus3301/herdr-phone/internal/config"
	"github.com/matheus3301/herdr-phone/internal/daemon"
)

// fakeCloudflared writes a stub cloudflared that immediately reports an edge
// connection (the named-tunnel readiness marker) and then stays alive until it
// is signalled, so the supervisor becomes ready without a real Cloudflare
// account or network.
func fakeCloudflared(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "hp-cfd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "cloudflared")
	script := "#!/bin/sh\necho 'registered tunnel connection'\nwhile true; do sleep 1; done\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// TestServeLifecycle runs the real production stack end to end over local HTTP:
// a fake Herdr socket, a fake cloudflared, the real state engine, security
// middleware, HTTP/WebSocket server, and daemon control socket. It verifies the
// origin serves /health, enforces auth on an API route, reports status over the
// control socket, and stops gracefully.
func TestServeLifecycle(t *testing.T) {
	herdrFake := startFakeHerdr(t)

	stateDir, err := os.MkdirTemp("/tmp", "hp-state-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })

	tokenFile := filepath.Join(stateDir, "token")
	if err := os.WriteFile(tokenFile, []byte("dummy-tunnel-token"), 0o600); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	root := t.TempDir()

	cfg := config.Config{
		Server: config.Server{
			Host:                  config.LoopbackHost,
			Port:                  port,
			SessionTTL:            12 * time.Hour,
			IdleLock:              30 * time.Minute,
			AllowedWorkspaceRoots: []string{root},
		},
		Cloudflare: config.Cloudflare{
			Mode:        config.ModeNamed,
			Binary:      fakeCloudflared(t),
			PublicURL:   "https://phone.test.example",
			TokenFile:   tokenFile,
			GracePeriod: time.Second,
		},
		Access: config.Access{
			Enabled:    true,
			TeamDomain: "team.cloudflareaccess.com",
			Audience:   "test-audience",
			JWKSTTL:    time.Hour,
		},
		Herdr: config.Herdr{PollHot: 250 * time.Millisecond, PollCold: time.Second},
		UI:    config.UI{Theme: config.ThemeSystem, TerminalFontSize: 13},
	}

	env := map[string]string{
		"HERDR_PLUGIN_STATE_DIR": stateDir,
		"HERDR_SOCKET_PATH":      herdrFake.path,
		"HERDR_PHONE_DEV":        "1", // allow the placeholder frontend in tests
		"HOME":                   t.TempDir(),
	}
	rt := New(Options{Getenv: func(k string) string { return env[k] }})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- rt.Serve(ctx, cfg, app.ServeOptions{}) }()

	// Wait for the daemon control socket to answer (means the stack is up).
	client, err := daemon.NewClientForStateDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	client = client.WithTimeout(time.Second)
	var status daemon.StatusResult
	deadline := time.After(15 * time.Second)
ready:
	for {
		st, err := client.Status(context.Background())
		if err == nil {
			status = st
			break ready
		}
		select {
		case serveErr := <-errCh:
			t.Fatalf("serve exited during startup: %v", serveErr)
		case <-deadline:
			t.Fatal("daemon never became reachable")
		case <-time.After(100 * time.Millisecond):
		}
	}

	if status.Mode != config.ModeNamed {
		t.Errorf("status mode = %q, want named", status.Mode)
	}
	if status.PublicURL != "https://phone.test.example" {
		t.Errorf("status public URL = %q", status.PublicURL)
	}

	base := "http://127.0.0.1:" + itoa(port)

	// /health is the only unauthenticated route and returns bare "ok".
	if body, code := httpGet(t, base+"/health"); code != 200 || body != "ok" {
		t.Errorf("/health = %d %q, want 200 \"ok\"", code, body)
	}

	// An API route requires a session; without one it is 401.
	if _, code := httpGet(t, base+"/api/v1/snapshot"); code != http.StatusUnauthorized {
		t.Errorf("/api/v1/snapshot without session = %d, want 401", code)
	}

	// A disallowed Host header is refused (loopback dev host is allowed, but an
	// arbitrary host is not).
	if code := httpGetHost(t, base+"/health", "evil.example.com"); code == 200 {
		t.Error("request with disallowed Host should not succeed")
	}

	// The named start readiness wait must observe cloudflared ready. Serve runs in
	// this process, so os.Getpid() is the live daemon pid; the fake cloudflared
	// reported an edge connection, so the tunnel component is ready.
	nctx, ncancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := rt.waitNamedTunnelReady(nctx, stateDir, os.Getpid(), "(test)"); err != nil {
		t.Errorf("waitNamedTunnelReady: %v", err)
	}
	ncancel()

	// Graceful stop over the control socket.
	if err := client.Stop(context.Background()); err != nil {
		t.Fatalf("control stop: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("serve returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not stop after control-socket stop")
	}
}

func httpGet(t *testing.T, url string) (string, int) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body), resp.StatusCode
}

func httpGetHost(t *testing.T, url, host string) int {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
