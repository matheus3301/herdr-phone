package daemon

import (
	"os"
	"testing"
)

// shortStateDir returns a state directory with a short absolute path so the
// control socket stays under the platform sun_path limit (~104 bytes on macOS).
// t.TempDir() can exceed that on macOS, so socket-bearing tests use this.
func shortStateDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "hp")
	if err != nil {
		// Fall back to the default temp dir if /tmp is unavailable.
		return t.TempDir()
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
