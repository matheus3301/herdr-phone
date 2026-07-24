package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func startTestServer(t *testing.T, h Handlers) (string, *ControlServer) {
	t.Helper()
	dir := shortStateDir(t)
	path := filepath.Join(dir, controlFileName)
	cs, err := Listen(path, h)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go cs.Serve()
	t.Cleanup(func() { _ = cs.Close() })
	return path, cs
}

func TestControlSocketPermissions(t *testing.T) {
	t.Parallel()
	path, _ := startTestServer(t, Handlers{})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("socket perm = %o, want 0600", perm)
	}
}

func TestControlStatus(t *testing.T) {
	t.Parallel()
	want := StatusResult{Health: HealthReady, Mode: "named", InstanceID: "inst-1", PID: 99}
	path, _ := startTestServer(t, Handlers{
		Status: func(ctx context.Context) (StatusResult, error) { return want, nil },
	})
	client := NewClient(path)
	got, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.InstanceID != want.InstanceID || got.Health != want.Health || got.Mode != want.Mode {
		t.Errorf("status = %+v, want %+v", got, want)
	}
}

func TestControlRotatePairing(t *testing.T) {
	t.Parallel()
	path, _ := startTestServer(t, Handlers{
		RotatePairing: func(ctx context.Context) (PairingResult, error) {
			return PairingResult{URL: "https://h/#pair=abc"}, nil
		},
	})
	client := NewClient(path)
	pr, err := client.RotatePairing(context.Background())
	if err != nil {
		t.Fatalf("RotatePairing: %v", err)
	}
	if pr.URL != "https://h/#pair=abc" {
		t.Errorf("pairing url = %q", pr.URL)
	}
}

func TestControlStop(t *testing.T) {
	t.Parallel()
	called := make(chan struct{}, 1)
	path, _ := startTestServer(t, Handlers{
		Stop: func(ctx context.Context) error { called <- struct{}{}; return nil },
	})
	client := NewClient(path)
	if err := client.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("stop handler not invoked")
	}
}

func TestControlUnsupportedCommand(t *testing.T) {
	t.Parallel()
	path, _ := startTestServer(t, Handlers{}) // no handlers
	client := NewClient(path)
	if _, err := client.Status(context.Background()); err == nil {
		t.Fatal("expected unsupported status error")
	}
}

func TestControlHandlerError(t *testing.T) {
	t.Parallel()
	path, _ := startTestServer(t, Handlers{
		Status: func(ctx context.Context) (StatusResult, error) {
			return StatusResult{}, errors.New("herdr unreachable")
		},
	})
	client := NewClient(path)
	_, err := client.Status(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestControlCloseRemovesSocket(t *testing.T) {
	t.Parallel()
	path, cs := startTestServer(t, Handlers{})
	if err := cs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("socket should be removed after Close, stat err=%v", err)
	}
}

// flakyListener injects a bounded number of transient Accept errors before
// delegating to the real listener, exercising the accept-retry path (M6).
type flakyListener struct {
	net.Listener
	remaining atomic.Int32
}

type transientErr struct{}

func (transientErr) Error() string   { return "transient accept failure" }
func (transientErr) Timeout() bool   { return false }
func (transientErr) Temporary() bool { return true }

func (l *flakyListener) Accept() (net.Conn, error) {
	if l.remaining.Add(-1) >= 0 {
		return nil, transientErr{}
	}
	return l.Listener.Accept()
}

// TestControlAcceptRetriesTransientErrors reproduces M6: a transient Accept
// error must not permanently disable the control socket. After several transient
// failures the server must still accept and serve a real request.
func TestControlAcceptRetriesTransientErrors(t *testing.T) {
	t.Parallel()
	dir := shortStateDir(t)
	path := filepath.Join(dir, controlFileName)
	cs, err := Listen(path, Handlers{
		Status: func(ctx context.Context) (StatusResult, error) {
			return StatusResult{Health: HealthReady, InstanceID: "inst-flaky"}, nil
		},
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	fl := &flakyListener{Listener: cs.listener}
	fl.remaining.Store(3) // 3 transient errors, then real accepts
	cs.listener = fl
	go cs.Serve()
	t.Cleanup(func() { _ = cs.Close() })

	st, err := NewClient(path).Status(context.Background())
	if err != nil {
		t.Fatalf("Status after transient accept errors: %v", err)
	}
	if st.InstanceID != "inst-flaky" {
		t.Errorf("instance = %q, want inst-flaky", st.InstanceID)
	}
}

func TestSanitizeErrStripsControl(t *testing.T) {
	t.Parallel()
	out := sanitizeErr(errors.New("bad\x00\x1b[31mvalue\n"))
	for _, r := range out {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("control char survived: %q", out)
		}
	}
}
