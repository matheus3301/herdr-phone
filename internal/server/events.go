package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// eventClient is one subscribed browser. Its queue holds at most the newest
// snapshot: consecutive updates coalesce, so a slow reader never grows an
// unbounded backlog, and a genuinely stuck client is dropped by its write
// deadline in the writer loop (section 11).
type eventClient struct {
	ch   chan Snapshot
	done chan struct{}
	once sync.Once
}

func newEventClient() *eventClient {
	return &eventClient{ch: make(chan Snapshot, 1), done: make(chan struct{})}
}

// enqueue coalesces to the newest snapshot without blocking the broadcaster.
func (c *eventClient) enqueue(s Snapshot) {
	select {
	case c.ch <- s:
		return
	default:
	}
	// Queue full: drop the stale snapshot and replace with the newest.
	select {
	case <-c.ch:
	default:
	}
	select {
	case c.ch <- s:
	default:
	}
}

func (c *eventClient) stop() {
	c.once.Do(func() { close(c.done) })
}

// hub fans one state subscription out to many event clients.
type hub struct {
	mu        sync.RWMutex
	clients   map[*eventClient]struct{}
	cancelSub func()
}

func newHub(state StateProvider) *hub {
	h := &hub{clients: make(map[*eventClient]struct{})}
	h.cancelSub = state.Subscribe(h.broadcast)
	return h
}

func (h *hub) broadcast(s Snapshot) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		c.enqueue(s)
	}
}

func (h *hub) add(c *eventClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *hub) remove(c *eventClient) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

func (h *hub) count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *hub) close() {
	if h.cancelSub != nil {
		h.cancelSub()
	}
	h.mu.Lock()
	for c := range h.clients {
		c.stop()
		delete(h.clients, c)
	}
	h.mu.Unlock()
}

// Event WebSocket tunables.
const (
	eventPingInterval = 25 * time.Second
	eventWriteTimeout = 10 * time.Second
	eventReadLimit    = 4096 // clients only send pings/close on /events
)

// handleEvents upgrades to a WebSocket and streams snapshot updates. The initial
// snapshot is sent immediately so a fresh client is consistent without waiting
// for the next change.
//
// Origin is already enforced by the central middleware (exact allowlist plus
// CrossOriginProtection) before this handler runs, so the accept skips the
// library's own origin check. Liveness is handled by periodic pings whose pong
// is awaited: a purely passive listener stays connected, while a silent or gone
// peer trips the ping and is dropped. That reading of control frames happens in
// the reader goroutine, which the coder/websocket API requires.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "websocket upgrade failed")
		return
	}
	conn.SetReadLimit(eventReadLimit)

	ctx, cancel := context.WithCancel(s.baseCtx)
	defer cancel()

	client := newEventClient()
	s.hub.add(client)
	defer func() {
		s.hub.remove(client)
		client.stop()
		_ = conn.Close(websocket.StatusNormalClosure, "bye")
	}()

	// Reader goroutine: process control frames and detect disconnect.
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				client.stop()
				cancel()
				return
			}
		}
	}()

	send := func(snap Snapshot) bool {
		b, err := json.Marshal(envelope{Type: "snapshot", Snapshot: &snap})
		if err != nil {
			return false
		}
		wctx, wcancel := context.WithTimeout(ctx, eventWriteTimeout)
		defer wcancel()
		return conn.Write(wctx, websocket.MessageText, b) == nil
	}

	// Send the current snapshot immediately.
	if !send(s.deps.State.Snapshot()) {
		return
	}

	ping := time.NewTicker(eventPingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-client.done:
			return
		case snap := <-client.ch:
			if !send(snap) {
				return
			}
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, eventWriteTimeout)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		}
	}
}

// envelope is the JSON frame sent to event clients.
type envelope struct {
	Type     string    `json:"type"`
	Snapshot *Snapshot `json:"snapshot,omitempty"`
}
