package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"sync"
	"testing"
)

// fakeHerdr is an in-memory Herdr socket server for tests. It answers ping,
// session.snapshot, and events.subscribe, and responds to every mutation method
// with a minimal success envelope of the correct result type while recording the
// params it received. It never spawns a real process or touches the network.
type fakeHerdr struct {
	t        *testing.T
	listener net.Listener
	path     string

	mu       sync.Mutex
	lastByOp map[string]json.RawMessage
	// snapshot is the `snapshot` object returned by session.snapshot. Tests that
	// exercise a projection over real topology replace it.
	snapshot string
	// readText is the text returned by pane.read/agent.read.
	readText string
}

// emptySnapshot is the default session.snapshot payload: a valid but empty
// topology.
const emptySnapshot = `{"version":"1","protocol":17,"workspaces":[],"tabs":[],"panes":[],"agents":[],"worktrees":[],"layouts":[]}`

// setSnapshot replaces the topology session.snapshot returns. The payload is
// compacted because the wire protocol is newline-delimited: an indented fixture
// would break framing.
func (f *fakeHerdr) setSnapshot(snapshot string) {
	f.t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(snapshot)); err != nil {
		f.t.Fatalf("snapshot fixture is not valid JSON: %v", err)
	}
	f.mu.Lock()
	f.snapshot = buf.String()
	f.mu.Unlock()
}

// setReadText replaces the text pane.read/agent.read return.
func (f *fakeHerdr) setReadText(text string) {
	f.mu.Lock()
	f.readText = text
	f.mu.Unlock()
}

// startFakeHerdr creates a Unix socket under /tmp (short path, so AF_UNIX bind
// never overflows) and serves it until the test ends.
func startFakeHerdr(t *testing.T) *fakeHerdr {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "hp-herdr-")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := dir + "/herdr.sock"
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeHerdr{t: t, listener: ln, path: path, lastByOp: map[string]json.RawMessage{}, snapshot: emptySnapshot}
	t.Cleanup(func() { _ = ln.Close() })
	go f.serve()
	return f
}

func (f *fakeHerdr) serve() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeHerdr) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil && len(line) == 0 {
			return
		}
		var req struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(trimLine(line), &req) != nil {
			return
		}
		f.record(req.Method, req.Params)
		resp := f.response(req.ID, req.Method)
		if _, err := conn.Write(append(resp, '\n')); err != nil {
			return
		}
		// events.subscribe keeps the connection open; the client reads the initial
		// frame then blocks. Every other method is one-shot: the client closes and
		// the next read returns EOF, ending this goroutine.
	}
}

func (f *fakeHerdr) record(method string, params json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make(json.RawMessage, len(params))
	copy(cp, params)
	f.lastByOp[method] = cp
}

// params returns the most recent params recorded for a method.
func (f *fakeHerdr) params(method string) json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastByOp[method]
}

func (f *fakeHerdr) response(id, method string) []byte {
	f.mu.Lock()
	snapshot, readText := f.snapshot, f.readText
	f.mu.Unlock()

	switch method {
	case "ping":
		return []byte(`{"id":"` + id + `","result":{"type":"pong","version":"0.7.5","protocol":17,"capabilities":{"live_handoff":true}}}`)
	case "session.snapshot":
		return []byte(`{"id":"` + id + `","result":{"type":"session_snapshot","snapshot":` + snapshot + `}}`)
	case "events.subscribe":
		return []byte(`{"id":"` + id + `","result":{"type":"subscription_started"}}`)
	case "pane.read", "agent.read":
		text, _ := json.Marshal(readText)
		return []byte(`{"id":"` + id + `","result":{"type":"pane_read","read":{"pane_id":"w1:p1",` +
			`"workspace_id":"w1","tab_id":"w1:t1","source":"recent_unwrapped","format":"text",` +
			`"text":` + string(text) + `,"revision":42,"truncated":false}}}`)
	}
	typ := mutationResultType(method)
	if typ == "" {
		return []byte(`{"id":"` + id + `","error":{"code":"unknown","message":"unmapped method"}}`)
	}
	return []byte(`{"id":"` + id + `","result":{"type":"` + typ + `"}}`)
}

// mutationResultType maps a Herdr method to the result discriminator the client
// expects. It mirrors the typed client and lets the fake answer any mutation.
func mutationResultType(method string) string {
	switch method {
	case "pane.read", "agent.read":
		return "pane_read"
	case "workspace.create":
		return "workspace_created"
	case "workspace.focus", "workspace.rename":
		return "workspace_info"
	case "tab.create":
		return "tab_created"
	case "tab.focus", "tab.rename":
		return "tab_info"
	case "tab.move":
		return "tab_list"
	case "pane.focus", "pane.split", "pane.rename":
		return "pane_info"
	case "pane.resize":
		return "pane_resize"
	case "pane.zoom":
		return "pane_zoom"
	case "pane.swap":
		return "pane_swap"
	case "pane.move":
		return "pane_move"
	case "agent.focus", "agent.rename":
		return "agent_info"
	case "agent.prompt":
		return "agent_prompted"
	case "agent.start":
		return "agent_started"
	case "workspace.close", "tab.close", "pane.close", "agent.send_keys":
		return "ok"
	case "worktree.create":
		return "worktree_created"
	case "worktree.open":
		return "worktree_opened"
	case "worktree.remove":
		return "worktree_removed"
	case "worktree.list":
		return "worktree_list"
	}
	return ""
}

func trimLine(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
