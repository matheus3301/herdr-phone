// Package webui embeds the built React/TypeScript PWA and serves it as a
// single-page application from the Go binary.
//
// It embeds two trees so the package always compiles and always serves a valid
// document:
//
//   - generated/ receives the real frontend build (web/dist) copied in by the
//     build step before a release compile. In a clean checkout only a committed
//     marker (.gitkeep) is present.
//   - dist/ holds a committed fallback placeholder shell (index.html plus a
//     .fallback marker) that is used when no production build has been embedded,
//     so clean-checkout tests and dev binaries still serve a page.
//
// Assets selects the generated tree when it contains an index.html, otherwise
// the fallback. IsFallback reports which tree is in effect, letting the daemon
// and tests distinguish "real production assets embedded" from "developer needs
// to run the frontend build".
//
// Handlers here only ever serve the static application shell. They never serve
// API or terminal data, and they never render untrusted content, so the shell
// may be cached but is always served with revalidation for index.html.
package webui

import (
	"embed"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// distFS embeds the committed fallback shell under dist/ (placeholder index.html
// plus the .fallback marker) so the package compiles without a frontend build.
//
//go:embed all:dist
var distFS embed.FS

// generatedFS embeds the generated production build under generated/. The build
// step copies web/dist into that directory; in a clean checkout it holds only a
// committed marker, so the all: prefix is required to embed the dotfile and give
// the directive a file to embed.
//
//go:embed all:generated
var generatedFS embed.FS

// generatedIndex is the sentinel whose presence means a real production build
// has been embedded into the generated tree.
const generatedIndex = "index.html"

// generatedSubtree returns the generated production asset tree.
func generatedSubtree() fs.FS { return mustSub(generatedFS, "generated") }

// fallbackSubtree returns the committed placeholder shell.
func fallbackSubtree() fs.FS { return mustSub(distFS, "dist") }

// pickAssets returns the generated tree when it contains an index.html, else the
// fallback tree; the bool reports whether the fallback was chosen. Both trees are
// parameters so the selection is unit-testable independent of what is embedded.
func pickAssets(generated, fallback fs.FS) (chosen fs.FS, usedFallback bool) {
	if _, err := fs.Stat(generated, generatedIndex); err == nil {
		return generated, false
	}
	return fallback, true
}

// Assets returns the embedded application shell: the generated production build
// when present, otherwise the committed fallback shell.
func Assets() fs.FS {
	chosen, _ := pickAssets(generatedSubtree(), fallbackSubtree())
	return chosen
}

// IsFallback reports whether the embedded assets are the committed placeholder
// shell (i.e. no generated production build was compiled in). It is accurate for
// whichever tree Assets currently serves.
func IsFallback() bool {
	_, usedFallback := pickAssets(generatedSubtree(), fallbackSubtree())
	return usedFallback
}

// mustSub returns the named subtree, panicking only on a programming error (the
// embed guarantees both directories exist at build time).
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("webui: embedded subtree missing: " + err.Error())
	}
	return sub
}

// Handler returns an http.Handler that serves the embedded application shell.
func Handler() (http.Handler, error) {
	return New(Assets())
}

// New returns an SPA handler over the provided filesystem. Injecting the
// filesystem lets tests supply an in-memory tree (for example fstest.MapFS)
// instead of the embedded assets. index.html must be present.
func New(fsys fs.FS) (http.Handler, error) {
	if fsys == nil {
		return nil, errors.New("webui: nil filesystem")
	}
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		return nil, errors.New("webui: index.html missing from assets")
	}
	return &spaHandler{fsys: fsys}, nil
}

type spaHandler struct {
	fsys fs.FS
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Normalize the request path into a slash-rooted, cleaned name relative to
	// the asset root. This rejects traversal (".." collapses to root).
	name := path.Clean("/" + r.URL.Path)
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		name = "index.html"
	}

	if h.tryServeFile(w, r, name) {
		return
	}

	// Client-side route: anything without a file extension falls back to the
	// shell so the SPA router can take over. Requests that look like a missing
	// static asset (they have an extension) get an honest 404.
	if ext := path.Ext(name); ext != "" && name != "index.html" {
		http.NotFound(w, r)
		return
	}
	if !h.tryServeFile(w, r, "index.html") {
		http.Error(w, "application shell missing", http.StatusInternalServerError)
	}
}

// tryServeFile serves a regular file from the asset filesystem and reports
// whether it did. Directories and missing files return false.
func (h *spaHandler) tryServeFile(w http.ResponseWriter, r *http.Request, name string) bool {
	f, err := h.fsys.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		// Buffer non-seekable files so http.ServeContent can range/serve them.
		data, err := io.ReadAll(f)
		if err != nil {
			return false
		}
		rs = strings.NewReader(string(data))
	}

	setCacheHeaders(w, name)
	// A zero modtime tells ServeContent to skip Last-Modified handling, which is
	// what we want: cache busting is via hashed asset filenames, not mtime.
	http.ServeContent(w, r, path.Base(name), time.Time{}, rs)
	return true
}

// setCacheHeaders applies the shell caching policy: fingerprinted build assets
// are immutable and long-lived; the entry document must always revalidate so a
// new deployment is picked up. API and terminal data are never served here.
func setCacheHeaders(w http.ResponseWriter, name string) {
	switch {
	case name == "index.html":
		w.Header().Set("Cache-Control", "no-cache")
	case strings.HasPrefix(name, "assets/"):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	default:
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
}
