// Package terminal bridges a browser WebSocket to a single `herdr terminal
// session control` subprocess.
//
// It implements section 13 of SPEC.md: it parses the controller's NDJSON output,
// validates monotonically increasing sequence numbers, base64-decodes frame
// bytes, runs them through an injected security Filter, and forwards them to the
// browser as binary WebSocket frames while metadata and lifecycle records travel
// as text JSON. Browser binary messages are terminal input bytes; browser text
// messages are typed resize, scroll, release, and ping commands. Exactly one
// goroutine owns the subprocess stdin and exactly one owns the WebSocket writer.
// Frame size, input size, resize rate, and pending writes are all bounded, and a
// slow client is disconnected rather than allowed to stall the controller.
package terminal

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Defaults for the tunables in Options. They are conservative but real; callers
// override them from config.
const (
	defaultMaxOutputFrameBytes = 1 << 20 // 1 MiB decoded frame
	defaultMaxInputBytes       = 1 << 16 // 64 KiB per browser input message
	defaultOutQueue            = 256
	defaultPingInterval        = 20 * time.Second
	defaultPongTimeout         = 75 * time.Second
	defaultWriteTimeout        = 10 * time.Second
	defaultResizeMinInterval   = 50 * time.Millisecond
	defaultMaxLineBytes        = 4 << 20 // NDJSON scanner line cap
	maxScrollLines             = 10000
)

// Event is a structural, content-free audit record emitted by the bridge. It
// never carries terminal content, only categories and counts (section 17).
type Event struct {
	Kind   string
	Bytes  int
	Cols   int
	Rows   int
	Lines  int
	Seq    uint64
	Reason string
}

// Event kinds.
const (
	EventInput        = "input"
	EventResize       = "resize"
	EventScroll       = "scroll"
	EventRelease      = "release"
	EventClosed       = "closed"
	EventConflict     = "conflict"
	EventBackpressure = "backpressure"
	EventProtocolErr  = "protocol_error"
)

// Options configures a single bridge run.
type Options struct {
	Spec Spec
	// NewFilter constructs the output filter for this controller. It is called
	// exactly once per Run, giving every controller (and therefore every
	// reconnect) a fresh, independent filter. That guarantees a fragmented escape
	// sequence buffered by one session's filter can never bleed into another.
	// Nil defaults to a pass-through filter.
	NewFilter           func() Filter
	MaxOutputFrameBytes int
	MaxInputBytes       int
	OutQueue            int
	PingInterval        time.Duration
	PongTimeout         time.Duration
	WriteTimeout        time.Duration
	ResizeMinInterval   time.Duration
	// Audit receives structural events. It must not block; it is called from
	// bridge goroutines. Nil disables auditing.
	Audit func(Event)
}

func (o *Options) applyDefaults() {
	if o.NewFilter == nil {
		o.NewFilter = func() Filter { return NopFilter{} }
	}
	if o.MaxOutputFrameBytes <= 0 {
		o.MaxOutputFrameBytes = defaultMaxOutputFrameBytes
	}
	if o.MaxInputBytes <= 0 {
		o.MaxInputBytes = defaultMaxInputBytes
	}
	if o.OutQueue <= 0 {
		o.OutQueue = defaultOutQueue
	}
	if o.PingInterval <= 0 {
		o.PingInterval = defaultPingInterval
	}
	if o.PongTimeout <= 0 {
		o.PongTimeout = defaultPongTimeout
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = defaultWriteTimeout
	}
	if o.ResizeMinInterval <= 0 {
		o.ResizeMinInterval = defaultResizeMinInterval
	}
}

type outMsg struct {
	binary bool
	data   []byte
}

type bridge struct {
	conn Conn
	ctrl Controller
	opts Options

	ctx    context.Context
	cancel context.CancelFunc

	filter Filter // fresh per Run, owned by readController

	outCh    chan outMsg
	stdinCh  chan controllerCommand
	resizeCh chan browserCommand

	lastSeq uint64 // touched only by readController
	haveSeq bool   // false until the first frame; distinguishes a valid seq 0

	mu          sync.Mutex
	closeCode   int
	closeReason string
	closeMsg    []byte // final text message sent synchronously on shutdown
	closeSet    bool

	writerDone chan struct{}
	closeOnce  sync.Once
}

// Run launches the controller for opts.Spec, bridges it to conn, and blocks
// until the session ends (controller exit, browser close, error, or ctx
// cancellation). It always terminates the controller and closes conn before
// returning. The returned error is nil for an orderly close.
func Run(ctx context.Context, conn Conn, launcher Launcher, opts Options) error {
	opts.applyDefaults()

	runCtx, cancel := context.WithCancel(ctx)
	ctrl, err := launcher.Launch(runCtx, opts.Spec)
	if err != nil {
		cancel()
		reason := "controller launch failed"
		_ = conn.SetWriteDeadline(time.Now().Add(opts.WriteTimeout))
		_ = conn.WriteText(serverMessage{Type: msgClosed, Reason: reason}.encode())
		_ = conn.Close(closeInternalError, reason)
		return fmt.Errorf("terminal: launch: %w", err)
	}

	b := &bridge{
		conn:       conn,
		ctrl:       ctrl,
		opts:       opts,
		filter:     opts.NewFilter(),
		ctx:        runCtx,
		cancel:     cancel,
		outCh:      make(chan outMsg, opts.OutQueue),
		stdinCh:    make(chan controllerCommand, 64),
		resizeCh:   make(chan browserCommand, 1),
		closeCode:  closeNormal,
		writerDone: make(chan struct{}),
	}
	return b.run()
}

func (b *bridge) run() error {
	defer b.cancel()

	// Signal the client the controller is live before pumping I/O. outCh is
	// buffered, so this never blocks.
	b.sendText(serverMessage{Type: msgOpened})

	var wg sync.WaitGroup
	launch := func(fn func()) {
		wg.Add(1)
		go func() { defer wg.Done(); defer b.cancel(); fn() }()
	}
	// The writer is not in wg: teardown must wait for it to drain queued frames
	// before the socket is closed, but the browser reader (in wg) cannot unblock
	// until the socket is closed. Sequencing them separately avoids that
	// deadlock while still flushing frames enqueued just before a close.
	go func() { defer close(b.writerDone); defer b.cancel(); b.writeLoop() }()

	launch(b.readController)
	launch(b.stdinLoop)
	launch(b.resizeLoop)
	launch(b.readBrowser)
	launch(b.pingLoop)

	// Any goroutine ending the session cancels ctx.
	<-b.ctx.Done()
	// 1. Stop the controller so its stdout EOFs and the scanner returns.
	_ = b.ctrl.Terminate()
	// 2. Give the writer a grace period to flush frames already queued at
	//    cancellation. Only if it is genuinely stuck on a slow client (still
	//    writing after the grace period) do we abort it with an immediate write
	//    deadline; aborting eagerly could truncate a frame mid-write.
	select {
	case <-b.writerDone:
	case <-time.After(b.opts.WriteTimeout):
		_ = b.conn.SetWriteDeadline(time.Now())
		<-b.writerDone
	}
	// 3. Close the socket, which unblocks the browser reader parked in
	//    ReadMessage.
	b.finalClose()
	// 3. Wait for the remaining readers, then reap the controller. Reaping only
	//    after readController has returned avoids racing the StdoutPipe close
	//    (see os/exec StdoutPipe docs).
	wg.Wait()
	_ = b.ctrl.Wait()
	return nil
}

// finalClose flushes the final close text (best effort) and closes the socket.
func (b *bridge) finalClose() {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		code, reason, msg := b.closeCode, b.closeReason, b.closeMsg
		b.mu.Unlock()
		if reason == "" {
			reason = "session ended"
		}
		if len(msg) > 0 {
			_ = b.conn.SetWriteDeadline(time.Now().Add(b.opts.WriteTimeout))
			_ = b.conn.WriteText(msg)
		}
		_ = b.conn.Close(code, reason)
	})
}

// setClose records the intended browser close code/reason and an optional final
// text message, first-writer-wins.
func (b *bridge) setClose(code int, reason string, final *serverMessage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closeSet {
		return
	}
	b.closeSet = true
	b.closeCode = code
	b.closeReason = reason
	if final != nil {
		b.closeMsg = final.encode()
	}
}

func (b *bridge) audit(ev Event) {
	if b.opts.Audit != nil {
		b.opts.Audit(ev)
	}
}

// readController parses NDJSON from the controller's stdout and forwards frames
// to the browser as binary messages, enforcing sequence monotonicity, frame
// size bounds, and the injected output filter.
func (b *bridge) readController() {
	sc := bufio.NewScanner(b.ctrl.Stdout())
	sc.Buffer(make([]byte, 0, 64*1024), defaultMaxLineBytes)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec controllerRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			b.audit(Event{Kind: EventProtocolErr, Reason: "bad controller record"})
			continue
		}
		switch rec.Type {
		case recordFrame:
			if !b.handleFrame(rec) {
				return
			}
		case recordClosed:
			b.handleControllerClosed(rec.Reason)
			return
		default:
			// Unknown record types are tolerated (forward-compatibility).
		}
	}
	b.setClose(closeGoingAway, "controller stream ended",
		&serverMessage{Type: msgClosed, Reason: "controller stream ended"})
}

// handleFrame validates and forwards a single terminal.frame record. It returns
// false if the session must end.
func (b *bridge) handleFrame(rec controllerRecord) bool {
	// The first frame is accepted at any sequence (including 0); only subsequent
	// frames must strictly increase. A zero-valued lastSeq is not "seq 0 seen".
	if b.haveSeq && rec.Seq <= b.lastSeq {
		b.audit(Event{Kind: EventProtocolErr, Seq: rec.Seq, Reason: "non-monotonic sequence"})
		b.setClose(closePolicy, "non-monotonic frame sequence",
			&serverMessage{Type: msgClosed, Reason: "non-monotonic frame sequence"})
		return false
	}
	b.haveSeq = true
	b.lastSeq = rec.Seq

	raw, err := rec.decodeBytes()
	if err != nil {
		b.audit(Event{Kind: EventProtocolErr, Seq: rec.Seq, Reason: "bad base64 frame"})
		return true // skip this frame, keep the session
	}
	if len(raw) > b.opts.MaxOutputFrameBytes {
		b.setClose(closePolicy, "frame exceeds output limit",
			&serverMessage{Type: msgClosed, Reason: "frame exceeds output limit"})
		return false
	}

	filtered := b.filter.FilterOutput(raw)

	// Emit dimension metadata as text on a full refresh or resize so the client
	// can fit xterm; content itself is always binary.
	if rec.Full || rec.Width > 0 || rec.Height > 0 {
		b.sendText(serverMessage{
			Type:   msgResized,
			Width:  rec.Width,
			Height: rec.Height,
			Full:   rec.Full,
			Seq:    rec.Seq,
		})
	}

	if len(filtered) > 0 {
		if !b.enqueue(outMsg{binary: true, data: filtered}) {
			return false
		}
	}
	return true
}

func (b *bridge) handleControllerClosed(reason string) {
	if isConflict(reason) {
		b.audit(Event{Kind: EventConflict, Reason: reason})
		b.setClose(closePolicy, "terminal controlled elsewhere",
			&serverMessage{Type: msgConflict, Reason: reason})
		return
	}
	b.audit(Event{Kind: EventClosed, Reason: reason})
	b.setClose(closeNormal, reason, &serverMessage{Type: msgClosed, Reason: reason})
}

// isConflict classifies a controller close reason as a takeover conflict.
func isConflict(reason string) bool {
	r := strings.ToLower(reason)
	for _, needle := range []string{"conflict", "in use", "another controller", "already controlled", "controlled elsewhere"} {
		if strings.Contains(r, needle) {
			return true
		}
	}
	return false
}

// writeLoop is the single WebSocket writer. It only drains outbound messages;
// keepalive pings run in pingLoop so a blocking ping (which waits for its pong
// RTT) can never stall frame delivery or teardown (M11). On ctx cancellation it
// makes a best-effort drain of any queued frames before returning.
func (b *bridge) writeLoop() {
	for {
		select {
		case <-b.ctx.Done():
			b.drainOut()
			return
		case m := <-b.outCh:
			if err := b.writeOut(m); err != nil {
				return
			}
		}
	}
}

// pingLoop sends periodic keepalive pings independently of the writer. A failed
// ping (no pong before the adapter's timeout, or a closed socket) ends the
// session via the launch wrapper's cancel. Because it is a dedicated goroutine,
// its blocking Ping never holds up outbound frames; teardown's finalClose closes
// the socket, which unblocks an in-flight Ping so cleanup cannot hang on RTT.
func (b *bridge) pingLoop() {
	ping := time.NewTicker(b.opts.PingInterval)
	defer ping.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ping.C:
			if err := b.conn.Ping(nil); err != nil {
				b.setClose(closeGoingAway, "keepalive failed", nil)
				return
			}
		}
	}
}

// drainOut flushes frames already queued at cancellation time so a client sees a
// controller's final output before the socket closes. The socket is still open
// here: finalClose runs only after writerDone.
func (b *bridge) drainOut() {
	for {
		select {
		case m := <-b.outCh:
			if err := b.writeOut(m); err != nil {
				return
			}
		default:
			return
		}
	}
}

func (b *bridge) writeOut(m outMsg) error {
	if err := b.conn.SetWriteDeadline(time.Now().Add(b.opts.WriteTimeout)); err != nil {
		return err
	}
	if m.binary {
		return b.conn.WriteBinary(m.data)
	}
	return b.conn.WriteText(m.data)
}

// sendText enqueues a metadata/lifecycle text message. It is best effort: a full
// queue drops the metadata rather than blocking, since terminal frames take
// priority and lifecycle is conveyed by the synchronous shutdown message.
func (b *bridge) sendText(m serverMessage) {
	select {
	case b.outCh <- outMsg{binary: false, data: m.encode()}:
	case <-b.ctx.Done():
	default:
	}
}

// enqueue applies backpressure: it waits briefly for queue space and, if the
// client cannot keep up, disconnects it (a reconnect gets a fresh full frame).
func (b *bridge) enqueue(m outMsg) bool {
	select {
	case b.outCh <- m:
		return true
	case <-b.ctx.Done():
		return false
	default:
	}
	timer := time.NewTimer(b.opts.WriteTimeout)
	defer timer.Stop()
	select {
	case b.outCh <- m:
		return true
	case <-timer.C:
		b.audit(Event{Kind: EventBackpressure, Bytes: len(m.data)})
		b.setClose(closeGoingAway, "client too slow", nil)
		return false
	case <-b.ctx.Done():
		return false
	}
}

// stdinLoop is the single writer of the controller's stdin.
func (b *bridge) stdinLoop() {
	enc := json.NewEncoder(b.ctrl.Stdin())
	for {
		select {
		case <-b.ctx.Done():
			return
		case cmd := <-b.stdinCh:
			if err := enc.Encode(cmd); err != nil {
				b.setClose(closeInternalError, "controller stdin closed", nil)
				return
			}
		}
	}
}

// resizeLoop coalesces resize commands and applies at most one per
// ResizeMinInterval, bounding the resize rate (section 13).
func (b *bridge) resizeLoop() {
	ticker := time.NewTicker(b.opts.ResizeMinInterval)
	defer ticker.Stop()
	var pending controllerCommand
	have := false
	for {
		select {
		case <-b.ctx.Done():
			return
		case rc := <-b.resizeCh:
			pending = resizeCommand(rc.Cols, rc.Rows, rc.CellWidthPx, rc.CellHeightPx)
			have = true
		case <-ticker.C:
			if have {
				b.sendStdin(pending)
				have = false
			}
		}
	}
}

func (b *bridge) sendStdin(cmd controllerCommand) {
	select {
	case b.stdinCh <- cmd:
	case <-b.ctx.Done():
	}
}

// readBrowser consumes browser messages until the session ends.
func (b *bridge) readBrowser() {
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}
		if err := b.conn.SetReadDeadline(time.Now().Add(b.opts.PongTimeout)); err != nil {
			return
		}
		mt, data, err := b.conn.ReadMessage()
		if err != nil {
			b.setClose(closeNormal, "client disconnected", nil)
			return
		}
		switch mt {
		case MessageBinary:
			if !b.handleInput(data) {
				return
			}
		case MessageText:
			if !b.handleCommand(data) {
				return
			}
		}
	}
}

// handleInput forwards raw browser bytes to the controller as a terminal.input
// command, bounding the input size.
func (b *bridge) handleInput(data []byte) bool {
	if len(data) > b.opts.MaxInputBytes {
		b.audit(Event{Kind: EventProtocolErr, Bytes: len(data), Reason: "input too large"})
		b.setClose(closePolicy, "input too large", nil)
		return false
	}
	if len(data) == 0 {
		return true
	}
	b.audit(Event{Kind: EventInput, Bytes: len(data)})
	b.sendStdin(inputBytesCommand(data))
	return true
}

// handleCommand parses and dispatches a typed browser text command.
func (b *bridge) handleCommand(data []byte) bool {
	cmd, err := parseBrowserCommand(data)
	if err != nil {
		b.audit(Event{Kind: EventProtocolErr, Reason: "bad command"})
		return true // ignore a single bad command
	}
	switch cmd.Type {
	case browserResize:
		if cmd.Cols <= 0 || cmd.Rows <= 0 || cmd.Cols > 10000 || cmd.Rows > 10000 {
			b.audit(Event{Kind: EventProtocolErr, Reason: "bad resize dims"})
			return true
		}
		b.audit(Event{Kind: EventResize, Cols: cmd.Cols, Rows: cmd.Rows})
		b.pushResize(cmd)
	case browserScroll:
		if cmd.Direction != "up" && cmd.Direction != "down" {
			return true
		}
		lines := cmd.Lines
		if lines <= 0 {
			lines = 1
		}
		if lines > maxScrollLines {
			lines = maxScrollLines
		}
		src := cmd.Source
		if src == "" {
			src = "wheel"
		}
		b.audit(Event{Kind: EventScroll, Lines: lines})
		b.sendStdin(scrollCommand(cmd.Direction, lines, src))
	case browserRelease:
		b.audit(Event{Kind: EventRelease})
		b.sendStdin(releaseCommand())
		// The controller relinquishes control and emits terminal.closed in
		// response, which drives teardown. Keep reading until then so the
		// release command is actually delivered before the session ends.
		return true
	case browserPing:
		b.sendText(serverMessage{Type: msgPong})
	default:
		// Unknown command type is ignored.
	}
	return true
}

// pushResize hands the newest resize to the coalescer without ever blocking the
// browser reader, replacing any stale pending resize.
func (b *bridge) pushResize(cmd browserCommand) {
	select {
	case b.resizeCh <- cmd:
		return
	default:
	}
	select {
	case <-b.resizeCh:
	default:
	}
	select {
	case b.resizeCh <- cmd:
	default:
	}
}
