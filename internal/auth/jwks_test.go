package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func jwksHandler(t *testing.T, hits *int32, keys ...*testKey) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		doc := map[string]any{"keys": jwksList(keys...)}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}
}

func jwksList(keys ...*testKey) []map[string]string {
	out := make([]map[string]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.jwk())
	}
	return out
}

func TestJWKS_FetchAndResolve(t *testing.T) {
	t.Parallel()
	k := newTestKey(t, "kid-a")
	var hits int32
	srv := httptest.NewServer(jwksHandler(t, &hits, k))
	defer srv.Close()

	c, err := NewJWKSCache("example.cloudflareaccess.com", WithCertsURL(srv.URL), WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("NewJWKSCache: %v", err)
	}
	pub, err := c.PublicKey(context.Background(), "kid-a")
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if pub.N.Cmp(k.priv.PublicKey.N) != 0 {
		t.Fatal("resolved key does not match")
	}
	// Second lookup within TTL must not refetch.
	if _, err := c.PublicKey(context.Background(), "kid-a"); err != nil {
		t.Fatalf("PublicKey 2: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("fetch count = %d, want 1", got)
	}
}

func TestJWKS_UnknownKIDTriggersRefresh(t *testing.T) {
	t.Parallel()
	ka := newTestKey(t, "kid-a")
	kb := newTestKey(t, "kid-b")
	var hits int32
	// Server returns both keys; first known, then a rotation adds kid-b.
	current := []*testKey{ka}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": jwksList(current...)})
	}))
	defer srv.Close()

	now := time.Unix(1000, 0)
	c, err := NewJWKSCache("example.cloudflareaccess.com",
		WithCertsURL(srv.URL), WithHTTPClient(srv.Client()),
		WithTTL(time.Hour), WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewJWKSCache: %v", err)
	}
	if _, err := c.PublicKey(context.Background(), "kid-a"); err != nil {
		t.Fatalf("kid-a: %v", err)
	}
	// kid-b unknown while cache fresh: must refetch (picks up rotation) once the
	// throttle window has elapsed since the last fetch attempt.
	current = []*testKey{ka, kb}
	now = now.Add(30 * time.Second) // past defaultMinRefreshInterval (15s)
	if _, err := c.PublicKey(context.Background(), "kid-b"); err != nil {
		t.Fatalf("kid-b after rotation: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("fetch count = %d, want 2", got)
	}
}

func TestJWKS_UnknownKIDRefreshThrottled(t *testing.T) {
	t.Parallel()
	ka := newTestKey(t, "kid-a")
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": jwksList(ka)})
	}))
	defer srv.Close()

	now := time.Unix(1000, 0)
	c, err := NewJWKSCache("example.cloudflareaccess.com",
		WithCertsURL(srv.URL), WithHTTPClient(srv.Client()),
		WithTTL(time.Hour), WithMinRefreshInterval(30*time.Second),
		WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewJWKSCache: %v", err)
	}
	// Prime the cache (fetch #1).
	if _, err := c.PublicKey(context.Background(), "kid-a"); err != nil {
		t.Fatalf("prime: %v", err)
	}
	// A flood of unknown kids while fresh and within the throttle window must
	// NOT drive additional fetches; each fails closed with ErrUnknownKID.
	for i := 0; i < 50; i++ {
		if _, err := c.PublicKey(context.Background(), "kid-unknown"); !errors.Is(err, ErrUnknownKID) {
			t.Fatalf("unknown kid: err = %v, want ErrUnknownKID", err)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("fetch count = %d, want 1 (throttled)", got)
	}
	// After the throttle window elapses, exactly one more refresh is allowed.
	now = now.Add(31 * time.Second)
	if _, err := c.PublicKey(context.Background(), "kid-unknown"); !errors.Is(err, ErrUnknownKID) {
		t.Fatalf("post-window unknown kid: err = %v, want ErrUnknownKID", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("fetch count = %d, want 2 (one refresh after window)", got)
	}
}

func TestJWKS_StaleFallbackWithinOneTTL(t *testing.T) {
	t.Parallel()
	k := newTestKey(t, "kid-a")
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": jwksList(k)})
	}))
	defer srv.Close()

	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	c, err := NewJWKSCache("example.cloudflareaccess.com",
		WithCertsURL(srv.URL), WithHTTPClient(srv.Client()),
		WithTTL(time.Hour), WithNow(clock))
	if err != nil {
		t.Fatalf("NewJWKSCache: %v", err)
	}
	if _, err := c.PublicKey(context.Background(), "kid-a"); err != nil {
		t.Fatalf("initial: %v", err)
	}

	// Endpoint now fails. Advance past TTL but within the extra TTL window.
	fail.Store(true)
	now = time.Unix(1000, 0).Add(90 * time.Minute) // age 1.5h, ttl 1h -> within 2*ttl
	if _, err := c.PublicKey(context.Background(), "kid-a"); err != nil {
		t.Fatalf("stale fallback should succeed: %v", err)
	}

	// Advance beyond the stale window: must fail closed.
	now = time.Unix(1000, 0).Add(3 * time.Hour)
	if _, err := c.PublicKey(context.Background(), "kid-a"); !errors.Is(err, ErrJWKSUnavailable) {
		t.Fatalf("err = %v, want ErrJWKSUnavailable", err)
	}
}

func TestJWKS_FailClosedNoCache(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, err := NewJWKSCache("example.cloudflareaccess.com", WithCertsURL(srv.URL), WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("NewJWKSCache: %v", err)
	}
	if _, err := c.PublicKey(context.Background(), "kid-a"); !errors.Is(err, ErrJWKSUnavailable) {
		t.Fatalf("err = %v, want ErrJWKSUnavailable", err)
	}
}

func TestJWKS_BoundedBody(t *testing.T) {
	t.Parallel()
	// Serve a body far larger than the limit; parsing must fail rather than
	// consume it all, so the cache stays empty and fails closed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"keys":[`))
		junk := make([]byte, 4096)
		for i := range junk {
			junk[i] = 'A'
		}
		for i := 0; i < 64; i++ { // ~256KiB of garbage, exceeds 1KiB limit below
			w.Write(junk)
		}
	}))
	defer srv.Close()

	c, err := NewJWKSCache("example.cloudflareaccess.com",
		WithCertsURL(srv.URL), WithHTTPClient(srv.Client()), WithMaxBody(1024))
	if err != nil {
		t.Fatalf("NewJWKSCache: %v", err)
	}
	if _, err := c.PublicKey(context.Background(), "kid-a"); err == nil {
		t.Fatal("expected failure on oversized/invalid JWKS body")
	}
}

func TestJWKS_BoundedBodyTruncatesValidJSON(t *testing.T) {
	t.Parallel()
	// A well-formed JWKS document that is larger than the configured limit: the
	// same body parses under a generous limit but is truncated (and therefore
	// fails closed) under a small one. This proves the bound truncates rather
	// than the body merely being invalid.
	k := newTestKey(t, "kid-a")
	keys := make([]map[string]string, 0, 200)
	for i := 0; i < 200; i++ {
		keys = append(keys, k.jwk())
	}
	body, err := json.Marshal(map[string]any{"keys": keys})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(body) < 4096 {
		t.Fatalf("test body too small to be meaningful: %d", len(body))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	// Generous limit: parses fine.
	cOK, err := NewJWKSCache("example.cloudflareaccess.com",
		WithCertsURL(srv.URL), WithHTTPClient(srv.Client()), WithMaxBody(int64(len(body))+16))
	if err != nil {
		t.Fatalf("NewJWKSCache: %v", err)
	}
	if _, err := cOK.PublicKey(context.Background(), "kid-a"); err != nil {
		t.Fatalf("full body should parse: %v", err)
	}

	// Small limit: truncation makes the valid JSON unparsable → fail closed.
	cSmall, err := NewJWKSCache("example.cloudflareaccess.com",
		WithCertsURL(srv.URL), WithHTTPClient(srv.Client()), WithMaxBody(int64(len(body)/2)))
	if err != nil {
		t.Fatalf("NewJWKSCache: %v", err)
	}
	if _, err := cSmall.PublicKey(context.Background(), "kid-a"); err == nil {
		t.Fatal("expected truncation of valid JSON to fail closed")
	}
}

func TestJWKS_RejectsUndersizedKey(t *testing.T) {
	t.Parallel()
	// A JWKS containing only a 1024-bit key yields no usable keys, so the cache
	// stays empty and PublicKey fails closed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{weakJWK(t, "weak", 1024)},
		})
	}))
	defer srv.Close()

	c, err := NewJWKSCache("example.cloudflareaccess.com", WithCertsURL(srv.URL), WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("NewJWKSCache: %v", err)
	}
	if _, err := c.PublicKey(context.Background(), "weak"); err == nil {
		t.Fatal("undersized JWKS key must be rejected")
	}
}

func TestJWKS_SingleflightCoalesces(t *testing.T) {
	t.Parallel()
	k := newTestKey(t, "kid-a")
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		time.Sleep(20 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": jwksList(k)})
	}))
	defer srv.Close()

	c, err := NewJWKSCache("example.cloudflareaccess.com", WithCertsURL(srv.URL), WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("NewJWKSCache: %v", err)
	}

	const n = 20
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := c.PublicKey(context.Background(), "kid-a")
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent PublicKey: %v", err)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("fetch count = %d, want 1 (singleflight)", got)
	}
}

func TestTeamDomainHelpers(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ iss, certs string }{
		"example.cloudflareaccess.com":          {testIssuer, testIssuer + "/cdn-cgi/access/certs"},
		"https://example.cloudflareaccess.com":  {testIssuer, testIssuer + "/cdn-cgi/access/certs"},
		"https://example.cloudflareaccess.com/": {testIssuer, testIssuer + "/cdn-cgi/access/certs"},
	}
	for in, want := range cases {
		if got := IssuerForTeam(in); got != want.iss {
			t.Errorf("IssuerForTeam(%q) = %q, want %q", in, got, want.iss)
		}
		if got := CertsURLForTeam(in); got != want.certs {
			t.Errorf("CertsURLForTeam(%q) = %q, want %q", in, got, want.certs)
		}
	}
}

// Verifier end-to-end against a live JWKS server.
func TestVerifier_WithJWKS(t *testing.T) {
	t.Parallel()
	now := int64(1_000_000)
	k := newTestKey(t, "kid-live")
	var hits int32
	srv := httptest.NewServer(jwksHandler(t, &hits, k))
	defer srv.Close()

	cache, err := NewJWKSCache("example.cloudflareaccess.com", WithCertsURL(srv.URL), WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("NewJWKSCache: %v", err)
	}
	v, err := NewVerifier(testIssuer, testAudience, nil, cache, WithClock(fixedClock(now)))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tok := k.signToken(t, "RS256", "kid-live", baseClaims(now))
	if _, err := v.Verify(context.Background(), tok); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
