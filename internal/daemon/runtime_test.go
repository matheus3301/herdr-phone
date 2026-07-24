package daemon

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func sampleRuntime() Runtime {
	return Runtime{
		PID:         4242,
		InstanceID:  "inst-abc",
		Mode:        "named",
		LocalAddr:   "127.0.0.1:8787",
		PublicURL:   "https://h.example.com",
		Version:     "0.1.0",
		StartUnixMs: 1780000000000,
		Health:      HealthReady,
	}
}

func TestWriteLoadRuntimeRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := RuntimePath(dir)
	in := sampleRuntime()
	if err := WriteRuntime(path, in); err != nil {
		t.Fatalf("WriteRuntime: %v", err)
	}
	out, err := LoadRuntime(path)
	if err != nil {
		t.Fatalf("LoadRuntime: %v", err)
	}
	if out != in {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", out, in)
	}
}

func TestWriteRuntimePermissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := RuntimePath(dir)
	if err := WriteRuntime(path, sampleRuntime()); err != nil {
		t.Fatalf("WriteRuntime: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("runtime perm = %o, want 0600", perm)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("state dir perm = %o, want 0700", perm)
	}
}

func TestRuntimeContainsNoSecretFields(t *testing.T) {
	t.Parallel()
	// The Runtime struct must not carry secret-bearing JSON keys.
	data, err := json.Marshal(sampleRuntime())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	lower := strings.ToLower(string(data))
	for _, forbidden := range []string{"token", "secret", "pair", "cookie", "jwt", "password", "credential"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("runtime JSON must not contain %q: %s", forbidden, data)
		}
	}
}

func TestLoadRuntimeMissing(t *testing.T) {
	t.Parallel()
	_, err := LoadRuntime(RuntimePath(t.TempDir()))
	if !os.IsNotExist(err) {
		t.Fatalf("expected not-exist error, got %v", err)
	}
}

func TestLoadRuntimeRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := RuntimePath(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"pid":1,"instance_id":"x","surprise":"boom"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRuntime(path); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestLoadRuntimeRejectsIncomplete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := RuntimePath(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"pid":0,"instance_id":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRuntime(path); err == nil {
		t.Fatal("expected error for incomplete runtime")
	}
}

func TestUpdateRuntimeHealth(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := RuntimePath(dir)
	if err := WriteRuntime(path, sampleRuntime()); err != nil {
		t.Fatal(err)
	}
	if err := UpdateRuntimeHealth(path, HealthStopping); err != nil {
		t.Fatalf("UpdateRuntimeHealth: %v", err)
	}
	out, err := LoadRuntime(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.Health != HealthStopping {
		t.Errorf("health = %q, want stopping", out.Health)
	}
	// Missing file is not an error.
	if err := UpdateRuntimeHealth(RuntimePath(t.TempDir()), HealthStopping); err != nil {
		t.Errorf("UpdateRuntimeHealth on missing file: %v", err)
	}
}
