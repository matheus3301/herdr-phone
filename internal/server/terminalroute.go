package server

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/matheus3301/herdr-phone/internal/terminal"
)

const (
	defaultTermCols = 80
	defaultTermRows = 24
	maxTermDim      = 10000
	// terminalReadLimit bounds a single browser input message.
	terminalReadLimit = 1 << 20
	// terminalPingTimeout bounds how long a keepalive ping waits for its pong
	// before the terminal is considered dead.
	terminalPingTimeout = 30 * time.Second
)

// handleTerminal opens an interactive terminal WebSocket bridged to a Herdr
// terminal controller subprocess (section 13). Takeover of an existing
// controller requires a scoped single-use confirmation nonce, validated before
// the socket is upgraded.
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	ident := identityFrom(r.Context())
	paneID := r.PathValue("pane_id")
	if paneID == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "missing pane id")
		return
	}
	if s.deps.TerminalLauncher == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "terminal launcher unavailable")
		return
	}
	// Fail closed: without an injected output filter the terminal would relay raw
	// escape sequences unfiltered, so refuse to open one rather than pass through.
	if s.deps.TerminalFilterFactory == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "terminal filtering unavailable")
		return
	}

	q := r.URL.Query()
	cols := queryDim(q.Get("cols"), defaultTermCols)
	rows := queryDim(q.Get("rows"), defaultTermRows)

	// Attach is generation-checked: the expected generation is mandatory so a
	// client can never attach to a recycled pane by omitting it (H1, SPEC §11).
	var expectedGen uint64
	if v := q.Get("expected_generation"); v != "" {
		g, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeBadRequest, "invalid generation")
			return
		}
		expectedGen = g
	}
	if expectedGen == 0 {
		writeError(w, http.StatusBadRequest, codeGenerationStale, "expected_generation is required to attach a terminal")
		return
	}

	takeover := q.Get("takeover") == "1" || q.Get("takeover") == "true"
	if takeover {
		// Takeover must be explicitly confirmed with a scoped, single-use nonce.
		confirmation := q.Get("confirmation")
		norm, _ := normalizeParams(nil) // takeover params are empty ("{}")
		if confirmation == "" || !s.nonces.consume(confirmation, opTakeover, paneID, expectedGen, ident.SessionID, hashParams(norm)) {
			writeError(w, http.StatusForbidden, codeConfirmationInvalid, "takeover confirmation invalid or expired")
			return
		}
	}

	// Generation guard: verify the asserted generation before launching so we
	// never attach to a stale/replaced pane.
	gen, exists := s.deps.State.Generation(paneID)
	if !exists {
		writeError(w, http.StatusConflict, codeGenerationStale, "pane no longer exists")
		return
	}
	if gen != expectedGen {
		writeError(w, http.StatusConflict, codeGenerationStale, "pane changed; refresh and retry")
		return
	}

	// Origin is already enforced by the central middleware, so the accept skips
	// the library's own origin check.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "websocket upgrade failed")
		return
	}
	conn.SetReadLimit(terminalReadLimit)

	// The session context is tied to the server base context (so Close() tears
	// down live terminals) and is cancelled when this handler returns.
	base, cancelBase := context.WithCancel(s.baseCtx)
	defer cancelBase()

	s.deps.Audit.Record(AuditEntry{
		Event:     "terminal.open",
		Subject:   ident.Subject,
		SessionID: ident.SessionID,
		Resource:  paneID,
		Detail:    takeoverDetail(takeover),
	})

	opts := terminal.Options{
		Spec: terminal.Spec{
			PaneID:   paneID,
			Cols:     cols,
			Rows:     rows,
			Takeover: takeover,
		},
		// A fresh filter per controller: fragmented escapes cannot bleed across
		// reconnects.
		NewFilter: s.deps.TerminalFilterFactory,
		Audit: func(ev terminal.Event) {
			s.deps.Audit.Record(AuditEntry{
				Event:     "terminal." + ev.Kind,
				SessionID: ident.SessionID,
				Resource:  paneID,
				Bytes:     ev.Bytes,
				Detail:    ev.Reason,
			})
		},
	}

	// Run bridges the socket to the controller until the session ends.
	_ = terminal.Run(base, newCoderConn(base, conn), s.deps.TerminalLauncher, opts)

	s.deps.Audit.Record(AuditEntry{
		Event:     "terminal.close",
		SessionID: ident.SessionID,
		Resource:  paneID,
	})
}

func takeoverDetail(takeover bool) string {
	if takeover {
		return "takeover"
	}
	return "attach"
}

func queryDim(v string, def int) int {
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	if n > maxTermDim {
		return maxTermDim
	}
	return n
}

// coderConn adapts a *coder/websocket.Conn to the narrow terminal.Conn
// interface, translating that interface's deadline model onto the library's
// context-per-operation model.
//
//   - Reads use the session context: liveness is enforced by the ping loop
//     (Ping awaits its pong) and by Close aborting a blocked read, so an idle
//     but live client is never dropped by a per-read kill deadline.
//   - Writes derive a per-call context from the requested write deadline; an
//     immediate write deadline (used at teardown) cancels an in-flight write.
type coderConn struct {
	c    *websocket.Conn
	base context.Context

	mu             sync.Mutex
	writeDeadline  time.Time
	curWriteCancel context.CancelFunc
}

func newCoderConn(base context.Context, c *websocket.Conn) *coderConn {
	return &coderConn{c: c, base: base}
}

func (a *coderConn) ReadMessage() (terminal.MessageType, []byte, error) {
	mt, data, err := a.c.Read(a.base)
	if err != nil {
		return 0, nil, err
	}
	return terminal.MessageType(mt), data, nil
}

func (a *coderConn) WriteBinary(b []byte) error { return a.write(websocket.MessageBinary, b) }
func (a *coderConn) WriteText(b []byte) error   { return a.write(websocket.MessageText, b) }

func (a *coderConn) write(typ websocket.MessageType, data []byte) error {
	ctx, cancel := a.writeContext()
	a.mu.Lock()
	a.curWriteCancel = cancel
	a.mu.Unlock()

	err := a.c.Write(ctx, typ, data)

	a.mu.Lock()
	a.curWriteCancel = nil
	a.mu.Unlock()
	cancel()
	return err
}

func (a *coderConn) writeContext() (context.Context, context.CancelFunc) {
	a.mu.Lock()
	dl := a.writeDeadline
	a.mu.Unlock()
	if dl.IsZero() {
		return context.WithCancel(a.base)
	}
	return context.WithDeadline(a.base, dl)
}

// Ping sends a ping and waits for the matching pong, giving true liveness. The
// payload is ignored (the library manages ping payloads).
func (a *coderConn) Ping([]byte) error {
	ctx, cancel := context.WithTimeout(a.base, terminalPingTimeout)
	defer cancel()
	return a.c.Ping(ctx)
}

func (a *coderConn) SetReadDeadline(time.Time) error {
	// Intentionally a no-op: see coderConn docs. Read liveness is via Ping/Close.
	return nil
}

func (a *coderConn) SetWriteDeadline(t time.Time) error {
	a.mu.Lock()
	a.writeDeadline = t
	// An immediate (past/now) deadline aborts any write currently in flight.
	if !t.IsZero() && !t.After(time.Now()) && a.curWriteCancel != nil {
		a.curWriteCancel()
	}
	a.mu.Unlock()
	return nil
}

func (a *coderConn) Close(code int, reason string) error {
	return a.c.Close(websocket.StatusCode(code), truncateCloseReason(reason))
}

// truncateCloseReason bounds a close reason to the RFC 6455 / library limit of
// 123 bytes and ensures valid UTF-8, since coder/websocket rejects an oversized
// or invalid reason.
func truncateCloseReason(reason string) string {
	const max = 123
	if len(reason) <= max && utf8.ValidString(reason) {
		return reason
	}
	if len(reason) > max {
		reason = reason[:max]
	}
	for !utf8.ValidString(reason) && len(reason) > 0 {
		reason = reason[:len(reason)-1]
	}
	return reason
}
