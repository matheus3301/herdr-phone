package herdr

import (
	"context"
	"testing"
)

func readDispatch(t *testing.T) *scriptedServer {
	t.Helper()
	readPayload := func(id string) map[string]any {
		return map[string]any{"type": "pane_read", "read": map[string]any{
			"pane_id": id, "workspace_id": "w1", "tab_id": "w1:t1",
			"source": "recent_unwrapped", "format": "text", "text": "hello", "revision": 12, "truncated": false,
		}}
	}
	return newServer(func(req map[string]any) []byte {
		switch req["method"] {
		case "pane.read", "agent.read":
			id, _ := paramField(req, "pane_id").(string)
			if id == "" {
				id, _ = paramField(req, "target").(string)
			}
			return reply(req, readPayload(id))
		case "workspace.list":
			return reply(req, map[string]any{"type": "workspace_list", "workspaces": []any{
				map[string]any{"workspace_id": "w1", "label": "a", "agent_status": "idle"},
				map[string]any{"workspace_id": "w2", "label": "b", "agent_status": "working"},
			}})
		case "tab.list":
			return reply(req, map[string]any{"type": "tab_list", "tabs": []any{
				map[string]any{"tab_id": "w1:t1", "workspace_id": "w1", "agent_status": "idle"},
			}})
		case "pane.list":
			return reply(req, map[string]any{"type": "pane_list", "panes": []any{
				map[string]any{"pane_id": "w1:p1", "workspace_id": "w1", "tab_id": "w1:t1", "agent_status": "idle", "revision": 1},
			}})
		case "agent.list":
			return reply(req, map[string]any{"type": "agent_list", "agents": []any{
				map[string]any{"agent": "claude", "pane_id": "w1:p1", "agent_status": "blocked", "workspace_id": "w1", "tab_id": "w1:t1"},
			}})
		case "agent.get":
			return reply(req, map[string]any{"type": "agent_info", "agent": map[string]any{
				"agent": "codex", "name": "rev", "pane_id": "w1:p2", "agent_status": "working", "workspace_id": "w1", "tab_id": "w1:t1",
			}})
		case "worktree.list":
			return reply(req, map[string]any{"type": "worktree_list", "source": "w1", "worktrees": []any{
				map[string]any{"path": "/r/wt", "label": "wt", "branch": "feat", "is_bare": false, "is_detached": false, "is_linked_worktree": true, "is_prunable": false},
			}})
		default:
			return replyError(req, "not_found", "no read dispatch")
		}
	})
}

func TestPaneReadAndSource(t *testing.T) {
	t.Parallel()
	s := readDispatch(t)
	c := newTestClient(t, s)
	res, err := c.PaneRead(context.Background(), "w1:p1", PaneReadOptions{Source: SourceRecentUnwrapped, Format: FormatText, Lines: 100})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "hello" || res.Revision != 12 || res.Source != SourceRecentUnwrapped {
		t.Fatalf("bad read result: %+v", res)
	}
	// Params must carry the source, format, and lines.
	p, _ := s.lastRequest()["params"].(map[string]any)
	mustEq(t, p["source"], "recent_unwrapped")
	mustEq(t, p["format"], "text")
	mustEq(t, p["lines"], float64(100))
	if got := res.String(); got == "" {
		t.Fatal("String() should render a header")
	}
}

func TestPaneReadDefaultsSourceToVisible(t *testing.T) {
	t.Parallel()
	s := readDispatch(t)
	c := newTestClient(t, s)
	if _, err := c.PaneRead(context.Background(), "w1:p1", PaneReadOptions{}); err != nil {
		t.Fatal(err)
	}
	p, _ := s.lastRequest()["params"].(map[string]any)
	mustEq(t, p["source"], "visible")
	// Lines omitted when zero.
	if _, present := p["lines"]; present {
		t.Fatal("lines should be omitted when unset")
	}
}

func TestAgentRead(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, readDispatch(t))
	res, err := c.AgentRead(context.Background(), "rev", PaneReadOptions{Source: SourceVisible})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "hello" {
		t.Fatalf("agent read text: %q", res.Text)
	}
}

func TestListReads(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, readDispatch(t))
	ctx := context.Background()

	ws, err := c.WorkspaceList(ctx)
	if err != nil || len(ws) != 2 || ws[1].AgentStatus != StatusWorking {
		t.Fatalf("workspace list: %v %+v", err, ws)
	}
	tabs, err := c.TabList(ctx, "w1")
	if err != nil || len(tabs) != 1 {
		t.Fatalf("tab list: %v %+v", err, tabs)
	}
	panes, err := c.PaneList(ctx, "w1")
	if err != nil || len(panes) != 1 {
		t.Fatalf("pane list: %v %+v", err, panes)
	}
	agents, err := c.AgentList(ctx)
	if err != nil || len(agents) != 1 || agents[0].AgentStatus != StatusBlocked {
		t.Fatalf("agent list: %v %+v", err, agents)
	}
	ag, err := c.AgentGet(ctx, "rev")
	if err != nil || ag.Name != "rev" || ag.AgentStatus != StatusWorking {
		t.Fatalf("agent get: %v %+v", err, ag)
	}
	wts, err := c.WorktreeList(ctx, "w1")
	if err != nil || len(wts) != 1 || !wts[0].IsLinkedWorktree {
		t.Fatalf("worktree list: %v %+v", err, wts)
	}
}

func TestReadAndListValidation(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, readDispatch(t))
	ctx := context.Background()
	if _, err := c.WorktreeList(ctx, ""); !IsCode(err, "invalid_params") {
		t.Fatalf("empty workspace must be rejected: %v", err)
	}
}

func TestServerErrorOnReadPreserved(t *testing.T) {
	t.Parallel()
	s := newServer(func(req map[string]any) []byte {
		return replyError(req, "not_found", "pane not found")
	})
	c := newTestClient(t, s)
	if _, err := c.PaneRead(context.Background(), "gone", PaneReadOptions{}); !IsCode(err, "not_found") {
		t.Fatalf("want not_found, got %v", err)
	}
}
