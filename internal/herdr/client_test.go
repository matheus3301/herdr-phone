package herdr

import (
	"bufio"
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, s *scriptedServer, opts ...Option) *Client {
	t.Helper()
	base := []Option{WithClock(newFakeClock())}
	return NewClient(s.dialer(), append(base, opts...)...)
}

func TestPing(t *testing.T) {
	t.Parallel()
	s := newServer(func(req map[string]any) []byte {
		if req["method"] != "ping" {
			return replyError(req, "not_found", "unexpected")
		}
		return reply(req, map[string]any{
			"type": "pong", "version": "0.7.5", "protocol": 17,
			"capabilities": map[string]any{"live_handoff": true, "detached_server_daemon": true},
		})
	})
	c := newTestClient(t, s)
	p, err := c.Ping(context.Background())
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if p.Protocol != 17 || p.Version != "0.7.5" || !p.Capabilities.LiveHandoff {
		t.Fatalf("unexpected pong: %+v", p)
	}
}

func TestHandshakeProtocolMismatch(t *testing.T) {
	t.Parallel()
	s := newServer(func(req map[string]any) []byte {
		return reply(req, map[string]any{"type": "pong", "version": "0.9.0", "protocol": 99})
	})
	c := newTestClient(t, s)
	_, err := c.Handshake(context.Background())
	if !IsCode(err, CodeIncompatible) {
		t.Fatalf("want incompatible, got %v", err)
	}
}

func TestResultTypeValidation(t *testing.T) {
	t.Parallel()
	s := newServer(func(req map[string]any) []byte {
		// Wrong discriminator for a ping.
		return reply(req, map[string]any{"type": "not_pong"})
	})
	c := newTestClient(t, s)
	_, err := c.Ping(context.Background())
	if !IsCode(err, CodeUnexpectedTyp) {
		t.Fatalf("want unexpected_type, got %v", err)
	}
}

func TestServerErrorPreservedAndSanitized(t *testing.T) {
	t.Parallel()
	s := newServer(func(req map[string]any) []byte {
		// Message carries an ANSI escape and a newline that must be stripped.
		return replyError(req, "not_found", "pane \x1b[31mgone\x1b[0m\nsecond")
	})
	c := newTestClient(t, s)
	_, err := c.Snapshot(context.Background())
	if !IsCode(err, "not_found") {
		t.Fatalf("want not_found, got %v", err)
	}
	if strings.ContainsRune(err.Error(), 0x1b) || strings.Contains(err.Error(), "\n") {
		t.Fatalf("error message not sanitized: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "gone") {
		t.Fatalf("message content lost: %q", err.Error())
	}
}

func TestResponseIDMismatch(t *testing.T) {
	t.Parallel()
	s := newServer(func(req map[string]any) []byte {
		env := map[string]any{"id": "not-the-request-id", "result": map[string]any{"type": "pong", "protocol": 17}}
		return mustJSON(t, env)
	})
	c := newTestClient(t, s)
	_, err := c.Ping(context.Background())
	if !IsCode(err, CodeProtocol) {
		t.Fatalf("want protocol error, got %v", err)
	}
}

func TestFragmentedUTF8Response(t *testing.T) {
	t.Parallel()
	// A label with multi-byte runes; the server will fragment the frame at
	// arbitrary byte boundaries, including inside runes.
	label := "café ☕ 日本語 — done"
	s := newServer(func(req map[string]any) []byte {
		return reply(req, map[string]any{
			"type": "workspace_info",
			"workspace": map[string]any{
				"workspace_id": "w1", "label": label, "agent_status": "idle",
			},
		})
	})
	// Fragment every 3 bytes to guarantee splitting multi-byte runes.
	s.writeGap = func(w *bufio.Writer, frame []byte) {
		full := append(append([]byte{}, frame...), '\n')
		for i := 0; i < len(full); i += 3 {
			end := min(i+3, len(full))
			w.Write(full[i:end])
			w.Flush()
		}
	}
	c := newTestClient(t, s)
	ws, err := c.WorkspaceFocus(context.Background(), "w1")
	if err != nil {
		t.Fatalf("focus: %v", err)
	}
	if ws.Label != label {
		t.Fatalf("label corrupted across fragments: got %q want %q", ws.Label, label)
	}
}

func TestFrameTooLarge(t *testing.T) {
	t.Parallel()
	s := newServer(func(req map[string]any) []byte {
		big := strings.Repeat("x", 4096)
		return reply(req, map[string]any{"type": "pong", "protocol": 17, "version": big})
	})
	c := newTestClient(t, s, WithMaxResponseBytes(256))
	_, err := c.Ping(context.Background())
	if !IsCode(err, CodeFrameTooLarge) {
		t.Fatalf("want frame_too_large, got %v", err)
	}
}

func TestTimeoutClosesConnection(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	s := newServer(func(req map[string]any) []byte {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release // never reply until released; forces the client to time out
		return nil
	})
	t.Cleanup(func() { close(release) })

	c := NewClient(s.dialer(), WithClock(clock), WithTimeout(5*time.Second))

	type res struct {
		err error
	}
	done := make(chan res, 1)
	go func() {
		_, err := c.Ping(context.Background())
		done <- res{err: err}
	}()

	// Wait until the request reached the server (so the client is mid-read) and
	// the client registered its timeout channel, then fire it.
	<-entered
	waitFor(t, func() bool { return clock.pendingCount() > 0 })
	clock.fireAfter()

	select {
	case r := <-done:
		if !IsCode(r.err, CodeTimeout) {
			t.Fatalf("want timeout, got %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not return after timeout fired (connection not cleaned up)")
	}
}

func TestContextCancelReturns(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	s := newServer(func(req map[string]any) []byte {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return nil
	})
	t.Cleanup(func() { close(release) })

	c := NewClient(s.dialer(), WithClock(newFakeClock()), WithTimeout(0))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Ping(ctx)
		done <- err
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !IsCode(err, CodeCanceled) {
			t.Fatalf("want canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not return after context cancel")
	}
}

func TestRequestIDsAreStringsAndUnique(t *testing.T) {
	t.Parallel()
	s := newServer(func(req map[string]any) []byte {
		return reply(req, map[string]any{"type": "pong", "protocol": 17})
	})
	c := newTestClient(t, s)
	for range 3 {
		if _, err := c.Ping(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// The harness records requests; ids must be distinct strings.
	seen := map[string]bool{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, req := range s.requests {
		id, ok := req["id"].(string)
		if !ok || id == "" {
			t.Fatalf("request id not a non-empty string: %v", req["id"])
		}
		if seen[id] {
			t.Fatalf("duplicate request id %q", id)
		}
		seen[id] = true
	}
}

// waitFor spins until cond holds, yielding the scheduler. It fails the test if
// cond never holds within a bounded number of iterations.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for range 100_000 {
		if cond() {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("condition not met")
}
