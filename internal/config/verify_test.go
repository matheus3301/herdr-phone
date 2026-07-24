//go:build unix

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifySecretFileOK(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("t"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifySecretFile(path); err != nil {
		t.Errorf("expected ok, got %v", err)
	}
}

func TestVerifySecretFileRejectsGroupOtherReadable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("t"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := VerifySecretFile(path)
	if err == nil || !strings.Contains(err.Error(), "group or other") {
		t.Fatalf("expected permission rejection, got %v", err)
	}
}

func TestVerifySecretFileRejectsDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := VerifySecretFile(dir)
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("expected regular-file rejection, got %v", err)
	}
}

func TestVerifySecretFileMissing(t *testing.T) {
	t.Parallel()
	err := VerifySecretFile(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestVerifyWorkspaceRootsOK(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resolved, err := VerifyWorkspaceRoots([]string{dir})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	// EvalSymlinks may canonicalize (e.g. /var -> /private/var on macOS); just
	// require a non-empty absolute result.
	if len(resolved) != 1 || !filepath.IsAbs(resolved[0]) {
		t.Errorf("resolved = %v", resolved)
	}
}

func TestVerifyWorkspaceRootsRejectsMissing(t *testing.T) {
	t.Parallel()
	_, err := VerifyWorkspaceRoots([]string{filepath.Join(t.TempDir(), "nope")})
	if err == nil {
		t.Fatal("expected error for missing root")
	}
}

func TestVerifyWorkspaceRootsRejectsFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "afile")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := VerifyWorkspaceRoots([]string{path})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected not-a-directory error, got %v", err)
	}
}

func TestVerifyWorkspaceRootsRejectsRelative(t *testing.T) {
	t.Parallel()
	_, err := VerifyWorkspaceRoots([]string{"relative/path"})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected absolute-path error, got %v", err)
	}
}
