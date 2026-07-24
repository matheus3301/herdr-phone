package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"testing/fstest"
)

// embeddedHasGeneratedIndex reports whether a real production build is embedded
// in the generated tree.
func embeddedHasGeneratedIndex() bool {
	_, err := fs.Stat(generatedSubtree(), generatedIndex)
	return err == nil
}

func TestEmbeddedAssetsPresent(t *testing.T) {
	t.Parallel()
	// Whichever tree is selected, a servable index.html must exist.
	fsys := Assets()
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		t.Fatalf("embedded index.html missing: %v", err)
	}
}

func TestPickAssetsPrefersGenerated(t *testing.T) {
	t.Parallel()
	gen := fstest.MapFS{
		"index.html":    {Data: []byte("GENERATED")},
		"assets/app.js": {Data: []byte("x")},
	}
	fb := fstest.MapFS{
		"index.html": {Data: []byte("FALLBACK")},
		".fallback":  {Data: []byte("marker")},
	}
	chosen, usedFallback := pickAssets(gen, fb)
	if usedFallback {
		t.Fatal("expected generated tree to be selected when it has index.html")
	}
	b, err := fs.ReadFile(chosen, "index.html")
	if err != nil || string(b) != "GENERATED" {
		t.Fatalf("served %q (err %v), want GENERATED", b, err)
	}
}

func TestPickAssetsFallsBackWithoutGeneratedIndex(t *testing.T) {
	t.Parallel()
	// Generated tree has only a marker, no index.html (clean checkout shape).
	gen := fstest.MapFS{".gitkeep": {Data: []byte("marker")}}
	fb := fstest.MapFS{
		"index.html": {Data: []byte("FALLBACK")},
		".fallback":  {Data: []byte("marker")},
	}
	chosen, usedFallback := pickAssets(gen, fb)
	if !usedFallback {
		t.Fatal("expected fallback tree when generated has no index.html")
	}
	b, err := fs.ReadFile(chosen, "index.html")
	if err != nil || string(b) != "FALLBACK" {
		t.Fatalf("served %q (err %v), want FALLBACK", b, err)
	}
}

func TestIsFallbackMatchesEmbeddedTree(t *testing.T) {
	t.Parallel()
	hasGen := embeddedHasGeneratedIndex()
	if IsFallback() == hasGen {
		t.Fatalf("IsFallback()=%v but generated index present=%v (must be opposite)", IsFallback(), hasGen)
	}
	if IsFallback() {
		// Fallback in effect: the committed placeholder marker must be embedded.
		if _, err := fs.Stat(Assets(), ".fallback"); err != nil {
			t.Errorf("fallback reported but .fallback marker not in served tree: %v", err)
		}
	} else {
		if _, err := fs.Stat(Assets(), "index.html"); err != nil {
			t.Errorf("generated reported but index.html not in served tree: %v", err)
		}
	}
}

// TestRequireGeneratedWebAssets fails a release build that forgot to embed the
// frontend. It is inert by default and only enforced when the build/CI sets
// HERDR_PHONE_REQUIRE_WEB=1, so clean-checkout unit tests still pass.
func TestRequireGeneratedWebAssets(t *testing.T) {
	if os.Getenv("HERDR_PHONE_REQUIRE_WEB") != "1" {
		t.Skip("set HERDR_PHONE_REQUIRE_WEB=1 to require embedded production web assets")
	}
	if IsFallback() {
		t.Fatal("HERDR_PHONE_REQUIRE_WEB=1: production web assets are not embedded " +
			"(generated/index.html missing; build the frontend into internal/webui/generated before compiling)")
	}
	if _, err := fs.Stat(Assets(), "index.html"); err != nil {
		t.Fatalf("generated assets present but index.html not servable: %v", err)
	}
}

func TestNewRejectsNilAndMissingIndex(t *testing.T) {
	t.Parallel()
	if _, err := New(nil); err == nil {
		t.Fatal("expected error for nil filesystem")
	}
	if _, err := New(fstest.MapFS{"other.txt": {Data: []byte("x")}}); err == nil {
		t.Fatal("expected error when index.html absent")
	}
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	fsys := fstest.MapFS{
		"index.html":           {Data: []byte("SHELL")},
		"assets/app.a1b2.js":   {Data: []byte("JS")},
		"manifest.webmanifest": {Data: []byte(`{"name":"Herdr Phone"}`)},
	}
	h, err := New(fsys)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

func TestServesIndexAtRoot(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != "SHELL" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("index cache-control = %q, want no-cache", cc)
	}
}

func TestServesHashedAssetWithImmutableCache(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app.a1b2.js", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "JS" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatalf("asset cache-control = %q", cc)
	}
}

func TestUnknownRouteFallsBackToShell(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)
	rec := httptest.NewRecorder()
	// A client-side route with no file extension must serve the shell.
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/terminal/pane-123", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "SHELL" {
		t.Fatalf("status=%d body=%q, want shell fallback", rec.Code, rec.Body.String())
	}
}

func TestMissingStaticAssetIs404(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)
	rec := httptest.NewRecorder()
	// A missing file with an extension must not silently fall back to the shell.
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestTraversalIsConfined(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/../../etc/passwd", nil))
	// Cleaned to /etc/passwd -> not found as asset, has extension? no -> shell.
	// Either way it must never escape the asset fs; body is never passwd content.
	if rec.Body.String() == "" && rec.Code == http.StatusOK {
		t.Fatal("unexpected empty ok")
	}
	if rec.Code == http.StatusOK && rec.Body.String() != "SHELL" {
		t.Fatalf("traversal served unexpected content: %q", rec.Body.String())
	}
}

func TestMethodNotAllowed(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
