//go:build unix

package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// maxSocketPathLen is a conservative bound on a Unix domain socket path. The
// kernel sun_path array is 104 bytes on darwin and 108 on linux (both including
// the trailing NUL). Staying at or below 100 leaves margin on every supported
// platform.
const maxSocketPathLen = 100

// SocketPath returns the control socket path for a state directory.
//
// Runtime state (runtime.json, logs, audit) always lives in stateDir. The
// control socket normally lives there too, but AF_UNIX bind/connect both embed
// the path in a fixed-size kernel buffer, so a long stateDir (common on macOS,
// where per-user temp dirs sit under /var/folders/...) would exceed the limit.
// When the in-state-dir path is too long, the socket is deterministically
// relocated to a validated short per-user runtime directory. Both the server and
// any client compute the same path from stateDir, so no discovery is needed.
func SocketPath(stateDir string) (string, error) {
	inState := ControlPath(stateDir)
	if len(inState) <= maxSocketPathLen {
		return inState, nil
	}

	base, err := ensureUserRuntimeDir()
	if err != nil {
		return "", fmt.Errorf("daemon: resolve short socket dir: %w", err)
	}
	// Derive a stable, collision-resistant name from the absolute state dir so
	// distinct daemons sharing the base directory never clash and the same
	// daemon is always reachable at the same path.
	key := stateDir
	if abs, err := filepath.Abs(stateDir); err == nil {
		key = filepath.Clean(abs)
	}
	sum := sha256.Sum256([]byte(key))
	name := "hp-" + hex.EncodeToString(sum[:6]) + ".sock"
	short := filepath.Join(base, name)
	if len(short) > maxSocketPathLen {
		return "", fmt.Errorf("daemon: short socket path %q still exceeds %d bytes", short, maxSocketPathLen)
	}
	return short, nil
}

// ensureUserRuntimeDir returns a short, per-user, private directory suitable for
// a Unix socket, creating it if necessary. Candidates are tried in order of
// preference; each is validated for ownership and permissions before use.
func ensureUserRuntimeDir() (string, error) {
	uid := os.Getuid()

	var candidates []string
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" && filepath.IsAbs(xdg) {
		candidates = append(candidates, filepath.Join(xdg, "herdr-phone"))
	}
	candidates = append(candidates, filepath.Join("/tmp", fmt.Sprintf("herdr-phone-%d", uid)))

	var lastErr error
	for _, dir := range candidates {
		if err := ensureSecureDir(dir, uid); err != nil {
			lastErr = err
			continue
		}
		if len(dir) > maxSocketPathLen-len("/hp-000000000000.sock") {
			lastErr = fmt.Errorf("daemon: runtime dir %q too long for a socket", dir)
			continue
		}
		return dir, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("daemon: no usable runtime directory")
	}
	return "", lastErr
}

// ensureSecureDir creates dir with mode 0700 if absent and validates that an
// existing dir is a real directory (not a symlink), owned by uid, and not
// accessible by group or other. A hostile squatter at the path is refused rather
// than silently reused, preventing socket hijacking.
func ensureSecureDir(dir string, uid int) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.Mkdir(dir, 0o700); err != nil {
			// A concurrent creator is fine; re-validate below.
			if !os.IsExist(err) {
				return err
			}
		}
		fi, err = os.Lstat(dir)
		if err != nil {
			return err
		}
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("daemon: runtime dir %q is a symlink; refusing", dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("daemon: runtime path %q is not a directory", dir)
	}

	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		if int(st.Uid) != uid {
			return fmt.Errorf("daemon: runtime dir %q is owned by uid %d, not %d", dir, st.Uid, uid)
		}
	}

	// Tighten permissions to owner-only. Refuse if group/other retain access
	// after the attempt (for example on a filesystem that ignores chmod).
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("daemon: chmod runtime dir %q: %w", dir, err)
		}
		fi, err = os.Lstat(dir)
		if err != nil {
			return err
		}
		if fi.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("daemon: runtime dir %q remains group/other accessible", dir)
		}
	}

	return nil
}
