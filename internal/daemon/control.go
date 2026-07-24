package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// controlFileName is the fixed name of the control socket within the state
// directory.
const controlFileName = "control.sock"

// controlReadLimit bounds a single request line so a misbehaving client cannot
// exhaust memory.
const controlReadLimit = 64 * 1024

// controlDeadline bounds how long a single request/response exchange may take.
const controlDeadline = 10 * time.Second

// ControlPath returns the control socket path for a given state directory.
func ControlPath(stateDir string) string {
	return filepath.Join(stateDir, controlFileName)
}

// Command names accepted by the control socket.
const (
	CmdStatus        = "status"
	CmdRotatePairing = "rotate-pairing"
	CmdStop          = "stop"
)

// controlRequest is the wire request over the control socket (one JSON object
// per line).
type controlRequest struct {
	Command   string `json:"command"`
	RequestID string `json:"request_id,omitempty"`
}

// controlResponse is the wire response.
type controlResponse struct {
	RequestID string         `json:"request_id,omitempty"`
	OK        bool           `json:"ok"`
	Error     string         `json:"error,omitempty"`
	Status    *StatusResult  `json:"status,omitempty"`
	Pairing   *PairingResult `json:"pairing,omitempty"`
}

// StatusResult is the non-secret status snapshot returned over the control
// socket. It mirrors runtime metadata plus per-component readiness.
type StatusResult struct {
	Health      Health            `json:"health"`
	Mode        string            `json:"mode"`
	PublicURL   string            `json:"public_url"`
	LocalAddr   string            `json:"local_addr"`
	Version     string            `json:"version"`
	InstanceID  string            `json:"instance_id"`
	PID         int               `json:"pid"`
	StartUnixMs int64             `json:"start_unix_ms"`
	ClientCount int               `json:"client_count"`
	Components  []ComponentStatus `json:"components"`
}

// ComponentStatus reports the readiness of one supervised subsystem (HTTP,
// Herdr, tunnel, state engine).
type ComponentStatus struct {
	Name   string `json:"name"`
	Ready  bool   `json:"ready"`
	Detail string `json:"detail,omitempty"`
}

// PairingResult carries a freshly rotated pairing artifact. The URL embeds the
// single-use secret in its fragment; it is returned only over the local 0600
// control socket and must never be persisted or logged.
type PairingResult struct {
	URL string `json:"url"`
}

// Handlers are the callbacks the control server invokes. Any may be nil, in
// which case the corresponding command returns an "unsupported" error.
type Handlers struct {
	Status        func(ctx context.Context) (StatusResult, error)
	RotatePairing func(ctx context.Context) (PairingResult, error)
	Stop          func(ctx context.Context) error
}

// ControlServer serves the private control socket.
type ControlServer struct {
	path     string
	listener net.Listener
	handlers Handlers

	wg       sync.WaitGroup
	mu       sync.Mutex
	closed   bool
	closedCh chan struct{}
	conns    map[net.Conn]struct{}
	// exempt is the connection currently handling a stop request; Close leaves it
	// open so its response can flush before the socket goes away.
	exempt net.Conn
}

// Listen creates the control socket at path with mode 0600 (parent dir 0700),
// removing a stale socket file first. Call Serve to begin accepting.
func Listen(path string, h Handlers) (*ControlServer, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("daemon: create control dir: %w", err)
	}
	_ = os.Chmod(dir, 0o700)

	// Remove a stale socket; a live one would be caught by reconciliation before
	// we get here.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("daemon: remove stale socket: %w", err)
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("daemon: listen control socket: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("daemon: chmod control socket: %w", err)
	}

	return &ControlServer{
		path:     path,
		listener: ln,
		handlers: h,
		closedCh: make(chan struct{}),
		conns:    make(map[net.Conn]struct{}),
	}, nil
}

// Serve accepts connections until Close is called. It blocks.
//
// A transient Accept error (for example EMFILE under fd pressure, or EINTR) must
// not permanently disable the control socket — that would strand status/stop.
// Such errors are retried after a bounded backoff; the loop returns only when the
// listener is closed.
func (s *ControlServer) Serve() {
	acceptFail := 0
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.isClosed() || errors.Is(err, net.ErrClosed) {
				return
			}
			acceptFail++
			timer := time.NewTimer(acceptBackoff(acceptFail))
			select {
			case <-s.closedCh:
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		acceptFail = 0
		s.trackConn(conn, true)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.trackConn(conn, false)
			defer conn.Close()
			s.handleConn(conn)
		}()
	}
}

// acceptBackoff returns a bounded retry delay for a transient Accept failure.
func acceptBackoff(fail int) time.Duration {
	d := 5 * time.Millisecond << (fail - 1)
	if d > time.Second || d <= 0 {
		d = time.Second
	}
	return d
}

func (s *ControlServer) handleConn(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(controlDeadline))
	reader := bufio.NewReader(io.LimitReader(conn, controlReadLimit))
	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}

	var req controlRequest
	resp := controlResponse{}
	if err := json.Unmarshal(trimNewline(line), &req); err != nil {
		resp.OK = false
		resp.Error = "invalid request"
		s.writeResponse(conn, resp)
		return
	}
	resp.RequestID = req.RequestID

	ctx, cancel := context.WithTimeout(context.Background(), controlDeadline)
	defer cancel()

	switch req.Command {
	case CmdStatus:
		if s.handlers.Status == nil {
			resp.OK = false
			resp.Error = "status unsupported"
			break
		}
		st, err := s.handlers.Status(ctx)
		if err != nil {
			resp.OK = false
			resp.Error = sanitizeErr(err)
			break
		}
		resp.OK = true
		resp.Status = &st
	case CmdRotatePairing:
		if s.handlers.RotatePairing == nil {
			resp.OK = false
			resp.Error = "rotate-pairing unsupported"
			break
		}
		pr, err := s.handlers.RotatePairing(ctx)
		if err != nil {
			resp.OK = false
			resp.Error = sanitizeErr(err)
			break
		}
		resp.OK = true
		resp.Pairing = &pr
	case CmdStop:
		if s.handlers.Stop == nil {
			resp.OK = false
			resp.Error = "stop unsupported"
			break
		}
		// Stop typically triggers an asynchronous shutdown that closes this
		// server. Mark this connection exempt so Close does not force it shut
		// before the "ok" reply below is flushed.
		s.setExempt(conn)
		if err := s.handlers.Stop(ctx); err != nil {
			resp.OK = false
			resp.Error = sanitizeErr(err)
			break
		}
		resp.OK = true
	default:
		resp.OK = false
		resp.Error = fmt.Sprintf("unknown command %q", req.Command)
	}

	s.writeResponse(conn, resp)
}

func (s *ControlServer) writeResponse(conn net.Conn, resp controlResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_ = conn.SetWriteDeadline(time.Now().Add(controlDeadline))
	_, _ = conn.Write(data)
}

// Close stops accepting new connections, unblocks in-flight handlers, waits for
// them to finish, and removes the socket file.
//
// The connection currently handling a stop request (if any) is left open so its
// response can flush; every other connection is force-closed so an idle client
// cannot delay shutdown up to the per-request deadline. Handlers are
// deadline-bounded, so wg.Wait is bounded.
func (s *ControlServer) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.closedCh)
	exempt := s.exempt
	others := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		if c != exempt {
			others = append(others, c)
		}
	}
	s.mu.Unlock()

	err := s.listener.Close()
	for _, c := range others {
		_ = c.Close()
	}
	s.wg.Wait()
	if rmErr := os.Remove(s.path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) && err == nil {
		err = rmErr
	}
	return err
}

func (s *ControlServer) setExempt(conn net.Conn) {
	s.mu.Lock()
	s.exempt = conn
	s.mu.Unlock()
}

func (s *ControlServer) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *ControlServer) trackConn(conn net.Conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		s.conns[conn] = struct{}{}
	} else {
		delete(s.conns, conn)
	}
}

// Client is a control-socket client used by other daemon entrypoints (status,
// stop, setup-link).
type Client struct {
	path    string
	timeout time.Duration
}

// NewClient returns a control-socket client for the socket at path.
func NewClient(path string) *Client {
	return &Client{path: path, timeout: controlDeadline}
}

// NewClientForStateDir returns a control-socket client that targets the same
// socket the daemon serves for stateDir, resolving any relocation applied for
// path-length safety. Other daemon entrypoints (status, stop, setup-link) use
// this so they always agree with the running daemon on the socket location.
func NewClientForStateDir(stateDir string) (*Client, error) {
	path, err := SocketPath(stateDir)
	if err != nil {
		return nil, err
	}
	return NewClient(path), nil
}

// WithTimeout overrides the per-call timeout.
func (c *Client) WithTimeout(d time.Duration) *Client {
	c.timeout = d
	return c
}

// Status requests the daemon status.
func (c *Client) Status(ctx context.Context) (StatusResult, error) {
	resp, err := c.call(ctx, controlRequest{Command: CmdStatus})
	if err != nil {
		return StatusResult{}, err
	}
	if resp.Status == nil {
		return StatusResult{}, errors.New("daemon: status response missing payload")
	}
	return *resp.Status, nil
}

// RotatePairing requests a fresh pairing secret and returns its URL.
func (c *Client) RotatePairing(ctx context.Context) (PairingResult, error) {
	resp, err := c.call(ctx, controlRequest{Command: CmdRotatePairing})
	if err != nil {
		return PairingResult{}, err
	}
	if resp.Pairing == nil {
		return PairingResult{}, errors.New("daemon: pairing response missing payload")
	}
	return *resp.Pairing, nil
}

// Stop requests a graceful shutdown.
func (c *Client) Stop(ctx context.Context) error {
	_, err := c.call(ctx, controlRequest{Command: CmdStop})
	return err
}

// Ping verifies the socket is reachable and answers status; it is used by
// reconciliation to distinguish a live daemon from a stale socket.
func (c *Client) Ping(ctx context.Context) (StatusResult, error) {
	return c.Status(ctx)
}

func (c *Client) call(ctx context.Context, req controlRequest) (controlResponse, error) {
	var d net.Dialer
	dialCtx := ctx
	if c.timeout > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	conn, err := d.DialContext(dialCtx, "unix", c.path)
	if err != nil {
		return controlResponse{}, fmt.Errorf("daemon: dial control socket: %w", err)
	}
	defer conn.Close()

	if deadline, ok := dialCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(controlDeadline))
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return controlResponse{}, err
	}
	payload = append(payload, '\n')
	if _, err := conn.Write(payload); err != nil {
		return controlResponse{}, fmt.Errorf("daemon: write control request: %w", err)
	}

	reader := bufio.NewReader(io.LimitReader(conn, controlReadLimit))
	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return controlResponse{}, fmt.Errorf("daemon: read control response: %w", err)
	}
	var resp controlResponse
	if err := json.Unmarshal(trimNewline(line), &resp); err != nil {
		return controlResponse{}, fmt.Errorf("daemon: decode control response: %w", err)
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "control command failed"
		}
		return resp, fmt.Errorf("daemon: %s", resp.Error)
	}
	return resp, nil
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// sanitizeErr produces a bounded, control-character-free error string safe to
// send over the wire and into logs.
func sanitizeErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	const max = 500
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		out = append(out, r)
		if len(out) >= max {
			break
		}
	}
	return string(out)
}
