package herdr

import (
	"context"
	"testing"
)

// dispatchServer replies with a canned success result per method, so we can
// exercise every mutation and inspect the exact request params Herdr received.
func dispatchServer(t *testing.T) *scriptedServer {
	t.Helper()
	paneInfo := func(id string) map[string]any {
		return map[string]any{"pane_id": id, "terminal_id": "term_" + id, "workspace_id": "w1", "tab_id": "w1:t1", "focused": true, "agent_status": "idle", "revision": 1}
	}
	tabInfo := func(id string) map[string]any {
		return map[string]any{"tab_id": id, "workspace_id": "w1", "number": 1, "label": "t", "focused": true, "pane_count": 1, "agent_status": "idle"}
	}
	wsInfo := func(id string) map[string]any {
		return map[string]any{"workspace_id": id, "number": 1, "label": "w", "focused": true, "pane_count": 1, "tab_count": 1, "active_tab_id": id + ":t1", "agent_status": "idle"}
	}
	return newServer(func(req map[string]any) []byte {
		switch req["method"] {
		case "workspace.create":
			return reply(req, map[string]any{"type": "workspace_created", "workspace": wsInfo("w9"), "tab": tabInfo("w9:t1"), "root_pane": paneInfo("w9:p1")})
		case "workspace.focus", "workspace.rename":
			return reply(req, map[string]any{"type": "workspace_info", "workspace": wsInfo("w1")})
		case "workspace.close", "tab.close", "pane.close", "agent.send_keys":
			return reply(req, map[string]any{"type": "ok"})
		case "tab.create":
			return reply(req, map[string]any{"type": "tab_created", "tab": tabInfo("w1:t9"), "root_pane": paneInfo("w1:p9")})
		case "tab.focus", "tab.rename":
			return reply(req, map[string]any{"type": "tab_info", "tab": tabInfo("w1:t1")})
		case "tab.move":
			return reply(req, map[string]any{"type": "tab_list", "tabs": []any{tabInfo("w1:t1")}})
		case "pane.split", "pane.focus", "pane.rename":
			return reply(req, map[string]any{"type": "pane_info", "pane": paneInfo("w1:p2")})
		case "pane.resize":
			return reply(req, map[string]any{"type": "pane_resize", "resize": map[string]any{"changed": true, "pane_id": "w1:p1", "focused_pane_id": "w1:p1"}})
		case "pane.zoom":
			return reply(req, map[string]any{"type": "pane_zoom", "zoom": map[string]any{"changed": true, "zoomed": true, "pane_id": "w1:p1", "focused_pane_id": "w1:p1"}})
		case "pane.swap":
			return reply(req, map[string]any{"type": "pane_swap", "swap": map[string]any{"changed": true, "source_pane_id": "w1:p1", "target_pane_id": "w1:p2", "focused_pane_id": "w1:p1"}})
		case "pane.move":
			return reply(req, map[string]any{"type": "pane_move", "move_result": map[string]any{
				"changed": true, "pane": paneInfo("w2:p1"), "previous_pane_id": "w1:p3",
				"previous_tab_id": "w1:t1", "previous_workspace_id": "w1", "focused_pane_id": "w2:p1"}})
		case "worktree.create":
			return reply(req, map[string]any{"type": "worktree_created",
				"worktree":  map[string]any{"path": "/repo/wt", "label": "wt", "branch": "feat", "is_bare": false, "is_detached": false, "is_linked_worktree": true, "is_prunable": false},
				"workspace": wsInfo("w1"), "tab": tabInfo("w1:t1"), "root_pane": paneInfo("w1:p1")})
		case "worktree.open":
			return reply(req, map[string]any{"type": "worktree_opened", "already_open": false,
				"worktree":  map[string]any{"path": "/repo/wt", "label": "wt", "is_bare": false, "is_detached": false, "is_linked_worktree": true, "is_prunable": false},
				"workspace": wsInfo("w1"), "tab": tabInfo("w1:t1"), "root_pane": paneInfo("w1:p1")})
		case "worktree.remove":
			return reply(req, map[string]any{"type": "worktree_removed", "forced": true, "path": "/repo/wt", "workspace_id": "w1"})
		case "agent.prompt":
			return reply(req, map[string]any{"type": "agent_prompted", "agent": map[string]any{"agent": "claude", "pane_id": "w1:p1", "agent_status": "working"}})
		case "agent.rename", "agent.focus", "agent.wait":
			return reply(req, map[string]any{"type": "agent_info", "agent": map[string]any{"agent": "claude", "name": "rev", "pane_id": "w1:p1", "agent_status": "idle"}})
		case "agent.start":
			return reply(req, map[string]any{"type": "agent_started", "agent": map[string]any{"agent": "codex", "name": "rev", "pane_id": "w1:p1", "agent_status": "idle"}, "argv": []any{"codex"}})
		default:
			return replyError(req, "not_found", "no dispatch for "+toStr(req["method"]))
		}
	})
}

func toStr(v any) string { s, _ := v.(string); return s }

func TestMutationParams(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		name   string
		call   func(c *Client) error
		method string
		// check inspects params of the recorded request.
		check func(t *testing.T, params map[string]any)
	}{
		{"workspace.create", func(c *Client) error {
			_, err := c.WorkspaceCreate(ctx, WorkspaceCreateOptions{CWD: "/x", Label: "L", Focus: false})
			return err
		}, "workspace.create", func(t *testing.T, p map[string]any) {
			mustEq(t, p["cwd"], "/x")
			mustEq(t, p["label"], "L")
			mustEq(t, p["focus"], false)
		}},
		{"workspace.focus", func(c *Client) error { _, err := c.WorkspaceFocus(ctx, "w1"); return err },
			"workspace.focus", func(t *testing.T, p map[string]any) { mustEq(t, p["workspace_id"], "w1") }},
		{"workspace.rename", func(c *Client) error { _, err := c.WorkspaceRename(ctx, "w1", "New"); return err },
			"workspace.rename", func(t *testing.T, p map[string]any) {
				mustEq(t, p["workspace_id"], "w1")
				mustEq(t, p["label"], "New")
			}},
		{"workspace.close", func(c *Client) error { return c.WorkspaceClose(ctx, "w1") },
			"workspace.close", func(t *testing.T, p map[string]any) { mustEq(t, p["workspace_id"], "w1") }},
		{"tab.create", func(c *Client) error {
			_, err := c.TabCreate(ctx, TabCreateOptions{WorkspaceID: "w1", Label: "T", Focus: true})
			return err
		}, "tab.create", func(t *testing.T, p map[string]any) {
			mustEq(t, p["workspace_id"], "w1")
			mustEq(t, p["focus"], true)
		}},
		{"tab.focus", func(c *Client) error { _, err := c.TabFocus(ctx, "w1:t1"); return err },
			"tab.focus", func(t *testing.T, p map[string]any) { mustEq(t, p["tab_id"], "w1:t1") }},
		{"tab.rename", func(c *Client) error { _, err := c.TabRename(ctx, "w1:t1", "R"); return err },
			"tab.rename", func(t *testing.T, p map[string]any) { mustEq(t, p["label"], "R") }},
		{"tab.move", func(c *Client) error { _, err := c.TabMove(ctx, "w1:t1", 2); return err },
			"tab.move", func(t *testing.T, p map[string]any) { mustEq(t, p["insert_index"], float64(2)) }},
		{"tab.close", func(c *Client) error { return c.TabClose(ctx, "w1:t1") },
			"tab.close", func(t *testing.T, p map[string]any) { mustEq(t, p["tab_id"], "w1:t1") }},
		{"pane.split", func(c *Client) error {
			_, err := c.PaneSplit(ctx, PaneSplitOptions{TargetPaneID: "w1:p1", Direction: SplitRight, Ratio: 0.4, CWD: "/z"})
			return err
		}, "pane.split", func(t *testing.T, p map[string]any) {
			mustEq(t, p["target_pane_id"], "w1:p1")
			mustEq(t, p["direction"], "right")
			mustEq(t, p["ratio"], 0.4)
			mustEq(t, p["cwd"], "/z")
		}},
		{"pane.focus", func(c *Client) error { _, err := c.PaneFocus(ctx, "w1:p1"); return err },
			"pane.focus", func(t *testing.T, p map[string]any) { mustEq(t, p["pane_id"], "w1:p1") }},
		{"pane.rename", func(c *Client) error { _, err := c.PaneRename(ctx, "w1:p1", "lbl"); return err },
			"pane.rename", func(t *testing.T, p map[string]any) { mustEq(t, p["label"], "lbl") }},
		{"pane.resize", func(c *Client) error { _, err := c.PaneResize(ctx, "w1:p1", DirLeft, 0.1); return err },
			"pane.resize", func(t *testing.T, p map[string]any) {
				mustEq(t, p["direction"], "left")
				mustEq(t, p["amount"], 0.1)
			}},
		{"pane.zoom", func(c *Client) error { _, err := c.PaneZoom(ctx, "w1:p1", ZoomOn); return err },
			"pane.zoom", func(t *testing.T, p map[string]any) { mustEq(t, p["mode"], "on") }},
		{"pane.swap", func(c *Client) error { _, err := c.PaneSwap(ctx, "w1:p1", "w1:p2"); return err },
			"pane.swap", func(t *testing.T, p map[string]any) {
				mustEq(t, p["source_pane_id"], "w1:p1")
				mustEq(t, p["target_pane_id"], "w1:p2")
			}},
		{"pane.move/new_tab", func(c *Client) error {
			_, err := c.PaneMove(ctx, "w1:p3", MoveToNewTab("w2", "moved"), false)
			return err
		}, "pane.move", func(t *testing.T, p map[string]any) {
			mustEq(t, p["pane_id"], "w1:p3")
			dest := p["destination"].(map[string]any)
			mustEq(t, dest["type"], "new_tab")
			mustEq(t, dest["workspace_id"], "w2")
			mustEq(t, dest["label"], "moved")
		}},
		{"pane.move/tab", func(c *Client) error {
			_, err := c.PaneMove(ctx, "w1:p3", MoveToTab("w1:t2", SplitDown, "w1:p5", 0.3), true)
			return err
		}, "pane.move", func(t *testing.T, p map[string]any) {
			dest := p["destination"].(map[string]any)
			mustEq(t, dest["type"], "tab")
			mustEq(t, dest["tab_id"], "w1:t2")
			mustEq(t, dest["split"], "down")
			mustEq(t, dest["target_pane_id"], "w1:p5")
			mustEq(t, dest["ratio"], 0.3)
		}},
		{"pane.close", func(c *Client) error { return c.PaneClose(ctx, "w1:p1") },
			"pane.close", func(t *testing.T, p map[string]any) { mustEq(t, p["pane_id"], "w1:p1") }},
		{"worktree.create", func(c *Client) error {
			_, err := c.WorktreeCreate(ctx, WorktreeCreateOptions{WorkspaceID: "w1", Branch: "feat", Base: "main"})
			return err
		}, "worktree.create", func(t *testing.T, p map[string]any) {
			mustEq(t, p["workspace_id"], "w1")
			mustEq(t, p["branch"], "feat")
			mustEq(t, p["base"], "main")
		}},
		{"worktree.open", func(c *Client) error {
			_, err := c.WorktreeOpen(ctx, WorktreeOpenOptions{WorkspaceID: "w1", Path: "/repo/wt"})
			return err
		}, "worktree.open", func(t *testing.T, p map[string]any) { mustEq(t, p["path"], "/repo/wt") }},
		{"worktree.remove", func(c *Client) error { _, err := c.WorktreeRemove(ctx, "w1", true); return err },
			"worktree.remove", func(t *testing.T, p map[string]any) {
				mustEq(t, p["workspace_id"], "w1")
				mustEq(t, p["force"], true)
			}},
		{"agent.prompt", func(c *Client) error {
			_, err := c.AgentPrompt(ctx, "rev", "hello", PromptOptions{Wait: true, TimeoutMS: 1000})
			return err
		}, "agent.prompt", func(t *testing.T, p map[string]any) {
			mustEq(t, p["target"], "rev")
			mustEq(t, p["text"], "hello")
			w := p["wait"].(map[string]any)
			mustEq(t, w["timeout_ms"], float64(1000))
		}},
		{"agent.send_keys", func(c *Client) error { return c.AgentSendKeys(ctx, "rev", []string{"ctrl+c", "enter"}) },
			"agent.send_keys", func(t *testing.T, p map[string]any) {
				keys := p["keys"].([]any)
				if len(keys) != 2 || keys[0] != "ctrl+c" {
					t.Fatalf("keys not sent verbatim: %v", keys)
				}
			}},
		{"agent.rename", func(c *Client) error { _, err := c.AgentRename(ctx, "w1:p1", "rev"); return err },
			"agent.rename", func(t *testing.T, p map[string]any) { mustEq(t, p["name"], "rev") }},
		{"agent.focus", func(c *Client) error { _, err := c.AgentFocus(ctx, "rev"); return err },
			"agent.focus", func(t *testing.T, p map[string]any) { mustEq(t, p["target"], "rev") }},
		{"agent.start", func(c *Client) error {
			_, err := c.AgentStart(ctx, StartOptions{Name: "rev", Kind: "codex", PaneID: "w1:p1", Args: []string{"--yolo"}})
			return err
		}, "agent.start", func(t *testing.T, p map[string]any) {
			mustEq(t, p["name"], "rev")
			mustEq(t, p["kind"], "codex")
			mustEq(t, p["pane_id"], "w1:p1")
			if a := p["args"].([]any); len(a) != 1 || a[0] != "--yolo" {
				t.Fatalf("args not passed: %v", a)
			}
		}},
		{"agent.wait", func(c *Client) error {
			_, err := c.AgentWait(ctx, "rev", []AgentStatus{StatusBlocked}, 500)
			return err
		}, "agent.wait", func(t *testing.T, p map[string]any) {
			until := p["until"].([]any)
			mustEq(t, until[0], "blocked")
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := dispatchServer(t)
			c := newTestClient(t, s)
			if err := tc.call(c); err != nil {
				t.Fatalf("call failed: %v", err)
			}
			req := s.lastRequest()
			if req["method"] != tc.method {
				t.Fatalf("method = %v want %v", req["method"], tc.method)
			}
			params, _ := req["params"].(map[string]any)
			if params == nil {
				t.Fatalf("no params recorded")
			}
			tc.check(t, params)
		})
	}
}

func TestPaneMoveReturnsReplacementID(t *testing.T) {
	t.Parallel()
	s := dispatchServer(t)
	c := newTestClient(t, s)
	res, err := c.PaneMove(context.Background(), "w1:p3", MoveToNewWorkspace("dest", "tab"), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewPaneID() != "w2:p1" {
		t.Fatalf("NewPaneID = %q want w2:p1", res.NewPaneID())
	}
	if res.MoveResult.PreviousPaneID != "w1:p3" {
		t.Fatalf("PreviousPaneID = %q want w1:p3", res.MoveResult.PreviousPaneID)
	}
}

func TestWorkspaceCreateReturnsIDs(t *testing.T) {
	t.Parallel()
	s := dispatchServer(t)
	c := newTestClient(t, s)
	res, err := c.WorkspaceCreate(context.Background(), WorkspaceCreateOptions{Label: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Workspace.WorkspaceID != "w9" || res.Tab.TabID != "w9:t1" || res.RootPane.PaneID != "w9:p1" {
		t.Fatalf("create ids: %+v", res)
	}
}

func TestMutationValidationRejectsBadInput(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, dispatchServer(t))
	ctx := context.Background()
	cases := map[string]error{}
	_, e1 := c.PaneSplit(ctx, PaneSplitOptions{TargetPaneID: "", Direction: SplitRight})
	cases["split without target"] = e1
	_, e2 := c.PaneSplit(ctx, PaneSplitOptions{TargetPaneID: "w1:p1", Direction: "sideways"})
	cases["split bad direction"] = e2
	_, e3 := c.AgentStart(ctx, StartOptions{Name: "BadName", Kind: "codex", PaneID: "w1:p1"})
	cases["start bad name"] = e3
	e4 := c.AgentSendKeys(ctx, "rev", []string{"ctrl+\x1b"})
	cases["send-keys bad key"] = e4
	_, e5 := c.PaneMove(ctx, "w1:p1", MoveToTab("", SplitRight, "", 0), false)
	cases["move to tab without id"] = e5
	for name, err := range cases {
		if !IsCode(err, "invalid_params") {
			t.Errorf("%s: want invalid_params, got %v", name, err)
		}
	}
	// A rejected mutation must not have reached the server.
	if s := dispatchServer(t); s.requestCount() != 0 {
		t.Fatal("validation should short-circuit before dialing")
	}
}

func mustEq(t *testing.T, got, want any) {
	t.Helper()
	if got != want {
		t.Fatalf("got %#v (%T) want %#v (%T)", got, got, want, want)
	}
}
