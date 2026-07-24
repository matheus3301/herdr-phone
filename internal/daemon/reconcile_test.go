package daemon

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestReconcileAbsent(t *testing.T) {
	t.Parallel()
	res := Reconcile(context.Background(), t.TempDir(), time.Second)
	if res.Liveness != LivenessAbsent {
		t.Errorf("liveness = %q, want absent", res.Liveness)
	}
}

func TestReconcileStaleDeadPID(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)
	rt := sampleRuntime()
	rt.PID = pickDeadPID(t)
	if err := WriteRuntime(RuntimePath(dir), rt); err != nil {
		t.Fatal(err)
	}
	res := Reconcile(context.Background(), dir, time.Second)
	if res.Liveness != LivenessStale {
		t.Errorf("liveness = %q, want stale (reason: %s)", res.Liveness, res.Reason)
	}
}

func TestReconcileStaleNoSocket(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)
	rt := sampleRuntime()
	rt.PID = os.Getpid() // alive
	if err := WriteRuntime(RuntimePath(dir), rt); err != nil {
		t.Fatal(err)
	}
	// No control socket present.
	res := Reconcile(context.Background(), dir, time.Second)
	if res.Liveness != LivenessStale {
		t.Errorf("liveness = %q, want stale", res.Liveness)
	}
}

func TestReconcileRunning(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)
	instance := "inst-live-1"

	// Start a real control server that answers with the matching instance id.
	cs, err := Listen(ControlPath(dir), Handlers{
		Status: func(ctx context.Context) (StatusResult, error) {
			return StatusResult{Health: HealthReady, InstanceID: instance, Mode: "quick", PublicURL: "https://x"}, nil
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
	if err := WriteRuntime(RuntimePath(dir), rt); err != nil {
		t.Fatal(err)
	}

	res := Reconcile(context.Background(), dir, 2*time.Second)
	if res.Liveness != LivenessRunning {
		t.Fatalf("liveness = %q, want running (reason: %s)", res.Liveness, res.Reason)
	}
	if res.Status == nil || res.Status.PublicURL != "https://x" {
		t.Errorf("expected running status carried through, got %+v", res.Status)
	}
}

func TestReconcileStaleInstanceMismatch(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)
	cs, err := Listen(ControlPath(dir), Handlers{
		Status: func(ctx context.Context) (StatusResult, error) {
			return StatusResult{InstanceID: "some-other-instance"}, nil
		},
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go cs.Serve()
	defer cs.Close()

	rt := sampleRuntime()
	rt.PID = os.Getpid()
	rt.InstanceID = "expected-instance"
	if err := WriteRuntime(RuntimePath(dir), rt); err != nil {
		t.Fatal(err)
	}

	res := Reconcile(context.Background(), dir, 2*time.Second)
	if res.Liveness != LivenessStale {
		t.Errorf("liveness = %q, want stale on instance mismatch", res.Liveness)
	}
}

func TestCleanupStale(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)
	if err := WriteRuntime(RuntimePath(dir), sampleRuntime()); err != nil {
		t.Fatal(err)
	}
	// Create a placeholder socket file.
	if err := os.WriteFile(ControlPath(dir), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CleanupStale(dir); err != nil {
		t.Fatalf("CleanupStale: %v", err)
	}
	if _, err := os.Stat(RuntimePath(dir)); !os.IsNotExist(err) {
		t.Error("runtime should be removed")
	}
	// Idempotent.
	if err := CleanupStale(dir); err != nil {
		t.Errorf("second CleanupStale: %v", err)
	}
}

func TestProcessAlive(t *testing.T) {
	t.Parallel()
	if !ProcessAlive(os.Getpid()) {
		t.Error("current process should be alive")
	}
	if ProcessAlive(pickDeadPID(t)) {
		t.Error("dead pid should not be alive")
	}
	if ProcessAlive(0) || ProcessAlive(-1) {
		t.Error("non-positive pids are not alive")
	}
}

// pickDeadPID returns a pid that is very unlikely to be running.
func pickDeadPID(t *testing.T) int {
	t.Helper()
	// Search downward from a high pid for one that is not alive.
	for pid := 999999; pid > 100000; pid-- {
		if !ProcessAlive(pid) {
			return pid
		}
	}
	t.Fatal("could not find a dead pid")
	return 0
}
