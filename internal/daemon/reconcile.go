package daemon

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// Liveness classifies an existing daemon instance discovered from state.
type Liveness string

const (
	// LivenessAbsent means no runtime state exists; a fresh start is safe.
	LivenessAbsent Liveness = "absent"
	// LivenessRunning means a healthy daemon is already serving; the caller
	// should reuse it (idempotent start) rather than replacing it.
	LivenessRunning Liveness = "running"
	// LivenessStale means state exists but no live daemon backs it; the caller
	// may clean up and replace it.
	LivenessStale Liveness = "stale"
)

// ReconcileResult reports the outcome of inspecting an existing instance.
type ReconcileResult struct {
	Liveness Liveness
	// Runtime is the loaded state when present (Running or Stale).
	Runtime Runtime
	// Status is the live control-socket status when Liveness is Running.
	Status *StatusResult
	// Reason is a short human-readable explanation, safe to log.
	Reason string
}

// ProcessAlive reports whether a process with pid exists and is signalable by
// the current user. Signal 0 performs the existence/permission check without
// delivering a signal.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM means the process exists but is owned by another user; that still
	// counts as alive for reconciliation purposes.
	return errors.Is(err, syscall.EPERM)
}

// Reconcile inspects the state directory and decides whether a daemon is already
// running. It never mutates state; the caller decides whether to clean up a
// stale instance. probeTimeout bounds the control-socket status probe.
func Reconcile(ctx context.Context, stateDir string, probeTimeout time.Duration) ReconcileResult {
	runtimePath := RuntimePath(stateDir)
	rt, err := LoadRuntime(runtimePath)
	if err != nil {
		if os.IsNotExist(err) {
			return ReconcileResult{Liveness: LivenessAbsent, Reason: "no runtime state"}
		}
		// A corrupt/incomplete runtime file is treated as stale so a healthy
		// daemon can replace it.
		return ReconcileResult{Liveness: LivenessStale, Reason: "unreadable runtime state"}
	}

	if !ProcessAlive(rt.PID) {
		return ReconcileResult{Liveness: LivenessStale, Runtime: rt, Reason: "recorded pid is not running"}
	}

	// The pid is alive; confirm it is actually our daemon by asking the control
	// socket and matching the instance id. A pid can be recycled by an unrelated
	// process, so process liveness alone is not sufficient.
	controlPath, spErr := SocketPath(stateDir)
	if spErr != nil {
		return ReconcileResult{Liveness: LivenessStale, Runtime: rt, Reason: "control socket path unresolved"}
	}
	if _, statErr := os.Stat(controlPath); statErr != nil {
		return ReconcileResult{Liveness: LivenessStale, Runtime: rt, Reason: "control socket missing"}
	}

	client := NewClient(controlPath).WithTimeout(probeTimeout)
	st, err := client.Ping(ctx)
	if err != nil {
		return ReconcileResult{Liveness: LivenessStale, Runtime: rt, Reason: "control socket unreachable"}
	}
	if st.InstanceID != rt.InstanceID {
		return ReconcileResult{Liveness: LivenessStale, Runtime: rt, Reason: "instance id mismatch"}
	}

	return ReconcileResult{
		Liveness: LivenessRunning,
		Runtime:  rt,
		Status:   &st,
		Reason:   "healthy daemon already running",
	}
}

// CleanupStale removes a stale runtime file and control socket. It is safe to
// call when the files are already absent. Callers must only invoke this after
// Reconcile returns LivenessStale.
func CleanupStale(stateDir string) error {
	paths := []string{RuntimePath(stateDir), ControlPath(stateDir)}
	// Also remove a relocated short socket, if one is used for this state dir.
	if sp, err := SocketPath(stateDir); err == nil {
		paths = append(paths, sp)
	}

	var firstErr error
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
