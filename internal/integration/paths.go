// Package integration composes the herdr-phone subsystems — configuration, the
// Herdr socket client and state engine, Cloudflare Access authentication,
// pairing and sessions, the security middleware and HTTP/WebSocket server, the
// terminal controller bridge, the cloudflared supervisor, and the daemon control
// socket — into the concrete app.Runtime the CLI drives.
//
// It is the one place that knows how every package fits together. The individual
// packages depend only on their own narrow interfaces; nothing here is imported
// by them, so this package can freely reference all of them without creating a
// cycle. cmd/herdr-phone wires New into app.Environment.
package integration

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/matheus3301/herdr-phone/internal/config"
)

// Getenv is the environment accessor the runtime reads (HERDR_* and XDG/HOME).
type Getenv func(string) string

func (g Getenv) get(k string) string {
	if g == nil {
		return ""
	}
	return g(k)
}

// State-directory and log file names.
const (
	logFileName        = "herdr-phone.log"
	cloudflaredLogName = "cloudflared.log"
)

// envQuickProbeToken carries the per-daemon Quick Tunnel public-probe secret
// from the invoking `start` process to the detached `serve` process. It travels
// only in the child's environment — never in runtime state, logs, or argv — so
// the parent can prove the public URL reaches this exact instance before it
// prints a pairing link.
const envQuickProbeToken = "HERDR_PHONE_QUICK_PROBE_TOKEN"

// newProbeToken returns a 256-bit random URL-safe token for the Quick Tunnel
// public-instance probe.
func newProbeToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// resolveStateDir follows the documented precedence for the daemon state
// directory: the Herdr-provided plugin state dir, then XDG_STATE_HOME, then
// ~/.local/state. It never returns an empty path.
func resolveStateDir(env Getenv) (string, error) {
	if dir := env.get("HERDR_PLUGIN_STATE_DIR"); dir != "" {
		return dir, nil
	}
	if dir := env.get("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "herdr-phone"), nil
	}
	if home := env.get("HOME"); home != "" {
		return filepath.Join(home, ".local", "state", "herdr-phone"), nil
	}
	return "", errNoStateDir
}

// resolveHerdrSocket resolves the Herdr socket path: config, then
// HERDR_SOCKET_PATH, then the documented default under the Herdr config dir.
func resolveHerdrSocket(cfg config.Config, env Getenv) string {
	if cfg.Herdr.SocketPath != "" {
		return cfg.Herdr.SocketPath
	}
	if p := env.get("HERDR_SOCKET_PATH"); p != "" {
		return p
	}
	if xdg := env.get("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "herdr", "herdr.sock")
	}
	if home := env.get("HOME"); home != "" {
		return filepath.Join(home, ".config", "herdr", "herdr.sock")
	}
	return ""
}

// resolveHerdrBin resolves the Herdr binary: config, then HERDR_BIN_PATH, then
// "herdr" on PATH.
func resolveHerdrBin(cfg config.Config, env Getenv) string {
	if cfg.Herdr.Binary != "" {
		return cfg.Herdr.Binary
	}
	if p := env.get("HERDR_BIN_PATH"); p != "" {
		return p
	}
	return "herdr"
}

// newInstanceID returns a random 128-bit hex identifier for a daemon instance.
func newInstanceID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// hostOnly returns the host[:port] of a URL string, or "" if it cannot be
// parsed. It trims a scheme and any path.
func hostOnly(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

// stateFile joins a file name onto the state directory.
func stateFile(stateDir, name string) string {
	return filepath.Join(stateDir, name)
}

// ensureStateDir creates the state directory with mode 0700.
func ensureStateDir(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(stateDir, 0o700)
	return nil
}
