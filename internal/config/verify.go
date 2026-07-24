//go:build unix

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// VerifySecretFile checks that path is a regular file owned by the current user
// and not readable by group or other. It is used for cloudflared credential and
// token files, which hold secret material. This touches the filesystem and is
// therefore separate from the deterministic structural Validate.
func VerifySecretFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s: must be a regular file", path)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: cannot determine file ownership", path)
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s: must be owned by the current user", path)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s: must not be readable by group or other (permissions are %04o)", path, perm)
	}
	return nil
}

// VerifyWorkspaceRoots confirms every configured root exists, is a directory,
// and resolves through symlinks without error. The paths are expected to be
// already expanded by Load. The fully resolved (symlink-evaluated) path is
// returned so the caller can confine directory listings to canonical roots.
func VerifyWorkspaceRoots(roots []string) ([]string, error) {
	resolved := make([]string, 0, len(roots))
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf("workspace root %q must be an absolute path", root)
		}
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, fmt.Errorf("workspace root %q: %w", root, err)
		}
		info, err := os.Stat(real)
		if err != nil {
			return nil, fmt.Errorf("workspace root %q: %w", root, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("workspace root %q is not a directory", root)
		}
		resolved = append(resolved, real)
	}
	return resolved, nil
}
