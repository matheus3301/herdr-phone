package app

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStartError(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{startErr: errors.New("cloudflared missing")}
	env, _, errb := newEnv(t, rt, "start")
	if code := Main(env); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(errb.String(), "cloudflared missing") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestStartForegroundError(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{serveErr: errors.New("bind failed")}
	env, _, errb := newEnv(t, rt, "start", "--foreground")
	if code := Main(env); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(errb.String(), "bind failed") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestServeError(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{serveErr: errors.New("state engine crashed")}
	env, _, errb := newEnv(t, rt, "serve")
	if code := Main(env); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(errb.String(), "state engine crashed") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestServeUnknownFlag(t *testing.T) {
	t.Parallel()
	env, _, errb := newEnv(t, &fakeRuntime{}, "serve", "--loud")
	if code := Main(env); code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errb.String(), "unknown flag") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestStopError(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{stopErr: errors.New("control socket unreachable")}
	env, _, errb := newEnv(t, rt, "stop")
	if code := Main(env); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(errb.String(), "control socket unreachable") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestToggleStatusError(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{statusErr: errors.New("state dir unreadable")}
	env, _, errb := newEnv(t, rt, "toggle")
	if code := Main(env); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(errb.String(), "state dir unreadable") {
		t.Errorf("stderr = %q", errb.String())
	}
	if rt.startCalled || rt.stopCalled {
		t.Error("toggle must not act on an unknown state")
	}
}

func TestToggleStartError(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{status: Status{Running: false}, startErr: errors.New("cloudflared missing")}
	env, _, errb := newEnv(t, rt, "toggle")
	if code := Main(env); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(errb.String(), "cloudflared missing") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestToggleStopError(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{status: Status{Running: true}, stopErr: errors.New("control socket unreachable")}
	env, _, errb := newEnv(t, rt, "toggle")
	if code := Main(env); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(errb.String(), "control socket unreachable") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestStatusError(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{statusErr: errors.New("not running")}
	env, _, errb := newEnv(t, rt, "status")
	if code := Main(env); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(errb.String(), "not running") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestSetupLinkError(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{pairErr: errors.New("daemon not running")}
	env, _, errb := newEnv(t, rt, "setup-link")
	if code := Main(env); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(errb.String(), "daemon not running") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestStatusHelpFlag(t *testing.T) {
	t.Parallel()
	env, out, _ := newEnv(t, &fakeRuntime{}, "status", "-h")
	if code := Main(env); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Error("expected usage for status -h")
	}
}

func TestStatusTextAllFields(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	rt := &fakeRuntime{status: Status{
		Running: true, Mode: "named", Health: "degraded",
		PublicURL: "https://h.example.com", LocalAddress: "127.0.0.1:8787",
		Version: "0.1.0", PID: 4242, StartedAt: started,
		HerdrHealthy: true, TunnelHealthy: false, StateHealthy: true, ConnectedClients: 3,
	}}
	env, out, _ := newEnv(t, rt, "status")
	if code := Main(env); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	s := out.String()
	for _, want := range []string{"127.0.0.1:8787", "0.1.0", "4242", "2026-07-23T10:00:00Z", "tunnel:            unhealthy"} {
		if !strings.Contains(s, want) {
			t.Errorf("status text missing %q:\n%s", want, s)
		}
	}
}

func TestStatusJSONWithStartedAt(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	rt := &fakeRuntime{status: Status{Running: true, Mode: "quick", StartedAt: started}}
	env, out, _ := newEnv(t, rt, "status", "--json")
	if code := Main(env); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "2026-07-23T10:00:00Z") {
		t.Errorf("json missing started_at:\n%s", out.String())
	}
}

func TestDoctorRuntimeError(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{doctorErr: errors.New("herdr socket missing")}
	env, out, _ := newEnv(t, rt, "doctor")
	if code := Main(env); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(out.String(), "herdr socket missing") {
		t.Errorf("doctor output:\n%s", out.String())
	}
}
