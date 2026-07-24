//go:build unix

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matheus3301/herdr-phone/internal/app"
	"github.com/matheus3301/herdr-phone/internal/auth"
	"github.com/matheus3301/herdr-phone/internal/config"
	"github.com/matheus3301/herdr-phone/internal/daemon"
)

// namedTestConfig builds a named-mode config wired to the fakes for a serve run.
func namedTestConfig(t *testing.T, port int, cloudflared string) (config.Config, string) {
	t.Helper()
	stateDir, err := os.MkdirTemp("/tmp", "hp-cc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })
	tokenFile := filepath.Join(stateDir, "token")
	if err := os.WriteFile(tokenFile, []byte("dummy-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Server: config.Server{
			Host: config.LoopbackHost, Port: port,
			SessionTTL: 12 * time.Hour, IdleLock: 30 * time.Minute,
			AllowedWorkspaceRoots: []string{t.TempDir()},
		},
		Cloudflare: config.Cloudflare{
			Mode: config.ModeNamed, Binary: cloudflared,
			PublicURL: "https://phone.test.example", TokenFile: tokenFile,
			GracePeriod: time.Second,
		},
		Access: config.Access{Enabled: true, TeamDomain: "team.cloudflareaccess.com", Audience: "aud", JWKSTTL: time.Hour},
		Herdr:  config.Herdr{PollHot: 250 * time.Millisecond, PollCold: time.Second},
		UI:     config.UI{Theme: config.ThemeSystem, TerminalFontSize: 13},
	}
	return cfg, stateDir
}

// TestConcurrentStartsConverge runs two Start calls at once against one state
// directory. The daemon state lock must let exactly one serve bind while both
// starts converge on that single running daemon (idempotent), never failing or
// double-binding.
func TestConcurrentStartsConverge(t *testing.T) {
	herdrFake := startFakeHerdr(t)
	port := freePort(t)
	cfg, stateDir := namedTestConfig(t, port, fakeCloudflared(t))

	values := map[string]string{
		"HERDR_PLUGIN_STATE_DIR": stateDir,
		"HERDR_SOCKET_PATH":      herdrFake.path,
		"HERDR_PHONE_DEV":        "1",
		"HOME":                   t.TempDir(),
	}
	rt := New(Options{Getenv: func(k string) string { return values[k] }})
	rt.executable = func() (string, error) { return "herdr-phone", nil }

	// spawnServe runs the real Serve in-process (one goroutine per start), so the
	// state lock is exercised across two concurrent serves in this process.
	serveCtx, serveCancel := context.WithCancel(context.Background())
	var serveWG sync.WaitGroup
	rt.spawnServe = func(_ string, _, _ []string, _ string) (int, error) {
		serveWG.Add(1)
		go func() {
			defer serveWG.Done()
			_ = rt.Serve(serveCtx, cfg, app.ServeOptions{})
		}()
		return os.Getpid(), nil
	}

	startCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	type outcome struct {
		res app.StartResult
		err error
	}
	results := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			r, err := rt.Start(startCtx, cfg, app.StartOptions{})
			results <- outcome{r, err}
		}()
	}

	var got []outcome
	for i := 0; i < 2; i++ {
		got = append(got, <-results)
	}
	for i, g := range got {
		if g.err != nil {
			t.Fatalf("start %d failed: %v", i, g.err)
		}
		if g.res.Mode != config.ModeNamed || g.res.PublicURL != cfg.Cloudflare.PublicURL {
			t.Errorf("start %d result = %+v", i, g.res)
		}
	}

	// Exactly one daemon is running: the control socket answers with a single
	// consistent instance id.
	client, err := daemon.NewClientForStateDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := client.WithTimeout(2 * time.Second).Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.PublicURL != cfg.Cloudflare.PublicURL {
		t.Errorf("running daemon public URL = %q", st.PublicURL)
	}

	// Tear down: stop the daemon and wait for both serve goroutines to exit.
	_ = client.Stop(context.Background())
	serveCancel()
	done := make(chan struct{})
	go func() { serveWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("serve goroutines did not exit after stop")
	}
}

// quickFakeCloudflared writes a stub that publishes a Quick Tunnel URL (matching
// the supervisor's trycloudflare regex) and stays alive, so quick mode learns a
// public URL without a real Cloudflare account.
func quickFakeCloudflared(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "hp-qcfd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "cloudflared")
	script := "#!/bin/sh\necho 'https://hp-test.trycloudflare.com'\nwhile true; do sleep 1; done\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCSRFAfterReloadHTTP drives the real HTTP stack: pair, then simulate a
// browser reload (a fresh request carrying only the persisted cookie) and confirm
// the session still resolves and a mutation succeeds with the retained CSRF
// token — the server-side CSRF/session survive a reload.
func TestCSRFAfterReloadHTTP(t *testing.T) {
	herdrFake := startFakeHerdr(t)
	port := freePort(t)

	stateDir, err := os.MkdirTemp("/tmp", "hp-sess-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })

	cfg := config.Config{
		Server: config.Server{
			Host: config.LoopbackHost, Port: port,
			SessionTTL: 12 * time.Hour, IdleLock: 30 * time.Minute,
			AllowedWorkspaceRoots: []string{t.TempDir()},
		},
		Cloudflare: config.Cloudflare{
			Mode: config.ModeQuick, Binary: quickFakeCloudflared(t),
			QuickEnabled: true, GracePeriod: time.Second,
		},
		Herdr: config.Herdr{PollHot: 250 * time.Millisecond, PollCold: time.Second},
		UI:    config.UI{Theme: config.ThemeSystem, TerminalFontSize: 13},
	}
	values := map[string]string{
		"HERDR_PLUGIN_STATE_DIR": stateDir,
		"HERDR_SOCKET_PATH":      herdrFake.path,
		"HERDR_PHONE_DEV":        "1",
		"HOME":                   t.TempDir(),
	}
	rt := New(Options{Getenv: func(k string) string { return values[k] }})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- rt.Serve(ctx, cfg, app.ServeOptions{Quick: true}) }()

	client, err := daemon.NewClientForStateDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	client = client.WithTimeout(time.Second)
	deadline := time.After(20 * time.Second)
	for {
		if _, err := client.Status(context.Background()); err == nil {
			break
		}
		select {
		case e := <-serveErr:
			t.Fatalf("serve exited during startup: %v", e)
		case <-deadline:
			t.Fatal("daemon never became reachable")
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Rotate a pairing secret over the control socket and extract it.
	pr, err := client.RotatePairing(context.Background())
	if err != nil {
		t.Fatalf("rotate pairing: %v", err)
	}
	_, frag, ok := strings.Cut(pr.URL, "#pair=")
	if !ok || frag == "" {
		t.Fatalf("pairing URL missing secret fragment: %q", pr.URL)
	}

	base := "http://127.0.0.1:" + itoa(port)
	origin := base

	// Pair: obtain the session cookie and CSRF token.
	pairBody, _ := json.Marshal(map[string]string{"secret": frag})
	pairResp := doReq(t, http.MethodPost, base+"/api/v1/pair", origin, "", pairBody)
	if pairResp.status != http.StatusOK {
		t.Fatalf("pair status = %d body=%s", pairResp.status, pairResp.body)
	}
	cookie := findCookie(pairResp.header.Values("Set-Cookie"), auth.CookieName)
	if cookie == "" {
		t.Fatal("pair did not set the session cookie")
	}
	var paired struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal([]byte(pairResp.body), &paired); err != nil || paired.CSRFToken == "" {
		t.Fatalf("pair response missing csrf token: %v body=%s", err, pairResp.body)
	}

	// Simulate a reload: a fresh request carrying only the persisted cookie. The
	// SPA has lost its in-memory CSRF token and recovers it from GET /session.
	sessResp := doReqCookie(t, http.MethodGet, base+"/api/v1/session", origin, "", nil, cookie)
	if sessResp.status != http.StatusOK {
		t.Fatalf("GET /session after reload = %d body=%s", sessResp.status, sessResp.body)
	}
	var recovered struct {
		CSRFToken     string `json:"csrf_token"`
		ExpiresUnixMs int64  `json:"expires_unix_ms"`
	}
	if err := json.Unmarshal([]byte(sessResp.body), &recovered); err != nil {
		t.Fatalf("GET /session body not JSON: %v body=%s", err, sessResp.body)
	}
	if recovered.CSRFToken == "" {
		t.Fatalf("GET /session must return csrf_token; body=%s", sessResp.body)
	}
	if recovered.CSRFToken != paired.CSRFToken {
		t.Errorf("GET /session csrf_token = %q, want the session token %q", recovered.CSRFToken, paired.CSRFToken)
	}
	if recovered.ExpiresUnixMs <= 0 {
		t.Errorf("GET /session must return expires_unix_ms; body=%s", sessResp.body)
	}
	// The bearer cookie value must never appear in the /session response.
	if strings.Contains(sessResp.body, cookie) {
		t.Fatalf("GET /session leaked the bearer cookie value")
	}

	// A mutation using the CSRF token recovered from GET /session succeeds after
	// the reload. A workspace-scoped op is used so no pane lifecycle generation is
	// required against the empty fake snapshot.
	mutBody, _ := json.Marshal(map[string]any{
		"request_id": "r1",
		"operation":  "workspace.focus",
		"params":     map[string]string{"workspace_id": "ws1"},
	})
	mutResp := doReqCookie(t, http.MethodPost, base+"/api/v1/mutations", origin, recovered.CSRFToken, mutBody, cookie)
	if mutResp.status != http.StatusOK {
		t.Fatalf("mutation after reload = %d body=%s", mutResp.status, mutResp.body)
	}
	var mut struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal([]byte(mutResp.body), &mut); err != nil || !mut.Accepted {
		t.Fatalf("mutation not accepted after reload: %v body=%s", err, mutResp.body)
	}

	// A mutation WITHOUT the CSRF token must be rejected, proving CSRF is enforced.
	badBody, _ := json.Marshal(map[string]any{
		"request_id": "r2", "operation": "workspace.focus", "params": map[string]string{"workspace_id": "ws1"},
	})
	badResp := doReqCookie(t, http.MethodPost, base+"/api/v1/mutations", origin, "", badBody, cookie)
	if badResp.status == http.StatusOK {
		t.Errorf("mutation without CSRF token must be rejected, got %d", badResp.status)
	}

	_ = client.Stop(context.Background())
	select {
	case <-serveErr:
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not stop")
	}
}

type httpResult struct {
	status int
	body   string
	header http.Header
}

func doReq(t *testing.T, method, url, origin, csrf string, body []byte) httpResult {
	return doReqCookie(t, method, url, origin, csrf, body, "")
}

func doReqCookie(t *testing.T, method, url, origin, csrf string, body []byte, cookie string) httpResult {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if cookie != "" {
		// Set the cookie header directly so a Secure cookie can be replayed over
		// the loopback http test endpoint.
		req.Header.Set("Cookie", auth.CookieName+"="+cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return httpResult{status: resp.StatusCode, body: string(b), header: resp.Header}
}

// findCookie extracts the value of the named cookie from Set-Cookie headers.
func findCookie(setCookies []string, name string) string {
	for _, sc := range setCookies {
		parts := strings.SplitN(sc, ";", 2)
		nv := strings.SplitN(strings.TrimSpace(parts[0]), "=", 2)
		if len(nv) == 2 && nv[0] == name {
			return nv[1]
		}
	}
	return ""
}
