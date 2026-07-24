package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeClock is a deterministic Clock. After channels are fired only when the
// test calls fireAfter, so timeouts never depend on wall time. Durations passed
// to After are recorded so tests can assert backoff behavior. Now can be
// advanced to exercise time-elapsed logic.
type fakeClock struct {
	mu        sync.Mutex
	now       time.Time
	pending   []chan time.Time
	durations []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	c.pending = append(c.pending, ch)
	c.durations = append(c.durations, d)
	c.mu.Unlock()
	return ch
}

// afterDurations returns a copy of every duration passed to After so far.
func (c *fakeClock) afterDurations() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.durations...)
}

// fireAfter fires every pending After channel, simulating elapsed time.
func (c *fakeClock) fireAfter() {
	c.mu.Lock()
	pend := c.pending
	c.pending = nil
	c.mu.Unlock()
	for _, ch := range pend {
		ch <- c.now
	}
}

func (c *fakeClock) pendingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

// handlerFunc receives a decoded request and returns the raw JSON response
// frame (without the trailing newline). Returning nil closes the connection
// without replying.
type handlerFunc func(req map[string]any) []byte

// scriptedServer is an in-memory Herdr socket. Each Dial creates a net.Pipe
// whose server end runs a goroutine feeding decoded requests to the handler.
type scriptedServer struct {
	mu       sync.Mutex
	handler  handlerFunc
	dials    int
	conns    []net.Conn
	writeGap func(w *bufio.Writer, frame []byte) // optional custom framing (fragmentation)
	requests []map[string]any
}

func newServer(h handlerFunc) *scriptedServer { return &scriptedServer{handler: h} }

func (s *scriptedServer) dialer() Dialer {
	return DialerFunc(func(ctx context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		s.mu.Lock()
		s.dials++
		s.conns = append(s.conns, server, client)
		gap := s.writeGap
		s.mu.Unlock()
		go s.serve(server, gap)
		return client, nil
	})
}

func (s *scriptedServer) serve(conn net.Conn, gap func(*bufio.Writer, []byte)) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var req map[string]any
		if err := json.Unmarshal(line, &req); err != nil {
			return
		}
		s.mu.Lock()
		s.requests = append(s.requests, req)
		h := s.handler
		s.mu.Unlock()
		resp := h(req)
		if resp == nil {
			return
		}
		if gap != nil {
			gap(w, resp)
		} else {
			w.Write(resp)
			w.WriteByte('\n')
		}
		if err := w.Flush(); err != nil {
			return
		}
	}
}

func (s *scriptedServer) dialCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dials
}

func (s *scriptedServer) lastRequest() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return nil
	}
	return s.requests[len(s.requests)-1]
}

func (s *scriptedServer) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

// reply builds a success frame echoing the request id with the given result.
func reply(req map[string]any, result map[string]any) []byte {
	id, _ := req["id"].(string)
	env := map[string]any{"id": id, "result": result}
	b, _ := json.Marshal(env)
	return b
}

// replyError builds an error frame.
func replyError(req map[string]any, code, msg string) []byte {
	id, _ := req["id"].(string)
	env := map[string]any{"id": id, "error": map[string]any{"code": code, "message": msg}}
	b, _ := json.Marshal(env)
	return b
}

// paramField extracts params.<key> from a decoded request.
func paramField(req map[string]any, key string) any {
	params, _ := req["params"].(map[string]any)
	if params == nil {
		return nil
	}
	return params[key]
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
