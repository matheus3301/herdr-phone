package server

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matheus3301/herdr-phone/internal/terminal"
)

// ---- fake Authenticator ---------------------------------------------------

type fakeAuth struct {
	mu         sync.Mutex
	named      bool
	accessErr  error
	pairSecret string
	pairErr    error
	sessions   map[string]Identity
	csrf       map[string]string
	exp        time.Time
	seq        int
}

func newFakeAuth() *fakeAuth {
	return &fakeAuth{
		pairSecret: "correct-secret",
		sessions:   map[string]Identity{},
		csrf:       map[string]string{},
		exp:        time.Unix(1_800_000_000, 0),
	}
}

func (f *fakeAuth) NamedMode() bool { return f.named }

func (f *fakeAuth) VerifyAccess(*http.Request) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.named {
		return f.accessErr
	}
	return nil
}

func (f *fakeAuth) Pair(_ *http.Request, secret string) (*Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pairErr != nil {
		return nil, f.pairErr
	}
	if secret != f.pairSecret {
		return nil, ErrPairing
	}
	f.seq++
	f.pairSecret = "rotated" // single-use rotation
	cookieVal := "sess-cookie"
	csrf := "csrf-token"
	id := Identity{Subject: "op@example.com", Display: "operator", SessionID: "sid-1", Quick: !f.named}
	f.sessions[cookieVal] = id
	f.csrf[cookieVal] = csrf
	return &Session{
		Cookie:    &http.Cookie{Name: f.CookieName(), Value: cookieVal, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode},
		CSRFToken: csrf,
		Identity:  id,
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil
}

func (f *fakeAuth) Session(cookieValue string) (Identity, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.sessions[cookieValue]
	if !ok {
		return Identity{}, false
	}
	// Surface the per-session CSRF token and expiry, as a real authenticator does,
	// so GET /session can return them. The cookie value is never placed here.
	id.CSRFToken = f.csrf[cookieValue]
	id.ExpiresAt = f.exp
	return id, true
}

func (f *fakeAuth) Revoke(cookieValue string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, cookieValue)
	delete(f.csrf, cookieValue)
}

func (f *fakeAuth) ValidateCSRF(cookieValue, token string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	want, ok := f.csrf[cookieValue]
	return ok && want != "" && want == token
}

func (f *fakeAuth) CookieName() string { return "__Host-herdr_phone" }

// addSession pre-creates a session for tests that skip pairing.
func (f *fakeAuth) addSession(cookieValue string, id Identity, csrf string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[cookieValue] = id
	f.csrf[cookieValue] = csrf
}

// ---- fake StateProvider ---------------------------------------------------

type fakeState struct {
	mu      sync.Mutex
	snap    Snapshot
	subs    map[int]func(Snapshot)
	nextID  int
	gens    map[string]uint64
	caps    json.RawMessage
	content map[string][]byte
	readErr error
	// readLines records the line bound the last ReadPane was asked for, so a test
	// can assert the clamp that was actually applied upstream.
	readLines int
	runs      []RunSummary
}

// lastReadLines returns the line bound of the most recent ReadPane call.
func (s *fakeState) lastReadLines() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLines
}

func newFakeState() *fakeState {
	return &fakeState{
		snap:    Snapshot{Version: 1, Hash: "hash-1", Data: json.RawMessage(`{"workspaces":[]}`), UpdatedAt: time.Unix(1000, 0)},
		subs:    map[int]func(Snapshot){},
		gens:    map[string]uint64{"pane-1": 7},
		caps:    json.RawMessage(`{"agent_kinds":["claude","codex"]}`),
		content: map[string][]byte{"pane-1": []byte("last visible output")},
		runs: []RunSummary{{
			RunID:            "pane-1@7#0123456789abcdef",
			PaneID:           "pane-1",
			PaneGeneration:   7,
			AgentIncarnation: "0123456789abcdef",
			WorkspaceID:      "w1",
			WorkspaceLabel:   "space-api",
			TabID:            "w1:t1",
			TabLabel:         "agents",
			TerminalID:       "term-1",
			AgentKind:        "claude",
			AgentName:        "auth",
			DisplayAgent:     "Claude Code",
			Title:            "Fix auth refresh",
			Status:           "blocked",
			InteractiveReady: true,
			CWD:              "/code/space-api",
			ForegroundCWD:    "/code/space-api",
			Worktree: &RunWorktree{
				RepoName:         "space-api",
				RepoRoot:         "/code/space-api",
				CheckoutPath:     "/code/space-api-auth",
				IsLinkedWorktree: true,
			},
			Revision:       42,
			StateChangeSeq: 9,
		}},
	}
}

func (s *fakeState) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}

func (s *fakeState) Subscribe(fn func(Snapshot)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	s.subs[id] = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subs, id)
	}
}

func (s *fakeState) push(snap Snapshot) {
	s.mu.Lock()
	s.snap = snap
	subs := make([]func(Snapshot), 0, len(s.subs))
	for _, fn := range s.subs {
		subs = append(subs, fn)
	}
	s.mu.Unlock()
	for _, fn := range subs {
		fn(snap)
	}
}

func (s *fakeState) Generation(paneID string) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.gens[paneID]
	return g, ok
}

func (s *fakeState) Capabilities() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.caps
}

func (s *fakeState) Runs() RunProjection {
	s.mu.Lock()
	defer s.mu.Unlock()
	return RunProjection{SnapshotHash: s.snap.Hash, Runs: slices.Clone(s.runs)}
}

func (s *fakeState) setRuns(runs []RunSummary) {
	s.mu.Lock()
	s.runs = runs
	s.mu.Unlock()
}

func (s *fakeState) setContent(paneID string, content []byte) {
	s.mu.Lock()
	s.content[paneID] = content
	s.mu.Unlock()
}

func (s *fakeState) setReadErr(err error) {
	s.mu.Lock()
	s.readErr = err
	s.mu.Unlock()
}

func (s *fakeState) ReadPane(_ context.Context, paneID, _ string, lines int) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readLines = lines
	if s.readErr != nil {
		return nil, s.readErr
	}
	c, ok := s.content[paneID]
	if !ok {
		return nil, io.EOF
	}
	return c, nil
}

// ---- fake HerdrMutator ----------------------------------------------------

type mutCall struct {
	op     string
	params json.RawMessage
}

type fakeMutator struct {
	mu     sync.Mutex
	calls  []mutCall
	result json.RawMessage
	err    error
	delay  time.Duration
	// enter, if set, receives the op each time Mutate is entered (before hold).
	enter chan string
	// hold, if set, blocks Mutate until closed (or ctx cancels), letting a test
	// keep a request in flight while it issues a concurrent duplicate.
	hold chan struct{}
}

func (m *fakeMutator) Mutate(ctx context.Context, op string, params json.RawMessage) (json.RawMessage, error) {
	m.mu.Lock()
	m.calls = append(m.calls, mutCall{op: op, params: params})
	delay, err, result, enter, hold := m.delay, m.err, m.result, m.enter, m.hold
	m.mu.Unlock()
	if enter != nil {
		enter <- op
	}
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = json.RawMessage(`{"ok":true}`)
	}
	return result, nil
}

func (m *fakeMutator) setDelay(d time.Duration) {
	m.mu.Lock()
	m.delay = d
	m.mu.Unlock()
}

func (m *fakeMutator) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func (m *fakeMutator) lastOp() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return ""
	}
	return m.calls[len(m.calls)-1].op
}

// ---- fake Daemon / Tunnel / Directories / Auditor -------------------------

type fakeDaemon struct{}

func (fakeDaemon) Status() DaemonStatus {
	return DaemonStatus{Version: "0.1.0", Protocol: 17, Mode: "quick", Ready: true,
		Herdr: ComponentHealth{Healthy: true}, State: ComponentHealth{Healthy: true}}
}

type fakeTunnel struct{}

func (fakeTunnel) Tunnel() TunnelInfo {
	return TunnelInfo{Mode: "quick", PublicURL: "https://example.trycloudflare.com", Health: ComponentHealth{Healthy: true}}
}

type fakeDirs struct {
	root string
	err  error
}

func (d fakeDirs) Resolve(path string) (string, error) {
	if d.err != nil {
		return "", d.err
	}
	// Confine to root: only allow the root itself or a path already under it.
	if path == "" {
		return "", io.EOF
	}
	if !strings.HasPrefix(path, d.root) {
		return "", io.ErrUnexpectedEOF
	}
	return path, nil
}

type fakeAuditor struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (a *fakeAuditor) Record(e AuditEntry) {
	a.mu.Lock()
	a.entries = append(a.entries, e)
	a.mu.Unlock()
}

func (a *fakeAuditor) events() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.entries))
	for i, e := range a.entries {
		out[i] = e.Event
	}
	return out
}

func (a *fakeAuditor) hasEvent(name string) bool {
	return slices.Contains(a.events(), name)
}

// ---- fake terminal launcher -----------------------------------------------

type fakeTermCtrl struct {
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	once    sync.Once
	term    chan struct{}
}

func (c *fakeTermCtrl) Stdout() io.Reader { return c.stdoutR }
func (c *fakeTermCtrl) Stdin() io.Writer  { return c.stdinW }
func (c *fakeTermCtrl) Terminate() error {
	c.once.Do(func() { close(c.term); _ = c.stdoutW.Close(); _ = c.stdinW.Close() })
	return nil
}
func (c *fakeTermCtrl) Wait() error { <-c.term; return nil }

type fakeTermLauncher struct {
	mu   sync.Mutex
	spec terminal.Spec
}

func (l *fakeTermLauncher) Launch(_ context.Context, spec terminal.Spec) (terminal.Controller, error) {
	l.mu.Lock()
	l.spec = spec
	l.mu.Unlock()
	or, ow := io.Pipe()
	ir, iw := io.Pipe()
	c := &fakeTermCtrl{stdoutR: or, stdoutW: ow, stdinR: ir, stdinW: iw, term: make(chan struct{})}
	// Emit one frame then a close record so the session ends deterministically.
	go func() {
		_, _ = io.WriteString(c.stdoutW, `{"type":"terminal.frame","seq":1,"encoding":"ansi","width":80,"height":24,"full":true,"bytes":"aGk="}`+"\n")
		_, _ = io.WriteString(c.stdoutW, `{"type":"terminal.closed","reason":"done"}`+"\n")
	}()
	return c, nil
}

func (l *fakeTermLauncher) lastSpec() terminal.Spec {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.spec
}

// ---- harness --------------------------------------------------------------

type harness struct {
	t       *testing.T
	srv     *httptest.Server
	server  *Server
	auth    *fakeAuth
	state   *fakeState
	mutator *fakeMutator
	dirs    *fakeDirs
	audit   *fakeAuditor
	term    *fakeTermLauncher
	addr    string
	origin  string
	now     func() time.Time
}

type harnessOpt func(*Config, *Deps, *harness)

func withNamed() harnessOpt {
	return func(_ *Config, d *Deps, h *harness) { h.auth.named = true }
}

func withClock(now func() time.Time) harnessOpt {
	return func(_ *Config, d *Deps, h *harness) { d.Now = now; h.now = now }
}

func withProbe(token, instanceID string) harnessOpt {
	return func(_ *Config, d *Deps, _ *harness) {
		d.QuickProbeToken = token
		d.InstanceID = instanceID
	}
}

func withConfig(fn func(*Config)) harnessOpt {
	return func(c *Config, _ *Deps, _ *harness) { fn(c) }
}

func withNoTerminalFilter() harnessOpt {
	return func(_ *Config, d *Deps, _ *harness) { d.TerminalFilterFactory = nil }
}

func newHarness(t *testing.T, opts ...harnessOpt) *harness {
	t.Helper()
	h := &harness{
		t:       t,
		auth:    newFakeAuth(),
		state:   newFakeState(),
		mutator: &fakeMutator{},
		audit:   &fakeAuditor{},
		term:    &fakeTermLauncher{},
	}
	dir := t.TempDir()
	h.dirs = &fakeDirs{root: dir}

	unstarted := httptest.NewUnstartedServer(nil)
	h.addr = unstarted.Listener.Addr().String()
	h.origin = "http://" + h.addr

	cfg := Config{
		PublicHost:     h.addr,
		AllowedOrigins: []string{h.origin},
		RateBurst:      1000,
	}
	deps := Deps{
		Auth:                  h.auth,
		State:                 h.state,
		Mutator:               h.mutator,
		Daemon:                fakeDaemon{},
		Tunnel:                fakeTunnel{},
		Directories:           h.dirs,
		Audit:                 h.audit,
		TerminalLauncher:      h.term,
		TerminalFilterFactory: func() terminal.Filter { return terminal.NopFilter{} },
		Assets: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("SHELL"))
		}),
	}
	for _, o := range opts {
		o(&cfg, &deps, h)
	}

	srv, err := New(cfg, deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.server = srv
	unstarted.Config.Handler = srv
	unstarted.Start()
	h.srv = unstarted
	t.Cleanup(func() {
		unstarted.Close()
		srv.Close()
	})
	return h
}

// sessionCookie returns a cookie value with a live session and its CSRF token.
func (h *harness) sessionCookie() (cookie, csrf string) {
	h.auth.addSession("live-session", Identity{Subject: "op@example.com", Display: "operator", SessionID: "sid-live", Quick: true}, "csrf-live")
	return "live-session", "csrf-live"
}

type reqOpt func(*http.Request)

func withCookie(value string) reqOpt {
	return func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "__Host-herdr_phone", Value: value})
	}
}

func withOrigin(origin string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Origin", origin) }
}

func withCSRF(token string) reqOpt {
	return func(r *http.Request) { r.Header.Set("X-CSRF-Token", token) }
}

func withHeader(k, v string) reqOpt {
	return func(r *http.Request) { r.Header.Set(k, v) }
}

func withHost(host string) reqOpt {
	return func(r *http.Request) { r.Host = host }
}

func (h *harness) do(method, path string, body string, opts ...reqOpt) *http.Response {
	h.t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, rdr)
	if err != nil {
		h.t.Fatalf("new request: %v", err)
	}
	for _, o := range opts {
		o(req)
	}
	// The default client does not follow to a different host; fine here.
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatalf("do %s %s: %v", method, path, err)
	}
	return resp
}

// authedGET issues a GET with a valid session cookie.
func (h *harness) authedGET(path string, opts ...reqOpt) *http.Response {
	cookie, _ := h.sessionCookie()
	return h.do(http.MethodGet, path, "", append([]reqOpt{withCookie(cookie), withOrigin(h.origin)}, opts...)...)
}

// authedJSON issues a mutating JSON request with session + CSRF + origin.
func (h *harness) authedJSON(method, path, body string, opts ...reqOpt) *http.Response {
	cookie, csrf := h.sessionCookie()
	base := []reqOpt{
		withCookie(cookie),
		withCSRF(csrf),
		withOrigin(h.origin),
		withHeader("Content-Type", "application/json"),
	}
	return h.do(method, path, body, append(base, opts...)...)
}

func decodeBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

// ---- raw websocket client (for events and terminal) -----------------------

type wsClient struct {
	raw net.Conn
	br  *bufio.Reader
}

func (h *harness) dialWS(path string, cookie string) (*wsClient, *http.Response) {
	h.t.Helper()
	raw, err := net.Dial("tcp", h.addr)
	if err != nil {
		h.t.Fatalf("dial: %v", err)
	}
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + h.addr + "\r\n" +
		"Origin: " + h.origin + "\r\n" +
		"Cookie: __Host-herdr_phone=" + cookie + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: AAAAAAAAAAAAAAAAAAAAAA==\r\n\r\n"
	if _, err := io.WriteString(raw, req); err != nil {
		h.t.Fatalf("ws write: %v", err)
	}
	br := bufio.NewReader(raw)
	status, err := br.ReadString('\n')
	if err != nil {
		h.t.Fatalf("ws status: %v", err)
	}
	if !strings.Contains(status, "101") {
		_ = raw.Close()
		return nil, &http.Response{Status: strings.TrimSpace(status)}
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			h.t.Fatalf("ws header: %v", err)
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	return &wsClient{raw: raw, br: br}, &http.Response{Status: "101"}
}

// readFrame returns the next non-control frame's opcode and payload, skipping
// ping/pong frames automatically. It fails the test on any read error.
func (c *wsClient) readFrame(t *testing.T) (opcode byte, data []byte) {
	t.Helper()
	op, buf, err := c.readFrameErr()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return op, buf
}

// readFrameErr is like readFrame but returns read errors (e.g. a deadline set on
// the underlying conn) instead of failing the test, so a caller can bound how
// long it waits for coalesced frames.
func (c *wsClient) readFrameErr() (opcode byte, data []byte, err error) {
	for {
		var h [2]byte
		if _, err = io.ReadFull(c.br, h[:]); err != nil {
			return 0, nil, err
		}
		op := h[0] & 0x0f
		length := int64(h[1] & 0x7f)
		switch length {
		case 126:
			var ext [2]byte
			if _, err = io.ReadFull(c.br, ext[:]); err != nil {
				return 0, nil, err
			}
			length = int64(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err = io.ReadFull(c.br, ext[:]); err != nil {
				return 0, nil, err
			}
			length = int64(binary.BigEndian.Uint64(ext[:]))
		}
		buf := make([]byte, length)
		if _, err = io.ReadFull(c.br, buf); err != nil {
			return 0, nil, err
		}
		if op == 0x9 || op == 0xA { // ping/pong: skip
			continue
		}
		return op, buf, nil
	}
}

// setReadDeadline bounds subsequent reads on the raw connection.
func (c *wsClient) setReadDeadline(t time.Time) { _ = c.raw.SetReadDeadline(t) }

func (c *wsClient) close() { _ = c.raw.Close() }
