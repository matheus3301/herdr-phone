//go:build unix

package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// MacOSKillWindowNote documents the residual risk that a cloudflared child can
// outlive an abnormally terminated daemon. It is exported so status/error text
// can surface the guarantee and its limit to operators.
//
// macOS has no parent-death signal (PDEATHSIG). If the daemon is SIGKILLed,
// OOM-killed, or panics without unwinding, its graceful/context teardown never
// runs and cloudflared keeps the public tunnel up as an orphan. The daemon
// closes this window on its next start by reconciling the recorded pidfile and
// terminating a verified orphan before starting a fresh child; the tunnel is
// only exposed for the interval between the crash and that next start.
const MacOSKillWindowNote = "macOS has no parent-death signal: a cloudflared child can outlive a SIGKILLed daemon and keep the public tunnel up until the next start reconciles and terminates the verified orphan."

// pidfileName is the fixed pidfile name within the pid directory.
const pidfileName = "cloudflared.pid"

// pidRecord is the persisted identity of a cloudflared child, used to safely
// reconcile an orphan on the next start.
type pidRecord struct {
	PID           int    `json:"pid"`
	Binary        string `json:"binary"`
	CreatedUnixMs int64  `json:"created_unix_ms"`
}

func pidfilePath(dir string) string {
	return filepath.Join(dir, pidfileName)
}

// writePidRecord atomically writes rec to the pidfile with mode 0600.
func writePidRecord(dir string, rec pidRecord) error {
	if dir == "" {
		return errors.New("tunnel: pid dir is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("tunnel: create pid dir: %w", err)
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	path := pidfilePath(dir)
	tmp, err := os.CreateTemp(dir, ".cloudflared-*.pid")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func readPidRecord(dir string) (pidRecord, error) {
	data, err := os.ReadFile(pidfilePath(dir))
	if err != nil {
		return pidRecord{}, err
	}
	var rec pidRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return pidRecord{}, err
	}
	if rec.PID <= 0 {
		return pidRecord{}, errors.New("tunnel: pidfile has no pid")
	}
	return rec, nil
}

func removePidRecord(dir string) {
	_ = os.Remove(pidfilePath(dir))
}

// reconcileOrphan terminates a cloudflared child recorded in dir's pidfile when,
// and only when, a process with that PID is still alive AND is verified to be
// the same cloudflared binary. This prevents killing an unrelated process that
// happens to have reused the PID. If identity cannot be confirmed, the process
// is left untouched (fail safe) and the pidfile is retained. The pidfile is
// removed once the recorded process is gone.
//
// grace bounds the wait between SIGTERM and SIGKILL of the orphan's group.
func reconcileOrphan(dir string, grace time.Duration, sink func(LogLine)) {
	rec, err := readPidRecord(dir)
	if err != nil {
		// No pidfile (clean prior shutdown) or unreadable: nothing to reconcile.
		return
	}

	if !processAliveUnix(rec.PID) {
		removePidRecord(dir)
		return
	}

	ok, verifyErr := verifyProcessIdentity(rec)
	if verifyErr != nil || !ok {
		// Could not positively confirm this is our cloudflared (identity or
		// start-time mismatch, or ps unavailable). Do not kill a possibly
		// unrelated PID; leave the record for a later, confirmable attempt.
		emit(sink, fmt.Sprintf("tunnel: left possible orphan pid=%d unverified (not terminating); %s", rec.PID, MacOSKillWindowNote))
		return
	}

	emit(sink, fmt.Sprintf("tunnel: terminating verified orphaned cloudflared pid=%d from a prior daemon; %s", rec.PID, MacOSKillWindowNote))
	terminateGroup(rec.PID, grace)
	removePidRecord(dir)
}

// terminateGroup sends SIGTERM to the process group, waits up to grace for it to
// exit, then sends SIGKILL. The child was started with its own process group
// (Setpgid), so the group id equals the leader pid.
func terminateGroup(pid int, grace time.Duration) {
	_ = signalGroup(pid, syscall.SIGTERM)
	if grace <= 0 {
		grace = defaultGracePeriod
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !processAliveUnix(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = signalGroup(pid, syscall.SIGKILL)
}

func signalGroup(pid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pid, sig); err != nil {
		return syscall.Kill(pid, sig)
	}
	return nil
}

func processAliveUnix(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}

// verifyProcessIsBinary confirms the live process pid is running the expected
// cloudflared binary, comparing the process command basename to the configured
// startTimeTolerance bounds the difference between a pidfile's recorded creation
// time and the live process's OS start time. It absorbs ps's whole-second
// truncation of the start time plus the small gap between fork/exec and the
// daemon recording the pidfile, while staying tight enough that a reused PID —
// whose process necessarily started much later (after the prior daemon died) —
// falls outside the window.
const startTimeTolerance = 3 * time.Second

// psCandidatePaths are absolute locations of the system ps on macOS and Linux
// (usr-merged and not). Resolving ps absolutely removes any PATH-integrity
// dependency from a security-relevant kill decision. It is a var so tests can
// exercise the unavailable→fail-safe path.
var psCandidatePaths = []string{"/bin/ps", "/usr/bin/ps"}

// resolvePS returns an absolute path to the system ps, or an error when none of
// the known locations exist so the caller fails safe (never kills).
func resolvePS() (string, error) {
	for _, p := range psCandidatePaths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("tunnel: ps not found at a known absolute path")
}

// verifyProcessIdentity confirms the live process for rec.PID is the same
// cloudflared the pidfile recorded, using two independent signals:
//
//  1. its command basename matches the recorded binary basename, and
//  2. its OS start time is within startTimeTolerance of the recorded creation
//     time — so a PID recycled by a *different* same-named cloudflared (started
//     much later) cannot match (finding N1).
//
// It resolves ps to an absolute system path (finding N2). An empty expected
// binary, an unavailable ps, or any inability to confirm identity returns
// (false, err) so the caller leaves the process untouched (fail safe).
func verifyProcessIdentity(rec pidRecord) (bool, error) {
	expected := commandBase(rec.Binary)
	if expected == "" {
		return false, errors.New("tunnel: no expected binary to verify against")
	}

	comm, err := processComm(rec.PID)
	if err != nil {
		return false, err
	}
	if comm == "" || commandBase(comm) != expected {
		return false, nil
	}

	// Start-time cross-check. A valid record always carries CreatedUnixMs; a
	// missing/zero value cannot be verified, so fail safe rather than fall back
	// to the weaker basename-only match.
	if rec.CreatedUnixMs <= 0 {
		return false, errors.New("tunnel: pidfile has no recorded start time")
	}
	start, err := processStartTime(rec.PID)
	if err != nil {
		return false, err
	}
	diff := start.Sub(time.UnixMilli(rec.CreatedUnixMs))
	if diff < -startTimeTolerance || diff > startTimeTolerance {
		return false, nil
	}
	return true, nil
}

// processComm returns the live process's command (ps comm), via absolute ps.
func processComm(pid int) (string, error) {
	psBin, err := resolvePS()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// `ps -p <pid> -o comm=` prints the executable path/name with no header.
	out, err := exec.CommandContext(ctx, psBin, "-p", fmt.Sprintf("%d", pid), "-o", "comm=").Output()
	if err != nil {
		return "", fmt.Errorf("tunnel: ps identity check failed: %w", redactExecError(err))
	}
	return strings.TrimSpace(string(out)), nil
}

// processStartTime returns the live process's OS start time, via absolute ps.
// `ps -o lstart=` prints an absolute, whole-second local timestamp on both macOS
// and Linux; LC_ALL/LANG=C forces the stable English form the layout expects.
func processStartTime(pid int) (time.Time, error) {
	psBin, err := resolvePS()
	if err != nil {
		return time.Time{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, psBin, "-o", "lstart=", "-p", fmt.Sprintf("%d", pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("tunnel: ps start-time check failed: %w", redactExecError(err))
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return time.Time{}, errors.New("tunnel: empty ps start time")
	}
	t, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("tunnel: parse ps start time: %w", err)
	}
	return t, nil
}

// commandBase reduces a binary path or ps comm value to its basename for a
// stable identity comparison.
func commandBase(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// ps comm can include a trailing argument on some platforms; take the first
	// whitespace-delimited field, then its path basename.
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		s = s[:i]
	}
	return filepath.Base(s)
}

func emit(sink func(LogLine), msg string) {
	if sink == nil {
		return
	}
	sink(LogLine{Raw: sanitizeLogText(msg), Message: msg})
}
