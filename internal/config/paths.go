package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExpandPath expands a leading "~" and explicit environment variables in p using
// env, then cleans the result. It requires every referenced environment variable
// to be set: an unset variable is an error rather than a silent empty segment. It
// does not resolve symlinks and does not require the path to exist.
func ExpandPath(p string, env func(string) string) (string, error) {
	if env == nil {
		env = os.Getenv
	}
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("path is empty")
	}
	var missing []string
	expanded := os.Expand(p, func(name string) string {
		v := env(name)
		if v == "" {
			missing = append(missing, name)
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("unresolved environment variable(s) %s in path %q", strings.Join(missing, ", "), p)
	}
	if expanded == "~" || strings.HasPrefix(expanded, "~/") {
		home := env("HOME")
		if home == "" {
			return "", fmt.Errorf("cannot expand %q because HOME is not set", p)
		}
		if expanded == "~" {
			expanded = home
		} else {
			expanded = filepath.Join(home, expanded[len("~/"):])
		}
	}
	if strings.TrimSpace(expanded) == "" {
		return "", fmt.Errorf("path %q is empty after expansion", p)
	}
	return filepath.Clean(expanded), nil
}

// looksLikePath reports whether s references a filesystem path (and therefore
// should be expanded) rather than a bare command name looked up on PATH.
func looksLikePath(s string) bool {
	return strings.ContainsAny(s, "/~$")
}

// expandIfPath expands s when it looks like a path, leaving bare command names
// (for example "cloudflared" or "herdr") untouched.
func expandIfPath(s string, env func(string) string) (string, error) {
	if s == "" || !looksLikePath(s) {
		return s, nil
	}
	return ExpandPath(s, env)
}
