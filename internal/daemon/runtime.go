// Package daemon provides the long-lived herdr-phone daemon primitives: a
// private mode-0600 Unix control socket, secret-free runtime state, start-time
// reconciliation against an existing instance, generic child supervision behind
// small interfaces, status aggregation, a pairing-rotation hook, and graceful
// stop. It deliberately owns no HTTP, Herdr, or tunnel logic; those subsystems
// are injected through interfaces so the daemon stays testable and decoupled.
package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Health is the coarse readiness of the daemon as a whole.
type Health string

const (
	// HealthStarting means the daemon is initializing subsystems.
	HealthStarting Health = "starting"
	// HealthReady means all supervised subsystems report ready.
	HealthReady Health = "ready"
	// HealthDegraded means at least one subsystem is unavailable.
	HealthDegraded Health = "degraded"
	// HealthStopping means graceful shutdown is in progress.
	HealthStopping Health = "stopping"
)

// Runtime is the on-disk description of a running daemon. It contains only
// non-secret operational metadata; pairing secrets, tokens, JWTs, and cookies
// must never be written here.
type Runtime struct {
	PID         int    `json:"pid"`
	InstanceID  string `json:"instance_id"`
	Mode        string `json:"mode"`
	LocalAddr   string `json:"local_addr"`
	PublicURL   string `json:"public_url"`
	Version     string `json:"version"`
	StartUnixMs int64  `json:"start_unix_ms"`
	Health      Health `json:"health"`
}

// StartTime returns the daemon start time.
func (r Runtime) StartTime() time.Time {
	return time.UnixMilli(r.StartUnixMs)
}

// runtimeFileName is the fixed name of the runtime state file within the state
// directory.
const runtimeFileName = "runtime.json"

// RuntimePath returns the runtime state path for a given state directory.
func RuntimePath(stateDir string) string {
	return filepath.Join(stateDir, runtimeFileName)
}

// WriteRuntime writes r to path atomically with mode 0600. The parent directory
// is created with mode 0700 if missing.
func WriteRuntime(path string, r Runtime) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("daemon: create state dir: %w", err)
	}
	// Tighten the directory in case it pre-existed with looser permissions.
	_ = os.Chmod(dir, 0o700)

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("daemon: marshal runtime: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".runtime-*.tmp")
	if err != nil {
		return fmt.Errorf("daemon: create temp runtime: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we fail before the rename.
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("daemon: chmod temp runtime: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("daemon: write temp runtime: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("daemon: sync temp runtime: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("daemon: close temp runtime: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("daemon: replace runtime: %w", err)
	}
	return nil
}

// LoadRuntime reads and validates a runtime file. A missing file returns
// os.ErrNotExist wrapped so callers can detect it with errors.Is.
func LoadRuntime(path string) (Runtime, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Runtime{}, err
	}
	var r Runtime
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Runtime{}, fmt.Errorf("daemon: decode runtime: %w", err)
	}
	if r.PID <= 0 || r.InstanceID == "" {
		return Runtime{}, fmt.Errorf("daemon: runtime file is incomplete")
	}
	return r, nil
}

// UpdateRuntimeHealth loads, mutates the health field, and rewrites the runtime
// file atomically. A missing file is not an error.
func UpdateRuntimeHealth(path string, health Health) error {
	r, err := LoadRuntime(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	r.Health = health
	return WriteRuntime(path, r)
}
