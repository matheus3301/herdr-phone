package integration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/matheus3301/herdr-phone/internal/server"
)

// errOutsideRoots is returned when a requested directory is not within any
// configured workspace root.
var errOutsideRoots = errors.New("path is outside the configured workspace roots")

// dirValidator confines directory browsing to a set of canonical (already
// symlink-resolved) workspace roots. It resolves symlinks on every request and
// rejects any path whose real location escapes the roots, so a symlink planted
// under a root cannot be used to read elsewhere.
type dirValidator struct {
	roots []string // absolute, symlink-resolved
}

var _ server.DirectoryValidator = dirValidator{}

func (d dirValidator) Resolve(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("empty path")
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("not a directory")
	}
	for _, root := range d.roots {
		if real == root || strings.HasPrefix(real, root+string(os.PathSeparator)) {
			return real, nil
		}
	}
	return "", errOutsideRoots
}
