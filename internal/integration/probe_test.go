package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/matheus3301/herdr-phone/internal/daemon"
	"github.com/matheus3301/herdr-phone/internal/server"
)

// probeContractHandler emulates the server's documented /health probe contract
// (see internal/server): it returns the instance id when the exact probe token
// is presented in server.ProbeHeader, and bare "ok" otherwise. The real server
// handler is covered by the server package's own tests; this exercises the
// integration probe client against that contract over real HTTP.
func probeContractHandler(token, instanceID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.Header.Get(server.ProbeHeader) == token {
			_, _ = w.Write([]byte(instanceID))
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
}

func TestQuickPublicProbe(t *testing.T) {
	t.Parallel()
	const token = "probe-secret-token"
	const instanceID = "instance-abc123"
	srv := httptest.NewServer(probeContractHandler(token, instanceID))
	defer srv.Close()

	// Correct token + matching instance id: success.
	if err := quickPublicProbe(context.Background(), srv.URL, token, instanceID); err != nil {
		t.Errorf("valid probe failed: %v", err)
	}
	// Wrong token: the endpoint returns "ok", which is not our instance id.
	if err := quickPublicProbe(context.Background(), srv.URL, "wrong-token", instanceID); err == nil {
		t.Error("probe with wrong token must fail")
	}
	// Right token but the endpoint is a different instance.
	if err := quickPublicProbe(context.Background(), srv.URL, token, "some-other-instance"); err == nil {
		t.Error("probe against a different instance must fail")
	}
}

func TestQuickPublicProbeUnreachable(t *testing.T) {
	t.Parallel()
	// A closed server: the GET fails at the transport layer.
	srv := httptest.NewServer(probeContractHandler("t", "i"))
	url := srv.URL
	srv.Close()
	if err := quickPublicProbe(context.Background(), url, "t", "i"); err == nil {
		t.Error("probe against an unreachable URL must fail")
	}
}

func TestComponentReady(t *testing.T) {
	t.Parallel()
	st := daemon.StatusResult{Components: []daemon.ComponentStatus{
		{Name: "tunnel", Ready: false},
		{Name: "herdr", Ready: true},
	}}
	if componentReady(st, "tunnel") {
		t.Error("tunnel should be reported not ready")
	}
	if !componentReady(st, "herdr") {
		t.Error("herdr should be reported ready")
	}
	if componentReady(st, "absent") {
		t.Error("an absent component is not ready")
	}
}

func TestTerminalFilterFactoryFreshPerSession(t *testing.T) {
	t.Parallel()
	factory := func() *ansiFilterAdapter { return newANSIFilter() }
	a, b := factory(), factory()
	if a == b {
		t.Fatal("factory must return a fresh filter per session")
	}
	// The filter strips a dangerous OSC 52 (clipboard) sequence while preserving
	// ordinary text, confirming it is the real security filter.
	in := []byte("\x1b]52;c;c2VjcmV0\x07visible")
	out := a.FilterOutput(in)
	if string(out) != "visible" {
		t.Errorf("filtered output = %q, want %q", out, "visible")
	}
	// A fragmented escape buffered in one session's filter does not affect another.
	_ = a.FilterOutput([]byte("\x1b]52;c;")) // incomplete OSC left pending in a
	if got := string(b.FilterOutput([]byte("clean"))); got != "clean" {
		t.Errorf("independent filter leaked state: %q", got)
	}
}
