package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"time"
)

// Dialer opens a fresh connection to the Herdr socket. Production uses
// [NewUnixDialer]; tests inject an in-memory dialer.
type Dialer interface {
	Dial(ctx context.Context) (net.Conn, error)
}

// DialerFunc adapts a function to the [Dialer] interface.
type DialerFunc func(ctx context.Context) (net.Conn, error)

// Dial implements [Dialer].
func (f DialerFunc) Dial(ctx context.Context) (net.Conn, error) { return f(ctx) }

// NewUnixDialer returns a [Dialer] that connects to a Unix domain socket.
func NewUnixDialer(path string) Dialer {
	return DialerFunc(func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	})
}

// DefaultTimeout is the per-request deadline when none is configured.
const DefaultTimeout = 5 * time.Second

// DefaultMaxResponseBytes bounds a single NDJSON response frame.
const DefaultMaxResponseBytes = 8 << 20 // 8 MiB

// Client is the typed Herdr socket client. It is safe for concurrent use; each
// call opens and closes its own connection.
type Client struct {
	dialer   Dialer
	clock    Clock
	timeout  time.Duration
	maxBytes int
	idPrefix string
	seq      atomic.Uint64

	// bin and runner back CLI-mediated discovery (agent-kind enumeration),
	// which the socket protocol does not expose. They are unused by socket
	// requests.
	bin    string
	runner Runner
}

// Option configures a [Client].
type Option func(*Client)

// WithClock injects a [Clock] (default [SystemClock]).
func WithClock(c Clock) Option { return func(cl *Client) { cl.clock = c } }

// WithTimeout sets the per-request deadline (default [DefaultTimeout]). A value
// <= 0 disables the client-side deadline (the context still applies).
func WithTimeout(d time.Duration) Option { return func(cl *Client) { cl.timeout = d } }

// WithMaxResponseBytes bounds a single response frame (default
// [DefaultMaxResponseBytes]).
func WithMaxResponseBytes(n int) Option {
	return func(cl *Client) {
		if n > 0 {
			cl.maxBytes = n
		}
	}
}

// WithIDPrefix sets the request-id prefix (default "phone"). Ids are always
// strings, as the protocol requires.
func WithIDPrefix(s string) Option { return func(cl *Client) { cl.idPrefix = s } }

// WithBin sets the Herdr CLI binary used for CLI-mediated discovery. An empty
// value resolves lazily via [ResolveBinaryPath].
func WithBin(path string) Option { return func(cl *Client) { cl.bin = path } }

// WithRunner injects the argv [Runner] used for CLI-mediated discovery
// (default [ExecRunner], which runs a bounded, shell-free subprocess).
func WithRunner(r Runner) Option {
	return func(cl *Client) {
		if r != nil {
			cl.runner = r
		}
	}
}

// NewClient builds a [Client] over the given dialer.
func NewClient(dialer Dialer, opts ...Option) *Client {
	c := &Client{
		dialer:   dialer,
		clock:    SystemClock,
		timeout:  DefaultTimeout,
		maxBytes: DefaultMaxResponseBytes,
		idPrefix: "phone",
		runner:   ExecRunner{MaxBytes: DefaultMaxCommandBytes},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) nextID() string {
	return c.idPrefix + "-" + strconv.FormatUint(c.seq.Add(1), 10)
}

// call performs one request/response round trip on a dedicated connection. It
// validates the response id and result discriminator, and decodes the result
// into out (which may be nil to discard it). Structured Herdr errors and
// transport failures are returned as *Error.
func (c *Client) call(ctx context.Context, method string, params any, wantType string, out any) error {
	id := c.nextID()
	reqBytes, err := json.Marshal(request{ID: id, Method: method, Params: params})
	if err != nil {
		return newError(CodeProtocol, "cannot encode request: "+err.Error())
	}
	reqBytes = append(reqBytes, '\n')

	conn, err := c.dialer.Dial(ctx)
	if err != nil {
		return newError(CodeConnect, err.Error())
	}
	// A single close is enough; guard against double close on the deadline path.
	closed := false
	closeConn := func() {
		if !closed {
			closed = true
			_ = conn.Close()
		}
	}
	defer closeConn()

	type roundTrip struct {
		frame []byte
		err   error
	}
	done := make(chan roundTrip, 1)
	go func() {
		if _, werr := conn.Write(reqBytes); werr != nil {
			done <- roundTrip{err: werr}
			return
		}
		frame, rerr := readFrame(bufio.NewReader(conn), c.maxBytes)
		done <- roundTrip{frame: frame, err: rerr}
	}()

	var timeout <-chan time.Time
	if c.timeout > 0 {
		timeout = c.clock.After(c.timeout)
	}

	select {
	case <-ctx.Done():
		closeConn() // unblock the goroutine's read
		<-done      // reap it; the channel is buffered so this never leaks
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return newError(CodeTimeout, "context deadline exceeded")
		}
		return newError(CodeCanceled, "request canceled")
	case <-timeout:
		closeConn()
		<-done
		return newError(CodeTimeout, "no response within "+c.timeout.String())
	case rt := <-done:
		if rt.err != nil {
			return classifyIOError(rt.err)
		}
		return decodeResponse(rt.frame, id, wantType, out)
	}
}

func classifyIOError(err error) *Error {
	switch {
	case errors.Is(err, errFrameTooLarge):
		return newError(CodeFrameTooLarge, err.Error())
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return newError(CodeTransport, "connection closed before a complete response")
	default:
		return newError(CodeTransport, err.Error())
	}
}

func decodeResponse(frame []byte, wantID, wantType string, out any) error {
	var env envelope
	if err := json.Unmarshal(frame, &env); err != nil {
		return newError(CodeProtocol, "malformed response frame: "+err.Error())
	}
	if env.Error != nil {
		// Preserve the structured server error with a bounded, control-free
		// message. A server error need not echo the request id.
		return newError(env.Error.Code, env.Error.Message)
	}
	// Herdr echoes the request id on success. An empty id marks a frame the
	// server could not associate (for example an unparsable request); anything
	// else must match.
	if env.ID != "" && env.ID != wantID {
		return newError(CodeProtocol, "response id mismatch: got "+env.ID+" want "+wantID)
	}
	if len(env.Result) == 0 {
		return newError(CodeProtocol, "response has neither result nor error")
	}
	if wantType != "" {
		var rt resultType
		if err := json.Unmarshal(env.Result, &rt); err != nil {
			return newError(CodeProtocol, "result missing type discriminator")
		}
		if rt.Type != wantType {
			return newError(CodeUnexpectedTyp, "expected result type "+wantType+" got "+rt.Type)
		}
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return newError(CodeProtocol, "cannot decode result "+wantType+": "+err.Error())
		}
	}
	return nil
}

var errFrameTooLarge = errors.New("response frame exceeds byte bound")

// readFrame reads one newline-delimited frame, tolerating fragmented reads
// (including reads that split a multi-byte UTF-8 rune across chunks, since
// framing is byte-oriented and decoding happens only on the whole frame). It
// fails if the frame would exceed max bytes, so a hostile or wedged peer cannot
// exhaust memory.
func readFrame(r *bufio.Reader, max int) ([]byte, error) {
	buf := make([]byte, 0, 512)
	for {
		b, err := r.ReadByte()
		if err != nil {
			if len(buf) > 0 && errors.Is(err, io.EOF) {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if b == '\n' {
			return buf, nil
		}
		if len(buf) >= max {
			return nil, errFrameTooLarge
		}
		buf = append(buf, b)
	}
}
