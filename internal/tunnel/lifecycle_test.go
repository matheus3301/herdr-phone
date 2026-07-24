//go:build unix

package tunnel

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeCloudflaredScript is a POSIX-sh stand-in for cloudflared. It emits
// cloudflared-shaped JSON log lines, optionally spawns a lingering grandchild,
// and either handles or ignores SIGTERM.
const fakeCloudflaredScript = `#!/bin/sh
url="https://fake-quick-tunnel.trycloudflare.com"
is_quick=0
for a in "$@"; do
  if [ "$a" = "--url" ]; then is_quick=1; fi
done

# Simulate a descendant that inherits (and keeps open) the stdout/stderr pipe,
# then the leader exits. The log pipe never reaches EOF, so cmd.Wait must return
# via WaitDelay rather than waiting for the copiers to drain.
if [ "${FAKE_CF_LEAK_AND_EXIT:-0}" = "1" ]; then
  sleep 300 &
  echo "{\"level\":\"info\",\"message\":\"Your quick Tunnel has been created! Visit it at $url\",\"time\":\"t\"}" 1>&2
  exit 0
fi

if [ "${FAKE_CF_SPAWN_CHILD:-0}" = "1" ]; then
  sh -c 'while true; do sleep 1; done' &
  child=$!
  echo "{\"level\":\"info\",\"message\":\"child-pid $child\",\"time\":\"t\"}" 1>&2
fi

if [ "$is_quick" = "1" ]; then
  echo "{\"level\":\"info\",\"message\":\"Your quick Tunnel has been created! Visit it at $url\",\"time\":\"t\"}" 1>&2
else
  echo "{\"level\":\"info\",\"message\":\"Registered tunnel connection connIndex=0\",\"time\":\"t\"}" 1>&2
fi

if [ "${FAKE_CF_IGNORE_TERM:-0}" = "1" ]; then
  trap '' TERM
  while true; do sleep 1; done
else
  trap 'exit 0' TERM
  while true; do sleep 1; done
fi
`

func writeFakeCloudflared(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cloudflared")
	if err := os.WriteFile(path, []byte(fakeCloudflaredScript), 0o755); err != nil {
		t.Fatalf("write fake cloudflared: %v", err)
	}
	return path
}

// TestProcessWaitIndependentOfEscapedWriter reproduces M4: a descendant keeps
// the stdout/stderr pipe open after the leader exits, so the log copiers never
// reach EOF. cmd.Wait must still return (via WaitDelay) instead of blocking on
// the drain, so the process is reaped and done() fires.
func TestProcessWaitIndependentOfEscapedWriter(t *testing.T) {
	bin := writeFakeCloudflared(t)
	t.Setenv("FAKE_CF_LEAK_AND_EXIT", "1")
	cfg := Config{Mode: ModeQuick, QuickEnabled: true, Binary: bin, LoopbackPort: 8787, GracePeriod: time.Second}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	h, err := startProcess(context.Background(), cfg, "", nil)
	if err != nil {
		t.Fatalf("startProcess: %v", err)
	}

	select {
	case <-h.done():
		// Reaped despite the lingering pipe writer.
	case <-time.After(6 * time.Second):
		t.Fatal("cmd.Wait blocked on an escaped pipe writer; WaitDelay ineffective")
	}

	// Clean up the lingering grandchild via a group kill.
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = h.stop(stopCtx)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, syscall.Signal(0)) == nil
}

func TestLifecycleQuickReadyAndGracefulStop(t *testing.T) {
	t.Parallel()
	bin := writeFakeCloudflared(t)
	cfg := Config{Mode: ModeQuick, QuickEnabled: true, Binary: bin, LoopbackPort: 8787, GracePeriod: 3 * time.Second}
	s, err := New(cfg, WithMaxConsecutiveFailures(2))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	select {
	case <-s.Ready():
	case <-time.After(5 * time.Second):
		t.Fatalf("quick tunnel never ready; logs=%v", s.RecentLogs())
	}
	if got := s.URL(); got != "https://fake-quick-tunnel.trycloudflare.com" {
		t.Errorf("quick url = %q", got)
	}
	if s.State() != StateReady {
		t.Errorf("state = %q", s.State())
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := s.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestLifecycleNamedTokenCommandDeletesTokenFile(t *testing.T) {
	t.Parallel()
	bin := writeFakeCloudflared(t)
	tokenDir := t.TempDir()
	const secret = "super-secret-tunnel-token-value"
	cfg := Config{
		Mode:         ModeNamed,
		PublicURL:    "https://named.example.com",
		Binary:       bin,
		TokenCommand: []string{"printf", secret},
		TokenDir:     tokenDir,
		LoopbackPort: 8787,
		GracePeriod:  3 * time.Second,
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	select {
	case <-s.Ready():
	case <-time.After(5 * time.Second):
		t.Fatalf("named tunnel never ready; logs=%v", s.RecentLogs())
	}
	if s.URL() != "https://named.example.com" {
		t.Errorf("named url = %q", s.URL())
	}

	// The temporary token file must be deleted after readiness. (The dir also
	// holds the cloudflared pidfile now, so check specifically that no token file
	// remains rather than that the dir is empty.)
	waitFor(t, func() bool {
		entries, _ := os.ReadDir(tokenDir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "herdr-phone-cf-token-") {
				return false
			}
		}
		return true
	})

	// The secret must never appear in captured logs.
	for _, line := range s.RecentLogs() {
		if strings.Contains(line, secret) {
			t.Fatalf("secret leaked into logs: %q", line)
		}
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	_ = s.Stop(stopCtx)
}

func TestLifecycleProcessGroupCleanup(t *testing.T) {
	bin := writeFakeCloudflared(t)
	t.Setenv("FAKE_CF_SPAWN_CHILD", "1")
	cfg := Config{Mode: ModeQuick, QuickEnabled: true, Binary: bin, LoopbackPort: 8787, GracePeriod: 3 * time.Second}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	select {
	case <-s.Ready():
	case <-time.After(5 * time.Second):
		t.Fatalf("never ready; logs=%v", s.RecentLogs())
	}

	childPID := 0
	re := regexp.MustCompile(`child-pid (\d+)`)
	waitFor(t, func() bool {
		for _, line := range s.RecentLogs() {
			if m := re.FindStringSubmatch(line); m != nil {
				childPID, _ = strconv.Atoi(m[1])
				return childPID > 0
			}
		}
		return false
	})
	if !processAlive(childPID) {
		t.Fatalf("grandchild %d should be alive before stop", childPID)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := s.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// The whole process group, including the grandchild, must be gone.
	waitFor(t, func() bool { return !processAlive(childPID) })
}

func TestLifecycleForcedKillWhenTermIgnored(t *testing.T) {
	bin := writeFakeCloudflared(t)
	t.Setenv("FAKE_CF_IGNORE_TERM", "1")
	cfg := Config{Mode: ModeQuick, QuickEnabled: true, Binary: bin, LoopbackPort: 8787, GracePeriod: 500 * time.Millisecond}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	select {
	case <-s.Ready():
	case <-time.After(5 * time.Second):
		t.Fatalf("never ready; logs=%v", s.RecentLogs())
	}

	start := time.Now()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := s.Stop(stopCtx); err != nil {
		t.Fatalf("Stop after ignored TERM: %v", err)
	}
	// Graceful period is short; forced kill must complete well within the test
	// timeout.
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("forced kill took too long: %v", elapsed)
	}
}

func TestLifecycleRestartsAfterRealCrash(t *testing.T) {
	t.Parallel()
	// A binary that exits immediately with failure forces the supervisor to
	// restart until it hits the failure cap and degrades.
	dir := t.TempDir()
	bin := filepath.Join(dir, "cloudflared")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatalf("write crashing fake: %v", err)
	}
	cfg := Config{Mode: ModeQuick, QuickEnabled: true, Binary: bin, LoopbackPort: 8787, GracePeriod: time.Second}
	s, err := New(cfg,
		WithBackoff(Backoff{Base: time.Millisecond, Max: 5 * time.Millisecond, Factor: 2}),
		WithMaxConsecutiveFailures(3),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	waitFor(t, func() bool { return s.State() == StateDegraded })
	<-s.Done()
}
