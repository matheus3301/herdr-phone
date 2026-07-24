package terminal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errConnClosed = errors.New("fake conn closed")

// ---- fakes ----------------------------------------------------------------

type inMsg struct {
	mt   MessageType
	data []byte
}

type fakeConn struct {
	inbound chan inMsg

	mu          sync.Mutex
	binaryOut   [][]byte
	textOut     [][]byte
	pings       int
	closeCode   int
	closeReason string
	closed      bool

	closedCh   chan struct{}
	writeAbort chan struct{} // closed by SetWriteDeadline(past) to abort a stalled write

	// gate, when non-nil, blocks every write until it is released, closed, or
	// aborted by a write deadline, simulating a slow client for backpressure.
	gate chan struct{}
	// pingGate, when non-nil, blocks Ping until it is closed or the conn closes,
	// simulating a high-latency/lost pong for keepalive tests.
	pingGate chan struct{}
}

func newFakeConn() *fakeConn {
	return &fakeConn{
		inbound:    make(chan inMsg, 64),
		closedCh:   make(chan struct{}),
		writeAbort: make(chan struct{}),
	}
}

func (c *fakeConn) push(mt MessageType, data []byte) { c.inbound <- inMsg{mt, data} }

func (c *fakeConn) gateWait() error {
	if c.gate == nil {
		return nil
	}
	c.mu.Lock()
	abort := c.writeAbort
	c.mu.Unlock()
	// Unblock on release, close, or an immediate write deadline, mirroring how a
	// real net.Conn aborts a stalled write.
	select {
	case <-c.gate:
		return nil
	case <-c.closedCh:
		return errConnClosed
	case <-abort:
		return errConnClosed
	}
}

func (c *fakeConn) ReadMessage() (MessageType, []byte, error) {
	select {
	case m, ok := <-c.inbound:
		if !ok {
			return 0, nil, errConnClosed
		}
		return m.mt, m.data, nil
	case <-c.closedCh:
		return 0, nil, errConnClosed
	}
}

func (c *fakeConn) WriteBinary(b []byte) error {
	if err := c.gateWait(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errConnClosed
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	c.binaryOut = append(c.binaryOut, cp)
	return nil
}

func (c *fakeConn) WriteText(b []byte) error {
	if err := c.gateWait(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errConnClosed
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	c.textOut = append(c.textOut, cp)
	return nil
}

func (c *fakeConn) Ping([]byte) error {
	c.mu.Lock()
	c.pings++
	gate := c.pingGate
	c.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-c.closedCh:
			return errConnClosed
		}
	}
	return nil
}

func (c *fakeConn) pingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pings
}

func (c *fakeConn) SetReadDeadline(time.Time) error { return nil }

func (c *fakeConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !t.IsZero() && !t.After(time.Now()) {
		// Past deadline: abort any in-flight write.
		select {
		case <-c.writeAbort:
		default:
			close(c.writeAbort)
		}
		return nil
	}
	// Future/zero deadline: re-arm a fresh abort channel if the previous one was
	// already tripped.
	select {
	case <-c.writeAbort:
		c.writeAbort = make(chan struct{})
	default:
	}
	return nil
}

func (c *fakeConn) Close(code int, reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	c.closeCode = code
	c.closeReason = reason
	close(c.closedCh)
	return nil
}

func (c *fakeConn) texts() []serverMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]serverMessage, 0, len(c.textOut))
	for _, t := range c.textOut {
		var m serverMessage
		if json.Unmarshal(t, &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func (c *fakeConn) binaries() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.binaryOut))
	copy(out, c.binaryOut)
	return out
}

func (c *fakeConn) closeInfo() (bool, int, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed, c.closeCode, c.closeReason
}

type fakeController struct {
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	cmds    chan controllerCommand

	once    sync.Once
	term    chan struct{}
	exitErr error
}

func newFakeController() *fakeController {
	or, ow := io.Pipe()
	ir, iw := io.Pipe()
	c := &fakeController{
		stdoutR: or, stdoutW: ow,
		stdinR: ir, stdinW: iw,
		cmds: make(chan controllerCommand, 64),
		term: make(chan struct{}),
	}
	go c.decodeStdin()
	return c
}

func (c *fakeController) decodeStdin() {
	dec := json.NewDecoder(c.stdinR)
	for {
		var cmd controllerCommand
		if err := dec.Decode(&cmd); err != nil {
			return
		}
		c.cmds <- cmd
	}
}

func (c *fakeController) Stdout() io.Reader { return c.stdoutR }
func (c *fakeController) Stdin() io.Writer  { return c.stdinW }

func (c *fakeController) emit(line string) error {
	_, err := io.WriteString(c.stdoutW, line+"\n")
	return err
}

func (c *fakeController) emitFrame(seq uint64, full bool, width, height int, payload []byte) error {
	rec := controllerRecord{
		Type: recordFrame, Seq: seq, Encoding: "ansi",
		Width: width, Height: height, Full: full,
		Bytes: base64.StdEncoding.EncodeToString(payload),
	}
	b, _ := json.Marshal(rec)
	return c.emit(string(b))
}

func (c *fakeController) emitClosed(reason string) error {
	b, _ := json.Marshal(controllerRecord{Type: recordClosed, Reason: reason})
	return c.emit(string(b))
}

func (c *fakeController) stop(err error) {
	c.once.Do(func() {
		c.exitErr = err
		close(c.term)
		_ = c.stdoutW.Close()
		_ = c.stdinW.Close()
	})
}

func (c *fakeController) Terminate() error { c.stop(nil); return nil }
func (c *fakeController) Wait() error      { <-c.term; return c.exitErr }

type fakeLauncher struct {
	ctrl *fakeController
	err  error

	mu   sync.Mutex
	spec Spec
}

func (l *fakeLauncher) Launch(_ context.Context, spec Spec) (Controller, error) {
	l.mu.Lock()
	l.spec = spec
	l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	return l.ctrl, nil
}

func (l *fakeLauncher) lastSpec() Spec {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.spec
}

// recordingFilter proves the injected filter is applied to every frame: it
// removes the byte sequence "SECRET".
type recordingFilter struct {
	mu    sync.Mutex
	calls int
}

func (f *recordingFilter) FilterOutput(src []byte) []byte {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return []byte(strings.ReplaceAll(string(src), "SECRET", ""))
}

// joiningFilter buffers an incomplete trailing "\x1b[" escape start and only
// emits it once completed on a later call, exercising fragmented-sequence
// handling across frames.
type joiningFilter struct {
	buf []byte
}

func (f *joiningFilter) FilterOutput(src []byte) []byte {
	data := append(f.buf, src...)
	f.buf = nil
	// If the chunk ends mid-escape (ESC not yet followed by a final byte), hold
	// the tail back until the next chunk completes it.
	if i := strings.LastIndexByte(string(data), 0x1b); i >= 0 && i == len(data)-1 {
		f.buf = data[i:]
		data = data[:i]
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out
}

// ---- helpers --------------------------------------------------------------

type harness struct {
	conn     *fakeConn
	ctrl     *fakeController
	launcher *fakeLauncher
	done     chan error
	events   *eventSink
}

type eventSink struct {
	mu     sync.Mutex
	events []Event
}

func (s *eventSink) add(ev Event) {
	s.mu.Lock()
	s.events = append(s.events, ev)
	s.mu.Unlock()
}

func (s *eventSink) kinds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.events))
	for i, e := range s.events {
		out[i] = e.Kind
	}
	return out
}

func (s *eventSink) has(kind string) bool {
	return slices.Contains(s.kinds(), kind)
}

func start(t *testing.T, opts Options) *harness {
	t.Helper()
	conn := newFakeConn()
	ctrl := newFakeController()
	launcher := &fakeLauncher{ctrl: ctrl}
	sink := &eventSink{}
	opts.Audit = sink.add
	if opts.Spec.PaneID == "" {
		opts.Spec = Spec{PaneID: "pane-1", Cols: 80, Rows: 24}
	}
	h := &harness{conn: conn, ctrl: ctrl, launcher: launcher, done: make(chan error, 1), events: sink}
	go func() { h.done <- Run(context.Background(), conn, launcher, opts) }()
	return h
}

func (h *harness) waitDone(t *testing.T) {
	t.Helper()
	select {
	case <-h.done:
	case <-time.After(3 * time.Second):
		t.Fatal("bridge did not return")
	}
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met: %s", msg)
}

func recvCmd(t *testing.T, ch chan controllerCommand, want string) controllerCommand {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case cmd := <-ch:
			if cmd.Type == want {
				return cmd
			}
		case <-deadline:
			t.Fatalf("did not receive %q command", want)
		}
	}
}

// ---- tests ----------------------------------------------------------------

func TestFrameForwardedAsFilteredBinary(t *testing.T) {
	t.Parallel()
	filter := &recordingFilter{}
	h := start(t, Options{NewFilter: func() Filter { return filter }})

	if err := h.ctrl.emitFrame(1, true, 80, 24, []byte("visibleSECRETtext")); err != nil {
		t.Fatalf("emit: %v", err)
	}
	waitFor(t, func() bool { return len(h.conn.binaries()) == 1 }, "one binary frame")
	got := string(h.conn.binaries()[0])
	if got != "visibletext" {
		t.Fatalf("filtered frame = %q, want %q", got, "visibletext")
	}
	if filter.calls == 0 {
		t.Fatal("filter was not applied")
	}

	_ = h.ctrl.emitClosed("bye")
	h.waitDone(t)
}

func TestNonMonotonicSequenceClosesPolicy(t *testing.T) {
	t.Parallel()
	h := start(t, Options{})
	_ = h.ctrl.emitFrame(5, false, 0, 0, []byte("a"))
	waitFor(t, func() bool { return len(h.conn.binaries()) == 1 }, "first frame")
	_ = h.ctrl.emitFrame(5, false, 0, 0, []byte("b")) // seq not increasing
	h.waitDone(t)

	closed, code, _ := h.conn.closeInfo()
	if !closed || code != closePolicy {
		t.Fatalf("close code = %d, want policy", code)
	}
	if !h.events.has(EventProtocolErr) {
		t.Fatalf("expected protocol_error event, got %v", h.events.kinds())
	}
}

func TestBrowserInputForwardedToStdin(t *testing.T) {
	t.Parallel()
	h := start(t, Options{})
	h.conn.push(MessageBinary, []byte("ls -la\r"))
	cmd := recvCmd(t, h.ctrl.cmds, cmdInput)
	dec, err := base64.StdEncoding.DecodeString(cmd.Bytes)
	if err != nil || string(dec) != "ls -la\r" {
		t.Fatalf("stdin input = %q (err %v)", dec, err)
	}
	if !h.events.has(EventInput) {
		t.Fatal("expected input event")
	}
	h.ctrl.stop(nil)
	h.waitDone(t)
}

func TestResizeCoalescedToLatest(t *testing.T) {
	t.Parallel()
	h := start(t, Options{ResizeMinInterval: 30 * time.Millisecond})
	for _, c := range []int{90, 100, 120} {
		b, _ := json.Marshal(browserCommand{Type: browserResize, Cols: c, Rows: 40})
		h.conn.push(MessageText, b)
	}
	cmd := recvCmd(t, h.ctrl.cmds, cmdResize)
	if cmd.Cols != 120 || cmd.Rows != 40 {
		t.Fatalf("coalesced resize = %dx%d, want 120x40", cmd.Cols, cmd.Rows)
	}
	h.ctrl.stop(nil)
	h.waitDone(t)
}

func TestScrollForwarded(t *testing.T) {
	t.Parallel()
	h := start(t, Options{})
	b, _ := json.Marshal(browserCommand{Type: browserScroll, Direction: "up", Lines: 3, Source: "wheel"})
	h.conn.push(MessageText, b)
	cmd := recvCmd(t, h.ctrl.cmds, cmdScroll)
	if cmd.Direction != "up" || cmd.Lines != 3 {
		t.Fatalf("scroll = %+v", cmd)
	}
	h.ctrl.stop(nil)
	h.waitDone(t)
}

func TestReleaseClosesSession(t *testing.T) {
	t.Parallel()
	h := start(t, Options{})
	b, _ := json.Marshal(browserCommand{Type: browserRelease})
	h.conn.push(MessageText, b)
	recvCmd(t, h.ctrl.cmds, cmdRelease)
	if !h.events.has(EventRelease) {
		t.Fatal("expected release event")
	}
	// The controller relinquishes and emits terminal.closed in response.
	_ = h.ctrl.emitClosed("released")
	h.waitDone(t)
	closed, code, _ := h.conn.closeInfo()
	if !closed || code != closeNormal {
		t.Fatalf("close code = %d, want normal", code)
	}
}

func TestPingAnsweredWithPong(t *testing.T) {
	t.Parallel()
	h := start(t, Options{})
	b, _ := json.Marshal(browserCommand{Type: browserPing})
	h.conn.push(MessageText, b)
	waitFor(t, func() bool {
		for _, m := range h.conn.texts() {
			if m.Type == msgPong {
				return true
			}
		}
		return false
	}, "pong text")
	h.ctrl.stop(nil)
	h.waitDone(t)
}

func TestControllerConflictSurfaced(t *testing.T) {
	t.Parallel()
	h := start(t, Options{})
	_ = h.ctrl.emitClosed("pane in use by another controller")
	h.waitDone(t)

	var sawConflict bool
	for _, m := range h.conn.texts() {
		if m.Type == msgConflict {
			sawConflict = true
		}
	}
	if !sawConflict {
		t.Fatalf("expected conflict message, texts=%v", h.conn.texts())
	}
	_, code, _ := h.conn.closeInfo()
	if code != closePolicy {
		t.Fatalf("close code = %d, want policy", code)
	}
	if !h.events.has(EventConflict) {
		t.Fatal("expected conflict event")
	}
}

func TestControllerExitEndsSession(t *testing.T) {
	t.Parallel()
	h := start(t, Options{})
	h.ctrl.stop(errors.New("boom")) // simulate process exit
	h.waitDone(t)
	closed, _, _ := h.conn.closeInfo()
	if !closed {
		t.Fatal("expected conn closed after controller exit")
	}
}

func TestTakeoverFlagPropagated(t *testing.T) {
	t.Parallel()
	h := start(t, Options{Spec: Spec{PaneID: "pane-x", Cols: 80, Rows: 24, Takeover: true}})
	waitFor(t, func() bool { return h.launcher.lastSpec().PaneID == "pane-x" }, "launch")
	if !h.launcher.lastSpec().Takeover {
		t.Fatal("takeover flag not propagated to launcher spec")
	}
	h.ctrl.stop(nil)
	h.waitDone(t)
}

func TestInputTooLargeClosesPolicy(t *testing.T) {
	t.Parallel()
	h := start(t, Options{MaxInputBytes: 8})
	h.conn.push(MessageBinary, []byte("this input is far too large"))
	h.waitDone(t)
	_, code, _ := h.conn.closeInfo()
	if code != closePolicy {
		t.Fatalf("close code = %d, want policy", code)
	}
}

func TestBackpressureDisconnectsSlowClient(t *testing.T) {
	t.Parallel()
	conn := newFakeConn()
	conn.gate = make(chan struct{}) // never released -> writes block forever
	ctrl := newFakeController()
	launcher := &fakeLauncher{ctrl: ctrl}
	sink := &eventSink{}
	done := make(chan error, 1)
	opts := Options{
		Spec:         Spec{PaneID: "p", Cols: 80, Rows: 24},
		OutQueue:     1,
		WriteTimeout: 40 * time.Millisecond,
		Audit:        sink.add,
	}
	go func() { done <- Run(context.Background(), conn, launcher, opts) }()

	// Flood frames faster than the (blocked) writer can drain.
	go func() {
		for i := uint64(1); i <= 50; i++ {
			if err := ctrl.emitFrame(i, false, 0, 0, []byte("payload-data-chunk")); err != nil {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("bridge did not disconnect slow client")
	}
	if !sink.has(EventBackpressure) {
		t.Fatalf("expected backpressure event, got %v", sink.kinds())
	}
}

func TestFragmentedEscapeAcrossFrames(t *testing.T) {
	t.Parallel()
	h := start(t, Options{NewFilter: func() Filter { return &joiningFilter{} }})
	// Frame 1 ends mid-escape (bare ESC); filter must hold it back.
	_ = h.ctrl.emitFrame(1, false, 0, 0, []byte("abc\x1b"))
	waitFor(t, func() bool { return len(h.conn.binaries()) == 1 }, "first partial frame")
	if got := string(h.conn.binaries()[0]); got != "abc" {
		t.Fatalf("partial frame = %q, want abc", got)
	}
	// Frame 2 completes the escape; combined output must include it.
	_ = h.ctrl.emitFrame(2, false, 0, 0, []byte("[0m"))
	waitFor(t, func() bool { return len(h.conn.binaries()) == 2 }, "completed frame")
	if got := string(h.conn.binaries()[1]); got != "\x1b[0m" {
		t.Fatalf("completed frame = %q, want ESC[0m", got)
	}
	_ = h.ctrl.emitClosed("done")
	h.waitDone(t)
}

func TestFilterFactoryFreshPerController(t *testing.T) {
	t.Parallel()
	var created int32
	factory := func() Filter {
		atomic.AddInt32(&created, 1)
		return &joiningFilter{}
	}

	// Session 1 ends mid-escape, leaving a buffered ESC in its filter instance.
	h1 := start(t, Options{NewFilter: factory})
	_ = h1.ctrl.emitFrame(1, false, 0, 0, []byte("abc\x1b"))
	waitFor(t, func() bool { return len(h1.conn.binaries()) == 1 }, "session1 partial frame")
	if got := string(h1.conn.binaries()[0]); got != "abc" {
		t.Fatalf("session1 frame = %q, want abc", got)
	}
	_ = h1.ctrl.emitClosed("done")
	h1.waitDone(t)

	// Session 2 (a reconnect) gets a fresh filter: the buffered ESC from session
	// 1 must not bleed in, so its first frame is exactly "[0m", not "\x1b[0m".
	h2 := start(t, Options{NewFilter: factory})
	_ = h2.ctrl.emitFrame(1, false, 0, 0, []byte("[0m"))
	waitFor(t, func() bool { return len(h2.conn.binaries()) == 1 }, "session2 frame")
	if got := string(h2.conn.binaries()[0]); got != "[0m" {
		t.Fatalf("session2 frame = %q, want [0m (no bled ESC)", got)
	}
	_ = h2.ctrl.emitClosed("done")
	h2.waitDone(t)

	if n := atomic.LoadInt32(&created); n != 2 {
		t.Fatalf("filter factory called %d times, want one per controller (2)", n)
	}
}

// L3: the first frame may legitimately carry seq 0; it must be accepted, while a
// subsequent non-increasing sequence is still rejected.
func TestFirstFrameSeqZeroAccepted(t *testing.T) {
	t.Parallel()
	h := start(t, Options{})
	if err := h.ctrl.emitFrame(0, false, 0, 0, []byte("hi")); err != nil {
		t.Fatalf("emit: %v", err)
	}
	waitFor(t, func() bool { return len(h.conn.binaries()) == 1 }, "seq-0 frame forwarded")
	if got := string(h.conn.binaries()[0]); got != "hi" {
		t.Fatalf("frame = %q, want hi", got)
	}
	// A repeated seq 0 is now non-monotonic and must close the session.
	_ = h.ctrl.emitFrame(0, false, 0, 0, []byte("again"))
	h.waitDone(t)
	_, code, _ := h.conn.closeInfo()
	if code != closePolicy {
		t.Fatalf("close code = %d, want policy for non-monotonic repeat", code)
	}
}

// M11: a blocking keepalive ping must not stall the sole WebSocket writer;
// frames keep flowing while a ping is parked waiting for a pong.
func TestKeepalivePingDoesNotBlockWriter(t *testing.T) {
	t.Parallel()
	conn := newFakeConn()
	conn.pingGate = make(chan struct{}) // never released -> ping blocks on RTT
	ctrl := newFakeController()
	launcher := &fakeLauncher{ctrl: ctrl}
	done := make(chan error, 1)
	opts := Options{
		Spec:         Spec{PaneID: "pane-1", Cols: 80, Rows: 24},
		PingInterval: 5 * time.Millisecond,
	}
	go func() { done <- Run(context.Background(), conn, launcher, opts) }()

	// Wait until a keepalive ping is in flight (blocked on pingGate).
	waitFor(t, func() bool { return conn.pingCount() >= 1 }, "a ping to be in flight")

	// Despite the blocked ping, output frames must still reach the client.
	_ = ctrl.emitFrame(1, false, 0, 0, []byte("live"))
	deadline := time.After(2 * time.Second)
	for {
		ok := false
		for _, b := range conn.binaries() {
			if string(b) == "live" {
				ok = true
			}
		}
		if ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("frame not delivered while keepalive ping was blocked (writer stalled)")
		case <-time.After(2 * time.Millisecond):
		}
	}

	// Teardown: closing the conn unblocks the parked ping so cleanup does not hang.
	_ = ctrl.emitClosed("done")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("session did not tear down; keepalive ping blocked cleanup")
	}
}

func TestLaunchFailureReportsError(t *testing.T) {
	t.Parallel()
	conn := newFakeConn()
	launcher := &fakeLauncher{err: errors.New("no such pane")}
	err := Run(context.Background(), conn, launcher, Options{Spec: Spec{PaneID: "ghost", Cols: 80, Rows: 24}})
	if err == nil {
		t.Fatal("expected launch error")
	}
	closed, code, _ := conn.closeInfo()
	if !closed || code != closeInternalError {
		t.Fatalf("close code = %d, want internal error", code)
	}
}

func TestBadResizeDimsRejected(t *testing.T) {
	t.Parallel()
	h := start(t, Options{})
	b, _ := json.Marshal(browserCommand{Type: browserResize, Cols: 0, Rows: 40})
	h.conn.push(MessageText, b)
	waitFor(t, func() bool { return h.events.has(EventProtocolErr) }, "protocol error for bad dims")
	// Session stays open on a bad resize.
	if closed, _, _ := h.conn.closeInfo(); closed {
		t.Fatal("bad resize should not close the session")
	}
	h.ctrl.stop(nil)
	h.waitDone(t)
}

func TestOpenedMessageSent(t *testing.T) {
	t.Parallel()
	h := start(t, Options{})
	waitFor(t, func() bool {
		for _, m := range h.conn.texts() {
			if m.Type == msgOpened {
				return true
			}
		}
		return false
	}, "opened message")
	h.ctrl.stop(nil)
	h.waitDone(t)
}

func TestExecLauncherArgvAndCleanup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	argsFile := dir + "/args.txt"
	script := dir + "/fake-herdr.sh"
	body := fmt.Sprintf(`#!/bin/sh
echo "$@" > %q
printf '{"type":"terminal.frame","seq":1,"encoding":"ansi","width":80,"height":24,"full":true,"bytes":"aGk="}\n'
printf '{"type":"terminal.closed","reason":"done"}\n'
`, argsFile)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}

	launcher := ExecLauncher{BinPath: script, SocketPath: "/tmp/does-not-matter.sock", WaitDelay: time.Second}
	conn := newFakeConn()
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), conn, launcher,
			Options{Spec: Spec{PaneID: "pane-42", Cols: 100, Rows: 30, Takeover: true}})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("exec bridge did not finish")
	}

	argsBytes, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	args := string(argsBytes)
	for _, want := range []string{"terminal session control pane-42", "--cols 100", "--rows 30", "--takeover"} {
		if !strings.Contains(args, want) {
			t.Fatalf("argv %q missing %q", strings.TrimSpace(args), want)
		}
	}
	// The decoded "hi" frame should have reached the browser as binary.
	waitFor(t, func() bool {
		for _, b := range conn.binaries() {
			if string(b) == "hi" {
				return true
			}
		}
		return false
	}, "decoded frame bytes")
}
