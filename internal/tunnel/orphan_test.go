//go:build unix

package tunnel

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPidRecordRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rec := pidRecord{PID: 4242, Binary: "/opt/homebrew/bin/cloudflared", CreatedUnixMs: 1_780_000_000_000}
	if err := writePidRecord(dir, rec); err != nil {
		t.Fatalf("writePidRecord: %v", err)
	}
	info, err := os.Stat(pidfilePath(dir))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("pidfile perm = %o, want 0600", perm)
	}
	got, err := readPidRecord(dir)
	if err != nil {
		t.Fatalf("readPidRecord: %v", err)
	}
	if got != rec {
		t.Errorf("round trip = %+v, want %+v", got, rec)
	}
	removePidRecord(dir)
	if _, err := readPidRecord(dir); err == nil {
		t.Error("expected read to fail after removePidRecord")
	}
}

func TestReconcileOrphanNoPidfile(t *testing.T) {
	t.Parallel()
	// Must not panic or error when there is nothing to reconcile.
	reconcileOrphan(t.TempDir(), time.Second, nil)
}

func TestReconcileOrphanDeadPidRemovesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dead := findDeadPID(t)
	if err := writePidRecord(dir, pidRecord{PID: dead, Binary: "cloudflared", CreatedUnixMs: 1}); err != nil {
		t.Fatal(err)
	}
	reconcileOrphan(dir, time.Second, nil)
	if _, err := readPidRecord(dir); err == nil {
		t.Error("pidfile for a dead pid should be removed")
	}
}

// TestReconcileOrphanTerminatesVerifiedChild starts a real long-lived child in
// its own process group, records it as an orphan whose binary matches, and
// asserts reconcile terminates it and clears the pidfile.
func TestReconcileOrphanTerminatesVerifiedChild(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }() // reap when killed
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	// CreatedUnixMs must be ~the child's real start time for the start-time
	// cross-check to accept it.
	if err := writePidRecord(dir, pidRecord{PID: pid, Binary: "sleep", CreatedUnixMs: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}

	reconcileOrphan(dir, 2*time.Second, nil)

	// The verified orphan must be gone and the pidfile cleared.
	waitProcessGone(t, pid, 4*time.Second)
	if _, err := readPidRecord(dir); err == nil {
		t.Error("pidfile should be removed after terminating a verified orphan")
	}
}

// TestReconcileOrphanLeavesUnverifiedProcess ensures a PID whose identity does
// not match the recorded binary is NOT killed (guards against terminating an
// unrelated process that reused the PID).
func TestReconcileOrphanLeavesUnverifiedProcess(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	// Record a mismatching binary: the live process is `sleep`, not cloudflared.
	if err := writePidRecord(dir, pidRecord{PID: pid, Binary: "cloudflared", CreatedUnixMs: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}

	reconcileOrphan(dir, time.Second, nil)

	if !processAliveUnix(pid) {
		t.Error("an unverified process must NOT be terminated")
	}
	// The pidfile is retained for a later, confirmable attempt.
	if _, err := readPidRecord(dir); err != nil {
		t.Errorf("pidfile should be retained when identity is unverified: %v", err)
	}
}

// TestReconcileOrphanLeavesPidReusedByAnotherCloudflared reproduces N1: a PID
// recycled by a *different* same-named process (started much later than the
// recorded time) must fail the start-time cross-check and not be terminated.
func TestReconcileOrphanLeavesPidReusedByAnotherCloudflared(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	// Basename matches ("sleep"), but the recorded creation time is an hour before
	// the live process actually started — i.e. this PID was reused.
	stale := time.Now().Add(-time.Hour).UnixMilli()
	if err := writePidRecord(dir, pidRecord{PID: pid, Binary: "sleep", CreatedUnixMs: stale}); err != nil {
		t.Fatal(err)
	}

	reconcileOrphan(dir, time.Second, nil)

	if !processAliveUnix(pid) {
		t.Error("a PID reused by another same-named process must NOT be terminated")
	}
	if _, err := readPidRecord(dir); err != nil {
		t.Errorf("pidfile should be retained on a start-time mismatch: %v", err)
	}
}

func TestVerifyProcessIdentity(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	now := time.Now().UnixMilli()

	ok, err := verifyProcessIdentity(pidRecord{PID: pid, Binary: "/usr/bin/sleep", CreatedUnixMs: now})
	if err != nil {
		t.Fatalf("verifyProcessIdentity: %v", err)
	}
	if !ok {
		t.Error("expected sleep process to verify (matching basename + fresh start time)")
	}

	// Wrong binary basename.
	if ok, _ := verifyProcessIdentity(pidRecord{PID: pid, Binary: "cloudflared", CreatedUnixMs: now}); ok {
		t.Error("sleep must not verify as cloudflared")
	}

	// Correct basename but stale recorded start time (PID reuse) -> false.
	stale := time.Now().Add(-time.Hour).UnixMilli()
	if ok, _ := verifyProcessIdentity(pidRecord{PID: pid, Binary: "sleep", CreatedUnixMs: stale}); ok {
		t.Error("a start-time mismatch must not verify")
	}

	// Missing recorded start time -> fail safe (error).
	if _, err := verifyProcessIdentity(pidRecord{PID: pid, Binary: "sleep", CreatedUnixMs: 0}); err == nil {
		t.Error("missing recorded start time must fail safe (error)")
	}

	// Empty expected binary -> fail safe (error).
	if _, err := verifyProcessIdentity(pidRecord{PID: pid, Binary: "", CreatedUnixMs: now}); err == nil {
		t.Error("empty expected binary must fail safe (error)")
	}
}

func TestResolvePSIsAbsoluteAndExists(t *testing.T) {
	t.Parallel()
	p, err := resolvePS()
	if err != nil {
		t.Fatalf("resolvePS: %v", err)
	}
	if !filepath.IsAbs(p) {
		t.Errorf("ps path %q is not absolute", p)
	}
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		t.Errorf("resolved ps %q does not exist as a file: %v", p, err)
	}
}

func TestProcessStartTimeMatchesRecord(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	start, err := processStartTime(pid)
	if err != nil {
		t.Fatalf("processStartTime: %v", err)
	}
	if diff := time.Since(start); diff < -startTimeTolerance || diff > time.Minute {
		t.Errorf("reported start time %v is implausibly far from now (diff %v)", start, diff)
	}
}

// TestOrphanIdentityFailsSafeWhenPSUnavailable reproduces N2's fail-safe: with no
// resolvable absolute ps, identity is unconfirmable and a live process is left
// untouched.
func TestOrphanIdentityFailsSafeWhenPSUnavailable(t *testing.T) {
	// Not parallel: mutates the package-level ps candidate list.
	orig := psCandidatePaths
	psCandidatePaths = []string{"/nonexistent/ps", "/also/missing/ps"}
	t.Cleanup(func() { psCandidatePaths = orig })

	if _, err := resolvePS(); err == nil {
		t.Fatal("resolvePS must fail when no absolute ps exists")
	}

	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	dir := t.TempDir()
	if err := writePidRecord(dir, pidRecord{PID: pid, Binary: "sleep", CreatedUnixMs: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}

	reconcileOrphan(dir, time.Second, nil)

	if !processAliveUnix(pid) {
		t.Error("with ps unavailable, identity is unconfirmable and the process must be left alive")
	}
	if _, err := readPidRecord(dir); err != nil {
		t.Errorf("pidfile should be retained when ps is unavailable: %v", err)
	}
}

func TestMacOSKillWindowNoteExported(t *testing.T) {
	t.Parallel()
	if MacOSKillWindowNote == "" {
		t.Fatal("MacOSKillWindowNote must document the residual orphan risk")
	}
}

func findDeadPID(t *testing.T) int {
	t.Helper()
	for pid := 999999; pid > 100000; pid-- {
		if !processAliveUnix(pid) {
			return pid
		}
	}
	t.Fatal("could not find a dead pid")
	return 0
}

func waitProcessGone(t *testing.T, pid int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !processAliveUnix(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d still alive after %v", pid, within)
}
