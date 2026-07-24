package integration

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matheus3301/herdr-phone/internal/auth"
	"github.com/matheus3301/herdr-phone/internal/config"
	"github.com/matheus3301/herdr-phone/internal/daemon"
	"github.com/matheus3301/herdr-phone/internal/herdr"
	"github.com/matheus3301/herdr-phone/internal/server"
	"github.com/matheus3301/herdr-phone/internal/state"
)

// ---- effectiveMode --------------------------------------------------------

func TestEffectiveMode(t *testing.T) {
	t.Parallel()
	named := config.Config{Cloudflare: config.Cloudflare{Mode: config.ModeNamed}}
	if m, err := effectiveMode(named, false); err != nil || m != config.ModeNamed {
		t.Errorf("named: %q %v", m, err)
	}
	if _, err := effectiveMode(named, true); err == nil {
		t.Error("--quick without quick_enabled must error")
	}
	quick := config.Config{Cloudflare: config.Cloudflare{Mode: config.ModeNamed, QuickEnabled: true}}
	if m, err := effectiveMode(quick, true); err != nil || m != config.ModeQuick {
		t.Errorf("quick: %q %v", m, err)
	}
}

// ---- serverConfig ---------------------------------------------------------

func TestServerConfigNamed(t *testing.T) {
	t.Parallel()
	c := serverConfig(config.ModeNamed, "https://phone.example.com", 8787)
	if c.PublicHost != "phone.example.com" {
		t.Errorf("PublicHost = %q", c.PublicHost)
	}
	if c.Quick {
		t.Error("named must not be Quick")
	}
	if !contains(c.AllowedOrigins, "https://phone.example.com") {
		t.Errorf("origins missing public https origin: %v", c.AllowedOrigins)
	}
	if !contains(c.DevHosts, "127.0.0.1:8787") {
		t.Errorf("dev hosts missing loopback: %v", c.DevHosts)
	}
}

func TestServerConfigQuick(t *testing.T) {
	t.Parallel()
	c := serverConfig(config.ModeQuick, "https://abc.trycloudflare.com", 9000)
	if c.PublicHost != "abc.trycloudflare.com" || !c.Quick {
		t.Errorf("quick server config = %+v", c)
	}
}

// ---- statusToApp ----------------------------------------------------------

func TestStatusToApp(t *testing.T) {
	t.Parallel()
	st := daemon.StatusResult{
		Health: daemon.HealthReady, Mode: "named", PublicURL: "https://h", LocalAddr: "127.0.0.1:8787",
		Version: "0.1.0", PID: 42, StartUnixMs: time.Now().UnixMilli(), ClientCount: 3,
		Components: []daemon.ComponentStatus{
			{Name: "herdr", Ready: true}, {Name: "tunnel", Ready: false}, {Name: "state", Ready: true},
		},
	}
	got := statusToApp(st)
	if !got.Running || got.Mode != "named" || got.ConnectedClients != 3 {
		t.Errorf("status = %+v", got)
	}
	if !got.HerdrHealthy || got.TunnelHealthy || !got.StateHealthy {
		t.Errorf("component health mapping wrong: %+v", got)
	}
	if got.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}
}

// ---- dirValidator ---------------------------------------------------------

func TestDirValidator(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(real, "child")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	d := dirValidator{roots: []string{real}}

	if got, err := d.Resolve(sub); err != nil || got != sub {
		t.Errorf("Resolve(sub) = %q %v", got, err)
	}
	if _, err := d.Resolve(t.TempDir()); err == nil {
		t.Error("path outside roots must be rejected")
	}
	if _, err := d.Resolve(filepath.Join(real, "nope")); err == nil {
		t.Error("missing path must error")
	}
	if _, err := d.Resolve(""); err == nil {
		t.Error("empty path must error")
	}
}

// ---- authAdapter (quick mode) ---------------------------------------------

func TestAuthAdapterQuickPairingLifecycle(t *testing.T) {
	t.Parallel()
	pairing, err := auth.NewPairing()
	if err != nil {
		t.Fatal(err)
	}
	ad := &authAdapter{
		named:    false,
		pairing:  pairing,
		sessions: auth.NewSessionStore(time.Hour, 0),
		baseURL:  func() string { return "https://phone.example.com" },
	}
	if ad.NamedMode() {
		t.Error("quick must not be named mode")
	}
	if ad.CookieName() != auth.CookieName {
		t.Errorf("cookie name = %q", ad.CookieName())
	}
	// VerifyAccess is a no-op in quick mode.
	if err := ad.VerifyAccess(httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Errorf("quick VerifyAccess = %v", err)
	}

	secret := pairing.Token()
	r := httptest.NewRequest("POST", "/api/v1/pair", nil)

	// Wrong secret is rejected.
	if _, err := ad.Pair(r, "not-the-secret"); err != server.ErrPairing {
		t.Fatalf("bad secret err = %v, want ErrPairing", err)
	}
	// Correct secret pairs.
	sess, err := ad.Pair(r, secret)
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if sess.Cookie == nil || sess.Cookie.Name != auth.CookieName || !sess.Cookie.HttpOnly || !sess.Cookie.Secure {
		t.Errorf("cookie attrs wrong: %+v", sess.Cookie)
	}
	if sess.CSRFToken == "" || !sess.Identity.Quick || sess.Identity.Display != "Quick Tunnel operator" {
		t.Errorf("identity = %+v", sess.Identity)
	}
	cookieVal := sess.Cookie.Value

	// Session lookup + CSRF.
	if _, ok := ad.Session(cookieVal); !ok {
		t.Error("session should resolve")
	}
	if !ad.ValidateCSRF(cookieVal, sess.CSRFToken) {
		t.Error("valid CSRF should pass")
	}
	if ad.ValidateCSRF(cookieVal, "wrong") {
		t.Error("wrong CSRF must fail")
	}

	// Single use: the same (now rotated) secret cannot pair again.
	if _, err := ad.Pair(r, secret); err != server.ErrPairing {
		t.Errorf("reused secret err = %v, want ErrPairing", err)
	}

	// Revoke.
	ad.Revoke(cookieVal)
	if _, ok := ad.Session(cookieVal); ok {
		t.Error("revoked session must not resolve")
	}
}

func TestAuthAdapterRotatePairing(t *testing.T) {
	t.Parallel()
	pairing, _ := auth.NewPairing()
	ad := &authAdapter{named: false, pairing: pairing, sessions: auth.NewSessionStore(time.Hour, 0), baseURL: func() string { return "https://phone.example.com" }}
	pr, err := ad.RotatePairing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pr.URL, "https://phone.example.com/#pair=") {
		t.Errorf("pairing url = %q", pr.URL)
	}
}

// ---- stateAdapter ---------------------------------------------------------

func TestStateAdapterCapabilitiesAndReadPane(t *testing.T) {
	t.Parallel()
	f := startFakeHerdr(t)
	client := herdr.NewClient(herdr.NewUnixDialer(f.path))
	engine, err := state.New(state.Config{Source: client})
	if err != nil {
		t.Fatal(err)
	}
	kinds := newAgentKinds(&fakeKindsSource{kinds: []string{"claude", "codex"}}, time.Minute, time.Now)
	ad := newStateAdapter(engine, client, capabilitiesBase{HerdrVersion: "0.7.5", HerdrProtocol: 17}, kinds, time.Now)

	var doc map[string]any
	if err := json.Unmarshal(ad.Capabilities(), &doc); err != nil {
		t.Fatalf("capabilities not JSON: %v", err)
	}
	if doc["herdr_protocol"].(float64) != 17 {
		t.Errorf("herdr_protocol = %v", doc["herdr_protocol"])
	}
	gotKinds, _ := doc["agent_kinds"].([]any)
	if len(gotKinds) != 2 || gotKinds[0] != "claude" {
		t.Errorf("agent_kinds = %v", doc["agent_kinds"])
	}
	// Snapshot with no poll yet yields a null-data snapshot rather than panicking.
	if snap := ad.Snapshot(); string(snap.Data) != "null" {
		t.Errorf("empty snapshot data = %s", snap.Data)
	}
	if _, ok := ad.Generation("nope"); ok {
		t.Error("unknown pane must not have a generation")
	}
	// ReadPane goes to Herdr and returns its text (empty for the fake).
	if _, err := ad.ReadPane(context.Background(), "p1", "visible", 100); err != nil {
		t.Errorf("ReadPane: %v", err)
	}
}

func TestStateAdapterSubscribeDelivers(t *testing.T) {
	t.Parallel()
	f := startFakeHerdr(t)
	client := herdr.NewClient(herdr.NewUnixDialer(f.path))
	engine, err := state.New(state.Config{Source: client})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = engine.Run(ctx) }()

	// Wait for the first poll to populate current state.
	deadline := time.After(2 * time.Second)
	for engine.Current() == nil {
		select {
		case <-deadline:
			t.Fatal("engine never produced a snapshot")
		case <-time.After(10 * time.Millisecond):
		}
	}

	ad := newStateAdapter(engine, client, capabilitiesBase{}, newAgentKinds(&fakeKindsSource{}, time.Minute, time.Now), time.Now)
	got := make(chan server.Snapshot, 1)
	stop := ad.Subscribe(func(s server.Snapshot) {
		select {
		case got <- s:
		default:
		}
	})
	defer stop()

	select {
	case s := <-got:
		if s.Hash == "" {
			t.Error("delivered snapshot has no hash")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber never received the seeded snapshot")
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
