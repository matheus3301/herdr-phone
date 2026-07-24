package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matheus3301/herdr-phone/internal/buildinfo"
	"github.com/matheus3301/herdr-phone/internal/config"
)

// TestMain replaces the signal-aware context with a plain cancellable one so
// tests never install real OS signal handlers.
func TestMain(m *testing.M) {
	newSignalContext = func() (context.Context, context.CancelFunc) {
		return context.WithCancel(context.Background())
	}
	os.Exit(m.Run())
}

const quickConfig = `
[cloudflare]
mode = "quick"
quick_enabled = true
`

// fakeRuntime is a controllable Runtime implementation for CLI tests.
type fakeRuntime struct {
	startResult StartResult
	startErr    error
	serveErr    error
	stopResult  StopResult
	stopErr     error
	status      Status
	statusErr   error
	pairing     PairingLink
	pairErr     error
	doctor      DoctorReport
	doctorErr   error

	startOpts   StartOptions
	serveCalled bool
	serveOpts   ServeOptions
}

func (f *fakeRuntime) Start(_ context.Context, _ config.Config, opts StartOptions) (StartResult, error) {
	f.startOpts = opts
	return f.startResult, f.startErr
}
func (f *fakeRuntime) Serve(_ context.Context, _ config.Config, opts ServeOptions) error {
	f.serveCalled = true
	f.serveOpts = opts
	return f.serveErr
}
func (f *fakeRuntime) Stop(_ context.Context, _ config.Config) (StopResult, error) {
	return f.stopResult, f.stopErr
}
func (f *fakeRuntime) Status(_ context.Context, _ config.Config) (Status, error) {
	return f.status, f.statusErr
}
func (f *fakeRuntime) RotatePairing(_ context.Context, _ config.Config) (PairingLink, error) {
	return f.pairing, f.pairErr
}
func (f *fakeRuntime) Doctor(_ context.Context, _ config.Config) (DoctorReport, error) {
	return f.doctor, f.doctorErr
}

// newEnv builds an Environment backed by a temporary valid quick-mode config and
// captured output buffers.
func newEnv(t *testing.T, rt Runtime, args ...string) (Environment, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	cfgDir := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(quickConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"HERDR_PLUGIN_CONFIG_DIR": cfgDir, "HOME": home}
	var out, errb bytes.Buffer
	env := Environment{
		Getenv:  func(k string) string { return values[k] },
		Args:    args,
		Stdout:  &out,
		Stderr:  &errb,
		Runtime: rt,
	}
	return env, &out, &errb
}

func TestVersion(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"version", "--version", "-v"} {
		env, out, _ := newEnv(t, nil, arg)
		if code := Main(env); code != exitOK {
			t.Fatalf("%s exit = %d", arg, code)
		}
		want := buildinfo.Name + " " + buildinfo.Version + "\n"
		if out.String() != want {
			t.Errorf("%s output = %q, want %q", arg, out.String(), want)
		}
	}
}

func TestHelp(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"help", "--help", "-h"} {
		env, out, _ := newEnv(t, nil, arg)
		if code := Main(env); code != exitOK {
			t.Fatalf("%s exit = %d", arg, code)
		}
		if !strings.Contains(out.String(), "Usage:") {
			t.Errorf("%s did not print usage", arg)
		}
	}
}

func TestNoArgs(t *testing.T) {
	t.Parallel()
	env, _, errb := newEnv(t, nil)
	if code := Main(env); code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errb.String(), "Usage:") {
		t.Error("expected usage on stderr")
	}
}

func TestUnknownCommand(t *testing.T) {
	t.Parallel()
	env, _, errb := newEnv(t, nil, "frobnicate")
	if code := Main(env); code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errb.String(), "unknown command") {
		t.Error("expected unknown-command error")
	}
}

func TestVersionRejectsExtraArgs(t *testing.T) {
	t.Parallel()
	env, _, _ := newEnv(t, nil, "version", "extra")
	if code := Main(env); code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
}

func TestStartPrintsResult(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{startResult: StartResult{Mode: "quick", PublicURL: "https://x.trycloudflare.com", PairingURL: "https://x.trycloudflare.com/#pair=abc"}}
	env, out, _ := newEnv(t, rt, "start")
	if code := Main(env); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	s := out.String()
	if !strings.Contains(s, "quick mode") || !strings.Contains(s, "trycloudflare.com") || !strings.Contains(s, "#pair=abc") {
		t.Errorf("start output missing fields:\n%s", s)
	}
	if rt.startOpts.Quick || rt.startOpts.Foreground {
		t.Errorf("unexpected opts: %+v", rt.startOpts)
	}
}

func TestStartQuickFlag(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{startResult: StartResult{Mode: "quick"}}
	env, _, _ := newEnv(t, rt, "start", "--quick")
	if code := Main(env); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !rt.startOpts.Quick {
		t.Error("expected Quick option set")
	}
}

func TestStartForegroundCallsServe(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{}
	env, _, _ := newEnv(t, rt, "start", "--foreground")
	if code := Main(env); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !rt.serveCalled {
		t.Error("expected Serve to be called for --foreground")
	}
}

func TestStartAlreadyRunning(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{startResult: StartResult{Mode: "named", AlreadyRunning: true}}
	env, out, _ := newEnv(t, rt, "start")
	if code := Main(env); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "already running") {
		t.Errorf("expected already-running message, got %q", out.String())
	}
}

func TestStartUnknownFlag(t *testing.T) {
	t.Parallel()
	env, _, errb := newEnv(t, &fakeRuntime{}, "start", "--turbo")
	if code := Main(env); code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errb.String(), "unknown flag") {
		t.Error("expected unknown-flag error")
	}
}

func TestServeCommand(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{}
	env, _, _ := newEnv(t, rt, "serve", "--quick")
	if code := Main(env); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !rt.serveCalled || !rt.serveOpts.Quick {
		t.Errorf("serve not invoked with quick: called=%v opts=%+v", rt.serveCalled, rt.serveOpts)
	}
}

func TestStop(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{stopResult: StopResult{WasRunning: true}}
	env, out, _ := newEnv(t, rt, "stop")
	if code := Main(env); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "stopped") {
		t.Errorf("got %q", out.String())
	}
}

func TestStopNotRunning(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{stopResult: StopResult{WasRunning: false}}
	env, out, _ := newEnv(t, rt, "stop")
	if code := Main(env); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "was not running") {
		t.Errorf("got %q", out.String())
	}
}

func TestStatusText(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{status: Status{Running: true, Mode: "named", Health: "ready", PublicURL: "https://h.example.com", HerdrHealthy: true, TunnelHealthy: true, StateHealthy: true, ConnectedClients: 2}}
	env, out, _ := newEnv(t, rt, "status")
	if code := Main(env); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	s := out.String()
	if !strings.Contains(s, "running (ready)") || !strings.Contains(s, "named") || !strings.Contains(s, "connected clients: 2") {
		t.Errorf("status text missing fields:\n%s", s)
	}
}

func TestStatusNotRunning(t *testing.T) {
	t.Parallel()
	env, out, _ := newEnv(t, &fakeRuntime{status: Status{Running: false}}, "status")
	if code := Main(env); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "not running") {
		t.Errorf("got %q", out.String())
	}
}

func TestStatusJSON(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{status: Status{Running: true, Mode: "named", Protocol: 17, ConnectedClients: 1, Health: "ready"}}
	env, out, _ := newEnv(t, rt, "status", "--json")
	if code := Main(env); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("status --json emitted invalid JSON: %v\n%s", err, out.String())
	}
	if payload["mode"] != "named" || payload["running"] != true {
		t.Errorf("json payload = %v", payload)
	}
	if payload["protocol"].(float64) != 17 {
		t.Errorf("protocol = %v", payload["protocol"])
	}
}

func TestSetupLinkPrintsURL(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{pairing: PairingLink{URL: "https://h.example.com/#pair=deadbeef"}}
	env, out, _ := newEnv(t, rt, "setup-link")
	if code := Main(env); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "#pair=deadbeef") {
		t.Errorf("setup-link did not print the URL:\n%s", out.String())
	}
}

func TestDoctorAllOK(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{doctor: DoctorReport{Checks: []DoctorCheck{
		{Name: "Herdr", OK: true, Detail: "protocol 17"},
		{Name: "cloudflared", OK: true, Detail: "2026.7.2"},
	}}}
	env, out, _ := newEnv(t, rt, "doctor")
	if code := Main(env); code != exitOK {
		t.Fatalf("exit = %d\n%s", code, out.String())
	}
	s := out.String()
	if !strings.Contains(s, "Configuration: valid") || !strings.Contains(s, "All checks passed") {
		t.Errorf("doctor output:\n%s", s)
	}
}

func TestDoctorFailingCheck(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{doctor: DoctorReport{Checks: []DoctorCheck{
		{Name: "Herdr", OK: false, Detail: "socket not found"},
	}}}
	env, out, _ := newEnv(t, rt, "doctor")
	if code := Main(env); code != exitError {
		t.Fatalf("exit = %d, want %d\n%s", code, exitError, out.String())
	}
	if !strings.Contains(out.String(), "Some checks failed") {
		t.Errorf("doctor output:\n%s", out.String())
	}
}

func TestRuntimeUnavailable(t *testing.T) {
	t.Parallel()
	for _, cmd := range [][]string{{"start"}, {"stop"}, {"status"}, {"setup-link"}} {
		env, _, errb := newEnv(t, nil, cmd...)
		if code := Main(env); code != exitError {
			t.Fatalf("%v exit = %d, want %d", cmd, code, exitError)
		}
		if !strings.Contains(errb.String(), "orchestration backend not configured") {
			t.Errorf("%v error = %q", cmd, errb.String())
		}
	}
}

func TestDoctorRuntimeUnavailableStillChecksConfig(t *testing.T) {
	t.Parallel()
	env, out, _ := newEnv(t, nil, "doctor")
	if code := Main(env); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	s := out.String()
	if !strings.Contains(s, "Configuration: valid") {
		t.Error("doctor should still validate config without a runtime")
	}
	if !strings.Contains(s, "Environment diagnostics") {
		t.Error("doctor should report the missing environment backend")
	}
}

func TestConfigLoadFailureReported(t *testing.T) {
	t.Parallel()
	cfgDir := t.TempDir()
	home := t.TempDir()
	// Invalid: quick mode without quick_enabled.
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("[cloudflare]\nmode = \"quick\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"HERDR_PLUGIN_CONFIG_DIR": cfgDir, "HOME": home}
	var out, errb bytes.Buffer
	env := Environment{
		Getenv:  func(k string) string { return values[k] },
		Args:    []string{"start"},
		Stdout:  &out,
		Stderr:  &errb,
		Runtime: &fakeRuntime{},
	}
	if code := Main(env); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(errb.String(), "quick_enabled") {
		t.Errorf("expected config error, got %q", errb.String())
	}
}
