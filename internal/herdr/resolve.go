package herdr

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveSocketPath resolves the Herdr socket following the spec precedence:
// configured value, then HERDR_SOCKET_PATH, then the default session socket at
// ~/.config/herdr/herdr.sock. A leading "~" in the configured value expands to
// the user's home directory.
func ResolveSocketPath(configured string) (string, error) {
	if p := strings.TrimSpace(configured); p != "" {
		return expandHome(p)
	}
	if p := strings.TrimSpace(os.Getenv("HERDR_SOCKET_PATH")); p != "" {
		return expandHome(p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", newError(CodeConnect, "cannot resolve home directory for default socket")
	}
	return filepath.Join(home, ".config", "herdr", "herdr.sock"), nil
}

// ResolveBinaryPath resolves the Herdr CLI binary following the spec
// precedence: configured value, then HERDR_BIN_PATH, then "herdr" on PATH. The
// returned value is a command name or path suitable for exec; the terminal
// bridge owns the actual invocation.
func ResolveBinaryPath(configured string) string {
	if p := strings.TrimSpace(configured); p != "" {
		if expanded, err := expandHome(p); err == nil {
			return expanded
		}
		return p
	}
	if p := strings.TrimSpace(os.Getenv("HERDR_BIN_PATH")); p != "" {
		return p
	}
	return "herdr"
}

func expandHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", newError(CodeConnect, "cannot expand ~ in path")
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}
