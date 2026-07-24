//go:build unix

package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockFileName is the advisory lock file held for a daemon's lifetime.
const lockFileName = "daemon.lock"

// ErrStateLocked indicates another live daemon already owns the state directory.
var ErrStateLocked = errors.New("daemon: state directory is locked by another daemon")

// StateLock is a process-lifetime exclusive advisory lock over a state
// directory. Holding it across reconcile+bind guarantees only one daemon can own
// a state dir, so concurrent starts cannot remove or rebind each other's control
// socket or clobber runtime.json. The lock is advisory (flock) and is released
// automatically by the kernel when the process exits — including an abnormal
// exit — so a crashed daemon never leaves the directory permanently locked.
type StateLock struct {
	f    *os.File
	path string
}

// AcquireStateLock takes the exclusive lock for stateDir without blocking. It
// returns ErrStateLocked if another daemon holds it. Call Release (or let the
// process exit) to free it.
func AcquireStateLock(stateDir string) (*StateLock, error) {
	if stateDir == "" {
		return nil, errors.New("daemon: state dir is required for locking")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("daemon: create state dir: %w", err)
	}
	_ = os.Chmod(stateDir, 0o700)

	path := filepath.Join(stateDir, lockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("daemon: open lock file: %w", err)
	}
	// Tighten in case it pre-existed with looser perms.
	_ = f.Chmod(0o600)

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrStateLocked
		}
		return nil, fmt.Errorf("daemon: flock: %w", err)
	}
	return &StateLock{f: f, path: path}, nil
}

// Release unlocks and closes the lock file. It is safe to call more than once.
func (l *StateLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	fd := int(l.f.Fd())
	_ = syscall.Flock(fd, syscall.LOCK_UN)
	err := l.f.Close()
	l.f = nil
	return err
}
