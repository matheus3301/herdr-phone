//go:build unix

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSocketPathShortStaysInStateDir(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)
	got, err := SocketPath(dir)
	if err != nil {
		t.Fatalf("SocketPath: %v", err)
	}
	if got != ControlPath(dir) {
		t.Errorf("short state dir socket = %q, want in-state-dir %q", got, ControlPath(dir))
	}
	if len(got) > maxSocketPathLen {
		t.Errorf("socket path length %d exceeds limit %d", len(got), maxSocketPathLen)
	}
}

func TestSocketPathLongRelocates(t *testing.T) {
	t.Parallel()
	// Build a state dir path longer than the socket limit.
	longDir := filepath.Join(t.TempDir(), strings.Repeat("nested-directory-segment/", 6))
	if len(ControlPath(longDir)) <= maxSocketPathLen {
		t.Skipf("constructed dir not long enough on this platform: %d", len(ControlPath(longDir)))
	}
	got, err := SocketPath(longDir)
	if err != nil {
		t.Fatalf("SocketPath: %v", err)
	}
	if got == ControlPath(longDir) {
		t.Errorf("long state dir socket was not relocated: %q", got)
	}
	if len(got) > maxSocketPathLen {
		t.Errorf("relocated socket length %d exceeds limit %d", len(got), maxSocketPathLen)
	}
}

func TestSocketPathDeterministic(t *testing.T) {
	t.Parallel()
	longDir := filepath.Join(t.TempDir(), strings.Repeat("nested-directory-segment/", 6))
	if len(ControlPath(longDir)) <= maxSocketPathLen {
		t.Skip("dir not long enough to force relocation")
	}
	a, err := SocketPath(longDir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := SocketPath(longDir)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("SocketPath not deterministic: %q vs %q", a, b)
	}
	// A different state dir must map to a different socket file.
	other := filepath.Join(t.TempDir(), strings.Repeat("nested-directory-segment/", 6))
	c, err := SocketPath(other)
	if err != nil {
		t.Fatal(err)
	}
	if c == a {
		t.Errorf("distinct state dirs collided on socket path %q", a)
	}
}

func TestEnsureUserRuntimeDirSecure(t *testing.T) {
	t.Parallel()
	dir, err := ensureUserRuntimeDir()
	if err != nil {
		t.Fatalf("ensureUserRuntimeDir: %v", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("runtime dir must not be a symlink")
	}
	if !info.IsDir() {
		t.Errorf("runtime dir is not a directory")
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("runtime dir perm = %o, want owner-only", perm)
	}
}

func TestEnsureSecureDirRejectsSymlink(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := filepath.Join(base, "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := ensureSecureDir(link, os.Getuid()); err == nil {
		t.Error("expected ensureSecureDir to reject a symlink")
	}
}

func TestEnsureSecureDirRejectsNonDir(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	file := filepath.Join(base, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureSecureDir(file, os.Getuid()); err == nil {
		t.Error("expected ensureSecureDir to reject a non-directory")
	}
}

func TestEnsureSecureDirTightensPermissions(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	loose := filepath.Join(base, "loose")
	if err := os.Mkdir(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureSecureDir(loose, os.Getuid()); err != nil {
		t.Fatalf("ensureSecureDir: %v", err)
	}
	info, err := os.Lstat(loose)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("perm after tighten = %o, want owner-only", perm)
	}
}

func TestEnsureSecureDirCreatesMissing(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "created")
	if err := ensureSecureDir(dir, os.Getuid()); err != nil {
		t.Fatalf("ensureSecureDir: %v", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Errorf("created dir mode = %v", info.Mode())
	}
}

// TestServeDialOverLongStateDir is the end-to-end path-length fix: a daemon with
// a long state dir must still bind a control socket a client can reach, while
// runtime state remains in the (long) state dir.
func TestServeDialOverLongStateDir(t *testing.T) {
	t.Parallel()
	longDir := filepath.Join(t.TempDir(), strings.Repeat("segment-xyz/", 8))
	if err := os.MkdirAll(longDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if len(ControlPath(longDir)) <= maxSocketPathLen {
		t.Skip("state dir not long enough to exercise relocation")
	}

	opts := baseOptions(longDir)
	d := New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Serve(ctx); err != nil {
		t.Fatalf("Serve over long state dir: %v", err)
	}
	defer d.Shutdown(context.Background())

	// Runtime state stays in the (long) state dir.
	if _, err := os.Stat(RuntimePath(longDir)); err != nil {
		t.Errorf("runtime.json should be in state dir: %v", err)
	}

	// The client resolves the same relocated socket and can talk to the daemon.
	client, err := NewClientForStateDir(longDir)
	if err != nil {
		t.Fatalf("NewClientForStateDir: %v", err)
	}
	if _, err := client.Status(context.Background()); err != nil {
		t.Fatalf("Status over relocated socket: %v", err)
	}

	// Socket is cleaned up on shutdown.
	sockPath, _ := SocketPath(longDir)
	if err := d.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("relocated socket should be removed after shutdown, stat err=%v", err)
	}
}

func TestReconcileRunningOverLongStateDir(t *testing.T) {
	t.Parallel()
	longDir := filepath.Join(t.TempDir(), strings.Repeat("segment-xyz/", 8))
	if err := os.MkdirAll(longDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if len(ControlPath(longDir)) <= maxSocketPathLen {
		t.Skip("state dir not long enough")
	}

	instance := "inst-long-1"
	sockPath, err := SocketPath(longDir)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := Listen(sockPath, Handlers{
		Status: func(ctx context.Context) (StatusResult, error) {
			return StatusResult{InstanceID: instance, Health: HealthReady}, nil
		},
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go cs.Serve()
	defer cs.Close()

	rt := sampleRuntime()
	rt.PID = os.Getpid()
	rt.InstanceID = instance
	if err := WriteRuntime(RuntimePath(longDir), rt); err != nil {
		t.Fatal(err)
	}

	res := Reconcile(context.Background(), longDir, controlDeadline)
	if res.Liveness != LivenessRunning {
		t.Fatalf("liveness = %q, want running (reason %q)", res.Liveness, res.Reason)
	}
}
